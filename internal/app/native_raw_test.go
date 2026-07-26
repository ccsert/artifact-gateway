package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

func TestNativeRawHostedPutReadRangeHeadAndDelete(t *testing.T) {
	store := repository.NewMemoryStore()
	_, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: "raw-repo", Name: "downloads", Format: repository.FormatRaw})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewGatewayHandler(Dependencies{NativeOCIObjectStore: NewMemoryOCIObjectStore()}, store, TestAdapter{}, testAuthenticator())
	request := func(method, path string, body []byte) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, bytes.NewReader(body))
		authorize(r, "resolver-secret")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	put := request(http.MethodPut, "/raw/downloads/releases/app.txt", []byte("native raw artifact"))
	if put.Code != http.StatusCreated || put.Header().Get("ETag") == "" {
		t.Fatalf("put=%d headers=%v", put.Code, put.Header())
	}
	sum := sha256.Sum256([]byte("native raw artifact"))
	putWithDigest := httptest.NewRequest(http.MethodPut, "/raw/downloads/releases/verified.txt", bytes.NewReader([]byte("native raw artifact")))
	putWithDigest.Header.Set("Digest", "sha-256="+base64.StdEncoding.EncodeToString(sum[:]))
	authorize(putWithDigest, "resolver-secret")
	verified := httptest.NewRecorder()
	handler.ServeHTTP(verified, putWithDigest)
	if verified.Code != http.StatusCreated {
		t.Fatalf("verified PUT=%d", verified.Code)
	}
	rangeRequest := httptest.NewRequest(http.MethodGet, "/raw/downloads/releases/app.txt", nil)
	rangeRequest.Header.Set("Range", "bytes=7-9")
	authorize(rangeRequest, "resolver-secret")
	ranged := httptest.NewRecorder()
	handler.ServeHTTP(ranged, rangeRequest)
	if ranged.Code != http.StatusPartialContent || ranged.Body.String() != "raw" {
		t.Fatalf("range=%d body=%q", ranged.Code, ranged.Body.String())
	}
	head := request(http.MethodHead, "/raw/downloads/releases/app.txt", nil)
	if head.Code != http.StatusOK || head.Body.Len() != 0 {
		t.Fatalf("head=%d body=%q", head.Code, head.Body.String())
	}
	deleted := request(http.MethodDelete, "/raw/downloads/releases/app.txt", nil)
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("delete=%d", deleted.Code)
	}
	missing := request(http.MethodGet, "/raw/downloads/releases/app.txt", nil)
	if missing.Code != http.StatusNotFound {
		t.Fatalf("missing=%d", missing.Code)
	}
}

func TestNativeRawHostedUsesManagedRepositoryGrants(t *testing.T) {
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: "raw-repo", Name: "downloads", Format: repository.FormatRaw})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReplaceRepositoryGrants(context.Background(), repo.ID, []repository.RepositoryGrant{{Principal: "reader", Scopes: []string{"repositories:read"}}}, "1"); err != nil {
		t.Fatal(err)
	}
	authenticator := testAuthenticator()
	handler := NewGatewayHandler(Dependencies{NativeOCIObjectStore: NewMemoryOCIObjectStore()}, store, TestAdapter{}, authenticator)
	adminPut := httptest.NewRequest(http.MethodPut, "/raw/downloads/releases/app.txt", strings.NewReader("native raw artifact"))
	authorize(adminPut, "admin-secret")
	adminPutResponse := httptest.NewRecorder()
	handler.ServeHTTP(adminPutResponse, adminPut)
	if adminPutResponse.Code != http.StatusCreated {
		t.Fatalf("admin put=%d body=%s", adminPutResponse.Code, adminPutResponse.Body.String())
	}
	readerGet := httptest.NewRequest(http.MethodGet, "/raw/downloads/releases/app.txt", nil)
	authorize(readerGet, authenticator.IssueToken("reader"))
	readerGetResponse := httptest.NewRecorder()
	handler.ServeHTTP(readerGetResponse, readerGet)
	if readerGetResponse.Code != http.StatusOK || readerGetResponse.Body.String() != "native raw artifact" {
		t.Fatalf("reader get=%d body=%s", readerGetResponse.Code, readerGetResponse.Body.String())
	}
	readerPut := httptest.NewRequest(http.MethodPut, "/raw/downloads/releases/denied.txt", strings.NewReader("denied"))
	authorize(readerPut, authenticator.IssueToken("reader"))
	readerPutResponse := httptest.NewRecorder()
	handler.ServeHTTP(readerPutResponse, readerPut)
	if readerPutResponse.Code != http.StatusUnauthorized || readerPutResponse.Header().Get("WWW-Authenticate") == "" {
		t.Fatalf("reader put=%d headers=%v", readerPutResponse.Code, readerPutResponse.Header())
	}
	if len(store.Audits) == 0 {
		t.Fatal("expected authorization audit")
	}
	audit := store.Audits[len(store.Audits)-1]
	if audit.AuthorizationSource != "repository_grants" || audit.AuthorizationReason != "scope_not_granted" || audit.Format != "raw" {
		t.Fatalf("audit=%#v", audit)
	}
	metrics := httptest.NewRecorder()
	handler.ServeHTTP(metrics, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if !strings.Contains(metrics.Body.String(), `artifact_gateway_repository_authorization_denials_total{format="raw",authorization_source="repository_grants",authorization_reason="scope_not_granted"} 1`) {
		t.Fatalf("raw authorization metric=%s", metrics.Body.String())
	}
}

func TestNativeRawHostedListsVisibleAssetsWithPagination(t *testing.T) {
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: "raw-repo", Name: "downloads", Format: repository.FormatRaw})
	if err != nil {
		t.Fatal(err)
	}
	authenticator := testAuthenticator()
	handler := NewGatewayHandler(Dependencies{NativeOCIObjectStore: NewMemoryOCIObjectStore()}, store, TestAdapter{}, authenticator)
	put := func(path, content string) {
		t.Helper()
		req := httptest.NewRequest(http.MethodPut, "/raw/downloads/"+path, strings.NewReader(content))
		req.Header.Set("Content-Type", "text/plain")
		authorize(req, "resolver-secret")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		if response.Code != http.StatusCreated {
			t.Fatalf("put %s=%d %s", path, response.Code, response.Body.String())
		}
	}
	put("releases/alpha.txt", "alpha")
	put("releases/beta.txt", "beta")
	put("snapshots/gamma.txt", "gamma")
	if _, err := store.ReplaceRepositoryGrants(context.Background(), repo.ID, []repository.RepositoryGrant{{Principal: "raw-browser", Scopes: []string{"repositories:read"}}}, "1"); err != nil {
		t.Fatal(err)
	}
	readerToken := authenticator.IssueToken("raw-browser")
	list := httptest.NewRequest(http.MethodGet, "/raw/downloads/releases/?n=1", nil)
	authorize(list, readerToken)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, list)
	var page struct {
		Path  string `json:"path"`
		Items []struct {
			Path        string `json:"path"`
			Digest      string `json:"digest"`
			Size        int64  `json:"size"`
			ContentType string `json:"contentType"`
		} `json:"items"`
	}
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &page) != nil || page.Path != "releases/" || len(page.Items) != 1 || page.Items[0].Path != "releases/alpha.txt" || page.Items[0].Digest == "" || page.Items[0].Size != 5 || page.Items[0].ContentType != "text/plain" {
		t.Fatalf("list=%d body=%s page=%#v", response.Code, response.Body.String(), page)
	}
	link := response.Header().Get("Link")
	if !strings.Contains(link, "last=releases%2Falpha.txt") || !strings.Contains(link, `rel="next"`) {
		t.Fatalf("link=%q", link)
	}
	next := httptest.NewRequest(http.MethodGet, "/raw/downloads/releases/?n=1&last=releases%2Falpha.txt", nil)
	authorize(next, readerToken)
	nextResponse := httptest.NewRecorder()
	handler.ServeHTTP(nextResponse, next)
	if nextResponse.Code != http.StatusOK || !strings.Contains(nextResponse.Body.String(), `"releases/beta.txt"`) {
		t.Fatalf("next=%d %s", nextResponse.Code, nextResponse.Body.String())
	}
	head := httptest.NewRequest(http.MethodHead, "/raw/downloads/releases/", nil)
	authorize(head, readerToken)
	headResponse := httptest.NewRecorder()
	handler.ServeHTTP(headResponse, head)
	if headResponse.Code != http.StatusOK || headResponse.Body.Len() != 0 {
		t.Fatalf("head=%d body=%q", headResponse.Code, headResponse.Body.String())
	}
	invalid := httptest.NewRequest(http.MethodGet, "/raw/downloads/releases/?last=../alpha.txt", nil)
	authorize(invalid, readerToken)
	invalidResponse := httptest.NewRecorder()
	handler.ServeHTTP(invalidResponse, invalid)
	if invalidResponse.Code != http.StatusBadRequest {
		t.Fatalf("invalid cursor=%d %s", invalidResponse.Code, invalidResponse.Body.String())
	}
	deleteRequest := httptest.NewRequest(http.MethodDelete, "/raw/downloads/releases/alpha.txt", nil)
	authorize(deleteRequest, "admin-secret")
	deleted := httptest.NewRecorder()
	handler.ServeHTTP(deleted, deleteRequest)
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("delete=%d %s", deleted.Code, deleted.Body.String())
	}
	remaining := httptest.NewRequest(http.MethodGet, "/raw/downloads/releases/", nil)
	authorize(remaining, readerToken)
	remainingResponse := httptest.NewRecorder()
	handler.ServeHTTP(remainingResponse, remaining)
	if remainingResponse.Code != http.StatusOK || strings.Contains(remainingResponse.Body.String(), "alpha.txt") || !strings.Contains(remainingResponse.Body.String(), "beta.txt") || strings.Contains(remainingResponse.Body.String(), "gamma.txt") {
		t.Fatalf("remaining=%d %s", remainingResponse.Code, remainingResponse.Body.String())
	}
}

func TestNativeRawHostedStreamsRangeFromObjectStore(t *testing.T) {
	store := repository.NewMemoryStore()
	_, _ = store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: "raw-repo", Name: "downloads", Format: repository.FormatRaw})
	objects := &recordingNativeRawObjectStore{MemoryOCIObjectStore: NewMemoryOCIObjectStore()}
	handler := NewGatewayHandler(Dependencies{NativeOCIObjectStore: objects}, store, TestAdapter{}, testAuthenticator())
	put := httptest.NewRequest(http.MethodPut, "/raw/downloads/releases/big.bin", strings.NewReader(strings.Repeat("abcdef", 128)))
	authorize(put, "resolver-secret")
	created := httptest.NewRecorder()
	handler.ServeHTTP(created, put)
	if created.Code != http.StatusCreated {
		t.Fatalf("put=%d %s", created.Code, created.Body.String())
	}
	rangeRequest := httptest.NewRequest(http.MethodGet, "/raw/downloads/releases/big.bin", nil)
	rangeRequest.Header.Set("Range", "bytes=6-11")
	authorize(rangeRequest, "resolver-secret")
	ranged := httptest.NewRecorder()
	handler.ServeHTTP(ranged, rangeRequest)
	if ranged.Code != http.StatusPartialContent || ranged.Body.String() != "abcdef" {
		t.Fatalf("range=%d body=%q", ranged.Code, ranged.Body.String())
	}
	if objects.openRangeCalls != 1 || objects.openCalls != 0 {
		t.Fatalf("open=%d openRange=%d", objects.openCalls, objects.openRangeCalls)
	}
	get := httptest.NewRequest(http.MethodGet, "/raw/downloads/releases/big.bin", nil)
	authorize(get, "resolver-secret")
	getResponse := httptest.NewRecorder()
	handler.ServeHTTP(getResponse, get)
	if getResponse.Code != http.StatusOK || getResponse.Body.Len() == 0 || objects.openCalls != 1 {
		t.Fatalf("get=%d bytes=%d open=%d", getResponse.Code, getResponse.Body.Len(), objects.openCalls)
	}
	head := httptest.NewRequest(http.MethodHead, "/raw/downloads/releases/big.bin", nil)
	authorize(head, "resolver-secret")
	headResponse := httptest.NewRecorder()
	handler.ServeHTTP(headResponse, head)
	if headResponse.Code != http.StatusOK || headResponse.Body.Len() != 0 {
		t.Fatalf("head=%d body=%q", headResponse.Code, headResponse.Body.String())
	}
	if objects.openCalls != 2 {
		t.Fatalf("head did not stream metadata through Open: open=%d", objects.openCalls)
	}
}

type recordingNativeRawObjectStore struct {
	*MemoryOCIObjectStore
	openCalls      int
	openRangeCalls int
}

func (s *recordingNativeRawObjectStore) Open(ctx context.Context, key string) (io.ReadCloser, int64, error) {
	s.openCalls++
	return s.MemoryOCIObjectStore.Open(ctx, key)
}

func (s *recordingNativeRawObjectStore) OpenRange(ctx context.Context, key string, offset, length int64) (io.ReadCloser, int64, error) {
	s.openRangeCalls++
	return s.MemoryOCIObjectStore.OpenRange(ctx, key, offset, length)
}
