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
	hostedRequest := httptest.NewRequest(http.MethodPost, "/api/v2/repositories", strings.NewReader(`{"name":"releases","format":"maven"}`))
	authorize(hostedRequest, "admin-secret")
	hostedRequest.Header.Set("Idempotency-Key", "integration-releases")
	hostedCreated := httptest.NewRecorder()
	handler.ServeHTTP(hostedCreated, hostedRequest)
	if hostedCreated.Code != http.StatusCreated {
		t.Fatalf("create Hosted repository = %d %s", hostedCreated.Code, hostedCreated.Body.String())
	}
	var hosted repository.HostedRepository
	if err := json.NewDecoder(hostedCreated.Body).Decode(&hosted); err != nil {
		t.Fatal(err)
	}
	if hosted.State != repository.RepositoryActive || hosted.Version != "1" {
		t.Fatalf("created Hosted repository = %#v", hosted)
	}
	replayRequest := httptest.NewRequest(http.MethodPost, "/api/v2/repositories", strings.NewReader(`{"name":"releases","format":"maven"}`))
	authorize(replayRequest, "admin-secret")
	replayRequest.Header.Set("Idempotency-Key", "integration-releases")
	replayed := httptest.NewRecorder()
	handler.ServeHTTP(replayed, replayRequest)
	var replay repository.HostedRepository
	if replayed.Code != http.StatusCreated || json.NewDecoder(replayed.Body).Decode(&replay) != nil || replay.ID != hosted.ID {
		t.Fatalf("replay Hosted repository = %d %s", replayed.Code, replayed.Body.String())
	}
	hostedDisabled := integrationRequest(handler, http.MethodDelete, "/api/v2/repositories/"+hosted.ID, "", "admin-secret")
	if hostedDisabled.Code != http.StatusAccepted {
		t.Fatalf("disable Hosted repository = %d %s", hostedDisabled.Code, hostedDisabled.Body.String())
	}

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
	var hostedState string
	if err := db.QueryRowContext(context.Background(), `SELECT state FROM hosted_repositories WHERE id=$1`, hosted.ID).Scan(&hostedState); err != nil {
		t.Fatal(err)
	}
	if hostedState != string(repository.RepositoryDeleting) {
		t.Fatalf("Hosted repository state = %q", hostedState)
	}
	nativeRead := httptest.NewRequest(http.MethodGet, "/maven/releases/com/example/library/1.0/library-1.0.pom", nil)
	authorize(nativeRead, "resolver-secret")
	nativeReadResponse := httptest.NewRecorder()
	handler.ServeHTTP(nativeReadResponse, nativeRead)
	if nativeReadResponse.Code != http.StatusForbidden {
		t.Fatalf("disabled Native Hosted read = %d %s", nativeReadResponse.Code, nativeReadResponse.Body.String())
	}
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
	if _, err := store.CreateRawGroup(context.Background(), repository.Group{Name: "raw-audit", CacheQuotaBytes: 1 << 30, Members: []repository.Member{{Name: "hosted", Type: repository.MemberHosted, Endpoint: "https://gitea.example:8443"}}}); err != nil {
		t.Fatal(err)
	}
	handler := RawHandler{
		Store:         store,
		Authenticator: testAuthenticator(),
		Client:        &rawFixtureClient{responses: map[string]int{"hosted": http.StatusOK}, body: []byte("artifact")},
		Cache:         NewRawCache(NewMemoryOCIObjectStore(), time.Hour, time.Hour, nil),
	}
	request := httptest.NewRequest(http.MethodGet, "/raw/raw-audit/release/app.txt", nil)
	authorize(request, "resolver-secret")
	request.Header.Set("X-Request-ID", "postgres-raw-request")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("Raw response = %d %s", response.Code, response.Body.String())
	}

	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var format, resource, representation, memberType, upstreamHost, operation, disposition, requestID, traceID string
	var status int
	var bytes int64
	err = db.QueryRowContext(context.Background(), `SELECT format, resource, representation, member_type, upstream_host, operation, http_status, cache_disposition, bytes, request_id, trace_id
		FROM resolver_audit_log WHERE group_name=$1 ORDER BY id DESC LIMIT 1`, "raw-audit").Scan(&format, &resource, &representation, &memberType, &upstreamHost, &operation, &status, &disposition, &bytes, &requestID, &traceID)
	if err != nil {
		t.Fatal(err)
	}
	if format != "raw" || resource != "release/app.txt" || representation != "body" || memberType != "hosted" || upstreamHost != "gitea.example" || operation != "get" || status != http.StatusOK || disposition != "miss" || bytes != 8 || requestID != "postgres-raw-request" || len(traceID) != 32 {
		t.Fatalf("Raw audit fields = format=%q resource=%q representation=%q member_type=%q upstream_host=%q operation=%q status=%d disposition=%q bytes=%d request_id=%q trace_id=%q", format, resource, representation, memberType, upstreamHost, operation, status, disposition, bytes, requestID, traceID)
	}
	audits, err := store.ListAudits(context.Background(), repository.AuditQuery{GroupName: "raw-audit"})
	if err != nil || len(audits) != 1 {
		t.Fatalf("ListAudits err=%v audits=%#v", err, audits)
	}
	if audit := audits[0]; audit.Format != "raw" || audit.Resource != "release/app.txt" || audit.Representation != "body" || audit.MemberType != "hosted" || audit.UpstreamHost != "gitea.example" || audit.Operation != "get" || audit.Status != http.StatusOK || audit.CacheDisposition != "miss" || audit.Bytes != 8 || audit.RequestID != "postgres-raw-request" || len(audit.TraceID) != 32 {
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

func TestPostgresAnonymousMigrationPreservesLegacyOCIAndMavenRows(t *testing.T) {
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
	for _, statement := range []string{
		`DELETE FROM resolver_audit_log WHERE group_name IN ('legacy-policy-oci', 'legacy-policy-maven')`,
		`DELETE FROM oci_groups WHERE name='legacy-policy-oci'`,
		`DELETE FROM maven_groups WHERE name='legacy-policy-maven'`,
	} {
		if _, err := db.ExecContext(context.Background(), statement); err != nil {
			t.Fatal(err)
		}
	}

	// These inserts and queries deliberately use the pre-policy column lists.
	// They model a populated database and the previous application binary.
	for _, statement := range []string{
		`INSERT INTO oci_groups (name, enabled) VALUES ('legacy-policy-oci', true)`,
		`INSERT INTO oci_group_members (group_name, name, member_type, endpoint, position) VALUES ('legacy-policy-oci', 'hosted', 'hosted', 'https://oci.example', 0)`,
		`INSERT INTO maven_groups (name, enabled) VALUES ('legacy-policy-maven', true)`,
		`INSERT INTO maven_group_members (group_name, name, member_type, endpoint, position) VALUES ('legacy-policy-maven', 'hosted', 'hosted', 'https://maven.example', 0)`,
		`INSERT INTO resolver_audit_log (group_name, repository, member_name, outcome, actor) VALUES ('legacy-policy-oci', 'team/app', 'hosted', 'resolved', 'legacy-reader')`,
	} {
		if _, err := db.ExecContext(context.Background(), statement); err != nil {
			t.Fatal(err)
		}
	}
	for _, query := range []string{
		`SELECT name, enabled, created_at FROM oci_groups WHERE name='legacy-policy-oci'`,
		`SELECT name, enabled, created_at FROM maven_groups WHERE name='legacy-policy-maven'`,
		`SELECT group_name, repository, member_name, outcome, actor, occurred_at FROM resolver_audit_log WHERE actor='legacy-reader'`,
	} {
		if err := db.QueryRowContext(context.Background(), query).Err(); err != nil {
			t.Fatalf("legacy query failed: %s: %v", query, err)
		}
	}
	oci, err := store.GetGroup(context.Background(), "legacy-policy-oci")
	if err != nil || oci.Anonymous || len(oci.Members) != 1 || oci.Members[0].Anonymous {
		t.Fatalf("OCI compatibility group=%#v err=%v", oci, err)
	}
	maven, err := store.GetMavenGroup(context.Background(), "legacy-policy-maven")
	if err != nil || maven.Anonymous || len(maven.Members) != 1 || maven.Members[0].Anonymous {
		t.Fatalf("Maven compatibility group=%#v err=%v", maven, err)
	}
	audits, err := store.ListAudits(context.Background(), repository.AuditQuery{GroupName: "legacy-policy-oci"})
	if err != nil || len(audits) != 1 || audits[0].Actor != "legacy-reader" || audits[0].Outcome != repository.AuditResolved {
		t.Fatalf("legacy audit compatibility audits=%#v err=%v", audits, err)
	}
}

func integrationRequest(handler http.Handler, method, target, body, token string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	authorize(request, token)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
