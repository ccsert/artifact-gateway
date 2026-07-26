//go:build integration

package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	rawprotocol "github.com/artifact-gateway/artifact-gateway/internal/protocol/raw"
	"github.com/artifact-gateway/artifact-gateway/internal/replication"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

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
	plan := repository.ReplicationPlan{ID: uuid.NewString(), SourceRepositoryID: source.ID, TargetRepositoryID: target.ID, Format: repository.FormatRaw, IdempotencyKey: "replication-" + uuid.NewString()}
	checks := []repository.ReplicationCheckpoint{
		{ObjectKey: "native/raw/a", Digest: "sha256:" + strings.Repeat("a", 64), Size: 3},
		{ObjectKey: "native/raw/b", Digest: "sha256:" + strings.Repeat("b", 64), Size: 5},
	}
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
	if err = store.UpdateReplicationCheckpoint(ctx, repository.ReplicationCheckpoint{PlanID: plan.ID, ObjectKey: checks[0].ObjectKey, Digest: checks[0].Digest, Size: checks[0].Size, ByteOffset: checks[0].Size, State: "verified", Attempts: 1, VerifiedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if err = store.FailReplicationPlan(ctx, plan.ID, "temporary object-store failure"); err != nil {
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
	if err = store.CompleteReplicationPlan(ctx, plan.ID); err != nil {
		t.Fatal(err)
	}
	plans, err := store.ListReplicationPlans(ctx, target.ID, 10)
	if err != nil || len(plans) != 1 || plans[0].ID != plan.ID || plans[0].State != "completed" {
		t.Fatalf("plans=%#v err=%v", plans, err)
	}
}

func TestPostgresMinIOReplicationCopiesAndVerifiesCheckpoint(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	endpoint := os.Getenv("TEST_S3_ENDPOINT")
	accessKey := os.Getenv("TEST_S3_ACCESS_KEY")
	secretKey := os.Getenv("TEST_S3_SECRET_KEY")
	if databaseURL == "" || endpoint == "" || accessKey == "" || secretKey == "" {
		t.Skip("PostgreSQL and S3 integration environment is required")
	}
	ctx := context.Background()
	store, err := repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	sourceRepo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "replication-minio-source-" + uuid.NewString(), Format: repository.FormatRaw})
	if err != nil {
		t.Fatal(err)
	}
	targetRepo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "replication-minio-target-" + uuid.NewString(), Format: repository.FormatRaw})
	if err != nil {
		t.Fatal(err)
	}
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:20]
	sourceObjects, err := NewS3OCIObjectStore(endpoint, accessKey, secretKey, "replication-source-"+suffix)
	if err != nil {
		t.Fatal(err)
	}
	targetObjects, err := NewS3OCIObjectStore(endpoint, accessKey, secretKey, "replication-target-"+suffix)
	if err != nil {
		t.Fatal(err)
	}
	if err = sourceObjects.EnsureBucket(ctx); err != nil {
		t.Fatal(err)
	}
	if err = targetObjects.EnsureBucket(ctx); err != nil {
		t.Fatal(err)
	}
	body := []byte("cross-bucket replication through PostgreSQL checkpoints")
	sum := sha256.Sum256(body)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	key := "native/raw/sha256/" + hex.EncodeToString(sum[:])
	if err = sourceObjects.PutVerifiedReader(ctx, key, strings.NewReader(string(body)), int64(len(body)), digest); err != nil {
		t.Fatal(err)
	}
	plan := repository.ReplicationPlan{ID: uuid.NewString(), SourceRepositoryID: sourceRepo.ID, TargetRepositoryID: targetRepo.ID, Format: repository.FormatRaw, IdempotencyKey: "minio-" + uuid.NewString()}
	if _, _, err = store.CreateReplicationPlan(ctx, plan, []repository.ReplicationCheckpoint{{ObjectKey: key, Digest: digest, Size: int64(len(body))}}); err != nil {
		t.Fatal(err)
	}
	if err = (replication.Worker{Store: store, Source: sourceObjects, Destination: targetObjects, ChunkBytes: 7}).Run(ctx, 1); err != nil {
		t.Fatal(err)
	}
	checks, err := store.ListReplicationCheckpoints(ctx, plan.ID)
	if err != nil || len(checks) != 1 || checks[0].State != "verified" || checks[0].ByteOffset != int64(len(body)) || checks[0].VerifiedAt.IsZero() {
		t.Fatalf("checkpoints=%#v err=%v", checks, err)
	}
	copied, err := targetObjects.Get(ctx, key)
	if err != nil || string(copied) != string(body) {
		t.Fatalf("target object=%q err=%v", copied, err)
	}
	info, err := targetObjects.Stat(ctx, key)
	if err != nil || info.Digest != digest || info.Size != int64(len(body)) {
		t.Fatalf("target info=%#v err=%v", info, err)
	}
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
}

func TestPostgresMinIORawPromotionRetainsSharedObject(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	endpoint := os.Getenv("TEST_S3_ENDPOINT")
	accessKey := os.Getenv("TEST_S3_ACCESS_KEY")
	secretKey := os.Getenv("TEST_S3_SECRET_KEY")
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
	objects, err := NewS3OCIObjectStore(endpoint, accessKey, secretKey, "promotion-raw-"+strings.ReplaceAll(uuid.NewString(), "-", "")[:20])
	if err != nil {
		t.Fatal(err)
	}
	if err = objects.EnsureBucket(ctx); err != nil {
		t.Fatal(err)
	}
	body := []byte("Raw promotion backed by MinIO")
	sum := sha256.Sum256(body)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	asset := repository.RawAsset{RepositoryID: source.ID, Path: "releases/widget.txt", ObjectKey: "native/raw/sha256/" + hex.EncodeToString(sum[:]), Digest: digest, Size: int64(len(body)), ContentType: "text/plain"}
	if err = objects.PutVerifiedReader(ctx, asset.ObjectKey, strings.NewReader(string(body)), asset.Size, asset.Digest); err != nil {
		t.Fatal(err)
	}
	if _, err = store.PutRawAsset(ctx, asset); err != nil {
		t.Fatal(err)
	}
	job, _, err := (rawprotocol.NativePromotion{Store: store}).Enqueue(ctx, target.ID, "minio-raw-promotion", rawprotocol.PromotionPayload{SourceRepositoryID: source.ID, Path: asset.Path, Digest: asset.Digest})
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
		t.Fatalf("promoted MinIO object=%#v err=%v", info, err)
	}
}
