package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
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
	_, err := store.CreateGroup(context.Background(), repository.Group{Name: "downloads", Members: []repository.Member{
		{Name: "hosted", Type: repository.MemberHosted, Endpoint: "http://gitea.local", Position: 0},
		{Name: "proxy", Type: repository.MemberProxy, Endpoint: "https://proxy.example", Position: 1},
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

func TestRawRejectsUnsafePathsAndUnauthorizedRequests(t *testing.T) {
	store := repository.NewMemoryStore()
	_, _ = store.CreateGroup(context.Background(), repository.Group{Name: "downloads", Members: []repository.Member{{Name: "hosted", Type: repository.MemberHosted, Endpoint: "http://gitea.local"}}})
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
		if _, err := store.CreateGroup(context.Background(), group); err != nil {
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
	if len(store.Audits) == 0 || store.Audits[len(store.Audits)-1].Actor != "anonymous" || store.Audits[len(store.Audits)-1].Outcome != repository.AuditResolved {
		t.Fatalf("anonymous audit=%#v", store.Audits)
	}
}

func TestRawProxyDenialAndNegativeCache(t *testing.T) {
	store := repository.NewMemoryStore()
	_, _ = store.CreateGroup(context.Background(), repository.Group{Name: "downloads", Members: []repository.Member{{Name: "blocked", Type: repository.MemberProxy, Endpoint: "http://blocked.example"}}})
	client := &rawFixtureClient{responses: map[string]int{"blocked": http.StatusNotFound}}
	h := RawHandler{Store: store, Authenticator: testAuthenticator(), Client: client, Metrics: &Metrics{}, Cache: NewRawCache(NewMemoryOCIObjectStore(), time.Hour, time.Hour, []string{"blocked.example"})}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, rawRequest(http.MethodGet, "/raw/downloads/a"))
	if w.Code != http.StatusForbidden || len(client.Calls()) != 0 {
		t.Fatalf("denial=%d calls=%v", w.Code, client.Calls())
	}
	_, _ = store.CreateGroup(context.Background(), repository.Group{Name: "cached", Members: []repository.Member{{Name: "proxy", Type: repository.MemberProxy, Endpoint: "https://proxy.example"}}})
	client.responses["proxy"] = http.StatusNotFound
	for range 2 {
		w = httptest.NewRecorder()
		h.Cache.allowed = map[string]struct{}{"proxy.example": {}}
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

func TestRawHeadConditionalAndChecksumSidecars(t *testing.T) {
	store := repository.NewMemoryStore()
	_, _ = store.CreateGroup(context.Background(), repository.Group{Name: "downloads", Members: []repository.Member{{Name: "hosted", Type: repository.MemberHosted, Endpoint: "http://gitea.local"}}})
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

func TestGatewayRoutesRawRequests(t *testing.T) {
	store := repository.NewMemoryStore()
	_, _ = store.CreateGroup(context.Background(), repository.Group{Name: "downloads", Members: []repository.Member{{Name: "hosted", Type: repository.MemberHosted, Endpoint: "http://gitea.local"}}})
	client := &rawFixtureClient{responses: map[string]int{"hosted": http.StatusOK}, body: []byte("artifact")}
	handler := NewGatewayHandlerWithRawCache(Dependencies{}, store, TestAdapter{}, testAuthenticator(), NewDefaultOCICache(NewMemoryOCIObjectStore(), nil), nil, NewRawCache(NewMemoryOCIObjectStore(), time.Hour, time.Hour, nil), nil, client)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, rawRequest(http.MethodGet, "/raw/downloads/release/app.txt"))
	if w.Code != http.StatusOK || w.Body.String() != "artifact" {
		t.Fatalf("route = %d %q", w.Code, w.Body.String())
	}
}

func TestRawStandardHTTPClientE2E(t *testing.T) {
	store := repository.NewMemoryStore()
	_, _ = store.CreateGroup(context.Background(), repository.Group{Name: "downloads", Members: []repository.Member{
		{Name: "hosted", Type: repository.MemberHosted, Endpoint: "http://gitea.local", Position: 0},
		{Name: "proxy", Type: repository.MemberProxy, Endpoint: "https://proxy.example", Position: 1},
	}})
	_, _ = store.CreateGroup(context.Background(), repository.Group{Name: "outage", Members: []repository.Member{{Name: "hosted", Type: repository.MemberHosted, Endpoint: "http://gitea.local"}}})
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
	_, _ = store.CreateGroup(context.Background(), repository.Group{Name: "hosted", Members: []repository.Member{{Name: "gitea", Type: repository.MemberHosted, Endpoint: upstream.URL}}})
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
	_, _ = store.CreateGroup(context.Background(), repository.Group{Name: "downloads", Members: []repository.Member{{Name: "hosted", Type: repository.MemberHosted, Endpoint: "http://gitea.local"}}})
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

	_, _ = store.CreateGroup(context.Background(), repository.Group{Name: "oversize", Members: []repository.Member{{Name: "large", Type: repository.MemberHosted, Endpoint: "http://gitea.local"}}})
	client.responses["large"] = http.StatusOK
	client.body = []byte("too-large")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, rawRequest(http.MethodGet, "/raw/oversize/release/app.txt"))
	if w.Code != http.StatusBadGateway {
		t.Fatalf("oversize response=%d", w.Code)
	}
}
