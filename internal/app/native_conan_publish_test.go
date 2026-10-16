package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

func TestNativeConanPublishSessionMakesRevisionVisibleOnlyOnCommit(t *testing.T) {
	store := repository.NewMemoryStore()
	objects := NewMemoryOCIObjectStore()
	repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "conan-hosted", Format: repository.FormatConan})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.ReplaceRepositoryGrants(context.Background(), repo.ID, []repository.RepositoryGrant{{Principal: "build-agent", Scopes: []string{"repositories:read", "repositories:write"}}}, "1"); err != nil {
		t.Fatal(err)
	}
	handler := NewGatewayHandler(Dependencies{NativeConanObjectStore: objects}, store, TestAdapter{}, testAuthenticator())
	body := []byte("from publish session")
	sum := sha256.Sum256(body)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	requestBody, _ := json.Marshal(map[string]any{"kind": "recipe", "reference": "pkg/1.0/user/stable", "recipeRevision": "rrev", "objects": []map[string]any{{"name": "conanfile.py", "digest": digest, "size": len(body)}}})
	create := httptest.NewRequest(http.MethodPost, "/api/v2/repositories/"+repo.ID+"/conan-publish-sessions", bytes.NewReader(requestBody))
	create.Header.Set("Authorization", "Bearer resolver-secret")
	created := httptest.NewRecorder()
	handler.ServeHTTP(created, create)
	if created.Code != http.StatusCreated {
		t.Fatalf("create=%d body=%s", created.Code, created.Body.String())
	}
	sessionID := requirePublishSessionContractKeys(t, created.Body.Bytes())
	before := httptest.NewRecorder()
	beforeRequest := httptest.NewRequest(http.MethodGet, "/conan/v2/conan-hosted/conans/pkg/1.0/user/stable/revisions/rrev/files/conanfile.py", nil)
	beforeRequest.Header.Set("Authorization", "Bearer resolver-secret")
	handler.ServeHTTP(before, beforeRequest)
	if before.Code != http.StatusNotFound {
		t.Fatalf("read before commit=%d body=%s", before.Code, before.Body.String())
	}
	upload := httptest.NewRequest(http.MethodPut, "/api/v2/conan-publish-sessions/"+sessionID+"/objects/conanfile.py", bytes.NewReader(body))
	upload.Header.Set("Authorization", "Bearer resolver-secret")
	uploaded := httptest.NewRecorder()
	handler.ServeHTTP(uploaded, upload)
	if uploaded.Code != http.StatusNoContent {
		t.Fatalf("upload=%d body=%s", uploaded.Code, uploaded.Body.String())
	}
	commit := httptest.NewRequest(http.MethodPost, "/api/v2/conan-publish-sessions/"+sessionID+":commit", nil)
	commit.Header.Set("Authorization", "Bearer resolver-secret")
	committed := httptest.NewRecorder()
	handler.ServeHTTP(committed, commit)
	if committed.Code != http.StatusOK {
		t.Fatalf("commit=%d body=%s", committed.Code, committed.Body.String())
	}
	after := httptest.NewRecorder()
	afterRequest := httptest.NewRequest(http.MethodGet, "/conan/v2/conan-hosted/conans/pkg/1.0/user/stable/revisions/rrev/files/conanfile.py", nil)
	afterRequest.Header.Set("Authorization", "Bearer resolver-secret")
	handler.ServeHTTP(after, afterRequest)
	if after.Code != http.StatusOK || after.Body.String() != string(body) {
		t.Fatalf("read after commit=%d body=%s", after.Code, after.Body.String())
	}
}

func TestNativeConanPublishSessionResponsesMatchContractKeys(t *testing.T) {
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "conan-hosted", Format: repository.FormatConan})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.ReplaceRepositoryGrants(context.Background(), repo.ID, []repository.RepositoryGrant{{Principal: "build-agent", Scopes: []string{"repositories:read", "repositories:write"}}}, "1"); err != nil {
		t.Fatal(err)
	}
	handler := NewGatewayHandler(Dependencies{NativeConanObjectStore: NewMemoryOCIObjectStore()}, store, TestAdapter{}, testAuthenticator())
	requestBody, _ := json.Marshal(map[string]any{"kind": "package", "reference": "pkg/1.0/user/stable", "recipeRevision": "rrev", "packageId": "pid", "packageRevision": "prev", "objects": []map[string]any{{"name": "conaninfo.txt", "digest": "sha256:" + strings.Repeat("a", 64), "size": 1}}})
	create := httptest.NewRequest(http.MethodPost, "/api/v2/repositories/"+repo.ID+"/conan-publish-sessions", bytes.NewReader(requestBody))
	create.Header.Set("Authorization", "Bearer resolver-secret")
	created := httptest.NewRecorder()
	handler.ServeHTTP(created, create)
	if created.Code != http.StatusCreated {
		t.Fatalf("create=%d body=%s", created.Code, created.Body.String())
	}
	id := requirePublishSessionContractKeys(t, created.Body.Bytes())

	get := httptest.NewRequest(http.MethodGet, "/api/v2/conan-publish-sessions/"+id, nil)
	get.Header.Set("Authorization", "Bearer resolver-secret")
	read := httptest.NewRecorder()
	handler.ServeHTTP(read, get)
	if read.Code != http.StatusOK {
		t.Fatalf("get=%d body=%s", read.Code, read.Body.String())
	}
	if got := requirePublishSessionContractKeys(t, read.Body.Bytes()); got != id {
		t.Fatalf("get session id=%q, want %q", got, id)
	}
	var session struct {
		RepositoryID string `json:"repositoryId"`
		Coordinate   string `json:"coordinate"`
		State        string `json:"state"`
	}
	if err = json.Unmarshal(read.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	if session.RepositoryID != repo.ID || session.Coordinate != "pkg/1.0/user/stable#rrev/pid#prev" || session.State != "open" {
		t.Fatalf("session=%+v", session)
	}
}

// The generated management routes cap JSON bodies, so Conan object uploads must
// stay exempt like Maven ones.
func TestNativeConanPublishObjectUploadExceedsManagementJSONLimit(t *testing.T) {
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "conan-hosted", Format: repository.FormatConan})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.ReplaceRepositoryGrants(context.Background(), repo.ID, []repository.RepositoryGrant{{Principal: "build-agent", Scopes: []string{"repositories:read", "repositories:write"}}}, "1"); err != nil {
		t.Fatal(err)
	}
	handler := NewGatewayHandler(Dependencies{NativeConanObjectStore: NewMemoryOCIObjectStore()}, store, TestAdapter{}, testAuthenticator())
	body := bytes.Repeat([]byte("c"), managementJSONBodyLimit+1)
	sum := sha256.Sum256(body)
	requestBody, _ := json.Marshal(map[string]any{"kind": "recipe", "reference": "pkg/1.0/user/stable", "recipeRevision": "rrev", "objects": []map[string]any{{"name": "conan_sources.tgz", "digest": "sha256:" + hex.EncodeToString(sum[:]), "size": len(body)}}})
	create := httptest.NewRequest(http.MethodPost, "/api/v2/repositories/"+repo.ID+"/conan-publish-sessions", bytes.NewReader(requestBody))
	create.Header.Set("Authorization", "Bearer resolver-secret")
	created := httptest.NewRecorder()
	handler.ServeHTTP(created, create)
	if created.Code != http.StatusCreated {
		t.Fatalf("create=%d body=%s", created.Code, created.Body.String())
	}
	sessionID := requirePublishSessionContractKeys(t, created.Body.Bytes())
	upload := httptest.NewRequest(http.MethodPut, "/api/v2/conan-publish-sessions/"+sessionID+"/objects/conan_sources.tgz", bytes.NewReader(body))
	upload.Header.Set("Authorization", "Bearer resolver-secret")
	uploaded := httptest.NewRecorder()
	handler.ServeHTTP(uploaded, upload)
	if uploaded.Code != http.StatusNoContent {
		t.Fatalf("upload=%d body=%s", uploaded.Code, uploaded.Body.String())
	}
}
