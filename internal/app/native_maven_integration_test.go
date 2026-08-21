//go:build integration

package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	mavenprotocol "github.com/artifact-gateway/artifact-gateway/internal/protocol/maven"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

func TestPostgresMavenProtocolPublishesDirectlyByDefault(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is required for PostgreSQL integration tests")
	}
	store, err := repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "maven-direct-" + uuid.NewString(), Format: repository.FormatMaven})
	if err != nil {
		t.Fatal(err)
	}
	objects := NewMemoryOCIObjectStore()
	handler := NewGatewayHandler(Dependencies{NativeMavenObjectStore: objects}, store, TestAdapter{}, Authenticator{
		ResolverToken:     "resolver-secret",
		RepositoryWriters: map[string][]string{"maven": {repo.Name}},
	})
	pom := []byte("<project><groupId>org.example</groupId><artifactId>direct</artifactId><version>1.0.0</version></project>")
	put := httptest.NewRequest(http.MethodPut, "/repository/maven/"+repo.Name+"/org/example/direct/1.0.0/direct-1.0.0.pom", bytes.NewReader(pom))
	put.SetBasicAuth("maven", "resolver-secret")
	putResponse := httptest.NewRecorder()
	handler.ServeHTTP(putResponse, put)
	if putResponse.Code != http.StatusCreated {
		t.Fatalf("PUT=%d %s", putResponse.Code, putResponse.Body.String())
	}
	get := httptest.NewRequest(http.MethodGet, "/repository/maven/"+repo.Name+"/org/example/direct/1.0.0/direct-1.0.0.pom", nil)
	get.SetBasicAuth("maven", "resolver-secret")
	getResponse := httptest.NewRecorder()
	handler.ServeHTTP(getResponse, get)
	if getResponse.Code != http.StatusOK || !bytes.Equal(getResponse.Body.Bytes(), pom) {
		t.Fatalf("GET=%d %q", getResponse.Code, getResponse.Body.String())
	}
}

func TestPostgresRustFSMavenPromotionSharesVerifiedObjectAndCompletesJob(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	s3Endpoint := os.Getenv("TEST_RUSTFS_ENDPOINT")
	s3AccessKey := os.Getenv("TEST_RUSTFS_ACCESS_KEY")
	s3SecretKey := os.Getenv("TEST_RUSTFS_SECRET_KEY")
	if databaseURL == "" || s3Endpoint == "" || s3AccessKey == "" || s3SecretKey == "" {
		t.Skip("PostgreSQL and S3 integration environment is required")
	}
	ctx := context.Background()
	store, err := repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	objects, err := NewRustFSOCIObjectStore(s3Endpoint, s3AccessKey, s3SecretKey, "maven-promotion-"+strings.ReplaceAll(uuid.NewString(), "-", "")[:20])
	if err != nil {
		t.Fatal(err)
	}
	if err = objects.EnsureBucket(ctx); err != nil {
		t.Fatal(err)
	}
	source, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "promotion-source-" + uuid.NewString(), Format: repository.FormatMaven})
	if err != nil {
		t.Fatal(err)
	}
	target, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "promotion-target-" + uuid.NewString(), Format: repository.FormatMaven})
	if err != nil {
		t.Fatal(err)
	}
	body := []byte("promoted from PostgreSQL and RustFS")
	sum := sha256.Sum256(body)
	digest := "sha256:" + fmt.Sprintf("%x", sum[:])
	objectKey := "native/maven/sha256/" + fmt.Sprintf("%x", sum[:])
	if err = objects.Put(ctx, objectKey, body); err != nil {
		t.Fatal(err)
	}
	coordinate := "org.example:promotion:1.0.0"
	session := repository.MavenPublishSession{ID: uuid.NewString(), RepositoryID: source.ID, Coordinate: coordinate, Publisher: "integration", State: "open", ExpiresAt: time.Now().Add(time.Hour), Objects: []repository.MavenDeclaredObject{{Name: "promotion-1.0.0.jar", Digest: digest, Size: int64(len(body))}}}
	if _, err = store.CreateMavenPublishSession(ctx, session); err != nil {
		t.Fatal(err)
	}
	if err = store.MarkMavenPublishObject(ctx, session.ID, session.Objects[0].Name, objectKey); err != nil {
		t.Fatal(err)
	}
	assetPath := "org/example/promotion/1.0.0/promotion-1.0.0.jar"
	if _, err = store.CommitMavenPublishSession(ctx, session.ID, []repository.MavenAsset{{RepositoryID: source.ID, Path: assetPath, ObjectKey: objectKey, Digest: digest, Size: int64(len(body))}}); err != nil {
		t.Fatal(err)
	}
	worker := mavenprotocol.NativePromotion{Store: store}
	job, _, err := worker.Enqueue(ctx, target.ID, "postgres-rustfs-promotion", mavenprotocol.PromotionPayload{SourceRepositoryID: source.ID, Coordinate: coordinate, Digest: digest, PromotionID: uuid.NewString()})
	if err != nil {
		t.Fatal(err)
	}
	if err = worker.RunJobs(ctx, 1); err != nil {
		t.Fatal(err)
	}
	jobs, err := store.ListLifecycleJobs(ctx, target.ID, 10)
	if err != nil || len(jobs) != 1 || jobs[0].ID != job.ID || jobs[0].State != repository.LifecycleJobCompleted {
		t.Fatalf("promotion job=%#v err=%v", jobs, err)
	}
	asset, err := store.GetMavenAsset(ctx, target.ID, assetPath)
	if err != nil || asset.ObjectKey != objectKey || asset.Digest != digest {
		t.Fatalf("promoted asset=%#v err=%v", asset, err)
	}
	if _, err = objects.Stat(ctx, objectKey); err != nil {
		t.Fatalf("promotion must retain the shared RustFS object: %v", err)
	}
}

func TestPostgresRustFSMavenReplicationPublishesTargetOwnedCheckpoints(t *testing.T) {
	databaseURL, endpoint := os.Getenv("TEST_DATABASE_URL"), os.Getenv("TEST_RUSTFS_ENDPOINT")
	accessKey, secretKey := os.Getenv("TEST_RUSTFS_ACCESS_KEY"), os.Getenv("TEST_RUSTFS_SECRET_KEY")
	if databaseURL == "" || endpoint == "" || accessKey == "" || secretKey == "" {
		t.Skip("PostgreSQL and S3 integration environment is required")
	}
	ctx := context.Background()
	store, err := repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	objects, err := NewRustFSOCIObjectStore(endpoint, accessKey, secretKey, "maven-replication-"+strings.ReplaceAll(uuid.NewString(), "-", "")[:20])
	if err != nil {
		t.Fatal(err)
	}
	if err = objects.EnsureBucket(ctx); err != nil {
		t.Fatal(err)
	}
	source, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "replication-source-" + uuid.NewString(), Format: repository.FormatMaven})
	if err != nil {
		t.Fatal(err)
	}
	target, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "replication-target-" + uuid.NewString(), Format: repository.FormatMaven})
	if err != nil {
		t.Fatal(err)
	}
	body := []byte("replicated through PostgreSQL and RustFS")
	sum := sha256.Sum256(body)
	digest := "sha256:" + fmt.Sprintf("%x", sum[:])
	sourceKey := "native/maven/sha256/" + fmt.Sprintf("%x", sum[:])
	targetKey := mavenReplicationTargetObjectKey(target.ID, digest)
	if err = objects.Put(ctx, sourceKey, body); err != nil {
		t.Fatal(err)
	}
	coordinate, assetPath := "org.example:replication:1.0.0", "org/example/replication/1.0.0/replication-1.0.0.jar"
	session := repository.MavenPublishSession{ID: uuid.NewString(), RepositoryID: source.ID, Coordinate: coordinate, Publisher: "integration", PomObject: "replication-1.0.0.jar", State: "open", ExpiresAt: time.Now().Add(time.Hour), Objects: []repository.MavenDeclaredObject{{Name: "replication-1.0.0.jar", Digest: digest, Size: int64(len(body))}}}
	if _, err = store.CreateMavenPublishSession(ctx, session); err != nil {
		t.Fatal(err)
	}
	if err = store.MarkMavenPublishObject(ctx, session.ID, session.Objects[0].Name, sourceKey); err != nil {
		t.Fatal(err)
	}
	if _, err = store.CommitMavenPublishSession(ctx, session.ID, []repository.MavenAsset{{RepositoryID: source.ID, Path: assetPath, ObjectKey: sourceKey, Digest: digest, Size: int64(len(body))}}); err != nil {
		t.Fatal(err)
	}
	plan := repository.ReplicationPlan{ID: uuid.NewString(), SourceRepositoryID: source.ID, TargetRepositoryID: target.ID, Format: repository.FormatMaven, Coordinate: coordinate, Digest: digest, IdempotencyKey: "postgres-rustfs-maven"}
	if _, _, err = store.CreateReplicationPlan(ctx, plan, []repository.ReplicationCheckpoint{{SourceObjectKey: sourceKey, ObjectKey: targetKey, Digest: digest, Size: int64(len(body))}}); err != nil {
		t.Fatal(err)
	}
	if err = (MavenReplication{Store: store, Source: objects, Destination: objects, ChunkBytes: 5}).RunJobs(ctx, 1); err != nil {
		t.Fatal(err)
	}
	asset, err := store.GetMavenAsset(ctx, target.ID, assetPath)
	if err != nil || asset.ObjectKey != targetKey || asset.Digest != digest || asset.Size != int64(len(body)) {
		t.Fatalf("asset=%#v err=%v", asset, err)
	}
	if got, err := objects.Get(ctx, targetKey); err != nil || string(got) != string(body) {
		t.Fatalf("target object=%q err=%v", got, err)
	}
	plans, err := store.ListReplicationPlans(ctx, target.ID, 10)
	if err != nil || len(plans) == 0 || plans[0].State != "completed" {
		t.Fatalf("plans=%#v err=%v", plans, err)
	}
}

func TestPostgresMavenCollectorClaimSkipsCommitLockedSession(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is required for PostgreSQL integration tests")
	}
	store, err := repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "claim-fence-" + uuid.NewString(), Format: repository.FormatMaven})
	if err != nil {
		t.Fatal(err)
	}
	session := repository.MavenPublishSession{ID: uuid.NewString(), RepositoryID: repo.ID, Coordinate: "org.example:widget:1.0.0", Publisher: "alice", PomObject: "widget-1.0.0.pom", State: "open", ExpiresAt: time.Now().Add(-time.Hour), Objects: []repository.MavenDeclaredObject{{Name: "widget-1.0.0.pom", Digest: "sha256:claim-fence", Size: 1}}}
	if _, err = store.CreateMavenPublishSession(ctx, session); err != nil {
		t.Fatal(err)
	}
	key := "native/maven/sha256/claim-fence-" + uuid.NewString()
	if err = store.MarkMavenPublishObject(ctx, session.ID, session.Objects[0].Name, key); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, `SELECT 1 FROM native_maven_publish_sessions WHERE id=$1 FOR UPDATE`, session.ID); err != nil {
		t.Fatal(err)
	}
	claimed, err := store.ClaimExpiredMavenObjectIntents(ctx, time.Now().Add(time.Hour), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(claimed) != 0 {
		t.Fatalf("collector claimed an intent behind commit session lock: %#v", claimed)
	}
}

func TestPostgresMavenCoordinateSearchProjection(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is required for PostgreSQL integration tests")
	}
	store, err := repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	ctx := context.Background()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "maven-search-" + uuid.NewString(), Format: repository.FormatMaven})
	if err != nil {
		t.Fatal(err)
	}
	commit := func(coordinate string) {
		t.Helper()
		id := uuid.NewString()
		objectKey := "native/maven/sha256/search-" + id
		digest := "sha256:" + strings.Repeat("a", 64)
		session := repository.MavenPublishSession{ID: id, RepositoryID: repo.ID, Coordinate: coordinate, Publisher: "searcher", PomObject: "widget.pom", State: "open", ExpiresAt: time.Now().Add(time.Hour), Objects: []repository.MavenDeclaredObject{{Name: "widget.pom", Digest: digest, Size: 1}}}
		if _, err := store.CreateMavenPublishSession(ctx, session); err != nil {
			t.Fatal(err)
		}
		if err := store.MarkMavenPublishObject(ctx, session.ID, "widget.pom", objectKey); err != nil {
			t.Fatal(err)
		}
		if _, err := store.CommitMavenPublishSession(ctx, session.ID, []repository.MavenAsset{{RepositoryID: repo.ID, Path: strings.ReplaceAll(coordinate, ":", "/") + "/asset.pom", ObjectKey: objectKey, Digest: digest, Size: 1}}); err != nil {
			t.Fatal(err)
		}
	}
	commit("org.example:widget:2.0.0")
	commit("org.example:widget:1.0.0")
	commit("org.example:other:1.0.0")
	items, err := store.SearchMavenArtifacts(ctx, repo.ID, "org.example:widget:", 2, repository.MavenArtifactCursor{})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].Coordinate != "org.example:widget:1.0.0" || items[1].Coordinate != "org.example:widget:2.0.0" {
		t.Fatalf("search=%#v", items)
	}
	next, err := store.SearchMavenArtifacts(ctx, repo.ID, "org.example:widget:", 2, repository.MavenArtifactAfterCoordinate(items[0].Coordinate))
	if err != nil {
		t.Fatal(err)
	}
	if len(next) != 1 || next[0].Coordinate != "org.example:widget:2.0.0" {
		t.Fatalf("next=%#v", next)
	}
}

func TestPostgresMavenTombstoneRestoreBeforeAndAfterReclaim(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is required for PostgreSQL integration tests")
	}
	store, err := repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "maven-restore-" + uuid.NewString(), Format: repository.FormatMaven})
	if err != nil {
		t.Fatal(err)
	}
	coordinate := "org.example:restore:1.0.0"
	session := repository.MavenPublishSession{ID: uuid.NewString(), RepositoryID: repo.ID, Coordinate: coordinate, Publisher: "restore", PomObject: "restore-1.0.0.jar", State: "open", ExpiresAt: time.Now().Add(time.Hour), Objects: []repository.MavenDeclaredObject{{Name: "restore-1.0.0.jar", Digest: "sha256:restore", Size: 7}}}
	if _, err = store.CreateMavenPublishSession(ctx, session); err != nil {
		t.Fatal(err)
	}
	key := "native/maven/sha256/restore-" + uuid.NewString()
	if err = store.MarkMavenPublishObject(ctx, session.ID, session.PomObject, key); err != nil {
		t.Fatal(err)
	}
	artifact, err := store.CommitMavenPublishSession(ctx, session.ID, []repository.MavenAsset{{RepositoryID: repo.ID, Path: "org/example/restore/1.0.0/restore-1.0.0.jar", ObjectKey: key, Digest: session.Objects[0].Digest, Size: 7}})
	if err != nil {
		t.Fatal(err)
	}
	objects := NewMemoryOCIObjectStore()
	if err = objects.Put(ctx, key, []byte("restore")); err != nil {
		t.Fatal(err)
	}
	if _, err = store.TombstoneMavenArtifact(ctx, repo.ID, artifact.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = store.GetMavenAsset(ctx, repo.ID, "org/example/restore/1.0.0/restore-1.0.0.jar"); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("tombstoned asset remained readable: %v", err)
	}
	if _, err = store.RestoreMavenArtifact(ctx, repo.ID, artifact.ID); err != nil {
		t.Fatalf("restore before reclaim: %v", err)
	}
	if _, err = store.TombstoneMavenArtifact(ctx, repo.ID, artifact.ID); err != nil {
		t.Fatal(err)
	}
	if err = (NativeMavenMaintenance{Store: store, Objects: objects, Now: func() time.Time { return time.Now().Add(25 * time.Hour) }}).Collect(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = objects.Get(ctx, key); err == nil {
		t.Fatal("reclaim did not delete Maven object")
	}
	if _, err = store.RestoreMavenArtifact(ctx, repo.ID, artifact.ID); !errors.Is(err, repository.ErrDisabled) {
		t.Fatalf("restore after reclaim err=%v", err)
	}
}

func TestPostgresMavenMaintenanceRetainsObjectReferencedAfterClaim(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is required for PostgreSQL integration tests")
	}
	store, err := repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "claim-reference-" + uuid.NewString(), Format: repository.FormatMaven})
	if err != nil {
		t.Fatal(err)
	}
	session := repository.MavenPublishSession{ID: uuid.NewString(), RepositoryID: repo.ID, Coordinate: "org.example:claim-reference:1.0.0", Publisher: "collector", PomObject: "claim-reference-1.0.0.pom", State: "open", ExpiresAt: time.Now().Add(-25 * time.Hour), Objects: []repository.MavenDeclaredObject{{Name: "claim-reference-1.0.0.pom", Digest: "sha256:claim-reference", Size: 1}}}
	if _, err = store.CreateMavenPublishSession(ctx, session); err != nil {
		t.Fatal(err)
	}
	key := "native/maven/sha256/claim-reference-" + uuid.NewString()
	if err = store.MarkMavenPublishObject(ctx, session.ID, session.Objects[0].Name, key); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.ExecContext(ctx, `UPDATE native_maven_object_intents SET created_at=now()-interval '25 hours' WHERE object_key=$1`, key); err != nil {
		t.Fatal(err)
	}
	objects := NewMemoryOCIObjectStore()
	if err = objects.Put(ctx, key, []byte("staged")); err != nil {
		t.Fatal(err)
	}
	maintenance := NativeMavenMaintenance{Store: referenceAfterClaimStore{NativeMavenStore: store, LifecycleJobStore: store, db: db, repositoryID: repo.ID}, Objects: objects, Now: func() time.Time { return time.Now() }}
	if err = maintenance.Collect(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = objects.Get(ctx, key); err != nil {
		t.Fatalf("referenced object was deleted: %v", err)
	}
	var intents, references int
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM native_maven_object_intents WHERE object_key=$1`, key).Scan(&intents); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM native_maven_object_references WHERE object_key=$1`, key).Scan(&references); err != nil || intents != 1 || references != 1 {
		t.Fatalf("retained intent/reference intents=%d references=%d err=%v", intents, references, err)
	}
}

func TestPostgresMavenCollectorRecoversClaimAfterTombstoneWriteFailure(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is required for PostgreSQL integration tests")
	}
	store, err := repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "collector-recovery-" + uuid.NewString(), Format: repository.FormatMaven})
	if err != nil {
		t.Fatal(err)
	}
	session := repository.MavenPublishSession{ID: uuid.NewString(), RepositoryID: repo.ID, Coordinate: "org.example:collector-recovery:1.0.0", Publisher: "collector", PomObject: "collector-recovery-1.0.0.pom", State: "open", ExpiresAt: time.Now().Add(-25 * time.Hour), Objects: []repository.MavenDeclaredObject{{Name: "collector-recovery-1.0.0.pom", Digest: "sha256:collector-recovery", Size: 1}}}
	if _, err = store.CreateMavenPublishSession(ctx, session); err != nil {
		t.Fatal(err)
	}
	key := "native/maven/sha256/collector-recovery-" + uuid.NewString()
	if err = store.MarkMavenPublishObject(ctx, session.ID, session.Objects[0].Name, key); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.ExecContext(ctx, `UPDATE native_maven_object_intents SET created_at=now()-interval '25 hours' WHERE object_key=$1`, key); err != nil {
		t.Fatal(err)
	}
	objects := NewMemoryOCIObjectStore()
	if err = objects.Put(ctx, key, []byte("staged")); err != nil {
		t.Fatal(err)
	}
	failed := NativeMavenMaintenance{Store: failMavenTombstoneStore{NativeMavenStore: store, LifecycleJobStore: store}, Objects: objects, Now: func() time.Time { return time.Now() }}
	if err = failed.Collect(ctx); err == nil {
		t.Fatal("collector must report the interrupted tombstone write")
	}
	if _, err = objects.Get(ctx, key); err == nil {
		t.Fatal("first collector must have deleted the staged object")
	}
	var claimedAt, deletedAt sql.NullTime
	if err = db.QueryRowContext(ctx, `SELECT claimed_at, deleted_at FROM native_maven_object_intents WHERE object_key=$1`, key).Scan(&claimedAt, &deletedAt); err != nil || !claimedAt.Valid || deletedAt.Valid {
		t.Fatalf("interrupted state claimed=%v deleted=%v err=%v", claimedAt.Valid, deletedAt.Valid, err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE native_maven_object_intents SET claimed_at=now()-interval '6 minutes' WHERE object_key=$1`, key); err != nil {
		t.Fatal(err)
	}
	if err = (NativeMavenMaintenance{Store: store, Objects: objects, Now: func() time.Time { return time.Now() }}).Collect(ctx); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRowContext(ctx, `SELECT claimed_at, deleted_at FROM native_maven_object_intents WHERE object_key=$1`, key).Scan(&claimedAt, &deletedAt); err != nil || !claimedAt.Valid || !deletedAt.Valid {
		t.Fatalf("recovered tombstone claimed=%v deleted=%v err=%v", claimedAt.Valid, deletedAt.Valid, err)
	}
	retry := repository.MavenPublishSession{ID: uuid.NewString(), RepositoryID: repo.ID, Coordinate: "org.example:collector-retry:1.0.0", Publisher: "retry", PomObject: "collector-recovery-1.0.0.pom", State: "open", ExpiresAt: time.Now().Add(time.Hour), Objects: session.Objects}
	if _, err = store.CreateMavenPublishSession(ctx, retry); err != nil {
		t.Fatal(err)
	}
	if err = store.MarkMavenPublishObject(ctx, retry.ID, retry.PomObject, key); err != nil {
		t.Fatalf("tombstoned object cannot be re-staged: %v", err)
	}
	if err = objects.Put(ctx, key, []byte("restaged")); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRowContext(ctx, `SELECT claimed_at, deleted_at FROM native_maven_object_intents WHERE object_key=$1`, key).Scan(&claimedAt, &deletedAt); err != nil || claimedAt.Valid || deletedAt.Valid {
		t.Fatalf("restaged intent claimed=%v deleted=%v err=%v", claimedAt.Valid, deletedAt.Valid, err)
	}
}

func TestPostgresMavenCollectorClaimTokenFencesOverlappingCollectors(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is required for PostgreSQL integration tests")
	}
	store, err := repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "claim-token-" + uuid.NewString(), Format: repository.FormatMaven})
	if err != nil {
		t.Fatal(err)
	}
	declared := repository.MavenDeclaredObject{Name: "claim-token-1.0.0.pom", Digest: "sha256:claim-token", Size: 1}
	stale := repository.MavenPublishSession{ID: uuid.NewString(), RepositoryID: repo.ID, Coordinate: "org.example:claim-token:1.0.0", Publisher: "stale", PomObject: declared.Name, State: "open", ExpiresAt: time.Now().Add(-25 * time.Hour), Objects: []repository.MavenDeclaredObject{declared}}
	active := repository.MavenPublishSession{ID: uuid.NewString(), RepositoryID: repo.ID, Coordinate: "org.example:claim-token:1.0.0", Publisher: "active", PomObject: declared.Name, State: "open", ExpiresAt: time.Now().Add(time.Hour), Objects: []repository.MavenDeclaredObject{declared}}
	for _, session := range []repository.MavenPublishSession{stale, active} {
		if _, err = store.CreateMavenPublishSession(ctx, session); err != nil {
			t.Fatal(err)
		}
	}
	key := "native/maven/sha256/claim-token-" + uuid.NewString()
	if err = store.MarkMavenPublishObject(ctx, stale.ID, declared.Name, key); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.ExecContext(ctx, `UPDATE native_maven_object_intents SET created_at=now()-interval '25 hours' WHERE object_key=$1`, key); err != nil {
		t.Fatal(err)
	}
	if err = store.MarkMavenPublishObject(ctx, active.ID, declared.Name, key); err != nil {
		t.Fatal(err)
	}
	first, err := store.ClaimExpiredMavenObjectIntents(ctx, time.Now().Add(-24*time.Hour), 1)
	if err != nil || len(first) != 1 {
		t.Fatalf("first claim=%#v err=%v", first, err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE native_maven_object_intents SET claimed_at=now()-interval '6 minutes' WHERE object_key=$1`, key); err != nil {
		t.Fatal(err)
	}
	second, err := store.ClaimExpiredMavenObjectIntents(ctx, time.Now().Add(-24*time.Hour), 1)
	if err != nil || len(second) != 1 || second[0].ClaimToken == first[0].ClaimToken {
		t.Fatalf("second claim=%#v first=%#v err=%v", second, first, err)
	}
	if err = store.ReleaseClaimedMavenObjectIntent(ctx, key, first[0].ClaimToken); err != repository.ErrNotFound {
		t.Fatalf("stale claimant released current lease: %v", err)
	}
	var currentToken string
	if err = db.QueryRowContext(ctx, `SELECT claimed_token FROM native_maven_object_intents WHERE object_key=$1`, key).Scan(&currentToken); err != nil || currentToken != second[0].ClaimToken {
		t.Fatalf("current token=%q want=%q err=%v", currentToken, second[0].ClaimToken, err)
	}
	_, err = store.CommitMavenPublishSession(ctx, active.ID, []repository.MavenAsset{{RepositoryID: repo.ID, Path: "org/example/claim-token/1.0.0/claim-token-1.0.0.pom", ObjectKey: key, Digest: declared.Digest, Size: 1}})
	if err != repository.ErrDisabled {
		t.Fatalf("promotion bypassed current deletion fence: %v", err)
	}
	if err = store.DeleteClaimedMavenObjectIntent(ctx, key, second[0].ClaimToken); err != nil {
		t.Fatal(err)
	}
	var references int
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM native_maven_object_references WHERE object_key=$1`, key).Scan(&references); err != nil || references != 0 {
		t.Fatalf("fenced overlap references=%d err=%v", references, err)
	}
}

func TestPostgresMavenCommitCannotReferenceObjectDuringBlockedDeletion(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is required for PostgreSQL integration tests")
	}
	store, err := repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "delete-fence-" + uuid.NewString(), Format: repository.FormatMaven})
	if err != nil {
		t.Fatal(err)
	}
	pom := []byte("<project><groupId>org.example</groupId><artifactId>delete-fence</artifactId><version>1.0.0</version></project>")
	sum := sha256.Sum256(pom)
	digest := "sha256:" + fmt.Sprintf("%x", sum)
	declared := repository.MavenDeclaredObject{Name: "delete-fence-1.0.0.pom", Digest: digest, Size: int64(len(pom))}
	stale := repository.MavenPublishSession{ID: uuid.NewString(), RepositoryID: repo.ID, Coordinate: "org.example:delete-fence:1.0.0", Publisher: "stale", PomObject: declared.Name, State: "open", ExpiresAt: time.Now().Add(-25 * time.Hour), Objects: []repository.MavenDeclaredObject{declared}}
	active := repository.MavenPublishSession{ID: uuid.NewString(), RepositoryID: repo.ID, Coordinate: "org.example:delete-fence:1.0.0", Publisher: "active", PomObject: declared.Name, State: "open", ExpiresAt: time.Now().Add(time.Hour), Objects: []repository.MavenDeclaredObject{declared}}
	for _, session := range []repository.MavenPublishSession{stale, active} {
		if _, err = store.CreateMavenPublishSession(ctx, session); err != nil {
			t.Fatal(err)
		}
	}
	key := "native/maven/sha256/" + strings.TrimPrefix(digest, "sha256:")
	if err = store.MarkMavenPublishObject(ctx, stale.ID, declared.Name, key); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.ExecContext(ctx, `UPDATE native_maven_object_intents SET created_at=now()-interval '25 hours' WHERE object_key=$1`, key); err != nil {
		t.Fatal(err)
	}
	if err = store.MarkMavenPublishObject(ctx, active.ID, declared.Name, key); err != nil {
		t.Fatal(err)
	}
	objects := blockingDeleteObjectStore{MemoryOCIObjectStore: NewMemoryOCIObjectStore(), entered: make(chan struct{}, 1), release: make(chan struct{})}
	if err = objects.Put(ctx, key, pom); err != nil {
		t.Fatal(err)
	}
	commitStore := blockingMavenCommitStore{PostgresStore: store, entered: make(chan struct{}, 1), release: make(chan struct{})}
	handler := newNativeMavenHandler(commitStore, objects, testAuthenticator())
	commitResult := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		r := httptest.NewRequest(http.MethodPost, "/api/v2/publish-sessions/"+active.ID+":commit", nil)
		r.Header.Set("Authorization", "Bearer admin-secret")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		commitResult <- w
	}()
	<-commitStore.entered
	maintenanceResult := make(chan error, 1)
	go func() {
		maintenanceResult <- NativeMavenMaintenance{Store: store, Objects: objects, Now: func() time.Time { return time.Now() }}.Collect(ctx)
	}()
	<-objects.entered
	close(commitStore.release)
	commit := <-commitResult
	if commit.Code != http.StatusUnprocessableEntity {
		t.Fatalf("commit during deletion=%d %s", commit.Code, commit.Body.String())
	}
	close(objects.release)
	if err = <-maintenanceResult; err != nil {
		t.Fatal(err)
	}
	if _, err = objects.Get(ctx, key); err == nil {
		t.Fatal("deleted object remained after a fenced commit")
	}
	var references, assets int
	var deletedAt sql.NullTime
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM native_maven_object_references WHERE object_key=$1`, key).Scan(&references); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM native_maven_assets WHERE repository_id=$1`, repo.ID).Scan(&assets); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRowContext(ctx, `SELECT deleted_at FROM native_maven_object_intents WHERE object_key=$1`, key).Scan(&deletedAt); err != nil || !deletedAt.Valid || references != 0 || assets != 0 {
		t.Fatalf("fenced deletion tombstone=%v references=%d assets=%d err=%v", deletedAt.Valid, references, assets, err)
	}
}

func TestPostgresCreateMavenPublishSessionIdempotentlyCoalescesConcurrentFirstUse(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is required for PostgreSQL integration tests")
	}
	store, err := repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "maven-idempotency-race-" + uuid.NewString(), Format: repository.FormatMaven})
	if err != nil {
		t.Fatal(err)
	}

	const creators = 32
	type createResult struct {
		session  repository.MavenPublishSession
		replayed bool
		err      error
	}
	results := make(chan createResult, creators)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for range creators {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			session := repository.MavenPublishSession{ID: uuid.NewString(), RepositoryID: repo.ID, Coordinate: "org.example:concurrent:1.0.0", Publisher: "admin", PomObject: "concurrent-1.0.0.pom", State: "open", ExpiresAt: time.Now().Add(time.Hour), Objects: []repository.MavenDeclaredObject{{Name: "concurrent-1.0.0.pom", Digest: "sha256:concurrent", Size: 1}}}
			created, replayed, createErr := store.CreateMavenPublishSessionIdempotently(ctx, session, "admin", "repositories/"+repo.ID+"/publish-sessions", "concurrent-key", "same-payload")
			results <- createResult{created, replayed, createErr}
		}()
	}
	close(start)
	wg.Wait()
	close(results)

	var sessionID string
	createdCount := 0
	for result := range results {
		if result.err != nil {
			t.Fatalf("concurrent session create: %v", result.err)
		}
		if sessionID == "" {
			sessionID = result.session.ID
		} else if result.session.ID != sessionID {
			t.Fatalf("concurrent session IDs differ: %s != %s", result.session.ID, sessionID)
		}
		if !result.replayed {
			createdCount++
		}
	}
	if createdCount != 1 {
		t.Fatalf("created sessions=%d, want 1", createdCount)
	}
}

func TestPostgresNativeMavenPublishLifecycleIntegration(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is required for PostgreSQL integration tests")
	}
	store, err := repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "maven-lifecycle-" + uuid.NewString(), Format: repository.FormatMaven})
	if err != nil {
		t.Fatal(err)
	}

	// Concurrent first use must yield one session and one replay, then an expired
	// idempotency key can create a fresh session after the original is closed.
	newSession := func(id string) repository.MavenPublishSession {
		return repository.MavenPublishSession{ID: id, RepositoryID: repo.ID, Coordinate: "org.example:concurrent:1.0.0", Publisher: "admin", PomObject: "concurrent-1.0.0.pom", State: "open", ExpiresAt: time.Now().Add(time.Hour), Objects: []repository.MavenDeclaredObject{{Name: "concurrent-1.0.0.pom", Digest: "sha256:concurrent", Size: 1}}}
	}
	type createResult struct {
		session  repository.MavenPublishSession
		replayed bool
		err      error
	}
	results := make(chan createResult, 2)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			session, replayed, createErr := store.CreateMavenPublishSessionIdempotently(ctx, newSession(uuid.NewString()), "admin", "repositories/"+repo.ID+"/publish-sessions", "concurrent-key", "same-payload")
			results <- createResult{session, replayed, createErr}
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	var created []createResult
	for result := range results {
		if result.err != nil {
			t.Fatalf("concurrent session create: %v", result.err)
		}
		created = append(created, result)
	}
	if len(created) != 2 || created[0].session.ID != created[1].session.ID || created[0].replayed == created[1].replayed {
		t.Fatalf("concurrent results=%#v", created)
	}
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.ExecContext(ctx, `UPDATE native_maven_publish_sessions SET state='expired' WHERE id=$1`, created[0].session.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE native_maven_publish_idempotency SET expires_at=now()-interval '1 second' WHERE actor='admin' AND target=$1 AND key='concurrent-key'`, "repositories/"+repo.ID+"/publish-sessions"); err != nil {
		t.Fatal(err)
	}
	fresh, replayed, err := store.CreateMavenPublishSessionIdempotently(ctx, newSession(uuid.NewString()), "admin", "repositories/"+repo.ID+"/publish-sessions", "concurrent-key", "same-payload")
	if err != nil || replayed || fresh.ID == created[0].session.ID {
		t.Fatalf("expired idempotency retry session=%#v replayed=%t err=%v", fresh, replayed, err)
	}

	// Two publishers can stage the same coordinate, but the commit transaction
	// must make exactly one coordinate and its asset path visible.
	commitCoordinate := "org.example:commit-race:1.0.0"
	commitPath := "org/example/commit-race/1.0.0/commit-race-1.0.0.pom"
	commitResults := make(chan error, 2)
	commitStart := make(chan struct{})
	for i := range 2 {
		session := repository.MavenPublishSession{ID: uuid.NewString(), RepositoryID: repo.ID, Coordinate: commitCoordinate, Publisher: fmt.Sprintf("publisher-%d", i), PomObject: "commit-race-1.0.0.pom", State: "open", ExpiresAt: time.Now().Add(time.Hour), Objects: []repository.MavenDeclaredObject{{Name: "commit-race-1.0.0.pom", Digest: fmt.Sprintf("sha256:commit-race-%d", i), Size: 1}}}
		if _, err = store.CreateMavenPublishSession(ctx, session); err != nil {
			t.Fatal(err)
		}
		key := "native/maven/sha256/commit-race-" + uuid.NewString()
		if err = store.MarkMavenPublishObject(ctx, session.ID, session.Objects[0].Name, key); err != nil {
			t.Fatal(err)
		}
		go func(session repository.MavenPublishSession, key string) {
			<-commitStart
			_, commitErr := store.CommitMavenPublishSession(ctx, session.ID, []repository.MavenAsset{{RepositoryID: repo.ID, Path: commitPath, ObjectKey: key, Digest: session.Objects[0].Digest, Size: 1}})
			commitResults <- commitErr
		}(session, key)
	}
	close(commitStart)
	var committed, rejected int
	for range 2 {
		if commitErr := <-commitResults; commitErr == nil {
			committed++
		} else if commitErr == repository.ErrNameExists {
			rejected++
		} else {
			t.Fatalf("concurrent commit error=%v", commitErr)
		}
	}
	if committed != 1 || rejected != 1 {
		t.Fatalf("concurrent commit outcomes committed=%d rejected=%d", committed, rejected)
	}

	// A bad staged checksum must create neither upload metadata nor bytes. A
	// later retry can replace a lost staged object and commit its references.
	objects := NewMemoryOCIObjectStore()
	handler := newNativeMavenHandler(store, objects, testAuthenticator())
	pom := []byte("<project><groupId>org.example</groupId><artifactId>recovery</artifactId><version>1.0.0</version></project>")
	jar := []byte("recovery jar")
	digest := func(body []byte) string { sum := sha256.Sum256(body); return "sha256:" + fmt.Sprintf("%x", sum) }
	session := repository.MavenPublishSession{ID: uuid.NewString(), RepositoryID: repo.ID, Coordinate: "org.example:recovery:1.0.0", Publisher: "admin", PomObject: "recovery-1.0.0.pom", State: "open", ExpiresAt: time.Now().Add(time.Hour), Objects: []repository.MavenDeclaredObject{{Name: "recovery-1.0.0.pom", Digest: digest(pom), Size: int64(len(pom))}, {Name: "recovery-1.0.0.jar", Digest: digest(jar), Size: int64(len(jar))}}}
	if _, err = store.CreateMavenPublishSession(ctx, session); err != nil {
		t.Fatal(err)
	}
	upload := func(name string, body []byte) int {
		r := httptest.NewRequest(http.MethodPut, "/api/v2/publish-sessions/"+session.ID+"/objects/"+name, strings.NewReader(string(body)))
		r.Header.Set("Authorization", "Bearer admin-secret")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w.Code
	}
	if status := upload("recovery-1.0.0.jar", []byte("wrong bytes")); status != http.StatusUnprocessableEntity {
		t.Fatalf("bad checksum status=%d", status)
	}
	var uploads int
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM native_maven_publish_uploads WHERE session_id=$1`, session.ID).Scan(&uploads); err != nil || uploads != 0 {
		t.Fatalf("bad checksum uploads=%d err=%v", uploads, err)
	}
	if status := upload("recovery-1.0.0.pom", pom); status != http.StatusNoContent {
		t.Fatalf("stage pom=%d", status)
	}
	if status := upload("recovery-1.0.0.jar", jar); status != http.StatusNoContent {
		t.Fatalf("stage jar=%d", status)
	}
	jarKey := "native/maven/sha256/" + strings.TrimPrefix(digest(jar), "sha256:")
	if err = objects.Delete(ctx, jarKey); err != nil {
		t.Fatal(err)
	}
	commit := func() int {
		r := httptest.NewRequest(http.MethodPost, "/api/v2/publish-sessions/"+session.ID+":commit", nil)
		r.Header.Set("Authorization", "Bearer admin-secret")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w.Code
	}
	if status := commit(); status != http.StatusUnprocessableEntity {
		t.Fatalf("lost staged object commit=%d", status)
	}
	if status := upload("recovery-1.0.0.jar", jar); status != http.StatusNoContent {
		t.Fatalf("recovery stage jar=%d", status)
	}
	if status := commit(); status != http.StatusOK {
		t.Fatalf("recovered commit=%d", status)
	}
	var assets, references int
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM native_maven_assets WHERE repository_id=$1`, repo.ID).Scan(&assets); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM native_maven_object_references WHERE repository_id=$1`, repo.ID).Scan(&references); err != nil || assets == 0 || references != assets {
		t.Fatalf("asset/reference counts assets=%d references=%d err=%v", assets, references, err)
	}

	// After the 24-hour grace period, claims skip a row locked by a commit and
	// retain an already claimed intent when a reference appears before deletion.
	stale := repository.MavenPublishSession{ID: uuid.NewString(), RepositoryID: repo.ID, Coordinate: "org.example:stale:1.0.0", Publisher: "collector", PomObject: "stale-1.0.0.pom", State: "open", ExpiresAt: time.Now().Add(-25 * time.Hour), Objects: []repository.MavenDeclaredObject{{Name: "stale-1.0.0.pom", Digest: "sha256:stale", Size: 1}}}
	if _, err = store.CreateMavenPublishSession(ctx, stale); err != nil {
		t.Fatal(err)
	}
	staleKey := "native/maven/sha256/stale-" + uuid.NewString()
	if err = store.MarkMavenPublishObject(ctx, stale.ID, stale.Objects[0].Name, staleKey); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE native_maven_object_intents SET created_at=now()-interval '25 hours' WHERE object_key=$1`, staleKey); err != nil {
		t.Fatal(err)
	}
	lock, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = lock.ExecContext(ctx, `SELECT 1 FROM native_maven_publish_sessions WHERE id=$1 FOR UPDATE`, stale.ID); err != nil {
		lock.Rollback()
		t.Fatal(err)
	}
	claimed, err := store.ClaimExpiredMavenObjectIntents(ctx, time.Now().Add(-24*time.Hour), 10)
	if err != nil || len(claimed) != 0 {
		lock.Rollback()
		t.Fatalf("locked stale claim=%#v err=%v", claimed, err)
	}
	if err = lock.Rollback(); err != nil {
		t.Fatal(err)
	}
	claimed, err = store.ClaimExpiredMavenObjectIntents(ctx, time.Now().Add(-24*time.Hour), 10)
	if err != nil || len(claimed) != 1 || claimed[0].ObjectKey != staleKey {
		t.Fatalf("grace-period claim=%#v err=%v", claimed, err)
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO native_maven_object_references (object_key,repository_id) VALUES ($1,$2)`, staleKey, repo.ID); err != nil {
		t.Fatal(err)
	}
	if err = store.DeleteClaimedMavenObjectIntent(ctx, staleKey, claimed[0].ClaimToken); err != repository.ErrNotFound {
		t.Fatalf("reference recheck delete=%v", err)
	}
	var retained int
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM native_maven_object_intents WHERE object_key=$1`, staleKey).Scan(&retained); err != nil || retained != 1 {
		t.Fatalf("referenced intent retained=%d err=%v", retained, err)
	}
}

func TestPostgresNativeMavenPromotionFailureLeavesS3ObjectsUnpublished(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	s3Endpoint := os.Getenv("TEST_RUSTFS_ENDPOINT")
	s3AccessKey := os.Getenv("TEST_RUSTFS_ACCESS_KEY")
	s3SecretKey := os.Getenv("TEST_RUSTFS_SECRET_KEY")
	if databaseURL == "" || s3Endpoint == "" || s3AccessKey == "" || s3SecretKey == "" {
		t.Skip("PostgreSQL and S3 integration environment is required")
	}
	ctx := context.Background()
	store, err := repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	objects, err := NewRustFSOCIObjectStore(s3Endpoint, s3AccessKey, s3SecretKey, "native-maven-promotion-"+strings.ReplaceAll(uuid.NewString(), "-", "")[:20])
	if err != nil {
		t.Fatal(err)
	}
	if err = objects.EnsureBucket(ctx); err != nil {
		t.Fatal(err)
	}
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{
		ID:                     uuid.NewString(),
		Name:                   "promotion-retry-" + uuid.NewString(),
		Format:                 repository.FormatMaven,
		MavenStrictPublication: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := newNativeMavenHandler(store, objects, Authenticator{ResolverToken: "resolver-secret", RepositoryWriters: map[string][]string{"maven": {repo.Name}}})
	request := func(method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.SetBasicAuth("maven", "resolver-secret")
		if method == http.MethodPost {
			r.Header.Set("Idempotency-Key", "postgres-promotion-retry")
			r.Header.Set("Content-Type", "application/json")
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	const version = "1.0.0"
	pom := "<project><groupId>org.example</groupId><artifactId>promotion-retry</artifactId><version>1.0.0</version></project>"
	jar := "postgres-backed jar"
	for name, body := range map[string]string{"promotion-retry-1.0.0.pom": pom, "promotion-retry-1.0.0.jar": jar} {
		if response := request(http.MethodPut, "/repository/maven/"+repo.Name+"/org/example/promotion-retry/"+version+"/"+name, body); response.Code != http.StatusCreated {
			t.Fatalf("stage %s = %d %s", name, response.Code, response.Body.String())
		}
	}
	jarSum := sha256.Sum256([]byte(jar))
	jarKey := "native/maven/sha256/" + fmt.Sprintf("%x", jarSum[:])
	if _, err = objects.Stat(ctx, jarKey); err != nil {
		t.Fatalf("S3 object must exist before PostgreSQL promotion: %v", err)
	}

	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	failpoint := "native_maven_promotion_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	trigger := failpoint + "_trigger"
	if _, err = db.ExecContext(ctx, fmt.Sprintf(`CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected native Maven promotion failure'; END; $$`, failpoint)); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, fmt.Sprintf(`CREATE TRIGGER %s BEFORE INSERT ON native_maven_assets FOR EACH ROW EXECUTE FUNCTION %s()`, trigger, failpoint)); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = db.ExecContext(ctx, fmt.Sprintf(`DROP TRIGGER IF EXISTS %s ON native_maven_assets`, trigger))
		_, _ = db.ExecContext(ctx, fmt.Sprintf(`DROP FUNCTION IF EXISTS %s()`, failpoint))
	}()

	commitPath := "/repository/maven/" + repo.Name + "/coordinates/org.example:promotion-retry:" + version + ":commit"
	commitBody := `{"expectedAssetNames":["promotion-retry-1.0.0.pom","promotion-retry-1.0.0.jar"]}`
	if response := request(http.MethodPost, commitPath, commitBody); response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("injected PostgreSQL promotion failure = %d %s", response.Code, response.Body.String())
	}
	for _, path := range []string{
		"org/example/promotion-retry/1.0.0/promotion-retry-1.0.0.pom",
		"org/example/promotion-retry/1.0.0/promotion-retry-1.0.0.jar",
		"org/example/promotion-retry/1.0.0/promotion-retry-1.0.0.jar.sha256",
		"org/example/promotion-retry/maven-metadata.xml",
	} {
		if response := request(http.MethodGet, "/repository/maven/"+repo.Name+"/"+path, ""); response.Code != http.StatusNotFound {
			t.Fatalf("failed promotion read %s = %d, want 404", path, response.Code)
		}
	}
	var assets, references int
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM native_maven_assets WHERE repository_id=$1`, repo.ID).Scan(&assets); err != nil || assets != 0 {
		t.Fatalf("rolled back PostgreSQL assets=%d err=%v", assets, err)
	}
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM native_maven_object_references WHERE repository_id=$1`, repo.ID).Scan(&references); err != nil || references != 0 {
		t.Fatalf("rolled back PostgreSQL references=%d err=%v", references, err)
	}
	if _, err = db.ExecContext(ctx, fmt.Sprintf(`DROP TRIGGER %s ON native_maven_assets`, trigger)); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, fmt.Sprintf(`DROP FUNCTION %s()`, failpoint)); err != nil {
		t.Fatal(err)
	}
	if response := request(http.MethodPost, commitPath, commitBody); response.Code != http.StatusOK {
		t.Fatalf("commit retry after PostgreSQL recovery = %d %s", response.Code, response.Body.String())
	}
	for _, path := range []string{
		"org/example/promotion-retry/1.0.0/promotion-retry-1.0.0.pom",
		"org/example/promotion-retry/1.0.0/promotion-retry-1.0.0.jar",
		"org/example/promotion-retry/1.0.0/promotion-retry-1.0.0.jar.sha256",
		"org/example/promotion-retry/maven-metadata.xml",
	} {
		if response := request(http.MethodGet, "/repository/maven/"+repo.Name+"/"+path, ""); response.Code != http.StatusOK {
			t.Fatalf("retried promotion read %s = %d, want 200", path, response.Code)
		}
	}
	differentKey := httptest.NewRequest(http.MethodPost, commitPath, strings.NewReader(commitBody))
	differentKey.SetBasicAuth("maven", "resolver-secret")
	differentKey.Header.Set("Idempotency-Key", "different-postgres-promotion-retry")
	differentKeyResponse := httptest.NewRecorder()
	handler.ServeHTTP(differentKeyResponse, differentKey)
	if differentKeyResponse.Code != http.StatusConflict || !strings.Contains(differentKeyResponse.Body.String(), "idempotency_conflict") {
		t.Fatalf("different commit key=%d %s", differentKeyResponse.Code, differentKeyResponse.Body.String())
	}
}

func TestPostgresMavenRetentionTombstonesExpiredExcessVersions(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is required for PostgreSQL integration tests")
	}
	ctx := context.Background()
	store, err := repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "retention-pg-" + uuid.NewString(), Format: repository.FormatMaven})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.ReplaceRepositoryRetentionPolicy(ctx, repo.ID, repository.RepositoryRetentionPolicy{Enabled: true, KeepDays: 1, MinimumVersions: 1}, "1"); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	ids := []string{uuid.NewString(), uuid.NewString(), uuid.NewString()}
	for index, id := range ids {
		createdAt := time.Now().UTC().Add(-time.Duration(72-index*24) * time.Hour)
		coordinate := fmt.Sprintf("org.example:retained:%d.0.0", index+1)
		if _, err = db.ExecContext(ctx, `INSERT INTO native_maven_artifacts (id,repository_id,coordinate,digest,state,created_at) VALUES ($1,$2,$3,$4,'visible',$5)`, id, repo.ID, coordinate, "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", createdAt); err != nil {
			t.Fatal(err)
		}
	}
	if err = (NativeMavenRetention{Store: store, Now: func() time.Time { return time.Now().UTC() }}).Collect(ctx); err != nil {
		t.Fatal(err)
	}
	visible, err := store.ListMavenArtifacts(ctx, repo.ID)
	if err != nil || len(visible) != 1 || visible[0].ID != ids[2] {
		t.Fatalf("visible artifacts=%#v err=%v", visible, err)
	}
	for _, id := range ids[:2] {
		artifact, getErr := store.GetMavenArtifact(ctx, repo.ID, id)
		if getErr != nil || artifact.State != "deleted" {
			t.Fatalf("artifact=%#v err=%v", artifact, getErr)
		}
		tombstone, getErr := store.GetArtifactTombstone(ctx, repo.ID, repository.FormatMaven, artifact.Coordinate)
		if getErr != nil || tombstone.Digest != artifact.Digest || tombstone.TombstonedAt.IsZero() {
			t.Fatalf("tombstone=%#v err=%v", tombstone, getErr)
		}
	}
}
