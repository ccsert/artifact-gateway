package app

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"

	"github.com/ccsert/artifact-gateway/internal/objectstore"
	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

func TestNativeConanPublishSessionMakesRevisionVisibleOnlyOnCommit(t *testing.T) {
	store := repository.NewMemoryStore()
	objects := objectstore.NewMemoryStore()
	repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "conan-hosted", Format: repository.FormatConan})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.ReplaceRepositoryGrants(context.Background(), repo.ID, []repository.RepositoryGrant{{Principal: "build-agent", Scopes: []string{"repositories:read", "repositories:write"}}}, "1"); err != nil {
		t.Fatal(err)
	}
	handler := newTestGateway(t, GatewayOptions{Dependencies: Dependencies{}, Store: store, ConanPublications: store, Adapter: TestAdapter{}, Authenticator: testAuthenticator(), Storage: ObjectStores{repository.FormatConan: objects}})
	body := []byte("from publish session")
	declaration, bodies := conanRecipePublicationFixture("pkg/1.0/user/stable", body)
	requestBody, _ := json.Marshal(declaration)
	create := httptest.NewRequest(http.MethodPost, "/api/v2/repositories/"+repo.ID+"/conan-publish-sessions", bytes.NewReader(requestBody))
	create.Header.Set("Authorization", "Bearer resolver-secret")
	created := httptest.NewRecorder()
	handler.ServeHTTP(created, create)
	if created.Code != http.StatusCreated {
		t.Fatalf("create=%d body=%s", created.Code, created.Body.String())
	}
	sessionID := requirePublishSessionContractKeys(t, created.Body.Bytes(), len(declaration.Objects))
	before := httptest.NewRecorder()
	nativePath := "/conan/conan-hosted/v2/conans/pkg/1.0/user/stable/revisions/" + declaration.RecipeRevision + "/files/conanfile.py"
	beforeRequest := httptest.NewRequest(http.MethodGet, nativePath, nil)
	beforeRequest.Header.Set("Authorization", "Bearer resolver-secret")
	handler.ServeHTTP(before, beforeRequest)
	if before.Code != http.StatusNotFound {
		t.Fatalf("read before commit=%d body=%s", before.Code, before.Body.String())
	}
	for name, assetBody := range bodies {
		upload := httptest.NewRequest(http.MethodPut, "/api/v2/conan-publish-sessions/"+sessionID+"/objects/"+name, bytes.NewReader(assetBody))
		upload.Header.Set("Authorization", "Bearer resolver-secret")
		uploaded := httptest.NewRecorder()
		handler.ServeHTTP(uploaded, upload)
		if uploaded.Code != http.StatusNoContent {
			t.Fatalf("upload %s=%d body=%s", name, uploaded.Code, uploaded.Body.String())
		}
	}
	commit := httptest.NewRequest(http.MethodPost, "/api/v2/conan-publish-sessions/"+sessionID+":commit", nil)
	commit.Header.Set("Authorization", "Bearer resolver-secret")
	committed := httptest.NewRecorder()
	handler.ServeHTTP(committed, commit)
	if committed.Code != http.StatusOK {
		t.Fatalf("commit=%d body=%s", committed.Code, committed.Body.String())
	}
	after := httptest.NewRecorder()
	afterRequest := httptest.NewRequest(http.MethodGet, nativePath, nil)
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
	handler := newTestGateway(t, GatewayOptions{Dependencies: Dependencies{}, Store: store, ConanPublications: store, Adapter: TestAdapter{}, Authenticator: testAuthenticator(), Storage: ObjectStores{repository.FormatConan: objectstore.NewMemoryStore()}})
	recipe, _ := conanRecipePublicationFixture("pkg/1.0/user/stable", []byte("recipe"))
	declaration, _ := conanPackagePublicationFixture(t, recipe.Reference, recipe.RecipeRevision)
	requestBody, _ := json.Marshal(declaration)
	create := httptest.NewRequest(http.MethodPost, "/api/v2/repositories/"+repo.ID+"/conan-publish-sessions", bytes.NewReader(requestBody))
	create.Header.Set("Authorization", "Bearer resolver-secret")
	created := httptest.NewRecorder()
	handler.ServeHTTP(created, create)
	if created.Code != http.StatusCreated {
		t.Fatalf("create=%d body=%s", created.Code, created.Body.String())
	}
	id := requirePublishSessionContractKeys(t, created.Body.Bytes(), len(declaration.Objects))

	get := httptest.NewRequest(http.MethodGet, "/api/v2/conan-publish-sessions/"+id, nil)
	get.Header.Set("Authorization", "Bearer resolver-secret")
	read := httptest.NewRecorder()
	handler.ServeHTTP(read, get)
	if read.Code != http.StatusOK {
		t.Fatalf("get=%d body=%s", read.Code, read.Body.String())
	}
	if got := requirePublishSessionContractKeys(t, read.Body.Bytes(), len(declaration.Objects)); got != id {
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
	wantCoordinate := declaration.Reference + "#" + declaration.RecipeRevision + "/" + declaration.PackageID + "#" + declaration.PackageRevision
	if session.RepositoryID != repo.ID || session.Coordinate != wantCoordinate || session.State != "open" {
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
	handler := newTestGateway(t, GatewayOptions{Dependencies: Dependencies{}, Store: store, ConanPublications: store, Adapter: TestAdapter{}, Authenticator: testAuthenticator(), Storage: ObjectStores{repository.FormatConan: objectstore.NewMemoryStore()}})
	recipeBody := []byte("recipe with exported sources")
	sourceBody := bytes.Repeat([]byte("c"), managementJSONBodyLimit+1)
	body := conanPublicationArchiveFixture(t, map[string][]byte{"source.txt": sourceBody})
	if len(body) <= managementJSONBodyLimit {
		t.Fatalf("archive size=%d does not exceed management JSON limit", len(body))
	}
	revision, manifest := conanPublicationManifestFixture(map[string][]byte{"conanfile.py": recipeBody, "export_source/source.txt": sourceBody})
	bodies := map[string][]byte{"conanfile.py": recipeBody, "conan_sources.tgz": body, "conanmanifest.txt": manifest}
	declaration := nativeConanPublishRequest{Kind: "recipe", Reference: "pkg/1.0/user/stable", RecipeRevision: revision, Objects: conanPublicationDeclaredObjectsFixture(bodies)}
	requestBody, _ := json.Marshal(declaration)
	create := httptest.NewRequest(http.MethodPost, "/api/v2/repositories/"+repo.ID+"/conan-publish-sessions", bytes.NewReader(requestBody))
	create.Header.Set("Authorization", "Bearer resolver-secret")
	created := httptest.NewRecorder()
	handler.ServeHTTP(created, create)
	if created.Code != http.StatusCreated {
		t.Fatalf("create=%d body=%s", created.Code, created.Body.String())
	}
	sessionID := requirePublishSessionContractKeys(t, created.Body.Bytes(), len(declaration.Objects))
	upload := httptest.NewRequest(http.MethodPut, "/api/v2/conan-publish-sessions/"+sessionID+"/objects/conan_sources.tgz", bytes.NewReader(body))
	upload.Header.Set("Authorization", "Bearer resolver-secret")
	uploaded := httptest.NewRecorder()
	handler.ServeHTTP(uploaded, upload)
	if uploaded.Code != http.StatusNoContent {
		t.Fatalf("upload=%d body=%s", uploaded.Code, uploaded.Body.String())
	}
}

func conanRecipePublicationFixture(reference string, body []byte) (nativeConanPublishRequest, map[string][]byte) {
	revision, manifest := conanPublicationManifestFixture(map[string][]byte{"conanfile.py": body})
	bodies := map[string][]byte{"conanfile.py": body, "conanmanifest.txt": manifest}
	return nativeConanPublishRequest{Kind: "recipe", Reference: reference, RecipeRevision: revision, Objects: conanPublicationDeclaredObjectsFixture(bodies)}, bodies
}

func conanPackagePublicationFixture(t *testing.T, reference, recipeRevision string) (nativeConanPublishRequest, map[string][]byte) {
	t.Helper()
	info := []byte("[settings]\nos=Linux\narch=x86_64\n")
	payload := []byte("complete package bytes")
	revision, manifest := conanPublicationManifestFixture(map[string][]byte{"conaninfo.txt": info, "payload.txt": payload})
	bodies := map[string][]byte{"conaninfo.txt": info, "conan_package.tgz": conanPublicationArchiveFixture(t, map[string][]byte{"payload.txt": payload}), "conanmanifest.txt": manifest}
	return nativeConanPublishRequest{Kind: "package", Reference: reference, RecipeRevision: recipeRevision, PackageID: fmt.Sprintf("%x", sha1.Sum(info)), PackageRevision: revision, Objects: conanPublicationDeclaredObjectsFixture(bodies)}, bodies
}

func conanPublicationManifestFixture(tree map[string][]byte) (string, []byte) {
	names := make([]string, 0, len(tree))
	for name := range tree {
		names = append(names, name)
	}
	sort.Strings(names)
	var entries bytes.Buffer
	for _, name := range names {
		_, _ = fmt.Fprintf(&entries, "%s: %x\n", name, md5.Sum(tree[name]))
	}
	return fmt.Sprintf("%x", md5.Sum(entries.Bytes())), append([]byte("1700000000\n"), entries.Bytes()...)
}

func conanPublicationDeclaredObjectsFixture(bodies map[string][]byte) []repository.MavenDeclaredObject {
	objects := make([]repository.MavenDeclaredObject, 0, len(bodies))
	for name, body := range bodies {
		objects = append(objects, repository.MavenDeclaredObject{Name: name, Digest: fmt.Sprintf("sha256:%x", sha256.Sum256(body)), Size: int64(len(body))})
	}
	sort.Slice(objects, func(i, j int) bool { return objects[i].Name < objects[j].Name })
	return objects
}

func conanPublicationArchiveFixture(t *testing.T, tree map[string][]byte) []byte {
	t.Helper()
	var archive bytes.Buffer
	gz, err := gzip.NewWriterLevel(&archive, gzip.NoCompression)
	if err != nil {
		t.Fatal(err)
	}
	tw := tar.NewWriter(gz)
	names := make([]string, 0, len(tree))
	for name := range tree {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		body := tree[name]
		if err = tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body))}); err != nil {
			t.Fatal(err)
		}
		if _, err = tw.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	if err = tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err = gz.Close(); err != nil {
		t.Fatal(err)
	}
	return archive.Bytes()
}
