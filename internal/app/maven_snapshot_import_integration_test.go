//go:build integration

package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	mavenprotocol "github.com/artifact-gateway/artifact-gateway/internal/protocol/maven"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/artifact-gateway/artifact-gateway/internal/snapshotimport"
	testsupport "github.com/artifact-gateway/artifact-gateway/internal/testsupport/snapshotfixture"
	"github.com/google/uuid"
)

func TestPostgresRustFSArchetypeSnapshotImportCLIExplicitTarget(t *testing.T) {
	if os.Getenv("TEST_DATABASE_URL") == "" || os.Getenv("TEST_RUSTFS_ENDPOINT") == "" {
		t.Skip("isolated PostgreSQL/RustFS required")
	}
	ctx := context.Background()
	store, err := repository.NewPostgresStore(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "snapshot-cli-" + uuid.NewString(), Format: repository.FormatMaven})
	if err != nil {
		t.Fatal(err)
	}
	bucket := "snapshot-cli-" + uuid.NewString()
	objects, err := NewRustFSOCIObjectStore(os.Getenv("TEST_RUSTFS_ENDPOINT"), os.Getenv("TEST_RUSTFS_ACCESS_KEY"), os.Getenv("TEST_RUSTFS_SECRET_KEY"), bucket)
	if err != nil {
		t.Fatal(err)
	}
	if err = objects.EnsureBucket(ctx); err != nil {
		t.Fatal(err)
	}
	dir, digest, _, _ := testsupport.SnapshotArchetypeBundle(t)
	p, rejected, err := snapshotimport.Prepare(ctx, dir, digest)
	if err != nil {
		t.Fatalf("%v %v", rejected, err)
	}
	defer func() { _ = p.Close() }()
	spec := snapshotimport.TargetSpec{TargetID: "synthetic-target", Actor: "synthetic-operator", RepositoryID: repo.ID, DatabaseURLEnv: "TEST_DATABASE_URL", S3Endpoint: os.Getenv("TEST_RUSTFS_ENDPOINT"), S3Bucket: bucket, S3AccessKeyEnv: "TEST_RUSTFS_ACCESS_KEY", S3SecretKeyEnv: "TEST_RUSTFS_SECRET_KEY"}
	specPath := filepath.Join(t.TempDir(), "target.json")
	b, _ := json.Marshal(spec)
	if err = os.WriteFile(specPath, b, 0600); err != nil {
		t.Fatal(err)
	}
	run := func(action string, extra ...string) (snapshotimport.Report, int) {
		t.Helper()
		args := []string{action, "--bundle", dir, "--manifest-sha256", digest, "--spec", specPath}
		args = append(args, extra...)
		var out, stderr bytes.Buffer
		code := snapshotimport.RunCLI(ctx, args, &out, &stderr)
		var report snapshotimport.Report
		if out.Len() > 0 {
			if err := json.Unmarshal(out.Bytes(), &report); err != nil {
				t.Fatal(err)
			}
		}
		return report, code
	}
	dry, code := run("dry-run")
	if code != 0 || dry.Status != "ready" || len(dry.CapacityReferences) == 0 || dry.TargetBinding == "" {
		t.Fatalf("dry=%v exit=%d", dry, code)
	}
	if _, err = store.GetMavenSnapshotImport(ctx, repo.ID, p.Plans[0].Coordinate); !errors.Is(err, repository.ErrNotFound) {
		t.Fatal("dry-run reserved coordinate")
	}
	keys, err := objects.List(ctx, "")
	if err != nil || len(keys) != 0 {
		t.Fatal("dry-run wrote bucket")
	}
	cap := testsupport.SnapshotCapacity(t, p, repo.ID, spec.TargetID, spec.Actor)
	cap.TargetID = dry.TargetBinding
	cap.Snapshot.TargetID = dry.TargetBinding
	capPath := filepath.Join(t.TempDir(), "capacity.json")
	b, _ = json.Marshal(cap)
	if err = os.WriteFile(capPath, b, 0600); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		report, code := run("apply", "--capacity-plan", capPath)
		if code != 0 || report.Status != "verified" {
			t.Fatalf("apply=%v exit=%d", report, code)
		}
	}
	items, err := store.ListMavenArtifacts(ctx, repo.ID)
	if err != nil || len(items) != 3 {
		t.Fatalf("%v %v", items, err)
	}
	audits, err := store.ListAudits(ctx, repository.AuditQuery{Repository: repo.Name, Operation: "maven.snapshot.import", Limit: 100})
	if err != nil || len(audits) != 1 {
		t.Fatalf("audit=%v %v", audits, err)
	}
	// A changed physical bucket is rejected even with the same human targetId.
	spec.S3Bucket = "snapshot-other-" + uuid.NewString()
	other, err := NewRustFSOCIObjectStore(spec.S3Endpoint, os.Getenv(spec.S3AccessKeyEnv), os.Getenv(spec.S3SecretKeyEnv), spec.S3Bucket)
	if err != nil {
		t.Fatal(err)
	}
	if err = other.EnsureBucket(ctx); err != nil {
		t.Fatal(err)
	}
	b, _ = json.Marshal(spec)
	if err = os.WriteFile(specPath, b, 0600); err != nil {
		t.Fatal(err)
	}
	if report, code := run("dry-run"); code != 1 || report.Status != "rejected" {
		t.Fatalf("changed target=%v exit=%d", report, code)
	}
}

func TestPostgresSnapshotImportExpiredClaimAndNewProcessRecovery(t *testing.T) {
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL required")
	}
	ctx := context.Background()
	store, err := repository.NewPostgresStore(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	db, err := sql.Open("pgx", os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "snapshot-recovery-" + uuid.NewString(), Format: repository.FormatMaven})
	if err != nil {
		t.Fatal(err)
	}
	dir, _, m, _ := testsupport.SnapshotBundle(t)
	f := &m.Coordinates[0].Builds[0].Files[1]
	body := []byte("unique interrupted bytes " + uuid.NewString())
	sum := sha256.Sum256(body)
	f.Digest = "sha256:" + hex.EncodeToString(sum[:])
	f.Size = int64(len(body))
	if err = os.WriteFile(filepath.Join(dir, f.Path), body, 0600); err != nil {
		t.Fatal(err)
	}
	digest := testsupport.WriteSnapshotManifest(t, dir, m)
	p, rejected, err := snapshotimport.Prepare(ctx, dir, digest)
	if err != nil {
		t.Fatalf("%v %v", rejected, err)
	}
	defer func() { _ = p.Close() }()
	plans, err := p.References(repo.ID, "synthetic-target", "synthetic-operator", testsupport.SnapshotTargetBinding)
	if err != nil {
		t.Fatal(err)
	}
	checkpoint, err := store.BeginMavenSnapshotImport(ctx, plans[0])
	if err != nil {
		t.Fatal(err)
	}
	objects := NewMemoryOCIObjectStore()
	var asset repository.MavenAsset
	for _, a := range plans[0].Assets {
		if a.Path == f.Path {
			asset = a
		}
	}
	name := strings.TrimPrefix(asset.Path, "org/example/widget/1.0-SNAPSHOT/")
	if err = store.MarkMavenPublishObject(ctx, checkpoint.SessionID, name, asset.ObjectKey); err != nil {
		t.Fatal(err)
	}
	if err = objects.Put(ctx, asset.ObjectKey, body); err != nil {
		t.Fatal(err)
	}
	// Ordinary root metadata completion must leave this operator session open.
	if err = store.CompleteMavenProtocolPublications(ctx, repo.ID, "synthetic-operator", "org/example/widget/", true); err != nil {
		t.Fatal(err)
	}
	session, err := store.GetMavenPublishSession(ctx, checkpoint.SessionID)
	if err != nil || session.State != "open" {
		t.Fatal("ordinary metadata completed import session")
	}
	if _, err = db.ExecContext(ctx, `UPDATE native_maven_publish_sessions SET state='expired',expires_at=now()-interval '1 day' WHERE id=$1`, checkpoint.SessionID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE native_maven_object_intents SET created_at=now()-interval '2 days' WHERE object_key=$1`, asset.ObjectKey); err != nil {
		t.Fatal(err)
	}
	claims, err := store.ClaimExpiredMavenObjectIntents(ctx, time.Now().Add(-24*time.Hour), 100)
	if err != nil {
		t.Fatal(err)
	}
	token := ""
	for _, claim := range claims {
		if claim.ObjectKey == asset.ObjectKey {
			token = claim.ClaimToken
		}
	}
	if token == "" {
		t.Fatal("fixture was not claimed")
	}
	cap := testsupport.SnapshotCapacity(t, p, repo.ID, "synthetic-target", "synthetic-operator")
	if _, err = snapshotimport.Run(ctx, p, store, objects, repo.ID, "synthetic-target", "synthetic-operator", testsupport.SnapshotTargetBinding, true, cap); err == nil {
		t.Fatal("active GC claim admitted")
	}
	items, _ := store.ListMavenArtifacts(ctx, repo.ID)
	if len(items) != 0 {
		t.Fatal("active claim exposed partial history")
	}
	if _, err = db.ExecContext(ctx, `UPDATE native_maven_object_intents SET claimed_at=now()-interval '6 minutes' WHERE object_key=$1`, asset.ObjectKey); err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]string{"format": "maven", "objectKey": asset.ObjectKey, "claimToken": token})
	if _, _, err = store.EnqueueLifecycleJob(ctx, repository.LifecycleJob{ID: uuid.NewString(), RepositoryID: repo.ID, Kind: repository.LifecycleJobReclaim, IdempotencyKey: "synthetic-stale-" + uuid.NewString(), Payload: payload}); err != nil {
		t.Fatal(err)
	}
	other, err := repository.NewPostgresStore(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if report, err := snapshotimport.Run(ctx, p, other, objects, repo.ID, "synthetic-target", "recovery-operator", testsupport.SnapshotTargetBinding, true, cap); err != nil || report.Status != "verified" {
		t.Fatalf("recover=%v %v", report, err)
	}
	worker := mavenprotocol.NativeMaintenance{Store: other, Objects: objects}
	if err = worker.RunReclaimJobs(ctx, 100); err != nil {
		t.Fatal(err)
	}
	got, err := objects.Get(ctx, asset.ObjectKey)
	if err != nil || !bytes.Equal(got, body) {
		t.Fatal("stale GC job deleted restored object")
	}
	items, err = other.ListMavenArtifacts(ctx, repo.ID)
	if err != nil || len(items) != 3 {
		t.Fatal("restart duplicated or lost history")
	}
}

func TestPostgresSnapshotImportOneConnectionPoolsAndSourceBuildBoundary(t *testing.T) {
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	open := func() *sql.DB {
		t.Helper()
		db, err := sql.Open("pgx", os.Getenv("TEST_DATABASE_URL"))
		if err != nil {
			t.Fatal(err)
		}
		db.SetMaxOpenConns(1)
		db.SetMaxIdleConns(1)
		t.Cleanup(func() { _ = db.Close() })
		return db
	}
	db, listener, locks := open(), open(), open()
	store, err := repository.NewPostgresStoreWithPools(db, listener, locks)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "snapshot-small-pool-" + uuid.NewString(), Format: repository.FormatMaven})
	if err != nil {
		t.Fatal(err)
	}
	dir, _, m, _ := testsupport.SnapshotBundleWithBuilds(t, []testsupport.BuildIdentity{{Timestamp: "20260101.000000", Number: 1}, {Timestamp: "20260101.000000", Number: 10}, {Timestamp: "20260101.000000", Number: 70}})
	for i := range m.Coordinates[0].Builds {
		f := &m.Coordinates[0].Builds[i].Files[1]
		b := []byte("unique source build " + uuid.NewString())
		sum := sha256.Sum256(b)
		f.Digest = "sha256:" + hex.EncodeToString(sum[:])
		f.Size = int64(len(b))
		if err = os.WriteFile(filepath.Join(dir, f.Path), b, 0600); err != nil {
			t.Fatal(err)
		}
	}
	digest := testsupport.WriteSnapshotManifest(t, dir, m)
	p, rejected, err := snapshotimport.Prepare(ctx, dir, digest)
	if err != nil {
		t.Fatalf("%v %v", rejected, err)
	}
	defer func() { _ = p.Close() }()
	objects := NewMemoryOCIObjectStore()
	report, err := snapshotimport.Run(ctx, p, store, objects, repo.ID, "synthetic-target", "synthetic-operator", testsupport.SnapshotTargetBinding, true, testsupport.SnapshotCapacity(t, p, repo.ID, "synthetic-target", "synthetic-operator"))
	if err != nil || report.Status != "verified" {
		t.Fatalf("one-connection apply=%v %v", report, err)
	}
	if locks.Stats().InUse != 0 || db.Stats().InUse != 0 {
		t.Fatal("import leaked connection")
	}
	items, err := store.ListMavenArtifacts(ctx, repo.ID)
	if err != nil || len(items) != 3 {
		t.Fatalf("%v %v", items, err)
	}
	var first, second repository.MavenArtifact
	ids := map[string]bool{}
	for _, a := range items {
		if ids[a.ID] {
			t.Fatal("same POM/timestamp collapsed different source builds")
		}
		ids[a.ID] = true
		if a.SourceBuildNumber == 1 {
			first = a
		}
		if a.SourceBuildNumber == 10 {
			second = a
		}
	}
	for _, a := range []repository.MavenArtifact{first, second} {
		if _, err = store.TombstoneMavenArtifact(ctx, repo.ID, a.ID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = store.RestoreMavenArtifact(ctx, repo.ID, first.ID); err != nil {
		t.Fatal(err)
	}
	key := "native/maven/sha256/" + strings.TrimPrefix(m.Coordinates[0].Builds[1].Files[1].Digest, "sha256:")
	referenced, err := store.MavenObjectIntentHasReference(ctx, key)
	if err != nil || referenced {
		t.Fatal("restore of build 1 captured build 10 reference")
	}
	if _, err = store.GetMavenAsset(ctx, repo.ID, m.Coordinates[0].Builds[1].Files[1].Path); !errors.Is(err, repository.ErrNotFound) {
		t.Fatal("restore of build 1 exposed build 10")
	}
	if _, err = store.RestoreMavenArtifact(ctx, repo.ID, second.ID); err != nil {
		t.Fatal(err)
	}
}

func TestPostgresRustFSMavenSnapshotImportAtomicReplay(t *testing.T) {
	if os.Getenv("TEST_DATABASE_URL") == "" || os.Getenv("TEST_RUSTFS_ENDPOINT") == "" {
		t.Skip("isolated PostgreSQL/RustFS required")
	}
	ctx := context.Background()
	store, err := repository.NewPostgresStore(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "snapshot-import-" + uuid.NewString(), Format: repository.FormatMaven})
	if err != nil {
		t.Fatal(err)
	}
	objects, err := NewRustFSOCIObjectStore(os.Getenv("TEST_RUSTFS_ENDPOINT"), os.Getenv("TEST_RUSTFS_ACCESS_KEY"), os.Getenv("TEST_RUSTFS_SECRET_KEY"), "synthetic-snapshot-"+uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	if err = objects.EnsureBucket(ctx); err != nil {
		t.Fatal(err)
	}
	dir, digest, _, files := testsupport.SnapshotBundle(t)
	p, rejected, err := snapshotimport.Prepare(ctx, dir, digest)
	if err != nil {
		t.Fatalf("%v %v", rejected, err)
	}
	defer func() { _ = p.Close() }()
	cap := testsupport.SnapshotCapacity(t, p, repo.ID, "synthetic-target", "synthetic-operator")
	plans, err := p.References(repo.ID, "synthetic-target", "synthetic-operator", testsupport.SnapshotTargetBinding)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.BeginMavenSnapshotImport(ctx, plans[0]); err != nil {
		t.Fatal(err)
	}
	// A persistence commit with missing stage intents must roll back every
	// artifact/asset and selector, rather than expose a partial history.
	if _, err := store.CommitMavenSnapshotImport(ctx, plans[0]); err == nil {
		t.Fatal("committed unavailable objects")
	}
	histories, _ := store.ListMavenArtifacts(ctx, repo.ID)
	if len(histories) != 0 {
		t.Fatal("partial transaction visible")
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			report, err := snapshotimport.Run(ctx, p, store, objects, repo.ID, "synthetic-target", "synthetic-operator", testsupport.SnapshotTargetBinding, true, cap)
			if err == nil && report.Status != "verified" {
				t.Errorf("unexpected %v", report)
			}
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
	histories, err = store.ListMavenArtifacts(ctx, repo.ID)
	if err != nil || len(histories) != 3 {
		t.Fatalf("histories=%v %v", histories, err)
	}
	auth := Authenticator{ResolverToken: "synthetic-reader", RepositoryReaders: map[string][]string{"synthetic": {repo.Name}}}
	handler := newNativeMavenHandler(store, objects, auth)
	for path, want := range files {
		r := httptest.NewRequest(http.MethodGet, "/repository/maven/"+repo.Name+"/"+path, nil)
		r.SetBasicAuth("synthetic", "synthetic-reader")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != 200 || w.Body.String() != string(want) {
			t.Fatalf("GET %s=%d %q", path, w.Code, w.Body.String())
		}
	}
	first, err := store.SearchMavenArtifacts(ctx, repo.ID, "org.example:", 1, repository.MavenArtifactCursor{})
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.SearchMavenArtifacts(ctx, repo.ID, "org.example:", 1, repository.MavenArtifactCursor{Coordinate: first[0].Coordinate, BuildNumber: first[0].BuildNumber})
	if err != nil || len(second) != 1 || first[0].ID == second[0].ID {
		t.Fatalf("pagination=%v %v", second, err)
	}
	for _, a := range histories {
		if a.SourceTimestamp == "20260101.000000" {
			t.Cleanup(func() {
				if _, err := store.RestoreMavenArtifact(ctx, repo.ID, a.ID); err != nil {
					t.Error(err)
				}
			})
			if _, err := store.TombstoneMavenArtifact(ctx, repo.ID, a.ID); err != nil {
				t.Fatal(err)
			}
		}
	}
	r := httptest.NewRequest(http.MethodGet, "/repository/maven/"+repo.Name+"/org/example/widget/1.0-SNAPSHOT/maven-metadata.xml", nil)
	r.SetBasicAuth("synthetic", "synthetic-reader")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 404 {
		t.Fatalf("metadata changed after current removal=%d", w.Code)
	}
}
