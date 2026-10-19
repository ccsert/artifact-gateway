package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/ccsert/artifact-gateway/internal/objectstore"
	"github.com/ccsert/artifact-gateway/internal/repository"
)

func assertRawMultipart(t *testing.T, status int, headers http.Header, body []byte, parts, contentRanges []string) {
	t.Helper()
	typ, params, err := mime.ParseMediaType(headers.Get("Content-Type"))
	if status != 206 || err != nil || typ != "multipart/byteranges" || headers.Get("Content-Length") != strconv.Itoa(len(body)) || headers.Get("Content-Range") != "" {
		t.Fatalf("multipart status=%d headers=%v length=%d error=%v", status, headers, len(body), err)
	}
	r := multipart.NewReader(bytes.NewReader(body), params["boundary"])
	for i, want := range parts {
		part, err := r.NextPart()
		if err != nil {
			t.Fatalf("part %d: %v", i, err)
		}
		got, err := io.ReadAll(part)
		if err != nil || string(got) != want || part.Header.Get("Content-Range") != contentRanges[i] {
			t.Fatalf("part %d bytes=%q header=%v error=%v", i, got, part.Header, err)
		}
	}
	if _, err := r.NextPart(); err != io.EOF {
		t.Fatalf("extra multipart data: %v", err)
	}
}

func TestNativeRawMultipartPinsImmutableObjectAcrossPathReplacement(t *testing.T) {
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: "raw-multipart", Name: "downloads", Format: repository.FormatRaw})
	if err != nil {
		t.Fatal(err)
	}
	objects := &movingRawObjectStore{MemoryStore: objectstore.NewMemoryStore()}
	handler := newTestGateway(t, GatewayOptions{Store: store, Adapter: TestAdapter{}, Authenticator: testAuthenticator(), Storage: ObjectStores{repository.FormatRaw: objects, repository.FormatOCI: objectstore.NewMemoryStore()}})
	createV2Group(t, store, "all-downloads", repository.FormatRaw, repository.GroupMember{RepositoryID: repo.ID, Position: 0})
	request := func(method, target, body string, headers map[string]string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, target, strings.NewReader(body))
		authorize(r, "admin-secret")
		for key, value := range headers {
			r.Header.Set(key, value)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	const old = "0123456789abcdef"
	const path = "/raw/downloads/file"
	if w := request(http.MethodPut, path, old, nil); w.Code != 201 {
		t.Fatalf("publish=%d %s", w.Code, w.Body.String())
	}
	oldETag := request(http.MethodHead, path, "", nil).Header().Get("ETag")
	objects.onFirstRange = func() {
		const replacement = "new-immutable-file"
		sum := sha256.Sum256([]byte(replacement))
		asset := repository.RawAsset{RepositoryID: repo.ID, Path: "file", Digest: fmt.Sprintf("sha256:%x", sum), ObjectKey: fmt.Sprintf("native/raw/sha256/%x", sum), Size: int64(len(replacement)), ContentType: "text/plain"}
		if err := objects.Put(context.Background(), asset.ObjectKey, []byte(replacement)); err != nil {
			t.Fatal(err)
		}
		if _, err := store.PutRawAsset(context.Background(), asset); err != nil {
			t.Fatal(err)
		}
	}
	w := request(http.MethodGet, "/raw/all-downloads/file", "", map[string]string{"Range": "bytes=0-2,12-15", "If-Range": oldETag})
	assertRawMultipart(t, w.Code, w.Header(), w.Body.Bytes(), []string{"012", "cdef"}, []string{"bytes 0-2/16", "bytes 12-15/16"})
	if w.Header().Get("ETag") != oldETag || len(objects.keys) != 2 || objects.keys[0] != objects.keys[1] {
		t.Fatalf("changed representation etag=%s keys=%v", w.Header().Get("ETag"), objects.keys)
	}
	if w := request(http.MethodGet, path, "", nil); w.Code != 200 || w.Body.String() != "new-immutable-file" {
		t.Fatalf("path was not replaced during response=%d %q", w.Code, w.Body.String())
	}
	// The same native contract applies through the direct Hosted root.
	w = request(http.MethodGet, path, "", map[string]string{"Range": "bytes=0-2,14-17"})
	assertRawMultipart(t, w.Code, w.Header(), w.Body.Bytes(), []string{"new", "file"}, []string{"bytes 0-2/18", "bytes 14-17/18"})
}

type movingRawObjectStore struct {
	*objectstore.MemoryStore
	keys         []string
	onFirstRange func()
}

func (s *movingRawObjectStore) OpenRange(ctx context.Context, key string, offset, length int64) (io.ReadCloser, int64, error) {
	s.keys = append(s.keys, key)
	reader, size, err := s.MemoryStore.OpenRange(ctx, key, offset, length)
	if len(s.keys) == 1 && s.onFirstRange != nil {
		s.onFirstRange()
	}
	return reader, size, err
}

func TestV2RawProxyAndGroupMultipartColdAndCached(t *testing.T) {
	handler, _, client, _ := rawProxyGroupFixture(t, http.StatusNotFound, http.StatusOK)
	for _, root := range []string{"raw-chain", "second", "raw-chain"} {
		r := rawRequest(http.MethodGet, "/raw/"+root+"/file")
		r.Header.Set("Range", "bytes=0-3,12-16")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		assertRawMultipart(t, w.Code, w.Header(), w.Body.Bytes(), []string{"from", "proxy"}, []string{"bytes 0-3/17", "bytes 12-16/17"})
	}
	if calls := client.Calls(); len(calls) != 2 {
		t.Fatalf("ranges bypassed verified complete cache: %v", calls)
	}
	r := httptest.NewRequest(http.MethodGet, "/raw/raw-chain/file", nil)
	r.Header.Set("Range", "bytes=0-3,12-16")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 401 || len(client.Calls()) != 2 || strings.Contains(w.Body.String(), "from-second-proxy") {
		t.Fatalf("unauthorized cached response=%d %s calls=%v", w.Code, w.Body.String(), client.Calls())
	}
}
