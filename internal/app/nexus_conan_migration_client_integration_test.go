//go:build integration

package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ccsert/artifact-gateway/internal/egress"
	"github.com/ccsert/artifact-gateway/internal/nexusmigration"
	migrationpg "github.com/ccsert/artifact-gateway/internal/nexusmigration/pgstore"
	"github.com/ccsert/artifact-gateway/internal/nexussource"
	sourcepg "github.com/ccsert/artifact-gateway/internal/nexussource/pgstore"
	"github.com/ccsert/artifact-gateway/internal/objectstore"
	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

type conanMigrationSourceFixture struct {
	mu                  sync.Mutex
	files               map[string][]byte
	pages, gets, denied int
	change              func(string, []byte) []byte
}

func (f *conanMigrationSourceFixture) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Method != http.MethodGet {
		f.denied++
		http.Error(w, "source is read-only", http.StatusMethodNotAllowed)
		return
	}
	switch r.URL.Path {
	case "/service/rest/v1/repositories":
		_ = json.NewEncoder(w).Encode([]map[string]string{{"name": "source", "format": "conan", "type": "hosted"}})
	case "/service/rest/v1/repositories/conan/hosted/source":
		_, _ = io.WriteString(w, `{"name":"source","format":"conan","type":"hosted"}`)
	case "/service/rest/v1/assets":
		paths := make([]string, 0, len(f.files))
		for path := range f.files {
			paths = append(paths, path)
		}
		sort.Strings(paths)
		offset, _ := strconv.Atoi(r.URL.Query().Get("continuationToken"))
		if offset < 0 || offset > len(paths) || r.URL.Query().Get("repository") != "source" {
			f.denied++
			http.Error(w, "invalid inventory request", http.StatusBadRequest)
			return
		}
		end := min(offset+2, len(paths))
		items := make([]map[string]any, 0, end-offset)
		for _, path := range paths[offset:end] {
			body := f.files[path]
			items = append(items, map[string]any{"repository": "source", "format": "conan", "path": path, "fileSize": len(body),
				"checksum":    map[string]string{"sha256": fmt.Sprintf("%x", sha256.Sum256(body))},
				"downloadUrl": "https://example.com/repository/source/v2/" + path})
		}
		var next any
		if end < len(paths) {
			next = strconv.Itoa(end)
		}
		f.pages++
		_ = json.NewEncoder(w).Encode(map[string]any{"items": items, "continuationToken": next})
	default:
		if !strings.HasPrefix(r.URL.Path, "/repository/source/v2/conans/") {
			f.denied++
			http.Error(w, "not a source artifact request", http.StatusNotFound)
			return
		}
		path := strings.TrimPrefix(r.URL.Path, "/repository/source/v2/")
		body, ok := f.files[path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		f.gets++
		w.Header().Set("ETag", fmt.Sprintf(`"%x"`, sha256.Sum256(body)))
		if f.change != nil {
			body = f.change(path, body)
		}
		_, _ = w.Write(body)
	}
}

func conanMigrationSource(t *testing.T, f nexusConanPGFixture, files map[string][]byte) (*nexussource.Service, nexussource.Source, *httptest.Server, *conanMigrationSourceFixture) {
	t.Helper()
	remote := &conanMigrationSourceFixture{files: files}
	server := httptest.NewTLSServer(remote)
	t.Cleanup(server.Close)
	hooks := egress.DefaultHooks()
	hooks.ProxyFromEnvironment = func(*http.Request) (*url.URL, error) { return nil, nil }
	hooks.LookupIP = func(context.Context, string, string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("203.0.113.20")}, nil
	}
	hooks.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
	}
	source := nexussource.New(sourcepg.New(f.db), f.store, nexussource.Options{Client: server.Client(), EgressHooks: &hooks})
	created, err := source.Create(context.Background(), nexussource.Input{Name: "owned-conan-source", BaseURL: "https://example.com"})
	if err != nil {
		t.Fatal(err)
	}
	return source, created, server, remote
}

func conanMigrationEvidence() nexusmigration.CapacityEvidence {
	zero, free, download := int64(0), int64(1<<30), int64(16<<20)
	now := time.Now().UTC()
	return nexusmigration.CapacityEvidence{TargetID: "conan-fixture-target", PoolID: "conan-fixture-pool", ObservedAt: now.Add(-time.Second), ValidUntil: now.Add(30 * time.Minute),
		FreeBytes: &free, DownloadBytes: &download, UploadBytes: &zero, BackupBytes: &zero, RestoreBytes: &zero, HeadroomBytes: &zero}
}

func TestPostgresMigrationTargetOnlyConan2Client(t *testing.T) {
	if os.Getenv("CONAN_BINARY") == "" {
		if os.Getenv("CONAN_MIGRATION_REQUIRED") == "1" {
			t.Fatal("the pinned official Conan client is required")
		}
		t.Skip("set CONAN_BINARY to run the pinned official client")
	}
	f := newNexusConanPGFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	conan := os.Getenv("CONAN_BINARY")
	work, producer := t.TempDir(), t.TempDir()
	ownedHomes := map[string]bool{producer: true}
	sourceStore, sourceObjects := repository.NewMemoryStore(), objectstore.NewMemoryStore()
	sourceRepo, err := sourceStore.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "producer-input", Format: repository.FormatConan})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = sourceStore.ReplaceRepositoryGrants(ctx, sourceRepo.ID, []repository.RepositoryGrant{{Principal: "build-agent", Scopes: []string{"repositories:write"}}}, "1"); err != nil {
		t.Fatal(err)
	}
	sourceHandler := newTestGateway(t, GatewayOptions{Store: sourceStore, ConanPublications: sourceStore, Storage: ObjectStores{repository.FormatConan: sourceObjects}, Adapter: TestAdapter{}, Authenticator: testAuthenticator()})
	var inputMu sync.Mutex
	inputs := map[string][]byte{}
	producerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			body, err := io.ReadAll(io.LimitReader(r.Body, (1<<20)+1))
			_ = r.Body.Close()
			if err != nil || len(body) > 1<<20 {
				t.Error("official producer wire capture failed")
				http.Error(w, "invalid producer body", 400)
				return
			}
			storage := strings.TrimPrefix(r.URL.Path, "/conan/"+sourceRepo.Name+"/v2/")
			if _, _, err := nexusmigration.ConanAssetIdentity(storage); err != nil {
				t.Error(err)
				http.Error(w, "invalid producer source coordinate", 400)
				return
			}
			inputMu.Lock()
			inputs[storage] = bytes.Clone(body)
			inputMu.Unlock()
			r.Body = io.NopCloser(bytes.NewReader(body))
		}
		sourceHandler.ServeHTTP(w, r)
	}))
	t.Cleanup(producerServer.Close)
	run := func(home string, args ...string) string {
		t.Helper()
		if !ownedHomes[home] || !filepath.IsAbs(home) || filepath.Clean(home) != home {
			t.Fatal("refuse to run Conan outside an explicitly owned fixture home")
		}
		command := exec.CommandContext(ctx, conan, args...)
		command.Dir = work
		command.Env = append(conanFixtureEnvironment(home), "CONAN_LOGIN_USERNAME_GATEWAY=build-agent", "CONAN_PASSWORD_GATEWAY=resolver-secret")
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("official Conan %v: %v\n%s", args, err, output)
		}
		return string(output)
	}
	prepare := func(home, remote string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Join(home, "profiles"), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(home, "profiles", "default"), []byte("[settings]\nos=Linux\narch=x86_64\n"), 0600); err != nil {
			t.Fatal(err)
		}
		run(home, "remote", "remove", "conancenter")
		run(home, "remote", "add", "gateway", remote)
		run(home, "remote", "auth", "gateway", "--force", "-cc", "core:non_interactive=True")
	}
	if version := run(producer, "--version"); strings.TrimSpace(version) != "Conan version 2.21.0" {
		t.Fatalf("expected the fixed official Conan 2.21.0 client: %s", version)
	}
	prepare(producer, producerServer.URL+"/conan/"+sourceRepo.Name)
	recipe := `from conan import ConanFile
from conan.tools.files import copy
import os
class Fixture(ConanFile):
    name = "gateway-upload"
    version = "1.0"
    package_type = "header-library"
    exports = "LICENSE"
    exports_sources = "gateway.h"
    def package(self):
        copy(self, "gateway.h", self.source_folder, os.path.join(self.package_folder, "include"))
`
	for name, content := range map[string]string{"conanfile.py": recipe, "LICENSE": "owned Nexus Conan migration fixture\n"} {
		if err = os.WriteFile(filepath.Join(work, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	identities := []conanFixtureIdentity{}
	headers := []string{"#define MIGRATED_REVISION 1\n", "#define MIGRATED_REVISION 2\n"}
	for i, header := range headers {
		if err = os.WriteFile(filepath.Join(work, "gateway.h"), []byte(header), 0600); err != nil {
			t.Fatal(err)
		}
		run(producer, "create", ".", "--user=fixture", "--channel=stable", "-nr")
		listing := filepath.Join(work, fmt.Sprintf("producer-%d.json", i))
		run(producer, "list", "gateway-upload/1.0@fixture/stable#latest:*#latest", "--format=json", "--out-file="+listing)
		identities = append(identities, readConanFixtureIdentity(t, listing, "Local Cache"))
		run(producer, "upload", "gateway-upload/1.0@fixture/stable#*:*#*", "-r=gateway", "--confirm")
	}
	producerServer.Close()
	if identities[0].recipe == identities[1].recipe || identities[0].packageRevision == identities[1].packageRevision || len(inputs) != 14 {
		t.Fatalf("official producer did not supply two distinct complete historical trees: identities=%+v assets=%d", identities, len(inputs))
	}
	source, created, sourceServer, remote := conanMigrationSource(t, f, inputs)
	publication := NewNexusMigrationPublication(f.store, SharedObjectStores(f.objects), "conan-fixture-target", "conan-fixture-pool").WithConan(NewNexusConanMigrationPublication(f.store, f.store, f.objects))
	ledger := migrationpg.New(f.db)
	service := nexusmigration.New(ledger, source, publication, nexusmigration.Options{TargetID: "conan-fixture-target", PoolID: "conan-fixture-pool"})
	batch, err := service.Start(ctx, nexusmigration.StartInput{SourceID: created.ID, SourceVersion: created.Version, PolicyVersion: nexusmigration.PolicyVersion,
		Mappings: []nexusmigration.Mapping{{SourceRepository: "source", TargetRepositoryID: f.repo.ID}}, CapacityEvidence: conanMigrationEvidence()}, "actual-migration-admin", uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	if err = service.RunOnce(ctx, []string{"conan"}); err != nil {
		t.Fatal(err)
	}
	batch, err = service.Get(ctx, batch.ID)
	if err != nil || batch.State != "completed" || batch.Counts.Committed != 4 || batch.VisibleIdentities != 4 || batch.Counts.Unsupported != 0 {
		t.Fatalf("complete historical migration=%+v %v", batch, err)
	}
	entries, _, err := service.Entries(ctx, batch.ID, 100, "")
	if err != nil || len(entries) != 4 {
		t.Fatalf("durable exact revision inventory=%d %v", len(entries), err)
	}
	for _, entry := range entries {
		for _, asset := range entry.Assets {
			if asset.SHA256 != fmt.Sprintf("%x", sha256.Sum256(inputs[asset.SourcePath])) || !asset.Verified {
				t.Fatalf("ledger asset differs from original official input: %+v", asset)
			}
		}
	}
	remote.mu.Lock()
	pages, gets, denied := remote.pages, remote.gets, remote.denied
	remote.mu.Unlock()
	// Seven complete inventory pages are enumerated and then re-observed;
	// the original fourteen payload reads and all read-only checks stay exact.
	if pages != 2*7 || gets != 14 || denied != 0 {
		t.Fatalf("source REST-only complete pagination/read-only boundary pages=%d gets=%d denied=%d", pages, gets, denied)
	}
	sourceServer.Close()
	_ = f.store.Close()
	restartNexusConanOwnedServices(t, ctx)
	f.store, err = repository.NewPostgresStore(f.databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.store.Close() })
	target := httptest.NewServer(newTestGateway(t, GatewayOptions{Store: f.store, ConanPublications: f.store, Storage: ObjectStores{repository.FormatConan: f.objects}, Adapter: TestAdapter{}, Authenticator: testAuthenticator()}))
	t.Cleanup(target.Close)
	assertNexusConanNativeBytes(t, target.Config.Handler, f.repo, entries, inputs)
	for i, identity := range identities {
		consumer := t.TempDir()
		ownedHomes[consumer] = true
		prepare(consumer, target.URL+"/conan/"+f.repo.Name)
		run(consumer, "install", "--requires=gateway-upload/1.0@fixture/stable#"+identity.recipe, "-r=gateway", "--build=never")
		ref := "gateway-upload/1.0@fixture/stable#" + identity.recipe + ":" + identity.packageID + "#" + identity.packageRevision
		path := strings.TrimSpace(run(consumer, "cache", "path", ref))
		got, err := os.ReadFile(filepath.Join(path, "include", "gateway.h"))
		if err != nil || string(got) != headers[i] {
			t.Fatalf("cold installed historical exact package bytes=%q %v", got, err)
		}
	}
	t.Logf("official Conan 2.21.0; source stopped; two historical RREV/PREV trees and 14 original assets migrated; target-only new homes installed both exact revisions; manifest=%s", batch.ManifestSHA256)
}

func restartNexusConanOwnedServices(t *testing.T, ctx context.Context) {
	t.Helper()
	project, file := os.Getenv("TEST_CONAN_COMPOSE_PROJECT"), os.Getenv("TEST_CONAN_COMPOSE_FILE")
	override := os.Getenv("TEST_CONAN_COMPOSE_OVERRIDE")
	if project == "" && os.Getenv("CONAN_MIGRATION_REQUIRED") != "1" {
		return
	}
	if !strings.HasPrefix(project, "artifact-gateway-conan-") || !filepath.IsAbs(file) || filepath.Base(file) != "compose.integration.yml" ||
		!filepath.IsAbs(override) || filepath.Base(override) != "compose.conan.yml" {
		t.Fatal("refuse to restart an unowned Conan migration fixture")
	}
	before := map[string]string{}
	for _, service := range []string{"postgres", "rustfs"} {
		before[service] = conanOwnedServiceState(t, project, file, override, service)
	}
	for _, args := range [][]string{{"restart", "postgres", "rustfs"}, {"up", "-d", "--wait", "--wait-timeout", "45", "postgres", "rustfs"}, {"run", "--rm", "--no-deps", "rustfs-ready"}} {
		command := exec.CommandContext(ctx, "docker", append([]string{"compose", "--env-file", "/dev/null", "--project-name", project, "-f", file, "-f", override}, args...)...)
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("restart only owned Conan migration services: %v\n%s", err, output)
		}
	}
	for _, service := range []string{"postgres", "rustfs"} {
		after := conanOwnedServiceState(t, project, file, override, service)
		old, current := strings.Fields(before[service]), strings.Fields(after)
		if len(old) != 2 || len(current) != 2 || old[0] != current[0] || old[1] == current[1] {
			t.Fatalf("owned %s process did not restart in place: before=%q after=%q", service, before[service], after)
		}
		t.Logf("owned %s restart confirmed: container=%s StartedAt=%s -> %s", service, current[0], old[1], current[1])
	}
}
