package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

func TestNativeOCIHostedUploadMountManifestRangeAndDelete(t *testing.T) {
	store := repository.NewMemoryStore()
	_, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: "oci-repo", Name: "team", Format: repository.FormatOCI})
	if err != nil {
		t.Fatal(err)
	}
	objects := NewMemoryOCIObjectStore()
	handler := NewGatewayHandler(Dependencies{NativeOCIObjectStore: objects}, store, TestAdapter{}, testAuthenticator())
	auth := func(r *http.Request) { authorize(r, "resolver-secret") }

	start := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v2/team/app/blobs/uploads/", nil)
	auth(req)
	handler.ServeHTTP(start, req)
	if start.Code != http.StatusAccepted {
		t.Fatalf("start=%d %s", start.Code, start.Body.String())
	}
	location := start.Header().Get("Location")
	if location == "" {
		t.Fatal("upload location is missing")
	}
	chunk := []byte("native hosted blob")
	patch := httptest.NewRequest(http.MethodPatch, location, bytes.NewReader(chunk[:7]))
	patch.Header.Set("Content-Range", "0-6")
	auth(patch)
	patched := httptest.NewRecorder()
	handler.ServeHTTP(patched, patch)
	if patched.Code != http.StatusAccepted || patched.Header().Get("Range") != "0-6" {
		t.Fatalf("patch=%d headers=%v", patched.Code, patched.Header())
	}
	sum := sha256.Sum256(chunk)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	complete := httptest.NewRequest(http.MethodPut, location+"?digest="+digest, bytes.NewReader(chunk[7:]))
	complete.Header.Set("Content-Range", "7-"+utoa(uint64(len(chunk)-1)))
	auth(complete)
	completed := httptest.NewRecorder()
	handler.ServeHTTP(completed, complete)
	if completed.Code != http.StatusCreated || completed.Header().Get("Docker-Content-Digest") != digest {
		t.Fatalf("complete=%d headers=%v body=%s", completed.Code, completed.Header(), completed.Body.String())
	}

	get := httptest.NewRequest(http.MethodGet, "/v2/team/app/blobs/"+digest, nil)
	get.Header.Set("Range", "bytes=2-7")
	auth(get)
	got := httptest.NewRecorder()
	handler.ServeHTTP(got, get)
	if got.Code != http.StatusPartialContent || got.Body.String() != string(chunk[2:8]) || got.Header().Get("Content-Range") != "bytes 2-7/"+utoa(uint64(len(chunk))) {
		t.Fatalf("range=%d headers=%v body=%q", got.Code, got.Header(), got.Body.String())
	}

	mount := httptest.NewRequest(http.MethodPost, "/v2/team/other/blobs/uploads/?mount="+digest+"&from=team/app", nil)
	auth(mount)
	mounted := httptest.NewRecorder()
	handler.ServeHTTP(mounted, mount)
	if mounted.Code != http.StatusCreated {
		t.Fatalf("mount=%d %s", mounted.Code, mounted.Body.String())
	}
	manifest := []byte(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json"}`)
	put := httptest.NewRequest(http.MethodPut, "/v2/team/app/manifests/latest", bytes.NewReader(manifest))
	put.Header.Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
	auth(put)
	published := httptest.NewRecorder()
	handler.ServeHTTP(published, put)
	if published.Code != http.StatusCreated {
		t.Fatalf("publish=%d %s", published.Code, published.Body.String())
	}
	manifestDigest := published.Header().Get("Docker-Content-Digest")
	read := httptest.NewRequest(http.MethodHead, "/v2/team/app/manifests/latest", nil)
	auth(read)
	readResponse := httptest.NewRecorder()
	handler.ServeHTTP(readResponse, read)
	if readResponse.Code != http.StatusOK || readResponse.Header().Get("Docker-Content-Digest") != manifestDigest || readResponse.Body.Len() != 0 {
		t.Fatalf("head=%d headers=%v", readResponse.Code, readResponse.Header())
	}
	deleteRequest := httptest.NewRequest(http.MethodDelete, "/v2/team/app/manifests/"+manifestDigest, nil)
	auth(deleteRequest)
	deleted := httptest.NewRecorder()
	handler.ServeHTTP(deleted, deleteRequest)
	if deleted.Code != http.StatusAccepted {
		t.Fatalf("delete=%d %s", deleted.Code, deleted.Body.String())
	}
	missing := httptest.NewRecorder()
	missingRequest := httptest.NewRequest(http.MethodGet, "/v2/team/app/manifests/latest", nil)
	auth(missingRequest)
	handler.ServeHTTP(missing, missingRequest)
	if missing.Code != http.StatusNotFound || !strings.Contains(missing.Body.String(), "MANIFEST_UNKNOWN") {
		t.Fatalf("missing=%d %s", missing.Code, missing.Body.String())
	}
	tombstone, err := store.GetArtifactTombstone(context.Background(), "oci-repo", repository.FormatOCI, "app@"+manifestDigest)
	if err != nil || tombstone.Digest != manifestDigest || tombstone.TombstonedAt.IsZero() {
		t.Fatalf("tombstone=%#v err=%v", tombstone, err)
	}
}

func TestNativeOCIHostedUsesManagedRepositoryGrants(t *testing.T) {
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: "oci-repo", Name: "team", Format: repository.FormatOCI})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReplaceRepositoryGrants(context.Background(), repo.ID, []repository.RepositoryGrant{{Principal: "reader", Scopes: []string{"repositories:read"}}}, "1"); err != nil {
		t.Fatal(err)
	}
	authenticator := testAuthenticator()
	handler := NewGatewayHandler(Dependencies{NativeOCIObjectStore: NewMemoryOCIObjectStore()}, store, TestAdapter{}, authenticator)
	start := httptest.NewRequest(http.MethodPost, "/v2/team/app/blobs/uploads/", nil)
	authorize(start, "admin-secret")
	started := httptest.NewRecorder()
	handler.ServeHTTP(started, start)
	if started.Code != http.StatusAccepted {
		t.Fatalf("admin start=%d body=%s", started.Code, started.Body.String())
	}
	blob := []byte("native hosted blob")
	sum := sha256.Sum256(blob)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	complete := httptest.NewRequest(http.MethodPut, started.Header().Get("Location")+"?digest="+digest, bytes.NewReader(blob))
	authorize(complete, "admin-secret")
	completed := httptest.NewRecorder()
	handler.ServeHTTP(completed, complete)
	if completed.Code != http.StatusCreated {
		t.Fatalf("admin complete=%d body=%s", completed.Code, completed.Body.String())
	}
	readerGet := httptest.NewRequest(http.MethodGet, "/v2/team/app/blobs/"+digest, nil)
	authorize(readerGet, authenticator.IssueToken("reader"))
	readerGetResponse := httptest.NewRecorder()
	handler.ServeHTTP(readerGetResponse, readerGet)
	if readerGetResponse.Code != http.StatusOK || !bytes.Equal(readerGetResponse.Body.Bytes(), blob) {
		t.Fatalf("reader get=%d body=%s", readerGetResponse.Code, readerGetResponse.Body.String())
	}
	readerStart := httptest.NewRequest(http.MethodPost, "/v2/team/app/blobs/uploads/", nil)
	authorize(readerStart, authenticator.IssueToken("reader"))
	readerStartResponse := httptest.NewRecorder()
	handler.ServeHTTP(readerStartResponse, readerStart)
	if readerStartResponse.Code != http.StatusUnauthorized || readerStartResponse.Header().Get("WWW-Authenticate") == "" {
		t.Fatalf("reader start=%d headers=%v", readerStartResponse.Code, readerStartResponse.Header())
	}
	if len(store.Audits) == 0 {
		t.Fatal("expected authorization audit")
	}
	audit := store.Audits[len(store.Audits)-1]
	if audit.AuthorizationSource != "repository_grants" || audit.AuthorizationReason != "scope_not_granted" || audit.Format != "oci" {
		t.Fatalf("audit=%#v", audit)
	}
	metrics := httptest.NewRecorder()
	handler.ServeHTTP(metrics, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if !strings.Contains(metrics.Body.String(), `artifact_gateway_repository_authorization_denials_total{format="oci",authorization_source="repository_grants",authorization_reason="scope_not_granted"} 1`) {
		t.Fatalf("OCI authorization metric=%s", metrics.Body.String())
	}
}

func TestNativeOCITagsListPaginatesAndSupportsHead(t *testing.T) {
	store := repository.NewMemoryStore()
	_, _ = store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: "oci-repo", Name: "team", Format: repository.FormatOCI})
	handler := NewGatewayHandler(Dependencies{NativeOCIObjectStore: NewMemoryOCIObjectStore()}, store, TestAdapter{}, testAuthenticator())
	for _, tag := range []string{"gamma", "alpha", "beta"} {
		manifest := httptest.NewRequest(http.MethodPut, "/v2/team/app/manifests/"+tag, strings.NewReader(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json"}`))
		manifest.Header.Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
		authorize(manifest, "resolver-secret")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, manifest)
		if response.Code != http.StatusCreated {
			t.Fatalf("publish %s=%d %s", tag, response.Code, response.Body.String())
		}
	}
	request := httptest.NewRequest(http.MethodGet, "/v2/team/app/tags/list?n=2", nil)
	authorize(request, "resolver-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("tags=%d %s", response.Code, response.Body.String())
	}
	var body struct {
		Name string   `json:"name"`
		Tags []string `json:"tags"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Name != "team/app" || strings.Join(body.Tags, ",") != "alpha,beta" {
		t.Fatalf("body=%#v", body)
	}
	link := response.Header().Get("Link")
	if !strings.Contains(link, "/v2/team/app/tags/list?n=2&last=beta") || !strings.Contains(link, `rel="next"`) {
		t.Fatalf("link=%q", link)
	}
	next := httptest.NewRequest(http.MethodGet, "/v2/team/app/tags/list?n=2&last="+url.QueryEscape("beta"), nil)
	authorize(next, "resolver-secret")
	nextResponse := httptest.NewRecorder()
	handler.ServeHTTP(nextResponse, next)
	if nextResponse.Code != http.StatusOK || !strings.Contains(nextResponse.Body.String(), `"gamma"`) {
		t.Fatalf("next=%d %s", nextResponse.Code, nextResponse.Body.String())
	}
	head := httptest.NewRequest(http.MethodHead, "/v2/team/app/tags/list", nil)
	authorize(head, "resolver-secret")
	headResponse := httptest.NewRecorder()
	handler.ServeHTTP(headResponse, head)
	if headResponse.Code != http.StatusOK || headResponse.Body.Len() != 0 {
		t.Fatalf("head=%d body=%q", headResponse.Code, headResponse.Body.String())
	}
}

func TestNativeOCIReferrersPaginateAndExcludeDeletedManifests(t *testing.T) {
	store := repository.NewMemoryStore()
	_, _ = store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: "oci-repo", Name: "team", Format: repository.FormatOCI})
	handler := NewGatewayHandler(Dependencies{NativeOCIObjectStore: NewMemoryOCIObjectStore()}, store, TestAdapter{}, testAuthenticator())
	publish := func(tag string, body string) string {
		t.Helper()
		req := httptest.NewRequest(http.MethodPut, "/v2/team/app/manifests/"+tag, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
		authorize(req, "resolver-secret")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		if response.Code != http.StatusCreated {
			t.Fatalf("publish %s=%d %s", tag, response.Code, response.Body.String())
		}
		return response.Header().Get("Docker-Content-Digest")
	}

	subject := publish("subject", `{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json"}`)
	first := publish("referrer-a", `{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json","artifactType":"application/vnd.example.signature","subject":{"digest":"`+subject+`"}}`)
	second := publish("referrer-b", `{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json","artifactType":"application/vnd.example.attestation","subject":{"digest":"`+subject+`"}}`)

	request := httptest.NewRequest(http.MethodGet, "/v2/team/app/referrers/"+subject+"?n=1", nil)
	authorize(request, "resolver-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "application/vnd.oci.image.index.v1+json" {
		t.Fatalf("referrers=%d headers=%v body=%s", response.Code, response.Header(), response.Body.String())
	}
	var page struct {
		SchemaVersion int `json:"schemaVersion"`
		Manifests     []struct {
			Digest       string `json:"digest"`
			MediaType    string `json:"mediaType"`
			Size         int64  `json:"size"`
			ArtifactType string `json:"artifactType"`
		} `json:"manifests"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if page.SchemaVersion != 2 || len(page.Manifests) != 1 || page.Manifests[0].MediaType != "application/vnd.oci.image.manifest.v1+json" || page.Manifests[0].Size <= 0 || page.Manifests[0].ArtifactType == "" {
		t.Fatalf("page=%#v", page)
	}
	link := response.Header().Get("Link")
	if !strings.Contains(link, `rel="next"`) || !strings.Contains(link, "last=") {
		t.Fatalf("link=%q", link)
	}
	last := page.Manifests[0].Digest
	next := httptest.NewRequest(http.MethodGet, "/v2/team/app/referrers/"+subject+"?n=1&last="+url.QueryEscape(last), nil)
	authorize(next, "resolver-secret")
	nextResponse := httptest.NewRecorder()
	handler.ServeHTTP(nextResponse, next)
	if nextResponse.Code != http.StatusOK || !strings.Contains(nextResponse.Body.String(), first) && !strings.Contains(nextResponse.Body.String(), second) || strings.Contains(nextResponse.Body.String(), last) {
		t.Fatalf("next=%d %s", nextResponse.Code, nextResponse.Body.String())
	}

	head := httptest.NewRequest(http.MethodHead, "/v2/team/app/referrers/"+subject, nil)
	authorize(head, "resolver-secret")
	headResponse := httptest.NewRecorder()
	handler.ServeHTTP(headResponse, head)
	if headResponse.Code != http.StatusOK || headResponse.Body.Len() != 0 {
		t.Fatalf("head=%d body=%q", headResponse.Code, headResponse.Body.String())
	}

	deleteRequest := httptest.NewRequest(http.MethodDelete, "/v2/team/app/manifests/"+first, nil)
	authorize(deleteRequest, "resolver-secret")
	deleted := httptest.NewRecorder()
	handler.ServeHTTP(deleted, deleteRequest)
	if deleted.Code != http.StatusAccepted {
		t.Fatalf("delete=%d %s", deleted.Code, deleted.Body.String())
	}
	remaining := httptest.NewRequest(http.MethodGet, "/v2/team/app/referrers/"+subject, nil)
	authorize(remaining, "resolver-secret")
	remainingResponse := httptest.NewRecorder()
	handler.ServeHTTP(remainingResponse, remaining)
	if remainingResponse.Code != http.StatusOK || strings.Contains(remainingResponse.Body.String(), first) || !strings.Contains(remainingResponse.Body.String(), second) {
		t.Fatalf("remaining=%d %s", remainingResponse.Code, remainingResponse.Body.String())
	}
}

func TestNativeOCIUploadCanBeCancelled(t *testing.T) {
	store := repository.NewMemoryStore()
	_, _ = store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: "oci-repo", Name: "team", Format: repository.FormatOCI})
	handler := NewGatewayHandler(Dependencies{NativeOCIObjectStore: NewMemoryOCIObjectStore()}, store, TestAdapter{}, testAuthenticator())
	start := httptest.NewRequest(http.MethodPost, "/v2/team/app/blobs/uploads/", nil)
	authorize(start, "resolver-secret")
	started := httptest.NewRecorder()
	handler.ServeHTTP(started, start)
	if started.Code != http.StatusAccepted {
		t.Fatalf("start=%d", started.Code)
	}
	location := started.Header().Get("Location")
	patch := httptest.NewRequest(http.MethodPatch, location, strings.NewReader("partial"))
	authorize(patch, "resolver-secret")
	patched := httptest.NewRecorder()
	handler.ServeHTTP(patched, patch)
	if patched.Code != http.StatusAccepted {
		t.Fatalf("patch=%d %s", patched.Code, patched.Body.String())
	}
	cancel := httptest.NewRequest(http.MethodDelete, location, nil)
	authorize(cancel, "resolver-secret")
	cancelled := httptest.NewRecorder()
	handler.ServeHTTP(cancelled, cancel)
	if cancelled.Code != http.StatusNoContent {
		t.Fatalf("cancel=%d %s", cancelled.Code, cancelled.Body.String())
	}
	retry := httptest.NewRequest(http.MethodPatch, location, strings.NewReader("again"))
	authorize(retry, "resolver-secret")
	retried := httptest.NewRecorder()
	handler.ServeHTTP(retried, retry)
	if retried.Code != http.StatusNotFound || !strings.Contains(retried.Body.String(), "BLOB_UPLOAD_UNKNOWN") {
		t.Fatalf("retry=%d %s", retried.Code, retried.Body.String())
	}
}

func TestNativeOCIManifestAcceptNegotiation(t *testing.T) {
	store := repository.NewMemoryStore()
	_, _ = store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: "oci-repo", Name: "team", Format: repository.FormatOCI})
	handler := NewGatewayHandler(Dependencies{NativeOCIObjectStore: NewMemoryOCIObjectStore()}, store, TestAdapter{}, testAuthenticator())
	manifest := httptest.NewRequest(http.MethodPut, "/v2/team/app/manifests/latest", strings.NewReader(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json"}`))
	manifest.Header.Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
	authorize(manifest, "resolver-secret")
	published := httptest.NewRecorder()
	handler.ServeHTTP(published, manifest)
	if published.Code != http.StatusCreated {
		t.Fatalf("publish=%d %s", published.Code, published.Body.String())
	}
	for _, accept := range []string{"application/vnd.oci.image.manifest.v1+json", "application/*", "*/*"} {
		request := httptest.NewRequest(http.MethodGet, "/v2/team/app/manifests/latest", nil)
		request.Header.Set("Accept", accept)
		authorize(request, "resolver-secret")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("accept %q=%d %s", accept, response.Code, response.Body.String())
		}
	}
	reject := httptest.NewRequest(http.MethodGet, "/v2/team/app/manifests/latest", nil)
	reject.Header.Set("Accept", "text/plain")
	authorize(reject, "resolver-secret")
	rejected := httptest.NewRecorder()
	handler.ServeHTTP(rejected, reject)
	if rejected.Code != http.StatusNotAcceptable || !strings.Contains(rejected.Body.String(), "MANIFEST_UNKNOWN") {
		t.Fatalf("reject=%d %s", rejected.Code, rejected.Body.String())
	}
}

func TestNativeOCIRejectsDigestMismatchAndNonHostedRepository(t *testing.T) {
	store := repository.NewMemoryStore()
	_, _ = store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: "oci-repo", Name: "team", Format: repository.FormatOCI})
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())
	request := httptest.NewRequest(http.MethodPut, "/v2/team/app/manifests/sha256:"+strings.Repeat("0", 64), strings.NewReader(`{"schemaVersion":2}`))
	authorize(request, "resolver-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "DIGEST_INVALID") {
		t.Fatalf("response=%d %s", response.Code, response.Body.String())
	}
}

func TestNativeOCIConcurrentPatchUsesUploadOffsetFence(t *testing.T) {
	store := repository.NewMemoryStore()
	_, _ = store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: "oci-repo", Name: "team", Format: repository.FormatOCI})
	handler := NewGatewayHandler(Dependencies{NativeOCIObjectStore: NewMemoryOCIObjectStore()}, store, TestAdapter{}, testAuthenticator())
	start := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v2/team/app/blobs/uploads/", nil)
	authorize(request, "resolver-secret")
	handler.ServeHTTP(start, request)
	if start.Code != http.StatusAccepted {
		t.Fatal(start.Code)
	}
	location := start.Header().Get("Location")
	responses := make(chan int, 2)
	for range 2 {
		go func() {
			r := httptest.NewRequest(http.MethodPatch, location, strings.NewReader("abc"))
			r.Header.Set("Content-Range", "0-2")
			authorize(r, "resolver-secret")
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			responses <- w.Code
		}()
	}
	first, second := <-responses, <-responses
	if (first != http.StatusAccepted || second != http.StatusRequestedRangeNotSatisfiable) && (second != http.StatusAccepted || first != http.StatusRequestedRangeNotSatisfiable) {
		t.Fatalf("concurrent patch responses = %d, %d", first, second)
	}
}

func TestNativeOCIValidatesManifestDescriptors(t *testing.T) {
	store := repository.NewMemoryStore()
	_, _ = store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: "oci-repo", Name: "team", Format: repository.FormatOCI})
	objects := NewMemoryOCIObjectStore()
	handler := NewGatewayHandler(Dependencies{NativeOCIObjectStore: objects}, store, TestAdapter{}, testAuthenticator())
	auth := func(r *http.Request) { authorize(r, "resolver-secret") }

	missing := httptest.NewRequest(http.MethodPut, "/v2/team/app/manifests/latest", strings.NewReader(`{"schemaVersion":2,"config":{"digest":"sha256:`+strings.Repeat("a", 64)+`","size":1}}`))
	auth(missing)
	missingResponse := httptest.NewRecorder()
	handler.ServeHTTP(missingResponse, missing)
	if missingResponse.Code != http.StatusBadRequest || !strings.Contains(missingResponse.Body.String(), "MANIFEST_BLOB_UNKNOWN") {
		t.Fatalf("missing blob=%d %s", missingResponse.Code, missingResponse.Body.String())
	}

	invalid := httptest.NewRequest(http.MethodPut, "/v2/team/app/manifests/latest", strings.NewReader(`{"schemaVersion":2,"config":{"digest":"sha256:not-a-digest","size":1}}`))
	auth(invalid)
	invalidResponse := httptest.NewRecorder()
	handler.ServeHTTP(invalidResponse, invalid)
	if invalidResponse.Code != http.StatusBadRequest || !strings.Contains(invalidResponse.Body.String(), "MANIFEST_INVALID") {
		t.Fatalf("invalid descriptor=%d %s", invalidResponse.Code, invalidResponse.Body.String())
	}
	unknownChild := httptest.NewRequest(http.MethodPut, "/v2/team/app/manifests/latest", strings.NewReader(`{"schemaVersion":2,"manifests":[{"digest":"sha256:`+strings.Repeat("b", 64)+`","size":1}]}`))
	auth(unknownChild)
	unknownChildResponse := httptest.NewRecorder()
	handler.ServeHTTP(unknownChildResponse, unknownChild)
	if unknownChildResponse.Code != http.StatusBadRequest || !strings.Contains(unknownChildResponse.Body.String(), "MANIFEST_UNKNOWN") {
		t.Fatalf("unknown child manifest=%d %s", unknownChildResponse.Code, unknownChildResponse.Body.String())
	}

	body := []byte("descriptor content")
	digest := uploadNativeOCIBlob(t, handler, "/v2/team/app", body)
	manifest := []byte(`{"schemaVersion":2,"config":{"digest":"` + digest + `","size":` + utoa(uint64(len(body))) + `}}`)
	publish := httptest.NewRequest(http.MethodPut, "/v2/team/app/manifests/child", bytes.NewReader(manifest))
	auth(publish)
	published := httptest.NewRecorder()
	handler.ServeHTTP(published, publish)
	if published.Code != http.StatusCreated {
		t.Fatalf("valid manifest=%d %s", published.Code, published.Body.String())
	}
	childDigest := published.Header().Get("Docker-Content-Digest")
	index := []byte(`{"schemaVersion":2,"manifests":[{"digest":"` + childDigest + `","size":` + utoa(uint64(len(manifest))) + `}]}`)
	publishIndex := httptest.NewRequest(http.MethodPut, "/v2/team/app/manifests/latest", bytes.NewReader(index))
	auth(publishIndex)
	indexed := httptest.NewRecorder()
	handler.ServeHTTP(indexed, publishIndex)
	if indexed.Code != http.StatusCreated {
		t.Fatalf("valid index=%d %s", indexed.Code, indexed.Body.String())
	}
}

func TestNativeOCIMountRequiresSourceRepositoryOwnership(t *testing.T) {
	store := repository.NewMemoryStore()
	for _, repo := range []repository.HostedRepository{
		{ID: "source", Name: "source", Format: repository.FormatOCI},
		{ID: "target", Name: "target", Format: repository.FormatOCI},
		{ID: "other", Name: "other", Format: repository.FormatOCI},
	} {
		if _, err := store.CreateHostedRepository(context.Background(), repo); err != nil {
			t.Fatal(err)
		}
	}
	handler := NewGatewayHandler(Dependencies{NativeOCIObjectStore: NewMemoryOCIObjectStore()}, store, TestAdapter{}, testAuthenticator())
	digest := uploadNativeOCIBlob(t, handler, "/v2/source/app", []byte("source-only"))

	mount := httptest.NewRequest(http.MethodPost, "/v2/target/app/blobs/uploads/?mount="+digest+"&from=source/app", nil)
	authorize(mount, "resolver-secret")
	mounted := httptest.NewRecorder()
	handler.ServeHTTP(mounted, mount)
	if mounted.Code != http.StatusCreated {
		t.Fatalf("valid mount=%d %s", mounted.Code, mounted.Body.String())
	}

	wrongSource := httptest.NewRequest(http.MethodPost, "/v2/other/app/blobs/uploads/?mount="+digest+"&from=target-missing/app", nil)
	authorize(wrongSource, "resolver-secret")
	fallback := httptest.NewRecorder()
	handler.ServeHTTP(fallback, wrongSource)
	if fallback.Code != http.StatusAccepted {
		t.Fatalf("mount with unrelated source=%d %s", fallback.Code, fallback.Body.String())
	}
	read := httptest.NewRequest(http.MethodGet, "/v2/other/app/blobs/"+digest, nil)
	authorize(read, "resolver-secret")
	missing := httptest.NewRecorder()
	handler.ServeHTTP(missing, read)
	if missing.Code != http.StatusNotFound {
		t.Fatalf("unrelated mount made blob visible: %d %s", missing.Code, missing.Body.String())
	}
}

func uploadNativeOCIBlob(t *testing.T, handler http.Handler, prefix string, body []byte) string {
	t.Helper()
	start := httptest.NewRequest(http.MethodPost, prefix+"/blobs/uploads/", nil)
	authorize(start, "resolver-secret")
	started := httptest.NewRecorder()
	handler.ServeHTTP(started, start)
	if started.Code != http.StatusAccepted {
		t.Fatalf("start upload=%d %s", started.Code, started.Body.String())
	}
	sum := sha256.Sum256(body)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	complete := httptest.NewRequest(http.MethodPut, started.Header().Get("Location")+"?digest="+digest, bytes.NewReader(body))
	authorize(complete, "resolver-secret")
	completed := httptest.NewRecorder()
	handler.ServeHTTP(completed, complete)
	if completed.Code != http.StatusCreated {
		t.Fatalf("complete upload=%d %s", completed.Code, completed.Body.String())
	}
	return digest
}
