package agctl

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ccsert/artifact-gateway/internal/app"
	"github.com/ccsert/artifact-gateway/internal/apptest"
	"github.com/ccsert/artifact-gateway/internal/repository"
)

func TestPublishRawResumableUsesRealProtocolAndReconcilesLostPatchResponse(t *testing.T) {
	isolatedCLI(t)
	store := repository.NewMemoryStore()
	if _, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: testRepoID, Name: "downloads", Format: repository.FormatRaw}); err != nil {
		t.Fatal(err)
	}
	handler := apptest.NewGateway(t, app.GatewayOptions{Store: store, NPMMetadata: store, NPMRestore: store, Storage: apptest.MemoryObjectStores(nil), Adapter: app.TestAdapter{}, Authenticator: app.Authenticator{AdminToken: repoTestToken}}, app.NewGatewayHandler)
	var mu sync.Mutex
	posts, patches, completes := 0, 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Method == http.MethodPost && r.URL.Query().Get("resumable") == "1" {
			posts++
		}
		if r.Method == http.MethodPatch {
			patches++
			if patches == 1 {
				// The server commits the offset before the connection drops.
				handler.ServeHTTP(httptest.NewRecorder(), r)
				conn, _, err := w.(http.Hijacker).Hijack()
				if err != nil {
					t.Error(err)
					return
				}
				_ = conn.Close()
				return
			}
		}
		if r.Method == http.MethodPut && r.URL.Query().Get("complete") == "1" {
			completes++
		}
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	t.Setenv("AGCTL_SERVER", server.URL)
	t.Setenv("AGCTL_TOKEN", repoTestToken)
	dir := writeTree(t, map[string]string{"file.txt": "abcdefghi"})
	code, stdout, stderr := runCLI(t, "publish", "raw", filepath.Join(dir, "file.txt"), "--repo", testRepoID, "--to", "release/file with space.txt", "--resumable", "--chunk-size", "3", "--json")
	if code != ExitOK {
		t.Fatalf("exit=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	mu.Lock()
	if posts != 1 || patches != 3 || completes != 1 {
		t.Fatalf("unsafe replay: posts=%d patches=%d commits=%d", posts, patches, completes)
	}
	mu.Unlock()
	var results struct {
		Items []struct {
			UploadID string `json:"uploadId"`
			Offset   int64  `json:"uploadOffset"`
			Status   string `json:"status"`
		} `json:"items"`
	}
	if err := json.Unmarshal(decodeEnvelope(t, stdout).Data, &results); err != nil || len(results.Items) != 1 || results.Items[0].UploadID == "" || results.Items[0].Offset != 9 || results.Items[0].Status != "uploaded" {
		t.Fatalf("resume receipt=%+v error=%v", results, err)
	}
	request := httptest.NewRequest(http.MethodGet, "/raw/downloads/release/file%20with%20space.txt", nil)
	request.Header.Set("Authorization", "Bearer "+repoTestToken)
	read := httptest.NewRecorder()
	handler.ServeHTTP(read, request)
	if read.Code != 200 || read.Body.String() != "abcdefghi" {
		t.Fatalf("published body=%d %q", read.Code, read.Body.String())
	}
}

func TestPublishRawResumableRejectsInvalidFlagsWithoutRequests(t *testing.T) {
	gateway := newRawGateway(t)
	dir := writeTree(t, map[string]string{"file.txt": "abc", "other.txt": "def"})
	for _, args := range [][]string{
		{"--upload-id", "invalid"},
		{"--resumable", "--chunk-size", "0"},
		{"--resumable", "--chunk-size", "16777217"},
		{"--upload-id", "00000000-0000-4000-8000-000000000001"},
	} {
		code, stdout, stderr := runCLI(t, append([]string{"publish", "raw", dir, "--repo", testRepoID, "--json"}, args...)...)
		if code != ExitUsage {
			t.Fatalf("args=%v exit=%d stdout=%s stderr=%s", args, code, stdout, stderr)
		}
	}
	if gateway.calls != 0 {
		t.Fatalf("invalid flags sent %d requests", gateway.calls)
	}
}

func TestPublishRawResumableDirectoryAndChangedSource(t *testing.T) {
	for _, changed := range []bool{false, true} {
		t.Run(map[bool]string{false: "directory-with-empty-file", true: "changed-source"}[changed], func(t *testing.T) {
			isolatedCLI(t)
			store := repository.NewMemoryStore()
			if _, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: testRepoID, Name: "downloads", Format: repository.FormatRaw}); err != nil {
				t.Fatal(err)
			}
			handler := apptest.NewGateway(t, app.GatewayOptions{Store: store, NPMMetadata: store, NPMRestore: store, Storage: apptest.MemoryObjectStores(nil), Adapter: app.TestAdapter{}, Authenticator: app.Authenticator{AdminToken: repoTestToken}}, app.NewGatewayHandler)
			dir := writeTree(t, map[string]string{"empty": "", "file.txt": "abcdef"})
			file := filepath.Join(dir, "file.txt")
			var once sync.Once
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				handler.ServeHTTP(w, r)
				if changed && r.Method == http.MethodPatch {
					once.Do(func() {
						if err := os.WriteFile(file, []byte("abcXYZ"), 0o600); err != nil {
							t.Error(err)
						}
					})
				}
			}))
			t.Cleanup(server.Close)
			t.Setenv("AGCTL_SERVER", server.URL)
			t.Setenv("AGCTL_TOKEN", repoTestToken)
			source := dir
			if changed {
				source = file
			}
			code, stdout, stderr := runCLI(t, "publish", "raw", source, "--repo", testRepoID, "--resumable", "--chunk-size", "3", "--json")
			want := ExitOK
			if changed {
				want = ExitPartial
			}
			if code != want {
				t.Fatalf("exit=%d stdout=%s stderr=%s", code, stdout, stderr)
			}
			var results struct {
				Items []objectResult `json:"items"`
			}
			if err := json.Unmarshal(decodeEnvelope(t, stdout).Data, &results); err != nil {
				t.Fatal(err)
			}
			for _, result := range results.Items {
				request := httptest.NewRequest(http.MethodGet, "/raw/downloads/"+result.Coordinate, nil)
				request.Header.Set("Authorization", "Bearer "+repoTestToken)
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, request)
				if changed {
					if result.Status != "failed" || result.UploadID == "" || response.Code != http.StatusNotFound {
						t.Fatalf("changed source exposed: result=%+v read=%d %s", result, response.Code, response.Body.String())
					}
				} else if response.Code != http.StatusOK || int64(response.Body.Len()) != result.Size || normalizeRawETag(response.Header().Get("ETag")) != result.Digest {
					t.Fatalf("directory mismatch: result=%+v read=%d %s", result, response.Code, response.Body.String())
				}
			}
		})
	}
}

func TestPublishRawResumableSharesDeadlineAcrossChunks(t *testing.T) {
	isolatedCLI(t)
	store := repository.NewMemoryStore()
	if _, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: testRepoID, Name: "downloads", Format: repository.FormatRaw}); err != nil {
		t.Fatal(err)
	}
	handler := apptest.NewGateway(t, app.GatewayOptions{Store: store, NPMMetadata: store, NPMRestore: store, Storage: apptest.MemoryObjectStores(nil), Adapter: app.TestAdapter{}, Authenticator: app.Authenticator{AdminToken: repoTestToken}}, app.NewGatewayHandler)
	var mu sync.Mutex
	patches, commits := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPatch {
			mu.Lock()
			patches++
			mu.Unlock()
			select {
			case <-r.Context().Done():
				return
			case <-time.After(60 * time.Millisecond):
			}
		}
		if r.Method == http.MethodPut {
			mu.Lock()
			commits++
			mu.Unlock()
		}
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	t.Setenv("AGCTL_SERVER", server.URL)
	t.Setenv("AGCTL_TOKEN", repoTestToken)
	file := filepath.Join(writeTree(t, map[string]string{"file.txt": strings.Repeat("abc", 10)}), "file.txt")
	started := time.Now()
	code, stdout, stderr := runCLI(t, "publish", "raw", file, "--repo", testRepoID, "--resumable", "--chunk-size", "3", "--timeout", "150ms", "--json")
	if code != ExitPartial || !strings.Contains(stdout, `"uploadId"`) || !strings.Contains(stdout, `"unknown"`) || time.Since(started) > 2*time.Second {
		t.Fatalf("deadline not shared: exit=%d elapsed=%s stdout=%s stderr=%s", code, time.Since(started), stdout, stderr)
	}
	mu.Lock()
	defer mu.Unlock()
	if patches > 3 || commits != 0 {
		t.Fatalf("continued past deadline: patches=%d commits=%d", patches, commits)
	}
}

func TestPublishRawResumablePreservesUnknownAndRejectsForeignProgress(t *testing.T) {
	const id = "00000000-0000-4000-8000-000000000001"
	const target = "/raw/raw-hosted/file.txt"
	for _, scenario := range []string{"lost-start", "foreign-origin", "foreign-path", "missing-offset", "overflow-offset", "patch-refused", "patch-unknown-status-denied", "unexpected-offset", "missing-commit-identity", "wrong-commit-readback-size"} {
		t.Run(scenario, func(t *testing.T) {
			isolatedCLI(t)
			var mu sync.Mutex
			posts, patches, commits := 0, 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				if r.URL.Path == repoBase {
					writeJSON(w, 200, map[string]any{"id": testRepoID, "name": "raw-hosted", "format": "raw", "type": "hosted", "state": "active", "version": "1"})
					return
				}
				if r.URL.Path == repoBase+"/capabilities" {
					writeJSON(w, 200, map[string]any{"format": "raw", "type": "hosted", "operations": []string{"read", "publish"}})
					return
				}
				if r.Method == http.MethodHead {
					if scenario == "wrong-commit-readback-size" && commits > 0 {
						digest, header := sha("abc")
						w.Header().Set("ETag", `"`+digest+`"`)
						w.Header().Set("Digest", header)
						w.Header().Set("Content-Length", "2")
						w.WriteHeader(200)
						return
					}
					w.WriteHeader(404)
					return
				}
				w.Header().Set("Location", target+"?uploadId="+id)
				w.Header().Set("Upload-Offset", "0")
				switch r.Method {
				case http.MethodPost:
					posts++
					switch scenario {
					case "lost-start":
						conn, _, err := w.(http.Hijacker).Hijack()
						if err != nil {
							t.Error(err)
							return
						}
						_ = conn.Close()
						return
					case "foreign-origin":
						w.Header().Set("Location", "https://"+repoTestToken+"@foreign.example"+target+"?uploadId="+id)
					case "foreign-path":
						w.Header().Set("Location", "/raw/other/file.txt?uploadId="+id)
					case "missing-offset":
						w.Header().Del("Upload-Offset")
					case "overflow-offset":
						w.Header().Set("Upload-Offset", "9223372036854775808")
					}
					w.WriteHeader(201)
				case http.MethodPatch:
					patches++
					if scenario == "patch-refused" {
						w.WriteHeader(403)
						return
					}
					if scenario == "patch-unknown-status-denied" {
						w.WriteHeader(503)
						return
					}
					if scenario == "unexpected-offset" {
						w.Header().Set("Upload-Offset", "1")
					} else {
						w.Header().Set("Upload-Offset", "3")
					}
					w.WriteHeader(204)
				case http.MethodGet:
					if scenario == "patch-unknown-status-denied" {
						w.WriteHeader(401)
						return
					}
					w.Header().Set("Upload-Offset", "1")
					w.WriteHeader(204)
				case http.MethodPut:
					commits++
					if scenario == "wrong-commit-readback-size" {
						w.WriteHeader(503)
					} else {
						w.WriteHeader(201)
					}
				}
			}))
			t.Cleanup(server.Close)
			t.Setenv("AGCTL_SERVER", server.URL)
			t.Setenv("AGCTL_TOKEN", repoTestToken)
			file := filepath.Join(writeTree(t, map[string]string{"file.txt": "abc"}), "file.txt")
			code, stdout, stderr := runCLI(t, "publish", "raw", file, "--repo", testRepoID, "--resumable", "--chunk-size", "3", "--json")
			var payload struct {
				Items []objectResult `json:"items"`
			}
			err := json.Unmarshal(decodeEnvelope(t, stdout).Data, &payload)
			if code != ExitPartial || err != nil || len(payload.Items) != 1 {
				t.Fatalf("exit=%d stdout=%s stderr=%s error=%v", code, stdout, stderr, err)
			}
			item := payload.Items[0]
			wantStatus := "unknown"
			if scenario == "patch-refused" {
				wantStatus = "failed"
			}
			if item.Status != wantStatus {
				t.Fatalf("outcome=%+v", item)
			}
			mu.Lock()
			defer mu.Unlock()
			if posts != 1 || patches > 1 || commits > 1 {
				t.Fatalf("uncertain actions replayed: posts=%d patches=%d commits=%d", posts, patches, commits)
			}
			if patches > 0 && (item.UploadID != id || item.ReadBack == "") {
				t.Fatalf("lost recovery identity: %+v", item)
			}
			noSecret(t, repoTestToken, stdout, stderr)
		})
	}
}

func TestPublishRawResumeExistingUploadAndFinalResponseLoss(t *testing.T) {
	isolatedCLI(t)
	store := repository.NewMemoryStore()
	if _, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: testRepoID, Name: "downloads", Format: repository.FormatRaw}); err != nil {
		t.Fatal(err)
	}
	handler := apptest.NewGateway(t, app.GatewayOptions{Store: store, NPMMetadata: store, NPMRestore: store, Storage: apptest.MemoryObjectStores(nil), Adapter: app.TestAdapter{}, Authenticator: app.Authenticator{AdminToken: repoTestToken}}, app.NewGatewayHandler)
	const target = "/raw/downloads/file.txt"
	request := func(method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+repoTestToken)
		for name, value := range headers {
			r.Header.Set(name, value)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	start := request(http.MethodPost, target+"?resumable=1", "", nil)
	if start.Code != 201 {
		t.Fatalf("start=%d %s", start.Code, start.Body.String())
	}
	location := start.Header().Get("Location")
	id := strings.Split(location, "uploadId=")[1]
	if patch := request(http.MethodPatch, location, "abc", map[string]string{"Upload-Offset": "0"}); patch.Code != 204 {
		t.Fatalf("patch=%d %s", patch.Code, patch.Body.String())
	}
	var commits, posts int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			posts++
		}
		if r.Method == http.MethodPut {
			commits++
			handler.ServeHTTP(httptest.NewRecorder(), r)
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			_ = conn.Close()
			return
		}
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	t.Setenv("AGCTL_SERVER", server.URL)
	t.Setenv("AGCTL_TOKEN", repoTestToken)
	file := filepath.Join(t.TempDir(), "file.txt")
	if err := os.WriteFile(file, []byte("abcdef"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := runCLI(t, "publish", "raw", file, "--repo", testRepoID, "--upload-id", id, "--chunk-size", "3", "--json")
	if code != ExitOK || commits != 1 || posts != 0 || !strings.Contains(stdout, `"confirmedBy": "readBack"`) {
		t.Fatalf("exit=%d commits=%d starts=%d stdout=%s stderr=%s", code, commits, posts, stdout, stderr)
	}
	_, expected := sha("abcdef")
	read := request(http.MethodGet, target, "", nil)
	if read.Body.String() != "abcdef" || read.Header().Get("Digest") != expected {
		t.Fatalf("resumed object=%q headers=%v", read.Body.String(), read.Header())
	}
	// An existing complete object can be confirmed without opening a second
	// session or committing the closed upload again.
	code, stdout, _ = runCLI(t, "publish", "raw", file, "--repo", testRepoID, "--upload-id", id, "--json")
	if code != ExitOK || !strings.Contains(stdout, `"skipped"`) || commits != 1 || posts != 0 {
		t.Fatalf("completed resume replay exit=%d %s", code, stdout)
	}
}

func TestPublishChecksTargetIdentityAndAdvertisedCapabilityBeforeWrites(t *testing.T) {
	for _, format := range []string{"raw", "maven"} {
		for _, scenario := range []string{"wrong-repository", "wrong-format", "proxy", "missing-publish", "wrong-capability", "empty-capability"} {
			t.Run(format+"/"+scenario, func(t *testing.T) {
				api := newRouteAPI(t, "")
				repoID, repoFormat, repoType := testRepoID, format, "hosted"
				if scenario == "wrong-repository" {
					repoID = "00000000-0000-4000-8000-000000000099"
				}
				if scenario == "wrong-format" {
					repoFormat = "npm"
				}
				if scenario == "proxy" {
					repoType = "proxy"
				}
				repoBody, _ := json.Marshal(map[string]any{"id": repoID, "name": format + "-hosted", "format": repoFormat, "type": repoType, "state": "active", "version": "1"})
				api.on("GET", repoBase, 200, string(repoBody))
				operations := []string{"read", "publish"}
				if scenario == "missing-publish" {
					operations = []string{"read"}
				}
				capFormat := format
				if scenario == "wrong-capability" {
					capFormat = "npm"
				}
				caps, _ := json.Marshal(map[string]any{"format": capFormat, "type": "hosted", "operations": operations})
				if scenario == "empty-capability" {
					caps = []byte("{}")
				}
				api.on("GET", repoBase+"/capabilities", 200, string(caps))
				var args []string
				if format == "raw" {
					file := filepath.Join(writeTree(t, map[string]string{"file": "abc"}), "file")
					args = []string{"publish", "raw", file, "--repo", testRepoID, "--resumable", "--json"}
				} else {
					pom, jar := mavenFiles(t)
					args = []string{"publish", "maven", "--repo", testRepoID, "--coordinate", "com.example:app:1.0.0", "--pom", pom, "--file", jar, "--json"}
				}
				code, stdout, stderr := runCLI(t, args...)
				if code == ExitOK || api.count("POST") != 0 || api.count("PATCH") != 0 || api.count("PUT") != 0 {
					t.Fatalf("preflight failed open: exit=%d stdout=%s stderr=%s requests=%v", code, stdout, stderr, api.recorded())
				}
			})
		}
	}
}
