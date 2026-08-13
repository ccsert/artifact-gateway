//go:build integration

package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	rawmaintenance "github.com/artifact-gateway/artifact-gateway/internal/maintenance/raw"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
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

func TestNativeRawListingAcrossPostgresAndRustFSGatewayInstances(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	s3Endpoint := os.Getenv("TEST_RUSTFS_ENDPOINT")
	accessKey := os.Getenv("TEST_RUSTFS_ACCESS_KEY")
	secretKey := os.Getenv("TEST_RUSTFS_SECRET_KEY")
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
	objectsA, err := NewRustFSOCIObjectStore(s3Endpoint, accessKey, secretKey, bucket)
	if err != nil {
		t.Fatal(err)
	}
	if err = objectsA.EnsureBucket(context.Background()); err != nil {
		t.Fatal(err)
	}
	objectsB, err := NewRustFSOCIObjectStore(s3Endpoint, accessKey, secretKey, bucket)
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
	if _, err = storeA.ReplaceRepositoryGrants(context.Background(), repo.ID, []repository.RepositoryGrant{
		{Principal: "search-reader", Scopes: []string{"repositories:read"}},
		{Principal: "build-agent", Scopes: []string{"repositories:read", "repositories:write"}},
	}, "1"); err != nil {
		t.Fatal(err)
	}
	searchRequest, err := http.NewRequest(http.MethodGet, serverB.URL+"/api/v2/repositories/"+repo.ID+"/artifact-search?q=releases%2F&pageSize=1", nil)
	if err != nil {
		t.Fatal(err)
	}
	searchRequest.Header.Set("Authorization", "Bearer "+testAuthenticator().IssueToken("search-reader"))
	searchResponse, err := serverB.Client().Do(searchRequest)
	if err != nil {
		t.Fatal(err)
	}
	var searchPage struct {
		Items []struct {
			Coordinate string `json:"coordinate"`
			Digest     string `json:"digest"`
			Size       int64  `json:"size"`
		} `json:"items"`
		NextPageToken string `json:"nextPageToken"`
	}
	if err := json.NewDecoder(searchResponse.Body).Decode(&searchPage); err != nil {
		_ = searchResponse.Body.Close()
		t.Fatal(err)
	}
	_ = searchResponse.Body.Close()
	if searchResponse.StatusCode != http.StatusOK || len(searchPage.Items) != 1 || searchPage.Items[0].Coordinate != "releases/alpha.txt" || searchPage.Items[0].Digest == "" || searchPage.Items[0].Size != int64(len("releases/alpha.txt")) || searchPage.NextPageToken == "" {
		t.Fatalf("artifact search=%d page=%#v", searchResponse.StatusCode, searchPage)
	}
	nextSearchRequest, err := http.NewRequest(http.MethodGet, serverA.URL+"/api/v2/repositories/"+repo.ID+"/artifact-search?q=releases%2F&pageSize=1&pageToken="+url.QueryEscape(searchPage.NextPageToken), nil)
	if err != nil {
		t.Fatal(err)
	}
	nextSearchRequest.Header.Set("Authorization", "Bearer "+testAuthenticator().IssueToken("search-reader"))
	nextSearchResponse, err := serverA.Client().Do(nextSearchRequest)
	if err != nil {
		t.Fatal(err)
	}
	var nextSearchPage struct {
		Items []struct {
			Coordinate string `json:"coordinate"`
		} `json:"items"`
	}
	if err := json.NewDecoder(nextSearchResponse.Body).Decode(&nextSearchPage); err != nil {
		_ = nextSearchResponse.Body.Close()
		t.Fatal(err)
	}
	_ = nextSearchResponse.Body.Close()
	if nextSearchResponse.StatusCode != http.StatusOK || len(nextSearchPage.Items) != 1 || nextSearchPage.Items[0].Coordinate != "releases/beta.txt" {
		t.Fatalf("artifact search next=%d page=%#v", nextSearchResponse.StatusCode, nextSearchPage)
	}
	started := request(http.MethodPost, serverA.URL+"/raw/"+repo.Name+"/releases/resumable.bin?resumable=1", nil)
	if started.StatusCode != http.StatusCreated || started.Header.Get("Location") == "" {
		_ = started.Body.Close()
		t.Fatalf("start resumable=%d", started.StatusCode)
	}
	uploadLocation := started.Header.Get("Location")
	_ = started.Body.Close()
	patchRequest, err := http.NewRequest(http.MethodPatch, serverA.URL+uploadLocation, strings.NewReader("hello "))
	if err != nil {
		t.Fatal(err)
	}
	patchRequest.Header.Set("Authorization", "Bearer resolver-secret")
	patchRequest.Header.Set("Upload-Offset", "0")
	patched, err := serverA.Client().Do(patchRequest)
	if err != nil {
		t.Fatal(err)
	}
	if patched.StatusCode != http.StatusNoContent || patched.Header.Get("Upload-Offset") != "6" {
		_ = patched.Body.Close()
		t.Fatalf("patch=%d offset=%q", patched.StatusCode, patched.Header.Get("Upload-Offset"))
	}
	_ = patched.Body.Close()
	statusRequest, err := http.NewRequest(http.MethodGet, serverB.URL+uploadLocation, nil)
	if err != nil {
		t.Fatal(err)
	}
	statusRequest.Header.Set("Authorization", "Bearer resolver-secret")
	status, err := serverB.Client().Do(statusRequest)
	if err != nil {
		t.Fatal(err)
	}
	if status.StatusCode != http.StatusNoContent || status.Header.Get("Upload-Offset") != "6" {
		_ = status.Body.Close()
		t.Fatalf("status=%d offset=%q", status.StatusCode, status.Header.Get("Upload-Offset"))
	}
	_ = status.Body.Close()
	data := []byte("hello world")
	dataSum := sha256.Sum256(data)
	completeRequest, err := http.NewRequest(http.MethodPut, serverB.URL+uploadLocation+"&complete=1", strings.NewReader("world"))
	if err != nil {
		t.Fatal(err)
	}
	completeRequest.Header.Set("Authorization", "Bearer resolver-secret")
	completeRequest.Header.Set("Upload-Offset", "6")
	completeRequest.Header.Set("Digest", "sha-256="+base64.StdEncoding.EncodeToString(dataSum[:]))
	completed, err := serverB.Client().Do(completeRequest)
	if err != nil {
		t.Fatal(err)
	}
	if completed.StatusCode != http.StatusCreated {
		_ = completed.Body.Close()
		t.Fatalf("complete=%d", completed.StatusCode)
	}
	_ = completed.Body.Close()
	readResumable := request(http.MethodGet, serverA.URL+"/raw/"+repo.Name+"/releases/resumable.bin", nil)
	resumableBody, _ := io.ReadAll(readResumable.Body)
	_ = readResumable.Body.Close()
	if readResumable.StatusCode != http.StatusOK || string(resumableBody) != string(data) {
		t.Fatalf("read resumable=%d %q", readResumable.StatusCode, resumableBody)
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
	remainingPaths := make(map[string]bool, len(remaining.Items))
	for _, item := range remaining.Items {
		remainingPaths[item.Path] = true
	}
	if remainingResponse.StatusCode != http.StatusOK || len(remainingPaths) != 2 || !remainingPaths["releases/beta.txt"] || !remainingPaths["releases/resumable.bin"] || remainingPaths["releases/alpha.txt"] {
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
