package nexussource

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

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
			content, err := service.OpenAsset(context.Background(), id, test.version, "raw-a", AssetRequest{Path: test.path, Size: test.size, Validator: test.validator})
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
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		return true
	})
	service.options.OperationTimeout = 25 * time.Millisecond
	content, err := service.OpenAsset(context.Background(), id, "1", "raw-a", AssetRequest{Path: "slow.bin", Size: 4})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = content.Body.Close() }()
	if _, err = io.ReadAll(content.Body); err == nil {
		t.Fatal("slow body exceeded source request deadline")
	}
}
