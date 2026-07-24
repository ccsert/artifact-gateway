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
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

func TestOCILegacyGroupServesManifestAndRange(t *testing.T) {
	manifest := []byte(`{"schemaVersion":2,"config":{"digest":"sha256:config"}}`)
	digest := digestOf(manifest)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.URL.Path, "/v2/team/app/manifests/"+digest; got != want {
			t.Fatalf("path = %q, want %q", got, want)
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
	_, err := store.CreateGroup(context.Background(), repository.Group{Name: "team", Members: []repository.Member{{Name: "legacy", Type: repository.MemberHosted, Endpoint: upstream.URL, Position: 0}}})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator(), UpstreamClient{})

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
	if len(store.Audits) != 1 || store.Audits[0].MemberName != "legacy" || store.Audits[0].Actor != "ci" || store.Audits[0].Repository != "team/app" {
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

func TestGatewayPropagatesTraceContextToOCIUpstream(t *testing.T) {
	provider := sdktrace.NewTracerProvider(sdktrace.WithSampler(sdktrace.AlwaysSample()))
	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() {
		_ = provider.Shutdown(context.Background())
		otel.SetTracerProvider(noop.NewTracerProvider())
		otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator())
	})
	content := []byte(`{"schemaVersion":2}`)
	digest := digestOf(content)
	traceparent := ""
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		traceparent = request.Header.Get("traceparent")
		w.Header().Set("Docker-Content-Digest", digest)
		_, _ = w.Write(content)
	}))
	defer upstream.Close()
	store := repository.NewMemoryStore()
	_, _ = store.CreateGroup(context.Background(), repository.Group{Name: "team", Members: []repository.Member{{Name: "hosted", Type: repository.MemberHosted, Endpoint: upstream.URL, Position: 0}}})
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator(), UpstreamClient{})
	request := httptest.NewRequest(http.MethodGet, "/v2/team/app/manifests/latest", nil)
	request.Header.Set("traceparent", "00-0123456789abcdef0123456789abcdef-0123456789abcdef-01")
	authorize(request, "resolver-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(traceparent, "0123456789abcdef0123456789abcdef") {
		t.Fatalf("response=%d traceparent=%q", response.Code, traceparent)
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
	_, _ = store.CreateGroup(context.Background(), repository.Group{Name: "team", Members: []repository.Member{{Name: "legacy", Type: repository.MemberHosted, Endpoint: upstream.URL, Position: 0}}})
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator(), UpstreamClient{})
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
	_, _ = store.CreateGroup(context.Background(), repository.Group{Name: "team", Members: []repository.Member{{Name: "legacy", Type: repository.MemberHosted, Endpoint: upstream.URL, Position: 0}}})
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator(), UpstreamClient{})
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

func TestOCIDenylistedProxyReturnsForbiddenAndAudits(t *testing.T) {
	store := repository.NewMemoryStore()
	_, _ = store.CreateGroup(context.Background(), repository.Group{Name: "team", Members: []repository.Member{{Name: "blocked", Type: repository.MemberProxy, Endpoint: "https://blocked.example", Position: 0}}})
	cache := NewDefaultOCICache(NewMemoryOCIObjectStore(), []string{"allowed.example"})
	handler := OCIHandler{Resolver: Resolver{Store: store, Adapter: TestAdapter{}, Metrics: &Metrics{}}, Client: UpstreamClient{}, Authenticator: testAuthenticator(), Cache: cache}
	request := httptest.NewRequest(http.MethodGet, "/v2/team/app/manifests/latest", nil)
	authorize(request, "resolver-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), "DENIED") {
		t.Fatalf("response = %d %s", response.Code, response.Body.String())
	}
	if len(store.Audits) != 1 || store.Audits[0].Outcome != repository.AuditProxyDenied || store.Audits[0].Repository != "team/app" {
		t.Fatalf("audits = %#v", store.Audits)
	}
}

func TestOCICachedProxyIsDeniedAfterPolicyRevocation(t *testing.T) {
	content := []byte(`{"schemaVersion":2}`)
	client := &countingOCIClient{content: content, status: http.StatusOK}
	store := repository.NewMemoryStore()
	_, _ = store.CreateGroup(context.Background(), repository.Group{Name: "team", Members: []repository.Member{{Name: "proxy", Type: repository.MemberProxy, Endpoint: "https://registry.example", Position: 0}}})
	cache := NewDefaultOCICache(NewMemoryOCIObjectStore(), []string{"registry.example"})
	metrics := &Metrics{}
	handler := OCIHandler{Resolver: Resolver{Store: store, Adapter: TestAdapter{}, Metrics: metrics}, Client: client, Authenticator: testAuthenticator(), Cache: cache}
	request := httptest.NewRequest(http.MethodGet, "/v2/team/app/manifests/latest", nil)
	authorize(request, "resolver-secret")
	first := httptest.NewRecorder()
	handler.ServeHTTP(first, request)
	if first.Code != http.StatusOK || client.Calls() != 1 {
		t.Fatalf("first response = %d calls=%d", first.Code, client.Calls())
	}
	cache.allowedProxyHost = map[string]struct{}{}
	second := httptest.NewRecorder()
	handler.ServeHTTP(second, request)
	if second.Code != http.StatusForbidden || client.Calls() != 1 {
		t.Fatalf("revoked response = %d calls=%d", second.Code, client.Calls())
	}
	if len(store.Audits) != 2 || store.Audits[1].Outcome != repository.AuditProxyDenied {
		t.Fatalf("audits = %#v", store.Audits)
	}
	if metrics.ociProxyDenied.Load() != 1 {
		t.Fatalf("proxy denials = %d", metrics.ociProxyDenied.Load())
	}
}

func TestOCIUsesManagedGrantsForBoundMembersAndCachedSources(t *testing.T) {
	content := []byte(`{"schemaVersion":2}`)
	store := repository.NewMemoryStore()
	firstRepository, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: "oci-first", Name: "oci-first", Format: repository.FormatOCI})
	if err != nil {
		t.Fatal(err)
	}
	secondRepository, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: "oci-second", Name: "oci-second", Format: repository.FormatOCI})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReplaceRepositoryGrants(context.Background(), firstRepository.ID, []repository.RepositoryGrant{{Principal: "build-agent", Scopes: []string{"repositories:read"}}}, "1"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReplaceRepositoryGrants(context.Background(), secondRepository.ID, []repository.RepositoryGrant{{Principal: "build-agent", Scopes: []string{"repositories:read"}}}, "1"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateGroup(context.Background(), repository.Group{Name: "team", Members: []repository.Member{
		{Name: "first", Type: repository.MemberProxy, Endpoint: "https://first.example", Position: 0, RepositoryID: firstRepository.ID},
		{Name: "second", Type: repository.MemberProxy, Endpoint: "https://second.example", Position: 1, RepositoryID: secondRepository.ID},
	}}); err != nil {
		t.Fatal(err)
	}
	authenticator := Authenticator{ResolverToken: "resolver-secret", ResolverActor: "build-agent", RepositoryReaders: map[string][]string{"build-agent": {"team/app"}}}
	client := &countingOCIClient{content: content, status: http.StatusOK}
	metrics := &Metrics{}
	cache := NewDefaultOCICache(NewMemoryOCIObjectStore(), []string{"first.example", "second.example"})
	handler := OCIHandler{Resolver: Resolver{Store: store, Adapter: TestAdapter{}, Metrics: metrics}, Repositories: store, Authorizer: RepositoryAuthorizer{Grants: store, Legacy: authenticator}, Client: client, Authenticator: authenticator, Cache: cache}
	request := func() *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, "/v2/team/app/manifests/latest", nil)
		authorize(r, "resolver-secret")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	if response := request(); response.Code != http.StatusOK || strings.Join(client.Members(), ",") != "first" {
		t.Fatalf("first response=%d members=%v", response.Code, client.Members())
	}
	if _, err := store.ReplaceRepositoryGrants(context.Background(), firstRepository.ID, nil, "2"); err != nil {
		t.Fatal(err)
	}
	if response := request(); response.Code != http.StatusOK || strings.Join(client.Members(), ",") != "first,second" {
		t.Fatalf("fallback response=%d members=%v", response.Code, client.Members())
	}
	if _, err := store.ReplaceRepositoryGrants(context.Background(), secondRepository.ID, nil, "2"); err != nil {
		t.Fatal(err)
	}
	if response := request(); response.Code != http.StatusForbidden || client.Calls() != 2 {
		t.Fatalf("denied response=%d calls=%d", response.Code, client.Calls())
	}
	var foundGrantAudit bool
	for _, audit := range store.Audits {
		if audit.MemberName == "first" && audit.AuthorizationSource == "repository_grants" && audit.AuthorizationReason == "scope_not_granted" {
			foundGrantAudit = true
		}
	}
	if !foundGrantAudit {
		t.Fatalf("audits=%#v", store.Audits)
	}
	metricResponse := httptest.NewRecorder()
	metrics.Handler(metricResponse, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if !strings.Contains(metricResponse.Body.String(), `artifact_gateway_repository_authorization_denials_total{format="oci",authorization_source="repository_grants",authorization_reason="scope_not_granted"} 3`) {
		t.Fatalf("authorization metrics=%s", metricResponse.Body.String())
	}
}

func TestOCICacheWithoutEndpointProvenanceIsRefetched(t *testing.T) {
	content := []byte(`{"schemaVersion":2}`)
	client := &countingOCIClient{content: content, status: http.StatusOK}
	store := repository.NewMemoryStore()
	_, _ = store.CreateGroup(context.Background(), repository.Group{Name: "team", Members: []repository.Member{{Name: "proxy", Type: repository.MemberProxy, Endpoint: "https://registry.example", Position: 0}}})
	objects := NewMemoryOCIObjectStore()
	cache := NewDefaultOCICache(objects, []string{"registry.example"})
	handler := OCIHandler{Resolver: Resolver{Store: store, Adapter: TestAdapter{}, Metrics: &Metrics{}}, Client: client, Authenticator: testAuthenticator(), Cache: cache}
	request := httptest.NewRequest(http.MethodGet, "/v2/team/app/manifests/latest", nil)
	authorize(request, "resolver-secret")
	first := httptest.NewRecorder()
	handler.ServeHTTP(first, request)
	key := cache.key("team", "team/app", ociManifest, "latest")
	encoded, err := objects.Get(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	var index cachedOCIIndex
	if err := json.Unmarshal(encoded, &index); err != nil {
		t.Fatal(err)
	}
	index.Endpoint = ""
	encoded, err = json.Marshal(index)
	if err != nil {
		t.Fatal(err)
	}
	if err := objects.Put(context.Background(), key, encoded); err != nil {
		t.Fatal(err)
	}
	second := httptest.NewRecorder()
	handler.ServeHTTP(second, request)
	if second.Code != http.StatusOK || client.Calls() != 2 {
		t.Fatalf("response = %d calls=%d", second.Code, client.Calls())
	}
}

func TestOCIDenylistedProxyOverridesEarlierUpstreamFailure(t *testing.T) {
	client := &countingOCIClient{status: http.StatusInternalServerError}
	store := repository.NewMemoryStore()
	_, _ = store.CreateGroup(context.Background(), repository.Group{Name: "team", Members: []repository.Member{{Name: "failing", Type: repository.MemberProxy, Endpoint: "https://allowed.example", Position: 0}, {Name: "blocked", Type: repository.MemberProxy, Endpoint: "https://blocked.example", Position: 1}}})
	cache := NewDefaultOCICache(NewMemoryOCIObjectStore(), []string{"allowed.example"})
	handler := OCIHandler{Resolver: Resolver{Store: store, Adapter: TestAdapter{}, Metrics: &Metrics{}}, Client: client, Authenticator: testAuthenticator(), Cache: cache}
	request := httptest.NewRequest(http.MethodGet, "/v2/team/app/manifests/latest", nil)
	authorize(request, "resolver-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("response = %d %s", response.Code, response.Body.String())
	}
	if len(store.Audits) != 2 || store.Audits[1].Outcome != repository.AuditProxyDenied {
		t.Fatalf("audits = %#v", store.Audits)
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
	_, err := store.CreateGroup(context.Background(), repository.Group{Name: "team", Members: []repository.Member{{Name: "legacy-hosted", Type: repository.MemberHosted, Endpoint: upstream.URL, Position: 0}}})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator(), UpstreamClient{})
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
		{Name: "legacy-hosted", Type: repository.MemberHosted, Endpoint: upstream.URL, Position: 1},
	}})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator(), UpstreamClient{})
	request := httptest.NewRequest(http.MethodGet, "/v2/team/app/manifests/latest", nil)
	authorize(request, "resolver-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK || response.Body.String() != string(manifest) {
		t.Fatalf("manifest = %d %q", response.Code, response.Body.String())
	}
	if got := store.Audits[len(store.Audits)-1].MemberName; got != "legacy-hosted" {
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
			t.Fatal("proxy received Legacy credentials")
		}
		w.Header().Set("Docker-Content-Digest", digest)
		_, _ = w.Write(manifest)
	}))
	defer proxy.Close()
	store := repository.NewMemoryStore()
	_, _ = store.CreateGroup(context.Background(), repository.Group{Name: "team", Members: []repository.Member{
		{Name: "legacy-hosted", Type: repository.MemberHosted, Endpoint: hosted.URL, Position: 0},
		{Name: "proxy", Type: repository.MemberProxy, Endpoint: proxy.URL, Position: 1},
	}})
	handler := NewGatewayHandlerWithOCICache(Dependencies{}, store, TestAdapter{}, testAuthenticator(), NewDefaultOCICache(NewMemoryOCIObjectStore(), []string{strings.TrimPrefix(proxy.URL, "http://")}), UpstreamClient{})
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
		{Name: "legacy-hosted", Type: repository.MemberHosted, Endpoint: hosted.URL, Position: 0},
		{Name: "proxy", Type: repository.MemberProxy, Endpoint: proxy.URL, Position: 1},
	}})
	metrics := &Metrics{}
	handler := OCIHandler{Resolver: Resolver{Store: store, Adapter: TestAdapter{}, Metrics: metrics}, Client: UpstreamClient{}, Authenticator: testAuthenticator()}
	request := httptest.NewRequest(http.MethodGet, "/v2/team/app/manifests/latest", nil)
	authorize(request, "resolver-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK || response.Body.String() != string(manifest) {
		t.Fatalf("manifest = %d %q", response.Code, response.Body.String())
	}
	if len(store.Audits) != 2 || store.Audits[0].MemberName != "legacy-hosted" || store.Audits[0].Outcome != repository.AuditUpstreamError || store.Audits[1].MemberName != "proxy" || store.Audits[1].Outcome != repository.AuditResolved {
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
	_, _ = store.CreateGroup(context.Background(), repository.Group{Name: "team", Members: []repository.Member{{Name: "legacy", Type: repository.MemberHosted, Endpoint: upstream.URL, Position: 0}}})
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator(), UpstreamClient{})
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
	_, _ = store.CreateGroup(context.Background(), repository.Group{Name: "team", Members: []repository.Member{{Name: "legacy", Type: repository.MemberHosted, Endpoint: upstream.URL, Position: 0}}})
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator(), UpstreamClient{})
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
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator(), UpstreamClient{})
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
	_, _ = store.CreateGroup(context.Background(), repository.Group{Name: "team", Members: []repository.Member{{Name: "legacy", Type: repository.MemberHosted, Endpoint: upstream.URL, Position: 0}}})
	authenticator := testAuthenticator()
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, authenticator, UpstreamClient{})
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
		if audit.Outcome != repository.AuditNotFound || audit.MemberName != "legacy" || audit.Actor != "alice" || audit.Repository != "team/app" {
			t.Fatalf("audit = %#v", audit)
		}
	}
}

func TestOCIAuditsUpstreamTransportFailure(t *testing.T) {
	store := repository.NewMemoryStore()
	_, _ = store.CreateGroup(context.Background(), repository.Group{Name: "team", Members: []repository.Member{{Name: "legacy", Type: repository.MemberHosted, Endpoint: "://invalid", Position: 0}}})
	authenticator := testAuthenticator()
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, authenticator, UpstreamClient{})
	request := httptest.NewRequest(http.MethodGet, "/v2/team/app/manifests/latest", nil)
	authorize(request, authenticator.IssueToken("alice"))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusBadGateway {
		t.Fatalf("response = %d %s", response.Code, response.Body.String())
	}
	if len(store.Audits) != 1 || store.Audits[0].Outcome != repository.AuditUpstreamError || store.Audits[0].MemberName != "legacy" || store.Audits[0].Actor != "alice" || store.Audits[0].Repository != "team/app" {
		t.Fatalf("audits = %#v", store.Audits)
	}
}

func TestOCIRepositoryPermissionRejectsAndAuditsDeniedRead(t *testing.T) {
	store := repository.NewMemoryStore()
	authenticator := testAuthenticator()
	authenticator.RepositoryReaders = map[string][]string{"alice": {"team/allowed/*"}}
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, authenticator, UpstreamClient{})
	request := httptest.NewRequest(http.MethodGet, "/v2/team/private/manifests/latest", nil)
	authorize(request, authenticator.IssueToken("alice"))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), "DENIED") {
		t.Fatalf("response = %d %s", response.Code, response.Body.String())
	}
	if len(store.Audits) != 1 || store.Audits[0].Outcome != repository.AuditAccessDenied || store.Audits[0].Actor != "alice" || store.Audits[0].Repository != "team/private" {
		t.Fatalf("audits = %#v", store.Audits)
	}
}

func TestOCIResolutionFailuresIncrementMetrics(t *testing.T) {
	store := repository.NewMemoryStore()
	_, _ = store.CreateGroup(context.Background(), repository.Group{Name: "disabled", Members: []repository.Member{{Name: "legacy", Type: repository.MemberHosted, Endpoint: "http://legacy", Position: 0}}})
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
