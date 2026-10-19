//go:build integration

package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ccsert/artifact-gateway/internal/egress"
	source "github.com/ccsert/artifact-gateway/internal/nexussource"
	sourcepg "github.com/ccsert/artifact-gateway/internal/nexussource/pgstore"
	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/ccsert/artifact-gateway/internal/secrets"
	"github.com/ccsert/artifact-gateway/internal/testsupport"
	_ "github.com/jackc/pgx/v5/stdlib"
)

func TestPostgresMigrationSourceCredentialHTTPLifecycle(t *testing.T) {
	connection := os.Getenv("TEST_DATABASE_URL")
	if connection == "" {
		t.Skip("TEST_DATABASE_URL is required")
	}
	if err := testsupport.ValidateIsolatedDatabaseURL(connection); err != nil {
		t.Fatal(err)
	}
	t.Setenv(secrets.KeyEnv, "0123456789abcdef0123456789abcdef")
	ctx := context.Background()
	db, err := sql.Open("pgx", connection)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	store, err := repository.NewPostgresStore(connection)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	handler := newTestGateway(t, GatewayOptions{Dependencies: Dependencies{SourceService: source.New(sourcepg.New(db), store, source.Options{})}, Store: store, Adapter: TestAdapter{}, Authenticator: testAuthenticator()})
	call := func(method, path, body, match string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		authorize(r, "admin-secret")
		if match != "" {
			r.Header.Set("If-Match", match)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if strings.Contains(w.Body.String(), "synthetic-user-secret") || strings.Contains(w.Body.String(), "synthetic-password-secret") || strings.Contains(w.Body.String(), "credentialCiphertext") {
			t.Fatalf("secret echoed: %s", w.Body.String())
		}
		return w
	}
	payload := `{"name":"synthetic-connection","baseUrl":"https://nexus.example.test/context","credentials":{"username":"synthetic-user-secret","password":"synthetic-password-secret"}}`
	created := call("POST", "/api/v2/migration-sources", payload, "")
	if created.Code != 201 {
		t.Fatalf("create=%d %s", created.Code, created.Body.String())
	}
	var sourceState struct {
		ID, Version           string
		CredentialsConfigured bool
	}
	if json.Unmarshal(created.Body.Bytes(), &sourceState) != nil || sourceState.ID == "" || sourceState.Version != "1" || !sourceState.CredentialsConfigured {
		t.Fatalf("create response=%s", created.Body.String())
	}
	path := "/api/v2/migration-sources/" + sourceState.ID
	t.Cleanup(func() { _, _ = db.ExecContext(ctx, `DELETE FROM nexus_migration_sources WHERE id=$1`, sourceState.ID) })
	var ciphertext string
	if err := db.QueryRowContext(ctx, `SELECT credential_ciphertext FROM nexus_migration_sources WHERE id=$1`, sourceState.ID).Scan(&ciphertext); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(ciphertext, "synthetic") || ciphertext == "" {
		t.Fatal("PostgreSQL stored plaintext credentials")
	}
	plain, err := secrets.Open("nexus-source:"+sourceState.ID, ciphertext)
	if err != nil || !strings.Contains(plain, "synthetic-password-secret") {
		t.Fatal("credential cannot be reopened with its configured key")
	}
	// Restart the independent source persistence adapter and verify durable settings.
	restarted, err := sql.Open("pgx", connection)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = restarted.Close() }()
	handler = newTestGateway(t, GatewayOptions{Dependencies: Dependencies{SourceService: source.New(sourcepg.New(restarted), store, source.Options{})}, Store: store, Adapter: TestAdapter{}, Authenticator: testAuthenticator()})
	fetched := call("GET", path, "", "")
	if fetched.Code != 200 || fetched.Header().Get("ETag") != "1" {
		t.Fatalf("restart read=%d %s", fetched.Code, fetched.Body.String())
	}
	updated := call("PUT", path, `{"name":"renamed","baseUrl":"https://nexus.example.test/context"}`, "1")
	if updated.Code != 200 || updated.Header().Get("ETag") != "2" {
		t.Fatalf("update=%d %s", updated.Code, updated.Body.String())
	}
	stale := call("PUT", path, payload, "1")
	if stale.Code != 412 {
		t.Fatalf("stale update=%d", stale.Code)
	}
	rebound := call("PUT", path, `{"name":"renamed","baseUrl":"https://other.example.test"}`, "2")
	if rebound.Code != 400 {
		t.Fatalf("credential rebinding accepted: %d", rebound.Code)
	}
	cleared := call("PUT", path, `{"name":"anonymous","baseUrl":"https://nexus.example.test/context","clearCredentials":true}`, "2")
	if cleared.Code != 200 || !strings.Contains(cleared.Body.String(), `"credentialsConfigured":false`) {
		t.Fatalf("clear=%d %s", cleared.Code, cleared.Body.String())
	}
	replaced := call("PUT", path, payload, "3")
	if replaced.Code != 200 || replaced.Header().Get("ETag") != "4" {
		t.Fatalf("replace=%d %s", replaced.Code, replaced.Body.String())
	}
	if w := call("DELETE", path, "", "3"); w.Code != 412 {
		t.Fatalf("stale delete=%d", w.Code)
	}
	if w := call("DELETE", path, "", "4"); w.Code != 204 {
		t.Fatalf("delete=%d %s", w.Code, w.Body.String())
	}
	if w := call("GET", path, "", ""); w.Code != 404 {
		t.Fatalf("deleted read=%d", w.Code)
	}
	var rows int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM nexus_migration_sources WHERE id=$1`, sourceState.ID).Scan(&rows); err != nil || rows != 0 {
		t.Fatalf("credential row remained=%d err=%v", rows, err)
	}
	audits, err := store.ListAudits(ctx, repository.AuditQuery{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, audit := range audits {
		if audit.Resource != sourceState.ID {
			continue
		}
		encoded, _ := json.Marshal(audit)
		if strings.Contains(string(encoded), "synthetic-user-secret") || strings.Contains(string(encoded), "synthetic-password-secret") || strings.Contains(string(encoded), ciphertext) {
			t.Fatal("audit leaked credentials")
		}
		count++
	}
	if count < 9 {
		t.Fatalf("source lifecycle audit records=%d", count)
	}
}

func TestPostgresMigrationSourceReadOnlyDiscoveryAndMappingHTTP(t *testing.T) {
	connection := os.Getenv("TEST_DATABASE_URL")
	if connection == "" {
		t.Skip("TEST_DATABASE_URL is required")
	}
	if err := testsupport.ValidateIsolatedDatabaseURL(connection); err != nil {
		t.Fatal(err)
	}
	t.Setenv(secrets.KeyEnv, "0123456789abcdef0123456789abcdef")
	var calls atomic.Int64
	fixture := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != "GET" {
			t.Errorf("source write %s", r.Method)
			w.WriteHeader(500)
			return
		}
		username, password, ok := r.BasicAuth()
		if !ok || username != "read-only-fixture" || password != "fixture-read-password" {
			t.Error("synthetic source credentials missing")
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/service/rest/v1/repositories":
			_, _ = w.Write([]byte(`[{"name":"raw-source","format":"raw","type":"hosted"}]`))
		case "/service/rest/v1/repositories/raw/hosted/raw-source":
			_, _ = w.Write([]byte(`{"name":"raw-source","online":true,"storage":{"blobStoreName":"source-only","writePolicy":"ALLOW"}}`))
		case "/service/rest/v1/assets":
			_, _ = w.Write([]byte(`{"items":[{"repository":"raw-source","format":"raw","path":"a+build.txt","fileSize":3,"downloadUrl":"https://example.com/repository/raw-source/a%2Bbuild.txt"}],"continuationToken":null}`))
		default:
			t.Errorf("source operation outside read-only allowlist %s", r.URL.Path)
			w.WriteHeader(500)
		}
	}))
	defer fixture.Close()
	hooks := egress.DefaultHooks()
	hooks.ProxyFromEnvironment = func(*http.Request) (*url.URL, error) { return nil, nil }
	hooks.LookupIP = func(context.Context, string, string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("10.20.30.7")}, nil
	}
	hooks.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, fixture.Listener.Addr().String())
	}
	db, err := sql.Open("pgx", connection)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	store, err := repository.NewPostgresStore(connection)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	service := source.New(sourcepg.New(db), store, source.Options{Client: fixture.Client(), EgressHooks: &hooks, PrivateEndpoints: map[string][]netip.Prefix{"example.com": {netip.MustParsePrefix("10.20.30.0/24")}}})
	handler := newTestGateway(t, GatewayOptions{Dependencies: Dependencies{SourceService: service}, Store: store, Adapter: TestAdapter{}, Authenticator: testAuthenticator()})
	call := func(method, path, payload string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(payload))
		authorize(r, "admin-secret")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if strings.Contains(w.Body.String(), "read-only-fixture") || strings.Contains(w.Body.String(), "fixture-read-password") {
			t.Fatal("source credentials leaked")
		}
		return w
	}
	before, _, err := store.ListHostedRepositories(context.Background(), 100, "")
	if err != nil {
		t.Fatal(err)
	}
	created := call("POST", "/api/v2/migration-sources", `{"name":"source-fixture","baseUrl":"https://example.com","credentials":{"username":"read-only-fixture","password":"fixture-read-password"}}`)
	var state struct{ ID string }
	if created.Code != 201 || json.Unmarshal(created.Body.Bytes(), &state) != nil || state.ID == "" {
		t.Fatalf("create=%d %s", created.Code, created.Body.String())
	}
	defer func() { _ = service.Delete(context.Background(), state.ID, "1") }()
	root := "/api/v2/migration-sources/" + state.ID
	preflight := call("POST", root+"/preflight", "")
	if preflight.Code != 200 || !strings.Contains(preflight.Body.String(), `"visibility":"credential_visible"`) {
		t.Fatalf("preflight=%d %s", preflight.Code, preflight.Body.String())
	}
	discovery := call("POST", root+"/discovery", `{"repositories":["raw-source"],"maxPages":2,"maxAssets":5}`)
	if discovery.Code != 200 || !strings.Contains(discovery.Body.String(), `"downloadUrl":"https://example.com/repository/raw-source/a+build.txt"`) || !strings.Contains(discovery.Body.String(), `"unsupportedAssets":[]`) {
		t.Fatalf("discovery=%d %s", discovery.Code, discovery.Body.String())
	}
	preview := call("POST", root+"/mapping-preview", `{"mappings":[{"sourceRepository":"raw-source","targetName":"source-mapping-preview-only","format":"raw","type":"hosted"}]}`)
	if preview.Code != 200 || !strings.Contains(preview.Body.String(), `"valid":true`) || !strings.Contains(preview.Body.String(), `"status":"ready"`) {
		t.Fatalf("preview=%d %s", preview.Code, preview.Body.String())
	}
	after, _, err := store.ListHostedRepositories(context.Background(), 100, "")
	if err != nil || len(after) != len(before) {
		t.Fatalf("read-only preview changed target repositories before=%d after=%d err=%v", len(before), len(after), err)
	}
	if calls.Load() != 7 {
		t.Fatalf("read-only source request count=%d", calls.Load())
	}
	callsBefore := calls.Load()
	invalid := call("POST", root+"/discovery", `{"repositories":["raw-source"],"maxPages":0}`)
	if invalid.Code != 400 || calls.Load() != callsBefore {
		t.Fatalf("invalid discovery budget contacted source: %d", invalid.Code)
	}
}
