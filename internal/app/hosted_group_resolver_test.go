package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

// conanGatewayClient adapts the Conan-only fixture client to the OCIClient
// shape the gateway constructor expects; the other formats are unused.
type conanGatewayClient struct{ *conanFixtureClient }

func (conanGatewayClient) Fetch(context.Context, string, repository.Member, string, string, string, http.Header) (*http.Response, error) {
	return nil, errors.New("OCI fetch is not used by Conan group tests")
}

// createV2Group wires a V2 HostedGroup with the given members in the store.
func createV2Group(t *testing.T, store *repository.MemoryStore, name string, format repository.Format, members ...repository.GroupMember) repository.HostedGroup {
	t.Helper()
	group, _, err := store.CreateHostedGroupIdempotently(context.Background(), repository.HostedGroup{ID: "group-" + name, Name: name, Format: format, Members: members}, "test", "key-"+name, "payload")
	if err != nil {
		t.Fatal(err)
	}
	return group
}

func TestV2GroupOCIManifestHostedPreferredOverProxy(t *testing.T) {
	store := repository.NewMemoryStore()
	objects := NewMemoryOCIObjectStore()
	hosted, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: "oci-hosted", Name: "oci-hosted", Format: repository.FormatOCI})
	if err != nil {
		t.Fatal(err)
	}
	upstream := &proxyUpstream{bodies: map[string][]byte{}, calls: map[string]int{}}
	upstreamServer := httptest.NewServer(upstream)
	defer upstreamServer.Close()
	allowedHost := strings.TrimPrefix(upstreamServer.URL, "http://")
	proxy, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{
		ID: "oci-proxy", Name: "oci-proxy", Format: repository.FormatOCI,
		Type: repository.RepositoryTypeProxy, Endpoint: upstreamServer.URL, AllowedHosts: []string{allowedHost},
	})
	if err != nil {
		t.Fatal(err)
	}
	createV2Group(t, store, "oci-group", repository.FormatOCI,
		repository.GroupMember{RepositoryID: proxy.ID, Position: 0},
		repository.GroupMember{RepositoryID: hosted.ID, Position: 1},
	)

	manifest := []byte(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json"}`)
	sum := sha256.Sum256(manifest)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	manifestKey := "native/oci/manifests/oci-hosted/app/" + hex.EncodeToString(sum[:])
	if err := objects.Put(context.Background(), manifestKey, manifest); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PutOCIManifest(context.Background(), repository.OCIManifest{RepositoryID: hosted.ID, Name: "app", Digest: digest, ObjectKey: manifestKey, MediaType: "application/vnd.oci.image.manifest.v1+json", Size: int64(len(manifest))}, "latest"); err != nil {
		t.Fatal(err)
	}
	upstream.bodies["/v2/app/manifests/latest"] = []byte(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json","from":"upstream"}`)

	cache := NewOCICache(NewMemoryOCIObjectStore(), time.Hour, time.Hour, time.Hour, []string{allowedHost})
	handler := NewGatewayHandlerWithOCICache(Dependencies{NativeOCIObjectStore: objects}, store, TestAdapter{}, testAuthenticator(), cache, UpstreamClient{HTTPClient: upstreamServer.Client()})

	req := httptest.NewRequest(http.MethodGet, "/v2/oci-group/app/manifests/latest", nil)
	authorize(req, "resolver-secret")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK || w.Body.String() != string(manifest) {
		t.Fatalf("hosted read=%d body=%q", w.Code, w.Body.String())
	}
	if got := upstream.callCount("/v2/app/manifests/latest"); got != 0 {
		t.Fatalf("upstream calls=%d, want 0 (hosted member preferred)", got)
	}
}

func TestV2GroupOCIManifestFallsBackToProxy(t *testing.T) {
	store := repository.NewMemoryStore()
	objects := NewMemoryOCIObjectStore()
	hosted, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: "oci-hosted", Name: "oci-hosted", Format: repository.FormatOCI})
	if err != nil {
		t.Fatal(err)
	}
	upstream := &proxyUpstream{bodies: map[string][]byte{}, calls: map[string]int{}}
	upstreamServer := httptest.NewServer(upstream)
	defer upstreamServer.Close()
	allowedHost := strings.TrimPrefix(upstreamServer.URL, "http://")
	proxy, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{
		ID: "oci-proxy", Name: "oci-proxy", Format: repository.FormatOCI,
		Type: repository.RepositoryTypeProxy, Endpoint: upstreamServer.URL, AllowedHosts: []string{allowedHost},
	})
	if err != nil {
		t.Fatal(err)
	}
	createV2Group(t, store, "oci-group", repository.FormatOCI,
		repository.GroupMember{RepositoryID: hosted.ID, Position: 0},
		repository.GroupMember{RepositoryID: proxy.ID, Position: 1},
	)

	manifest := []byte(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json","from":"upstream"}`)
	upstream.bodies["/v2/app/manifests/latest"] = manifest

	cache := NewOCICache(NewMemoryOCIObjectStore(), time.Hour, time.Hour, time.Hour, []string{allowedHost})
	handler := NewGatewayHandlerWithOCICache(Dependencies{NativeOCIObjectStore: objects}, store, TestAdapter{}, testAuthenticator(), cache, UpstreamClient{HTTPClient: upstreamServer.Client()})

	get := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/v2/oci-group/app/manifests/latest", nil)
		authorize(req, "resolver-secret")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		return w
	}
	first := get()
	if first.Code != http.StatusOK || first.Body.String() != string(manifest) {
		t.Fatalf("proxy read=%d body=%q", first.Code, first.Body.String())
	}
	if got := upstream.callCount("/v2/app/manifests/latest"); got != 1 {
		t.Fatalf("upstream calls=%d, want 1 (hosted miss falls back to proxy)", got)
	}
	second := get()
	if second.Code != http.StatusOK || second.Body.String() != string(manifest) {
		t.Fatalf("cached read=%d body=%q", second.Code, second.Body.String())
	}
	if got := upstream.callCount("/v2/app/manifests/latest"); got != 1 {
		t.Fatalf("upstream calls after cache=%d, want 1 (cache hit)", got)
	}
}

func TestV2GroupMavenHostedPreferredOverProxy(t *testing.T) {
	store := repository.NewMemoryStore()
	objects := NewMemoryOCIObjectStore()
	hosted, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: "maven-hosted", Name: "maven-hosted", Format: repository.FormatMaven})
	if err != nil {
		t.Fatal(err)
	}
	assetPath := "org/example/widget/1.0.0/widget-1.0.0.jar"
	asset := []byte("hosted-jar")
	sum := sha256.Sum256(asset)
	key := "native/maven/sha256/" + hex.EncodeToString(sum[:])
	if err := objects.Put(context.Background(), key, asset); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateMavenPublishSession(context.Background(), repository.MavenPublishSession{ID: "session", RepositoryID: hosted.ID, Coordinate: "org.example:widget:1.0.0", State: "open", Objects: []repository.MavenDeclaredObject{{Name: "widget-1.0.0.jar", Digest: "sha256:" + hex.EncodeToString(sum[:]), Size: int64(len(asset))}}, ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkMavenPublishObject(context.Background(), "session", "widget-1.0.0.jar", key); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CommitMavenPublishSession(context.Background(), "session", []repository.MavenAsset{{RepositoryID: hosted.ID, Path: assetPath, ObjectKey: key, Digest: "sha256:" + hex.EncodeToString(sum[:]), Size: int64(len(asset))}}); err != nil {
		t.Fatal(err)
	}

	upstream := &proxyUpstream{bodies: map[string][]byte{"/" + assetPath: []byte("upstream-jar")}, calls: map[string]int{}}
	upstreamServer := httptest.NewServer(upstream)
	defer upstreamServer.Close()
	allowedHost := strings.TrimPrefix(upstreamServer.URL, "http://")
	proxy, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{
		ID: "maven-proxy", Name: "maven-proxy", Format: repository.FormatMaven,
		Type: repository.RepositoryTypeProxy, Endpoint: upstreamServer.URL, AllowedHosts: []string{allowedHost},
	})
	if err != nil {
		t.Fatal(err)
	}
	createV2Group(t, store, "maven-group", repository.FormatMaven,
		repository.GroupMember{RepositoryID: proxy.ID, Position: 0},
		repository.GroupMember{RepositoryID: hosted.ID, Position: 1},
	)

	mavenCache := NewMavenCache(NewMemoryOCIObjectStore(), time.Hour, time.Hour, time.Hour, time.Hour, []string{allowedHost})
	handler := NewGatewayHandlerWithCaches(Dependencies{NativeMavenObjectStore: objects}, store, TestAdapter{}, testAuthenticator(), NewDefaultOCICache(NewMemoryOCIObjectStore(), nil), mavenCache, UpstreamClient{HTTPClient: upstreamServer.Client()})

	req := httptest.NewRequest(http.MethodGet, "/maven/maven-group/"+assetPath, nil)
	authorize(req, "resolver-secret")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK || w.Body.String() != "hosted-jar" {
		t.Fatalf("hosted read=%d body=%q", w.Code, w.Body.String())
	}
	if got := upstream.callCount("/" + assetPath); got != 0 {
		t.Fatalf("upstream calls=%d, want 0 (hosted member preferred)", got)
	}
}

func TestV2GroupMavenFallsBackToProxy(t *testing.T) {
	store := repository.NewMemoryStore()
	objects := NewMemoryOCIObjectStore()
	hosted, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: "maven-hosted", Name: "maven-hosted", Format: repository.FormatMaven})
	if err != nil {
		t.Fatal(err)
	}
	assetPath := "org/example/widget/1.0.0/widget-1.0.0.jar"
	upstream := &proxyUpstream{bodies: map[string][]byte{"/" + assetPath: []byte("upstream-jar")}, calls: map[string]int{}}
	upstreamServer := httptest.NewServer(upstream)
	defer upstreamServer.Close()
	allowedHost := strings.TrimPrefix(upstreamServer.URL, "http://")
	proxy, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{
		ID: "maven-proxy", Name: "maven-proxy", Format: repository.FormatMaven,
		Type: repository.RepositoryTypeProxy, Endpoint: upstreamServer.URL, AllowedHosts: []string{allowedHost},
	})
	if err != nil {
		t.Fatal(err)
	}
	createV2Group(t, store, "maven-group", repository.FormatMaven,
		repository.GroupMember{RepositoryID: hosted.ID, Position: 0},
		repository.GroupMember{RepositoryID: proxy.ID, Position: 1},
	)

	mavenCache := NewMavenCache(NewMemoryOCIObjectStore(), time.Hour, time.Hour, time.Hour, time.Hour, []string{allowedHost})
	handler := NewGatewayHandlerWithCaches(Dependencies{NativeMavenObjectStore: objects}, store, TestAdapter{}, testAuthenticator(), NewDefaultOCICache(NewMemoryOCIObjectStore(), nil), mavenCache, UpstreamClient{HTTPClient: upstreamServer.Client()})

	get := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/maven/maven-group/"+assetPath, nil)
		authorize(req, "resolver-secret")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		return w
	}
	first := get()
	if first.Code != http.StatusOK || first.Body.String() != "upstream-jar" {
		t.Fatalf("proxy read=%d body=%q", first.Code, first.Body.String())
	}
	if got := upstream.callCount("/" + assetPath); got != 1 {
		t.Fatalf("upstream calls=%d, want 1 (hosted miss falls back to proxy)", got)
	}
	second := get()
	if second.Code != http.StatusOK || second.Body.String() != "upstream-jar" {
		t.Fatalf("cached read=%d body=%q", second.Code, second.Body.String())
	}
	if got := upstream.callCount("/" + assetPath); got != 1 {
		t.Fatalf("upstream calls after cache=%d, want 1 (cache hit)", got)
	}
}

func TestV2GroupRawHostedPreferredOverProxy(t *testing.T) {
	store := repository.NewMemoryStore()
	objects := NewMemoryOCIObjectStore()
	hosted, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: "raw-hosted", Name: "raw-hosted", Format: repository.FormatRaw})
	if err != nil {
		t.Fatal(err)
	}
	body := []byte("hosted-raw")
	sum := sha256.Sum256(body)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	objectKey := "native/raw/sha256/" + hex.EncodeToString(sum[:])
	if err := objects.Put(context.Background(), objectKey, body); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PutRawAsset(context.Background(), repository.RawAsset{RepositoryID: hosted.ID, Path: "release/app.txt", Digest: digest, ObjectKey: objectKey, Size: int64(len(body)), ContentType: "text/plain"}); err != nil {
		t.Fatal(err)
	}
	proxy, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{
		ID: "raw-proxy", Name: "raw-proxy", Format: repository.FormatRaw,
		Type: repository.RepositoryTypeProxy, Endpoint: "https://proxy.example", AllowedHosts: []string{"proxy.example"},
	})
	if err != nil {
		t.Fatal(err)
	}
	createV2Group(t, store, "raw-group", repository.FormatRaw,
		repository.GroupMember{RepositoryID: proxy.ID, Position: 0},
		repository.GroupMember{RepositoryID: hosted.ID, Position: 1},
	)

	client := &rawFixtureClient{responses: map[string]int{"raw-proxy": http.StatusOK}, body: []byte("upstream-raw")}
	rawCache := NewRawCache(NewMemoryOCIObjectStore(), time.Hour, time.Hour, []string{"proxy.example"})
	handler := NewGatewayHandlerWithRawCache(Dependencies{NativeOCIObjectStore: objects}, store, TestAdapter{}, testAuthenticator(), NewDefaultOCICache(NewMemoryOCIObjectStore(), nil), nil, rawCache, nil, client)

	req := httptest.NewRequest(http.MethodGet, "/raw/raw-group/release/app.txt", nil)
	authorize(req, "resolver-secret")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK || w.Body.String() != "hosted-raw" {
		t.Fatalf("hosted read=%d body=%q", w.Code, w.Body.String())
	}
	if got := client.Calls(); len(got) != 0 {
		t.Fatalf("upstream calls=%v, want none (hosted member preferred)", got)
	}
}

func TestV2GroupRawFallsBackToProxy(t *testing.T) {
	store := repository.NewMemoryStore()
	objects := NewMemoryOCIObjectStore()
	hosted, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: "raw-hosted", Name: "raw-hosted", Format: repository.FormatRaw})
	if err != nil {
		t.Fatal(err)
	}
	proxy, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{
		ID: "raw-proxy", Name: "raw-proxy", Format: repository.FormatRaw,
		Type: repository.RepositoryTypeProxy, Endpoint: "https://proxy.example", AllowedHosts: []string{"proxy.example"},
	})
	if err != nil {
		t.Fatal(err)
	}
	createV2Group(t, store, "raw-group", repository.FormatRaw,
		repository.GroupMember{RepositoryID: hosted.ID, Position: 0},
		repository.GroupMember{RepositoryID: proxy.ID, Position: 1},
	)

	client := &rawFixtureClient{responses: map[string]int{"raw-proxy": http.StatusOK}, body: []byte("upstream-raw")}
	rawCache := NewRawCache(NewMemoryOCIObjectStore(), time.Hour, time.Hour, []string{"proxy.example"})
	handler := NewGatewayHandlerWithRawCache(Dependencies{NativeOCIObjectStore: objects}, store, TestAdapter{}, testAuthenticator(), NewDefaultOCICache(NewMemoryOCIObjectStore(), nil), nil, rawCache, nil, client)

	get := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/raw/raw-group/release/app.txt", nil)
		authorize(req, "resolver-secret")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		return w
	}
	first := get()
	if first.Code != http.StatusOK || first.Body.String() != "upstream-raw" {
		t.Fatalf("proxy read=%d body=%q", first.Code, first.Body.String())
	}
	if got := client.Calls(); len(got) != 1 || got[0] != "raw-proxy" {
		t.Fatalf("upstream calls=%v, want a single fetch through the proxy member", got)
	}
	second := get()
	if second.Code != http.StatusOK || second.Body.String() != "upstream-raw" {
		t.Fatalf("cached read=%d body=%q", second.Code, second.Body.String())
	}
	if got := client.Calls(); len(got) != 1 {
		t.Fatalf("upstream calls after cache=%v, want 1 (cache hit)", got)
	}
}

func TestV2GroupConanHostedPreferredOverProxy(t *testing.T) {
	store := repository.NewMemoryStore()
	objects := NewMemoryOCIObjectStore()
	hosted, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: "conan-hosted", Name: "conan-hosted", Format: repository.FormatConan})
	if err != nil {
		t.Fatal(err)
	}
	body := []byte("recipe")
	sum := sha256.Sum256(body)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	objectKey := "native/conan/objects/recipe"
	if err := store.StageConanObject(context.Background(), repository.ConanObjectIntent{RepositoryID: hosted.ID, ObjectKey: objectKey, Digest: digest, Size: int64(len(body))}); err != nil {
		t.Fatal(err)
	}
	if err := objects.PutVerifiedReader(context.Background(), objectKey, bytes.NewReader(body), int64(len(body)), digest); err != nil {
		t.Fatal(err)
	}
	reference := "pkg/1.0/user/stable"
	if _, err := store.PutConanRecipeRevision(context.Background(), repository.ConanRecipeRevision{RepositoryID: hosted.ID, Reference: reference, Revision: "rrev", Digest: digest}, []repository.ConanAsset{{RepositoryID: hosted.ID, Reference: reference, RecipeRevision: "rrev", Path: "conanfile.py", ObjectKey: objectKey, Digest: digest, Size: int64(len(body))}}); err != nil {
		t.Fatal(err)
	}

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"revisions":[{"revision":"upstream","time":"2024-01-01T00:00:00Z"}]}`))
	}))
	defer upstream.Close()
	proxy, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{
		ID: "conan-proxy", Name: "conan-proxy", Format: repository.FormatConan,
		Type: repository.RepositoryTypeProxy, Endpoint: "https://proxy.example", AllowedHosts: []string{"proxy.example"},
	})
	if err != nil {
		t.Fatal(err)
	}
	createV2Group(t, store, "conan-group", repository.FormatConan,
		repository.GroupMember{RepositoryID: proxy.ID, Position: 0},
		repository.GroupMember{RepositoryID: hosted.ID, Position: 1},
	)

	client := &conanFixtureClient{proxyURL: upstream.URL}
	handler := NewGatewayHandlerWithFormatCaches(Dependencies{NativeConanObjectStore: objects}, store, TestAdapter{}, testAuthenticator(), NewDefaultOCICache(NewMemoryOCIObjectStore(), nil), nil, nil, NewConanCache(nil), nil, conanGatewayClient{client})

	req := httptest.NewRequest(http.MethodGet, "/conan/v2/conan-group/conans/"+reference+"/revisions", nil)
	authorize(req, "resolver-secret")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"revision":"rrev"`) {
		t.Fatalf("hosted read=%d body=%q", w.Code, w.Body.String())
	}
	if client.calls != 0 {
		t.Fatalf("upstream calls=%d, want 0 (hosted member preferred)", client.calls)
	}
}

func TestV2GroupConanFallsBackToProxy(t *testing.T) {
	store := repository.NewMemoryStore()
	objects := NewMemoryOCIObjectStore()
	hosted, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: "conan-hosted", Name: "conan-hosted", Format: repository.FormatConan})
	if err != nil {
		t.Fatal(err)
	}
	revisionsBody := []byte(`{"revisions":[{"revision":"upstream","time":"2024-01-01T00:00:00Z"}]}`)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(revisionsBody)
	}))
	defer upstream.Close()
	proxy, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{
		ID: "conan-proxy", Name: "conan-proxy", Format: repository.FormatConan,
		Type: repository.RepositoryTypeProxy, Endpoint: "https://proxy.example", AllowedHosts: []string{"proxy.example"},
	})
	if err != nil {
		t.Fatal(err)
	}
	createV2Group(t, store, "conan-group", repository.FormatConan,
		repository.GroupMember{RepositoryID: hosted.ID, Position: 0},
		repository.GroupMember{RepositoryID: proxy.ID, Position: 1},
	)

	client := &conanFixtureClient{proxyURL: upstream.URL}
	handler := NewGatewayHandlerWithFormatCaches(Dependencies{NativeConanObjectStore: objects}, store, TestAdapter{}, testAuthenticator(), NewDefaultOCICache(NewMemoryOCIObjectStore(), nil), nil, nil, NewConanCache(nil), nil, conanGatewayClient{client})

	req := httptest.NewRequest(http.MethodGet, "/conan/v2/conan-group/conans/pkg/1.0/user/stable/revisions", nil)
	authorize(req, "resolver-secret")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK || w.Body.String() != string(revisionsBody) {
		t.Fatalf("proxy read=%d body=%q", w.Code, w.Body.String())
	}
	if client.calls != 1 {
		t.Fatalf("upstream calls=%d, want 1 (hosted miss falls back to proxy)", client.calls)
	}
}

func TestV2GroupNameDoesNotShadowV2RepositoryOrLegacyGroup(t *testing.T) {
	store := repository.NewMemoryStore()
	objects := NewMemoryOCIObjectStore()
	// A V2 repository and a V2 group share the same name; the repository must
	// keep priority because the native handler claims the path first.
	repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: "oci-repo", Name: "shared", Format: repository.FormatOCI})
	if err != nil {
		t.Fatal(err)
	}
	createV2Group(t, store, "shared", repository.FormatOCI, repository.GroupMember{RepositoryID: repo.ID, Position: 0})

	manifest := []byte(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json"}`)
	sum := sha256.Sum256(manifest)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	manifestKey := "native/oci/manifests/oci-repo/app/" + hex.EncodeToString(sum[:])
	if err := objects.Put(context.Background(), manifestKey, manifest); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PutOCIManifest(context.Background(), repository.OCIManifest{RepositoryID: repo.ID, Name: "app", Digest: digest, ObjectKey: manifestKey, MediaType: "application/vnd.oci.image.manifest.v1+json", Size: int64(len(manifest))}, "latest"); err != nil {
		t.Fatal(err)
	}
	handler := NewGatewayHandler(Dependencies{NativeOCIObjectStore: objects}, store, TestAdapter{}, testAuthenticator())

	req := httptest.NewRequest(http.MethodGet, "/v2/shared/app/manifests/latest", nil)
	authorize(req, "resolver-secret")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK || w.Body.String() != string(manifest) {
		t.Fatalf("v2 repository read=%d body=%q", w.Code, w.Body.String())
	}

	// A legacy group with no V2 group of the same name keeps resolving through
	// the legacy path.
	legacyStore := repository.NewMemoryStore()
	if _, err := legacyStore.CreateGroup(context.Background(), repository.Group{Name: "legacy", Enabled: true, Members: []repository.Member{{Name: "proxy", Type: repository.MemberProxy, Endpoint: "http://proxy.example", Position: 0}}}); err != nil {
		t.Fatal(err)
	}
	legacyCache := NewOCICache(NewMemoryOCIObjectStore(), time.Hour, time.Hour, time.Hour, []string{"proxy.example"})
	legacyHandler := NewGatewayHandlerWithOCICache(Dependencies{}, legacyStore, TestAdapter{}, testAuthenticator(), legacyCache)
	legacyReq := httptest.NewRequest(http.MethodGet, "/v2/legacy/app/manifests/latest", nil)
	authorize(legacyReq, "resolver-secret")
	legacyW := httptest.NewRecorder()
	legacyHandler.ServeHTTP(legacyW, legacyReq)
	// The legacy proxy member is unreachable, but the request must be resolved
	// by the legacy group path (502 from the upstream fetch), not rejected as
	// an unknown repository name.
	if legacyW.Code != http.StatusBadGateway {
		t.Fatalf("legacy group read=%d body=%q", legacyW.Code, legacyW.Body.String())
	}
}
