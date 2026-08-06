package app

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

func TestRepositoryEgressProxyCreateRedactsAndEncrypts(t *testing.T) {
	t.Setenv("GATEWAY_EGRESS_PROXY_KEY", strings.Repeat("ab", 32))
	store := repository.NewMemoryStore()
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())

	body := `{"name":"maven-proxy","format":"maven","type":"proxy","endpoint":"https://upstream.example","egressProxy":{"mode":"custom","protocol":"socks5","host":"proxy.corp.example","port":1080,"username":"gateway","password":"s3cret","remoteDns":true,"noProxy":["*.internal.example"]}}`
	r := httptest.NewRequest(http.MethodPost, "/api/v2/repositories", strings.NewReader(body))
	r.Header.Set("Idempotency-Key", "create-egress-1")
	authorize(r, "admin-secret")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, r)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create=%d body=%s", rec.Code, rec.Body.String())
	}
	var response repository.HostedRepository
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.EgressProxy == nil || !response.EgressProxy.CredentialsConfigured {
		t.Fatalf("response egressProxy=%+v", response.EgressProxy)
	}
	if response.EgressProxy.Password != "" || strings.Contains(rec.Body.String(), "s3cret") {
		t.Fatalf("response leaks credentials: %s", rec.Body.String())
	}

	persisted, err := store.GetHostedRepository(context.Background(), response.ID)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.EgressProxy == nil || persisted.EgressProxy.Password == "" || persisted.EgressProxy.Password == "s3cret" {
		t.Fatalf("persisted password not encrypted: %+v", persisted.EgressProxy)
	}
	if persisted.EgressProxy.CredentialsConfigured {
		t.Fatalf("response-only marker must not persist: %+v", persisted.EgressProxy)
	}
	if persisted.EgressProxy.Mode != repository.EgressProxyModeCustom || !persisted.EgressProxy.RemoteDNS || persisted.EgressProxy.NoProxy[0] != "*.internal.example" {
		t.Fatalf("persisted config=%+v", persisted.EgressProxy)
	}
}

func TestRepositoryEgressProxyUpdateKeepsAndClearsCredentials(t *testing.T) {
	t.Setenv("GATEWAY_EGRESS_PROXY_KEY", strings.Repeat("ab", 32))
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{
		ID:           uuid.NewString(),
		Name:         "raw-proxy",
		Format:       repository.FormatRaw,
		Type:         repository.RepositoryTypeProxy,
		Endpoint:     "https://upstream.example",
		AllowedHosts: []string{"upstream.example"},
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())
	patch := func(version, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPatch, "/api/v2/repositories/"+repo.ID, strings.NewReader(body))
		authorize(r, "admin-secret")
		r.Header.Set("If-Match", version)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, r)
		return rec
	}

	configured := patch("1", `{"egressProxy":{"mode":"custom","protocol":"http","host":"proxy.corp.example","port":8080,"username":"gateway","password":"s3cret"}}`)
	if configured.Code != http.StatusOK {
		t.Fatalf("configure=%d body=%s", configured.Code, configured.Body.String())
	}
	if strings.Contains(configured.Body.String(), "s3cret") || !strings.Contains(configured.Body.String(), `"credentialsConfigured":true`) {
		t.Fatalf("configure body=%s", configured.Body.String())
	}
	persisted, _ := store.GetHostedRepository(context.Background(), repo.ID)
	ciphertext := persisted.EgressProxy.Password
	if ciphertext == "" || ciphertext == "s3cret" {
		t.Fatalf("stored password=%q", ciphertext)
	}

	kept := patch("2", `{"egressProxy":{"mode":"custom","protocol":"http","host":"proxy.corp.example","port":8443}}`)
	if kept.Code != http.StatusOK {
		t.Fatalf("keep=%d body=%s", kept.Code, kept.Body.String())
	}
	persisted, _ = store.GetHostedRepository(context.Background(), repo.ID)
	if persisted.EgressProxy.Port != 8443 || persisted.EgressProxy.Password != ciphertext {
		t.Fatalf("credentials not preserved across update: %+v", persisted.EgressProxy)
	}

	cleared := patch("3", `{"egressProxy":{"mode":"custom","protocol":"http","host":"proxy.corp.example","port":8443,"clearCredentials":true}}`)
	if cleared.Code != http.StatusOK {
		t.Fatalf("clear=%d body=%s", cleared.Code, cleared.Body.String())
	}
	persisted, _ = store.GetHostedRepository(context.Background(), repo.ID)
	if persisted.EgressProxy.Password != "" {
		t.Fatalf("credentials not cleared: %+v", persisted.EgressProxy)
	}
	if strings.Contains(cleared.Body.String(), `"credentialsConfigured":true`) {
		t.Fatalf("clear body=%s", cleared.Body.String())
	}
}

func TestRepositoryEgressProxyValidation(t *testing.T) {
	t.Setenv("GATEWAY_EGRESS_PROXY_KEY", strings.Repeat("ab", 32))
	store := repository.NewMemoryStore()
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())
	post := func(body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/api/v2/repositories", strings.NewReader(body))
		r.Header.Set("Idempotency-Key", uuid.NewString())
		authorize(r, "admin-secret")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, r)
		return rec
	}
	hosted := post(`{"name":"hosted-egress","format":"raw","egressProxy":{"mode":"direct"}}`)
	if hosted.Code != http.StatusBadRequest {
		t.Fatalf("hosted with egressProxy=%d body=%s", hosted.Code, hosted.Body.String())
	}
	invalid := post(`{"name":"proxy-egress","format":"maven","type":"proxy","endpoint":"https://upstream.example","egressProxy":{"mode":"custom","protocol":"socks4","host":"proxy.example","port":1080}}`)
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("invalid protocol=%d body=%s", invalid.Code, invalid.Body.String())
	}
}

func TestRepositoryEgressProxyCreateRequiresKeyForPassword(t *testing.T) {
	store := repository.NewMemoryStore()
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())
	body := `{"name":"maven-proxy","format":"maven","type":"proxy","endpoint":"https://upstream.example","egressProxy":{"mode":"custom","protocol":"http","host":"proxy.example","port":8080,"password":"s3cret"}}`
	r := httptest.NewRequest(http.MethodPost, "/api/v2/repositories", strings.NewReader(body))
	r.Header.Set("Idempotency-Key", uuid.NewString())
	authorize(r, "admin-secret")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, r)
	if rec.Code != http.StatusInternalServerError || !strings.Contains(rec.Body.String(), "encryption key") {
		t.Fatalf("create=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestEgressProxyTestEndpoint(t *testing.T) {
	store := repository.NewMemoryStore()
	hosted, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "raw-hosted", Format: repository.FormatRaw})
	if err != nil {
		t.Fatal(err)
	}
	proxy, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{
		ID: uuid.NewString(), Name: "raw-proxy", Format: repository.FormatRaw, Type: repository.RepositoryTypeProxy,
		Endpoint: "https://upstream.example", AllowedHosts: []string{"upstream.example"},
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())
	testCall := func(id string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/api/v2/repositories/"+id+"/egress-proxy:test", nil)
		authorize(r, "admin-secret")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, r)
		return rec
	}

	if rec := testCall(hosted.ID); rec.Code != http.StatusBadRequest {
		t.Fatalf("hosted test=%d body=%s", rec.Code, rec.Body.String())
	}
	if rec := testCall(uuid.NewString()); rec.Code != http.StatusNotFound {
		t.Fatalf("missing test=%d body=%s", rec.Code, rec.Body.String())
	}

	previousLookup, previousDial, previousProxy := rawProxyLookupIP, rawProxyDialContext, rawProxyFromEnvironment
	rawProxyLookupIP = func(context.Context, string, string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("203.0.113.7")}, nil
	}
	rawProxyDialContext = func(context.Context, string, string) (net.Conn, error) { return nil, errors.New("connection refused") }
	rawProxyFromEnvironment = func(*http.Request) (*url.URL, error) { return nil, nil }
	t.Cleanup(func() {
		rawProxyLookupIP, rawProxyDialContext, rawProxyFromEnvironment = previousLookup, previousDial, previousProxy
	})

	rec := testCall(proxy.ID)
	if rec.Code != http.StatusOK {
		t.Fatalf("proxy test=%d body=%s", rec.Code, rec.Body.String())
	}
	var result egressProxyTestResult
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Reachable || result.Error == "" || result.EgressMode != string(repository.EgressProxyModeEnvironment) {
		t.Fatalf("result=%+v", result)
	}
	if strings.Contains(rec.Body.String(), "password") || strings.Contains(rec.Body.String(), "://proxy") {
		t.Fatalf("result leaks details: %s", rec.Body.String())
	}
}
