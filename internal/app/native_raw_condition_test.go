package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/ccsert/artifact-gateway/internal/repository"
)

func newConditionalRawHandler(t *testing.T) (*repository.MemoryStore, func(method, path string, body []byte, headers map[string]string) *httptest.ResponseRecorder) {
	t.Helper()
	store := repository.NewMemoryStore()
	if _, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: "raw-repo", Name: "downloads", Format: repository.FormatRaw}); err != nil {
		t.Fatal(err)
	}
	handler := NewGatewayHandler(Dependencies{NativeOCIObjectStore: NewMemoryOCIObjectStore()}, store, TestAdapter{}, testAuthenticator())
	return store, func(method, path string, body []byte, headers map[string]string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, bytes.NewReader(body))
		for name, value := range headers {
			r.Header.Set(name, value)
		}
		authorize(r, "resolver-secret")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
}

func rawReadETag(body string) string {
	sum := sha256.Sum256([]byte(body))
	return `"sha256-` + hex.EncodeToString(sum[:]) + `"`
}

func TestNativeRawConditionalPutCreateOnly(t *testing.T) {
	_, request := newConditionalRawHandler(t)
	const path = "/raw/downloads/releases/app.txt"
	created := request(http.MethodPut, path, []byte("first"), map[string]string{"If-None-Match": "*"})
	if created.Code != http.StatusCreated {
		t.Fatalf("create-only on absent path=%d %s", created.Code, created.Body.String())
	}
	for _, body := range []string{"second", "first"} {
		repeated := request(http.MethodPut, path, []byte(body), map[string]string{"If-None-Match": "*"})
		if repeated.Code != http.StatusPreconditionFailed || repeated.Header().Get("ETag") != "" {
			t.Fatalf("create-only %q over existing path=%d etag=%q", body, repeated.Code, repeated.Header().Get("ETag"))
		}
	}
	read := request(http.MethodGet, path, nil, nil)
	if read.Code != http.StatusOK || read.Body.String() != "first" {
		t.Fatalf("read after rejected writes=%d %q", read.Code, read.Body.String())
	}
}

func TestNativeRawConditionalPutIfMatch(t *testing.T) {
	_, request := newConditionalRawHandler(t)
	const path = "/raw/downloads/releases/app.txt"
	if missing := request(http.MethodPut, path, []byte("v1"), map[string]string{"If-Match": "*"}); missing.Code != http.StatusPreconditionFailed {
		t.Fatalf("If-Match * on absent path=%d", missing.Code)
	}
	if missing := request(http.MethodPut, path, []byte("v1"), map[string]string{"If-Match": rawReadETag("v0")}); missing.Code != http.StatusPreconditionFailed {
		t.Fatalf("If-Match tag on absent path=%d", missing.Code)
	}
	if created := request(http.MethodPut, path, []byte("v1"), nil); created.Code != http.StatusCreated {
		t.Fatalf("unconditional put=%d", created.Code)
	}
	readETag := request(http.MethodHead, path, nil, nil).Header().Get("ETag")
	if readETag != rawReadETag("v1") {
		t.Fatalf("read ETag=%q want %q", readETag, rawReadETag("v1"))
	}
	if weak := request(http.MethodPut, path, []byte("v2"), map[string]string{"If-Match": "W/" + readETag}); weak.Code != http.StatusPreconditionFailed {
		t.Fatalf("weak If-Match=%d", weak.Code)
	}
	updated := request(http.MethodPut, path, []byte("v2"), map[string]string{"If-Match": `"other", ` + readETag})
	if updated.Code != http.StatusCreated {
		t.Fatalf("If-Match current read ETag=%d %s", updated.Code, updated.Body.String())
	}
	if stale := request(http.MethodPut, path, []byte("v3"), map[string]string{"If-Match": readETag}); stale.Code != http.StatusPreconditionFailed {
		t.Fatalf("stale If-Match=%d", stale.Code)
	}
	// The write response tag identifies the same digest as the read tag.
	writeETag := updated.Header().Get("ETag")
	if again := request(http.MethodPut, path, []byte("v3"), map[string]string{"If-Match": writeETag}); again.Code != http.StatusCreated {
		t.Fatalf("If-Match write ETag %q=%d", writeETag, again.Code)
	}
	if wildcard := request(http.MethodPut, path, []byte("v4"), map[string]string{"If-Match": "*"}); wildcard.Code != http.StatusCreated {
		t.Fatalf("If-Match * on existing path=%d", wildcard.Code)
	}
	if noneMatch := request(http.MethodPut, path, []byte("v5"), map[string]string{"If-None-Match": "W/" + rawReadETag("v4")}); noneMatch.Code != http.StatusPreconditionFailed {
		t.Fatalf("If-None-Match current tag=%d", noneMatch.Code)
	}
	if read := request(http.MethodGet, path, nil, nil); read.Body.String() != "v4" {
		t.Fatalf("final content=%q", read.Body.String())
	}
}

func TestNativeRawConditionalPutRejectsMalformedPreconditions(t *testing.T) {
	_, request := newConditionalRawHandler(t)
	const path = "/raw/downloads/releases/app.txt"
	for name, headers := range map[string]map[string]string{
		"unquoted tag":        {"If-Match": "sha256-abc"},
		"wildcard with tags":  {"If-None-Match": `*, "a"`},
		"empty list":          {"If-Match": " , "},
		"space inside tag":    {"If-Match": `"a b"`},
		"unsupported date":    {"If-Unmodified-Since": "Wed, 21 Oct 2015 07:28:00 GMT"},
		"date beside a match": {"If-Match": "*", "If-Unmodified-Since": "Wed, 21 Oct 2015 07:28:00 GMT"},
	} {
		if response := request(http.MethodPut, path, []byte("body"), headers); response.Code != http.StatusBadRequest {
			t.Errorf("%s: status=%d", name, response.Code)
		}
	}
	if read := request(http.MethodGet, path, nil, nil); read.Code != http.StatusNotFound {
		t.Fatalf("rejected preconditions wrote the path: %d", read.Code)
	}
}

func TestNativeRawConditionalPutEntityTagLists(t *testing.T) {
	const path = "/raw/downloads/releases/app.txt"
	for _, tc := range []struct {
		name    string
		headers http.Header
		want    int
	}{
		{"quoted comma beside a match", http.Header{"If-Match": {`"other,tag", ` + rawReadETag("base")}}, http.StatusCreated},
		{"quoted comma without a match", http.Header{"If-Match": {`"other,tag"`}}, http.StatusPreconditionFailed},
		{"quoted comma in none-match", http.Header{"If-None-Match": {`"other,tag"`}}, http.StatusCreated},
		{"obs-text beside a match", http.Header{"If-Match": {"\"other\x80\xfftag\", " + rawReadETag("base")}}, http.StatusCreated},
		{"obs-text without a match", http.Header{"If-Match": {"\"other\x80\xfftag\""}}, http.StatusPreconditionFailed},
		{"obs-text in none-match", http.Header{"If-None-Match": {"W/\"other\x80\xfftag\""}}, http.StatusCreated},
		{"multiple field lines", http.Header{"If-Match": {`"other,tag"`, rawReadETag("base")}}, http.StatusCreated},
		{"empty list members", http.Header{"If-Match": {", , " + rawReadETag("base") + ", "}}, http.StatusCreated},
		{"wildcard with whitespace", http.Header{"If-Match": {" \t*\t "}}, http.StatusCreated},
		{"duplicate wildcard", http.Header{"If-Match": {"*, *"}}, http.StatusBadRequest},
		{"duplicate wildcard field lines", http.Header{"If-None-Match": {"*", "*"}}, http.StatusBadRequest},
		{"wildcard with empty member", http.Header{"If-Match": {"*,"}}, http.StatusBadRequest},
		{"wildcard mixed with a tag", http.Header{"If-Match": {"*", rawReadETag("base")}}, http.StatusBadRequest},
		{"missing delimiter", http.Header{"If-Match": {rawReadETag("base") + ` "other"`}}, http.StatusBadRequest},
		{"unterminated tag", http.Header{"If-Match": {`"other,tag`}}, http.StatusBadRequest},
		{"control in opaque tag", http.Header{"If-Match": {"\"other\x1ftag\""}}, http.StatusBadRequest},
		{"delete in opaque tag", http.Header{"If-Match": {"\"other\x7ftag\""}}, http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := repository.NewMemoryStore()
			if _, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: "raw-repo", Name: "downloads", Format: repository.FormatRaw}); err != nil {
				t.Fatal(err)
			}
			handler := NewGatewayHandler(Dependencies{NativeOCIObjectStore: NewMemoryOCIObjectStore()}, store, TestAdapter{}, testAuthenticator())
			request := func(method, body string, headers http.Header) *httptest.ResponseRecorder {
				r := httptest.NewRequest(method, path, strings.NewReader(body))
				for name, values := range headers {
					for _, value := range values {
						r.Header.Add(name, value)
					}
				}
				authorize(r, "resolver-secret")
				w := httptest.NewRecorder()
				handler.ServeHTTP(w, r)
				return w
			}
			if seeded := request(http.MethodPut, "base", nil); seeded.Code != http.StatusCreated {
				t.Fatalf("seed=%d %s", seeded.Code, seeded.Body.String())
			}
			response := request(http.MethodPut, "next", tc.headers)
			if response.Code != tc.want {
				t.Fatalf("conditional PUT=%d %s, want %d", response.Code, response.Body.String(), tc.want)
			}
			wantBody := "base"
			if tc.want == http.StatusCreated {
				wantBody = "next"
			}
			if read := request(http.MethodGet, "", nil); read.Code != http.StatusOK || read.Body.String() != wantBody {
				t.Fatalf("read after conditional PUT=%d %q, want %q", read.Code, read.Body.String(), wantBody)
			}
		})
	}
}

func TestNativeRawConditionalPutAdmitsOneWriterPerValidator(t *testing.T) {
	_, request := newConditionalRawHandler(t)
	const path = "/raw/downloads/releases/app.txt"
	if created := request(http.MethodPut, path, []byte("base"), nil); created.Code != http.StatusCreated {
		t.Fatalf("seed=%d", created.Code)
	}
	validator := rawReadETag("base")
	const writers = 16
	statuses := make([]int, writers)
	var wg sync.WaitGroup
	for i := range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			statuses[i] = request(http.MethodPut, path, []byte(fmt.Sprintf("writer-%d", i)), map[string]string{"If-Match": validator}).Code
		}()
	}
	wg.Wait()
	winner := -1
	for i, status := range statuses {
		switch status {
		case http.StatusCreated:
			if winner >= 0 {
				t.Fatalf("writers %d and %d both succeeded with the same validator", winner, i)
			}
			winner = i
		case http.StatusPreconditionFailed:
		default:
			t.Fatalf("writer %d status=%d", i, status)
		}
	}
	if winner < 0 {
		t.Fatal("no writer succeeded")
	}
	if read := request(http.MethodGet, path, nil, nil); read.Body.String() != fmt.Sprintf("writer-%d", winner) {
		t.Fatalf("final content=%q winner=%d", read.Body.String(), winner)
	}
}

func TestNativeRawStoreEvaluatesConditionAtomically(t *testing.T) {
	store := repository.NewMemoryStore()
	ctx := context.Background()
	seed := repository.RawAsset{RepositoryID: "raw-repo", Path: "a.txt", Digest: "sha256:" + strings.Repeat("0", 64), ObjectKey: "k0", Size: 1}
	if _, err := store.PutRawAsset(ctx, seed); err != nil {
		t.Fatal(err)
	}
	condition := repository.RawWriteCondition{MatchDigests: []string{seed.Digest}}
	const writers = 16
	errs := make([]error, writers)
	var wg sync.WaitGroup
	for i := range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = store.PutRawAssetIf(ctx, repository.RawAsset{RepositoryID: "raw-repo", Path: "a.txt", Digest: fmt.Sprintf("sha256:%064d", i+1), ObjectKey: fmt.Sprint("k", i+1), Size: 1}, condition)
		}()
	}
	wg.Wait()
	succeeded := 0
	for i, err := range errs {
		switch {
		case err == nil:
			succeeded++
		case err != repository.ErrPreconditionFailed:
			t.Fatalf("writer %d: %v", i, err)
		}
	}
	if succeeded != 1 {
		t.Fatalf("succeeded=%d want 1", succeeded)
	}
}

func TestNativeRawResumableCompleteChecksCondition(t *testing.T) {
	_, request := newConditionalRawHandler(t)
	const path = "/raw/downloads/releases/app.bin"
	if created := request(http.MethodPut, path, []byte("old"), nil); created.Code != http.StatusCreated {
		t.Fatalf("seed=%d", created.Code)
	}
	validator := rawReadETag("old")
	started := request(http.MethodPost, path+"?resumable=1", nil, nil)
	location := started.Header().Get("Location")
	if started.Code != http.StatusCreated || location == "" {
		t.Fatalf("start=%d headers=%v", started.Code, started.Header())
	}
	if patched := request(http.MethodPatch, location, []byte("resumed"), map[string]string{"Upload-Offset": "0"}); patched.Code != http.StatusNoContent {
		t.Fatalf("patch=%d", patched.Code)
	}
	// Another writer moves the path while the upload is open.
	if moved := request(http.MethodPut, path, []byte("concurrent"), map[string]string{"If-Match": validator}); moved.Code != http.StatusCreated {
		t.Fatalf("concurrent writer=%d", moved.Code)
	}
	sum := sha256.Sum256([]byte("resumed"))
	digest := "sha-256=" + base64.StdEncoding.EncodeToString(sum[:])
	stale := request(http.MethodPut, location+"&complete=1", nil, map[string]string{"Digest": digest, "If-Match": validator})
	if stale.Code != http.StatusPreconditionFailed {
		t.Fatalf("stale complete=%d %s", stale.Code, stale.Body.String())
	}
	if read := request(http.MethodGet, path, nil, nil); read.Body.String() != "concurrent" {
		t.Fatalf("content after rejected complete=%q", read.Body.String())
	}
	if status := request(http.MethodGet, location, nil, nil); status.Code != http.StatusNoContent || status.Header().Get("Upload-Offset") != "7" {
		t.Fatalf("upload after rejected complete=%d headers=%v", status.Code, status.Header())
	}
	completed := request(http.MethodPut, location+"&complete=1", nil, map[string]string{"Digest": digest, "If-Match": rawReadETag("concurrent")})
	if completed.Code != http.StatusCreated {
		t.Fatalf("current complete=%d %s", completed.Code, completed.Body.String())
	}
	if read := request(http.MethodGet, path, nil, nil); read.Body.String() != "resumed" {
		t.Fatalf("content after complete=%q", read.Body.String())
	}
}
