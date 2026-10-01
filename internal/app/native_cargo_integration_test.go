//go:build integration

package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/artifact-gateway/artifact-gateway/internal/objectstore"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

func TestPostgresRustFSCargoHostedInterruptedPublishAndRestart(t *testing.T) {
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
	bucket := "cargo-hosted-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:20]
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
	repo, err := storeA.CreateHostedRepository(ctx, repository.HostedRepository{
		ID: uuid.NewString(), Name: "cargo-integration-" + uuid.NewString()[:8], Format: repository.FormatCargo,
	})
	if err != nil {
		t.Fatal(err)
	}
	body, crate := cargoC0PublishFixture(t, "demo", "1.0.0", "demo")
	inspection, err := inspectCargoPublicationIdentity(ctx, repo.ID, bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := storeA.ReserveCargoIdentity(ctx, inspection.Claim); err != nil {
		t.Fatal(err)
	}
	objectKey := "native/cargo/sha256/" + strings.TrimPrefix(inspection.Claim.Digest, "sha256:")
	if err := objectsA.PutVerifiedReader(ctx, objectKey, bytes.NewReader(crate), int64(len(crate)), inspection.Claim.Digest); err != nil {
		t.Fatal(err)
	}
	if _, err := storeB.GetCargoPublication(ctx, repo.ID, "demo", "1.0.0"); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("uploaded object without commit became visible: %v", err)
	}
	serverA := httptest.NewServer(NewGatewayHandler(Dependencies{NativeCargoObjectStore: objectsA}, storeA, TestAdapter{}, testAuthenticator()))
	defer serverA.Close()
	serverB := httptest.NewServer(NewGatewayHandler(Dependencies{NativeCargoObjectStore: objectsB}, storeB, TestAdapter{}, testAuthenticator()))
	defer func() { serverB.Close() }()
	get := func(server *httptest.Server, path string) *http.Response {
		t.Helper()
		request, reqErr := http.NewRequest(http.MethodGet, server.URL+path, nil)
		if reqErr != nil {
			t.Fatal(reqErr)
		}
		request.Header.Set("Authorization", "resolver-secret")
		response, sendErr := server.Client().Do(request)
		if sendErr != nil {
			t.Fatal(sendErr)
		}
		return response
	}
	indexPath := "/cargo/" + repo.Name + "/de/mo/demo"
	if response := get(serverB, indexPath); response.StatusCode != http.StatusNotFound {
		t.Fatalf("precommit index=%d", response.StatusCode)
	} else {
		_ = response.Body.Close()
	}
	publish, err := http.NewRequest(http.MethodPut, serverA.URL+"/cargo/"+repo.Name+"/api/v1/crates/new", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	publish.Header.Set("Authorization", "admin-secret")
	result, err := serverA.Client().Do(publish)
	if err != nil {
		t.Fatal(err)
	}
	if result.StatusCode != http.StatusOK {
		output, _ := io.ReadAll(result.Body)
		t.Fatalf("retry after staged object=%d: %s", result.StatusCode, output)
	}
	_ = result.Body.Close()
	if err := storeB.Close(); err != nil {
		t.Fatal(err)
	}
	storeB, err = repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	serverB.Close()
	serverB = httptest.NewServer(NewGatewayHandler(Dependencies{NativeCargoObjectStore: objectsB}, storeB, TestAdapter{}, testAuthenticator()))
	index := get(serverB, indexPath)
	indexBody, _ := io.ReadAll(index.Body)
	_ = index.Body.Close()
	if index.StatusCode != http.StatusOK || !bytes.Contains(indexBody, []byte(`"vers":"1.0.0"`)) {
		t.Fatalf("restart index=%d body=%s", index.StatusCode, indexBody)
	}
	download := get(serverB, "/cargo/"+repo.Name+"/api/v1/crates/demo/1.0.0/download")
	returned, _ := io.ReadAll(download.Body)
	_ = download.Body.Close()
	if download.StatusCode != http.StatusOK || !bytes.Equal(returned, crate) {
		t.Fatalf("restart download=%d bytes=%d", download.StatusCode, len(returned))
	}
	yankURL := serverA.URL + "/cargo/" + repo.Name + "/api/v1/crates/demo/1.0.0/yank"
	yank, err := http.NewRequest(http.MethodDelete, yankURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	yank.Header.Set("Authorization", "admin-secret")
	yankResult, err := serverA.Client().Do(yank)
	if err != nil {
		t.Fatal(err)
	}
	_ = yankResult.Body.Close()
	if yankResult.StatusCode != http.StatusOK {
		t.Fatalf("cross-instance yank=%d", yankResult.StatusCode)
	}
	yankedIndex := get(serverB, indexPath)
	yankedBody, _ := io.ReadAll(yankedIndex.Body)
	_ = yankedIndex.Body.Close()
	if yankedIndex.StatusCode != http.StatusOK || !bytes.Contains(yankedBody, []byte(`"yanked":true`)) {
		t.Fatalf("cross-instance yanked index=%d body=%s", yankedIndex.StatusCode, yankedBody)
	}
	yankedDownload := get(serverB, "/cargo/"+repo.Name+"/api/v1/crates/demo/1.0.0/download")
	yankedBytes, _ := io.ReadAll(yankedDownload.Body)
	_ = yankedDownload.Body.Close()
	if yankedDownload.StatusCode != http.StatusOK || !bytes.Equal(yankedBytes, crate) {
		t.Fatalf("cross-instance yanked download=%d bytes=%d", yankedDownload.StatusCode, len(yankedBytes))
	}
	undoURL := serverB.URL + "/cargo/" + repo.Name + "/api/v1/crates/demo/1.0.0/unyank"
	undo, err := http.NewRequest(http.MethodPut, undoURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	undo.Header.Set("Authorization", "admin-secret")
	undoResult, err := serverB.Client().Do(undo)
	if err != nil {
		t.Fatal(err)
	}
	_ = undoResult.Body.Close()
	if undoResult.StatusCode != http.StatusOK {
		t.Fatalf("cross-instance unyank=%d", undoResult.StatusCode)
	}
	if err := (NativeCargoMaintenance{Store: storeB, Objects: objectsB}).RunReclaimJobs(ctx, 10); err != nil {
		t.Fatal(err)
	}
	if _, err := objectsB.Stat(ctx, objectKey); err != nil {
		t.Fatalf("published object was reclaimed: %v", err)
	}
	crashedBody, crashedCrate := cargoC0PublishFixture(t, "demo", "1.1.0", "demo")
	crashed, err := inspectCargoPublicationIdentity(ctx, repo.ID, bytes.NewReader(crashedBody), int64(len(crashedBody)))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := storeA.ReserveCargoIdentity(ctx, crashed.Claim); err != nil {
		t.Fatal(err)
	}
	crashedKey := "native/cargo/sha256/" + strings.TrimPrefix(crashed.Claim.Digest, "sha256:")
	payload, err := json.Marshal(cargoReclaimPayload{Format: repository.FormatCargo, ObjectKey: crashedKey})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := storeA.EnqueueLifecycleJob(ctx, repository.LifecycleJob{
		ID: uuid.NewString(), RepositoryID: repo.ID, Kind: repository.LifecycleJobReclaim,
		IdempotencyKey: "cargo-crashed-publisher:" + uuid.NewString(), Payload: payload,
	}); err != nil {
		t.Fatal(err)
	}
	if err := objectsA.PutVerifiedReader(ctx, crashedKey, bytes.NewReader(crashedCrate), int64(len(crashedCrate)), crashed.Claim.Digest); err != nil {
		t.Fatal(err)
	}
	if err := (NativeCargoMaintenance{Store: storeB, Objects: objectsB}).RunReclaimJobs(ctx, 10); err != nil {
		t.Fatal(err)
	}
	if _, err := objectsB.Stat(ctx, crashedKey); !errors.Is(err, objectstore.ErrNotFound) {
		t.Fatalf("crashed publisher's object was not reclaimed: %v", err)
	}
	if _, err := storeB.GetCargoPublication(ctx, repo.ID, "demo", "1.1.0"); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("crashed publisher's version became visible: %v", err)
	}
}
