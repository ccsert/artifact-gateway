package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"go.opentelemetry.io/otel/trace"
)

type rawFixtureClient struct {
	mu        sync.Mutex
	calls     []string
	responses map[string]int
	body      []byte
}

func (c *rawFixtureClient) Fetch(_ context.Context, _ string, _ repository.Member, _, _, _ string, _ http.Header) (*http.Response, error) {
	return nil, errors.New("OCI fetch is not used by Raw tests")
}

func (c *rawFixtureClient) FetchRaw(_ context.Context, _ string, member repository.Member, _ string, _ http.Header) (*http.Response, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = append(c.calls, member.Name)
	status := c.responses[member.Name]
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"text/plain"}}, Body: io.NopCloser(bytes.NewReader(c.body))}, nil
}

func (c *rawFixtureClient) Calls() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.calls...)
}

func rawRequest(method, path string) *http.Request {
	r := httptest.NewRequest(method, path, nil)
	r.Header.Set("Authorization", "Bearer resolver-secret")
	return r
}

func TestRawHostedFirstCacheAndRange(t *testing.T) {
	store := repository.NewMemoryStore()
	_, err := store.CreateRawGroup(context.Background(), repository.Group{Name: "downloads", Members: []repository.Member{
		{Name: "hosted", Type: repository.MemberHosted, Endpoint: "http://gitea.local", Position: 0},
		{Name: "proxy", Type: repository.MemberProxy, Endpoint: "https://proxy.example", Position: 1, AllowedHosts: []string{"proxy.example"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	client := &rawFixtureClient{responses: map[string]int{"hosted": http.StatusNotFound, "proxy": http.StatusOK}, body: []byte("artifact")}
	h := RawHandler{Store: store, Authenticator: testAuthenticator(), Client: client, Metrics: &Metrics{}, Cache: NewRawCache(NewMemoryOCIObjectStore(), time.Hour, time.Hour, []string{"proxy.example"})}
	for range 2 {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, rawRequest(http.MethodGet, "/raw/downloads/release/app.txt"))
		if w.Code != http.StatusOK || w.Body.String() != "artifact" || w.Header().Get("ETag") == "" || w.Header().Get("Digest") == "" {
			t.Fatalf("response=%d headers=%v body=%q", w.Code, w.Header(), w.Body.String())
		}
	}
	if got := client.Calls(); len(got) != 2 || got[0] != "hosted" || got[1] != "proxy" {
		t.Fatalf("calls=%v", got)
	}
	r := rawRequest(http.MethodGet, "/raw/downloads/release/app.txt")
	r.Header.Set("Range", "bytes=1-3")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusPartialContent || w.Body.String() != "rti" {
		t.Fatalf("range=%d %q", w.Code, w.Body.String())
	}
}

func TestRawRejectsMultipartRangeOverHTTP(t *testing.T) {
	store := repository.NewMemoryStore()
	_, err := store.CreateRawGroup(context.Background(), repository.Group{Name: "downloads", Members: []repository.Member{{Name: "hosted", Type: repository.MemberHosted, Endpoint: "http://gitea.local"}}})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(RawHandler{Store: store, Authenticator: testAuthenticator(), Client: &rawFixtureClient{responses: map[string]int{"hosted": http.StatusOK}, body: []byte("artifact")}, Metrics: &Metrics{}, Cache: NewRawCache(NewMemoryOCIObjectStore(), time.Hour, time.Hour, nil)})
	defer server.Close()
	for _, values := range [][]string{{"bytes=0-1,3-4"}, {"bytes=0-1", "bytes=3-4"}} {
		request, err := http.NewRequest(http.MethodGet, server.URL+"/raw/downloads/release/app.txt", nil)
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Authorization", "Bearer resolver-secret")
		for _, value := range values {
			request.Header.Add("Range", value)
		}
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
		if response.StatusCode != http.StatusRequestedRangeNotSatisfiable {
			t.Fatalf("multipart ranges %v status = %d", values, response.StatusCode)
		}
	}
}

func TestRawCacheUsesPersistedGroupQuota(t *testing.T) {
	store := NewMemoryOCIObjectStore()
	cache := NewRawCache(store, time.Hour, time.Hour, nil).WithQuota(NewCacheQuota(store, nil))
	first := cache.key("downloads", "one", "hosted", "http://gitea.local")
	if err := cache.Store(context.Background(), first, RawContent{Body: []byte("1234"), Repository: "downloads", CacheQuotaBytes: 5}); err != nil {
		t.Fatal(err)
	}
	second := cache.key("downloads", "two", "hosted", "http://gitea.local")
	if err := cache.Store(context.Background(), second, RawContent{Body: []byte("23"), Repository: "downloads", CacheQuotaBytes: 5}); !errors.Is(err, ErrCacheQuotaExceeded) {
		t.Fatalf("second cache store = %v, want quota rejection", err)
	}
}

func TestRawCacheLoadsLegacyIndex(t *testing.T) {
	store := NewMemoryOCIObjectStore()
	cache := NewRawCache(store, time.Hour, time.Hour, nil)
	key := cache.key("downloads", "artifact", "hosted", "http://gitea.local")
	body := []byte("artifact")
	sum := sha256.Sum256(body)
	digest := hex.EncodeToString(sum[:])
	object := "raw/objects/" + digest
	if err := store.Put(context.Background(), object, body); err != nil {
		t.Fatal(err)
	}
	legacy, err := json.Marshal(struct {
		Object, Digest, ContentType, Member, Endpoint, Repository string
		Size                                                      int64
		ExpiresAt                                                 time.Time
	}{Object: object, Digest: digest, ContentType: "text/plain", Member: "hosted", Endpoint: "http://gitea.local", Repository: "downloads", Size: int64(len(body)), ExpiresAt: time.Now().UTC().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Put(context.Background(), key, legacy); err != nil {
		t.Fatal(err)
	}
	content, err := cache.Load(context.Background(), key)
	if err != nil || string(content.Body) != "artifact" {
		t.Fatalf("legacy cache load content=%q err=%v", content.Body, err)
	}
}

func TestRawCacheQuotaCountsLegacyIndex(t *testing.T) {
	store := NewMemoryOCIObjectStore()
	cache := NewRawCache(store, time.Hour, time.Hour, nil).WithQuota(NewCacheQuota(store, nil))
	key := cache.key("downloads", "existing", "hosted", "http://gitea.local")
	legacy, err := json.Marshal(struct {
		Repository string
		Size       int64
		ExpiresAt  time.Time
	}{Repository: "downloads", Size: 5, ExpiresAt: time.Now().UTC().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Put(context.Background(), key, legacy); err != nil {
		t.Fatal(err)
	}
	newKey := cache.key("downloads", "new", "hosted", "http://gitea.local")
	if err := cache.Store(context.Background(), newKey, RawContent{Body: []byte("x"), Repository: "downloads", CacheQuotaBytes: 5}); !errors.Is(err, ErrCacheQuotaExceeded) {
		t.Fatalf("legacy quota admission = %v, want quota rejection", err)
	}
}

func TestRawQuotaExcludesSameNamedOCICacheAndRecordsRejection(t *testing.T) {
	store := NewMemoryOCIObjectStore()
	quota := NewCacheQuota(store, nil)
	ociCache := NewDefaultOCICache(store, nil)
	ociKey := ociCache.key("downloads", "downloads", ociManifest, "latest")
	if err := ociCache.Store(context.Background(), ociKey, CachedOCIContent{Body: []byte("12345"), Digest: digestOf([]byte("12345")), Repository: "downloads"}); err != nil {
		t.Fatal(err)
	}
	rawCache := NewRawCache(store, time.Hour, time.Hour, nil).WithQuota(quota)
	key := rawCache.key("downloads", "artifact", "hosted", "http://gitea.local")
	if err := rawCache.Store(context.Background(), key, RawContent{Body: []byte("12345"), Repository: "downloads", CacheQuotaBytes: 5}); err != nil {
		t.Fatalf("same-named OCI cache consumed Raw quota: %v", err)
	}

	groups := repository.NewMemoryStore()
	_, err := groups.CreateRawGroup(context.Background(), repository.Group{Name: "limited", CacheQuotaBytes: 1, Members: []repository.Member{{Name: "hosted", Type: repository.MemberHosted, Endpoint: "http://gitea.local"}}})
	if err != nil {
		t.Fatal(err)
	}
	metrics := &Metrics{}
	handler := RawHandler{Store: groups, Authenticator: testAuthenticator(), Client: &rawFixtureClient{responses: map[string]int{"hosted": http.StatusOK}, body: []byte("two")}, Metrics: metrics, Cache: NewRawCache(NewMemoryOCIObjectStore(), time.Hour, time.Hour, nil).WithQuota(NewCacheQuota(NewMemoryOCIObjectStore(), nil))}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, rawRequest(http.MethodGet, "/raw/limited/artifact"))
	if response.Code != http.StatusOK || metrics.cacheQuotaDenied.Load() != 1 {
		t.Fatalf("quota rejection response=%d metric=%d", response.Code, metrics.cacheQuotaDenied.Load())
	}
}

func TestRawRejectsUnsafePathsAndUnauthorizedRequests(t *testing.T) {
	store := repository.NewMemoryStore()
	_, _ = store.CreateRawGroup(context.Background(), repository.Group{Name: "downloads", Members: []repository.Member{{Name: "hosted", Type: repository.MemberHosted, Endpoint: "http://gitea.local"}}})
	h := RawHandler{Store: store, Authenticator: testAuthenticator(), Client: &rawFixtureClient{responses: map[string]int{"hosted": http.StatusOK}}, Metrics: &Metrics{}}
	for _, path := range []string{"/raw/downloads/../secret", "/raw/downloads/%2e%2e/secret"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, rawRequest(http.MethodGet, path))
		if w.Code != http.StatusBadRequest {
			t.Fatalf("%s status=%d", path, w.Code)
		}
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, rawRequest(http.MethodGet, "/raw/downloads/release/"))
	if w.Code != http.StatusNotFound {
		t.Fatalf("directory status=%d", w.Code)
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/raw/downloads/a", nil))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated=%d", w.Code)
	}
}

func TestRawAnonymousPolicyRequiresPublicGroupAndMember(t *testing.T) {
	store := repository.NewMemoryStore()
	for _, group := range []repository.Group{
		{Name: "private", Members: []repository.Member{{Name: "hosted", Type: repository.MemberHosted, Anonymous: true}}},
		{Name: "member-private", Anonymous: true, Members: []repository.Member{{Name: "hosted", Type: repository.MemberHosted}}},
		{Name: "public", Anonymous: true, Members: []repository.Member{{Name: "hosted", Type: repository.MemberHosted, Anonymous: true}}},
	} {
		if _, err := store.CreateRawGroup(context.Background(), group); err != nil {
			t.Fatal(err)
		}
	}
	client := &rawFixtureClient{responses: map[string]int{"hosted": http.StatusOK}, body: []byte("artifact")}
	h := RawHandler{Store: store, Authenticator: testAuthenticator(), Client: client, Metrics: &Metrics{}, Cache: NewRawCache(NewMemoryOCIObjectStore(), time.Hour, time.Hour, nil)}
	for group, want := range map[string]int{"private": http.StatusUnauthorized, "member-private": http.StatusUnauthorized, "public": http.StatusOK} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/raw/"+group+"/artifact", nil))
		if w.Code != want {
			t.Errorf("%s: got %d want %d", group, w.Code, want)
		}
	}
	var anonymousResolved bool
	for _, audit := range store.Audits {
		anonymousResolved = anonymousResolved || audit.GroupName == "public" && audit.Actor == "anonymous" && audit.Outcome == repository.AuditResolved
	}
	if !anonymousResolved {
		t.Fatalf("anonymous audit=%#v", store.Audits)
	}
}

func TestRawProxyDenialAndNegativeCache(t *testing.T) {
	store := repository.NewMemoryStore()
	_, _ = store.CreateRawGroup(context.Background(), repository.Group{Name: "downloads", Members: []repository.Member{{Name: "blocked", Type: repository.MemberProxy, Endpoint: "http://blocked.example"}}})
	client := &rawFixtureClient{responses: map[string]int{"blocked": http.StatusNotFound}}
	h := RawHandler{Store: store, Authenticator: testAuthenticator(), Client: client, Metrics: &Metrics{}, Cache: NewRawCache(NewMemoryOCIObjectStore(), time.Hour, time.Hour, []string{"blocked.example"})}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, rawRequest(http.MethodGet, "/raw/downloads/a"))
	if w.Code != http.StatusForbidden || len(client.Calls()) != 0 {
		t.Fatalf("denial=%d calls=%v", w.Code, client.Calls())
	}
	_, _ = store.CreateRawGroup(context.Background(), repository.Group{Name: "cached", Members: []repository.Member{{Name: "proxy", Type: repository.MemberProxy, Endpoint: "https://proxy.example", AllowedHosts: []string{"proxy.example"}}}})
	client.responses["proxy"] = http.StatusNotFound
	for range 2 {
		w = httptest.NewRecorder()
		h.ServeHTTP(w, rawRequest(http.MethodGet, "/raw/cached/missing"))
		if w.Code != http.StatusNotFound {
			t.Fatalf("negative=%d", w.Code)
		}
	}
	if got := client.Calls(); len(got) != 1 {
		t.Fatalf("negative calls=%v", got)
	}
}

func TestRawProxyPolicyRejectsPrivateAddresses(t *testing.T) {
	cache := NewRawCache(NewMemoryOCIObjectStore(), time.Hour, time.Hour, []string{"127.0.0.1", "::1"})
	for _, endpoint := range []string{"http://proxy.example", "https://user:secret@proxy.example", "https://127.0.0.1", "https://[::1]"} {
		if cache.ProxyAllowed(endpoint) {
			t.Fatalf("proxy endpoint %q was allowed", endpoint)
		}
	}
}

func TestRawProxyRejectsTLSOverrideWithoutDialing(t *testing.T) {
	withRawProxyNetwork(t, func(_ context.Context, _ string, name string) ([]net.IP, error) {
		if name != "example.com" {
			t.Fatalf("lookup = %q", name)
		}
		return []net.IP{net.ParseIP("203.0.113.9")}, nil
	}, func(context.Context, string, string) (net.Conn, error) {
		t.Fatal("pinned DialContext must not run after TLS override rejection")
		return nil, nil
	})
	for _, test := range []struct {
		name      string
		transport func(*bool) *http.Transport
	}{
		{"DialTLSContext", func(called *bool) *http.Transport {
			return &http.Transport{DialTLSContext: func(context.Context, string, string) (net.Conn, error) {
				*called = true
				return nil, errors.New("unexpected TLS dial")
			}}
		}},
		{"DialTLS", func(called *bool) *http.Transport {
			return &http.Transport{DialTLS: func(string, string) (net.Conn, error) {
				*called = true
				return nil, errors.New("unexpected TLS dial")
			}}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			called := false
			client := &http.Client{Transport: test.transport(&called)}
			_, err := (GiteaClient{HTTPClient: client}).FetchRaw(context.Background(), http.MethodGet, repository.Member{Type: repository.MemberProxy, Endpoint: "https://example.com", AllowedHosts: []string{"example.com"}}, "artifact", nil)
			if err == nil || !strings.Contains(err.Error(), "must not override TLS dialing") || called {
				t.Fatalf("err = %v, TLS dial called = %t", err, called)
			}
		})
	}
}

func TestRawProxyTLSE2ESafelyPinsDialedAddressAndCaches(t *testing.T) {
	var upstreamCalls int
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls++
		if r.URL.Path != "/release/app.txt" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("proxy-artifact"))
	}))
	defer upstream.Close()

	host, port := rawTLSServerAddress(t, upstream.URL)
	withRawProxyNetwork(t, func(_ context.Context, network, name string) ([]net.IP, error) {
		if network != "ip" || name != "example.com" {
			t.Fatalf("lookup = %s %s", network, name)
		}
		return []net.IP{net.ParseIP("203.0.113.7")}, nil
	}, func(ctx context.Context, network, address string) (net.Conn, error) {
		if address != net.JoinHostPort("203.0.113.7", port) {
			t.Fatalf("dial was not pinned to validated address: %q", address)
		}
		return (&net.Dialer{}).DialContext(ctx, network, net.JoinHostPort(host, port))
	})

	endpoint := "https://example.com:" + port
	store := repository.NewMemoryStore()
	_, _ = store.CreateRawGroup(context.Background(), repository.Group{Name: "downloads", Members: []repository.Member{{Name: "proxy", Type: repository.MemberProxy, Endpoint: endpoint, AllowedHosts: []string{"example.com"}}}})
	_, _ = store.CreateRawGroup(context.Background(), repository.Group{Name: "public", Anonymous: true, Members: []repository.Member{{Name: "proxy", Type: repository.MemberProxy, Endpoint: endpoint, AllowedHosts: []string{"example.com"}, Anonymous: true}}})
	cache := NewRawCache(NewMemoryOCIObjectStore(), time.Hour, time.Hour, []string{"example.com"})
	gateway := httptest.NewServer(RawHandler{Store: store, Authenticator: testAuthenticator(), Client: GiteaClient{HTTPClient: upstream.Client()}, Metrics: &Metrics{}, Cache: cache})
	defer gateway.Close()

	request := func(group, authorization string) *http.Response {
		t.Helper()
		r, err := http.NewRequest(http.MethodGet, gateway.URL+"/raw/"+group+"/release/app.txt", nil)
		if err != nil {
			t.Fatal(err)
		}
		if authorization != "" {
			r.Header.Set("Authorization", authorization)
		}
		response, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		return response
	}

	for range 2 {
		response := request("downloads", "Bearer resolver-secret")
		body, _ := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if response.StatusCode != http.StatusOK || string(body) != "proxy-artifact" {
			t.Fatalf("response = %d %q", response.StatusCode, body)
		}
	}
	if upstreamCalls != 1 {
		t.Fatalf("upstream calls = %d, want cache hit", upstreamCalls)
	}
	response := request("downloads", "")
	_ = response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized || upstreamCalls != 1 {
		t.Fatalf("anonymous response = %d, upstream calls = %d", response.StatusCode, upstreamCalls)
	}
	response = request("public", "")
	body, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK || string(body) != "proxy-artifact" || upstreamCalls != 2 {
		t.Fatalf("public anonymous response = %d %q, upstream calls = %d", response.StatusCode, body, upstreamCalls)
	}
}

func TestRawProxyTLSRejectsPrivateResolutionAndUnsafeRedirectsWithoutCaching(t *testing.T) {
	var redirectedTo int
	redirectTarget := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { redirectedTo++ }))
	defer redirectTarget.Close()
	var upstreamCalls int
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls++
		switch r.URL.Path {
		case "/cross":
			http.Redirect(w, r, redirectTarget.URL, http.StatusFound)
		case "/downgrade":
			http.Redirect(w, r, "http://example.com/insecure", http.StatusFound)
		case "/private":
			http.Redirect(w, r, "https://127.0.0.1/private", http.StatusFound)
		case "/failure":
			http.Error(w, "upstream failure", http.StatusInternalServerError)
		default:
			http.Error(w, "unexpected path", http.StatusInternalServerError)
		}
	}))
	defer upstream.Close()

	host, port := rawTLSServerAddress(t, upstream.URL)
	var dialAttempts int
	withRawProxyNetwork(t, func(_ context.Context, _ string, name string) ([]net.IP, error) {
		if name == "private.example" {
			return []net.IP{net.ParseIP("127.0.0.1")}, nil
		}
		return []net.IP{net.ParseIP("203.0.113.8")}, nil
	}, func(ctx context.Context, network, address string) (net.Conn, error) {
		dialAttempts++
		if address != net.JoinHostPort("203.0.113.8", port) {
			t.Fatalf("unexpected dial %q", address)
		}
		return (&net.Dialer{}).DialContext(ctx, network, net.JoinHostPort(host, port))
	})

	if _, err := (GiteaClient{}).FetchRaw(context.Background(), http.MethodGet, repository.Member{Type: repository.MemberProxy, Endpoint: "https://private.example", AllowedHosts: []string{"private.example"}}, "artifact", nil); err == nil {
		t.Fatal("private DNS answer was accepted")
	}
	if dialAttempts != 0 {
		t.Fatalf("private DNS answer reached dialer %d times", dialAttempts)
	}
	endpoint := "https://example.com:" + port
	store := repository.NewMemoryStore()
	_, _ = store.CreateRawGroup(context.Background(), repository.Group{Name: "redirects", Members: []repository.Member{{Name: "proxy", Type: repository.MemberProxy, Endpoint: endpoint, AllowedHosts: []string{"example.com"}}}})
	_, _ = store.CreateRawGroup(context.Background(), repository.Group{Name: "blocked", Members: []repository.Member{{Name: "proxy", Type: repository.MemberProxy, Endpoint: endpoint, AllowedHosts: []string{"other.example"}}}})
	cache := NewRawCache(NewMemoryOCIObjectStore(), time.Hour, time.Hour, []string{"example.com"})
	gateway := httptest.NewServer(RawHandler{Store: store, Authenticator: testAuthenticator(), Client: GiteaClient{HTTPClient: upstream.Client()}, Metrics: &Metrics{}, Cache: cache})
	defer gateway.Close()

	for _, path := range []string{"cross", "downgrade", "private", "failure"} {
		for range 2 {
			r, err := http.NewRequest(http.MethodGet, gateway.URL+"/raw/redirects/"+path, nil)
			if err != nil {
				t.Fatal(err)
			}
			r.Header.Set("Authorization", "Bearer resolver-secret")
			response, err := http.DefaultClient.Do(r)
			if err != nil {
				t.Fatal(err)
			}
			_ = response.Body.Close()
			if response.StatusCode != http.StatusBadGateway {
				t.Fatalf("%s redirect response = %d", path, response.StatusCode)
			}
		}
	}
	if upstreamCalls != 8 || redirectedTo != 0 {
		t.Fatalf("upstream calls = %d, redirected target calls = %d", upstreamCalls, redirectedTo)
	}
	blocked, err := http.NewRequest(http.MethodGet, gateway.URL+"/raw/blocked/artifact", nil)
	if err != nil {
		t.Fatal(err)
	}
	blocked.Header.Set("Authorization", "Bearer resolver-secret")
	response, err := http.DefaultClient.Do(blocked)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusForbidden || upstreamCalls != 8 {
		t.Fatalf("blocked response = %d, upstream calls = %d", response.StatusCode, upstreamCalls)
	}
}

func rawTLSServerAddress(t *testing.T, rawURL string) (string, string) {
	t.Helper()
	host, port, err := net.SplitHostPort(strings.TrimPrefix(rawURL, "https://"))
	if err != nil {
		t.Fatal(err)
	}
	return host, port
}

func withRawProxyNetwork(t *testing.T, lookup func(context.Context, string, string) ([]net.IP, error), dial func(context.Context, string, string) (net.Conn, error)) {
	t.Helper()
	previousLookup, previousDial := rawProxyLookupIP, rawProxyDialContext
	rawProxyLookupIP, rawProxyDialContext = lookup, dial
	t.Cleanup(func() { rawProxyLookupIP, rawProxyDialContext = previousLookup, previousDial })
}

func TestRawHeadConditionalAndChecksumSidecars(t *testing.T) {
	store := repository.NewMemoryStore()
	_, _ = store.CreateRawGroup(context.Background(), repository.Group{Name: "downloads", Members: []repository.Member{{Name: "hosted", Type: repository.MemberHosted, Endpoint: "http://gitea.local"}}})
	client := &rawFixtureClient{responses: map[string]int{"hosted": http.StatusOK}, body: []byte("artifact")}
	h := RawHandler{Store: store, Authenticator: testAuthenticator(), Client: client, Metrics: &Metrics{}, Cache: NewRawCache(NewMemoryOCIObjectStore(), time.Hour, time.Hour, nil)}

	head := rawRequest(http.MethodHead, "/raw/downloads/release/app.txt")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, head)
	if w.Code != http.StatusOK || w.Body.Len() != 0 || w.Header().Get("Content-Length") != "8" || w.Header().Get("ETag") == "" {
		t.Fatalf("HEAD = %d headers=%v body=%q", w.Code, w.Header(), w.Body.String())
	}

	conditional := rawRequest(http.MethodGet, "/raw/downloads/release/app.txt")
	conditional.Header.Set("If-None-Match", w.Header().Get("ETag"))
	w = httptest.NewRecorder()
	h.ServeHTTP(w, conditional)
	if w.Code != http.StatusNotModified || w.Body.Len() != 0 {
		t.Fatalf("conditional = %d body=%q", w.Code, w.Body.String())
	}

	client.body = []byte("not-a-checksum")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, rawRequest(http.MethodGet, "/raw/downloads/release/app.sha256"))
	if w.Code != http.StatusBadGateway {
		t.Fatalf("invalid sidecar = %d", w.Code)
	}

	sum := sha256.Sum256([]byte("artifact"))
	client.body = []byte(hex.EncodeToString(sum[:]) + "\n")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, rawRequest(http.MethodGet, "/raw/downloads/release/app.sha256"))
	if w.Code != http.StatusOK {
		t.Fatalf("valid sidecar = %d", w.Code)
	}
}

func TestRawWritesV2AuditFieldsForRequestOutcomes(t *testing.T) {
	store := repository.NewMemoryStore()
	for _, group := range []repository.Group{
		{Name: "hosted", Members: []repository.Member{{Name: "gitea", Type: repository.MemberHosted, Endpoint: "https://gitea.example:8443"}}},
		{Name: "negative", Members: []repository.Member{{Name: "proxy", Type: repository.MemberProxy, Endpoint: "https://proxy.example", AllowedHosts: []string{"proxy.example"}}}},
		{Name: "fallback", Members: []repository.Member{{Name: "hosted-miss", Type: repository.MemberHosted, Endpoint: "https://hosted.example", Position: 0}, {Name: "proxy-ok", Type: repository.MemberProxy, Endpoint: "https://proxy-ok.example", Position: 1, AllowedHosts: []string{"proxy-ok.example"}}}},
		{Name: "blocked", Members: []repository.Member{{Name: "blocked-proxy", Type: repository.MemberProxy, Endpoint: "https://user:secret@blocked.example"}}},
		{Name: "outage", Members: []repository.Member{{Name: "offline", Type: repository.MemberHosted, Endpoint: "https://offline.example"}}},
	} {
		if _, err := store.CreateRawGroup(context.Background(), group); err != nil {
			t.Fatal(err)
		}
	}
	client := &rawFixtureClient{responses: map[string]int{"gitea": http.StatusOK, "proxy": http.StatusNotFound, "hosted-miss": http.StatusNotFound, "proxy-ok": http.StatusOK, "offline": http.StatusServiceUnavailable}, body: []byte("artifact")}
	h := RawHandler{Store: store, Authenticator: testAuthenticator(), Client: client, Cache: NewRawCache(NewMemoryOCIObjectStore(), time.Hour, time.Hour, nil)}
	request := func(method, path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, rawRequest(method, path))
		return w
	}
	last := func() repository.AuditRecord { return store.Audits[len(store.Audits)-1] }

	resolvedResponse := request(http.MethodGet, "/raw/hosted/release/app.txt")
	resolved := last()
	if resolved.Format != "raw" || resolved.Resource != "release/app.txt" || resolved.Representation != "body" || resolved.MemberName != "gitea" || resolved.MemberType != "hosted" || resolved.UpstreamHost != "gitea.example" || resolved.Operation != "get" || resolved.Status != http.StatusOK || resolved.CacheDisposition != "miss" || resolved.Bytes != 8 {
		t.Fatalf("resolved audit=%#v", resolved)
	}

	request(http.MethodHead, "/raw/hosted/release/app.txt")
	head := last()
	if head.Operation != "head" || head.Status != http.StatusOK || head.CacheDisposition != "hit" || head.Bytes != 0 {
		t.Fatalf("HEAD audit=%#v", head)
	}
	rangeRequest := rawRequest(http.MethodGet, "/raw/hosted/release/app.txt")
	rangeRequest.Header.Set("Range", "bytes=0-1")
	h.ServeHTTP(httptest.NewRecorder(), rangeRequest)
	rangeAudit := last()
	if rangeAudit.Status != http.StatusPartialContent || rangeAudit.CacheDisposition != "hit" || rangeAudit.Bytes != 2 {
		t.Fatalf("range audit=%#v", rangeAudit)
	}
	conditional := rawRequest(http.MethodGet, "/raw/hosted/release/app.txt")
	conditional.Header.Set("If-None-Match", resolvedResponse.Header().Get("ETag"))
	h.ServeHTTP(httptest.NewRecorder(), conditional)
	if conditionalAudit := last(); conditionalAudit.Status != http.StatusNotModified || conditionalAudit.Bytes != 0 || conditionalAudit.CacheDisposition != "hit" {
		t.Fatalf("conditional audit=%#v", conditionalAudit)
	}

	request(http.MethodGet, "/raw/negative/missing")
	request(http.MethodGet, "/raw/negative/missing")
	negative := last()
	if negative.Outcome != repository.AuditNotFound || negative.MemberName != "proxy" || negative.MemberType != "proxy" || negative.UpstreamHost != "proxy.example" || negative.Status != http.StatusNotFound || negative.CacheDisposition != "hit" || negative.Bytes != 0 {
		t.Fatalf("negative-cache audit=%#v", negative)
	}

	request(http.MethodGet, "/raw/fallback/artifact")
	fallback := last()
	if fallback.Outcome != repository.AuditResolved || fallback.MemberName != "proxy-ok" || fallback.MemberType != "proxy" || fallback.UpstreamHost != "proxy-ok.example" || fallback.Status != http.StatusOK || fallback.CacheDisposition != "miss" || fallback.Bytes != 8 {
		t.Fatalf("group fallback audit=%#v", fallback)
	}

	request(http.MethodGet, "/raw/blocked/artifact")
	blocked := last()
	if blocked.Outcome != repository.AuditProxyDenied || blocked.MemberName != "blocked-proxy" || blocked.UpstreamHost != "blocked.example" || blocked.Status != http.StatusForbidden || blocked.CacheDisposition != "bypass" {
		t.Fatalf("proxy-denied audit=%#v", blocked)
	}

	request(http.MethodGet, "/raw/outage/artifact")
	outage := last()
	if outage.Outcome != repository.AuditUpstreamError || outage.MemberName != "offline" || outage.Status != http.StatusBadGateway || outage.CacheDisposition != "bypass" {
		t.Fatalf("upstream-error audit=%#v", outage)
	}

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/raw/hosted/release/app.txt", nil))
	denied := last()
	if denied.Actor != "anonymous" || denied.Outcome != repository.AuditAccessDenied || denied.Status != http.StatusUnauthorized || denied.Format != "raw" || denied.Resource != "release/app.txt" || denied.Operation != "get" || denied.CacheDisposition != "bypass" {
		t.Fatalf("authentication-denied audit=%#v", denied)
	}

	methodDenied := httptest.NewRecorder()
	h.ServeHTTP(methodDenied, rawRequest(http.MethodPost, "/raw/hosted/release/app.txt"))
	if methodDenied.Code != http.StatusMethodNotAllowed || last().Actor != "build-agent" || last().Status != http.StatusMethodNotAllowed || last().Operation != "post" {
		t.Fatalf("method-denied response=%d audit=%#v", methodDenied.Code, last())
	}

	invalidRange := rawRequest(http.MethodGet, "/raw/hosted/release/app.txt")
	invalidRange.Header.Set("Range", "bytes=99-100")
	invalidRangeResponse := httptest.NewRecorder()
	h.ServeHTTP(invalidRangeResponse, invalidRange)
	if invalidRangeResponse.Code != http.StatusRequestedRangeNotSatisfiable || last().Status != http.StatusRequestedRangeNotSatisfiable || last().CacheDisposition != "hit" || last().Bytes != 0 {
		t.Fatalf("invalid-range response=%d audit=%#v", invalidRangeResponse.Code, last())
	}
}

func TestRawAuditsCorrelationAndExportsMetrics(t *testing.T) {
	store := repository.NewMemoryStore()
	for _, group := range []repository.Group{
		{Name: "hosted", Members: []repository.Member{{Name: "hosted", Type: repository.MemberHosted, Endpoint: "https://hosted.example"}}},
		{Name: "negative", Members: []repository.Member{{Name: "negative", Type: repository.MemberHosted, Endpoint: "https://negative.example"}}},
		{Name: "blocked", Members: []repository.Member{{Name: "blocked", Type: repository.MemberProxy, Endpoint: "https://blocked.example"}}},
		{Name: "outage", Members: []repository.Member{{Name: "outage", Type: repository.MemberHosted, Endpoint: "https://outage.example"}}},
	} {
		if _, err := store.CreateRawGroup(context.Background(), group); err != nil {
			t.Fatal(err)
		}
	}
	client := &rawFixtureClient{responses: map[string]int{
		"hosted": http.StatusOK, "negative": http.StatusNotFound, "outage": http.StatusBadGateway,
	}, body: []byte("artifact")}
	metrics := &Metrics{}
	handler := RawHandler{Store: store, Authenticator: testAuthenticator(), Client: client, Metrics: metrics, Cache: NewRawCache(NewMemoryOCIObjectStore(), time.Hour, time.Hour, nil)}

	traceID := trace.TraceID{1}
	spanID := trace.SpanID{1}
	request := rawRequest(http.MethodGet, "/raw/hosted/release/app.txt?access_token=do-not-record")
	request.Header.Set("X-Request-ID", "request-42")
	request = request.WithContext(trace.ContextWithSpanContext(request.Context(), trace.NewSpanContext(trace.SpanContextConfig{TraceID: traceID, SpanID: spanID, TraceFlags: trace.FlagsSampled})))
	handler.ServeHTTP(httptest.NewRecorder(), request)
	handler.ServeHTTP(httptest.NewRecorder(), rawRequest(http.MethodGet, "/raw/hosted/release/app.txt"))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/raw/hosted/release/app.txt", nil))
	handler.ServeHTTP(httptest.NewRecorder(), rawRequest(http.MethodPost, "/raw/hosted/release/app.txt"))
	handler.ServeHTTP(httptest.NewRecorder(), rawRequest(http.MethodGet, "/raw/negative/missing"))
	handler.ServeHTTP(httptest.NewRecorder(), rawRequest(http.MethodGet, "/raw/negative/missing"))
	handler.ServeHTTP(httptest.NewRecorder(), rawRequest(http.MethodGet, "/raw/blocked/artifact"))
	handler.ServeHTTP(httptest.NewRecorder(), rawRequest(http.MethodGet, "/raw/outage/artifact"))
	handler.ServeHTTP(httptest.NewRecorder(), rawRequest(http.MethodGet, "/raw/hosted/release/app.sha256"))
	handler.ServeHTTP(httptest.NewRecorder(), rawRequest(http.MethodGet, "/raw/hosted/%2e%2e/secret?token=do-not-record"))

	if len(store.Audits) == 0 {
		t.Fatal("no Raw audit records")
	}
	for _, audit := range store.Audits {
		if audit.RequestID == "" || audit.TraceID == "" {
			t.Fatalf("missing correlation fields: %#v", audit)
		}
		if strings.Contains(audit.Resource, "token") || strings.Contains(audit.Resource, "secret") {
			t.Fatalf("audit resource exposes request secret: %#v", audit)
		}
	}
	first := store.Audits[0]
	if first.RequestID != "request-42" || first.TraceID != traceID.String() {
		t.Fatalf("propagated correlation = %#v", first)
	}
	last := store.Audits[len(store.Audits)-1]
	if last.Status != http.StatusBadRequest || last.Resource != "" {
		t.Fatalf("malformed request audit = %#v", last)
	}
	if metrics.rawGetRequests.Load() != 9 || metrics.rawOtherRequests.Load() != 1 || metrics.rawAuthorizationDenied.Load() != 2 || metrics.rawCacheHit.Load() != 1 || metrics.rawCacheMiss.Load() != 5 || metrics.rawNegativeHit.Load() != 1 || metrics.rawProxyDenied.Load() != 1 || metrics.rawChecksumFailure.Load() != 1 || metrics.rawUpstreamFailure.Load() != 1 || metrics.rawResponseBytes.Load() != 16 {
		t.Fatalf("Raw metrics = %#v", metrics)
	}
	response := httptest.NewRecorder()
	metrics.Handler(response, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	for _, want := range []string{
		"artifact_gateway_raw_requests_total{method=\"get\"} 9",
		"artifact_gateway_raw_authorization_denials_total 2",
		"artifact_gateway_raw_cache_requests_total{outcome=\"hit\"} 1",
		"artifact_gateway_raw_cache_requests_total{outcome=\"miss\"} 5",
		"artifact_gateway_raw_negative_cache_hits_total 1",
		"artifact_gateway_raw_proxy_denied_total 1",
		"artifact_gateway_raw_checksum_failures_total 1",
		"artifact_gateway_raw_upstream_failures_total 1",
		"artifact_gateway_raw_response_bytes_total 16",
	} {
		if !strings.Contains(response.Body.String(), want) {
			t.Fatalf("metrics missing %q:\n%s", want, response.Body.String())
		}
	}
}

func TestGatewayRoutesRawRequests(t *testing.T) {
	store := repository.NewMemoryStore()
	_, _ = store.CreateRawGroup(context.Background(), repository.Group{Name: "downloads", Members: []repository.Member{{Name: "hosted", Type: repository.MemberHosted, Endpoint: "http://gitea.local"}}})
	client := &rawFixtureClient{responses: map[string]int{"hosted": http.StatusOK}, body: []byte("artifact")}
	handler := NewGatewayHandlerWithRawCache(Dependencies{}, store, TestAdapter{}, testAuthenticator(), NewDefaultOCICache(NewMemoryOCIObjectStore(), nil), nil, NewRawCache(NewMemoryOCIObjectStore(), time.Hour, time.Hour, nil), nil, client)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, rawRequest(http.MethodGet, "/raw/downloads/release/app.txt"))
	if w.Code != http.StatusOK || w.Body.String() != "artifact" {
		t.Fatalf("route = %d %q", w.Code, w.Body.String())
	}
}

func TestRawDoesNotExposeOCIGroupAndChecksMemberGrantBeforeCache(t *testing.T) {
	store := repository.NewMemoryStore()
	_, _ = store.CreateGroup(context.Background(), repository.Group{Name: "shared", Members: []repository.Member{{Name: "oci", Type: repository.MemberHosted}}})
	client := &rawFixtureClient{responses: map[string]int{"hosted": http.StatusOK}, body: []byte("artifact")}
	denied := RawHandler{Store: store, Authenticator: Authenticator{ResolverToken: "resolver-secret", RepositoryReaders: map[string][]string{"build-agent": {"downloads"}}}, Client: client, Cache: NewRawCache(NewMemoryOCIObjectStore(), time.Hour, time.Hour, nil)}
	w := httptest.NewRecorder()
	denied.ServeHTTP(w, rawRequest(http.MethodGet, "/raw/shared/a"))
	if w.Code != http.StatusNotFound {
		t.Fatalf("OCI group exposed as Raw: %d", w.Code)
	}
	_, _ = store.CreateRawGroup(context.Background(), repository.Group{Name: "downloads", Members: []repository.Member{{Name: "hosted", Type: repository.MemberHosted}}})
	w = httptest.NewRecorder()
	denied.ServeHTTP(w, rawRequest(http.MethodGet, "/raw/downloads/a"))
	if w.Code != http.StatusForbidden || len(client.Calls()) != 0 {
		t.Fatalf("member grant status=%d calls=%v", w.Code, client.Calls())
	}
}

func TestGiteaRawClientDecodesCanonicalPath(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/a b" {
			t.Fatalf("path=%q", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()
	client := GiteaClient{}
	response, err := client.FetchRaw(context.Background(), http.MethodGet, repository.Member{Type: repository.MemberHosted, Endpoint: upstream.URL}, "a%20b", nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
}

func TestRawStandardHTTPClientE2E(t *testing.T) {
	store := repository.NewMemoryStore()
	_, _ = store.CreateRawGroup(context.Background(), repository.Group{Name: "downloads", Members: []repository.Member{
		{Name: "hosted", Type: repository.MemberHosted, Endpoint: "http://gitea.local", Position: 0},
		{Name: "proxy", Type: repository.MemberProxy, Endpoint: "https://proxy.example", Position: 1, AllowedHosts: []string{"proxy.example"}},
	}})
	_, _ = store.CreateRawGroup(context.Background(), repository.Group{Name: "outage", Members: []repository.Member{{Name: "hosted", Type: repository.MemberHosted, Endpoint: "http://gitea.local"}}})
	client := &rawFixtureClient{responses: map[string]int{"hosted": http.StatusNotFound, "proxy": http.StatusOK}, body: []byte("artifact")}
	handler := RawHandler{Store: store, Authenticator: testAuthenticator(), Client: client, Metrics: &Metrics{}, Cache: NewRawCache(NewMemoryOCIObjectStore(), time.Hour, time.Hour, []string{"proxy.example"})}
	server := httptest.NewServer(handler)
	defer server.Close()

	do := func(method, path string, headers http.Header) *http.Response {
		t.Helper()
		r, err := http.NewRequest(method, server.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		for name, values := range headers {
			r.Header[name] = values
		}
		response, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		return response
	}
	authorized := http.Header{"Authorization": []string{"Bearer resolver-secret"}}

	response := do(http.MethodGet, "/raw/downloads/release/app.txt", authorized)
	body, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK || string(body) != "artifact" || response.Header.Get("Content-Type") != "text/plain" || response.Header.Get("Content-Length") != "8" {
		t.Fatalf("GET status=%d headers=%v body=%q", response.StatusCode, response.Header, body)
	}
	if got := client.Calls(); len(got) != 2 || got[0] != "hosted" || got[1] != "proxy" {
		t.Fatalf("resolution calls=%v", got)
	}

	response = do(http.MethodHead, "/raw/downloads/release/app.txt", authorized)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK || response.Header.Get("Content-Length") != "8" {
		t.Fatalf("HEAD status=%d headers=%v", response.StatusCode, response.Header)
	}
	rangeHeaders := authorized.Clone()
	rangeHeaders.Set("Range", "bytes=1-3")
	response = do(http.MethodGet, "/raw/downloads/release/app.txt", rangeHeaders)
	body, _ = io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusPartialContent || string(body) != "rti" {
		t.Fatalf("range status=%d body=%q", response.StatusCode, body)
	}
	if got := client.Calls(); len(got) != 2 {
		t.Fatalf("cache did not isolate upstream: calls=%v", got)
	}

	response = do(http.MethodGet, "/raw/downloads/release/app.txt", http.Header{})
	_ = response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous status=%d", response.StatusCode)
	}

	deniedHandler := RawHandler{Store: store, Authenticator: Authenticator{ResolverToken: "resolver-secret", RepositoryReaders: map[string][]string{"reader": {"other"}}}, Client: client, Metrics: &Metrics{}, Cache: handler.Cache}
	denied := httptest.NewServer(deniedHandler)
	defer denied.Close()
	r, _ := http.NewRequest(http.MethodGet, denied.URL+"/raw/downloads/release/app.txt", nil)
	r.Header.Set("Authorization", "Bearer resolver-secret")
	response, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("permission status=%d", response.StatusCode)
	}

	client.responses["hosted"] = http.StatusBadGateway
	response = do(http.MethodGet, "/raw/outage/release/app.txt", authorized)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusBadGateway {
		t.Fatalf("upstream status=%d", response.StatusCode)
	}
}

func TestRawHostedStandardHTTPClientE2E(t *testing.T) {
	var upstreamCalls int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls++
		user, password, ok := r.BasicAuth()
		if !ok || user != "gitea" || password != "gitea-token" {
			http.Error(w, "missing hosted credentials", http.StatusUnauthorized)
			return
		}
		if r.URL.Path != "/release/app.txt" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("hosted-artifact"))
	}))
	defer upstream.Close()
	store := repository.NewMemoryStore()
	_, _ = store.CreateRawGroup(context.Background(), repository.Group{Name: "hosted", Members: []repository.Member{{Name: "gitea", Type: repository.MemberHosted, Endpoint: upstream.URL}}})
	handler := RawHandler{Store: store, Authenticator: testAuthenticator(), Client: GiteaClient{Username: "gitea", Token: "gitea-token"}, Metrics: &Metrics{}, Cache: NewRawCache(NewMemoryOCIObjectStore(), time.Hour, time.Hour, nil)}
	gateway := httptest.NewServer(handler)
	defer gateway.Close()

	request, err := http.NewRequest(http.MethodGet, gateway.URL+"/raw/hosted/release/app.txt", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer resolver-secret")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK || string(body) != "hosted-artifact" || response.Header.Get("Content-Length") != "15" {
		t.Fatalf("hosted response=%d headers=%v body=%q", response.StatusCode, response.Header, body)
	}

	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK || upstreamCalls != 1 {
		t.Fatalf("hosted cache response=%d upstream calls=%d", response.StatusCode, upstreamCalls)
	}
}

func TestRawFetchesCanonicalRepresentationAndBoundsObjectSize(t *testing.T) {
	store := repository.NewMemoryStore()
	_, _ = store.CreateRawGroup(context.Background(), repository.Group{Name: "downloads", Members: []repository.Member{{Name: "hosted", Type: repository.MemberHosted, Endpoint: "http://gitea.local"}}})
	cache := NewRawCache(NewMemoryOCIObjectStore(), time.Hour, time.Hour, nil).WithMaxObjectBytes(8)
	client := &rawFixtureClient{responses: map[string]int{"hosted": http.StatusOK}, body: []byte("artifact")}
	h := RawHandler{Store: store, Authenticator: testAuthenticator(), Client: client, Metrics: &Metrics{}, Cache: cache}
	r := rawRequest(http.MethodGet, "/raw/downloads/release/app.txt")
	r.Header.Set("Range", "bytes=0-1")
	r.Header.Set("Accept", "application/not-cached")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusPartialContent || w.Body.String() != "ar" {
		t.Fatalf("range response=%d body=%q", w.Code, w.Body.String())
	}

	client.body = []byte("different")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, rawRequest(http.MethodGet, "/raw/downloads/release/app.txt"))
	if w.Code != http.StatusOK || w.Body.String() != "artifact" || len(client.Calls()) != 1 {
		t.Fatalf("cached response=%d body=%q calls=%v", w.Code, w.Body.String(), client.Calls())
	}

	_, _ = store.CreateRawGroup(context.Background(), repository.Group{Name: "oversize", Members: []repository.Member{{Name: "large", Type: repository.MemberHosted, Endpoint: "http://gitea.local"}}})
	client.responses["large"] = http.StatusOK
	client.body = []byte("too-large")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, rawRequest(http.MethodGet, "/raw/oversize/release/app.txt"))
	if w.Code != http.StatusBadGateway {
		t.Fatalf("oversize response=%d", w.Code)
	}
}

func TestRawCacheCollectorKeepsLiveReferences(t *testing.T) {
	store := NewMemoryOCIObjectStore()
	cache := NewRawCache(store, time.Hour, time.Hour, nil)
	live := cache.key("g", "live", "m", "https://host")
	expired := cache.key("g", "expired", "m", "https://host")
	if err := cache.Store(context.Background(), live, RawContent{Body: []byte("live"), Repository: "g"}); err != nil {
		t.Fatal(err)
	}
	if err := cache.Store(context.Background(), expired, RawContent{Body: []byte("expired"), Repository: "g"}); err != nil {
		t.Fatal(err)
	}
	encoded, err := store.Get(context.Background(), expired)
	if err != nil {
		t.Fatal(err)
	}
	var index rawIndex
	if err := json.Unmarshal(encoded, &index); err != nil {
		t.Fatal(err)
	}
	index.ExpiresAt = time.Now().UTC().Add(-time.Second)
	encoded, _ = json.Marshal(index)
	if err := store.Put(context.Background(), expired, encoded); err != nil {
		t.Fatal(err)
	}
	if err := cache.CollectGarbage(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.Load(context.Background(), live); err != nil {
		t.Fatalf("live cache = %v", err)
	}
	if _, err := cache.Load(context.Background(), expired); err == nil {
		t.Fatal("expired index remained readable")
	}
	cache.Invalidate(context.Background(), live)
	if err := cache.CollectGarbage(context.Background()); err != nil {
		t.Fatal(err)
	}
	objects, err := store.List(context.Background(), "raw/objects/")
	if err != nil || len(objects) != 0 {
		t.Fatalf("objects=%v err=%v", objects, err)
	}
}

func TestRawCacheInvalidationKeepsObjectsReferencedByOtherIndexes(t *testing.T) {
	store := NewMemoryOCIObjectStore()
	cacheA := NewRawCache(store, time.Hour, time.Hour, nil)
	cacheB := NewRawCache(store, time.Hour, time.Hour, nil)
	liveKey := cacheA.key("g", "live", "m", "https://host")
	badKey := cacheB.key("g", "bad", "m", "https://host")
	content := RawContent{Body: []byte("shared"), Repository: "g"}
	if err := cacheA.Store(context.Background(), liveKey, content); err != nil {
		t.Fatal(err)
	}
	liveEncoded, err := store.Get(context.Background(), liveKey)
	if err != nil {
		t.Fatal(err)
	}
	var live rawIndex
	if err := json.Unmarshal(liveEncoded, &live); err != nil {
		t.Fatal(err)
	}
	badEncoded, err := json.Marshal(rawIndex{Object: live.Object, Digest: "not-the-object-digest", ExpiresAt: time.Now().UTC().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Put(context.Background(), badKey, badEncoded); err != nil {
		t.Fatal(err)
	}
	if _, err := cacheB.Load(context.Background(), badKey); !errors.Is(err, errRawCacheMiss) {
		t.Fatalf("bad cache load = %v", err)
	}
	loaded, err := cacheA.Load(context.Background(), liveKey)
	if err != nil || string(loaded.Body) != "shared" {
		t.Fatalf("live cache after invalidation = %q, err = %v", loaded.Body, err)
	}
}
