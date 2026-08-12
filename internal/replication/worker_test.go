package replication

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/objectstore"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

type failingDestination struct {
	objectstore.Store
	failOnPut int
	puts      int
}

type blockingParkReplicationStore struct {
	*repository.MemoryStore
	parkStarted  chan struct{}
	continuePark chan struct{}
}

type replicationMetrics struct {
	events   []string
	inFlight []int64
}

func (m *replicationMetrics) RecordBackgroundOperation(kind string, format repository.Format, outcome string) {
	m.events = append(m.events, kind+":"+string(format)+":"+outcome)
}

func (m *replicationMetrics) AddBackgroundOperationInFlight(kind string, format repository.Format, delta int64) {
	if kind == "replication" && format == repository.FormatRaw {
		m.inFlight = append(m.inFlight, delta)
	}
}

func (s *blockingParkReplicationStore) ParkReplicationPlanWithLease(ctx context.Context, id, message, leaseToken string) error {
	close(s.parkStarted)
	select {
	case <-s.continuePark:
	case <-ctx.Done():
		return ctx.Err()
	}
	return s.MemoryStore.ParkReplicationPlanWithLease(ctx, id, message, leaseToken)
}

func (s *failingDestination) Put(ctx context.Context, key string, value []byte) error {
	s.puts++
	if s.failOnPut == s.puts {
		return errors.New("injected object-store failure")
	}
	return s.Store.Put(ctx, key, value)
}

func TestWorkerResumesFromCheckpointAndVerifiesSHA256(t *testing.T) {
	ctx := context.Background()
	source := objectstore.NewMemoryStore()
	destination := &failingDestination{Store: objectstore.NewMemoryStore()}
	body := []byte("checkpointed replication body")
	sum := sha256.Sum256(body)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	if err := source.Put(ctx, "objects/source-widget", body); err != nil {
		t.Fatal(err)
	}
	store := repository.NewMemoryStore()
	plan := repository.ReplicationPlan{ID: "plan", SourceRepositoryID: "source", TargetRepositoryID: "target", Format: repository.FormatRaw, Coordinate: "releases/widget.bin", Digest: digest, IdempotencyKey: "key"}
	if _, _, err := store.CreateReplicationPlan(ctx, plan, []repository.ReplicationCheckpoint{{SourceObjectKey: "objects/source-widget", ObjectKey: "objects/widget", Digest: digest, Size: int64(len(body))}}); err != nil {
		t.Fatal(err)
	}
	locked, released := make([]string, 0), make([]string, 0)
	lockObject := func(_ context.Context, key string) (func(), error) {
		locked = append(locked, key)
		return func() { released = append(released, key) }, nil
	}
	// The second chunk fails after the first offset has been durably saved.
	destination.failOnPut = 2
	worker := Worker{Store: store, Source: source, Destination: destination, ChunkBytes: 3, LockObject: lockObject}
	if err := worker.Run(ctx, 1); err != nil {
		t.Fatal(err)
	}
	checks, err := store.ListReplicationCheckpoints(ctx, plan.ID)
	if err != nil || len(checks) != 1 || checks[0].State != "failed" || checks[0].ByteOffset != 3 || checks[0].Attempts != 1 {
		t.Fatalf("failed checkpoint=%#v err=%v", checks, err)
	}
	if err = worker.Run(ctx, 1); err != nil {
		t.Fatal(err)
	}
	checks, err = store.ListReplicationCheckpoints(ctx, plan.ID)
	if err != nil || checks[0].State != "verified" || checks[0].ByteOffset != int64(len(body)) || checks[0].VerifiedAt.IsZero() {
		t.Fatalf("verified checkpoint=%#v err=%v", checks, err)
	}
	got, err := destination.Get(ctx, "objects/widget")
	if err != nil || string(got) != string(body) {
		t.Fatalf("destination=%q err=%v", got, err)
	}
	if len(locked) != 4 || len(released) != 4 || locked[0] != "objects/source-widget" || locked[1] != "objects/widget" {
		t.Fatalf("object coordination locked=%v released=%v", locked, released)
	}
}

func TestWorkerPublishesVerifiedCheckpointAndRecordsMetrics(t *testing.T) {
	ctx := context.Background()
	source := objectstore.NewMemoryStore()
	destination := objectstore.NewMemoryStore()
	body := []byte("verified replication publication")
	digest := sha256Digest(body)
	if err := source.Put(ctx, "source/widget", body); err != nil {
		t.Fatal(err)
	}
	store := repository.NewMemoryStore()
	plan := repository.ReplicationPlan{
		ID: "publish", SourceRepositoryID: "source", TargetRepositoryID: "target",
		Format: repository.FormatRaw, Coordinate: "releases/widget.bin", Digest: digest,
		IdempotencyKey: "publish",
	}
	if _, _, err := store.CreateReplicationPlan(ctx, plan, []repository.ReplicationCheckpoint{{
		SourceObjectKey: "source/widget", ObjectKey: "target/widget", Digest: digest, Size: int64(len(body)),
	}}); err != nil {
		t.Fatal(err)
	}
	metrics := &replicationMetrics{}
	published := false
	worker := Worker{
		Store: store, Source: source, Destination: destination, Format: repository.FormatRaw, Metrics: metrics,
		Publish: func(_ context.Context, claimed repository.ReplicationPlan, checkpoints []repository.ReplicationCheckpoint) error {
			published = true
			if claimed.ID != plan.ID || len(checkpoints) != 1 || checkpoints[0].State != "verified" {
				t.Fatalf("publish plan=%#v checkpoints=%#v", claimed, checkpoints)
			}
			return nil
		},
	}
	if err := worker.Run(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if !published {
		t.Fatal("verified replication did not publish metadata")
	}
	completed, err := store.GetReplicationPlan(ctx, plan.TargetRepositoryID, plan.ID)
	if err != nil || completed.State != "completed" {
		t.Fatalf("completed plan=%#v err=%v", completed, err)
	}
	got, err := destination.Get(ctx, "target/widget")
	if err != nil || string(got) != string(body) {
		t.Fatalf("destination=%q err=%v", got, err)
	}
	if strings.Join(metrics.events, ",") != "replication:raw:started,replication:raw:completed" {
		t.Fatalf("metric events=%v", metrics.events)
	}
	if len(metrics.inFlight) != 2 || metrics.inFlight[0] != 1 || metrics.inFlight[1] != -1 {
		t.Fatalf("in-flight metrics=%v", metrics.inFlight)
	}
}

func TestWorkerParksWhenAggregateSnapshotChangesBeforePublication(t *testing.T) {
	ctx := context.Background()
	source := objectstore.NewMemoryStore()
	body := []byte("original aggregate snapshot")
	digest := sha256Digest(body)
	if err := source.Put(ctx, "source/project", body); err != nil {
		t.Fatal(err)
	}
	store := repository.NewMemoryStore()
	plan := repository.ReplicationPlan{
		ID: "snapshot-changed", SourceRepositoryID: "source", TargetRepositoryID: "target",
		Format: repository.FormatPyPI, Coordinate: "widget@1.0.0", Digest: digest,
		IdempotencyKey: "snapshot-changed", MaxAttempts: 3,
	}
	checkpoints := []repository.ReplicationCheckpoint{{
		SourceObjectKey: "source/project", ObjectKey: "target/project", Digest: digest, Size: int64(len(body)),
	}}
	if _, _, err := store.CreateReplicationPlan(ctx, plan, checkpoints); err != nil {
		t.Fatal(err)
	}
	published := false
	worker := Worker{
		Store: store, Source: source, Destination: objectstore.NewMemoryStore(), Format: repository.FormatPyPI,
		AdmissionSnapshot: func(_ context.Context, claimed repository.ReplicationPlan, verified []repository.ReplicationCheckpoint) ([]string, bool, error) {
			if claimed.ID != plan.ID || len(verified) != 1 || verified[0].State != "verified" {
				t.Fatalf("snapshot plan=%#v checkpoints=%#v", claimed, verified)
			}
			return []string{digest}, false, nil
		},
		Publish: func(context.Context, repository.ReplicationPlan, []repository.ReplicationCheckpoint) error {
			published = true
			return nil
		},
	}
	if err := worker.Run(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if published {
		t.Fatal("changed aggregate snapshot reached publication")
	}
	parked, err := store.GetReplicationPlan(ctx, plan.TargetRepositoryID, plan.ID)
	if err != nil || parked.State != "failed" || parked.LastError != repository.ReplicationSnapshotChangedReason || parked.Attempts != 0 || !parked.NextAttemptAt.IsZero() {
		t.Fatalf("parked plan=%#v err=%v", parked, err)
	}
}

func TestWorkerRejectsInvalidDigest(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	plan := repository.ReplicationPlan{ID: "invalid", SourceRepositoryID: "source", TargetRepositoryID: "target", Format: repository.FormatRaw, Coordinate: "releases/invalid.bin", Digest: "sha256:" + strings.Repeat("a", 64), IdempotencyKey: "invalid"}
	if _, _, err := store.CreateReplicationPlan(ctx, plan, []repository.ReplicationCheckpoint{{ObjectKey: "object", Digest: "sha256:" + strings.Repeat("x", 64), Size: 1}}); err != nil {
		t.Fatal(err)
	}
	if err := (Worker{Store: store, Source: objectstore.NewMemoryStore(), Destination: objectstore.NewMemoryStore()}).Run(ctx, 1); err != nil {
		t.Fatal(err)
	}
	checks, err := store.ListReplicationCheckpoints(ctx, plan.ID)
	if err != nil || checks[0].State != "failed" || checks[0].Attempts != 1 {
		t.Fatalf("checks=%#v err=%v", checks, err)
	}
}

func TestWorkerFailsPlanWhenObjectCoordinationFails(t *testing.T) {
	ctx := context.Background()
	source := objectstore.NewMemoryStore()
	body := []byte("replication")
	sum := sha256.Sum256(body)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	if err := source.Put(ctx, "source", body); err != nil {
		t.Fatal(err)
	}
	store := repository.NewMemoryStore()
	plan := repository.ReplicationPlan{ID: "lock-failure", SourceRepositoryID: "source-repo", TargetRepositoryID: "target-repo", Format: repository.FormatNPM, Coordinate: "widget@1.0.0", Digest: digest, IdempotencyKey: "lock-failure"}
	if _, _, err := store.CreateReplicationPlan(ctx, plan, []repository.ReplicationCheckpoint{{SourceObjectKey: "source", ObjectKey: "target", Digest: digest, Size: int64(len(body))}}); err != nil {
		t.Fatal(err)
	}
	worker := Worker{
		Store: store, Source: source, Destination: objectstore.NewMemoryStore(), Format: repository.FormatNPM,
		LockObject: func(context.Context, string) (func(), error) {
			return nil, errors.New("coordination unavailable")
		},
	}
	if err := worker.Run(ctx, 1); err != nil {
		t.Fatal(err)
	}
	failed, err := store.GetReplicationPlan(ctx, plan.TargetRepositoryID, plan.ID)
	if err != nil || failed.State != "failed" || failed.LastError != "replication object coordination failed" {
		t.Fatalf("failed plan=%#v err=%v", failed, err)
	}
	if _, err = worker.Destination.Get(ctx, "target"); !errors.Is(err, objectstore.ErrNotFound) {
		t.Fatalf("destination changed before lock: %v", err)
	}
}

func TestWorkerFailsClosedForLegacyPlanWithoutArtifactIdentity(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	digest := "sha256:" + strings.Repeat("a", 64)
	plan := repository.ReplicationPlan{
		ID: "legacy-empty-identity", SourceRepositoryID: "source", TargetRepositoryID: "target",
		Format: repository.FormatRaw, IdempotencyKey: "legacy-empty-identity",
	}
	if _, _, err := store.CreateReplicationPlan(ctx, plan, []repository.ReplicationCheckpoint{{ObjectKey: "objects/widget", Digest: digest, Size: 1}}); err != nil {
		t.Fatal(err)
	}
	publishCalled := false
	worker := Worker{
		Store: store, Source: objectstore.NewMemoryStore(), Destination: objectstore.NewMemoryStore(),
		Publish: func(context.Context, repository.ReplicationPlan, []repository.ReplicationCheckpoint) error {
			publishCalled = true
			return nil
		},
	}
	if err := worker.Run(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if publishCalled {
		t.Fatal("legacy plan without artifact identity reached publication")
	}
	failed, err := store.GetReplicationPlan(ctx, plan.TargetRepositoryID, plan.ID)
	if err != nil || failed.State != "failed" || failed.LastError != "replication artifact identity is unavailable" {
		t.Fatalf("legacy plan=%#v err=%v", failed, err)
	}
}

func TestWorkerParksBeforeReleaseTransitionAndReplayCanRequeue(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	base := repository.NewMemoryStore()
	store := &blockingParkReplicationStore{
		MemoryStore:  base,
		parkStarted:  make(chan struct{}),
		continuePark: make(chan struct{}),
	}
	digest := "sha256:" + strings.Repeat("d", 64)
	coordinate := "releases/quarantined.bin"
	plan := repository.ReplicationPlan{
		ID: "park-release-replay", SourceRepositoryID: "source", TargetRepositoryID: "target",
		Format: repository.FormatRaw, Coordinate: coordinate, Digest: digest,
		IdempotencyKey: "park-release-replay", MaxAttempts: 2,
	}
	checkpoints := []repository.ReplicationCheckpoint{{ObjectKey: "native/raw/quarantined", Digest: digest, Size: 12}}
	if _, replayed, err := store.CreateReplicationPlan(ctx, plan, checkpoints); err != nil || replayed {
		t.Fatalf("create replayed=%t err=%v", replayed, err)
	}
	quarantined, err := store.ReplaceArtifactQuarantine(ctx, repository.ArtifactQuarantine{
		RepositoryID: plan.SourceRepositoryID,
		Format:       plan.Format,
		Coordinate:   plan.Coordinate,
		Digest:       plan.Digest,
		State:        repository.ArtifactQuarantineStateQuarantined,
		Reason:       "hold final publication",
		UpdatedBy:    "user:security-admin",
	}, "0")
	if err != nil {
		t.Fatal(err)
	}

	workerDone := make(chan error, 1)
	go func() {
		workerDone <- (Worker{Store: store, Source: objectstore.NewMemoryStore(), Destination: objectstore.NewMemoryStore()}).Run(ctx, 1)
	}()
	select {
	case <-store.parkStarted:
	case <-ctx.Done():
		t.Fatal("worker did not reach quarantine Park")
	}

	type replayResult struct {
		plan     repository.ReplicationPlan
		replayed bool
		err      error
	}
	transitionAttempted := make(chan struct{})
	transitionAcquired := make(chan struct{})
	replayDone := make(chan replayResult, 1)
	go func() {
		close(transitionAttempted)
		releaseTransition, lockErr := repository.LockArtifactQuarantineTransition(ctx, store, plan.SourceRepositoryID, plan.Format, plan.Coordinate, plan.Digest)
		if lockErr != nil {
			replayDone <- replayResult{err: lockErr}
			return
		}
		close(transitionAcquired)
		released := quarantined
		released.State = repository.ArtifactQuarantineStateReleased
		released.Reason = "security review complete"
		released.UpdatedBy = "user:security-admin"
		_, replaceErr := store.ReplaceArtifactQuarantine(ctx, released, quarantined.Version)
		releaseTransition()
		if replaceErr != nil {
			replayDone <- replayResult{err: replaceErr}
			return
		}
		requeued, replayed, replayErr := store.CreateReplicationPlan(ctx, plan, checkpoints)
		replayDone <- replayResult{plan: requeued, replayed: replayed, err: replayErr}
	}()
	<-transitionAttempted
	transitionRanBeforePark := false
	select {
	case <-transitionAcquired:
		transitionRanBeforePark = true
	case <-time.After(100 * time.Millisecond):
	}
	close(store.continuePark)
	select {
	case runErr := <-workerDone:
		if runErr != nil {
			t.Fatal(runErr)
		}
	case <-ctx.Done():
		t.Fatal("worker did not finish after Park was released")
	}
	var replay replayResult
	select {
	case replay = <-replayDone:
	case <-ctx.Done():
		t.Fatal("release transition and replay did not finish")
	}
	if transitionRanBeforePark {
		t.Fatal("release transition acquired the admission lock before Park completed")
	}
	if replay.err != nil || !replay.replayed || replay.plan.ID != plan.ID || replay.plan.State != "pending" || replay.plan.LastError != "" || replay.plan.Attempts != 0 || replay.plan.NextAttemptAt.IsZero() {
		t.Fatalf("release replay=%#v replayed=%t err=%v", replay.plan, replay.replayed, replay.err)
	}
	claimed, err := store.ClaimReplicationPlansByFormat(ctx, repository.FormatRaw, 1)
	if err != nil || len(claimed) != 1 || claimed[0].ID != plan.ID {
		t.Fatalf("requeued claim=%#v err=%v", claimed, err)
	}
}
