//go:build integration && npmclient

package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
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
	clientToken := "resolver-secret"
	writeConfig := func() {
		t.Helper()
		parsed, parseErr := url.Parse(registry)
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		text := "registry=" + registry + "\n//" + parsed.Host + parsed.Path + ":_authToken=" + clientToken + "\n"
		if writeErr := os.WriteFile(config, []byte(text), 0600); writeErr != nil {
			t.Fatal(writeErr)
		}
	}
	writeConfig()
	npmAttempt := func(directory string, args ...string) ([]byte, error) {
		t.Helper()
		commandCtx, cancel := context.WithTimeout(ctx, time.Minute)
		defer cancel()
		arguments := append([]string{cli}, args...)
		arguments = append(arguments, "--userconfig="+config, "--globalconfig="+os.DevNull, "--registry="+registry, "--cache="+t.TempDir(), "--no-audit", "--no-fund", "--ignore-scripts")
		command := exec.CommandContext(commandCtx, node, arguments...)
		command.Dir = directory
		command.Env = append(os.Environ(), "npm_config_update_notifier=false")
		output, commandErr := command.CombinedOutput()
		return output, commandErr
	}
	npm := func(directory string, args ...string) []byte {
		t.Helper()
		output, commandErr := npmAttempt(directory, args...)
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

	// Metadata governance uses the same four official tarballs, with actual
	// repository-scoped writer and administrator identities.
	const operator = "npm-package-operator"
	secondary, err := restarted.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "npm-secondary-" + uuid.NewString(), Format: repository.FormatNPM})
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []repository.HostedRepository{repo, secondary} {
		if _, err = restarted.ReplaceRepositoryGrants(ctx, target.ID, []repository.RepositoryGrant{
			{Principal: "build-agent", Scopes: []string{"repositories:write"}},
			{Principal: operator, Scopes: []string{"repositories:admin"}},
		}, "1"); err != nil {
			t.Fatal(err)
		}
	}
	for _, item := range published {
		version, e := restarted.GetNPMVersion(ctx, repo.ID, item.name, item.version)
		if e != nil {
			t.Fatal(e)
		}
		version.RepositoryID = secondary.ID
		tag := "latest"
		if item.version == "1.1.0" {
			tag = "next"
		}
		if _, e = restarted.PublishNPMVersion(ctx, version, map[string]string{tag: item.version}); e != nil {
			t.Fatal(e)
		}
	}
	groupName := "npm-governance-group-" + uuid.NewString()
	if _, _, err = restarted.CreateHostedGroupIdempotently(ctx, repository.HostedGroup{ID: uuid.NewString(), Name: groupName, Format: repository.FormatNPM, Members: []repository.GroupMember{{RepositoryID: repo.ID, Position: 0}, {RepositoryID: secondary.ID, Position: 1}}}, "npm-client", uuid.NewString(), "fixture"); err != nil {
		t.Fatal(err)
	}
	primaryRegistry := registry
	operatorToken := testAuthenticator().IssueToken(operator)
	for _, name := range []string{"plain-dist-tags", "@scope/dist-tags"} {
		npm(t.TempDir(), "deprecate", name+"@1.0.0", "exact warning")
		if output := npm(t.TempDir(), "view", name+"@1.0.0", "deprecated", "--json"); !bytes.Contains(output, []byte(`"exact warning"`)) {
			t.Fatalf("exact deprecation: %s", output)
		}
		npm(t.TempDir(), "deprecate", name+"@>=1.0.0 <2.0.0", "range warning")
		for _, version := range []string{"1.0.0", "1.1.0"} {
			if output := npm(t.TempDir(), "view", name+"@"+version, "deprecated", "--json"); !bytes.Contains(output, []byte(`"range warning"`)) {
				t.Fatalf("range selection %s@%s: %s", name, version, output)
			}
		}
		warningDir := t.TempDir()
		if output := npm(warningDir, "install", name+"@1.0.0", "--package-lock=false"); !bytes.Contains(output, []byte("deprecated "+name+"@1.0.0: range warning")) {
			t.Fatalf("cold install warning missing: %s", output)
		}
		npm(t.TempDir(), "deprecate", name+"@1.0.0", "")
		if output := npm(t.TempDir(), "view", name+"@1.0.0", "deprecated", "--json"); len(bytes.TrimSpace(output)) != 0 {
			t.Fatalf("deprecation not cleared: %s", output)
		}
		clearDir := t.TempDir()
		if output := npm(clearDir, "install", name+"@1.0.0", "--package-lock=false"); bytes.Contains(output, []byte("deprecated "+name+"@1.0.0:")) {
			t.Fatalf("cleared warning remains: %s", output)
		}
		// A writer cannot acquire the repository's delete/restore capability.
		if output, e := npmAttempt(t.TempDir(), "unpublish", name+"@1.0.0", "--force"); e == nil || !bytes.Contains(output, []byte("E403")) {
			t.Fatalf("writer unpublish should be 403: %v %s", e, output)
		}
		clientToken = operatorToken
		writeConfig()
		npm(t.TempDir(), "unpublish", name+"@1.0.0", "--force")
		if output, e := npmAttempt(t.TempDir(), "view", name+"@1.0.0", "version"); e == nil || !bytes.Contains(output, []byte("E404")) {
			t.Fatalf("withdrawn version remains: %v %s", e, output)
		}
		if output := npm(t.TempDir(), "view", name, "dist-tags", "--json"); !bytes.Contains(output, []byte(`"latest": "1.1.0"`)) {
			t.Fatalf("unpublish left dangling latest: %s", output)
		}
		registry = restartedServer.URL + "/npm/" + groupName + "/"
		writeConfig()
		if output, e := npmAttempt(t.TempDir(), "install", name+"@1.0.0", "--package-lock=false"); e == nil || !bytes.Contains(output, []byte("ETARGET")) && !bytes.Contains(output, []byte("E404")) {
			t.Fatalf("Group resurrected lower member: %v %s", e, output)
		}
		registry = primaryRegistry
		writeConfig()
		npm(t.TempDir(), "unpublish", name, "--force")
		registry = restartedServer.URL + "/npm/" + groupName + "/"
		writeConfig()
		if output, e := npmAttempt(t.TempDir(), "view", name, "versions", "--json"); e == nil || !bytes.Contains(output, []byte("E404")) {
			t.Fatalf("Group resurrected whole package: %v %s", e, output)
		}
		registry = primaryRegistry
		clientToken = "resolver-secret"
		writeConfig()
	}
	// A fresh Gateway instance, metadata pool and object-store client see the committed
	// tombstones. Restore is the existing management operation, not republish.
	restartedServer.Close()
	if err = restarted.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err = repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	restartedObjects, err = objectstore.NewRustFSStore(endpoint, access, secret, bucket)
	if err != nil {
		t.Fatal(err)
	}
	restartedServer = httptest.NewServer(newTestGateway(t, GatewayOptions{Store: restarted, Adapter: TestAdapter{}, Authenticator: testAuthenticator(), Storage: ObjectStores{repository.FormatNPM: restartedObjects}}))
	t.Cleanup(restartedServer.Close)
	registry = restartedServer.URL + "/npm/" + repo.Name + "/"
	writeConfig()
	for _, item := range published {
		if _, e := restarted.GetNPMVersion(ctx, repo.ID, item.name, item.version); !errors.Is(e, repository.ErrNotFound) {
			t.Fatalf("restart lost tombstone: %v", e)
		}
		data, _ := json.Marshal(map[string]string{"coordinate": item.name + "@" + item.version})
		request, _ := http.NewRequest(http.MethodPost, restartedServer.URL+"/api/v2/repositories/"+repo.ID+"/restore", bytes.NewReader(data))
		request.Header.Set("Authorization", "Bearer "+operatorToken)
		request.Header.Set("Content-Type", "application/json")
		response, e := restartedServer.Client().Do(request)
		if e != nil {
			t.Fatal(e)
		}
		body, _ := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if response.StatusCode != http.StatusNoContent {
			t.Fatalf("management restore %s@%s=%d %s", item.name, item.version, response.StatusCode, body)
		}
		stored, e := restarted.GetNPMVersion(ctx, repo.ID, item.name, item.version)
		sum := sha256.Sum256(item.tarball)
		if e != nil || stored.Size != int64(len(item.tarball)) || stored.Digest != fmt.Sprintf("sha256:%x", sum) {
			t.Fatalf("restored identity=%#v %v", stored, e)
		}
		object, e := restartedObjects.Get(ctx, stored.ObjectKey)
		if e != nil || !bytes.Equal(object, item.tarball) {
			t.Fatalf("restored complete object changed: %v", e)
		}
		directory := t.TempDir()
		npm(directory, "install", item.name+"@"+item.version, "--package-lock=false")
		installed, e := os.ReadFile(filepath.Join(directory, "node_modules", item.name, "payload.bin"))
		if e != nil || !bytes.Equal(installed, item.file) {
			t.Fatalf("restored native install mismatch: %v", e)
		}
		t.Logf("governance restored %s@%s bytes=%d sha256=%x", item.name, item.version, len(object), sum)
	}
	t.Log("official npm 11.6.2 governance: scoped/unscoped exact and range deprecation, cold install warnings/clear, writer refusal, single/whole unpublish, Group tombstone authority, fresh Gateway instance with PostgreSQL/RustFS clients and management restore preserve all four complete tarballs")
	t.Log("official npm 11.6.2: scoped/unscoped publish, add/list/move/remove, empty-cache target-only install and full-byte SHA-256 readback survived a fresh Gateway instance")
}
