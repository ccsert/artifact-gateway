package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

func TestOCIHostedGroupServesManifestAndRange(t *testing.T) {
	manifest := []byte(`{"schemaVersion":2,"config":{"digest":"sha256:config"}}`)
	digest := digestOf(manifest)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.URL.Path, "/v2/team/app/manifests/"+digest; got != want {
			t.Fatalf("path = %q, want %q", got, want)
		}
		user, password, ok := r.BasicAuth()
		if !ok || user != "gitea" || password != "gitea-token" {
			t.Fatal("missing Gitea basic authentication")
		}
		w.Header().Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
		w.Header().Set("Docker-Content-Digest", digest)
		w.Header().Set("Accept-Ranges", "bytes")
		if r.Header.Get("Range") != "" {
			w.Header().Set("Content-Range", "bytes 0-3/"+utoa(uint64(len(manifest))))
			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write(manifest[:4])
			return
		}
		_, _ = w.Write(manifest)
	}))
	defer upstream.Close()

	store := repository.NewMemoryStore()
	_, err := store.CreateGroup(context.Background(), repository.Group{Name: "team", Members: []repository.Member{{Name: "gitea-hosted", Type: repository.MemberHosted, Endpoint: upstream.URL, Position: 0}}})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator(), GiteaClient{Username: "gitea", Token: "gitea-token"})

	challenge := httptest.NewRecorder()
	handler.ServeHTTP(challenge, httptest.NewRequest(http.MethodGet, "/v2/", nil))
	if challenge.Code != http.StatusUnauthorized || !strings.Contains(challenge.Header().Get("WWW-Authenticate"), "/auth/token") {
		t.Fatalf("challenge = %d %q", challenge.Code, challenge.Header().Get("WWW-Authenticate"))
	}
	token := httptest.NewRecorder()
	tokenRequest := httptest.NewRequest(http.MethodGet, "/auth/token", nil)
	tokenRequest.SetBasicAuth("ci", "resolver-secret")
	handler.ServeHTTP(token, tokenRequest)
	if token.Code != http.StatusOK {
		t.Fatalf("token = %d %s", token.Code, token.Body.String())
	}
	clientToken := ociToken(t, token)

	request := httptest.NewRequest(http.MethodGet, "/v2/team/app/manifests/"+digest, nil)
	authorize(request, clientToken)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "application/vnd.oci.image.manifest.v1+json" || response.Header().Get("Docker-Content-Digest") != digest || response.Body.String() != string(manifest) {
		t.Fatalf("manifest = %d headers=%v body=%q", response.Code, response.Header(), response.Body.String())
	}
	if len(store.Audits) != 1 || store.Audits[0].MemberName != "gitea-hosted" || store.Audits[0].Actor != "ci" || store.Audits[0].Repository != "team/app" {
		t.Fatalf("audit = %#v", store.Audits)
	}

	rangeRequest := httptest.NewRequest(http.MethodGet, "/v2/team/app/manifests/"+digest, nil)
	rangeRequest.Header.Set("Range", "bytes=0-3")
	authorize(rangeRequest, clientToken)
	ranged := httptest.NewRecorder()
	handler.ServeHTTP(ranged, rangeRequest)
	if ranged.Code != http.StatusPartialContent || ranged.Header().Get("Content-Range") != "bytes 0-3/"+utoa(uint64(len(manifest))) || ranged.Body.String() != string(manifest[:4]) {
		t.Fatalf("range = %d headers=%v body=%q", ranged.Code, ranged.Header(), ranged.Body.String())
	}
}

func TestOCIRangeIsServedFromVerifiedFullResponse(t *testing.T) {
	content := []byte("verified blob content")
	digest := digestOf(content)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Range"); got != "" {
			t.Fatalf("upstream range = %q, want none", got)
		}
		w.Header().Set("Docker-Content-Digest", digest)
		_, _ = w.Write(content)
	}))
	defer upstream.Close()
	store := repository.NewMemoryStore()
	_, _ = store.CreateGroup(context.Background(), repository.Group{Name: "team", Members: []repository.Member{{Name: "gitea", Type: repository.MemberHosted, Endpoint: upstream.URL, Position: 0}}})
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator(), GiteaClient{Username: "gitea", Token: "gitea-token"})
	request := httptest.NewRequest(http.MethodGet, "/v2/team/app/blobs/"+digest, nil)
	request.Header.Set("Range", "bytes=0-7")
	authorize(request, "resolver-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusPartialContent || response.Body.String() != string(content[:8]) || response.Header().Get("Content-Range") != "bytes 0-7/"+utoa(uint64(len(content))) {
		t.Fatalf("range = %d headers=%v body=%q", response.Code, response.Header(), response.Body.String())
	}
}

func TestOCIRejectsDigestMismatchAndSupportsHead(t *testing.T) {
	content := []byte("wrong-content")
	expected := "sha256:" + strings.Repeat("0", 64)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Docker-Content-Digest", expected)
		if r.Method == http.MethodHead {
			return
		}
		_, _ = w.Write(content)
	}))
	defer upstream.Close()
	store := repository.NewMemoryStore()
	_, _ = store.CreateGroup(context.Background(), repository.Group{Name: "team", Members: []repository.Member{{Name: "gitea", Type: repository.MemberHosted, Endpoint: upstream.URL, Position: 0}}})
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator(), GiteaClient{})
	request := httptest.NewRequest(http.MethodGet, "/v2/team/app/blobs/"+expected, nil)
	authorize(request, "resolver-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadGateway || !strings.Contains(response.Body.String(), "DIGEST_INVALID") {
		t.Fatalf("mismatch = %d %s", response.Code, response.Body.String())
	}

	head := httptest.NewRequest(http.MethodHead, "/v2/team/app/blobs/"+expected, nil)
	authorize(head, "resolver-secret")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, head)
	if response.Code != http.StatusOK || response.Header().Get("Docker-Content-Digest") != expected || response.Body.Len() != 0 {
		t.Fatalf("head = %d %q", response.Code, response.Body.String())
	}
}

func TestOCIHostedGroupServesTaggedManifest(t *testing.T) {
	manifest := []byte(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json"}`)
	digest := digestOf(manifest)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.URL.Path, "/v2/team/app/manifests/latest"; got != want {
			t.Fatalf("path = %q, want %q", got, want)
		}
		w.Header().Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
		w.Header().Set("Docker-Content-Digest", digest)
		_, _ = w.Write(manifest)
	}))
	defer upstream.Close()

	store := repository.NewMemoryStore()
	_, err := store.CreateGroup(context.Background(), repository.Group{Name: "team", Members: []repository.Member{{Name: "gitea-hosted", Type: repository.MemberHosted, Endpoint: upstream.URL, Position: 0}}})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator(), GiteaClient{Username: "gitea", Token: "gitea-token"})
	request := httptest.NewRequest(http.MethodGet, "/v2/team/app/manifests/latest", nil)
	authorize(request, "resolver-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK || response.Header().Get("Docker-Content-Digest") != digest || response.Body.String() != string(manifest) {
		t.Fatalf("manifest = %d headers=%v body=%q", response.Code, response.Header(), response.Body.String())
	}
}

func TestOCIHostedGroupPrefersHostedMember(t *testing.T) {
	manifest := []byte(`{"schemaVersion":2}`)
	digest := digestOf(manifest)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Docker-Content-Digest", digest)
		_, _ = w.Write(manifest)
	}))
	defer upstream.Close()

	store := repository.NewMemoryStore()
	_, err := store.CreateGroup(context.Background(), repository.Group{Name: "team", Members: []repository.Member{
		{Name: "proxy-first", Type: repository.MemberProxy, Endpoint: "http://proxy.example", Position: 0},
		{Name: "gitea-hosted", Type: repository.MemberHosted, Endpoint: upstream.URL, Position: 1},
	}})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator(), GiteaClient{Username: "gitea", Token: "gitea-token"})
	request := httptest.NewRequest(http.MethodGet, "/v2/team/app/manifests/latest", nil)
	authorize(request, "resolver-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK || response.Body.String() != string(manifest) {
		t.Fatalf("manifest = %d %q", response.Code, response.Body.String())
	}
	if got := store.Audits[len(store.Audits)-1].MemberName; got != "gitea-hosted" {
		t.Fatalf("audit member = %q", got)
	}
}

func TestOCITriesProxyAfterHostedMiss(t *testing.T) {
	manifest := []byte(`{"schemaVersion":2}`)
	digest := digestOf(manifest)
	proxyAvailable := true
	hosted := httptest.NewServer(http.NotFoundHandler())
	defer hosted.Close()
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !proxyAvailable {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		if _, _, ok := r.BasicAuth(); ok {
			t.Fatal("proxy received Gitea credentials")
		}
		w.Header().Set("Docker-Content-Digest", digest)
		_, _ = w.Write(manifest)
	}))
	defer proxy.Close()
	store := repository.NewMemoryStore()
	_, _ = store.CreateGroup(context.Background(), repository.Group{Name: "team", Members: []repository.Member{
		{Name: "gitea-hosted", Type: repository.MemberHosted, Endpoint: hosted.URL, Position: 0},
		{Name: "proxy", Type: repository.MemberProxy, Endpoint: proxy.URL, Position: 1},
	}})
	handler := NewGatewayHandlerWithOCICache(Dependencies{}, store, TestAdapter{}, testAuthenticator(), NewDefaultOCICache(NewMemoryOCIObjectStore(), []string{strings.TrimPrefix(proxy.URL, "http://")}), GiteaClient{Username: "gitea", Token: "gitea-token"})
	request := httptest.NewRequest(http.MethodGet, "/v2/team/app/manifests/latest", nil)
	authorize(request, "resolver-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK || response.Body.String() != string(manifest) {
		t.Fatalf("manifest = %d %q", response.Code, response.Body.String())
	}
	if got := store.Audits[len(store.Audits)-1].MemberName; got != "proxy" {
		t.Fatalf("audit member = %q", got)
	}
	proxyAvailable = false
	cachedRequest := httptest.NewRequest(http.MethodGet, "/v2/team/app/manifests/latest", nil)
	authorize(cachedRequest, "resolver-secret")
	cachedResponse := httptest.NewRecorder()
	handler.ServeHTTP(cachedResponse, cachedRequest)
	if cachedResponse.Code != http.StatusOK || cachedResponse.Body.String() != string(manifest) {
		t.Fatalf("cached manifest = %d %q", cachedResponse.Code, cachedResponse.Body.String())
	}
}

func TestOCITriesProxyAfterHostedFailure(t *testing.T) {
	manifest := []byte(`{"schemaVersion":2}`)
	digest := digestOf(manifest)
	hosted := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) }))
	defer hosted.Close()
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Docker-Content-Digest", digest)
		_, _ = w.Write(manifest)
	}))
	defer proxy.Close()
	store := repository.NewMemoryStore()
	_, _ = store.CreateGroup(context.Background(), repository.Group{Name: "team", Members: []repository.Member{
		{Name: "gitea-hosted", Type: repository.MemberHosted, Endpoint: hosted.URL, Position: 0},
		{Name: "proxy", Type: repository.MemberProxy, Endpoint: proxy.URL, Position: 1},
	}})
	metrics := &Metrics{}
	handler := OCIHandler{Resolver: Resolver{Store: store, Adapter: TestAdapter{}, Metrics: metrics}, Client: GiteaClient{}, Authenticator: testAuthenticator()}
	request := httptest.NewRequest(http.MethodGet, "/v2/team/app/manifests/latest", nil)
	authorize(request, "resolver-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK || response.Body.String() != string(manifest) {
		t.Fatalf("manifest = %d %q", response.Code, response.Body.String())
	}
	if len(store.Audits) != 2 || store.Audits[0].MemberName != "gitea-hosted" || store.Audits[0].Outcome != repository.AuditUpstreamError || store.Audits[1].MemberName != "proxy" || store.Audits[1].Outcome != repository.AuditResolved {
		t.Fatalf("audits = %#v", store.Audits)
	}
	if metrics.failed.Load() != 0 || metrics.resolved.Load() != 1 {
		t.Fatalf("metrics = failed:%d resolved:%d", metrics.failed.Load(), metrics.resolved.Load())
	}
}

func TestOCITokensAuditLoginSubject(t *testing.T) {
	manifest := []byte(`{"schemaVersion":2}`)
	digest := digestOf(manifest)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Docker-Content-Digest", digest)
		_, _ = w.Write(manifest)
	}))
	defer upstream.Close()
	store := repository.NewMemoryStore()
	_, _ = store.CreateGroup(context.Background(), repository.Group{Name: "team", Members: []repository.Member{{Name: "gitea", Type: repository.MemberHosted, Endpoint: upstream.URL, Position: 0}}})
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator(), GiteaClient{})
	for _, username := range []string{"alice", "bob"} {
		tokenResponse := httptest.NewRecorder()
		tokenRequest := httptest.NewRequest(http.MethodGet, "/auth/token", nil)
		tokenRequest.SetBasicAuth(username, "resolver-secret")
		handler.ServeHTTP(tokenResponse, tokenRequest)
		request := httptest.NewRequest(http.MethodGet, "/v2/team/app/manifests/latest", nil)
		authorize(request, ociToken(t, tokenResponse))
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("%s response = %d", username, response.Code)
		}
	}
	if len(store.Audits) != 2 || store.Audits[0].Actor != "alice" || store.Audits[1].Actor != "bob" {
		t.Fatalf("audits = %#v", store.Audits)
	}
}

func TestOCIRejectsTaggedManifestWithMismatchedDigest(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Docker-Content-Digest", digestOf([]byte("different manifest")))
		_, _ = w.Write([]byte(`{"schemaVersion":2}`))
	}))
	defer upstream.Close()
	store := repository.NewMemoryStore()
	_, _ = store.CreateGroup(context.Background(), repository.Group{Name: "team", Members: []repository.Member{{Name: "gitea", Type: repository.MemberHosted, Endpoint: upstream.URL, Position: 0}}})
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator(), GiteaClient{})
	request := httptest.NewRequest(http.MethodGet, "/v2/team/app/manifests/latest", nil)
	authorize(request, "resolver-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusBadGateway || !strings.Contains(response.Body.String(), "DIGEST_INVALID") {
		t.Fatalf("response = %d %s", response.Code, response.Body.String())
	}
}

func TestOCIErrorContract(t *testing.T) {
	store := repository.NewMemoryStore()
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator(), GiteaClient{})
	tests := []struct {
		name   string
		method string
		path   string
		token  string
		status int
		code   string
	}{
		{name: "unauthenticated", method: http.MethodGet, path: "/v2/team/app/manifests/latest", status: http.StatusUnauthorized, code: "UNAUTHORIZED"},
		{name: "unsupported method", method: http.MethodPost, path: "/v2/team/app/manifests/latest", status: http.StatusMethodNotAllowed, code: "UNSUPPORTED"},
		{name: "malformed path", method: http.MethodGet, path: "/v2/team/app/tags/list", token: "resolver-secret", status: http.StatusNotFound, code: "NAME_UNKNOWN"},
		{name: "unknown manifest", method: http.MethodGet, path: "/v2/team/app/manifests/latest", token: "resolver-secret", status: http.StatusNotFound, code: "NAME_UNKNOWN"},
		{name: "unknown blob", method: http.MethodGet, path: "/v2/team/app/blobs/sha256:abc", token: "resolver-secret", status: http.StatusNotFound, code: "NAME_UNKNOWN"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(test.method, test.path, nil)
			if test.token != "" {
				authorize(request, test.token)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.status || !strings.Contains(response.Body.String(), test.code) {
				t.Fatalf("response = %d %s", response.Code, response.Body.String())
			}
		})
	}
}

func TestOCIMapsUpstreamNotFoundByResource(t *testing.T) {
	upstream := httptest.NewServer(http.NotFoundHandler())
	defer upstream.Close()
	store := repository.NewMemoryStore()
	_, _ = store.CreateGroup(context.Background(), repository.Group{Name: "team", Members: []repository.Member{{Name: "gitea", Type: repository.MemberHosted, Endpoint: upstream.URL, Position: 0}}})
	authenticator := testAuthenticator()
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, authenticator, GiteaClient{})
	for _, test := range []struct {
		path string
		code string
	}{
		{path: "/v2/team/app/manifests/latest", code: "MANIFEST_UNKNOWN"},
		{path: "/v2/team/app/blobs/sha256:abc", code: "BLOB_UNKNOWN"},
	} {
		request := httptest.NewRequest(http.MethodGet, test.path, nil)
		authorize(request, authenticator.IssueToken("alice"))
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusNotFound || !strings.Contains(response.Body.String(), test.code) {
			t.Fatalf("%s response = %d %s", test.path, response.Code, response.Body.String())
		}
	}
	if len(store.Audits) != 2 {
		t.Fatalf("audits = %#v", store.Audits)
	}
	for _, audit := range store.Audits {
		if audit.Outcome != repository.AuditNotFound || audit.MemberName != "gitea" || audit.Actor != "alice" || audit.Repository != "team/app" {
			t.Fatalf("audit = %#v", audit)
		}
	}
}

func TestOCIAuditsUpstreamTransportFailure(t *testing.T) {
	store := repository.NewMemoryStore()
	_, _ = store.CreateGroup(context.Background(), repository.Group{Name: "team", Members: []repository.Member{{Name: "gitea", Type: repository.MemberHosted, Endpoint: "://invalid", Position: 0}}})
	authenticator := testAuthenticator()
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, authenticator, GiteaClient{})
	request := httptest.NewRequest(http.MethodGet, "/v2/team/app/manifests/latest", nil)
	authorize(request, authenticator.IssueToken("alice"))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusBadGateway {
		t.Fatalf("response = %d %s", response.Code, response.Body.String())
	}
	if len(store.Audits) != 1 || store.Audits[0].Outcome != repository.AuditUpstreamError || store.Audits[0].MemberName != "gitea" || store.Audits[0].Actor != "alice" || store.Audits[0].Repository != "team/app" {
		t.Fatalf("audits = %#v", store.Audits)
	}
}

func TestOCIResolutionFailuresIncrementMetrics(t *testing.T) {
	store := repository.NewMemoryStore()
	_, _ = store.CreateGroup(context.Background(), repository.Group{Name: "disabled", Members: []repository.Member{{Name: "gitea", Type: repository.MemberHosted, Endpoint: "http://gitea", Position: 0}}})
	if err := store.DisableGroup(context.Background(), "disabled"); err != nil {
		t.Fatal(err)
	}
	_, _ = store.CreateGroup(context.Background(), repository.Group{Name: "empty"})
	metrics := &Metrics{}
	resolver := Resolver{Store: store, Adapter: TestAdapter{}, Metrics: metrics}
	for _, groupName := range []string{"missing", "disabled", "empty"} {
		if _, err := resolver.ResolveOCIMembers(context.Background(), groupName, "team/app", "alice"); err == nil {
			t.Fatalf("ResolveOCIMembers(%q) error = nil", groupName)
		}
	}
	if got := metrics.failed.Load(); got != 3 {
		t.Fatalf("failed metric = %d, want 3", got)
	}
}

func digestOf(content []byte) string {
	sum := sha256.Sum256(content)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func ociToken(t *testing.T, response *httptest.ResponseRecorder) string {
	t.Helper()
	var payload struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil || payload.Token == "" {
		t.Fatalf("token payload = %q, error = %v", response.Body.String(), err)
	}
	return payload.Token
}
