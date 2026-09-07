package app

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

func TestV2GroupRawProxyFallbackOverTLS(t *testing.T) {
	var requests atomic.Int32
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if strings.HasPrefix(r.URL.Path, "/first/") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("proxy-artifact"))
	}))
	defer upstream.Close()
	host, port := rawTLSServerAddress(t, upstream.URL)
	withRawProxyNetwork(t, func(context.Context, string, string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("203.0.113.7")}, nil
	}, func(ctx context.Context, network, address string) (net.Conn, error) {
		if address != net.JoinHostPort("203.0.113.7", port) {
			t.Errorf("dial did not use validated address: %q", address)
		}
		return (&net.Dialer{}).DialContext(ctx, network, net.JoinHostPort(host, port))
	})
	store := repository.NewMemoryStore()
	var members []repository.GroupMember
	for i, name := range []string{"first", "second"} {
		repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{
			ID: name, Name: name, Format: repository.FormatRaw, Type: repository.RepositoryTypeProxy,
			Endpoint: "https://example.com:" + port + "/" + name, AllowedHosts: []string{"example.com"},
		})
		if err != nil {
			t.Fatal(err)
		}
		members = append(members, repository.GroupMember{RepositoryID: repo.ID, Position: i})
	}
	createV2Group(t, store, "tls-chain", repository.FormatRaw, members...)
	cache := NewRawCache(NewMemoryOCIObjectStore(), time.Hour, time.Hour, []string{"example.com"})
	handler := NewGatewayHandlerWithRawCache(Dependencies{NativeOCIObjectStore: NewMemoryOCIObjectStore()}, store, TestAdapter{}, testAuthenticator(), NewDefaultOCICache(NewMemoryOCIObjectStore(), nil), nil, cache, nil, UpstreamClient{HTTPClient: upstream.Client()})
	gateway := httptest.NewServer(handler)
	defer gateway.Close()
	for _, check := range []struct {
		method, path, byteRange, body string
		status, upstreamCalls         int
	}{
		{http.MethodGet, "artifact.txt", "", "proxy-artifact", 200, 2},
		{http.MethodGet, "artifact.txt", "bytes=0-4", "proxy", 206, 2},
		{http.MethodHead, "artifact.txt", "", "", 200, 2},
		{http.MethodHead, "uncached.txt", "", "", 200, 4},
		{http.MethodHead, "uncached.txt", "", "", 200, 5},
	} {
		r, err := http.NewRequest(check.method, gateway.URL+"/raw/tls-chain/"+check.path, nil)
		if err != nil {
			t.Fatal(err)
		}
		authorize(r, "resolver-secret")
		if check.byteRange != "" {
			r.Header.Set("Range", check.byteRange)
		}
		response, err := gateway.Client().Do(r)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if err != nil || response.StatusCode != check.status || string(body) != check.body || int(requests.Load()) != check.upstreamCalls {
			t.Fatalf("%s %s status=%d body=%q upstreamCalls=%d err=%v", check.method, check.path, response.StatusCode, body, requests.Load(), err)
		}
	}
}

func TestV2GroupRawInvalidChecksumDoesNotFallThrough(t *testing.T) {
	handler, _, client, _ := rawProxyGroupFixture(t, http.StatusOK, http.StatusOK)
	r := httptest.NewRequest(http.MethodGet, "/raw/raw-chain/release.txt.sha256", nil)
	authorize(r, "resolver-secret")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusBadGateway || !reflect.DeepEqual(client.Calls(), []string{"first"}) {
		t.Fatalf("status=%d body=%q calls=%v", w.Code, w.Body.String(), client.Calls())
	}
}

func TestV2GroupRawContinuesAfterProxyMiss(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		for _, firstStatus := range []int{http.StatusNotFound, http.StatusGone} {
			t.Run(method+http.StatusText(firstStatus), func(t *testing.T) {
				handler, cache, client, first := rawProxyGroupFixture(t, firstStatus, http.StatusOK)
				for attempt := 0; attempt < 2; attempt++ {
					r := httptest.NewRequest(method, "/raw/raw-chain/release.txt", nil)
					authorize(r, "resolver-secret")
					w := httptest.NewRecorder()
					handler.ServeHTTP(w, r)
					if w.Code != http.StatusOK || (method == http.MethodGet && w.Body.String() != "from-second-proxy") || (method == http.MethodHead && w.Body.Len() != 0) {
						t.Fatalf("attempt %d status=%d body=%q", attempt, w.Code, w.Body.String())
					}
				}
				want := []string{"first", "second"}
				if method == http.MethodHead {
					want = append(want, "second")
				}
				if !reflect.DeepEqual(client.Calls(), want) {
					t.Fatalf("calls=%v want=%v", client.Calls(), want)
				}
				key := cache.Key(first.Name, "release.txt", first.Name, first.Endpoint)
				if _, err := cache.Load(context.Background(), key); err != errRawCacheNegative {
					t.Fatalf("first member negative cache=%v", err)
				}
			})
		}
	}
}

func TestV2GroupRawPreservesTerminalErrors(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		for _, status := range []int{http.StatusForbidden, http.StatusInternalServerError} {
			t.Run(method+http.StatusText(status), func(t *testing.T) {
				handler, _, client, _ := rawProxyGroupFixture(t, status, http.StatusOK)
				r := httptest.NewRequest(method, "/raw/raw-chain/release.txt", nil)
				authorize(r, "resolver-secret")
				w := httptest.NewRecorder()
				handler.ServeHTTP(w, r)
				if w.Code != http.StatusBadGateway || !reflect.DeepEqual(client.Calls(), []string{"first"}) {
					t.Fatalf("status=%d calls=%v", w.Code, client.Calls())
				}
			})
		}
	}
}

func TestV2GroupRawAllProxyMissesReturnSingleNotFound(t *testing.T) {
	handler, _, client, _ := rawProxyGroupFixture(t, http.StatusNotFound, http.StatusGone)
	for i := 0; i < 2; i++ {
		r := httptest.NewRequest(http.MethodGet, "/raw/raw-chain/release.txt", nil)
		authorize(r, "resolver-secret")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != http.StatusNotFound || w.Body.String() != "404 page not found\n" {
			t.Fatalf("status=%d body=%q", w.Code, w.Body.String())
		}
	}
	if !reflect.DeepEqual(client.Calls(), []string{"first", "second"}) {
		t.Fatalf("calls=%v", client.Calls())
	}
}

func rawProxyGroupFixture(t *testing.T, firstStatus, secondStatus int) (http.Handler, *RawCache, *rawFixtureClient, repository.HostedRepository) {
	t.Helper()
	store := repository.NewMemoryStore()
	var repos []repository.HostedRepository
	for _, name := range []string{"first", "second"} {
		repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{
			ID: name, Name: name, Format: repository.FormatRaw, Type: repository.RepositoryTypeProxy,
			Endpoint: "https://" + name + ".example", AllowedHosts: []string{name + ".example"},
		})
		if err != nil {
			t.Fatal(err)
		}
		repos = append(repos, repo)
	}
	createV2Group(t, store, "raw-chain", repository.FormatRaw, repository.GroupMember{RepositoryID: repos[0].ID, Position: 0}, repository.GroupMember{RepositoryID: repos[1].ID, Position: 1})
	client := &rawFixtureClient{responses: map[string]int{"first": firstStatus, "second": secondStatus}, body: []byte("from-second-proxy")}
	cache := NewRawCache(NewMemoryOCIObjectStore(), time.Hour, time.Hour, []string{"first.example", "second.example"})
	handler := NewGatewayHandlerWithRawCache(Dependencies{NativeOCIObjectStore: NewMemoryOCIObjectStore()}, store, TestAdapter{}, testAuthenticator(), NewDefaultOCICache(NewMemoryOCIObjectStore(), nil), nil, cache, nil, client)
	return handler, cache, client, repos[0]
}
