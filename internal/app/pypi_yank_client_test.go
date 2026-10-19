package app

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ccsert/artifact-gateway/internal/objectstore"
	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

// The official pip process has a fresh destination and cache for every case,
// reads only this Gateway, and cannot resolve build dependencies elsewhere.
func TestPyPIYankRealPipHostedProxyGroup(t *testing.T) {
	if os.Getenv("ARTIFACT_GATEWAY_PYPI_CLI_E2E") == "" {
		t.Skip("set ARTIFACT_GATEWAY_PYPI_CLI_E2E=1 for official pip acceptance")
	}
	python := os.Getenv("ARTIFACT_GATEWAY_PYTHON")
	if python == "" {
		t.Fatal("ARTIFACT_GATEWAY_PYTHON must name the isolated pinned client")
	}
	version, err := exec.CommandContext(t.Context(), python, "-m", "pip", "--version").CombinedOutput()
	if err != nil || !strings.HasPrefix(string(version), "pip 26.2 ") {
		t.Fatalf("expected pinned pip 26.2: %s %v", version, err)
	}
	t.Log(strings.TrimSpace(string(version)))
	const project = "yank-widget"
	archives := make(map[string][]byte)
	var records []repository.PyPIFile
	for _, v := range []string{"1.0", "2.0", "3.0"} {
		for _, wheel := range []bool{true, false} {
			name := "yank_widget-" + v + ".tar.gz"
			data := pypiYankSdist(t, "yank_widget", v)
			kind := "sdist"
			if wheel {
				name = "yank_widget-" + v + "-py3-none-any.whl"
				data = pypiFixtureWheel(t, "yank_widget", v)
				kind = "bdist_wheel"
			}
			sum := sha256.Sum256(data)
			file := repository.PyPIFile{Project: project, Version: v, Filename: name, FileType: kind, Digest: fmt.Sprintf("sha256:%x", sum), ObjectKey: fmt.Sprintf("native/pypi/sha256/%x", sum), Size: int64(len(data))}
			if v == "2.0" {
				file.Yanked = true
				file.YankReason = "release ABI regression"
			}
			if v == "3.0" && wheel {
				file.Yanked = true
				file.YankReason = "wheel ABI regression"
			}
			records = append(records, file)
			archives[name] = data
		}
	}
	for _, kind := range []string{"hosted", "proxy", "group"} {
		t.Run(kind, func(t *testing.T) {
			store := repository.NewMemoryStore()
			objects := objectstore.NewMemoryStore()
			var unyanked atomic.Bool
			var upstreamCalls atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				upstreamCalls.Add(1)
				if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
					t.Error("downstream identity reached the upstream")
				}
				if r.URL.Path == "/simple/"+project+"/" {
					files := make([]map[string]any, 0, len(records))
					for _, file := range records {
						var yank any = false
						if file.Yanked && (file.Version != "2.0" || !unyanked.Load()) {
							yank = file.YankReason
						}
						files = append(files, map[string]any{"filename": file.Filename, "url": "/packages/" + file.Filename, "hashes": map[string]string{"sha256": strings.TrimPrefix(file.Digest, "sha256:")}, "yanked": yank})
					}
					w.Header().Set("Content-Type", "application/vnd.pypi.simple.v1+json")
					_ = json.NewEncoder(w).Encode(map[string]any{"meta": map[string]string{"api-version": "1.0"}, "name": project, "files": files})
					return
				}
				if data, found := archives[strings.TrimPrefix(r.URL.Path, "/packages/")]; found {
					_, _ = w.Write(data)
					return
				}
				http.NotFound(w, r)
			}))
			t.Cleanup(upstream.Close)
			repoKind := repository.RepositoryTypeHosted
			if kind != "hosted" {
				repoKind = repository.RepositoryTypeProxy
			}
			candidate := repository.HostedRepository{ID: uuid.NewString(), Name: "pip-yank-" + kind, Format: repository.FormatPyPI, Type: repoKind}
			if repoKind == repository.RepositoryTypeProxy {
				candidate.Endpoint = upstream.URL
			}
			repo, err := store.CreateHostedRepository(t.Context(), candidate)
			if err != nil {
				t.Fatal(err)
			}
			root := repo.Name
			if kind == "hosted" {
				for _, source := range records {
					file := source
					file.RepositoryID = repo.ID
					if err = objects.Put(t.Context(), file.ObjectKey, archives[file.Filename]); err != nil {
						t.Fatal(err)
					}
					if _, err = store.PublishPyPIFile(t.Context(), file); err != nil {
						t.Fatal(err)
					}
				}
			}
			if kind == "group" {
				group, _, createErr := store.CreateHostedGroupIdempotently(t.Context(), repository.HostedGroup{ID: uuid.NewString(), Name: "pip-yank-group", Format: repository.FormatPyPI, Members: []repository.GroupMember{{RepositoryID: repo.ID, Position: 0}}}, "admin", "pip-yank", "payload")
				if createErr != nil {
					t.Fatal(createErr)
				}
				root = group.Name
			}
			handler := newTestGateway(t, GatewayOptions{Store: store, Adapter: TestAdapter{}, Authenticator: testAuthenticator(), Storage: ObjectStores{repository.FormatPyPI: objects}, Upstream: UpstreamClient{HTTPClient: upstream.Client()}})
			server := httptest.NewServer(handler)
			t.Cleanup(server.Close)
			indexURL := strings.Replace(server.URL, "http://", "http://ci:resolver-secret@", 1) + "/pypi/" + root + "/simple/"
			run := func(requirement, format, wantVersion, wantReason string) {
				t.Helper()
				directory := t.TempDir()
				ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
				defer cancel()
				args := []string{"-m", "pip", "--isolated", "download", "--disable-pip-version-check", "--no-cache-dir", "--no-deps", "--no-build-isolation", "--keyring-provider", "disabled", "--index-url", indexURL, "--trusted-host", strings.TrimPrefix(server.URL, "http://"), "--dest", directory}
				if format == "wheel" {
					args = append(args, "--only-binary", ":all:")
				} else {
					args = append(args, "--no-binary", ":all:")
				}
				args = append(args, requirement)
				command := exec.CommandContext(ctx, python, args...)
				command.Env = pypiYankClientEnvironment(t.TempDir())
				output, runErr := command.CombinedOutput()
				safeOutput := strings.ReplaceAll(string(output), "resolver-secret", "<redacted>")
				if runErr != nil {
					t.Fatalf("%s %s pip failed: %v\n%s", kind, requirement, runErr, safeOutput)
				}
				name := "yank_widget-" + wantVersion + ".tar.gz"
				if format == "wheel" {
					name = "yank_widget-" + wantVersion + "-py3-none-any.whl"
				}
				entries, readErr := os.ReadDir(directory)
				if readErr != nil || len(entries) != 1 || entries[0].Name() != name {
					t.Fatalf("resolved file identity=%v %v", entries, readErr)
				}
				data, readErr := os.ReadFile(filepath.Join(directory, name))
				expected := archives[name]
				if readErr != nil || !bytes.Equal(data, expected) || sha256.Sum256(data) != sha256.Sum256(expected) {
					t.Fatalf("archive size/digest mismatch for %s (%d/%d): %v", name, len(data), len(expected), readErr)
				}
				if wantReason != "" && (!strings.Contains(safeOutput, "yanked") || !strings.Contains(safeOutput, wantReason)) {
					t.Fatalf("missing exact-pin warning: %s", safeOutput)
				}
				t.Logf("%s %s -> %s size=%d SHA-256=%x yank-warning=%t", format, requirement, name, len(data), sha256.Sum256(data), wantReason != "")
			}
			for _, format := range []string{"wheel", "sdist"} {
				run(project+"<3", format, "1.0", "")
				run(project+"==2.0", format, "2.0", "release ABI regression")
			}
			run(project+"<4", "wheel", "1.0", "")
			run(project+"<4", "sdist", "3.0", "")
			run(project+"==3.0", "wheel", "3.0", "wheel ABI regression")
			if kind == "hosted" {
				path := "/api/v2/repositories/" + repo.ID + "/pypi/projects/" + project + "/versions/2.0/yank"
				for _, next := range []bool{false, true} {
					current := pypiYankRequest(handler, "GET", path, "", "", "admin-secret")
					changed := pypiYankRequest(handler, "PATCH", path, fmt.Sprintf(`{"yanked":%t,"reason":"release ABI regression"}`, next), current.Header().Get("ETag"), "admin-secret")
					if changed.Code != 200 {
						t.Fatalf("management transition=%d %s", changed.Code, changed.Body.String())
					}
					want := "2.0"
					if next {
						want = "1.0"
					}
					run(project+"<3", "wheel", want, "")
				}
			} else {
				unyanked.Store(true)
				run(project+"<3", "wheel", "2.0", "")
				unyanked.Store(false)
				run(project+"<3", "wheel", "1.0", "")
				upstream.Close()
				calls := upstreamCalls.Load()
				for _, format := range []string{"wheel", "sdist"} {
					run(project+"<3", format, "1.0", "")
					run(project+"==2.0", format, "2.0", "release ABI regression")
				}
				if upstreamCalls.Load() != calls {
					t.Fatal("offline cache attempted another live upstream request")
				}
			}
		})
	}
}

func pypiYankClientEnvironment(home string) []string {
	var environment []string
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(key, "PIP_") || strings.HasPrefix(key, "UV_") || strings.HasSuffix(strings.ToUpper(key), "_PROXY") || key == "HOME" || key == "NETRC" || key == "PYTHONPATH" || key == "PYTHONHOME" {
			continue
		}
		environment = append(environment, entry)
	}
	return append(environment, "HOME="+home, "PIP_CONFIG_FILE="+os.DevNull, "PIP_DISABLE_PIP_VERSION_CHECK=1")
}

func pypiYankSdist(t *testing.T, distribution, version string) []byte {
	t.Helper()
	buffer := new(bytes.Buffer)
	compressed := gzip.NewWriter(buffer)
	archive := tar.NewWriter(compressed)
	files := map[string]string{
		"PKG-INFO":       "Metadata-Version: 2.1\nName: " + strings.ReplaceAll(distribution, "_", "-") + "\nVersion: " + version + "\n",
		"pyproject.toml": "[build-system]\nrequires = []\nbuild-backend = '_ag_build'\nbackend-path = ['.']\n",
		"_ag_build.py":   "from pathlib import Path\ndef prepare_metadata_for_build_wheel(metadata_directory, config_settings=None):\n    name = '" + distribution + "-" + version + ".dist-info'\n    destination = Path(metadata_directory) / name\n    destination.mkdir()\n    (destination / 'METADATA').write_text('Metadata-Version: 2.1\\nName: " + strings.ReplaceAll(distribution, "_", "-") + "\\nVersion: " + version + "\\n')\n    return name\n",
	}
	for name, data := range files {
		if err := archive.WriteHeader(&tar.Header{Name: distribution + "-" + version + "/" + name, Mode: 0600, Size: int64(len(data))}); err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(archive, data); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := compressed.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}
