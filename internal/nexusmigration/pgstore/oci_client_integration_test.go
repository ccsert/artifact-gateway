//go:build integration && ociclient

package pgstore_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ccsert/artifact-gateway/internal/app"
	"github.com/ccsert/artifact-gateway/internal/ocigraph"
	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/ccsert/artifact-gateway/internal/testsupport/ocifixture"
	"github.com/google/uuid"
)

// The fixed official client uses fresh content/config directories for every
// pull and an allowlisted target-only HTTP listener after the TLS source stops.
func TestPostgresNexusOCIMigrationTargetOnlyCraneClient(t *testing.T) {
	for _, variant := range []struct {
		name  string
		build func(string) ocifixture.Fixture
	}{{"oci-index", ocifixture.New}, {"docker-list", ocifixture.NewDocker}, {"nested-oci-index", ocifixture.NewNested}} {
		t.Run(variant.name, func(t *testing.T) { exerciseNexusOCIMigrationCraneClient(t, variant.build(t.Name()+uuid.NewString())) })
	}
}

func exerciseNexusOCIMigrationCraneClient(t *testing.T, producer ocifixture.Fixture) {
	t.Helper()
	crane := os.Getenv("MIGRATION_CRANE_BIN")
	if crane == "" {
		t.Fatal("pinned official crane is mandatory; use scripts/nexus-oci-migration-e2e.sh")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	f, _, producer := newOCIMigrationFixtureWithProducer(t, producer)
	batch := f.start()
	runOCIBatch(t, f, batch)
	requireState(t, *batch, "completed")
	f.closeSource()
	if err := f.native.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := repository.NewPostgresStore(f.connection)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reopened.Close() }()
	credential := uuid.NewString()
	handler, err := app.NewGatewayHandler(app.GatewayOptions{Store: reopened, NPMMetadata: reopened, NPMRestore: reopened, Storage: app.SharedObjectStores(f.objects), Adapter: app.TestAdapter{}, Authenticator: app.Authenticator{ResolverToken: credential, RepositoryReaders: map[string][]string{"migration-reader": {f.repo.Name}}}})
	if err != nil {
		t.Fatal(err)
	}
	mu := sync.Mutex{}
	requests := map[string]int{}
	prefix := "/v2/" + f.repo.Name + "/" + producer.Name + "/"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if (r.Method != http.MethodGet && r.Method != http.MethodHead) || r.URL.Path != "/v2/" && r.URL.Path != "/auth/token" && !strings.HasPrefix(r.URL.Path, prefix) {
			t.Errorf("client escaped owned native target: %s %s", r.Method, r.URL.Path)
			http.Error(w, "owned route denied", 403)
			return
		}
		mu.Lock()
		requests[r.URL.Path]++
		mu.Unlock()
		handler.ServeHTTP(w, r)
	}))
	defer server.Close()
	reference := strings.TrimPrefix(server.URL, "http://") + "/" + ociTargetReference(f, producer)
	for _, platform := range []string{"all", "linux/amd64", "linux/arm64"} {
		t.Run(platform, func(t *testing.T) {
			root := t.TempDir()
			config := filepath.Join(root, "docker")
			if err := os.Mkdir(config, 0700); err != nil {
				t.Fatal(err)
			}
			auth := base64.StdEncoding.EncodeToString([]byte("migration-reader:" + credential))
			encoded, _ := json.Marshal(map[string]any{"auths": map[string]any{strings.TrimPrefix(server.URL, "http://"): map[string]string{"auth": auth}}})
			if err := os.WriteFile(filepath.Join(config, "config.json"), encoded, 0600); err != nil {
				t.Fatal(err)
			}
			env := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + root, "DOCKER_CONFIG=" + config, "XDG_CACHE_HOME=" + filepath.Join(root, "cache"), "TMPDIR=" + root, "HTTP_PROXY=", "HTTPS_PROXY=", "ALL_PROXY=", "NO_PROXY=*"}
			run := func(args ...string) []byte {
				t.Helper()
				command := exec.CommandContext(ctx, crane, args...)
				command.Dir = root
				command.Env = env
				out, err := command.CombinedOutput()
				if err != nil {
					t.Fatalf("crane %v failed: %v\n%s", args, err, out)
				}
				return out
			}
			version := strings.TrimSpace(string(run("version")))
			if version != "0.22.1" {
				t.Fatalf("official crane version=%q want 0.22.1", version)
			}
			layout := filepath.Join(root, "cold-layout")
			if _, err := os.Stat(layout); !os.IsNotExist(err) {
				t.Fatal("client content store was not empty")
			}
			args := []string{"pull", "--insecure", "--format=oci"}
			if platform != "all" {
				args = append(args, "--platform", platform)
			}
			args = append(args, reference, layout)
			run(args...)
			var index struct {
				Manifests []struct {
					Digest string `json:"digest"`
				} `json:"manifests"`
			}
			indexBytes, err := os.ReadFile(filepath.Join(layout, "index.json"))
			if err != nil || json.Unmarshal(indexBytes, &index) != nil || len(index.Manifests) != 1 {
				t.Fatalf("client layout index=%s %v", indexBytes, err)
			}
			if platform == "all" {
				if index.Manifests[0].Digest != producer.Root {
					t.Fatalf("all-platform pull replaced original root: %s want %s", index.Manifests[0].Digest, producer.Root)
				}
				for source, want := range producer.Bytes {
					got, err := os.ReadFile(filepath.Join(layout, "blobs", "sha256", producer.Digests[source]))
					if err != nil || !bytes.Equal(got, want) {
						t.Fatalf("official client closure bytes differ: %s digest=%s %v", source, producer.Digests[source], err)
					}
				}
			} else {
				manifestBytes, err := os.ReadFile(filepath.Join(layout, "blobs", "sha256", strings.TrimPrefix(index.Manifests[0].Digest, "sha256:")))
				if err != nil {
					t.Fatal(err)
				}
				document, err := ocigraph.Decode(manifestBytes, "")
				if err != nil {
					t.Fatal(err)
				}
				configBytes, err := os.ReadFile(filepath.Join(layout, "blobs", "sha256", strings.TrimPrefix(document.Config.Digest, "sha256:")))
				if err != nil {
					t.Fatal(err)
				}
				var config struct{ Architecture, OS string }
				if json.Unmarshal(configBytes, &config) != nil || config.OS+"/"+config.Architecture != platform {
					t.Fatalf("wrong cold platform=%s bytes=%s", platform, configBytes)
				}
				for _, descriptor := range document.Dependencies() {
					body, err := os.ReadFile(filepath.Join(layout, "blobs", "sha256", strings.TrimPrefix(descriptor.Digest, "sha256:")))
					if err != nil || int64(len(body)) != *descriptor.Size || ocigraph.Digest(body) != descriptor.Digest {
						t.Fatalf("platform client dependency=%+v %v", descriptor, err)
					}
				}
			}
		})
	}
	for source := range producer.Bytes {
		kind := "blobs/"
		if strings.Contains(source, "/manifests/") {
			kind = "manifests/"
		}
		path := prefix + kind + "sha256:" + producer.Digests[source]
		mu.Lock()
		count := requests[path]
		mu.Unlock()
		if count == 0 {
			t.Fatalf("official client did not request migrated asset %s", path)
		}
	}
	t.Logf("official crane v0.22.1: source stopped, fresh SQL store/Gateway, target-only independent all/amd64/arm64 cold OCI layouts; complete source-object bytes and original root %s preserved; sealed migration manifest %s", producer.Root, batch.ManifestSHA256)
}
