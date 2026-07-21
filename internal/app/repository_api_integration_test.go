//go:build integration

package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

func TestPostgresHTTPIntegration(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is required for PostgreSQL integration tests")
	}
	store, err := repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())

	group := `{"name":"engineering","members":[{"name":"proxy","type":"proxy","endpoint":"test://available","position":1},{"name":"hosted","type":"hosted","endpoint":"test://unavailable","position":0}]}`
	created := integrationRequest(handler, http.MethodPost, "/api/v1/oci/groups", group, "admin-secret")
	if created.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", created.Code, created.Body.String())
	}
	got := integrationRequest(handler, http.MethodGet, "/api/v1/oci/groups/engineering", "", "admin-secret")
	if got.Code != http.StatusOK {
		t.Fatalf("get = %d %s", got.Code, got.Body.String())
	}
	var stored repository.Group
	if err := json.NewDecoder(got.Body).Decode(&stored); err != nil {
		t.Fatal(err)
	}
	if len(stored.Members) != 2 || stored.Members[0].Name != "hosted" || stored.Members[1].Type != repository.MemberProxy {
		t.Fatalf("stored group = %#v", stored)
	}
	resolved := integrationRequest(handler, http.MethodGet, "/api/v1/oci/groups/engineering/resolve?repository=team/app", "", "resolver-secret")
	if resolved.Code != http.StatusOK || !strings.Contains(resolved.Body.String(), `"name":"proxy"`) {
		t.Fatalf("resolve = %d %s", resolved.Code, resolved.Body.String())
	}

	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var actor, outcome, member string
	if err := db.QueryRowContext(context.Background(), `SELECT actor, outcome, member_name FROM resolver_audit_log WHERE group_name=$1`, "engineering").Scan(&actor, &outcome, &member); err != nil {
		t.Fatal(err)
	}
	if actor != "build-agent" || outcome != string(repository.AuditResolved) || member != "proxy" {
		t.Fatalf("audit = actor=%q outcome=%q member=%q", actor, outcome, member)
	}
	disabled := integrationRequest(handler, http.MethodPost, "/api/v1/oci/groups/engineering/disable", "", "admin-secret")
	if disabled.Code != http.StatusNoContent {
		t.Fatalf("disable = %d %s", disabled.Code, disabled.Body.String())
	}
	blocked := integrationRequest(handler, http.MethodGet, "/api/v1/oci/groups/engineering/resolve?repository=team/app", "", "resolver-secret")
	if blocked.Code != http.StatusConflict || !strings.Contains(blocked.Body.String(), `group_disabled`) {
		t.Fatalf("disabled resolve = %d %s", blocked.Code, blocked.Body.String())
	}

	mavenUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("artifact")) }))
	defer mavenUpstream.Close()
	mavenGroup := fmt.Sprintf(`{"name":"maven-engineering","members":[{"name":"hosted","type":"hosted","endpoint":"%s","position":0},{"name":"proxy","type":"proxy","endpoint":"%s","position":1}]}`, mavenUpstream.URL, mavenUpstream.URL)
	createdMaven := integrationRequest(handler, http.MethodPost, "/api/v1/maven/groups", mavenGroup, "admin-secret")
	if createdMaven.Code != http.StatusCreated {
		t.Fatalf("create Maven group = %d %s", createdMaven.Code, createdMaven.Body.String())
	}
	mavenRequest := httptest.NewRequest(http.MethodGet, "/maven/maven-engineering/com/example/library/1.0/library-1.0.pom", nil)
	mavenRequest.SetBasicAuth("integration", "resolver-secret")
	mavenResponse := httptest.NewRecorder()
	handler.ServeHTTP(mavenResponse, mavenRequest)
	if mavenResponse.Code != http.StatusOK || mavenResponse.Header().Get("X-Artifact-Gateway-Conflict") != "internal-preferred" || mavenResponse.Body.String() != "artifact" {
		t.Fatalf("Maven response = %d headers=%v body=%q", mavenResponse.Code, mavenResponse.Header(), mavenResponse.Body.String())
	}
	var conflictCount, resolvedCount int
	if err := db.QueryRowContext(context.Background(), `SELECT count(*) FROM resolver_audit_log WHERE group_name=$1 AND outcome=$2`, "maven-engineering", repository.AuditInternalPreferred).Scan(&conflictCount); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(context.Background(), `SELECT count(*) FROM resolver_audit_log WHERE group_name=$1 AND outcome=$2`, "maven-engineering", repository.AuditResolved).Scan(&resolvedCount); err != nil {
		t.Fatal(err)
	}
	if conflictCount != 1 || resolvedCount != 1 {
		t.Fatalf("Maven audit counts = conflict:%d resolved:%d", conflictCount, resolvedCount)
	}
}

func TestPostgresRawAuditFieldsIntegration(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is required for PostgreSQL integration tests")
	}
	store, err := repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	if _, err := store.CreateGroup(context.Background(), repository.Group{Name: "raw-audit", Members: []repository.Member{{Name: "hosted", Type: repository.MemberHosted, Endpoint: "https://gitea.example:8443"}}}); err != nil {
		t.Fatal(err)
	}
	handler := RawHandler{
		Store:         store,
		Authenticator: testAuthenticator(),
		Client:        &rawFixtureClient{responses: map[string]int{"hosted": http.StatusOK}, body: []byte("artifact")},
		Cache:         NewRawCache(NewMemoryOCIObjectStore(), time.Hour, time.Hour, nil),
	}
	response := integrationRequest(handler, http.MethodGet, "/raw/raw-audit/release/app.txt", "", "resolver-secret")
	if response.Code != http.StatusOK {
		t.Fatalf("Raw response = %d %s", response.Code, response.Body.String())
	}

	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var format, resource, representation, memberType, upstreamHost, operation, disposition string
	var status int
	var bytes int64
	err = db.QueryRowContext(context.Background(), `SELECT format, resource, representation, member_type, upstream_host, operation, http_status, cache_disposition, bytes
		FROM resolver_audit_log WHERE group_name=$1 ORDER BY id DESC LIMIT 1`, "raw-audit").Scan(&format, &resource, &representation, &memberType, &upstreamHost, &operation, &status, &disposition, &bytes)
	if err != nil {
		t.Fatal(err)
	}
	if format != "raw" || resource != "release/app.txt" || representation != "body" || memberType != "hosted" || upstreamHost != "gitea.example" || operation != "get" || status != http.StatusOK || disposition != "miss" || bytes != 8 {
		t.Fatalf("Raw audit fields = format=%q resource=%q representation=%q member_type=%q upstream_host=%q operation=%q status=%d disposition=%q bytes=%d", format, resource, representation, memberType, upstreamHost, operation, status, disposition, bytes)
	}
	audits, err := store.ListAudits(context.Background(), repository.AuditQuery{GroupName: "raw-audit"})
	if err != nil || len(audits) != 1 {
		t.Fatalf("ListAudits err=%v audits=%#v", err, audits)
	}
	if audit := audits[0]; audit.Format != "raw" || audit.Resource != "release/app.txt" || audit.Representation != "body" || audit.MemberType != "hosted" || audit.UpstreamHost != "gitea.example" || audit.Operation != "get" || audit.Status != http.StatusOK || audit.CacheDisposition != "miss" || audit.Bytes != 8 {
		t.Fatalf("ListAudits Raw fields=%#v", audit)
	}
}

func TestPostgresLegacyAuditsRemainQueryable(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is required for PostgreSQL integration tests")
	}
	store, err := repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.ExecContext(context.Background(), `INSERT INTO resolver_audit_log (group_name, repository, member_name, outcome, actor) VALUES ($1,$2,$3,$4,$5)`, "legacy-audit", "legacy-repository", "legacy-member", repository.AuditResolved, "legacy-actor"); err != nil {
		t.Fatal(err)
	}
	audits, err := store.ListAudits(context.Background(), repository.AuditQuery{GroupName: "legacy-audit"})
	if err != nil || len(audits) != 1 {
		t.Fatalf("ListAudits err=%v audits=%#v", err, audits)
	}
	if audit := audits[0]; audit.Actor != "legacy-actor" || audit.Outcome != repository.AuditResolved || audit.Format != "" || audit.Resource != "" || audit.Representation != "" || audit.MemberType != "" || audit.UpstreamHost != "" || audit.Operation != "" || audit.Status != 0 || audit.CacheDisposition != "" || audit.Bytes != 0 {
		t.Fatalf("legacy audit=%#v", audit)
	}
}

func integrationRequest(handler http.Handler, method, target, body, token string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	authorize(request, token)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
