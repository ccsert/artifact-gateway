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

func TestRawProxyDenialAndNegativeCache(t *testing.T) {
	store := repository.NewMemoryStore()
	_, _ = store.CreateGroup(context.Background(), repository.Group{Name: "downloads", Members: []repository.Member{{Name: "blocked", Type: repository.MemberProxy, Endpoint: "http://blocked.example"}}})
	client := &rawFixtureClient{responses: map[string]int{"blocked": http.StatusNotFound}}
	h := RawHandler{Store: store, Authenticator: testAuthenticator(), Client: client, Metrics: &Metrics{}, Cache: NewRawCache(NewMemoryOCIObjectStore(), time.Hour, time.Hour, []string{"blocked.example"})}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, rawRequest(http.MethodGet, "/raw/downloads/a"))
	if w.Code != http.StatusNotFound || len(client.Calls()) != 0 {
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
