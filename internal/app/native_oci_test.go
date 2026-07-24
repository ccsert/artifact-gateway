package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
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
	if !((first == http.StatusAccepted && second == http.StatusRequestedRangeNotSatisfiable) || (second == http.StatusAccepted && first == http.StatusRequestedRangeNotSatisfiable)) {
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
