package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/artifact-gateway/artifact-gateway/internal/objectstore"
	"github.com/artifact-gateway/artifact-gateway/internal/protocol/cargo"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

func TestNativeCargoHostedPublishSparseIndexAndDownload(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{
		ID: "cargo-hosted", Name: "cargo-hosted", Format: repository.FormatCargo,
	})
	if err != nil {
		t.Fatal(err)
	}
	objects := NewMemoryOCIObjectStore()
	server := httptest.NewServer(NewGatewayHandler(Dependencies{NativeCargoObjectStore: objects}, store, TestAdapter{}, testAuthenticator()))
	defer server.Close()
	request := func(method, path, token string, body []byte, headers map[string]string) *http.Response {
		t.Helper()
		req, reqErr := http.NewRequest(method, server.URL+path, bytes.NewReader(body))
		if reqErr != nil {
			t.Fatal(reqErr)
		}
		if token != "" {
			req.Header.Set("Authorization", token) // Cargo sends the registry token without Bearer.
		}
		for key, value := range headers {
			req.Header.Set(key, value)
		}
		response, sendErr := server.Client().Do(req)
		if sendErr != nil {
			t.Fatal(sendErr)
		}
		return response
	}
	read := func(response *http.Response) []byte {
		t.Helper()
		defer func() { _ = response.Body.Close() }()
		body, readErr := io.ReadAll(response.Body)
		if readErr != nil {
			t.Fatal(readErr)
		}
		return body
	}
	base := "/cargo/" + repo.Name
	if response := request(http.MethodGet, base+"/config.json", "", nil, nil); response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous private config=%d body=%s", response.StatusCode, read(response))
	} else {
		_ = read(response)
	}
	configResponse := request(http.MethodGet, base+"/config.json", "resolver-secret", nil, nil)
	var config struct {
		DL           string `json:"dl"`
		API          string `json:"api"`
		AuthRequired bool   `json:"auth-required"`
	}
	if configResponse.StatusCode != http.StatusOK || json.NewDecoder(configResponse.Body).Decode(&config) != nil ||
		config.API != server.URL+base || config.DL != server.URL+base+"/api/v1/crates" || !config.AuthRequired {
		t.Fatalf("private config=%+v status=%d", config, configResponse.StatusCode)
	}
	_ = configResponse.Body.Close()
	if head := request(http.MethodHead, base+"/config.json", "resolver-secret", nil, nil); head.StatusCode != http.StatusOK || len(read(head)) != 0 {
		t.Fatalf("config HEAD=%d", head.StatusCode)
	}
	indexPath, err := cargo.SparseIndexPath("demo")
	if err != nil {
		t.Fatal(err)
	}
	indexURL := base + "/" + indexPath
	if response := request(http.MethodGet, indexURL, "resolver-secret", nil, nil); response.StatusCode != http.StatusNotFound {
		t.Fatalf("unpublished index=%d", response.StatusCode)
	} else {
		_ = read(response)
	}
	publishBody, crate := cargoC0PublishFixture(t, "demo", "1.0.0", "demo")
	publishURL := base + "/api/v1/crates/new"
	if denied := request(http.MethodPut, publishURL, "resolver-secret", publishBody, nil); denied.StatusCode != http.StatusForbidden {
		t.Fatalf("read-only credential published=%d body=%s", denied.StatusCode, read(denied))
	} else {
		_ = read(denied)
	}
	if _, err := store.GetCargoIdentityReservation(ctx, repo.ID, "demo", "1.0.0"); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("denied publish reserved an identity: %v", err)
	}
	if denied := request(http.MethodPut, publishURL, "", publishBody, nil); denied.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous publish=%d body=%s", denied.StatusCode, read(denied))
	} else {
		_ = read(denied)
	}
	response := request(http.MethodPut, publishURL, "admin-secret", publishBody, nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("publish=%d body=%s", response.StatusCode, read(response))
	}
	if !bytes.Contains(read(response), []byte(`"warnings"`)) {
		t.Fatal("Cargo publish success envelope is missing")
	}
	response = request(http.MethodGet, indexURL, "resolver-secret", nil, nil)
	indexBytes := read(response)
	checksum := sha256.Sum256(crate)
	if response.StatusCode != http.StatusOK || !bytes.Contains(indexBytes, []byte(`"cksum":"`+hex.EncodeToString(checksum[:])+`"`)) ||
		!strings.HasSuffix(string(indexBytes), "\n") {
		t.Fatalf("index=%d body=%s", response.StatusCode, indexBytes)
	}
	if etag := response.Header.Get("ETag"); etag == "" {
		t.Fatal("index ETag missing")
	} else if cached := request(http.MethodGet, indexURL, "resolver-secret", nil, map[string]string{"If-None-Match": etag}); cached.StatusCode != http.StatusNotModified {
		t.Fatalf("conditional index=%d body=%s", cached.StatusCode, read(cached))
	} else {
		_ = read(cached)
	}
	if head := request(http.MethodHead, indexURL, "resolver-secret", nil, nil); head.StatusCode != http.StatusOK || len(read(head)) != 0 {
		t.Fatalf("index HEAD=%d", head.StatusCode)
	}
	downloadURL := base + "/api/v1/crates/demo/1.0.0/download"
	response = request(http.MethodGet, downloadURL, "resolver-secret", nil, nil)
	if response.StatusCode != http.StatusOK || !bytes.Equal(read(response), crate) {
		t.Fatalf("download=%d", response.StatusCode)
	}
	if etag := response.Header.Get("ETag"); etag == "" {
		t.Fatal("crate ETag missing")
	} else if cached := request(http.MethodGet, downloadURL, "resolver-secret", nil, map[string]string{"If-None-Match": etag}); cached.StatusCode != http.StatusNotModified {
		t.Fatalf("conditional download=%d body=%s", cached.StatusCode, read(cached))
	} else {
		_ = read(cached)
	}
	if head := request(http.MethodHead, downloadURL, "resolver-secret", nil, nil); head.StatusCode != http.StatusOK || len(read(head)) != 0 {
		t.Fatalf("download HEAD=%d", head.StatusCode)
	}
	if partial := request(http.MethodGet, downloadURL, "resolver-secret", nil, map[string]string{"Range": "bytes=0-9"}); partial.StatusCode != http.StatusPartialContent || !bytes.Equal(read(partial), crate[:10]) {
		t.Fatalf("download range=%d", partial.StatusCode)
	}
	if retry := request(http.MethodPut, publishURL, "admin-secret", publishBody, nil); retry.StatusCode != http.StatusOK {
		t.Fatalf("exact retry=%d body=%s", retry.StatusCode, read(retry))
	} else {
		_ = read(retry)
	}
	changed, _ := cargoC0PublishFixture(t, "demo", "1.0.0", "demo", "changed-author")
	if conflict := request(http.MethodPut, publishURL, "admin-secret", changed, nil); conflict.StatusCode != http.StatusConflict {
		t.Fatalf("changed metadata=%d body=%s", conflict.StatusCode, read(conflict))
	} else {
		_ = read(conflict)
	}
	capacity, err := store.GetRepositoryCapacity(ctx, repo.ID)
	if err != nil || capacity.UsedBytes != int64(len(crate)) || capacity.ObjectCount != 1 {
		t.Fatalf("capacity=%+v err=%v", capacity, err)
	}
	if _, err := store.ReplaceRepositoryCapacityQuota(ctx, repo.ID, capacity.UsedBytes); err != nil {
		t.Fatal(err)
	}
	overQuotaBody, overQuotaCrate := cargoC0PublishFixture(t, "demo", "1.1.0", "demo")
	if overQuota := request(http.MethodPut, publishURL, "admin-secret", overQuotaBody, nil); overQuota.StatusCode != http.StatusInsufficientStorage {
		t.Fatalf("over-quota publish=%d body=%s", overQuota.StatusCode, read(overQuota))
	} else {
		_ = read(overQuota)
	}
	if _, err := store.GetCargoPublication(ctx, repo.ID, "demo", "1.1.0"); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("over-quota version became visible: %v", err)
	}
	overQuotaChecksum := sha256.Sum256(overQuotaCrate)
	if stored, err := objects.Stat(ctx, "native/cargo/sha256/"+hex.EncodeToString(overQuotaChecksum[:])); !errors.Is(err, objectstore.ErrNotFound) {
		t.Fatalf("over-quota object remained: %+v err=%v", stored, err)
	}
	audits, err := store.ListAudits(ctx, repository.AuditQuery{Repository: repo.Name, Format: string(repository.FormatCargo), Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	var publishSuccesses, deniedWrites int
	for _, audit := range audits {
		if audit.Operation == "put" && audit.Outcome == repository.AuditResolved {
			publishSuccesses++
		}
		if audit.Operation == "put" && audit.Outcome == repository.AuditAccessDenied {
			deniedWrites++
		}
	}
	if publishSuccesses != 1 || deniedWrites != 2 {
		t.Fatalf("publish audits: successes=%d denied=%d", publishSuccesses, deniedWrites)
	}
	if err := (NativeCargoMaintenance{Store: store, Objects: objects}).RunReclaimJobs(ctx, 10); err != nil {
		t.Fatalf("recovery job for a committed object failed: %v", err)
	}
	if _, err := objects.Stat(ctx, "native/cargo/sha256/"+hex.EncodeToString(checksum[:])); err != nil {
		t.Fatalf("recovery job deleted a committed crate: %v", err)
	}
	updated := repo
	updated.AnonymousRead = true
	if _, err := store.UpdateHostedRepository(ctx, updated, repo.Version); err != nil {
		t.Fatal(err)
	}
	enableAnonymousAccess(t, store)
	publicConfig := request(http.MethodGet, base+"/config.json", "", nil, nil)
	config = struct {
		DL           string `json:"dl"`
		API          string `json:"api"`
		AuthRequired bool   `json:"auth-required"`
	}{}
	if publicConfig.StatusCode != http.StatusOK || json.NewDecoder(publicConfig.Body).Decode(&config) != nil || config.AuthRequired {
		t.Fatalf("public config=%+v status=%d", config, publicConfig.StatusCode)
	}
	_ = publicConfig.Body.Close()
	if publicDownload := request(http.MethodGet, downloadURL, "", nil, nil); publicDownload.StatusCode != http.StatusOK || !bytes.Equal(read(publicDownload), crate) {
		t.Fatalf("public download=%d", publicDownload.StatusCode)
	}
}

func TestNativeCargoMaintenanceReclaimsUncommittedObject(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	if _, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: "cargo-repo", Name: "cargo-repo", Format: repository.FormatCargo}); err != nil {
		t.Fatal(err)
	}
	objects := NewMemoryOCIObjectStore()
	body := []byte("uncommitted Cargo crate bytes")
	sum := sha256.Sum256(body)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	key := "native/cargo/sha256/" + hex.EncodeToString(sum[:])
	if err := objects.PutVerifiedReader(ctx, key, bytes.NewReader(body), int64(len(body)), digest); err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(cargoReclaimPayload{Format: repository.FormatCargo, ObjectKey: key})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.EnqueueLifecycleJob(ctx, repository.LifecycleJob{
		ID: "orphan", RepositoryID: "cargo-repo", Kind: repository.LifecycleJobReclaim,
		IdempotencyKey: "cargo-orphan", Payload: payload,
	}); err != nil {
		t.Fatal(err)
	}
	if err := (NativeCargoMaintenance{Store: store, Objects: objects}).RunReclaimJobs(ctx, 10); err != nil {
		t.Fatal(err)
	}
	if _, err := objects.Stat(ctx, key); !errors.Is(err, objectstore.ErrNotFound) {
		t.Fatalf("orphan still present: %v", err)
	}
}

func TestNativeCargoBuildMetadataDownloadAndCollision(t *testing.T) {
	store := repository.NewMemoryStore()
	if _, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{
		ID: "cargo-build", Name: "cargo-build", Format: repository.FormatCargo,
	}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(NewGatewayHandler(Dependencies{NativeCargoObjectStore: NewMemoryOCIObjectStore()}, store, TestAdapter{}, testAuthenticator()))
	defer server.Close()
	publish := func(version string) int {
		t.Helper()
		body, _ := cargoC0PublishFixture(t, "demo", version, "demo")
		request, err := http.NewRequest(http.MethodPut, server.URL+"/cargo/cargo-build/api/v1/crates/new", bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Authorization", "admin-secret")
		response, err := server.Client().Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = response.Body.Close() }()
		return response.StatusCode
	}
	if got := publish("1.2.3+build.1"); got != http.StatusOK {
		t.Fatalf("first publish=%d", got)
	}
	request, err := http.NewRequest(http.MethodGet, server.URL+"/cargo/cargo-build/api/v1/crates/demo/1.2.3%2Bbuild.1/download", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "resolver-secret")
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("encoded build metadata download=%d", response.StatusCode)
	}
	if got := publish("1.2.3+build.2"); got != http.StatusConflict {
		t.Fatalf("conflicting build metadata publish=%d", got)
	}
}
