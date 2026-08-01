package app

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

func TestConanProxyTLSE2EPinsVerifiedAddressAndUsesPersistentCache(t *testing.T) {
	var calls, lookups, dials atomic.Int32
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/conans/pkg/1.0/user/stable/revisions" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"revisions":[{"revision":"rrev","time":1}]}`))
	}))
	defer upstream.Close()
	host, port := rawTLSServerAddress(t, upstream.URL)
	withRawProxyNetwork(t, func(_ context.Context, network, name string) ([]net.IP, error) {
		if lookups.Add(1) > 1 {
			return []net.IP{net.ParseIP("127.0.0.1")}, nil
		}
		if network != "ip" || name != "example.com" {
			t.Fatalf("lookup=%s %s", network, name)
		}
		return []net.IP{net.ParseIP("203.0.113.11")}, nil
	}, func(ctx context.Context, network, address string) (net.Conn, error) {
		dials.Add(1)
		if address != net.JoinHostPort("203.0.113.11", port) {
			t.Fatalf("unverified dial=%q", address)
		}
		return (&net.Dialer{}).DialContext(ctx, network, net.JoinHostPort(host, port))
	})
	store := repository.NewMemoryStore()
	endpoint := "https://example.com:" + port
	member := repository.Member{Name: "proxy", Type: repository.MemberProxy, Endpoint: endpoint, AllowedHosts: []string{"example.com"}}
	_, _ = store.CreateConanGroup(context.Background(), repository.Group{Name: "central", Members: []repository.Member{member}})
	objects := NewMemoryOCIObjectStore()
	first := ConanHandler{Store: store, Authenticator: testAuthenticator(), Client: UpstreamClient{HTTPClient: upstream.Client()}, Cache: NewDefaultConanCache(objects, []string{"example.com"})}
	second := ConanHandler{Store: store, Authenticator: testAuthenticator(), Client: UpstreamClient{HTTPClient: upstream.Client()}, Cache: NewDefaultConanCache(objects, []string{"example.com"})}
	path := "/conan/v2/central/conans/pkg/1.0/user/stable/revisions"
	for _, handler := range []ConanHandler{first, second} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, conanRequest(path))
		if w.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
	}
	if calls.Load() != 1 || lookups.Load() != 1 || dials.Load() != 1 {
		t.Fatalf("calls=%d lookups=%d dials=%d", calls.Load(), lookups.Load(), dials.Load())
	}
}

func TestConanProxyRedirectIsNotFollowed(t *testing.T) {
	var redirected atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { redirected.Add(1) }))
	defer target.Close()
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, http.StatusFound) }))
	defer upstream.Close()
	host, port := rawTLSServerAddress(t, upstream.URL)
	withRawProxyNetwork(t, func(context.Context, string, string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("203.0.113.12")}, nil
	}, func(ctx context.Context, network, address string) (net.Conn, error) {
		if address != net.JoinHostPort("203.0.113.12", port) {
			t.Fatalf("dial=%q", address)
		}
		return (&net.Dialer{}).DialContext(ctx, network, net.JoinHostPort(host, port))
	})
	response, err := (UpstreamClient{HTTPClient: upstream.Client()}).FetchConan(context.Background(), http.MethodGet, repository.Member{Type: repository.MemberProxy, Endpoint: "https://example.com:" + port}, "pkg/1.0/user/stable/revisions", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusFound || redirected.Load() != 0 {
		t.Fatalf("status=%d redirected=%d", response.StatusCode, redirected.Load())
	}
}

func TestConanProxyRejectsTLSOverrideWithoutDialing(t *testing.T) {
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
			_, err := (UpstreamClient{HTTPClient: client}).FetchConan(context.Background(), http.MethodGet, repository.Member{Type: repository.MemberProxy, Endpoint: "https://example.com", AllowedHosts: []string{"example.com"}}, "pkg/1.0/user/stable/revisions", nil)
			if err == nil || !strings.Contains(err.Error(), "must not override TLS dialing") || called {
				t.Fatalf("err=%v TLS dial called=%t", err, called)
			}
		})
	}
}

func TestConanProxyAllowlistIsolatedPerMember(t *testing.T) {
	store := repository.NewMemoryStore()
	endpoint := "https://shared.example"
	allowed := repository.Member{Name: "allowed", Type: repository.MemberProxy, Endpoint: endpoint, AllowedHosts: []string{"shared.example"}}
	denied := repository.Member{Name: "denied", Type: repository.MemberProxy, Endpoint: endpoint, AllowedHosts: []string{"other.example"}}
	_, _ = store.CreateConanGroup(context.Background(), repository.Group{Name: "allowed-group", Members: []repository.Member{allowed}})
	_, _ = store.CreateConanGroup(context.Background(), repository.Group{Name: "denied-group", Members: []repository.Member{denied}})
	client := &conanAnonymousClient{}
	for group, want := range map[string]int{"allowed-group": http.StatusOK, "denied-group": http.StatusForbidden} {
		w := httptest.NewRecorder()
		ConanHandler{Store: store, Authenticator: testAuthenticator(), Client: client, Cache: NewConanCache([]string{"shared.example"})}.ServeHTTP(w, conanRequest("/conan/v2/"+group+"/conans/pkg/1.0/user/stable/revisions"))
		if w.Code != want {
			t.Fatalf("%s status=%d", group, w.Code)
		}
	}
}
