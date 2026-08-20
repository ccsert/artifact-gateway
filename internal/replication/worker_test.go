package replication

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"strings"
	"sync/atomic"
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

type heartbeatReplicationStore struct {
	*repository.MemoryStore
	renewals atomic.Int32
	expireAt int32
}

type cancelAwareHeartbeatStore struct {
	*repository.MemoryStore
	renewals atomic.Int32
	entered  chan struct{}
}

func (s *heartbeatReplicationStore) RenewReplicationPlanLease(ctx context.Context, id, leaseToken string) error {
	call := s.renewals.Add(1)
	if s.expireAt > 0 && call == s.expireAt {
		_, _ = s.RecoverExpiredReplicationPlans(ctx, time.Now().UTC().Add(11*time.Minute))
		return repository.ErrNotFound
	}
	return s.MemoryStore.RenewReplicationPlanLease(ctx, id, leaseToken)
}

func (s *cancelAwareHeartbeatStore) RenewReplicationPlanLease(ctx context.Context, id, leaseToken string) error {
	if s.renewals.Add(1) == 1 {
		return s.MemoryStore.RenewReplicationPlanLease(ctx, id, leaseToken)
	}
	close(s.entered)
	<-ctx.Done()
	return ctx.Err()
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

func (s *failingDestination) PutReader(ctx context.Context, key string, value io.Reader, size int64) error {
	s.puts++
	if s.failOnPut == s.puts {
		return errors.New("injected object-store failure")
	}
	return s.Store.PutReader(ctx, key, value, size)
}

func TestWorkerRetriesStreamingCheckpointAndVerifiesSHA256(t *testing.T) {
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
	destination.failOnPut = 1
	worker := Worker{Store: store, Source: source, Destination: destination, ChunkBytes: 3, LockObject: lockObject}
	if err := worker.Run(ctx, 1); err != nil {
		t.Fatal(err)
	}
	checks, err := store.ListReplicationCheckpoints(ctx, plan.ID)
	if err != nil || len(checks) != 1 || checks[0].State != "failed" || checks[0].ByteOffset != 0 || checks[0].Attempts != 1 {
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

type failCheckpointUpdateOnceStore struct {
	*repository.MemoryStore
	fail bool
}

func (s *failCheckpointUpdateOnceStore) UpdateReplicationCheckpointWithLease(ctx context.Context, checkpoint repository.ReplicationCheckpoint, leaseToken string) error {
	if s.fail {
		s.fail = false
		return errors.New("injected checkpoint persistence failure")
	}
	return s.MemoryStore.UpdateReplicationCheckpointWithLease(ctx, checkpoint, leaseToken)
}

func TestWorkerRecoversWhenObjectCommitPrecedesCheckpointUpdate(t *testing.T) {
	ctx := context.Background()
	source := objectstore.NewMemoryStore()
	destination := objectstore.NewMemoryStore()
	body := []byte("object committed before checkpoint update")
	digest := sha256Digest(body)
	if err := source.Put(ctx, "source/crash-window", body); err != nil {
		t.Fatal(err)
	}
	base := repository.NewMemoryStore()
	store := &failCheckpointUpdateOnceStore{MemoryStore: base, fail: true}
	plan := repository.ReplicationPlan{
		ID: "object-before-checkpoint", SourceRepositoryID: "source", TargetRepositoryID: "target",
		Format: repository.FormatRaw, Coordinate: "releases/crash-window.bin", Digest: digest,
		IdempotencyKey: "object-before-checkpoint",
	}
	if _, _, err := store.CreateReplicationPlan(ctx, plan, []repository.ReplicationCheckpoint{{
		SourceObjectKey: "source/crash-window", ObjectKey: "target/crash-window", Digest: digest, Size: int64(len(body)),
	}}); err != nil {
		t.Fatal(err)
	}
	worker := Worker{Store: store, Source: source, Destination: destination}
	if err := worker.Run(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if committed, err := destination.Get(ctx, "target/crash-window"); err != nil || string(committed) != string(body) {
		t.Fatalf("committed destination=%q err=%v", committed, err)
	}
	checks, err := store.ListReplicationCheckpoints(ctx, plan.ID)
	if err != nil || len(checks) != 1 || checks[0].State != "failed" || checks[0].ByteOffset != 0 {
		t.Fatalf("checkpoint after persistence failure=%#v err=%v", checks, err)
	}
	if err = worker.Run(ctx, 1); err != nil {
		t.Fatal(err)
	}
	checks, err = store.ListReplicationCheckpoints(ctx, plan.ID)
	if err != nil || checks[0].State != "verified" || checks[0].ByteOffset != int64(len(body)) {
		t.Fatalf("recovered checkpoint=%#v err=%v", checks, err)
	}
}

type streamingDestinationProbe struct {
	objectstore.Store
	getCalls       int
	putCalls       int
	putReaderCalls int
}

func (s *streamingDestinationProbe) Get(ctx context.Context, key string) ([]byte, error) {
	s.getCalls++
	return s.Store.Get(ctx, key)
}

func (s *streamingDestinationProbe) Put(ctx context.Context, key string, value []byte) error {
	s.putCalls++
	return s.Store.Put(ctx, key, value)
}

func (s *streamingDestinationProbe) PutReader(ctx context.Context, key string, value io.Reader, size int64) error {
	s.putReaderCalls++
	return s.Store.PutReader(ctx, key, value, size)
}

func TestWorkerStreamsLargeObjectWithoutPrefixRewrites(t *testing.T) {
	ctx := context.Background()
	source := objectstore.NewMemoryStore()
	destination := &streamingDestinationProbe{Store: objectstore.NewMemoryStore()}
	body := bytes.Repeat([]byte("0123456789abcdef"), (17<<20)/16)
	digest := sha256Digest(body)
	if err := source.Put(ctx, "source/large", body); err != nil {
		t.Fatal(err)
	}
	store := repository.NewMemoryStore()
	plan := repository.ReplicationPlan{
		ID: "large-stream", SourceRepositoryID: "source", TargetRepositoryID: "target", Format: repository.FormatGo,
		Coordinate: "example.com/team/large@v1.0.0", Digest: digest, IdempotencyKey: "large-stream",
	}
	if _, _, err := store.CreateReplicationPlan(ctx, plan, []repository.ReplicationCheckpoint{{
		SourceObjectKey: "source/large", ObjectKey: "target/large", Digest: digest, Size: int64(len(body)),
	}}); err != nil {
		t.Fatal(err)
	}
	if err := (Worker{Store: store, Source: source, Destination: destination, ChunkBytes: 1 << 20}).Run(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if destination.putReaderCalls != 1 || destination.getCalls != 0 || destination.putCalls != 0 {
		t.Fatalf("destination operations putReader=%d get=%d put=%d", destination.putReaderCalls, destination.getCalls, destination.putCalls)
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

func TestWorkerHeartbeatCancelsObjectLockWaitAfterLeaseLoss(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	base := repository.NewMemoryStore()
	store := &heartbeatReplicationStore{MemoryStore: base, expireAt: 2}
	body := []byte("heartbeat fenced object lock")
	digest := sha256Digest(body)
	plan := repository.ReplicationPlan{
		ID: "heartbeat-lock", SourceRepositoryID: "source", TargetRepositoryID: "target",
		Format: repository.FormatRaw, Coordinate: "releases/heartbeat.bin", Digest: digest, IdempotencyKey: "heartbeat-lock",
	}
	if _, _, err := store.CreateReplicationPlan(ctx, plan, []repository.ReplicationCheckpoint{{
		SourceObjectKey: "source/heartbeat", ObjectKey: "target/heartbeat", Digest: digest, Size: int64(len(body)),
	}}); err != nil {
		t.Fatal(err)
	}
	worker := Worker{
		Store: store, Source: objectstore.NewMemoryStore(), Destination: objectstore.NewMemoryStore(),
		LeaseHeartbeatInterval: 5 * time.Millisecond,
		LockObject: func(lockCtx context.Context, _ string) (func(), error) {
			<-lockCtx.Done()
			return nil, lockCtx.Err()
		},
	}
	if err := worker.Run(ctx, 1); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("worker after heartbeat lease loss err=%v", err)
	}
	if store.renewals.Load() < 2 {
		t.Fatalf("lease renewals=%d", store.renewals.Load())
	}
	persisted, err := store.GetReplicationPlan(ctx, plan.TargetRepositoryID, plan.ID)
	if err != nil || persisted.State != "failed" || persisted.LastError != "replication worker lease expired" {
		t.Fatalf("recovered plan=%#v err=%v", persisted, err)
	}
}

func TestWorkerHeartbeatTreatsCancellationDuringRenewalAsCleanStop(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	store := &cancelAwareHeartbeatStore{MemoryStore: repository.NewMemoryStore(), entered: make(chan struct{})}
	plan := repository.ReplicationPlan{
		ID: "heartbeat-cancel-renewal", SourceRepositoryID: "source", TargetRepositoryID: "target",
		Format: repository.FormatRaw, Coordinate: "releases/cancel.bin", Digest: "sha256:" + strings.Repeat("c", 64), IdempotencyKey: "heartbeat-cancel-renewal",
	}
	if _, _, err := store.CreateReplicationPlan(ctx, plan, []repository.ReplicationCheckpoint{{
		ObjectKey: "target/cancel", Digest: plan.Digest, Size: 1,
	}}); err != nil {
		t.Fatal(err)
	}
	claimed, err := store.ClaimReplicationPlans(ctx, 1)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claimed=%#v err=%v", claimed, err)
	}
	_, heartbeat, err := (Worker{Store: store, LeaseHeartbeatInterval: time.Millisecond}).startLeaseHeartbeat(ctx, plan.ID, claimed[0].LeaseToken)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-store.entered:
	case <-ctx.Done():
		t.Fatal("heartbeat did not enter renewal")
	}
	if err = heartbeat.stop(); err != nil {
		t.Fatalf("heartbeat stop after cancellation=%v", err)
	}
}

func TestWorkerLeaseFenceCoversPublishThroughCompletion(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	store := &heartbeatReplicationStore{MemoryStore: repository.NewMemoryStore()}
	source := objectstore.NewMemoryStore()
	destination := objectstore.NewMemoryStore()
	body := []byte("lease fenced metadata publication")
	digest := sha256Digest(body)
	if err := source.Put(ctx, "source/fenced", body); err != nil {
		t.Fatal(err)
	}
	plan := repository.ReplicationPlan{
		ID: "publish-fence", SourceRepositoryID: "source", TargetRepositoryID: "target",
		Format: repository.FormatRaw, Coordinate: "releases/fenced.bin", Digest: digest, IdempotencyKey: "publish-fence",
	}
	if _, _, err := store.CreateReplicationPlan(ctx, plan, []repository.ReplicationCheckpoint{{
		SourceObjectKey: "source/fenced", ObjectKey: "target/fenced", Digest: digest, Size: int64(len(body)),
	}}); err != nil {
		t.Fatal(err)
	}
	publishEntered := make(chan struct{})
	releasePublish := make(chan struct{})
	workerDone := make(chan error, 1)
	go func() {
		workerDone <- (Worker{
			Store: store, Source: source, Destination: destination, Format: repository.FormatRaw,
			LeaseHeartbeatInterval: 5 * time.Millisecond,
			Publish: func(publishCtx context.Context, _ repository.ReplicationPlan, _ []repository.ReplicationCheckpoint) error {
				close(publishEntered)
				select {
				case <-releasePublish:
					return nil
				case <-publishCtx.Done():
					return publishCtx.Err()
				}
			},
		}).Run(ctx, 1)
	}()
	select {
	case <-publishEntered:
	case <-ctx.Done():
		t.Fatal("worker did not enter metadata publication")
	}
	type recoveryResult struct {
		count int
		err   error
	}
	recoveryDone := make(chan recoveryResult, 1)
	go func() {
		count, err := store.RecoverExpiredReplicationPlans(ctx, time.Now().UTC().Add(11*time.Minute))
		recoveryDone <- recoveryResult{count: count, err: err}
	}()
	select {
	case result := <-recoveryDone:
		t.Fatalf("lease recovery crossed publication fence: %#v", result)
	case <-time.After(50 * time.Millisecond):
	}
	close(releasePublish)
	select {
	case err := <-workerDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("worker did not complete publication")
	}
	select {
	case result := <-recoveryDone:
		if result.err != nil || result.count != 0 {
			t.Fatalf("recovery after completed publication=%#v", result)
		}
	case <-ctx.Done():
		t.Fatal("lease recovery remained blocked after completion")
	}
	if store.renewals.Load() < 2 {
		t.Fatalf("publication heartbeat renewals=%d", store.renewals.Load())
	}
	persisted, err := store.GetReplicationPlan(ctx, plan.TargetRepositoryID, plan.ID)
	if err != nil || persisted.State != "completed" {
		t.Fatalf("completed plan=%#v err=%v", persisted, err)
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
