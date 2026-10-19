//go:build integration

package app

import (
	"context"
	"errors"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/ccsert/artifact-gateway/internal/objectstore"
	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

type npmPostgresCollectionMarkFailure struct{ *repository.PostgresStore }

func (s npmPostgresCollectionMarkFailure) MarkNPMObjectCollected(context.Context, string) error {
	return errors.New("fixture final metadata update unavailable")
}

func TestPostgresRustFSNPMReclaimFailureSurvivesRestart(t *testing.T) {
	databaseURL, endpoint := os.Getenv("TEST_DATABASE_URL"), os.Getenv("TEST_RUSTFS_ENDPOINT")
	access, secret := os.Getenv("TEST_RUSTFS_ACCESS_KEY"), os.Getenv("TEST_RUSTFS_SECRET_KEY")
	if databaseURL == "" || endpoint == "" || access == "" || secret == "" {
		t.Skip("PostgreSQL and S3 integration environment is required")
	}
	ctx := context.Background()
	store, err := repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	bucket := "npm-reclaim-" + uuid.NewString()
	objects, err := objectstore.NewRustFSStore(endpoint, access, secret, bucket)
	if err != nil {
		t.Fatal(err)
	}
	if err = objects.EnsureBucket(ctx); err != nil {
		t.Fatal(err)
	}
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "npm-reclaim-" + uuid.NewString(), Format: repository.FormatNPM})
	if err != nil {
		t.Fatal(err)
	}
	handler := newTestGateway(t, GatewayOptions{Store: store, Adapter: TestAdapter{}, Authenticator: testAuthenticator(), Storage: ObjectStores{repository.FormatNPM: objects}})
	publishNPMDistTagHTTPFixture(t, handler, repo.Name, "widget", "1.0.0", map[string]string{"latest": "1.0.0"})
	version, err := store.GetNPMVersion(ctx, repo.ID, "widget", "1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	path := "/npm/" + repo.Name + "/widget"
	document := npmMetadataDocument(t, handler, path)
	if response := npmDistTagRequest(t, handler, http.MethodDelete, path+"/-rev/"+document["_rev"].(string), "admin-secret", ""); response.Code != 200 {
		t.Fatalf("unpublish=%d %s", response.Code, response.Body.String())
	}
	maintenance := NativeNPMMaintenance{Store: npmPostgresCollectionMarkFailure{store}, Objects: objects, Now: func() time.Time { return time.Now().Add(25 * time.Hour) }}
	if err = maintenance.Collect(ctx); err == nil {
		t.Fatal("final collection update failure was not injected")
	}
	if _, err = objects.Get(ctx, version.ObjectKey); !errors.Is(err, objectstore.ErrNotFound) {
		t.Fatalf("S3 delete did not complete: %v", err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	objects, err = objectstore.NewRustFSStore(endpoint, access, secret, bucket)
	if err != nil {
		t.Fatal(err)
	}
	pkg, err := store.GetNPMPackage(ctx, repo.ID, "widget")
	if err != nil || len(pkg.TombstonedVersions) != 1 || pkg.TombstonedVersions[0].CollectingAt.IsZero() || !pkg.TombstonedVersions[0].CollectedAt.IsZero() {
		t.Fatalf("restart lost partial-collection fence: %#v %v", pkg, err)
	}
	handler = newTestGateway(t, GatewayOptions{Store: store, Adapter: TestAdapter{}, Authenticator: testAuthenticator(), Storage: ObjectStores{repository.FormatNPM: objects}})
	restore := npmDistTagRequest(t, handler, http.MethodPost, "/api/v2/repositories/"+repo.ID+"/restore", "admin-secret", `{"coordinate":"widget@1.0.0"}`)
	if restore.Code != http.StatusConflict {
		t.Fatalf("restart restored missing bytes: %d %s", restore.Code, restore.Body.String())
	}
	jobs, err := store.ListLifecycleJobs(ctx, repo.ID, 10)
	if err != nil || len(jobs) != 1 {
		t.Fatalf("job inventory=%#v %v", jobs, err)
	}
	if _, err = store.CancelLifecycleJob(ctx, repo.ID, jobs[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err = store.RetryLifecycleJob(ctx, repo.ID, jobs[0].ID); err != nil {
		t.Fatal(err)
	}
	maintenance.Store, maintenance.Objects = store, objects
	if err = maintenance.RunReclaimJobs(ctx, 10); err != nil {
		t.Fatal(err)
	}
	pkg, err = store.GetNPMPackage(ctx, repo.ID, "widget")
	if err != nil || pkg.TombstonedVersions[0].CollectedAt.IsZero() || !pkg.TombstonedVersions[0].CollectingAt.IsZero() || pkg.TombstonedVersions[0].Digest != version.Digest {
		t.Fatalf("retry did not finish fenced collection: %#v %v", pkg, err)
	}
	if response := npmDistTagRequest(t, handler, http.MethodGet, path+"/1.0.0", "resolver-secret", ""); response.Code != 404 {
		t.Fatalf("collected version visible=%d", response.Code)
	}
}
