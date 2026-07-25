//go:build integration

package app

import (
	"context"
	"database/sql"
	"errors"
	rawmaintenance "github.com/artifact-gateway/artifact-gateway/internal/maintenance/raw"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

func TestPostgresNativeRawStateTransitions(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is required for PostgreSQL integration tests")
	}
	store, err := repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "raw-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:20], Format: repository.FormatRaw})
	if err != nil {
		t.Fatal(err)
	}
	digest := "sha256:" + strings.Repeat("c", 64)
	objectKey := "native/raw/sha256/" + strings.Repeat("c", 64)
	if err := store.StageRawObject(context.Background(), repository.RawObject{Digest: digest, ObjectKey: objectKey, Size: 12}); err != nil {
		t.Fatalf("stage raw object: %v", err)
	}
	asset, err := store.PutRawAsset(context.Background(), repository.RawAsset{RepositoryID: repo.ID, Path: "releases/app.txt", Digest: digest, ObjectKey: objectKey, Size: 12, ContentType: "text/plain"})
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := store.GetRawAsset(context.Background(), repo.ID, asset.Path)
	if err != nil || loaded.Digest != digest || loaded.ContentType != "text/plain" {
		t.Fatalf("load=%#v err=%v", loaded, err)
	}
	if err = store.DeleteRawAsset(context.Background(), repo.ID, asset.Path); err != nil {
		t.Fatal(err)
	}
	if _, err = store.GetRawAsset(context.Background(), repo.ID, asset.Path); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("deleted asset lookup=%v", err)
	}
	objects, err := store.ListUnreferencedRawObjects(context.Background(), time.Now().Add(time.Hour), 10)
	if err != nil || len(objects) != 1 || objects[0].Digest != digest {
		t.Fatalf("unreferenced raw objects=%#v err=%v", objects, err)
	}
	if referenced, err := store.RawObjectIsUnreferenced(context.Background(), digest); err != nil || !referenced {
		t.Fatalf("raw object unreferenced=%t err=%v", referenced, err)
	}
	objectsStore := NewMemoryOCIObjectStore()
	if err = objectsStore.Put(context.Background(), objectKey, []byte("raw payload")); err != nil {
		t.Fatal(err)
	}
	collector := rawmaintenance.Collector{Store: store, Objects: objectsStore, Now: func() time.Time { return time.Now().Add(25 * time.Hour) }}
	if err = collector.Collect(context.Background()); err != nil {
		t.Fatalf("collect raw object: %v", err)
	}
	if _, err = objectsStore.Get(context.Background(), objectKey); err == nil {
		t.Fatal("raw object bytes remain after lifecycle reclaim")
	}
	objects, err = store.ListUnreferencedRawObjects(context.Background(), time.Now().Add(time.Hour), 10)
	if err != nil || len(objects) != 0 {
		t.Fatalf("collected raw objects=%#v err=%v", objects, err)
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
	if _, err := store.CreateRawGroup(context.Background(), repository.Group{Name: "raw-audit", CacheQuotaBytes: 1 << 30, Members: []repository.Member{{Name: "hosted", Type: repository.MemberHosted, Endpoint: "https://legacy.example:8443"}}}); err != nil {
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
	if format != "raw" || resource != "release/app.txt" || representation != "body" || memberType != "hosted" || upstreamHost != "legacy.example" || operation != "get" || status != http.StatusOK || disposition != "miss" || bytes != 8 || requestID != "postgres-raw-request" || len(traceID) != 32 {
		t.Fatalf("Raw audit fields = format=%q resource=%q representation=%q member_type=%q upstream_host=%q operation=%q status=%d disposition=%q bytes=%d request_id=%q trace_id=%q", format, resource, representation, memberType, upstreamHost, operation, status, disposition, bytes, requestID, traceID)
	}
	audits, err := store.ListAudits(context.Background(), repository.AuditQuery{GroupName: "raw-audit"})
	if err != nil || len(audits) != 1 {
		t.Fatalf("ListAudits err=%v audits=%#v", err, audits)
	}
	if audit := audits[0]; audit.Format != "raw" || audit.Resource != "release/app.txt" || audit.Representation != "body" || audit.MemberType != "hosted" || audit.UpstreamHost != "legacy.example" || audit.Operation != "get" || audit.Status != http.StatusOK || audit.CacheDisposition != "miss" || audit.Bytes != 8 || audit.RequestID != "postgres-raw-request" || len(audit.TraceID) != 32 {
		t.Fatalf("ListAudits Raw fields=%#v", audit)
	}
}
