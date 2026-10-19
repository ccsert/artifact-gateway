//go:build integration && npmclient

package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ccsert/artifact-gateway/internal/objectstore"
	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

// This test is selected only by the mandatory, isolated official-client gate.
// Missing clients or persistence are failures rather than successful skips.
func TestPostgresRustFSNPMDistTagOfficialClient(t *testing.T) {
	required := func(name string) string {
		t.Helper()
		value := os.Getenv(name)
		if value == "" {
			t.Fatalf("%s is required by the official npm client gate", name)
		}
		return value
	}
	node, cli := required("NPM_CLIENT_NODE"), required("NPM_CLIENT_JS")
	databaseURL := required("TEST_DATABASE_URL")
	endpoint, access, secret := required("TEST_RUSTFS_ENDPOINT"), required("TEST_RUSTFS_ACCESS_KEY"), required("TEST_RUSTFS_SECRET_KEY")
	ctx := context.Background()
	store, err := repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	bucket := "npm-client-" + uuid.NewString()
	objects, err := objectstore.NewRustFSStore(endpoint, access, secret, bucket)
	if err != nil {
		t.Fatal(err)
	}
	if err = objects.EnsureBucket(ctx); err != nil {
		t.Fatal(err)
	}
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{
		ID: uuid.NewString(), Name: "npm-clients-" + uuid.NewString(), Format: repository.FormatNPM,
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(newTestGateway(t, GatewayOptions{
		Store: store, Adapter: TestAdapter{}, Authenticator: testAuthenticator(),
		Storage: ObjectStores{repository.FormatNPM: objects},
	}))
	t.Cleanup(server.Close)
	registry := server.URL + "/npm/" + repo.Name + "/"
	config := filepath.Join(t.TempDir(), "npmrc")
	writeConfig := func() {
		t.Helper()
		parsed, parseErr := url.Parse(registry)
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		text := "registry=" + registry + "\n//" + parsed.Host + parsed.Path + ":_authToken=resolver-secret\n"
		if writeErr := os.WriteFile(config, []byte(text), 0600); writeErr != nil {
			t.Fatal(writeErr)
		}
	}
	writeConfig()
	npm := func(directory string, args ...string) []byte {
		t.Helper()
		commandCtx, cancel := context.WithTimeout(ctx, time.Minute)
		defer cancel()
		arguments := append([]string{cli}, args...)
		arguments = append(arguments, "--userconfig="+config, "--globalconfig="+os.DevNull, "--registry="+registry, "--cache="+t.TempDir(), "--no-audit", "--no-fund", "--ignore-scripts")
		command := exec.CommandContext(commandCtx, node, arguments...)
		command.Dir = directory
		command.Env = append(os.Environ(), "npm_config_update_notifier=false")
		output, commandErr := command.CombinedOutput()
		if commandErr != nil {
			t.Fatalf("official npm %v: %v\n%s", args, commandErr, output)
		}
		return output
	}
	if version := strings.TrimSpace(string(npm(t.TempDir(), "--version"))); version != "11.6.2" {
		t.Fatalf("official npm version=%q, require 11.6.2", version)
	}
	type publication struct {
		name, version string
		tarball, file []byte
	}
	var published []publication
	for _, name := range []string{"plain-dist-tags", "@scope/dist-tags"} {
		for index, version := range []string{"1.0.0", "1.1.0"} {
			directory := t.TempDir()
			manifest, _ := json.Marshal(map[string]string{"name": name, "version": version, "license": "MIT"})
			payload := bytes.Repeat([]byte(name+"@"+version+"\x00\xff"), 257)
			for path, data := range map[string][]byte{"package.json": manifest, "payload.bin": payload} {
				if err = os.WriteFile(filepath.Join(directory, path), data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			npm(directory, "pack", "--json")
			archives, globErr := filepath.Glob(filepath.Join(directory, "*.tgz"))
			if globErr != nil || len(archives) != 1 {
				t.Fatalf("official npm pack archives=%v err=%v", archives, globErr)
			}
			archive, readErr := os.ReadFile(archives[0])
			if readErr != nil {
				t.Fatal(readErr)
			}
			tag := "latest"
			if index == 1 {
				tag = "next"
			}
			npm(directory, "publish", archives[0], "--tag="+tag)
			published = append(published, publication{name, version, archive, payload})
		}
		npm(t.TempDir(), "dist-tag", "add", name+"@1.1.0", "beta")
		npm(t.TempDir(), "dist-tag", "add", name+"@1.0.0", "beta")
		if listed := string(npm(t.TempDir(), "dist-tag", "ls", name)); !strings.Contains(listed, "beta: 1.0.0") {
			t.Fatalf("official npm did not observe committed tag: %s", listed)
		}
	}

	// Close the publishing HTTP instance and database pool. A new instance
	// resolves only the committed PostgreSQL and RustFS state.
	server.Close()
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = restarted.Close() })
	restartedObjects, err := objectstore.NewRustFSStore(endpoint, access, secret, bucket)
	if err != nil {
		t.Fatal(err)
	}
	restartedServer := httptest.NewServer(newTestGateway(t, GatewayOptions{
		Store: restarted, Adapter: TestAdapter{}, Authenticator: testAuthenticator(),
		Storage: ObjectStores{repository.FormatNPM: restartedObjects},
	}))
	t.Cleanup(restartedServer.Close)
	registry = restartedServer.URL + "/npm/" + repo.Name + "/"
	writeConfig()
	for _, item := range published {
		stored, getErr := restarted.GetNPMVersion(ctx, repo.ID, item.name, item.version)
		sum := sha256.Sum256(item.tarball)
		if getErr != nil || stored.Digest != "sha256:"+hex.EncodeToString(sum[:]) || stored.Size != int64(len(item.tarball)) {
			t.Fatalf("publication identity changed for %s@%s: %#v %v", item.name, item.version, stored, getErr)
		}
		request, requestErr := http.NewRequest(http.MethodGet, registry+item.name+"/-/"+stored.TarballName, nil)
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		request.Header.Set("Authorization", "Bearer resolver-secret")
		response, requestErr := restartedServer.Client().Do(request)
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		body, readErr := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if readErr != nil || response.StatusCode != http.StatusOK || !bytes.Equal(body, item.tarball) {
			t.Fatalf("complete tarball mismatch %s@%s status=%d read=%v", item.name, item.version, response.StatusCode, readErr)
		}
		t.Logf("restarted target inventory %s@%s bytes=%d sha256=%x", item.name, item.version, len(body), sum)
		if item.version != "1.0.0" {
			continue
		}
		directory := t.TempDir()
		npm(directory, "install", item.name+"@beta", "--package-lock=false")
		installed, readErr := os.ReadFile(filepath.Join(directory, "node_modules", item.name, "payload.bin"))
		if readErr != nil || !bytes.Equal(installed, item.file) {
			t.Fatalf("empty-cache tag install payload %s: %v", item.name, readErr)
		}
		npm(t.TempDir(), "dist-tag", "rm", item.name, "beta")
		if tags := string(npm(t.TempDir(), "dist-tag", "ls", item.name)); strings.Contains(tags, "beta:") {
			t.Fatalf("removed tag remains after restart: %s", tags)
		}
	}
	t.Log("official npm 11.6.2: scoped/unscoped publish, add/list/move/remove, empty-cache target-only install and full-byte SHA-256 readback survived a fresh Gateway instance")
}
