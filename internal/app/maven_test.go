package app

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

type countingMavenClient struct {
	mu       sync.Mutex
	calls    int
	members  []string
	status   int
	statuses []int
	body     []byte
	err      error
}

func (c *countingMavenClient) FetchMaven(_ context.Context, method string, member repository.Member, _ string, _ http.Header) (*http.Response, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	c.members = append(c.members, member.Name)
	if c.err != nil {
		return nil, c.err
	}
	status := c.status
	if index := c.calls - 1; index < len(c.statuses) {
		status = c.statuses[index]
	}
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/java-archive"}, "ETag": []string{`"fixture"`}, "Last-Modified": []string{"Wed, 01 Jan 2025 00:00:00 GMT"}}, Body: io.NopCloser(bytes.NewReader(c.body)), Request: &http.Request{Method: method}}, nil
}

func (c *countingMavenClient) Calls() int { c.mu.Lock(); defer c.mu.Unlock(); return c.calls }

func (c *countingMavenClient) Members() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.members...)
}

func TestUpstreamMavenClientSendsMavenUserAgent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ua := r.Header.Get("User-Agent"); !strings.Contains(ua, "Apache-Maven/") || !strings.Contains(ua, "Artifact-Gateway/") {
			t.Fatalf("User-Agent = %q", ua)
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)

	response, err := (UpstreamClient{HTTPClient: server.Client()}).FetchMaven(context.Background(), http.MethodGet, repository.Member{Endpoint: server.URL}, "org/example/widget/1.0/widget-1.0.pom", nil)
	if err != nil {
		t.Fatalf("FetchMaven returned error: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", response.StatusCode)
	}
}

func TestMavenProxyCacheCachesComponentsAndMetadataSeparately(t *testing.T) {
	store := repository.NewMemoryStore()
	_, _ = store.CreateMavenGroup(context.Background(), repository.Group{Name: "engineering", Members: []repository.Member{{Name: "central", Type: repository.MemberProxy, Endpoint: "https://repo.example", Position: 0}}})
	client := &countingMavenClient{status: http.StatusOK, body: []byte("jar")}
	cache := NewMavenCache(NewMemoryOCIObjectStore(), time.Hour, -time.Millisecond, time.Hour, time.Hour, []string{"repo.example"})
	metrics := &Metrics{}
	handler := MavenHandler{Store: store, Authenticator: testAuthenticator(), Client: client, Metrics: metrics, Cache: cache}
	request := func(path string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, "/maven/engineering/"+path, nil)
		r.SetBasicAuth("maven", "resolver-secret")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	for range 2 {
		if response := request("com/example/library/1.0/library-1.0.jar"); response.Code != http.StatusOK || response.Body.String() != "jar" {
			t.Fatalf("component response = %d %q", response.Code, response.Body.String())
		}
	}
	if calls := client.Calls(); calls != 1 {
		t.Fatalf("component upstream calls = %d, want 1", calls)
	}
	rangeRequest := httptest.NewRequest(http.MethodGet, "/maven/engineering/com/example/library/1.0/library-1.0.jar", nil)
	rangeRequest.Header.Set("Range", "bytes=0-1")
	rangeRequest.SetBasicAuth("maven", "resolver-secret")
	rangeResponse := httptest.NewRecorder()
	handler.ServeHTTP(rangeResponse, rangeRequest)
	if rangeResponse.Code != http.StatusPartialContent || rangeResponse.Body.String() != "ja" || client.Calls() != 1 {
		t.Fatalf("cached range response=%d body=%q calls=%d", rangeResponse.Code, rangeResponse.Body.String(), client.Calls())
	}
	conditionalRequest := httptest.NewRequest(http.MethodGet, "/maven/engineering/com/example/library/1.0/library-1.0.jar", nil)
	conditionalRequest.Header.Set("If-Modified-Since", "Wed, 01 Jan 2025 00:00:00 GMT")
	conditionalRequest.SetBasicAuth("maven", "resolver-secret")
	conditionalResponse := httptest.NewRecorder()
	handler.ServeHTTP(conditionalResponse, conditionalRequest)
	if conditionalResponse.Code != http.StatusNotModified || client.Calls() != 1 {
		t.Fatalf("cached conditional response=%d calls=%d", conditionalResponse.Code, client.Calls())
	}
	client.body = []byte("metadata-v1")
	if response := request("com/example/library/maven-metadata.xml"); response.Code != http.StatusOK || response.Body.String() != "metadata-v1" {
		t.Fatalf("metadata response = %d %q", response.Code, response.Body.String())
	}
	client.body = []byte("metadata-v2")
	if response := request("com/example/library/maven-metadata.xml"); response.Code != http.StatusOK || response.Body.String() != "metadata-v2" {
		t.Fatalf("expired metadata response = %d %q", response.Code, response.Body.String())
	}
	if calls := client.Calls(); calls != 3 {
		t.Fatalf("metadata should refresh separately; calls = %d", calls)
	}
	if metrics.mavenCacheHit.Load() != 3 || metrics.mavenCacheMiss.Load() < 3 {
		t.Fatalf("cache metrics hit=%d miss=%d", metrics.mavenCacheHit.Load(), metrics.mavenCacheMiss.Load())
	}
	metricResponse := httptest.NewRecorder()
	metrics.Handler(metricResponse, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if !strings.Contains(metricResponse.Body.String(), `artifact_gateway_maven_cache_requests_total{outcome="hit"} 3`) {
		t.Fatalf("metrics = %s", metricResponse.Body.String())
	}
}

func TestMavenProxyCacheNegativeWhitelistRetryAndCorruption(t *testing.T) {
	store := repository.NewMemoryStore()
	_, _ = store.CreateMavenGroup(context.Background(), repository.Group{Name: "engineering", Members: []repository.Member{{Name: "central", Type: repository.MemberProxy, Endpoint: "https://repo.example", Position: 0}}})
	objects := NewMemoryOCIObjectStore()
	cache := NewMavenCache(objects, time.Hour, time.Hour, time.Hour, time.Hour, []string{"repo.example"})
	client := &countingMavenClient{status: http.StatusNotFound}
	handler := MavenHandler{Store: store, Authenticator: testAuthenticator(), Client: client, Metrics: &Metrics{}, Cache: cache}
	for range 2 {
		r := httptest.NewRequest(http.MethodGet, "/maven/engineering/com/example/missing/1.0/missing-1.0.pom", nil)
		r.SetBasicAuth("maven", "resolver-secret")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != http.StatusNotFound {
			t.Fatalf("negative response = %d", w.Code)
		}
	}
	if calls := client.Calls(); calls != 1 {
		t.Fatalf("negative cache calls = %d", calls)
	}
	cache.SetAllowedProxyHosts(nil)
	r := httptest.NewRequest(http.MethodGet, "/maven/engineering/com/example/missing/1.0/missing-1.0.pom", nil)
	r.SetBasicAuth("maven", "resolver-secret")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden || client.Calls() != 1 {
		t.Fatalf("revoked negative response=%d calls=%d", w.Code, client.Calls())
	}
	cache.SetAllowedProxyHosts([]string{"repo.example"})
	_, _ = store.CreateMavenGroup(context.Background(), repository.Group{Name: "untrusted", Members: []repository.Member{{Name: "untrusted", Type: repository.MemberProxy, Endpoint: "https://untrusted.example", Position: 0}}})
	disallowedRequest := httptest.NewRequest(http.MethodGet, "/maven/untrusted/com/example/library/1.0/library-1.0.pom", nil)
	disallowedRequest.SetBasicAuth("maven", "resolver-secret")
	disallowedResponse := httptest.NewRecorder()
	handler.ServeHTTP(disallowedResponse, disallowedRequest)
	if disallowedResponse.Code != http.StatusForbidden || client.Calls() != 1 {
		t.Fatalf("disallowed proxy response=%d calls=%d", disallowedResponse.Code, client.Calls())
	}

	client.err = errors.New("temporary upstream failure")
	cache.RecordUpstreamSuccess(context.Background(), "https://repo.example")
	r = httptest.NewRequest(http.MethodGet, "/maven/engineering/com/example/live/1.0/live-1.0.pom", nil)
	r.SetBasicAuth("maven", "resolver-secret")
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusBadGateway || client.Calls() != 3 {
		t.Fatalf("retry response=%d calls=%d", w.Code, client.Calls())
	}

	key := cache.Key("engineering", "com/example/corrupt/1.0/corrupt-1.0.pom")
	if err := objects.Put(context.Background(), key, []byte("not json")); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.Load(context.Background(), key); !errors.Is(err, errMavenCacheMiss) {
		t.Fatalf("corrupt cache = %v", err)
	}
}

func TestMavenProxyRetriesHTTPFailuresAndInvalidatesUnauthorizedCachedSource(t *testing.T) {
	store := repository.NewMemoryStore()
	member := repository.Member{Name: "central", Type: repository.MemberProxy, Endpoint: "https://repo.example", Position: 0}
	_, _ = store.CreateMavenGroup(context.Background(), repository.Group{Name: "engineering", Members: []repository.Member{member}})
	cache := NewMavenCache(NewMemoryOCIObjectStore(), time.Hour, time.Hour, time.Hour, time.Hour, []string{"repo.example"})
	client := &countingMavenClient{status: http.StatusOK, statuses: []int{http.StatusServiceUnavailable, http.StatusOK}, body: []byte("recovered")}
	metrics := &Metrics{}
	handler := MavenHandler{Store: store, Authenticator: testAuthenticator(), Client: client, Metrics: metrics, Cache: cache}
	request := httptest.NewRequest(http.MethodGet, "/maven/engineering/com/example/library/1.0/library-1.0.pom", nil)
	request.SetBasicAuth("maven", "resolver-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Body.String() != "recovered" || client.Calls() != 2 {
		t.Fatalf("HTTP retry response=%d body=%q calls=%d", response.Code, response.Body.String(), client.Calls())
	}

	cache.SetAllowedProxyHosts(nil)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden || client.Calls() != 2 {
		t.Fatalf("revoked cached source response=%d calls=%d", response.Code, client.Calls())
	}
	if _, err := cache.Load(context.Background(), cache.Key("engineering", "com/example/library/1.0/library-1.0.pom")); err == nil {
		t.Fatal("revoked cache was still readable")
	}
	if metrics.mavenCacheInvalidated.Load() != 1 {
		t.Fatalf("cache invalidations = %d", metrics.mavenCacheInvalidated.Load())
	}
}

func TestMavenLegacyGroupServesArtifactsMetadataAndChecksums(t *testing.T) {
	files := map[string]string{
		"/com/example/library/1.0/library-1.0.pom":        "<project/>",
		"/com/example/library/1.0/library-1.0.jar":        "jar-content",
		"/com/example/library/1.0/library-1.0.jar.sha1":   "sha1",
		"/com/example/library/1.0/library-1.0.jar.sha256": "sha256",
		"/com/example/library/1.0/library-1.0.jar.md5":    "md5",
		"/com/example/library/maven-metadata.xml":         "<metadata/>",
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		content, exists := files[request.URL.Path]
		if !exists {
			http.NotFound(w, request)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("ETag", `"fixture"`)
		if request.Method != http.MethodHead {
			_, _ = w.Write([]byte(content))
		}
	}))
	defer upstream.Close()
	store := repository.NewMemoryStore()
	_, err := store.CreateMavenGroup(context.Background(), repository.Group{Name: "engineering", Members: []repository.Member{{Name: "legacy", Type: repository.MemberHosted, Endpoint: upstream.URL, Position: 0}}})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator(), UpstreamClient{})
	for path, want := range files {
		request := httptest.NewRequest(http.MethodGet, "/maven/engineering"+path, nil)
		request.SetBasicAuth("gradle", "resolver-secret")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK || response.Body.String() != want || response.Header().Get("ETag") != `"fixture"` {
			t.Fatalf("%s = %d headers=%v body=%q", path, response.Code, response.Header(), response.Body.String())
		}
	}
	head := httptest.NewRequest(http.MethodHead, "/maven/engineering/com/example/library/1.0/library-1.0.jar", nil)
	head.SetBasicAuth("maven", "resolver-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, head)
	if response.Code != http.StatusOK || response.Body.Len() != 0 {
		t.Fatalf("HEAD = %d %q", response.Code, response.Body.String())
	}
	if len(store.Audits) != len(files)+1 || store.Audits[0].Actor != "gradle" || store.Audits[0].Outcome != repository.AuditResolved {
		t.Fatalf("audits = %#v", store.Audits)
	}
}

func TestMavenHostedMemberWinsAndProxyIsFallback(t *testing.T) {
	hosted := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/com/example/library/1.0/library-1.0.pom" {
			_, _ = w.Write([]byte("internal"))
			return
		}
		http.NotFound(w, request)
	}))
	defer hosted.Close()
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if _, _, ok := request.BasicAuth(); ok {
			t.Fatal("proxy received Legacy credentials")
		}
		if request.URL.Path != "/com/example/library/2.0/library-2.0.pom" {
			http.NotFound(w, request)
			return
		}
		_, _ = w.Write([]byte("proxy"))
	}))
	defer proxy.Close()
	store := repository.NewMemoryStore()
	_, _ = store.CreateMavenGroup(context.Background(), repository.Group{Name: "engineering", Members: []repository.Member{
		{Name: "proxy-first", Type: repository.MemberProxy, Endpoint: proxy.URL, Position: 0},
		{Name: "hosted", Type: repository.MemberHosted, Endpoint: hosted.URL, Position: 1},
	}})
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator(), UpstreamClient{})
	request := httptest.NewRequest(http.MethodGet, "/maven/engineering/com/example/library/1.0/library-1.0.pom", nil)
	request.SetBasicAuth("maven", "resolver-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Body.String() != "internal" || len(store.Audits) != 2 || store.Audits[0].Outcome != repository.AuditNotFound || store.Audits[1].MemberName != "hosted" {
		t.Fatalf("response=%d body=%q audits=%#v", response.Code, response.Body.String(), store.Audits)
	}

	request = httptest.NewRequest(http.MethodGet, "/maven/engineering/com/example/library/2.0/library-2.0.pom", nil)
	request.SetBasicAuth("maven", "resolver-secret")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Body.String() != "proxy" || len(store.Audits) != 4 || store.Audits[2].Outcome != repository.AuditNotFound || store.Audits[3].MemberName != "proxy-first" {
		t.Fatalf("fallback response=%d body=%q audits=%#v", response.Code, response.Body.String(), store.Audits)
	}
}

func TestMavenSignalsAndAuditsAnInternalCoordinateConflict(t *testing.T) {
	hosted := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("internal"))
	}))
	defer hosted.Close()
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodHead {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		_, _ = w.Write([]byte("external"))
	}))
	defer proxy.Close()
	store := repository.NewMemoryStore()
	_, _ = store.CreateMavenGroup(context.Background(), repository.Group{Name: "engineering", Members: []repository.Member{
		{Name: "hosted", Type: repository.MemberHosted, Endpoint: hosted.URL, Position: 0},
		{Name: "proxy", Type: repository.MemberProxy, Endpoint: proxy.URL, Position: 1},
	}})
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator(), UpstreamClient{})
	request := httptest.NewRequest(http.MethodGet, "/maven/engineering/com/example/library/1.0/library-1.0.pom", nil)
	request.SetBasicAuth("maven", "resolver-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Body.String() != "internal" || response.Header().Get("X-Artifact-Gateway-Conflict") != "internal-preferred" {
		t.Fatalf("response=%d header=%q body=%q", response.Code, response.Header().Get("X-Artifact-Gateway-Conflict"), response.Body.String())
	}
	if len(store.Audits) != 2 || store.Audits[0].Outcome != repository.AuditInternalPreferred || store.Audits[0].MemberName != "proxy" || store.Audits[1].Outcome != repository.AuditResolved || store.Audits[1].MemberName != "hosted" {
		t.Fatalf("audits = %#v", store.Audits)
	}
}

func TestMavenGroupManagementAndPathValidation(t *testing.T) {
	store := repository.NewMemoryStore()
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())
	create := httptest.NewRequest(http.MethodPost, "/api/v1/maven/groups", strings.NewReader(`{"name":"engineering","members":[{"name":"hosted","type":"hosted","endpoint":"http://legacy","position":0}]}`))
	authorize(create, "admin-secret")
	created := httptest.NewRecorder()
	handler.ServeHTTP(created, create)
	if created.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", created.Code, created.Body.String())
	}
	if _, _, ok := parseMavenPath("/maven/engineering/../secret"); ok {
		t.Fatal("path traversal was accepted")
	}
}

func TestMavenFailsWhenAuditCannotBeRecorded(t *testing.T) {
	store := repository.NewMemoryStore()
	_, _ = store.CreateMavenGroup(context.Background(), repository.Group{Name: "engineering", Members: []repository.Member{{Name: "hosted", Type: repository.MemberHosted, Endpoint: "test://available", Position: 0}}})
	handler := MavenHandler{Store: failingAuditStore{store}, Authenticator: testAuthenticator(), Client: UpstreamClient{}, Metrics: &Metrics{}}
	request := httptest.NewRequest(http.MethodGet, "/maven/engineering/com/example/library/1.0/library-1.0.pom", nil)
	request.SetBasicAuth("maven", "resolver-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("response = %d %s", response.Code, response.Body.String())
	}
}

func TestMavenRepositoryPermissionRejectsAndAuditsDeniedRead(t *testing.T) {
	store := repository.NewMemoryStore()
	authenticator := testAuthenticator()
	authenticator.RepositoryReaders = map[string][]string{"maven": {"engineering/allowed/*"}}
	handler := MavenHandler{Store: store, Authenticator: authenticator, Client: UpstreamClient{}, Metrics: &Metrics{}}
	request := httptest.NewRequest(http.MethodGet, "/maven/engineering/com/example/private/1.0/private-1.0.pom", nil)
	request.SetBasicAuth("maven", "resolver-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), "repository read permission required") {
		t.Fatalf("response = %d %s", response.Code, response.Body.String())
	}
	if len(store.Audits) != 1 || store.Audits[0].Outcome != repository.AuditAccessDenied || store.Audits[0].Actor != "maven" {
		t.Fatalf("audits = %#v", store.Audits)
	}
}

func TestMavenUsesManagedGrantsForBoundMembersAndCachedSources(t *testing.T) {
	store := repository.NewMemoryStore()
	deniedRepository, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: "maven-denied", Name: "maven-denied", Format: repository.FormatMaven})
	if err != nil {
		t.Fatal(err)
	}
	allowedRepository, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: "maven-allowed", Name: "maven-allowed", Format: repository.FormatMaven})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReplaceRepositoryGrants(context.Background(), deniedRepository.ID, nil, "1"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReplaceRepositoryGrants(context.Background(), allowedRepository.ID, []repository.RepositoryGrant{{Principal: "maven", Scopes: []string{"repositories:read"}}}, "1"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateMavenGroup(context.Background(), repository.Group{Name: "engineering", Members: []repository.Member{
		{Name: "denied", Type: repository.MemberHosted, Endpoint: "https://denied.example", Position: 0, RepositoryID: deniedRepository.ID},
		{Name: "allowed", Type: repository.MemberProxy, Endpoint: "https://repo.example", Position: 1, RepositoryID: allowedRepository.ID},
	}}); err != nil {
		t.Fatal(err)
	}
	authenticator := Authenticator{ResolverToken: "resolver-secret", RepositoryReaders: map[string][]string{"maven": {"engineering"}}}
	client := &countingMavenClient{status: http.StatusOK, body: []byte("artifact")}
	metrics := &Metrics{}
	cache := NewMavenCache(NewMemoryOCIObjectStore(), time.Hour, time.Hour, time.Hour, time.Hour, []string{"repo.example"})
	handler := MavenHandler{Store: store, Repositories: store, Authorizer: RepositoryAuthorizer{Grants: store, Legacy: authenticator}, Authenticator: authenticator, Client: client, Metrics: metrics, Cache: cache}
	request := func() *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, "/maven/engineering/com/example/library/1.0/library-1.0.jar", nil)
		r.SetBasicAuth("maven", "resolver-secret")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	if response := request(); response.Code != http.StatusOK || strings.Join(client.Members(), ",") != "allowed" {
		t.Fatalf("response=%d members=%v", response.Code, client.Members())
	}
	if _, err := store.ReplaceRepositoryGrants(context.Background(), allowedRepository.ID, nil, "2"); err != nil {
		t.Fatal(err)
	}
	if response := request(); response.Code != http.StatusForbidden || client.Calls() != 1 {
		t.Fatalf("cached response=%d calls=%d", response.Code, client.Calls())
	}
	var foundDeniedAudit bool
	for _, audit := range store.Audits {
		if audit.MemberName == "denied" && audit.AuthorizationSource == "repository_grants" && audit.AuthorizationReason == "scope_not_granted" {
			foundDeniedAudit = true
		}
	}
	if !foundDeniedAudit {
		t.Fatalf("audits=%#v", store.Audits)
	}
	metricResponse := httptest.NewRecorder()
	metrics.Handler(metricResponse, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if !strings.Contains(metricResponse.Body.String(), `artifact_gateway_repository_authorization_denials_total{format="maven",authorization_source="repository_grants",authorization_reason="scope_not_granted"} 3`) {
		t.Fatalf("authorization metrics=%s", metricResponse.Body.String())
	}
}

func TestMavenGroupRepositoryKeyAuthorizesAuditsAndEnforcesQuota(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("artifact")) }))
	defer upstream.Close()
	store := repository.NewMemoryStore()
	_, _ = store.CreateMavenGroup(context.Background(), repository.Group{Name: "engineering", Members: []repository.Member{{Name: "proxy", Type: repository.MemberProxy, Endpoint: upstream.URL, Position: 0}}})
	authenticator := testAuthenticator()
	authenticator.RepositoryReaders = map[string][]string{"maven": {"engineering/*"}}
	objects := NewMemoryOCIObjectStore()
	cache := NewDefaultMavenCache(objects, []string{strings.TrimPrefix(upstream.URL, "http://")}).WithQuota(NewCacheQuota(objects, map[string]int64{"engineering": 1}))
	metrics := &Metrics{}
	handler := MavenHandler{Store: store, Authenticator: authenticator, Client: UpstreamClient{}, Metrics: metrics, Cache: cache}
	request := httptest.NewRequest(http.MethodGet, "/maven/engineering/com/example/library/1.0/library-1.0.pom", nil)
	request.SetBasicAuth("maven", "resolver-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Body.String() != "artifact" {
		t.Fatalf("response = %d %q", response.Code, response.Body.String())
	}
	if len(store.Audits) != 1 || store.Audits[0].Repository != "engineering" || store.Audits[0].Outcome != repository.AuditResolved {
		t.Fatalf("audits = %#v", store.Audits)
	}
	if status := metrics.repository("engineering"); status.Requests != 1 || status.CacheMisses != 1 {
		t.Fatalf("group metrics = %#v", status)
	}
}

func TestMavenDenylistedProxyReturnsForbiddenAndAudits(t *testing.T) {
	store := repository.NewMemoryStore()
	_, _ = store.CreateMavenGroup(context.Background(), repository.Group{Name: "engineering", Members: []repository.Member{{Name: "blocked", Type: repository.MemberProxy, Endpoint: "https://blocked.example", Position: 0}}})
	handler := MavenHandler{Store: store, Authenticator: testAuthenticator(), Client: UpstreamClient{}, Metrics: &Metrics{}, Cache: NewDefaultMavenCache(NewMemoryOCIObjectStore(), []string{"allowed.example"})}
	request := httptest.NewRequest(http.MethodGet, "/maven/engineering/com/example/library/1.0/library-1.0.pom", nil)
	request.SetBasicAuth("maven", "resolver-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("response = %d %s", response.Code, response.Body.String())
	}
	if len(store.Audits) != 1 || store.Audits[0].Outcome != repository.AuditProxyDenied || store.Audits[0].Repository != "engineering" {
		t.Fatalf("audits = %#v", store.Audits)
	}
}

func TestMavenForwardsConditionalRequestsAndDisabledGroupsAreAudited(t *testing.T) {
	hosted := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Header.Get("If-None-Match") != `"cached"` {
			t.Fatalf("If-None-Match = %q", request.Header.Get("If-None-Match"))
		}
		w.Header().Set("ETag", `"cached"`)
		w.WriteHeader(http.StatusNotModified)
	}))
	defer hosted.Close()
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodHead {
			t.Fatal("conflict probe did not use HEAD")
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer proxy.Close()
	store := repository.NewMemoryStore()
	_, _ = store.CreateMavenGroup(context.Background(), repository.Group{Name: "engineering", Members: []repository.Member{{Name: "hosted", Type: repository.MemberHosted, Endpoint: hosted.URL, Position: 0}, {Name: "proxy", Type: repository.MemberProxy, Endpoint: proxy.URL, Position: 1}}})
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator(), UpstreamClient{})
	request := httptest.NewRequest(http.MethodGet, "/maven/engineering/com/example/library/maven-metadata.xml", nil)
	request.Header.Set("If-None-Match", `"cached"`)
	request.SetBasicAuth("maven", "resolver-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNotModified || response.Header().Get("ETag") != `"cached"` || response.Header().Get("X-Artifact-Gateway-Conflict") != "internal-preferred" || len(store.Audits) != 2 || store.Audits[0].Outcome != repository.AuditInternalPreferred || store.Audits[1].Outcome != repository.AuditResolved {
		t.Fatalf("response=%d headers=%v audits=%#v", response.Code, response.Header(), store.Audits)
	}
	if err := store.DisableMavenGroup(context.Background(), "engineering"); err != nil {
		t.Fatal(err)
	}
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), "disabled") || len(store.Audits) != 3 || store.Audits[2].Outcome != repository.AuditGroupDisabled {
		t.Fatalf("disabled response=%d body=%q audits=%#v", response.Code, response.Body.String(), store.Audits)
	}
}
