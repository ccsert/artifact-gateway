package nexussource

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestSourceContentUsesExplicitNativeFormatRoutes(t *testing.T) {
	revision := strings.Repeat("a", 32)
	conanPath := "conans/widget/1.0/_/_/revisions/" + revision + "/files/conanfile.py"
	for _, tc := range []struct {
		format, storage, wire string
	}{
		{"maven", "org/example/a/1/a-1.jar", "org/example/a/1/a-1.jar"},
		{"raw", "build+1.bin", "build+1.bin"},
		{"oci", "v2/team/image/manifests/latest", "v2/team/image/manifests/latest"},
		{"conan", conanPath, "v2/" + conanPath},
		{"", "build.bin", ""},
		{"future", "build.bin", ""},
		{"conan", "widget/1.0/conanfile.py", ""},
		{"oci", "v2/team/image/tags/list", ""},
		{"oci", "/v2/team/image/manifests/latest", ""},
	} {
		t.Run(tc.format+"/"+tc.storage, func(t *testing.T) {
			var requests atomic.Int32
			service, id, _ := fixtureService(t, func(w http.ResponseWriter, r *http.Request) bool {
				if !strings.HasPrefix(r.URL.Path, "/repository/raw-a/") {
					return false
				}
				requests.Add(1)
				if r.URL.Path != "/repository/raw-a/"+tc.wire {
					t.Errorf("unexpected source route %q", r.URL.Path)
				}
				if tc.format == "oci" {
					for _, mediaType := range []string{"application/vnd.oci.image.index.v1+json", "application/vnd.docker.distribution.manifest.list.v2+json"} {
						if !strings.Contains(r.Header.Get("Accept"), mediaType) {
							t.Errorf("all-platform media type %q missing from source request", mediaType)
						}
					}
					w.Header().Set("Content-Type", "application/vnd.oci.image.index.v1+json; charset=utf-8")
				}
				_, _ = io.WriteString(w, "data")
				return true
			})
			content, err := service.OpenAsset(t.Context(), id, "1", "raw-a", AssetRequest{Format: tc.format, Path: tc.storage, Size: 4})
			if tc.wire == "" {
				requireCode(t, err, "invalid_request")
				if requests.Load() != 0 {
					t.Fatal("invalid native route reached the source")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = content.Body.Close() }()
			body, err := io.ReadAll(content.Body)
			if err != nil || string(body) != "data" || requests.Load() != 1 {
				t.Fatalf("source body or request count mismatch: body=%q requests=%d err=%v", body, requests.Load(), err)
			}
			if tc.format == "oci" && content.MediaType != "application/vnd.oci.image.index.v1+json" {
				t.Fatalf("unexpected normalized source media type %q", content.MediaType)
			}
		})
	}
}

func TestSourceContentPinsIdentityBoundsBytesAndValidators(t *testing.T) {
	for _, test := range []struct {
		name, path, version, validator, body, etag string
		status                                     int
		size                                       int64
		wantCode                                   string
		wantBytes                                  int
	}{
		{name: "encoded plus", path: "a+build.bin", version: "1", body: "data", etag: `"v1"`, size: 4, wantBytes: 4},
		{name: "changed source version", path: "a.bin", version: "2", body: "data", size: 4, wantCode: "source_version_changed"},
		{name: "traversal", path: "../a.bin", version: "1", body: "data", size: 4, wantCode: "invalid_request"},
		{name: "double escape", path: "%252e%252e/a.bin", version: "1", body: "data", size: 4, wantCode: "invalid_request"},
		{name: "validator changed", path: "a.bin", version: "1", validator: `etag:"v1"`, body: "data", etag: `"v2"`, size: 4, wantCode: "source_changed"},
		{name: "source missing", path: "a.bin", version: "1", status: 404, size: 4, wantCode: "source_missing"},
		{name: "redirect denied", path: "a.bin", version: "1", status: 302, size: 4, wantCode: "source_download_failed"},
		{name: "unknown length extra byte bounded", path: "a.bin", version: "1", body: strings.Repeat("x", 100), size: 4, wantBytes: 5},
	} {
		t.Run(test.name, func(t *testing.T) {
			service, id, _ := fixtureService(t, func(w http.ResponseWriter, r *http.Request) bool {
				if !strings.HasPrefix(r.URL.Path, "/repository/raw-a/") {
					return false
				}
				if strings.Contains(r.URL.Path, "/../") {
					t.Fatal("unsafe path reached source")
				}
				if test.validator != "" && r.Header.Get("If-Match") != strings.TrimPrefix(test.validator, "etag:") {
					t.Error("validator condition missing")
				}
				if test.etag != "" {
					w.Header().Set("ETag", test.etag)
				}
				if test.status != 0 {
					w.WriteHeader(test.status)
					return true
				}
				w.(http.Flusher).Flush() // Deliberately omit Content-Length.
				_, _ = io.WriteString(w, test.body)
				return true
			})
			content, err := service.OpenAsset(context.Background(), id, test.version, "raw-a", AssetRequest{Format: "raw", Path: test.path, Size: test.size, Validator: test.validator})
			if test.wantCode != "" {
				requireCode(t, err, test.wantCode)
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			data, err := io.ReadAll(content.Body)
			_ = content.Body.Close()
			if err != nil || len(data) != test.wantBytes {
				t.Fatalf("read=%d err=%v", len(data), err)
			}
		})
	}
}
func TestSourceContentSlowBodyHonorsBoundedDeadline(t *testing.T) {
	service, id, _ := fixtureService(t, func(w http.ResponseWriter, r *http.Request) bool {
		if !strings.HasPrefix(r.URL.Path, "/repository/raw-a/") {
			return false
		}
		_, _ = io.WriteString(w, "a")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		return true
	})
	// Leave room for DNS pinning and a real TLS handshake under the race
	// detector; the received prefix proves the timeout occurs in the body.
	service.options.OperationTimeout = time.Second
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	started := time.Now()
	content, err := service.OpenAsset(ctx, id, "1", "raw-a", AssetRequest{Format: "raw", Path: "slow.bin", Size: 4})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = content.Body.Close() }()
	body, err := io.ReadAll(content.Body)
	if string(body) != "a" {
		t.Fatalf("slow-body fixture must deliver its prefix before timing out: %q", body)
	}
	requireCode(t, err, "source_timeout")
	if time.Since(started) >= 4*time.Second {
		t.Fatal("body read exhausted the outer context instead of its operation budget")
	}
}

func TestSourceContentDelayedHeadersReturnBoundedTimeout(t *testing.T) {
	service, id, _ := fixtureService(t, func(_ http.ResponseWriter, r *http.Request) bool {
		if !strings.HasPrefix(r.URL.Path, "/repository/raw-a/") {
			return false
		}
		<-r.Context().Done()
		return true
	})
	service.options.OperationTimeout = 25 * time.Millisecond
	_, err := service.OpenAsset(context.Background(), id, "1", "raw-a", AssetRequest{Format: "raw", Path: "slow.bin", Size: 4})
	requireCode(t, err, "source_timeout")
}
