//go:build integration

package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
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

func TestNativeRawListingAcrossPostgresAndMinIOGatewayInstances(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	s3Endpoint := os.Getenv("TEST_S3_ENDPOINT")
	accessKey := os.Getenv("TEST_S3_ACCESS_KEY")
	secretKey := os.Getenv("TEST_S3_SECRET_KEY")
	if databaseURL == "" || s3Endpoint == "" || accessKey == "" || secretKey == "" {
		t.Skip("PostgreSQL and S3 integration environment is required")
	}
	storeA, err := repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer storeA.Close()
	storeB, err := repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer storeB.Close()
	bucket := "native-raw-list-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:20]
	objectsA, err := NewS3OCIObjectStore(s3Endpoint, accessKey, secretKey, bucket)
	if err != nil {
		t.Fatal(err)
	}
	if err = objectsA.EnsureBucket(context.Background()); err != nil {
		t.Fatal(err)
	}
	objectsB, err := NewS3OCIObjectStore(s3Endpoint, accessKey, secretKey, bucket)
	if err != nil {
		t.Fatal(err)
	}
	repo, err := storeA.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "raw-list-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:20], Format: repository.FormatRaw})
	if err != nil {
		t.Fatal(err)
	}
	serverA := httptest.NewServer(NewGatewayHandler(Dependencies{NativeOCIObjectStore: objectsA}, storeA, TestAdapter{}, testAuthenticator()))
	defer serverA.Close()
	serverB := httptest.NewServer(NewGatewayHandler(Dependencies{NativeOCIObjectStore: objectsB}, storeB, TestAdapter{}, testAuthenticator()))
	defer serverB.Close()
	request := func(method, address string, body []byte) *http.Response {
		t.Helper()
		req, err := http.NewRequest(method, address, bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer resolver-secret")
		response, err := serverA.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return response
	}
	for _, name := range []string{"releases/alpha.txt", "releases/beta.txt"} {
		response := request(http.MethodPut, serverA.URL+"/raw/"+repo.Name+"/"+name, []byte(name))
		if response.StatusCode != http.StatusCreated {
			_ = response.Body.Close()
			t.Fatalf("put %s=%d", name, response.StatusCode)
		}
		_ = response.Body.Close()
	}
	checksumRequest, err := http.NewRequest(http.MethodGet, serverB.URL+"/raw/"+repo.Name+"/releases/alpha.txt.sha256", nil)
	if err != nil {
		t.Fatal(err)
	}
	checksumRequest.Header.Set("Authorization", "Bearer resolver-secret")
	checksumResponse, err := serverB.Client().Do(checksumRequest)
	if err != nil {
		t.Fatal(err)
	}
	defer checksumResponse.Body.Close()
	sum := sha256.Sum256([]byte("releases/alpha.txt"))
	var checksumBody bytes.Buffer
	if _, err := checksumBody.ReadFrom(checksumResponse.Body); err != nil {
		t.Fatal(err)
	}
	if checksumResponse.StatusCode != http.StatusOK || checksumBody.String() != hex.EncodeToString(sum[:])+"\n" {
		t.Fatalf("checksum status=%d body=%q", checksumResponse.StatusCode, checksumBody.String())
	}
	listRequest, err := http.NewRequest(http.MethodGet, serverB.URL+"/raw/"+repo.Name+"/releases/?n=1", nil)
	if err != nil {
		t.Fatal(err)
	}
	listRequest.Header.Set("Authorization", "Bearer resolver-secret")
	listResponse, err := serverB.Client().Do(listRequest)
	if err != nil {
		t.Fatal(err)
	}
	defer listResponse.Body.Close()
	var page struct {
		Items []struct {
			Path string `json:"path"`
		} `json:"items"`
	}
	if err := json.NewDecoder(listResponse.Body).Decode(&page); err != nil {
		t.Fatal(err)
	}
	if listResponse.StatusCode != http.StatusOK || len(page.Items) != 1 || page.Items[0].Path != "releases/alpha.txt" || listResponse.Header.Get("Link") == "" {
		t.Fatalf("list status=%d page=%#v link=%q", listResponse.StatusCode, page, listResponse.Header.Get("Link"))
	}
	deleted := request(http.MethodDelete, serverA.URL+"/raw/"+repo.Name+"/releases/alpha.txt", nil)
	if deleted.StatusCode != http.StatusNoContent {
		_ = deleted.Body.Close()
		t.Fatalf("delete=%d", deleted.StatusCode)
	}
	_ = deleted.Body.Close()
	remainingRequest, err := http.NewRequest(http.MethodGet, serverB.URL+"/raw/"+repo.Name+"/releases/", nil)
	if err != nil {
		t.Fatal(err)
	}
	remainingRequest.Header.Set("Authorization", "Bearer resolver-secret")
	remainingResponse, err := serverB.Client().Do(remainingRequest)
	if err != nil {
		t.Fatal(err)
	}
	defer remainingResponse.Body.Close()
	var remaining struct {
		Items []struct {
			Path string `json:"path"`
		} `json:"items"`
	}
	if err := json.NewDecoder(remainingResponse.Body).Decode(&remaining); err != nil {
		t.Fatal(err)
	}
	if remainingResponse.StatusCode != http.StatusOK || len(remaining.Items) != 1 || remaining.Items[0].Path != "releases/beta.txt" {
		t.Fatalf("remaining status=%d page=%#v", remainingResponse.StatusCode, remaining)
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
