//go:build integration

package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	rawprotocol "github.com/artifact-gateway/artifact-gateway/internal/protocol/raw"
	"github.com/artifact-gateway/artifact-gateway/internal/replication"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

type failOnceReplicationCheckpointPostgresStore struct {
	*repository.PostgresStore
	fail bool
}

type blockingReplicationObjectPostgresStore struct {
	*repository.PostgresStore
	once    sync.Once
	entered chan struct{}
	release chan struct{}
}

func (s *blockingReplicationObjectPostgresStore) LockArtifactObjectKeys(ctx context.Context, format repository.Format, objectKeys []string) (context.Context, func(), error) {
	lockedCtx, release, err := s.PostgresStore.LockArtifactObjectKeys(ctx, format, objectKeys)
	if err != nil {
		return ctx, nil, err
	}
	var waitErr error
	s.once.Do(func() {
		close(s.entered)
		select {
		case <-s.release:
		case <-ctx.Done():
			waitErr = ctx.Err()
		}
	})
	if waitErr != nil {
		release()
		return ctx, nil, waitErr
	}
	return lockedCtx, release, nil
}

func (s *failOnceReplicationCheckpointPostgresStore) UpdateReplicationCheckpointWithLease(ctx context.Context, checkpoint repository.ReplicationCheckpoint, leaseToken string) error {
	if s.fail && checkpoint.State == "verified" {
		s.fail = false
		return errors.New("database unavailable after object commit")
	}
	return s.PostgresStore.UpdateReplicationCheckpointWithLease(ctx, checkpoint, leaseToken)
}

func TestPostgresReplicationPlansPersistCheckpointsAndRetry(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is required for PostgreSQL integration tests")
	}
	ctx := context.Background()
	store, err := repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	source, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "replication-source-" + uuid.NewString(), Format: repository.FormatRaw})
	if err != nil {
		t.Fatal(err)
	}
	target, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "replication-target-" + uuid.NewString(), Format: repository.FormatRaw})
	if err != nil {
		t.Fatal(err)
	}
	checks := []repository.ReplicationCheckpoint{
		{ObjectKey: "native/raw/a", Digest: "sha256:" + strings.Repeat("a", 64), Size: 3},
		{ObjectKey: "native/raw/b", Digest: "sha256:" + strings.Repeat("b", 64), Size: 5},
	}
	plan := repository.ReplicationPlan{ID: uuid.NewString(), SourceRepositoryID: source.ID, TargetRepositoryID: target.ID, Format: repository.FormatRaw, Coordinate: "releases/a.bin", Digest: checks[0].Digest, IdempotencyKey: "replication-" + uuid.NewString()}
	created, replayed, err := store.CreateReplicationPlan(ctx, plan, checks)
	if err != nil || replayed || created.State != "pending" {
		t.Fatalf("created=%#v replayed=%t err=%v", created, replayed, err)
	}
	if replay, replayed, err := store.CreateReplicationPlan(ctx, plan, checks); err != nil || !replayed || replay.ID != plan.ID {
		t.Fatalf("replay=%#v replayed=%t err=%v", replay, replayed, err)
	}
	if _, _, err := store.CreateReplicationPlan(ctx, plan, []repository.ReplicationCheckpoint{{ObjectKey: "native/raw/a", Digest: "sha256:" + strings.Repeat("c", 64), Size: 3}}); !errors.Is(err, repository.ErrIdempotencyConflict) {
		t.Fatalf("conflicting replay err=%v", err)
	}
	claimed, err := store.ClaimReplicationPlans(ctx, 1)
	if err != nil || len(claimed) != 1 || claimed[0].ID != plan.ID || claimed[0].State != "running" {
		t.Fatalf("claimed=%#v err=%v", claimed, err)
	}
	if err = store.UpdateReplicationCheckpointWithLease(ctx, repository.ReplicationCheckpoint{PlanID: plan.ID, ObjectKey: checks[0].ObjectKey, Digest: checks[0].Digest, Size: checks[0].Size, ByteOffset: checks[0].Size, State: "verified", Attempts: 1, VerifiedAt: time.Now().UTC()}, claimed[0].LeaseToken); err != nil {
		t.Fatal(err)
	}
	if err = store.FailReplicationPlanWithLease(ctx, plan.ID, "temporary object-store failure", claimed[0].LeaseToken); err != nil {
		t.Fatal(err)
	}
	retried, err := store.ClaimReplicationPlans(ctx, 1)
	if err != nil || len(retried) != 1 || retried[0].ID != plan.ID || retried[0].LastError != "" {
		t.Fatalf("retried=%#v err=%v", retried, err)
	}
	persisted, err := store.ListReplicationCheckpoints(ctx, plan.ID)
	if err != nil || len(persisted) != 2 || persisted[0].State != "verified" || persisted[0].ByteOffset != checks[0].Size || persisted[0].VerifiedAt.IsZero() {
		t.Fatalf("checkpoints=%#v err=%v", persisted, err)
	}
	if err = store.CompleteReplicationPlanWithLease(ctx, plan.ID, retried[0].LeaseToken); err != nil {
		t.Fatal(err)
	}
	plans, err := store.ListReplicationPlans(ctx, target.ID, 10)
	if err != nil || len(plans) != 1 || plans[0].ID != plan.ID || plans[0].State != "completed" {
		t.Fatalf("plans=%#v err=%v", plans, err)
	}
	if got, err := store.GetReplicationPlan(ctx, source.ID, plan.ID); err != nil || got.ID != plan.ID {
		t.Fatalf("source-scoped plan=%#v err=%v", got, err)
	}
	other, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "replication-other-" + uuid.NewString(), Format: repository.FormatRaw})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetReplicationPlan(ctx, other.ID, plan.ID); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("unrelated scoped plan err=%v", err)
	}

	exhausted := repository.ReplicationPlan{ID: uuid.NewString(), SourceRepositoryID: source.ID, TargetRepositoryID: target.ID, Format: repository.FormatRaw, Coordinate: "releases/exhausted.bin", Digest: checks[0].Digest, IdempotencyKey: "exhausted-" + uuid.NewString(), MaxAttempts: 1}
	if _, _, err = store.CreateReplicationPlan(ctx, exhausted, checks[:1]); err != nil {
		t.Fatal(err)
	}
	claimed, err = store.ClaimReplicationPlans(ctx, 1)
	if err != nil || len(claimed) != 1 || claimed[0].ID != exhausted.ID {
		t.Fatalf("exhausted initial claim=%#v err=%v", claimed, err)
	}
	if err = store.FailReplicationPlanWithLease(ctx, exhausted.ID, "final failure", claimed[0].LeaseToken); err != nil {
		t.Fatal(err)
	}
	if claimed, err = store.ClaimReplicationPlans(ctx, 1); err != nil || len(claimed) != 0 {
		t.Fatalf("exhausted retry claim=%#v err=%v", claimed, err)
	}
}

func TestPostgresRustFSReplicationStreamsLargeObjectAndRecoversCheckpointFailure(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	endpoint := os.Getenv("TEST_RUSTFS_ENDPOINT")
	accessKey := os.Getenv("TEST_RUSTFS_ACCESS_KEY")
	secretKey := os.Getenv("TEST_RUSTFS_SECRET_KEY")
	if databaseURL == "" || endpoint == "" || accessKey == "" || secretKey == "" {
		t.Skip("PostgreSQL and S3 integration environment is required")
	}
	ctx := context.Background()
	store, err := repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	storeB, err := repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer storeB.Close()
	sourceRepo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "replication-rustfs-source-" + uuid.NewString(), Format: repository.FormatRaw})
	if err != nil {
		t.Fatal(err)
	}
	targetRepo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "replication-rustfs-target-" + uuid.NewString(), Format: repository.FormatRaw})
	if err != nil {
		t.Fatal(err)
	}
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:20]
	sourceObjects, err := NewRustFSOCIObjectStore(endpoint, accessKey, secretKey, "replication-source-"+suffix)
	if err != nil {
		t.Fatal(err)
	}
	targetObjects, err := NewRustFSOCIObjectStore(endpoint, accessKey, secretKey, "replication-target-"+suffix)
	if err != nil {
		t.Fatal(err)
	}
	if err = sourceObjects.EnsureBucket(ctx); err != nil {
		t.Fatal(err)
	}
	if err = targetObjects.EnsureBucket(ctx); err != nil {
		t.Fatal(err)
	}
	body := bytes.Repeat([]byte("0123456789abcdef"), (17<<20)/16)
	sum := sha256.Sum256(body)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	key := "native/raw/sha256/" + hex.EncodeToString(sum[:])
	if err = sourceObjects.PutVerifiedReader(ctx, key, bytes.NewReader(body), int64(len(body)), digest); err != nil {
		t.Fatal(err)
	}
	plan := repository.ReplicationPlan{ID: uuid.NewString(), SourceRepositoryID: sourceRepo.ID, TargetRepositoryID: targetRepo.ID, Format: repository.FormatRaw, Coordinate: "releases/cross-bucket.bin", Digest: digest, IdempotencyKey: "rustfs-" + uuid.NewString()}
	if _, _, err = store.CreateReplicationPlan(ctx, plan, []repository.ReplicationCheckpoint{{ObjectKey: key, Digest: digest, Size: int64(len(body))}}); err != nil {
		t.Fatal(err)
	}
	failingStore := &failOnceReplicationCheckpointPostgresStore{PostgresStore: store, fail: true}
	if err = (replication.Worker{Store: failingStore, Source: sourceObjects, Destination: targetObjects, ChunkBytes: 1 << 20}).Run(ctx, 1); err != nil {
		t.Fatal(err)
	}
	checks, err := store.ListReplicationCheckpoints(ctx, plan.ID)
	if err != nil || len(checks) != 1 || checks[0].State != "failed" || checks[0].ByteOffset != 0 || checks[0].LastError != "database unavailable after object commit" {
		t.Fatalf("checkpoint after object/DB failure=%#v err=%v", checks, err)
	}
	info, err := targetObjects.Stat(ctx, key)
	if err != nil || info.Digest != digest || info.Size != int64(len(body)) {
		t.Fatalf("committed target info=%#v err=%v", info, err)
	}
	if err = (replication.Worker{Store: storeB, Source: sourceObjects, Destination: targetObjects, ChunkBytes: 1 << 20}).Run(ctx, 1); err != nil {
		t.Fatal(err)
	}
	checks, err = storeB.ListReplicationCheckpoints(ctx, plan.ID)
	if err != nil || len(checks) != 1 || checks[0].State != "verified" || checks[0].ByteOffset != int64(len(body)) || checks[0].VerifiedAt.IsZero() {
		t.Fatalf("recovered checkpoints=%#v err=%v", checks, err)
	}
	info, err = targetObjects.Stat(ctx, key)
	if err != nil || info.Digest != digest || info.Size != int64(len(body)) {
		t.Fatalf("target info=%#v err=%v", info, err)
	}
}

func TestPostgresReplicationPublicationIsFencedAfterLeaseExpiry(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is required for PostgreSQL integration tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	storeA, err := repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer storeA.Close()
	storeB, err := repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer storeB.Close()
	sourceRepo, err := storeA.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "replication-fence-source-" + uuid.NewString(), Format: repository.FormatRaw})
	if err != nil {
		t.Fatal(err)
	}
	targetRepo, err := storeA.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "replication-fence-target-" + uuid.NewString(), Format: repository.FormatRaw})
	if err != nil {
		t.Fatal(err)
	}
	body := []byte("lease-fenced replication publication")
	digest := sha256DigestForIntegration(body)
	objects := NewMemoryOCIObjectStore()
	if err = objects.Put(ctx, "source/fenced", body); err != nil {
		t.Fatal(err)
	}
	plan := repository.ReplicationPlan{
		ID: uuid.NewString(), SourceRepositoryID: sourceRepo.ID, TargetRepositoryID: targetRepo.ID,
		Format: repository.FormatRaw, Coordinate: "releases/fenced.bin", Digest: digest, IdempotencyKey: "fence-" + uuid.NewString(),
	}
	if _, _, err = storeA.CreateReplicationPlan(ctx, plan, []repository.ReplicationCheckpoint{{
		SourceObjectKey: "source/fenced", ObjectKey: "target/fenced", Digest: digest, Size: int64(len(body)),
	}}); err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{})
	release := make(chan struct{})
	published := false
	workerDone := make(chan error, 1)
	go func() {
		workerDone <- (replication.Worker{
			Store: storeA, Source: objects, Destination: objects, Format: repository.FormatRaw,
			AdmissionSnapshot: func(context.Context, repository.ReplicationPlan, []repository.ReplicationCheckpoint) ([]string, bool, error) {
				close(entered)
				select {
				case <-release:
					return []string{digest}, true, nil
				case <-ctx.Done():
					return nil, false, ctx.Err()
				}
			},
			Publish: func(context.Context, repository.ReplicationPlan, []repository.ReplicationCheckpoint) error {
				published = true
				return nil
			},
		}).Run(ctx, 1)
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("replication worker did not reach final admission barrier")
	}
	if recovered, recoverErr := storeB.RecoverExpiredReplicationPlans(ctx, time.Now().UTC().Add(11*time.Minute)); recoverErr != nil || recovered != 1 {
		t.Fatalf("recover expired replication plans=%d err=%v", recovered, recoverErr)
	}
	close(release)
	select {
	case runErr := <-workerDone:
		if !errors.Is(runErr, repository.ErrNotFound) {
			t.Fatalf("stale replication worker err=%v", runErr)
		}
	case <-ctx.Done():
		t.Fatal("stale replication worker did not finish")
	}
	if published {
		t.Fatal("expired replication worker published target metadata")
	}
	persisted, err := storeB.GetReplicationPlan(ctx, targetRepo.ID, plan.ID)
	if err != nil || persisted.State != "failed" || persisted.LastError != "replication worker lease expired" {
		t.Fatalf("expired replication plan=%#v err=%v", persisted, err)
	}
}

func TestPostgresReplicationHeartbeatsAcrossObjectLockAndFencesPublication(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is required for PostgreSQL integration tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	storeA, err := repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer storeA.Close()
	storeB, err := repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer storeB.Close()
	sourceRepo, err := storeA.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "repl-heartbeat-src-" + uuid.NewString()[:8], Format: repository.FormatRaw})
	if err != nil {
		t.Fatal(err)
	}
	targetRepo, err := storeA.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "repl-heartbeat-dst-" + uuid.NewString()[:8], Format: repository.FormatRaw})
	if err != nil {
		t.Fatal(err)
	}
	body := []byte("heartbeat and publication fence")
	digest := sha256DigestForIntegration(body)
	objects := NewMemoryOCIObjectStore()
	if err = objects.Put(ctx, "source/heartbeat", body); err != nil {
		t.Fatal(err)
	}
	plan := repository.ReplicationPlan{
		ID: uuid.NewString(), SourceRepositoryID: sourceRepo.ID, TargetRepositoryID: targetRepo.ID,
		Format: repository.FormatRaw, Coordinate: "releases/heartbeat.bin", Digest: digest, IdempotencyKey: "heartbeat-" + uuid.NewString(),
	}
	if _, _, err = storeA.CreateReplicationPlan(ctx, plan, []repository.ReplicationCheckpoint{{
		SourceObjectKey: "source/heartbeat", ObjectKey: "target/heartbeat", Digest: digest, Size: int64(len(body)),
	}}); err != nil {
		t.Fatal(err)
	}
	blockingStore := &blockingReplicationObjectPostgresStore{PostgresStore: storeA, entered: make(chan struct{}), release: make(chan struct{})}
	publishEntered := make(chan struct{})
	publishRelease := make(chan struct{})
	workerDone := make(chan error, 1)
	go func() {
		workerDone <- (replication.Worker{
			Store: blockingStore, Source: objects, Destination: objects, Format: repository.FormatRaw,
			LockObject: blockingStore.LockRawObject, LeaseHeartbeatInterval: 20 * time.Millisecond,
			Publish: func(publishCtx context.Context, _ repository.ReplicationPlan, _ []repository.ReplicationCheckpoint) error {
				close(publishEntered)
				select {
				case <-publishRelease:
					return nil
				case <-publishCtx.Done():
					return publishCtx.Err()
				}
			},
		}).Run(ctx, 1)
	}()
	select {
	case <-blockingStore.entered:
	case <-ctx.Done():
		t.Fatal("worker did not acquire the object lock")
	}
	before, err := storeB.GetReplicationPlan(ctx, targetRepo.ID, plan.ID)
	if err != nil || before.State != "running" {
		t.Fatalf("running plan before heartbeat=%#v err=%v", before, err)
	}
	time.Sleep(100 * time.Millisecond)
	after, err := storeB.GetReplicationPlan(ctx, targetRepo.ID, plan.ID)
	if err != nil || !after.LeaseExpiresAt.After(before.LeaseExpiresAt) {
		t.Fatalf("heartbeat did not extend lease before=%#v after=%#v err=%v", before, after, err)
	}
	close(blockingStore.release)
	select {
	case <-publishEntered:
	case <-ctx.Done():
		t.Fatal("worker did not reach fenced publication")
	}
	recoveryDone := make(chan struct {
		count int
		err   error
	}, 1)
	go func() {
		count, recoverErr := storeB.RecoverExpiredReplicationPlans(ctx, time.Now().UTC().Add(11*time.Minute))
		recoveryDone <- struct {
			count int
			err   error
		}{count: count, err: recoverErr}
	}()
	select {
	case result := <-recoveryDone:
		if result.err != nil || result.count != 0 {
			t.Fatalf("publication fence recovery=%#v", result)
		}
	case <-ctx.Done():
		t.Fatal("lease recovery blocked instead of skipping the fenced plan")
	}
	stillRunning, err := storeB.GetReplicationPlan(ctx, targetRepo.ID, plan.ID)
	if err != nil || stillRunning.State != "running" || stillRunning.LeaseToken == "" {
		t.Fatalf("fenced plan changed ownership=%#v err=%v", stillRunning, err)
	}
	close(publishRelease)
	select {
	case runErr := <-workerDone:
		if runErr != nil {
			t.Fatal(runErr)
		}
	case <-ctx.Done():
		t.Fatal("heartbeat worker did not finish")
	}
	completed, err := storeB.GetReplicationPlan(ctx, targetRepo.ID, plan.ID)
	if err != nil || completed.State != "completed" {
		t.Fatalf("completed fenced plan=%#v err=%v", completed, err)
	}
}

func sha256DigestForIntegration(body []byte) string {
	sum := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func TestPostgresRawReplicationManagementAPI(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is required for PostgreSQL integration tests")
	}
	ctx := context.Background()
	store, err := repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	source, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "replication-api-source-" + uuid.NewString(), Format: repository.FormatRaw})
	if err != nil {
		t.Fatal(err)
	}
	target, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "replication-api-target-" + uuid.NewString(), Format: repository.FormatRaw})
	if err != nil {
		t.Fatal(err)
	}
	body := []byte("PostgreSQL managed replication source")
	sum := sha256.Sum256(body)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	asset := repository.RawAsset{RepositoryID: source.ID, Path: "releases/widget.txt", ObjectKey: "native/raw/sha256/" + hex.EncodeToString(sum[:]), Digest: digest, Size: int64(len(body))}
	if _, err = store.PutRawAsset(ctx, asset); err != nil {
		t.Fatal(err)
	}
	objects := NewMemoryOCIObjectStore()
	if err = objects.Put(ctx, asset.ObjectKey, body); err != nil {
		t.Fatal(err)
	}
	grants := []repository.RepositoryGrant{{Principal: "replicator", Scopes: []string{"repositories:admin"}}}
	if _, err = store.ReplaceRepositoryGrants(ctx, source.ID, grants, "1"); err != nil {
		t.Fatal(err)
	}
	if _, err = store.ReplaceRepositoryGrants(ctx, target.ID, grants, "1"); err != nil {
		t.Fatal(err)
	}
	authenticator := testAuthenticator()
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, authenticator)
	req := httptest.NewRequest(http.MethodPost, "/api/v2/repositories/"+source.ID+"/replications", strings.NewReader(`{"targetRepositoryId":"`+target.ID+`","coordinate":"`+asset.Path+`","digest":"`+digest+`"}`))
	authorize(req, authenticator.IssueToken("replicator"))
	req.Header.Set("Idempotency-Key", "postgres-replication")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	if response.Code != http.StatusAccepted {
		t.Fatalf("create=%d %s", response.Code, response.Body.String())
	}
	plans, err := store.ListReplicationPlans(ctx, target.ID, 10)
	if err != nil || len(plans) != 1 || plans[0].SourceRepositoryID != source.ID || plans[0].TargetRepositoryID != target.ID || plans[0].Format != repository.FormatRaw {
		t.Fatalf("plans=%#v err=%v", plans, err)
	}
	if err = (RawReplication{Store: store, Source: objects, Destination: objects}).RunJobs(ctx, 1); err != nil {
		t.Fatal(err)
	}
	plans, err = store.ListReplicationPlans(ctx, target.ID, 10)
	if err != nil || len(plans) != 1 || plans[0].State != "completed" {
		t.Fatalf("completed plans=%#v err=%v", plans, err)
	}
}

func TestPostgresRustFSRawPromotionRetainsSharedObject(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	endpoint := os.Getenv("TEST_RUSTFS_ENDPOINT")
	accessKey := os.Getenv("TEST_RUSTFS_ACCESS_KEY")
	secretKey := os.Getenv("TEST_RUSTFS_SECRET_KEY")
	if databaseURL == "" || endpoint == "" || accessKey == "" || secretKey == "" {
		t.Skip("PostgreSQL and S3 integration environment is required")
	}
	ctx := context.Background()
	store, err := repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	source, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "promotion-raw-source-" + uuid.NewString(), Format: repository.FormatRaw})
	if err != nil {
		t.Fatal(err)
	}
	target, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "promotion-raw-target-" + uuid.NewString(), Format: repository.FormatRaw})
	if err != nil {
		t.Fatal(err)
	}
	objects, err := NewRustFSOCIObjectStore(endpoint, accessKey, secretKey, "promotion-raw-"+strings.ReplaceAll(uuid.NewString(), "-", "")[:20])
	if err != nil {
		t.Fatal(err)
	}
	if err = objects.EnsureBucket(ctx); err != nil {
		t.Fatal(err)
	}
	body := []byte("Raw promotion backed by RustFS")
	sum := sha256.Sum256(body)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	asset := repository.RawAsset{RepositoryID: source.ID, Path: "releases/widget.txt", ObjectKey: "native/raw/sha256/" + hex.EncodeToString(sum[:]), Digest: digest, Size: int64(len(body)), ContentType: "text/plain"}
	if err = objects.PutVerifiedReader(ctx, asset.ObjectKey, strings.NewReader(string(body)), asset.Size, asset.Digest); err != nil {
		t.Fatal(err)
	}
	if _, err = store.PutRawAsset(ctx, asset); err != nil {
		t.Fatal(err)
	}
	job, _, err := (rawprotocol.NativePromotion{Store: store}).Enqueue(ctx, target.ID, "rustfs-raw-promotion", rawprotocol.PromotionPayload{SourceRepositoryID: source.ID, Path: asset.Path, Digest: asset.Digest})
	if err != nil {
		t.Fatal(err)
	}
	if err = (rawprotocol.NativePromotion{Store: store}).RunJobs(ctx, 1); err != nil {
		t.Fatal(err)
	}
	jobs, err := store.ListLifecycleJobs(ctx, target.ID, 10)
	if err != nil || len(jobs) != 1 || jobs[0].ID != job.ID || jobs[0].State != repository.LifecycleJobCompleted {
		t.Fatalf("jobs=%#v err=%v", jobs, err)
	}
	targetAsset, err := store.GetRawAsset(ctx, target.ID, asset.Path)
	if err != nil || targetAsset.ObjectKey != asset.ObjectKey || targetAsset.Digest != asset.Digest {
		t.Fatalf("target asset=%#v err=%v", targetAsset, err)
	}
	if info, err := objects.Stat(ctx, asset.ObjectKey); err != nil || info.Size != asset.Size || info.Digest != asset.Digest {
		t.Fatalf("promoted RustFS object=%#v err=%v", info, err)
	}
}

func TestPostgresRustFSRawReplicationPublishesTargetAsset(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	endpoint := os.Getenv("TEST_RUSTFS_ENDPOINT")
	accessKey := os.Getenv("TEST_RUSTFS_ACCESS_KEY")
	secretKey := os.Getenv("TEST_RUSTFS_SECRET_KEY")
	if databaseURL == "" || endpoint == "" || accessKey == "" || secretKey == "" {
		t.Skip("PostgreSQL and S3 integration environment is required")
	}
	ctx := context.Background()
	store, err := repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	objects, err := NewRustFSOCIObjectStore(endpoint, accessKey, secretKey, "replication-raw-"+strings.ReplaceAll(uuid.NewString(), "-", "")[:20])
	if err != nil {
		t.Fatal(err)
	}
	if err = objects.EnsureBucket(ctx); err != nil {
		t.Fatal(err)
	}
	source, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "replication-raw-source-" + uuid.NewString(), Format: repository.FormatRaw})
	if err != nil {
		t.Fatal(err)
	}
	target, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "replication-raw-target-" + uuid.NewString(), Format: repository.FormatRaw})
	if err != nil {
		t.Fatal(err)
	}
	body := []byte("Raw replication published after RustFS verification")
	sum := sha256.Sum256(body)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	asset := repository.RawAsset{RepositoryID: source.ID, Path: "releases/widget.txt", ObjectKey: "native/raw/sha256/" + hex.EncodeToString(sum[:]), Digest: digest, Size: int64(len(body)), ContentType: "text/plain"}
	if err = objects.PutVerifiedReader(ctx, asset.ObjectKey, strings.NewReader(string(body)), asset.Size, digest); err != nil {
		t.Fatal(err)
	}
	if _, err = store.PutRawAsset(ctx, asset); err != nil {
		t.Fatal(err)
	}
	plan := repository.ReplicationPlan{ID: uuid.NewString(), SourceRepositoryID: source.ID, TargetRepositoryID: target.ID, Format: repository.FormatRaw, Coordinate: asset.Path, Digest: digest, IdempotencyKey: "raw-runtime-" + uuid.NewString()}
	if _, _, err = store.CreateReplicationPlan(ctx, plan, []repository.ReplicationCheckpoint{{ObjectKey: asset.ObjectKey, Digest: digest, Size: asset.Size}}); err != nil {
		t.Fatal(err)
	}
	if err = (RawReplication{Store: store, Source: objects, Destination: objects}).RunJobs(ctx, 1); err != nil {
		t.Fatal(err)
	}
	targetAsset, err := store.GetRawAsset(ctx, target.ID, asset.Path)
	if err != nil || targetAsset.Digest != asset.Digest || targetAsset.ObjectKey != asset.ObjectKey {
		t.Fatalf("target asset=%#v err=%v", targetAsset, err)
	}
	plans, err := store.ListReplicationPlans(ctx, target.ID, 10)
	if err != nil || len(plans) != 1 || plans[0].State != "completed" {
		t.Fatalf("plans=%#v err=%v", plans, err)
	}
}
