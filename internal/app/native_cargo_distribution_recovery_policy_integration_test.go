//go:build integration

package app

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/artifact-gateway/artifact-gateway/internal/protocol/cargo"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

func TestPostgresRustFSCargoReplicationRecoveryPreservesIndependentReadPolicies(t *testing.T) {
	databaseURL, endpoint := os.Getenv("TEST_DATABASE_URL"), os.Getenv("TEST_RUSTFS_ENDPOINT")
	accessKey, secretKey := os.Getenv("TEST_RUSTFS_ACCESS_KEY"), os.Getenv("TEST_RUSTFS_SECRET_KEY")
	if databaseURL == "" || endpoint == "" || accessKey == "" || secretKey == "" {
		t.Skip("PostgreSQL and RustFS integration environment is required")
	}
	ctx := context.Background()
	storeA, err := repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	storeB, err := repository.NewPostgresStore(databaseURL)
	if err != nil {
		_ = storeA.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = storeA.Close(); _ = storeB.Close() })
	bucketSuffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:20]
	sourceObjects, err := NewRustFSOCIObjectStore(endpoint, accessKey, secretKey, "cargo-policy-src-"+bucketSuffix)
	if err != nil {
		t.Fatal(err)
	}
	if err := sourceObjects.EnsureBucket(ctx); err != nil {
		t.Fatal(err)
	}
	targetObjects, err := NewRustFSOCIObjectStore(endpoint, accessKey, secretKey, "cargo-policy-dst-"+bucketSuffix)
	if err != nil {
		t.Fatal(err)
	}
	if err := targetObjects.EnsureBucket(ctx); err != nil {
		t.Fatal(err)
	}
	suffix := uuid.NewString()[:8]
	source, err := storeA.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "cargo-policy-src-" + suffix, Format: repository.FormatCargo})
	if err != nil {
		t.Fatal(err)
	}
	target, err := storeA.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "cargo-policy-dst-" + suffix, Format: repository.FormatCargo})
	if err != nil {
		t.Fatal(err)
	}
	for _, repo := range []repository.HostedRepository{source, target} {
		policy, err := storeA.GetRepositoryQuarantineReadPolicy(ctx, repo.ID)
		if err != nil {
			t.Fatal(err)
		}
		policy.Enabled = true
		if _, err := storeA.ReplaceRepositoryQuarantineReadPolicy(ctx, repo.ID, policy, policy.Version); err != nil {
			t.Fatal(err)
		}
	}
	request := func(store *repository.PostgresStore, objects OCIObjectStore, method, path string, body []byte) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, bytes.NewReader(body))
		r.Host = "localhost:8080"
		authorize(r, "admin-secret")
		w := httptest.NewRecorder()
		NewGatewayHandler(Dependencies{NativeCargoObjectStore: objects}, store, TestAdapter{}, testAuthenticator()).ServeHTTP(w, r)
		return w
	}
	crateName := "policy" + strings.ReplaceAll(uuid.NewString(), "-", "")[:8]
	payload, archive := cargoC0PublishFixture(t, crateName, "1.0.0", crateName)
	if w := request(storeA, sourceObjects, http.MethodPut, "/cargo/"+source.Name+"/api/v1/crates/new", payload); w.Code != http.StatusOK {
		t.Fatalf("source publish=%d %s", w.Code, w.Body.String())
	}
	publication, err := storeB.GetCargoPublication(ctx, source.ID, crateName, "1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	indexPath, err := cargo.SparseIndexPath(crateName)
	if err != nil {
		t.Fatal(err)
	}
	checkRead := func(store *repository.PostgresStore, objects OCIObjectStore, repo repository.HostedRepository, status int, indexVisible bool) {
		t.Helper()
		base := "/cargo/" + repo.Name
		index := request(store, objects, http.MethodGet, base+"/"+indexPath, nil)
		if index.Code != http.StatusOK || strings.Contains(index.Body.String(), `"vers":"1.0.0"`) != indexVisible {
			t.Fatalf("%s index=%d body=%s visible=%t", repo.Name, index.Code, index.Body.String(), indexVisible)
		}
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			download := request(store, objects, method, base+"/api/v1/crates/"+crateName+"/1.0.0/download", nil)
			if download.Code != status || (status == http.StatusOK && method == http.MethodGet && !bytes.Equal(download.Body.Bytes(), archive)) {
				t.Fatalf("%s %s download=%d bytes=%d want=%d", repo.Name, method, download.Code, download.Body.Len(), status)
			}
		}
	}
	checkRead(storeA, sourceObjects, source, http.StatusOK, true)
	planInput := repository.ReplicationPlan{
		ID: uuid.NewString(), SourceRepositoryID: source.ID, TargetRepositoryID: target.ID,
		Format: repository.FormatCargo, Coordinate: crateName + "@1.0.0", Digest: publication.Digest,
		IdempotencyKey: "cargo-policy-" + suffix, MaxAttempts: 3,
	}
	checkpoints := []repository.ReplicationCheckpoint{{
		SourceObjectKey: publication.ObjectKey, ObjectKey: publication.ObjectKey,
		Digest: publication.Digest, Size: publication.Size,
	}}
	plan, replayed, err := storeA.CreateReplicationPlan(ctx, planInput, checkpoints)
	if err != nil || replayed {
		t.Fatalf("create plan=%+v replayed=%t err=%v", plan, replayed, err)
	}
	interrupt := &interruptedCargoAdmissionStore{OCIObjectStore: targetObjects}
	if err := (CargoReplication{Store: storeB, Source: sourceObjects, Destination: interrupt, ChunkBytes: 8}).RunJobs(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := storeA.GetCargoPublication(ctx, target.ID, crateName, "1.0.0"); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("target metadata exposed after interrupted admission: %v", err)
	}
	if object, err := targetObjects.Stat(ctx, publication.ObjectKey); err != nil || object.Digest != publication.Digest {
		t.Fatalf("uploaded target object=%+v err=%v", object, err)
	}
	if failed, err := storeA.GetReplicationPlan(ctx, target.ID, plan.ID); err != nil || failed.State != "failed" || failed.NextAttemptAt.IsZero() {
		t.Fatalf("interrupted plan=%+v err=%v", failed, err)
	}
	if w := request(storeB, targetObjects, http.MethodGet, "/cargo/"+target.Name+"/api/v1/crates/"+crateName+"/1.0.0/download", nil); w.Code != http.StatusNotFound {
		t.Fatalf("uncommitted target download=%d %s", w.Code, w.Body.String())
	}
	if err := storeB.Close(); err != nil {
		t.Fatal(err)
	}
	storeB, err = repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	quarantine := func(repo repository.HostedRepository) repository.ArtifactQuarantine {
		t.Helper()
		value, err := storeA.ReplaceArtifactQuarantine(ctx, repository.ArtifactQuarantine{
			RepositoryID: repo.ID, Format: repository.FormatCargo,
			Coordinate: crateName + "@1.0.0", Digest: publication.Digest,
			State:  repository.ArtifactQuarantineStateQuarantined,
			Reason: "recovery policy check", UpdatedBy: "operator",
		}, "0")
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	release := func(value repository.ArtifactQuarantine) {
		t.Helper()
		version := value.Version
		value.State, value.Reason, value.UpdatedBy = repository.ArtifactQuarantineStateReleased, "reviewed", "operator"
		if _, err := storeA.ReplaceArtifactQuarantine(ctx, value, version); err != nil {
			t.Fatal(err)
		}
	}
	sourceQuarantine := quarantine(source)
	checkRead(storeA, sourceObjects, source, http.StatusForbidden, false)
	if err := (CargoReplication{Store: storeB, Source: sourceObjects, Destination: targetObjects}).RunJobs(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if parked, err := storeA.GetReplicationPlan(ctx, target.ID, plan.ID); err != nil || parked.State != "failed" || parked.LastError != repository.ArtifactQuarantinedReason || !parked.NextAttemptAt.IsZero() {
		t.Fatalf("source policy did not park retry=%+v err=%v", parked, err)
	}
	if _, err := storeA.GetCargoPublication(ctx, target.ID, crateName, "1.0.0"); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("target metadata exposed while source quarantined: %v", err)
	}
	release(sourceQuarantine)
	requeued, replayed, err := storeB.CreateReplicationPlan(ctx, planInput, checkpoints)
	if err != nil || !replayed || requeued.State != "pending" {
		t.Fatalf("replay plan=%+v replayed=%t err=%v", requeued, replayed, err)
	}
	if err := (CargoReplication{Store: storeB, Source: sourceObjects, Destination: targetObjects}).RunJobs(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if completed, err := storeA.GetReplicationPlan(ctx, target.ID, plan.ID); err != nil || completed.State != "completed" {
		t.Fatalf("replayed plan=%+v err=%v", completed, err)
	}
	checkRead(storeA, sourceObjects, source, http.StatusOK, true)
	checkRead(storeB, targetObjects, target, http.StatusOK, true)
	targetQuarantine := quarantine(target)
	checkRead(storeA, sourceObjects, source, http.StatusOK, true)
	checkRead(storeB, targetObjects, target, http.StatusForbidden, false)
	release(targetQuarantine)
	checkRead(storeB, targetObjects, target, http.StatusOK, true)
}
