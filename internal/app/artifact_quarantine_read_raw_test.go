package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

func enableQuarantineReadPolicy(t *testing.T, store *repository.MemoryStore, repositoryID string) {
	t.Helper()
	ctx := context.Background()
	policy, err := store.GetRepositoryQuarantineReadPolicy(ctx, repositoryID)
	if err != nil {
		t.Fatal(err)
	}
	policy.Enabled = true
	if _, err = store.ReplaceRepositoryQuarantineReadPolicy(ctx, repositoryID, policy, policy.Version); err != nil {
		t.Fatal(err)
	}
}

func quarantineRawReadArtifact(t *testing.T, store *repository.MemoryStore, repositoryID, path, digest string) repository.ArtifactQuarantine {
	t.Helper()
	value, err := store.ReplaceArtifactQuarantine(context.Background(), repository.ArtifactQuarantine{
		RepositoryID: repositoryID,
		Format:       repository.FormatRaw,
		Coordinate:   path,
		Digest:       digest,
		State:        repository.ArtifactQuarantineStateQuarantined,
		Reason:       "malware confirmed",
		UpdatedBy:    "security-admin",
	}, "0")
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func requireQuarantineReadDeniedAudit(t *testing.T, store *repository.MemoryStore, format repository.Format) {
	t.Helper()
	audits, err := store.ListAudits(context.Background(), repository.AuditQuery{Format: string(format)})
	if err != nil {
		t.Fatal(err)
	}
	for _, audit := range audits {
		if audit.Outcome == repository.AuditAccessDenied && audit.Status == http.StatusForbidden && audit.AuthorizationSource == "quarantine_read_policy" && audit.AuthorizationReason == repository.ArtifactQuarantinedReason {
			return
		}
	}
	t.Fatalf("missing quarantine read denial audit for %s: %#v", format, audits)
}

func TestRawQuarantineReadPolicyBlocksGetHeadAndChecksumButDefaultsCompatible(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	objects := NewMemoryOCIObjectStore()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: "raw-read-hosted", Name: "raw-read-hosted", Format: repository.FormatRaw})
	if err != nil {
		t.Fatal(err)
	}
	body := []byte("quarantined raw bytes")
	sum := sha256.Sum256(body)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	objectKey := "native/raw/sha256/" + hex.EncodeToString(sum[:])
	if err = objects.Put(ctx, objectKey, body); err != nil {
		t.Fatal(err)
	}
	if _, err = store.PutRawAsset(ctx, repository.RawAsset{RepositoryID: repo.ID, Path: "release/app.bin", Digest: digest, ObjectKey: objectKey, Size: int64(len(body)), ContentType: "application/octet-stream"}); err != nil {
		t.Fatal(err)
	}
	quarantine := quarantineRawReadArtifact(t, store, repo.ID, "release/app.bin", digest)
	handler := NewGatewayHandler(Dependencies{NativeOCIObjectStore: objects}, store, TestAdapter{}, testAuthenticator())
	request := func(method, suffix string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "/raw/raw-read-hosted/release/app.bin"+suffix, nil)
		authorize(r, "resolver-secret")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}

	if compatible := request(http.MethodGet, ""); compatible.Code != http.StatusOK || compatible.Body.String() != string(body) {
		t.Fatalf("disabled policy read=%d body=%q", compatible.Code, compatible.Body.String())
	}
	enableQuarantineReadPolicy(t, store, repo.ID)
	for _, test := range []struct{ method, suffix string }{{http.MethodGet, ""}, {http.MethodHead, ""}, {http.MethodGet, ".sha256"}} {
		response := request(test.method, test.suffix)
		if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), repository.ArtifactQuarantinedReason) {
			t.Fatalf("%s %s=%d body=%q", test.method, test.suffix, response.Code, response.Body.String())
		}
	}
	requireQuarantineReadDeniedAudit(t, store, repository.FormatRaw)
	listRequest := httptest.NewRequest(http.MethodGet, "/raw/raw-read-hosted/release/", nil)
	authorize(listRequest, "resolver-secret")
	listResponse := httptest.NewRecorder()
	handler.ServeHTTP(listResponse, listRequest)
	if listResponse.Code != http.StatusOK || strings.Contains(listResponse.Body.String(), "release/app.bin") {
		t.Fatalf("quarantined raw list=%d body=%q", listResponse.Code, listResponse.Body.String())
	}

	policy, err := store.GetRepositoryQuarantineReadPolicy(ctx, repo.ID)
	if err != nil {
		t.Fatal(err)
	}
	policy.Enabled = false
	disabled, err := store.ReplaceRepositoryQuarantineReadPolicy(ctx, repo.ID, policy, policy.Version)
	if err != nil {
		t.Fatal(err)
	}
	if compatible := request(http.MethodGet, ""); compatible.Code != http.StatusOK || compatible.Body.String() != string(body) {
		t.Fatalf("disabled-again read=%d body=%q", compatible.Code, compatible.Body.String())
	}
	disabled.Enabled = true
	if _, err = store.ReplaceRepositoryQuarantineReadPolicy(ctx, repo.ID, disabled, disabled.Version); err != nil {
		t.Fatal(err)
	}

	release := quarantine
	release.State = repository.ArtifactQuarantineStateReleased
	release.Reason = "false positive"
	release.UpdatedBy = "security-admin"
	if _, err = store.ReplaceArtifactQuarantine(ctx, release, quarantine.Version); err != nil {
		t.Fatal(err)
	}
	if restored := request(http.MethodGet, ""); restored.Code != http.StatusOK || restored.Body.String() != string(body) {
		t.Fatalf("released read=%d body=%q", restored.Code, restored.Body.String())
	}
}

func TestRawGroupDoesNotFallThroughPastQuarantinedHostedArtifact(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	objects := NewMemoryOCIObjectStore()
	hosted, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: "raw-read-member", Name: "raw-read-member", Format: repository.FormatRaw})
	if err != nil {
		t.Fatal(err)
	}
	body := []byte("blocked hosted bytes")
	sum := sha256.Sum256(body)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	key := "native/raw/sha256/" + hex.EncodeToString(sum[:])
	if err = objects.Put(ctx, key, body); err != nil {
		t.Fatal(err)
	}
	if _, err = store.PutRawAsset(ctx, repository.RawAsset{RepositoryID: hosted.ID, Path: "release/app.bin", Digest: digest, ObjectKey: key, Size: int64(len(body))}); err != nil {
		t.Fatal(err)
	}
	proxy, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: "raw-read-proxy", Name: "raw-read-proxy", Format: repository.FormatRaw, Type: repository.RepositoryTypeProxy, Endpoint: "https://proxy.example", AllowedHosts: []string{"proxy.example"}})
	if err != nil {
		t.Fatal(err)
	}
	createV2Group(t, store, "raw-read-group", repository.FormatRaw,
		repository.GroupMember{RepositoryID: hosted.ID, Position: 0},
		repository.GroupMember{RepositoryID: proxy.ID, Position: 1},
	)
	enableQuarantineReadPolicy(t, store, hosted.ID)
	quarantineRawReadArtifact(t, store, hosted.ID, "release/app.bin", digest)
	client := &rawFixtureClient{responses: map[string]int{proxy.Name: http.StatusOK}, body: []byte("proxy bypass")}
	handler := NewGatewayHandlerWithRawCache(Dependencies{NativeOCIObjectStore: objects}, store, TestAdapter{}, testAuthenticator(), NewDefaultOCICache(NewMemoryOCIObjectStore(), nil), nil, NewRawCache(NewMemoryOCIObjectStore(), time.Hour, time.Hour, []string{"proxy.example"}), nil, client)

	request := httptest.NewRequest(http.MethodGet, "/raw/raw-read-group/release/app.bin", nil)
	authorize(request, "resolver-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), repository.ArtifactQuarantinedReason) {
		t.Fatalf("group read=%d body=%q", response.Code, response.Body.String())
	}
	if calls := client.Calls(); len(calls) != 0 {
		t.Fatalf("proxy bypass calls=%v", calls)
	}
}
