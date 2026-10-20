package nuget

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ccsert/artifact-gateway/internal/authorization"
	"github.com/ccsert/artifact-gateway/internal/objectstore"
	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/ccsert/artifact-gateway/internal/uploadspool"
	"github.com/google/uuid"
)

type fixture struct {
	module  *Module
	store   *repository.MemoryStore
	objects *objectstore.MemoryStore
	repo    repository.HostedRepository
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	store := repository.NewMemoryStore()
	objects := objectstore.NewMemoryStore()
	repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "nuget-test", Format: repository.FormatNuGet, Type: repository.RepositoryTypeHosted})
	if err != nil {
		t.Fatal(err)
	}
	auth := authorization.Authenticator{AdminToken: "nuget-test-admin", AdminActor: "operator", ResolverToken: "nuget-test-reader", ResolverActor: "reader", RepositoryReaders: map[string][]string{"reader": {"nuget-test"}}}
	m, err := New(Options{Store: store, Repositories: store, Objects: objects, Authenticator: auth, Authorizer: authorization.RepositoryAuthorizer{Grants: store, Legacy: auth}, Audit: store, Anonymous: store})
	if err != nil {
		t.Fatal(err)
	}
	return fixture{m, store, objects, repo}
}
func packageFixture(t *testing.T, id, version, dependencies string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for _, entry := range []struct{ name, body string }{{id + ".nuspec", `<?xml version="1.0"?><package><metadata><id>` + id + `</id><version>` + version + `</version><authors>Acme</authors><description>Hosted test</description>` + dependencies + `</metadata></package>`}, {"lib/net10.0/Example.dll", "test assembly fixture"}} {
		file, err := writer.Create(entry.name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = file.Write([]byte(entry.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}
func multipartPackage(t *testing.T, body []byte) ([]byte, string) {
	t.Helper()
	var buffer bytes.Buffer
	writer := multipart.NewWriter(&buffer)
	part, err := writer.CreateFormFile("package", "ignored.nupkg")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = part.Write(body); err != nil {
		t.Fatal(err)
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes(), writer.FormDataContentType()
}
func request(t *testing.T, handler http.Handler, method, path string, body []byte, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, path, bytes.NewReader(body))
	r.Host = "packages.example.test"
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	return w
}
func push(t *testing.T, f fixture, body []byte) *httptest.ResponseRecorder {
	payload, contentType := multipartPackage(t, body)
	return request(t, f.module, http.MethodPut, "/nuget/nuget-test/api/v2/package/", payload, map[string]string{"Content-Type": contentType, "X-NuGet-ApiKey": "nuget-test-admin"})
}

var bearer = map[string]string{"Authorization": "Bearer nuget-test-admin"}

func TestNuGetHostedNativePublicationAndV3Read(t *testing.T) {
	f := newFixture(t)
	body := packageFixture(t, "Acme.App", "1.0", `<dependencies><group targetFramework="net10.0"><dependency id="Acme.Core" version="[1.0.0,2.0.0)"/></group></dependencies>`)
	for range 2 {
		if w := push(t, f, body); w.Code != 201 {
			t.Fatalf("push=%d %s", w.Code, w.Body.String())
		}
	}
	if w := push(t, f, packageFixture(t, "ACME.APP", "1.0.0", "")); w.Code != 409 {
		t.Fatalf("normalized collision=%d %s", w.Code, w.Body.String())
	}
	index := request(t, f.module, "GET", "/nuget/nuget-test/v3/index.json", nil, bearer)
	if index.Code != 200 || !strings.Contains(index.Body.String(), "http://packages.example.test/nuget/nuget-test/v3/flat/") {
		t.Fatalf("service index=%d %s", index.Code, index.Body.String())
	}
	https := request(t, f.module, "GET", "/nuget/nuget-test/v3/index.json", nil, map[string]string{"Authorization": "Bearer nuget-test-admin", "X-Forwarded-Proto": "https"})
	if !strings.Contains(https.Body.String(), "https://packages.example.test/") {
		t.Fatalf("external origin=%s", https.Body.String())
	}
	list := request(t, f.module, "GET", "/nuget/nuget-test/v3/flat/acme.app/index.json", nil, bearer)
	if list.Code != 200 || strings.TrimSpace(list.Body.String()) != `{"versions":["1.0.0"]}` {
		t.Fatalf("versions=%d %s", list.Code, list.Body.String())
	}
	path := "/nuget/nuget-test/v3/flat/acme.app/1.0.0/acme.app.1.0.0.nupkg"
	w := request(t, f.module, "GET", path, nil, bearer)
	if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), body) {
		t.Fatalf("package=%d", w.Code)
	}
	head := request(t, f.module, "HEAD", path, nil, bearer)
	if head.Code != 200 || head.Body.Len() != 0 || head.Header().Get("ETag") != w.Header().Get("ETag") {
		t.Fatalf("HEAD=%+v", head)
	}
	rangeResponse := request(t, f.module, "GET", path, nil, map[string]string{"Authorization": "Bearer nuget-test-admin", "Range": "bytes=2-7"})
	if rangeResponse.Code != 206 || !bytes.Equal(rangeResponse.Body.Bytes(), body[2:8]) {
		t.Fatalf("range=%d %q", rangeResponse.Code, rangeResponse.Body.Bytes())
	}
	conditional := request(t, f.module, "GET", path, nil, map[string]string{"Authorization": "Bearer nuget-test-admin", "If-None-Match": w.Header().Get("ETag")})
	if conditional.Code != 304 {
		t.Fatalf("validator=%d", conditional.Code)
	}
	reg := request(t, f.module, "GET", "/nuget/nuget-test/v3/registration/acme.app/index.json", nil, bearer)
	if reg.Code != 200 || reg.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("registration=%d %s", reg.Code, reg.Body.String())
	}
	reader, err := gzip.NewReader(reg.Body)
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	_ = reader.Close()
	if !bytes.Contains(metadata, []byte(`"id":"Acme.Core"`)) || !bytes.Contains(metadata, []byte(`"range":"[1.0.0,2.0.0)"`)) {
		t.Fatalf("transitive metadata=%s", metadata)
	}
	for _, p := range []string{"/nuget/nuget-test/v3/query?q=acme", "/nuget/nuget-test/v3/autocomplete?q=acme", "/nuget/nuget-test/v3/autocomplete?id=acme.app"} {
		w := request(t, f.module, "GET", p, nil, bearer)
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"totalHits":1`) {
			t.Fatalf("discovery %s=%d %s", p, w.Code, w.Body.String())
		}
	}
	if repository.IsSupportedFormat(repository.FormatNuGet) {
		t.Fatal("preview format admitted")
	}
}

func TestNuGetPreviewStagingVisibilityAndCommitReadback(t *testing.T) {
	f := newFixture(t)
	body := packageFixture(t, "Acme.Core", "1.0.0", "")
	sum := sha256.Sum256(body)
	base := "/api/v2/previews/nuget/repositories/" + f.repo.ID + "/publication-sessions"
	in, _ := json.Marshal(publicationRequest{PackageID: "Acme.Core", Version: "1", Digest: "sha256:" + hex.EncodeToString(sum[:]), Size: int64(len(body))})
	created := request(t, f.module.Preview(), "POST", base, in, map[string]string{"Authorization": "Bearer nuget-test-admin", "Idempotency-Key": "stage-1"})
	if created.Code != 201 {
		t.Fatalf("reserve=%d %s", created.Code, created.Body.String())
	}
	var session previewSession
	if err := json.Unmarshal(created.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	path := "/nuget/nuget-test/v3/flat/acme.core/index.json"
	if w := request(t, f.module, "GET", path, nil, bearer); w.Code != 404 {
		t.Fatalf("reserved visible=%d", w.Code)
	}
	staged := request(t, f.module.Preview(), "PUT", base+"/"+session.ID+"/package", body, bearer)
	if staged.Code != 200 {
		t.Fatalf("stage=%d %s", staged.Code, staged.Body.String())
	}
	if w := request(t, f.module, "GET", path, nil, bearer); w.Code != 404 {
		t.Fatalf("staged visible=%d", w.Code)
	}
	if w := request(t, f.module.Preview(), "POST", base+"/"+session.ID+"/commit", nil, bearer); w.Code != 200 {
		t.Fatalf("commit=%d %s", w.Code, w.Body.String())
	}
	if w := request(t, f.module.Preview(), "POST", base+"/"+session.ID+"/commit", nil, bearer); w.Code != 200 {
		t.Fatalf("lost commit reply retry=%d %s", w.Code, w.Body.String())
	}
	if w := request(t, f.module.Preview(), "PUT", base+"/"+session.ID+"/package", packageFixture(t, "Acme.Core", "1.0.0", "<tags>changed</tags>"), bearer); w.Code != 409 {
		t.Fatalf("completed session changed bytes=%d %s", w.Code, w.Body.String())
	}
	if w := request(t, f.module, "GET", path, nil, bearer); w.Code != 200 {
		t.Fatalf("committed absent=%d", w.Code)
	}
	pub, err := f.store.GetNuGetPublication(context.Background(), f.repo.ID, "acme.core", "1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(pub.Nuspec, []byte(`<?xml version="1.0"?><package><metadata><id>Acme.Core</id><version>1.0.0</version><authors>Acme</authors><description>Hosted test</description></metadata></package>`)) {
		t.Fatalf("original nuspec=%s", pub.Nuspec)
	}
}

func TestNuGetCommitRejectsCorruptStagedBytes(t *testing.T) {
	f := newFixture(t)
	body := packageFixture(t, "Example", "1.0.0", "")
	sum := sha256.Sum256(body)
	session, err := f.module.reserve(context.Background(), f.repo, "operator", "corruption", publicationRequest{PackageID: "Example", Version: "1", Digest: "sha256:" + hex.EncodeToString(sum[:]), Size: int64(len(body))})
	if err != nil {
		t.Fatal(err)
	}
	spool, err := uploadspool.Read(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = spool.Close() }()
	session, err = f.module.stage(context.Background(), session, spool)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.objects.Put(context.Background(), session.ObjectKey, []byte("corrupt")); err != nil {
		t.Fatal(err)
	}
	if _, err = f.module.commit(context.Background(), session, "operator"); err == nil {
		t.Fatal("corrupt package committed")
	}
	if _, err = f.store.GetNuGetPublication(context.Background(), f.repo.ID, "example", "1.0.0"); err == nil {
		t.Fatal("corrupt package became visible")
	}
}

func TestNuGetConcurrentPushAndLostResponseAreIdempotent(t *testing.T) {
	f := newFixture(t)
	body := packageFixture(t, "Concurrent", "1.0.0", "")
	var wg sync.WaitGroup
	codes := make(chan int, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			payload, contentType := multipartPackage(t, body)
			w := request(t, f.module, "PUT", "/nuget/nuget-test/api/v2/package/", payload, map[string]string{"Content-Type": contentType, "X-NuGet-ApiKey": "nuget-test-admin"})
			codes <- w.Code
		}()
	}
	wg.Wait()
	close(codes)
	for code := range codes {
		if code != 201 {
			t.Fatalf("concurrent push=%d", code)
		}
	}
	var dropped atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if dropped.CompareAndSwap(false, true) {
			record := httptest.NewRecorder()
			f.module.ServeHTTP(record, r)
			conn, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				_ = conn.Close()
			}
			return
		}
		f.module.ServeHTTP(w, r)
	}))
	defer server.Close()
	payload, contentType := multipartPackage(t, body)
	for attempt := 0; attempt < 2; attempt++ {
		r, err := http.NewRequest("PUT", server.URL+"/nuget/nuget-test/api/v2/package/", bytes.NewReader(payload))
		if err != nil {
			t.Fatal(err)
		}
		r.Header.Set("Content-Type", contentType)
		r.Header.Set("X-NuGet-ApiKey", "nuget-test-admin")
		response, err := server.Client().Do(r)
		if attempt == 0 {
			if err == nil {
				_ = response.Body.Close()
				t.Fatal("expected dropped response")
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
		if response.StatusCode != 201 {
			t.Fatalf("replay=%d", response.StatusCode)
		}
	}
	items, err := f.store.ListNuGetPublications(context.Background(), f.repo.ID, "concurrent", 100, "")
	if err != nil || len(items) != 1 {
		t.Fatalf("visible duplicates=%+v %v", items, err)
	}
	audits, err := f.store.ListAudits(context.Background(), repository.AuditQuery{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	publications := 0
	for _, a := range audits {
		if a.Operation == "nuget.publish" {
			publications++
		}
	}
	if publications != 1 {
		t.Fatalf("duplicate publication audits=%d", publications)
	}
}

func TestNuGetAuthenticationAndPreviewCapabilityBoundary(t *testing.T) {
	f := newFixture(t)
	body := packageFixture(t, "Auth", "1.0.0", "")
	if w := push(t, f, body); w.Code != 201 {
		t.Fatalf("push=%d", w.Code)
	}
	for _, headers := range []map[string]string{nil, {"Authorization": "Bearer bad"}, {"X-NuGet-ApiKey": "bad"}, {"Authorization": "Bearer nuget-test-admin", "X-NuGet-ApiKey": "nuget-test-reader"}} {
		if w := request(t, f.module, "GET", "/nuget/nuget-test/v3/index.json", nil, headers); w.Code != 401 {
			t.Fatalf("invalid credentials=%d %s", w.Code, w.Body.String())
		}
	}
	r := httptest.NewRequest("GET", "/nuget/nuget-test/v3/index.json", nil)
	r.SetBasicAuth("token", "nuget-test-admin")
	w := httptest.NewRecorder()
	f.module.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("native Basic source=%d %s", w.Code, w.Body.String())
	}
	payload, ct := multipartPackage(t, body)
	if w := request(t, f.module, "PUT", "/nuget/nuget-test/api/v2/package/", payload, map[string]string{"Content-Type": ct, "X-NuGet-ApiKey": "nuget-test-reader"}); w.Code != 403 {
		t.Fatalf("read credential wrote=%d %s", w.Code, w.Body.String())
	}
	var absent *repository.MemoryStore
	if _, err := New(Options{Store: absent, Repositories: f.store, Objects: f.objects, Audit: f.store}); err == nil {
		t.Fatal("typed nil publication store accepted")
	}
	created := request(t, f.module.Preview(), "POST", "/api/v2/previews/nuget/repositories", []byte(`{"name":"nuget-preview-two"}`), bearer)
	if created.Code != 201 || !strings.Contains(created.Body.String(), `"preview":true`) {
		t.Fatalf("explicit preview create=%d %s", created.Code, created.Body.String())
	}
	if repository.IsSupportedFormat(repository.FormatNuGet) {
		t.Fatal("NuGet leaked into formal format profile")
	}
}

func TestNuGetPackagePrefixGrantsAndAnonymousCredentialFailure(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if _, err := f.store.ReplaceRepositoryGrants(ctx, f.repo.ID, []repository.RepositoryGrant{{Principal: "reader", Scopes: []string{"repositories:write"}, ResourcePrefix: "acme."}}, "1"); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"Acme.Widget", "Other.Widget"} {
		body, ct := multipartPackage(t, packageFixture(t, id, "1.0.0", ""))
		w := request(t, f.module, "PUT", "/nuget/nuget-test/api/v2/package/", body, map[string]string{"Content-Type": ct, "X-NuGet-ApiKey": "nuget-test-reader"})
		want := 201
		if id == "Other.Widget" {
			want = 403
		}
		if w.Code != want {
			t.Fatalf("prefix push %s=%d %s", id, w.Code, w.Body.String())
		}
	}
	if w := push(t, f, packageFixture(t, "Other.Widget", "1.0.0", "")); w.Code != 201 {
		t.Fatal(w.Code)
	}
	read := map[string]string{"Authorization": "Bearer nuget-test-reader"}
	for _, path := range []string{"index.json", "flat/acme.widget/index.json", "query", "autocomplete", "registration/acme.widget/index.json"} {
		w := request(t, f.module, "GET", "/nuget/nuget-test/v3/"+path, nil, read)
		if w.Code != 200 || path == "query" && strings.Contains(w.Body.String(), "Other.Widget") {
			t.Fatalf("prefix read %s=%d %s", path, w.Code, w.Body.String())
		}
	}
	if w := request(t, f.module, "GET", "/nuget/nuget-test/v3/flat/other.widget/index.json", nil, read); w.Code != 403 {
		t.Fatalf("prefix escape=%d", w.Code)
	}
	if _, err := f.store.ReplaceAnonymousAccessPolicy(ctx, repository.AnonymousAccessPolicy{Enabled: true}, "1"); err != nil {
		t.Fatal(err)
	}
	f.repo.AnonymousRead = true
	if _, err := f.store.UpdateHostedRepository(ctx, f.repo, f.repo.Version); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if w := request(t, f.module, "GET", "/nuget/nuget-test/v3/flat/acme.widget/index.json", nil, nil); w.Code != 200 {
			t.Fatalf("anonymous=%d", w.Code)
		}
		if w := request(t, f.module, "GET", "/nuget/nuget-test/v3/flat/acme.widget/index.json", nil, map[string]string{"X-NuGet-ApiKey": "revoked"}); w.Code != 401 {
			t.Fatalf("invalid key fell back=%d", w.Code)
		}
	}
}

func TestNuGetCommittedReplayRevalidatesOriginalBytes(t *testing.T) {
	f := newFixture(t)
	body := packageFixture(t, "Readback", "1.0.0", "")
	if w := push(t, f, body); w.Code != 201 {
		t.Fatal(w.Code)
	}
	pub, err := f.store.GetNuGetPublication(t.Context(), f.repo.ID, "readback", "1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	corrupt := append([]byte(nil), body...)
	corrupt[len(corrupt)/2] ^= 0xff
	if err = f.objects.Put(t.Context(), pub.ObjectKey, corrupt); err != nil {
		t.Fatal(err)
	}
	if w := push(t, f, body); w.Code != 409 {
		t.Fatalf("corrupt committed replay=%d %s", w.Code, w.Body.String())
	}
	base := "/api/v2/previews/nuget/repositories/" + f.repo.ID + "/publication-sessions/" + pub.SessionID
	for _, entry := range []struct {
		method, path string
		data         []byte
	}{{"POST", "/commit", nil}, {"PUT", "/package", body}} {
		if w := request(t, f.module.Preview(), entry.method, base+entry.path, entry.data, bearer); w.Code != 409 {
			t.Fatalf("corrupt committed %s=%d %s", entry.path, w.Code, w.Body.String())
		}
	}
	if data, err := f.objects.Get(t.Context(), pub.ObjectKey); err != nil || !bytes.Equal(data, corrupt) {
		t.Fatal("idempotent replay rewrote published bytes", err)
	}
}

func TestNuGetAccountStateBlocksNativeAndPreviewRequests(t *testing.T) {
	f := newFixture(t)
	f.module.auth.Users, f.module.auth.UserSessions = f.store, f.store
	f.module.access.Legacy = f.module.auth
	for _, entry := range []struct {
		name, role, reason string
		change             bool
	}{{"pending", "none", "authorization_pending", false}, {"change", "member", "password_change_required", true}} {
		user, err := f.store.CreateUser(t.Context(), repository.User{ID: uuid.NewString(), Name: entry.name, Role: entry.role, MustChangePassword: entry.change})
		if err != nil {
			t.Fatal(err)
		}
		session, err := f.store.CreateUserSession(t.Context(), repository.UserSession{ID: uuid.NewString(), UserID: user.ID, Kind: repository.UserSessionLocal, CreatedAt: time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(time.Hour)})
		if err != nil {
			t.Fatal(err)
		}
		token := f.module.auth.IssueUserSessionWithID(user.ID, session.ID, user.SessionVersion)
		for _, handler := range []struct {
			h    http.Handler
			path string
		}{{f.module, "/nuget/nuget-test/v3/index.json"}, {f.module.Preview(), "/api/v2/previews/nuget/repositories/" + f.repo.ID}} {
			w := request(t, handler.h, "GET", handler.path, nil, map[string]string{"Authorization": "Bearer " + token})
			if w.Code != 403 || !strings.Contains(w.Body.String(), entry.reason) {
				t.Fatalf("account %s %s=%d %s", entry.name, handler.path, w.Code, w.Body.String())
			}
		}
	}
}
