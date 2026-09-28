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
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/objectstore"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

func TestPostgresRustFSCargoTombstoneRestoreAndReclaim(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	endpoint := os.Getenv("TEST_RUSTFS_ENDPOINT")
	accessKey := os.Getenv("TEST_RUSTFS_ACCESS_KEY")
	secretKey := os.Getenv("TEST_RUSTFS_SECRET_KEY")
	if databaseURL == "" || endpoint == "" || accessKey == "" || secretKey == "" {
		t.Skip("PostgreSQL and RustFS integration environment is required")
	}
	ctx := context.Background()
	storeA, err := repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = storeA.Close() }()
	storeB, err := repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = storeB.Close() }()
	bucket := "cargo-life-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:20]
	objectsA, err := NewRustFSOCIObjectStore(endpoint, accessKey, secretKey, bucket)
	if err != nil {
		t.Fatal(err)
	}
	if err := objectsA.EnsureBucket(ctx); err != nil {
		t.Fatal(err)
	}
	objectsB, err := NewRustFSOCIObjectStore(endpoint, accessKey, secretKey, bucket)
	if err != nil {
		t.Fatal(err)
	}
	repo, err := storeA.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "cargo-life-" + uuid.NewString()[:8], Format: repository.FormatCargo})
	if err != nil {
		t.Fatal(err)
	}
	handlerA := NewGatewayHandler(Dependencies{NativeCargoObjectStore: objectsA}, storeA, TestAdapter{}, testAuthenticator())
	handlerB := NewGatewayHandler(Dependencies{NativeCargoObjectStore: objectsB}, storeB, TestAdapter{}, testAuthenticator())
	request := func(handler http.Handler, method, path string, body []byte) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, bytes.NewReader(body))
		r.Host = "localhost:8080"
		authorize(r, "admin-secret")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	crateName := "life" + strings.ReplaceAll(uuid.NewString(), "-", "")[:8]
	payload, archive := cargoC0PublishFixture(t, crateName, "1.0.0", crateName)
	if w := request(handlerA, http.MethodPut, "/cargo/"+repo.Name+"/api/v1/crates/new", payload); w.Code != http.StatusOK {
		t.Fatalf("publish=%d %s", w.Code, w.Body.String())
	}
	publication, err := storeB.GetCargoPublication(ctx, repo.ID, crateName, "1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	management := "/api/v2/repositories/" + repo.ID
	coordinate := []byte(`{"coordinate":"` + crateName + `@1.0.0"}`)
	if w := request(handlerA, http.MethodPost, management+"/tombstones", coordinate); w.Code != http.StatusNoContent {
		t.Fatalf("tombstone=%d %s", w.Code, w.Body.String())
	}
	download := "/cargo/" + repo.Name + "/api/v1/crates/" + crateName + "/1.0.0/download"
	if w := request(handlerB, http.MethodGet, download, nil); w.Code != http.StatusNotFound {
		t.Fatalf("cross-instance tombstone read=%d %s", w.Code, w.Body.String())
	}
	if w := request(handlerB, http.MethodPost, management+"/restore", coordinate); w.Code != http.StatusNoContent {
		t.Fatalf("cross-instance restore=%d %s", w.Code, w.Body.String())
	}
	if w := request(handlerA, http.MethodGet, download, nil); w.Code != http.StatusOK || !bytes.Equal(w.Body.Bytes(), archive) {
		t.Fatalf("restored download=%d %s", w.Code, w.Body.String())
	}
	if w := request(handlerB, http.MethodPost, management+"/tombstones", coordinate); w.Code != http.StatusNoContent {
		t.Fatalf("second tombstone=%d %s", w.Code, w.Body.String())
	}
	if visible, err := storeB.CargoObjectHasVisibleReference(ctx, publication.ObjectKey); err != nil || visible {
		t.Fatalf("tombstoned Cargo object still has a visible reference=%t err=%v", visible, err)
	}
	maintenance := NativeCargoMaintenance{Store: storeB, Objects: objectsB, RecoveryWindow: time.Hour,
		Now: func() time.Time { return time.Now().UTC().Add(2 * time.Hour) }}
	if err := maintenance.Collect(ctx); err != nil {
		t.Fatal(err)
	}
	if err := maintenance.RunReclaimJobs(ctx, 20); err != nil {
		t.Fatal(err)
	}
	if _, err := objectsA.Stat(ctx, publication.ObjectKey); !errors.Is(err, objectstore.ErrNotFound) {
		jobs, _ := storeB.ListLifecycleJobs(ctx, repo.ID, 20)
		t.Fatalf("tombstoned crate bytes remain: %v jobs=%+v", err, jobs)
	}
	if w := request(handlerA, http.MethodPost, management+"/restore", coordinate); w.Code != http.StatusConflict {
		t.Fatalf("reclaim allowed restore=%d %s", w.Code, w.Body.String())
	}
	capacity, err := storeA.GetRepositoryCapacity(ctx, repo.ID)
	if err != nil || capacity.UsedBytes != 0 || capacity.ObjectCount != 0 {
		t.Fatalf("reclaim capacity=%+v err=%v", capacity, err)
	}
}
