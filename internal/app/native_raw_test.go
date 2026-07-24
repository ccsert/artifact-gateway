package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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

func TestNativeRawCollectorTracksAndCollectsUnreferencedObject(t *testing.T) {
	store := repository.NewMemoryStore()
	objects := NewMemoryOCIObjectStore()
	digest := "sha256:" + strings.Repeat("a", 64)
	key := "native/raw/sha256/" + strings.Repeat("a", 64)
	if err := store.StageRawObject(context.Background(), repository.RawObject{Digest: digest, ObjectKey: key, Size: 6}); err != nil {
		t.Fatal(err)
	}
	if err := objects.Put(context.Background(), key, []byte("orphan")); err != nil {
		t.Fatal(err)
	}
	if err := (NativeRawMaintenance{Store: store, Objects: objects, Now: func() time.Time { return time.Now().UTC().Add(25 * time.Hour) }}).Collect(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := objects.Get(context.Background(), key); !errors.Is(err, errOCICacheMiss) {
		t.Fatalf("orphan object err=%v", err)
	}
	candidates, err := store.ListUnreferencedRawObjects(context.Background(), time.Now().UTC().Add(48*time.Hour), 10)
	if err != nil || len(candidates) != 0 {
		t.Fatalf("remaining raw collection candidates=%#v err=%v", candidates, err)
	}
}
