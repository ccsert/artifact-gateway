package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

func TestRawCacheUsesPersistedGroupQuota(t *testing.T) {
	store := NewMemoryOCIObjectStore()
	cache := NewRawCache(store, time.Hour, time.Hour, nil).WithQuota(NewCacheQuota(store, nil))
	first := cache.Key("downloads", "one", "hosted", "http://legacy.local")
	if err := cache.Store(context.Background(), first, RawContent{Body: []byte("1234"), Repository: "downloads", CacheQuotaBytes: 5}); err != nil {
		t.Fatal(err)
	}
	second := cache.Key("downloads", "two", "hosted", "http://legacy.local")
	if err := cache.Store(context.Background(), second, RawContent{Body: []byte("23"), Repository: "downloads", CacheQuotaBytes: 5}); !errors.Is(err, ErrCacheQuotaExceeded) {
		t.Fatalf("second cache store = %v, want quota rejection", err)
	}
}

func TestRawCacheLoadsLegacyIndex(t *testing.T) {
	store := NewMemoryOCIObjectStore()
	cache := NewRawCache(store, time.Hour, time.Hour, nil)
	key := cache.Key("downloads", "artifact", "hosted", "http://legacy.local")
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
	}{Object: object, Digest: digest, ContentType: "text/plain", Member: "hosted", Endpoint: "http://legacy.local", Repository: "downloads", Size: int64(len(body)), ExpiresAt: time.Now().UTC().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Put(context.Background(), key, legacy); err != nil {
		t.Fatal(err)
	}
	content, err := cache.Load(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	reader, _, err := content.Open(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	loaded, readErr := io.ReadAll(reader)
	closeErr := reader.Close()
	if readErr != nil || closeErr != nil || string(loaded) != "artifact" {
		t.Fatalf("legacy cache load content=%q readErr=%v closeErr=%v", loaded, readErr, closeErr)
	}
}

func TestRawCacheQuotaCountsLegacyIndex(t *testing.T) {
	store := NewMemoryOCIObjectStore()
	cache := NewRawCache(store, time.Hour, time.Hour, nil).WithQuota(NewCacheQuota(store, nil))
	key := cache.Key("downloads", "existing", "hosted", "http://legacy.local")
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
	newKey := cache.Key("downloads", "new", "hosted", "http://legacy.local")
	if err := cache.Store(context.Background(), newKey, RawContent{Body: []byte("x"), Repository: "downloads", CacheQuotaBytes: 5}); !errors.Is(err, ErrCacheQuotaExceeded) {
		t.Fatalf("legacy quota admission = %v, want quota rejection", err)
	}
}

func TestRawQuotaExcludesSameNamedOCICacheAndRecordsRejection(t *testing.T) {
	store := NewMemoryOCIObjectStore()
	quota := NewCacheQuota(store, nil)
	ociCache := NewDefaultOCICache(store, nil)
	ociKey := ociCache.Key("downloads", "downloads", ociManifest, "latest")
	if err := ociCache.Store(context.Background(), ociKey, CachedOCIContent{Body: []byte("12345"), Digest: digestOf([]byte("12345")), Repository: "downloads"}); err != nil {
		t.Fatal(err)
	}
	rawCache := NewRawCache(store, time.Hour, time.Hour, nil).WithQuota(quota)
	key := rawCache.Key("downloads", "artifact", "hosted", "http://legacy.local")
	if err := rawCache.Store(context.Background(), key, RawContent{Body: []byte("12345"), Repository: "downloads", CacheQuotaBytes: 5}); err != nil {
		t.Fatalf("same-named OCI cache consumed Raw quota: %v", err)
	}

	groups := repository.NewMemoryStore()
	_, err := groups.CreateRawGroup(context.Background(), repository.Group{Name: "limited", CacheQuotaBytes: 1, Members: []repository.Member{{Name: "hosted", Type: repository.MemberHosted, Endpoint: "http://legacy.local"}}})
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

func TestRawProxyValidatesDirectRequestsWhenEgressIsBypassed(t *testing.T) {
	for _, test := range []struct {
		name  string
		proxy func(*http.Request) (*url.URL, error)
	}{
		{"HTTP proxy does not apply to HTTPS", func(*http.Request) (*url.URL, error) { return nil, nil }},
		{"NO_PROXY bypass", func(*http.Request) (*url.URL, error) { return nil, nil }},
	} {
		t.Run(test.name, func(t *testing.T) {
			previousLookup := rawProxyLookupIP
			previousProxy := rawProxyFromEnvironment
			rawProxyLookupIP = func(_ context.Context, network, name string) ([]net.IP, error) {
				if network != "ip" || name != "private.example" {
					t.Fatalf("lookup=%s %s", network, name)
				}
				return []net.IP{net.ParseIP("127.0.0.1")}, nil
			}
			rawProxyFromEnvironment = test.proxy
			t.Cleanup(func() {
				rawProxyLookupIP = previousLookup
				rawProxyFromEnvironment = previousProxy
			})
			_, err := (UpstreamClient{}).FetchRaw(context.Background(), http.MethodGet, repository.Member{Type: repository.MemberProxy, Endpoint: "https://private.example", AllowedHosts: []string{"private.example"}}, "artifact", nil)
			if err == nil || !strings.Contains(err.Error(), "private address") {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestRawProxyEgressClientClearsCustomDialers(t *testing.T) {
	client := rawProxyEgressClient(&http.Client{Transport: &http.Transport{DialContext: func(context.Context, string, string) (net.Conn, error) { return nil, nil }, Dial: func(string, string) (net.Conn, error) { return nil, nil }}})
	transport, ok := client.Transport.(*http.Transport)
	if !ok || transport.DialContext != nil || transport.Dial != nil || transport.Proxy == nil { //nolint:staticcheck // The regression explicitly covers the legacy Dial hook.
		t.Fatalf("egress transport did not use standard proxy dialing")
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
			_, err := (UpstreamClient{HTTPClient: client}).FetchRaw(context.Background(), http.MethodGet, repository.Member{Type: repository.MemberProxy, Endpoint: "https://example.com", AllowedHosts: []string{"example.com"}}, "artifact", nil)
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
	enableAnonymousAccess(t, store)
	_, _ = store.CreateRawGroup(context.Background(), repository.Group{Name: "downloads", Members: []repository.Member{{Name: "proxy", Type: repository.MemberProxy, Endpoint: endpoint, AllowedHosts: []string{"example.com"}}}})
	_, _ = store.CreateRawGroup(context.Background(), repository.Group{Name: "public", Anonymous: true, Members: []repository.Member{{Name: "proxy", Type: repository.MemberProxy, Endpoint: endpoint, AllowedHosts: []string{"example.com"}, Anonymous: true}}})
	cache := NewRawCache(NewMemoryOCIObjectStore(), time.Hour, time.Hour, []string{"example.com"})
	gateway := httptest.NewServer(RawHandler{Store: store, Authenticator: testAuthenticator(), Client: UpstreamClient{HTTPClient: upstream.Client()}, Metrics: &Metrics{}, Cache: cache})
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

	if _, err := (UpstreamClient{}).FetchRaw(context.Background(), http.MethodGet, repository.Member{Type: repository.MemberProxy, Endpoint: "https://private.example", AllowedHosts: []string{"private.example"}}, "artifact", nil); err == nil {
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
	gateway := httptest.NewServer(RawHandler{Store: store, Authenticator: testAuthenticator(), Client: UpstreamClient{HTTPClient: upstream.Client()}, Metrics: &Metrics{}, Cache: cache})
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
	// These tests exercise direct, DNS-pinned dialing. Isolate them from a
	// developer or CI egress proxy that deliberately selects a different path.
	for _, key := range []string{"HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "https_proxy", "NO_PROXY", "no_proxy"} {
		t.Setenv(key, "")
	}
	previousLookup, previousDial, previousProxy := rawProxyLookupIP, rawProxyDialContext, rawProxyFromEnvironment
	rawProxyLookupIP, rawProxyDialContext = lookup, dial
	rawProxyFromEnvironment = func(*http.Request) (*url.URL, error) { return nil, nil }
	t.Cleanup(func() {
		rawProxyLookupIP, rawProxyDialContext, rawProxyFromEnvironment = previousLookup, previousDial, previousProxy
	})
}

func TestRawCacheCollectorKeepsLiveReferences(t *testing.T) {
	store := NewMemoryOCIObjectStore()
	cache := NewRawCache(store, time.Hour, time.Hour, nil)
	live := cache.Key("g", "live", "m", "https://host")
	expired := cache.Key("g", "expired", "m", "https://host")
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
	var index struct {
		ExpiresAt time.Time `json:"expires_at"`
	}
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
	liveKey := cacheA.Key("g", "live", "m", "https://host")
	badKey := cacheB.Key("g", "bad", "m", "https://host")
	content := RawContent{Body: []byte("shared"), Repository: "g"}
	if err := cacheA.Store(context.Background(), liveKey, content); err != nil {
		t.Fatal(err)
	}
	liveEncoded, err := store.Get(context.Background(), liveKey)
	if err != nil {
		t.Fatal(err)
	}
	var live struct {
		Object string `json:"object"`
	}
	if err := json.Unmarshal(liveEncoded, &live); err != nil {
		t.Fatal(err)
	}
	badEncoded, err := json.Marshal(struct {
		Object    string    `json:"object"`
		Digest    string    `json:"digest"`
		ExpiresAt time.Time `json:"expires_at"`
	}{Object: live.Object, Digest: "not-the-object-digest", ExpiresAt: time.Now().UTC().Add(time.Hour)})
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
	if err != nil {
		t.Fatal(err)
	}
	reader, _, err := loaded.Open(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	body, readErr := io.ReadAll(reader)
	closeErr := reader.Close()
	if readErr != nil || closeErr != nil || string(body) != "shared" {
		t.Fatalf("live cache after invalidation = %q, readErr = %v, closeErr = %v", body, readErr, closeErr)
	}
}
