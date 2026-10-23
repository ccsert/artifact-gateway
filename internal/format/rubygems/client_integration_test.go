//go:build integration

package rubygems

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ccsert/artifact-gateway/internal/authorization"
	"github.com/ccsert/artifact-gateway/internal/database"
	"github.com/ccsert/artifact-gateway/internal/objectstore"
	protocol "github.com/ccsert/artifact-gateway/internal/protocol/rubygems"
	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/ccsert/artifact-gateway/internal/rubygemspublication/pgstore"
	"github.com/ccsert/artifact-gateway/internal/testsupport"
	"github.com/google/uuid"
)

func ownedFile(t *testing.T, root, name, body string) {
	t.Helper()
	p := filepath.Join(root, name)
	if e := os.MkdirAll(filepath.Dir(p), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(p, []byte(body), 0600); e != nil {
		t.Fatal(e)
	}
}
func TestRubyGemsFixedOfficialClientColdTransitiveInstallAndRestart(t *testing.T) {
	image := os.Getenv("RUBYGEMS_TEST_IMAGE")
	dsn := os.Getenv("TEST_DATABASE_URL")
	if image == "" || dsn == "" || os.Getenv("TEST_RUSTFS_ENDPOINT") == "" {
		t.Skip("fixed official Ruby image and owned PostgreSQL/RustFS required")
	}
	if e := testsupport.ValidateIsolatedDatabaseURL(dsn); e != nil {
		t.Fatal(e)
	}
	work := t.TempDir()
	evidence := os.Getenv("RUBYGEMS_OWNED_EVIDENCE_DIR")
	if evidence == "" {
		t.Fatal("owned evidence directory required")
	}
	project := os.Getenv("RUBYGEMS_OWNED_PROJECT")
	if project == "" {
		t.Fatal("owned Docker project required")
	}
	admin := uuid.NewString()
	reader := uuid.NewString()
	commands := []map[string]any{}
	var mu sync.Mutex
	paths := []string{}
	t.Cleanup(func() {
		b, _ := json.MarshalIndent(map[string]any{"clientCommands": commands, "nativeRequestPaths": paths}, "", "  ")
		_ = os.WriteFile(filepath.Join(evidence, "official-client-evidence.json"), append(b, '\n'), 0600)
	})
	run := func(phase, name string, offline bool, args ...string) string {
		t.Helper()
		for _, dir := range []string{phase + "/home", phase + "/gems", phase + "/bundle", phase + "/bundle-user", phase + "/bundle-cache"} {
			if e := os.MkdirAll(filepath.Join(work, dir), 0700); e != nil {
				t.Fatal(e)
			}
		}
		container := project + "-cli-" + strings.ToLower(uuid.NewString())
		dockerArgs := []string{"run", "--rm", "--name", container, "--label", "com.artifact-gateway.rubygems-e2e.run=" + project, "--user", strconv.Itoa(os.Getuid()) + ":" + strconv.Itoa(os.Getgid()), "--mount", "type=bind,src=" + work + ",dst=/work", "--workdir", "/work", "--env", "HOME=/work/" + phase + "/home", "--env", "GEM_HOME=/work/" + phase + "/gems", "--env", "GEM_PATH=/work/" + phase + "/gems", "--env", "GEM_SPEC_CACHE=/work/" + phase + "/specs", "--env", "BUNDLE_PATH=/work/" + phase + "/bundle", "--env", "BUNDLE_USER_HOME=/work/" + phase + "/bundle-user", "--env", "BUNDLE_USER_CACHE=/work/" + phase + "/bundle-cache", "--env", "BUNDLE_IGNORE_CONFIG=1", "--env", "BUNDLE_DISABLE_SHARED_GEMS=true", "--env", "BUNDLE_RETRY=0", "--env", "BUNDLE_TIMEOUT=30", "--env", "GEM_HOST_API_KEY=" + admin}
		if offline {
			dockerArgs = append(dockerArgs, "--network", "none")
		} else if runtime.GOOS == "linux" {
			dockerArgs = append(dockerArgs, "--network", "host", "--add-host", "host.docker.internal:127.0.0.1")
		} else {
			dockerArgs = append(dockerArgs, "--add-host", "host.docker.internal:host-gateway")
		}
		dockerArgs = append(dockerArgs, image)
		dockerArgs = append(dockerArgs, args...)
		ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
		defer cancel()
		output, err := exec.CommandContext(ctx, "docker", dockerArgs...).CombinedOutput()
		if ctx.Err() != nil {
			cleanup, c := context.WithTimeout(context.Background(), 10*time.Second)
			_ = exec.CommandContext(cleanup, "docker", "rm", "-f", container).Run()
			c()
		}
		safe := strings.ReplaceAll(strings.ReplaceAll(string(output), admin, "[owned-admin-key]"), reader, "[owned-reader-key]")
		safeArgs := strings.ReplaceAll(strings.ReplaceAll(strings.Join(args, " "), admin, "[owned-admin-key]"), reader, "[owned-reader-key]")
		commands = append(commands, map[string]any{"phase": phase, "command": safeArgs, "output": safe, "exitSuccess": err == nil, "networkNone": offline})
		if err != nil {
			t.Fatalf("official %s: %v\n%s", name, err, safe)
		}
		return safe
	}
	versions := strings.TrimSpace(run("pack", "versions", true, "ruby", "-e", `require "rubygems"; require "bundler"; puts [RUBY_VERSION,Gem::VERSION,Bundler::VERSION].join(" ")`))
	if versions != "3.4.4 3.6.7 2.6.7" {
		t.Fatalf("fixed official toolchain required, got %q", versions)
	}
	proof := []map[string]any{}
	for _, spec := range []struct{ name, version, platform, dep, expr string }{{"owned_leaf", "1.0.0", "ruby", "", "'leaf'"}, {"owned_middle", "1.1.0", "ruby", "s.add_runtime_dependency('owned_leaf','>= 1.0','< 2.0')", "require 'owned_leaf'; 'middle-' + OWNED_LEAF"}, {"owned_top", "1.2.0", "ruby", "s.add_runtime_dependency('owned_middle','~> 1.1')", "require 'owned_middle'; 'top-' + OWNED_MIDDLE"}, {"owned_top", "1.3.0.pre", "ruby", "s.add_runtime_dependency('owned_middle','>= 1.1','< 2.0')", "require 'owned_middle'; 'pre-' + OWNED_MIDDLE"}, {"owned_platform", "1.0.0", "x86_64-linux", "s.add_runtime_dependency('owned_leaf','= 1.0.0')", "'platform'"}} {
		file := spec.name + "-" + spec.version + "-" + spec.platform + ".gemspec"
		content := "Gem::Specification.new do |s|\ns.name=" + strconv.Quote(spec.name) + ";s.version=" + strconv.Quote(spec.version) + ";s.platform=" + strconv.Quote(spec.platform) + ";s.summary='Owned fixture';s.authors=['Gateway'];s.licenses=['MIT'];s.files=['lib/" + spec.name + ".rb'];s.require_paths=['lib'];s.required_ruby_version='>= 3.0';s.metadata={'source_code_uri'=>'https://example.invalid/owned','changelog_uri'=>'https://example.invalid/tail'};" + spec.dep + "\ns.description='Benign tail after dependency fields';s.post_install_message='owned metadata tail';\nend\n"
		ownedFile(t, work, file, content)
		ownedFile(t, work, "lib/"+spec.name+".rb", strings.ToUpper(spec.name)+" = begin "+spec.expr+" end\n")
		run("pack", "build-"+spec.name, true, "gem", "build", file)
		full := spec.name + "-" + spec.version
		if spec.platform != "ruby" {
			full += "-" + spec.platform
		}
		data, e := os.ReadFile(filepath.Join(work, full+".gem"))
		if e != nil {
			t.Fatal(e)
		}
		in, e := protocol.Inspect(bytes.NewReader(data))
		if e != nil {
			t.Fatalf("official archive rejected %s: %v", full, e)
		}
		if in.Metadata.Name != spec.name || in.Metadata.Version != spec.version || in.Metadata.Platform != spec.platform || spec.dep != "" && len(in.Metadata.Dependencies) != 1 {
			t.Fatalf("official identity/deps %s %+v", full, in.Metadata)
		}
		sum := sha256.Sum256(data)
		proof = append(proof, map[string]any{"name": spec.name, "version": spec.version, "platform": spec.platform, "filename": full + ".gem", "size": len(data), "sha256": hex.EncodeToString(sum[:]), "dependencies": in.Metadata.Dependencies})
	}
	encoded, _ := json.MarshalIndent(proof, "", "  ")
	if e := os.WriteFile(filepath.Join(evidence, "original-gem-closures.json"), append(encoded, '\n'), 0600); e != nil {
		t.Fatal(e)
	}
	var repos *repository.PostgresStore
	var repo repository.HostedRepository
	var server *httptest.Server
	var address string
	bucket := "ruby-owned-" + uuid.NewString()
	start := func() {
		t.Helper()
		var e error
		repos, e = repository.NewPostgresStore(dsn)
		if e != nil {
			t.Fatal(e)
		}
		db, e := database.OpenPostgres(dsn, database.DefaultPoolConfig())
		if e != nil {
			t.Fatal(e)
		}
		locks, e := database.OpenPostgres(dsn, database.DefaultArtifactLockPoolConfig())
		if e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() { _ = db.Close(); _ = locks.Close(); _ = repos.Close() })
		store, e := pgstore.New(db, locks)
		if e != nil {
			t.Fatal(e)
		}
		objects, e := objectstore.NewRustFSStore(os.Getenv("TEST_RUSTFS_ENDPOINT"), os.Getenv("TEST_RUSTFS_ACCESS_KEY"), os.Getenv("TEST_RUSTFS_SECRET_KEY"), bucket)
		if e != nil {
			t.Fatal(e)
		}
		if e = objects.EnsureBucket(t.Context()); e != nil {
			t.Fatal(e)
		}
		if repo.ID == "" {
			repo, e = repos.CreateHostedRepository(t.Context(), repository.HostedRepository{ID: uuid.NewString(), Name: "ruby-owned-" + uuid.NewString(), Format: repository.FormatRubyGems, Type: repository.RepositoryTypeHosted})
			if e != nil {
				t.Fatal(e)
			}
		}
		auth := authorization.Authenticator{AdminToken: admin, AdminActor: "ruby-gate-operator", ResolverToken: reader, ResolverActor: "ruby-gate-reader", RepositoryReaders: map[string][]string{"ruby-gate-reader": {repo.Name}}}
		module, e := New(Options{Store: store, Repositories: repos, Objects: objects, Authenticator: auth, Authorizer: authorization.RepositoryAuthorizer{Grants: repos, Legacy: auth}, Audit: repos, Anonymous: repos})
		if e != nil {
			t.Fatal(e)
		}
		server = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			paths = append(paths, r.Method+" "+r.URL.Path)
			mu.Unlock()
			module.ServeHTTP(w, r)
		}))
		if address == "" {
			server.Listener, e = net.Listen("tcp", "0.0.0.0:0")
		} else {
			server.Listener, e = net.Listen("tcp", address)
		}
		if e != nil {
			t.Fatal(e)
		}
		address = server.Listener.Addr().String()
		server.Start()
		t.Cleanup(server.Close)
	}
	start()
	port := strconv.Itoa(server.Listener.Addr().(*net.TCPAddr).Port)
	source := "http://host.docker.internal:" + port + "/rubygems/" + repo.Name + "/"
	readSource := strings.Replace(source, "http://", "http://gateway:"+reader+"@", 1)
	for _, record := range proof {
		run("push", "push-"+record["filename"].(string), false, "gem", "push", record["filename"].(string), "--host", source)
	}
	// Native retries retain exactly the same original closure and one audit.
	run("push", "idempotent-push", false, "gem", "push", "owned_top-1.2.0.gem", "--host", source)
	for _, record := range proof {
		w := request(server.Config.Handler, "GET", "/rubygems/"+repo.Name+"/gems/"+record["filename"].(string), nil, admin)
		sum := sha256.Sum256(w.Body.Bytes())
		if w.Code != 200 || w.Body.Len() != record["size"].(int) || hex.EncodeToString(sum[:]) != record["sha256"] {
			t.Fatalf("original closure %v status %d", record["filename"], w.Code)
		}
	}
	install := func(phase string) {
		t.Helper()
		run(phase, "gem-install", false, "gem", "install", "owned_top", "--version", "1.2.0", "--clear-sources", "--source", readSource, "--no-document", "--verbose")
		out := run(phase, "execute-transitive", true, "ruby", "-e", `require 'owned_top'; abort OWNED_TOP unless OWNED_TOP == 'top-middle-leaf'; puts OWNED_TOP`)
		if !strings.Contains(out, "top-middle-leaf") {
			t.Fatal(out)
		}
	}
	install("gem-cold-before")
	ownedFile(t, work, "Gemfile", "source "+strconv.Quote(readSource)+"\ngem 'owned_top', '= 1.2.0'\n")
	run("bundle-cold-before", "bundle-install", false, "bundle", "_2.6.7_", "install", "--jobs", "1", "--retry", "0")
	run("bundle-cold-before", "bundle-execute", true, "bundle", "_2.6.7_", "exec", "ruby", "-e", `require 'owned_top'; abort unless OWNED_TOP == 'top-middle-leaf'; puts OWNED_TOP`)
	// Explicit full-index proves the legacy list and generated Gem::Specification
	// path, independently of the compact index used by the previous install.
	run("bundle-full-index", "legacy-bundle-install", false, "bundle", "_2.6.7_", "install", "--full-index", "--jobs", "1", "--retry", "0")
	run("pre-cold", "prerelease-install", false, "gem", "install", "owned_top", "--version", "1.3.0.pre", "--prerelease", "--clear-sources", "--source", readSource, "--no-document")
	run("platform-cold", "platform-fetch", false, "gem", "fetch", "owned_platform", "--version", "1.0.0", "--platform", "x86_64-linux", "--clear-sources", "--source", readSource)
	audits, e := repos.ListAudits(t.Context(), repository.AuditQuery{Repository: repo.Name, Operation: "rubygems.publish", Limit: 100})
	if e != nil || len(audits) != len(proof) {
		t.Fatalf("durable idempotent audit %d %v", len(audits), e)
	}
	server.Close()
	_ = repos.Close()
	for _, service := range strings.Split(os.Getenv("RUBYGEMS_OWNED_RESTART_CONTAINERS"), ",") {
		if service == "" {
			t.Fatal("owned database/object service restart identities required")
		}
		ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
		out, e := exec.CommandContext(ctx, "docker", "restart", service).CombinedOutput()
		cancel()
		if e != nil {
			t.Fatalf("owned service restart: %v %s", e, out)
		}
	}
	// Readiness is bounded separately from individual native requests. No client
	// cache or default source is reused after physical dependency restart.
	deadline := time.Now().Add(30 * time.Second)
	for {
		db, e := database.OpenPostgres(dsn, database.DefaultPoolConfig())
		if e == nil {
			e = db.PingContext(t.Context())
			_ = db.Close()
		}
		if e == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("owned PostgreSQL restart did not become ready")
		}
		time.Sleep(200 * time.Millisecond)
	}
	start()
	install("gem-cold-after")
	_ = os.Remove(filepath.Join(work, "Gemfile.lock"))
	run("bundle-cold-after", "restarted-bundle-install", false, "bundle", "_2.6.7_", "install", "--jobs", "1", "--retry", "0")
	run("bundle-cold-after", "restarted-bundle-execute", true, "bundle", "_2.6.7_", "exec", "ruby", "-e", `require 'owned_top'; abort unless OWNED_TOP == 'top-middle-leaf'; puts OWNED_TOP`)
	mu.Lock()
	all := strings.Join(paths, "\n")
	mu.Unlock()
	for _, required := range []string{"/versions", "/info/owned_top", "/info/owned_middle", "/info/owned_leaf", "/specs.4.8.gz", "/quick/Marshal.4.8/owned_top-1.2.0.gemspec.rz", "/gems/owned_leaf-1.0.0.gem"} {
		if !strings.Contains(all, required) {
			t.Fatalf("native protocol branch not exercised: %s", required)
		}
	}
	t.Logf("official toolchain %s; %d original closures verified; real compact and legacy cold three-level installs survived owned PostgreSQL/RustFS restart", versions, len(proof))
}
