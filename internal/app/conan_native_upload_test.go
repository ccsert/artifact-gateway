package app

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/sha1"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/ccsert/artifact-gateway/internal/objectstore"
	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

type conanUploadFixture struct {
	store    *repository.MemoryStore
	objects  *objectstore.MemoryStore
	repo     repository.HostedRepository
	handler  http.Handler
	revision string
	manifest []byte
	base     string
}

func newConanUploadFixture(t *testing.T) conanUploadFixture {
	t.Helper()
	ctx := context.Background()
	store := repository.NewMemoryStore()
	objects := objectstore.NewMemoryStore()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "conan-upload", Format: repository.FormatConan})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.ReplaceRepositoryGrants(ctx, repo.ID, []repository.RepositoryGrant{{Principal: "build-agent", Scopes: []string{"repositories:write"}}, {Principal: "other-writer", Scopes: []string{"repositories:write"}}, {Principal: "reader", Scopes: []string{"repositories:read"}}}, "1"); err != nil {
		t.Fatal(err)
	}
	entries := fmt.Sprintf("conanfile.py: %x\n", md5.Sum([]byte("recipe")))
	revision := fmt.Sprintf("%x", md5.Sum([]byte(entries)))
	return conanUploadFixture{store: store, objects: objects, repo: repo, revision: revision, manifest: []byte("1700000000\n" + entries), base: "/conan/" + repo.Name + "/v2/conans/pkg/1.0/_/_/revisions/" + revision,
		handler: newTestGateway(t, GatewayOptions{Store: store, ConanPublications: store, Adapter: TestAdapter{}, Authenticator: testAuthenticator(), Storage: ObjectStores{repository.FormatConan: objects}})}
}

func (f conanUploadFixture) request(t *testing.T, method, path, actor string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, bytes.NewReader(body))
	request.SetBasicAuth(actor, "resolver-secret")
	if method == http.MethodPut {
		request.Header.Set("X-Checksum-Sha1", fmt.Sprintf("%x", sha1.Sum(body)))
	}
	response := httptest.NewRecorder()
	f.handler.ServeHTTP(response, request)
	return response
}

func (f conanUploadFixture) publish(t *testing.T, actor string) {
	t.Helper()
	for name, body := range map[string][]byte{"conanfile.py": []byte("recipe"), "conanmanifest.txt": f.manifest} {
		if name == "conanmanifest.txt" {
			continue
		}
		if w := f.request(t, http.MethodPut, f.base+"/files/"+name, actor, body); w.Code != 201 {
			t.Fatalf("asset=%d %s", w.Code, w.Body.String())
		}
	}
	if w := f.request(t, http.MethodPut, f.base+"/files/conanmanifest.txt", actor, f.manifest); w.Code != 201 {
		t.Fatalf("commit=%d %s", w.Code, w.Body.String())
	}
}

func TestConanNativeUploadGatesVisibilityAndPreservesImmutableReplay(t *testing.T) {
	f := newConanUploadFixture(t)
	if w := f.request(t, http.MethodPut, f.base+"/files/conanmanifest.txt", "build-agent", f.manifest); w.Code != 422 {
		t.Fatalf("missing asset=%d %s", w.Code, w.Body.String())
	}
	if w := f.request(t, http.MethodGet, f.base+"/files/conanmanifest.txt", "reader", nil); w.Code != 404 {
		t.Fatalf("partial revision read=%d", w.Code)
	}
	file := f.request(t, http.MethodPut, f.base+"/files/conanfile.py", "build-agent", []byte("recipe"))
	if file.Code != 201 || file.Header().Get("X-Artifact-Gateway-Publish-State") != "open" {
		t.Fatalf("stage=%d %s", file.Code, file.Body.String())
	}
	if w := f.request(t, http.MethodGet, f.base+"/files/conanfile.py", "reader", nil); w.Code != 404 {
		t.Fatalf("asset became visible before manifest: %d", w.Code)
	}
	committed := f.request(t, http.MethodPut, f.base+"/files/conanmanifest.txt", "build-agent", f.manifest)
	if committed.Code != 201 || committed.Header().Get("X-Artifact-Gateway-Publish-State") != "committed" || committed.Header().Get("X-Artifact-Gateway-Publish-Session") != file.Header().Get("X-Artifact-Gateway-Publish-Session") {
		t.Fatalf("commit=%d %s", committed.Code, committed.Body.String())
	}
	f.publish(t, "build-agent")
	if got, err := f.store.ListConanRecipeRevisions(context.Background(), f.repo.ID, "pkg/1.0/_/_"); err != nil || len(got) != 1 {
		t.Fatalf("duplicate visible identity=%v %v", got, err)
	}
	// A different manifest timestamp has the same RREV but different immutable
	// asset bytes, so it cannot overwrite the existing revision.
	if w := f.request(t, http.MethodPut, f.base+"/files/conanfile.py", "other-writer", []byte("recipe")); w.Code != 201 {
		t.Fatal(w.Code)
	}
	conflict := bytes.Replace(f.manifest, []byte("1700000000"), []byte("1700000001"), 1)
	if w := f.request(t, http.MethodPut, f.base+"/files/conanmanifest.txt", "other-writer", conflict); w.Code != 409 {
		t.Fatalf("immutable conflict=%d %s", w.Code, w.Body.String())
	}
	if w := f.request(t, http.MethodGet, f.base+"/files/conanmanifest.txt", "reader", nil); w.Code != 200 || !bytes.Equal(w.Body.Bytes(), f.manifest) {
		t.Fatalf("published bytes changed: %d %s", w.Code, w.Body.String())
	}
}

func TestConanNativeUploadRejectsCredentialsScopeQuotaAndNonHostedTargets(t *testing.T) {
	f := newConanUploadFixture(t)
	if w := f.request(t, http.MethodPut, f.base+"/files/conanfile.py", "reader", []byte("recipe")); w.Code != 403 {
		t.Fatalf("read-only write=%d", w.Code)
	}
	request := httptest.NewRequest(http.MethodPut, f.base+"/files/conanfile.py", strings.NewReader("recipe"))
	request.SetBasicAuth("build-agent", "incorrect")
	response := httptest.NewRecorder()
	f.handler.ServeHTTP(response, request)
	if response.Code != 401 || strings.Contains(response.Body.String(), "incorrect") {
		t.Fatalf("bad credential=%d %s", response.Code, response.Body.String())
	}
	request = httptest.NewRequest(http.MethodPut, f.base+"/files/conanfile.py", strings.NewReader("recipe"))
	request.SetBasicAuth("build-agent", "resolver-secret")
	request.Header.Set("X-Checksum-Sha1", strings.Repeat("0", 40))
	response = httptest.NewRecorder()
	f.handler.ServeHTTP(response, request)
	if response.Code != 422 {
		t.Fatalf("bad checksum=%d", response.Code)
	}
	if _, err := f.store.ReplaceRepositoryCapacityQuota(context.Background(), f.repo.ID, 1); err != nil {
		t.Fatal(err)
	}
	if w := f.request(t, http.MethodPut, f.base+"/files/conanfile.py", "build-agent", []byte("recipe")); w.Code != 201 {
		t.Fatal(w.Code)
	}
	if w := f.request(t, http.MethodPut, f.base+"/files/conanmanifest.txt", "build-agent", f.manifest); w.Code != 507 {
		t.Fatalf("quota=%d %s", w.Code, w.Body.String())
	}
	if w := f.request(t, http.MethodGet, f.base+"/files/conanfile.py", "reader", nil); w.Code != 404 {
		t.Fatalf("quota rejected revision visible=%d", w.Code)
	}
	if _, err := f.store.ReplaceRepositoryCapacityQuota(context.Background(), f.repo.ID, 0); err != nil {
		t.Fatal(err)
	}
	if w := f.request(t, http.MethodPut, f.base+"/files/conanmanifest.txt", "build-agent", f.manifest); w.Code != 201 {
		t.Fatalf("retry quota=%d %s", w.Code, w.Body.String())
	}
	proxy, err := f.store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "conan-proxy", Format: repository.FormatConan, Type: repository.RepositoryTypeProxy, Endpoint: "https://example.com", AllowedHosts: []string{"example.com"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.ReplaceRepositoryGrants(context.Background(), proxy.ID, []repository.RepositoryGrant{{Principal: "build-agent", Scopes: []string{"repositories:write"}}}, "1"); err != nil {
		t.Fatal(err)
	}
	if w := f.request(t, http.MethodPut, strings.Replace(f.base, "conan-upload", "conan-proxy", 1)+"/files/conanfile.py", "build-agent", []byte("recipe")); w.Code != 405 {
		t.Fatalf("Proxy write=%d %s", w.Code, w.Body.String())
	}
	createV2Group(t, f.store, "conan-group", repository.FormatConan, repository.GroupMember{RepositoryID: f.repo.ID, Position: 0})
	groupPath := strings.Replace(f.base, "/conan/conan-upload/v2/", "/conan/conan-group/v2/", 1)
	if w := f.request(t, http.MethodPut, groupPath+"/files/conanfile.py", "build-agent", []byte("recipe")); w.Code != 405 {
		t.Fatalf("Group write=%d %s", w.Code, w.Body.String())
	}
}

func TestConanNativeConcurrentIdenticalPublicationHasOneVisibleIdentity(t *testing.T) {
	f := newConanUploadFixture(t)
	for _, actor := range []string{"build-agent", "other-writer"} {
		if w := f.request(t, http.MethodPut, f.base+"/files/conanfile.py", actor, []byte("recipe")); w.Code != 201 {
			t.Fatal(w.Code)
		}
	}
	var wg sync.WaitGroup
	statuses := make(chan int, 2)
	for _, actor := range []string{"build-agent", "other-writer"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			statuses <- f.request(t, http.MethodPut, f.base+"/files/conanmanifest.txt", actor, f.manifest).Code
		}()
	}
	wg.Wait()
	close(statuses)
	for status := range statuses {
		if status != 201 {
			t.Fatalf("concurrent replay=%d", status)
		}
	}
	if revisions, err := f.store.ListConanRecipeRevisions(context.Background(), f.repo.ID, "pkg/1.0/_/_"); err != nil || len(revisions) != 1 {
		t.Fatalf("visible revisions=%v %v", revisions, err)
	}
}

func TestConanNativePackagePublicationRespectsRecipeQuarantine(t *testing.T) {
	f := newConanUploadFixture(t)
	f.publish(t, "build-agent")
	ctx := context.Background()
	recipe, err := f.store.GetConanRecipeRevision(ctx, f.repo.ID, "pkg/1.0/_/_", f.revision)
	if err != nil {
		t.Fatal(err)
	}
	quarantine, err := f.store.ReplaceArtifactQuarantine(ctx, repository.ArtifactQuarantine{RepositoryID: f.repo.ID, Format: repository.FormatConan, Coordinate: recipe.Reference + "#" + recipe.Revision, Digest: recipe.Digest, State: repository.ArtifactQuarantineStateQuarantined, Reason: "governed recipe closure", UpdatedBy: "security"}, "0")
	if err != nil {
		t.Fatal(err)
	}
	entries := fmt.Sprintf("conaninfo.txt: %x\npackage.txt: %x\n", md5.Sum(nil), md5.Sum([]byte("package binary")))
	previous := fmt.Sprintf("%x", md5.Sum([]byte(entries)))
	base := f.base + "/packages/da39a3ee5e6b4b0d3255bfef95601890afd80709/revisions/" + previous + "/files/"
	for name, body := range map[string][]byte{"conan_package.tgz": conanPackageArchive(), "conaninfo.txt": {}} {
		if w := f.request(t, http.MethodPut, base+name, "build-agent", body); w.Code != 201 {
			t.Fatalf("stage=%d %s", w.Code, w.Body.String())
		}
	}
	manifest := []byte("1700000000\n" + entries)
	if w := f.request(t, http.MethodPut, base+"conanmanifest.txt", "build-agent", manifest); w.Code != 409 {
		t.Fatalf("quarantined closure publication=%d %s", w.Code, w.Body.String())
	}
	if ids, err := f.store.ListConanPackageIDs(ctx, f.repo.ID, recipe.Reference, recipe.Revision); err != nil || len(ids) != 0 {
		t.Fatalf("quarantined closure gained visible package: %v %v", ids, err)
	}
	quarantine.State = repository.ArtifactQuarantineStateReleased
	quarantine.Reason = "reviewed"
	if _, err = f.store.ReplaceArtifactQuarantine(ctx, quarantine, quarantine.Version); err != nil {
		t.Fatal(err)
	}
	if w := f.request(t, http.MethodPut, base+"conanmanifest.txt", "build-agent", manifest); w.Code != 201 {
		t.Fatalf("retry after release=%d %s", w.Code, w.Body.String())
	}
}

func TestConanNativeTokenCannotCrossIntoAnonymousRepository(t *testing.T) {
	f := newConanUploadFixture(t)
	f.publish(t, "build-agent")
	enableAnonymousAccess(t, f.store)
	other, err := f.store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "other-conan", Format: repository.FormatConan, AnonymousRead: true})
	if err != nil {
		t.Fatal(err)
	}
	token, ok := testAuthenticator().IssueConanToken("build-agent", "resolver-secret", f.repo.ID)
	if !ok {
		t.Fatal("token exchange failed")
	}
	for _, root := range []struct {
		name   string
		status int
	}{{f.repo.Name, 200}, {other.Name, 401}} {
		request := httptest.NewRequest(http.MethodGet, strings.Replace(f.base, "/conan/"+f.repo.Name+"/", "/conan/"+root.name+"/", 1)+"/files/conanfile.py", nil)
		request.Header.Set("Authorization", "Bearer "+token)
		response := httptest.NewRecorder()
		f.handler.ServeHTTP(response, request)
		if response.Code != root.status {
			t.Fatalf("repository=%s status=%d", root.name, response.Code)
		}
	}
}

func TestConanRetiredGatewayLayoutHasNoAlias(t *testing.T) {
	f := newConanUploadFixture(t)
	for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodPut} {
		old := strings.Replace(f.base, "/conan/conan-upload/v2/", "/conan/v2/conan-upload/", 1)
		if w := f.request(t, method, old+"/files/conanfile.py", "build-agent", []byte("recipe")); w.Code != http.StatusNotFound {
			t.Fatalf("retired layout %s=%d %s", method, w.Code, w.Body.String())
		}
	}
	for _, item := range []struct{ path, body string }{
		{"/api/v2/repositories", `{"name":"v2","format":"conan","type":"hosted"}`},
		{"/api/v2/groups", `{"name":"v2","format":"conan","members":[{"repositoryId":"` + f.repo.ID + `","position":0}]}`},
	} {
		request := httptest.NewRequest(http.MethodPost, item.path, strings.NewReader(item.body))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Idempotency-Key", uuid.NewString())
		authorize(request, "admin-secret")
		response := httptest.NewRecorder()
		f.handler.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("reserved Conan remote name=%d %s", response.Code, response.Body.String())
		}
	}
}

func TestConanNativeAuthenticationFailsClosedWithoutIndependentSigningSecret(t *testing.T) {
	f := newConanUploadFixture(t)
	auth := testAuthenticator()
	auth.ConanTokenSecret = ""
	handler := newTestGateway(t, GatewayOptions{Store: f.store, ConanPublications: f.store, Adapter: TestAdapter{}, Authenticator: auth, Storage: ObjectStores{repository.FormatConan: f.objects}})
	req := httptest.NewRequest(http.MethodGet, "/conan/"+f.repo.Name+"/v2/users/authenticate", nil)
	req.SetBasicAuth("build-agent", "resolver-secret")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusNotImplemented || !strings.Contains(w.Body.String(), "not_supported") {
		t.Fatalf("unconfigured handshake=%d %s", w.Code, w.Body.String())
	}
}

func TestConanNativeUploadMissingObjectCapabilityReturnsNotSupported(t *testing.T) {
	f := newConanUploadFixture(t)
	var typedNil *objectstore.MemoryStore
	for _, objects := range []ObjectStore{nil, typedNil} {
		handler := ConanHandler{Repositories: f.store, NativeStore: f.store, Publications: f.store, NativeObjects: objects, Authenticator: testAuthenticator(), Authorizer: RepositoryAuthorizer{Grants: f.store, Legacy: testAuthenticator()}}
		request := httptest.NewRequest(http.MethodPut, f.base+"/files/conanfile.py", strings.NewReader("recipe"))
		request.SetBasicAuth("build-agent", "resolver-secret")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusNotImplemented || !strings.Contains(response.Body.String(), "not_supported") {
			t.Fatalf("missing objects=%d %s", response.Code, response.Body.String())
		}
		request = httptest.NewRequest(http.MethodPut, f.base+"/files/conanfile.py", strings.NewReader("recipe"))
		request.SetBasicAuth("reader", "resolver-secret")
		response = httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusForbidden {
			t.Fatalf("missing objects bypassed authorization=%d %s", response.Code, response.Body.String())
		}
	}
}

func TestConanNativeSessionCannotBypassManifestThroughManagementCommit(t *testing.T) {
	f := newConanUploadFixture(t)
	staged := f.request(t, http.MethodPut, f.base+"/files/conanfile.py", "build-agent", []byte("recipe"))
	if staged.Code != 201 {
		t.Fatal(staged.Code)
	}
	sessionID := staged.Header().Get("X-Artifact-Gateway-Publish-Session")
	upload := httptest.NewRequest(http.MethodPut, "/api/v2/conan-publish-sessions/"+sessionID+"/objects/conanfile.py", strings.NewReader("recipe"))
	authorize(upload, "resolver-secret")
	denied := httptest.NewRecorder()
	f.handler.ServeHTTP(denied, upload)
	if denied.Code != http.StatusConflict {
		t.Fatalf("management changed native pointers=%d %s", denied.Code, denied.Body.String())
	}

	request := httptest.NewRequest(http.MethodPost, "/api/v2/conan-publish-sessions/"+sessionID+":commit", nil)
	authorize(request, "resolver-secret")
	response := httptest.NewRecorder()
	f.handler.ServeHTTP(response, request)
	if response.Code != http.StatusConflict {
		t.Errorf("management bypass status=%d body=%s", response.Code, response.Body.String())
	}
	if revisions, err := f.store.ListConanRecipeRevisions(context.Background(), f.repo.ID, "pkg/1.0/_/_"); err != nil || len(revisions) != 0 {
		t.Errorf("management bypass exposed incomplete revision=%+v %v", revisions, err)
	}
	status := httptest.NewRequest(http.MethodGet, "/api/v2/conan-publish-sessions/"+sessionID, nil)
	authorize(status, "resolver-secret")
	read := httptest.NewRecorder()
	f.handler.ServeHTTP(read, status)
	if read.Code != http.StatusOK || !strings.Contains(read.Body.String(), `"state":"open"`) {
		t.Fatalf("native session status unavailable=%d %s", read.Code, read.Body.String())
	}
	if committed := f.request(t, http.MethodPut, f.base+"/files/conanmanifest.txt", "build-agent", f.manifest); committed.Code != http.StatusCreated {
		t.Fatalf("valid native resume failed=%d %s", committed.Code, committed.Body.String())
	}

}
