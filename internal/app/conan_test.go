package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

func conanRequest(path string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, path, nil)
	r.Header.Set("Authorization", "Bearer resolver-secret")
	return r
}

func TestConanHostedRecipeAndPackageFilesAreChecksumVerifiedAndCached(t *testing.T) {
	artifact := []byte("package archive")
	sum := sha256.Sum256(artifact)
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path == "/conans/pkg/1.0/user/stable/revisions/rrev/packages/package-id/revisions/prev/files" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"files":{"package.tgz":{"sha256":"` + hex.EncodeToString(sum[:]) + `","size":15}}}`))
			return
		}
		if r.URL.Path == "/conans/pkg/1.0/user/stable/revisions/rrev/packages/package-id/revisions/prev/files/package.tgz" {
			_, _ = w.Write(artifact)
			return
		}
		http.NotFound(w, r)
	}))
	defer upstream.Close()
	store := repository.NewMemoryStore()
	_, _ = store.CreateConanGroup(context.Background(), repository.Group{Name: "central", Members: []repository.Member{{Name: "hosted", Type: repository.MemberHosted, Endpoint: upstream.URL}}})
	h := ConanHandler{Store: store, Authenticator: testAuthenticator(), Client: GiteaClient{}, Cache: NewConanCache(nil), Metrics: &Metrics{}}
	path := "/conan/v2/central/conans/pkg/1.0/user/stable/revisions/rrev/packages/package-id/revisions/prev/files/package.tgz"
	for range 2 {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, conanRequest(path))
		if w.Code != http.StatusOK || w.Body.String() != string(artifact) {
			t.Fatalf("response=%d %q", w.Code, w.Body.String())
		}
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("upstream calls=%d, want file + metadata once", got)
	}
}

func TestConanChecksumMismatchIsNotCached(t *testing.T) {
	store := repository.NewMemoryStore()
	_, _ = store.CreateConanGroup(context.Background(), repository.Group{Name: "central", Members: []repository.Member{{Name: "hosted", Type: repository.MemberHosted, Endpoint: "test://hosted"}}})
	client := &conanChecksumClient{}
	cache := NewConanCache(nil)
	h := ConanHandler{Store: store, Authenticator: testAuthenticator(), Client: client, Cache: cache, Metrics: &Metrics{}}
	path := "/conan/v2/central/conans/pkg/1.0/user/stable/revisions/rrev/files/conanfile.py"
	for range 2 {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, conanRequest(path))
		if w.Code != http.StatusBadGateway {
			t.Fatalf("response=%d", w.Code)
		}
	}
	if client.fileCalls != 2 {
		t.Fatalf("bad artifact was cached: calls=%d", client.fileCalls)
	}
}

type conanChecksumClient struct{ fileCalls int }

func (c *conanChecksumClient) FetchConan(_ context.Context, _ string, _ repository.Member, path string, _ http.Header) (*http.Response, error) {
	if strings.HasSuffix(path, "/files") {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"files":{"conanfile.py":{"sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","size":3}}}`)), Header: http.Header{}}, nil
	}
	c.fileCalls++
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("bad")), Header: http.Header{}}, nil
}

func TestConanHostedPrecedesProxyAndCachesNotFound(t *testing.T) {
	var hostedCalls, proxyCalls atomic.Int32
	hosted := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hostedCalls.Add(1); http.NotFound(w, r) }))
	defer hosted.Close()
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxyCalls.Add(1)
		_, _ = w.Write([]byte(`{"revisions":[{"revision":"rrev","time":1}]}`))
	}))
	defer proxy.Close()
	store := repository.NewMemoryStore()
	_, _ = store.CreateConanGroup(context.Background(), repository.Group{Name: "central", Members: []repository.Member{{Name: "proxy", Type: repository.MemberProxy, Endpoint: "https://proxy.example", Position: 9}, {Name: "hosted", Type: repository.MemberHosted, Endpoint: hosted.URL, Position: 0}}})
	// Use a fixture client for Proxy to avoid requiring TLS while still proving
	// resolution order and the configured allowlist contract.
	client := conanFixtureClient{hostedURL: hosted.URL, proxyURL: proxy.URL}
	h := ConanHandler{Store: store, Authenticator: testAuthenticator(), Client: &client, Cache: NewConanCache([]string{"proxy.example"}), Metrics: &Metrics{}}
	path := "/conan/v2/central/conans/pkg/1.0/user/stable/revisions"
	for range 2 {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, conanRequest(path))
		if w.Code != http.StatusOK {
			t.Fatalf("response=%d %s", w.Code, w.Body.String())
		}
	}
	if hostedCalls.Load() != 1 || proxyCalls.Load() != 1 {
		t.Fatalf("hosted=%d proxy=%d", hostedCalls.Load(), proxyCalls.Load())
	}

	missing := "/conan/v2/central/conans/pkg/2.0/user/stable/revisions"
	client.notFound = true
	for range 2 {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, conanRequest(missing))
		if w.Code != http.StatusNotFound {
			t.Fatalf("missing=%d", w.Code)
		}
	}
	if client.calls != 4 {
		t.Fatalf("negative cache calls=%d", client.calls)
	}
}

type conanFixtureClient struct {
	hostedURL, proxyURL string
	calls               int
	notFound            bool
}

func (c *conanFixtureClient) FetchConan(_ context.Context, _ string, member repository.Member, _ string, _ http.Header) (*http.Response, error) {
	c.calls++
	if c.notFound {
		return &http.Response{StatusCode: http.StatusNotFound, Body: io.NopCloser(strings.NewReader("")), Header: http.Header{}}, nil
	}
	url := c.hostedURL
	if member.Type == repository.MemberProxy {
		url = c.proxyURL
	}
	return http.Get(url)
}

func TestConanRejectsMalformedAndUnsupportedRoutes(t *testing.T) {
	for _, path := range []string{
		"/conan/v2/central/conans/pkg/1.0/user/stable/revisions/%23bad/files",
		"/conan/v2/central/conans/pkg/1.0/user/stable/revisions/rrev/files/a%2fb",
		"/conan/v2/central/conans/pkg/1.0/user/stable/search",
	} {
		if _, _, _, _, ok := parseConanPath(http.MethodGet, path); ok {
			t.Fatalf("accepted %s", path)
		}
	}
}

func TestConanAnonymousReadRequiresPublicGroupAndMember(t *testing.T) {
	store := repository.NewMemoryStore()
	for _, group := range []repository.Group{
		{Name: "private", Members: []repository.Member{{Name: "hosted", Type: repository.MemberHosted, Anonymous: true}}},
		{Name: "partial", Anonymous: true, Members: []repository.Member{{Name: "hosted", Type: repository.MemberHosted}}},
		{Name: "public", Anonymous: true, Members: []repository.Member{{Name: "private", Type: repository.MemberHosted}, {Name: "public", Type: repository.MemberHosted, Anonymous: true}}},
	} {
		if _, err := store.CreateConanGroup(context.Background(), group); err != nil {
			t.Fatal(err)
		}
	}
	client := &conanAnonymousClient{}
	h := ConanHandler{Store: store, Authenticator: testAuthenticator(), Client: client, Cache: NewConanCache(nil), Metrics: &Metrics{}}
	path := "/conan/v2/%s/conans/pkg/1.0/user/stable/revisions"
	for _, group := range []string{"private", "partial"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, fmt.Sprintf(path, group), nil))
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("%s status=%d", group, w.Code)
		}
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, fmt.Sprintf(path, "public"), nil))
	if w.Code != http.StatusOK || client.member != "public" {
		t.Fatalf("response=%d member=%q", w.Code, client.member)
	}
}

type conanAnonymousClient struct{ member string }

func (c *conanAnonymousClient) FetchConan(_ context.Context, _ string, member repository.Member, _ string, _ http.Header) (*http.Response, error) {
	c.member = member.Name
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"revisions":[{"revision":"rrev","time":1}]}`))}, nil
}
