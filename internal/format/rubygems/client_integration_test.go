//go:build integration

package rubygems

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ccsert/artifact-gateway/internal/database"
	"github.com/ccsert/artifact-gateway/internal/objectstore"
	protocol "github.com/ccsert/artifact-gateway/internal/protocol/rubygems"
	"github.com/ccsert/artifact-gateway/internal/repository"
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
		if strings.HasPrefix(phase, "bundle-") {
			gemfile := filepath.Join(work, phase, "Gemfile")
			if _, e := os.Stat(gemfile); os.IsNotExist(e) {
				data, e := os.ReadFile(filepath.Join(work, "Gemfile"))
				if e != nil {
					t.Fatal(e)
				}
				ownedFile(t, work, phase+"/Gemfile", string(data))
			}
			dockerArgs = append(dockerArgs, "--env", "BUNDLE_GEMFILE=/work/"+phase+"/Gemfile")
		}
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
	var stopGateway func()
	start := func() {
		t.Helper()
		var e error
		repos, e = repository.NewPostgresStore(dsn)
		if e != nil {
			t.Fatal(e)
		}
		ownedRepos := repos
		t.Cleanup(func() { _ = ownedRepos.Close() })
		objects, e := objectstore.NewRustFSStore(os.Getenv("TEST_RUSTFS_ENDPOINT"), os.Getenv("TEST_RUSTFS_ACCESS_KEY"), os.Getenv("TEST_RUSTFS_SECRET_KEY"), bucket)
		if e != nil {
			t.Fatal(e)
		}
		if e = objects.EnsureBucket(t.Context()); e != nil {
			t.Fatal(e)
		}
		var backend string
		stopGateway, backend = startRealRubyGateway(t, admin, reader, bucket, evidence)
		target, e := url.Parse(backend)
		if e != nil {
			t.Fatal(e)
		}
		proxy := httputil.NewSingleHostReverseProxy(target)
		server = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			paths = append(paths, r.Method+" "+r.URL.Path)
			mu.Unlock()
			proxy.ServeHTTP(w, r)
		}))
		_ = server.Listener.Close()
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
		server.URL = "http://127.0.0.1:" + strconv.Itoa(server.Listener.Addr().(*net.TCPAddr).Port)
		t.Cleanup(server.Close)
		if repo.ID == "" {
			name := "ruby-owned-" + uuid.NewString()
			r, e := http.NewRequestWithContext(t.Context(), "POST", server.URL+"/api/v2/previews/rubygems/repositories", strings.NewReader(`{"name":"`+name+`"}`))
			if e != nil {
				t.Fatal(e)
			}
			r.Header.Set("Authorization", "Bearer "+admin)
			r.Header.Set("Content-Type", "application/json")
			res, e := (&http.Client{Timeout: 30 * time.Second}).Do(r)
			if e != nil {
				t.Fatal(e)
			}
			raw, _ := io.ReadAll(res.Body)
			_ = res.Body.Close()
			if res.StatusCode != 201 {
				t.Fatalf("production preview creation %d %s", res.StatusCode, raw)
			}
			var result struct{ ID string }
			if e = json.Unmarshal(raw, &result); e != nil || result.ID == "" {
				t.Fatal("missing production repository id")
			}
			repo, e = repos.GetHostedRepository(t.Context(), result.ID)
			if e != nil {
				t.Fatal(e)
			}
			if _, e = repos.ReplaceRepositoryGrants(t.Context(), repo.ID, []repository.RepositoryGrant{{Principal: "ruby-gate-reader", Scopes: []string{"repositories:read"}}}, "1"); e != nil {
				t.Fatal(e)
			}
		}
	}
	start()
	port := strconv.Itoa(server.Listener.Addr().(*net.TCPAddr).Port)
	source := "http://host.docker.internal:" + port + "/rubygems/" + repo.Name + "/"
	readSource := strings.Replace(source, "http://", "http://gateway:"+reader+"@", 1)
	for _, record := range proof {
		run("push", "push-"+record["filename"].(string), false, "gem", "push", record["filename"].(string), "--host", strings.TrimSuffix(source, "/"))
	}
	// Native retries retain exactly the same original closure and one audit.
	run("push", "idempotent-push", false, "gem", "push", "owned_top-1.2.0.gem", "--host", strings.TrimSuffix(source, "/"))
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
	stopGateway()
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

func startRealRubyGateway(t *testing.T, admin, reader, bucket, evidence string) (func(), string) {
	t.Helper()
	binary := os.Getenv("RUBYGEMS_GATEWAY_BIN")
	if binary == "" {
		t.Fatal("full production gateway binary required")
	}
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	address := listener.Addr().String()
	_ = listener.Close()
	log, e := os.OpenFile(filepath.Join(evidence, "production-gateway.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if e != nil {
		t.Fatal(e)
	}
	cmd := exec.Command(binary)
	for _, value := range os.Environ() {
		if !strings.HasPrefix(value, "GATEWAY_") {
			cmd.Env = append(cmd.Env, value)
		}
	}
	cmd.Env = append(cmd.Env, "GATEWAY_DATABASE_URL="+os.Getenv("TEST_DATABASE_URL"), "GATEWAY_RUSTFS_ENDPOINT="+os.Getenv("TEST_RUSTFS_ENDPOINT"), "GATEWAY_RUSTFS_ACCESS_KEY="+os.Getenv("TEST_RUSTFS_ACCESS_KEY"), "GATEWAY_RUSTFS_SECRET_KEY="+os.Getenv("TEST_RUSTFS_SECRET_KEY"), "GATEWAY_RUSTFS_BUCKET="+bucket, "GATEWAY_ADMIN_TOKEN="+admin, "GATEWAY_ADMIN_ACTOR=ruby-gate-operator", "GATEWAY_RESOLVER_TOKEN="+reader, "GATEWAY_RESOLVER_ACTOR=ruby-gate-reader", "GATEWAY_RUBYGEMS_PREVIEW_ENABLED=true", "GATEWAY_REPOSITORY_PRESETS=none", "GATEWAY_NODE_ROLES=api", "GATEWAY_LISTEN_ADDRESS="+address, "GATEWAY_ACCESS_LOG=limited")
	cmd.Stdout, cmd.Stderr = log, log
	if e = cmd.Start(); e != nil {
		_ = log.Close()
		t.Fatal(e)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	stopped := false
	stop := func() {
		if stopped {
			return
		}
		stopped = true
		_ = cmd.Process.Signal(os.Interrupt)
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			_ = cmd.Process.Kill()
			<-done
		}
		_ = log.Close()
		b, _ := json.Marshal(map[string]any{"pid": cmd.Process.Pid, "waitReturned": true, "productionBinary": true, "observer": "owned transparent forwarding HTTP server; no protocol, authentication, persistence or byte responses mocked"})
		_ = os.WriteFile(filepath.Join(evidence, "gateway-process-"+strconv.Itoa(cmd.Process.Pid)+".json"), append(b, '\n'), 0600)
	}
	t.Cleanup(stop)
	base := "http://" + address
	client := &http.Client{Timeout: time.Second}
	deadline := time.Now().Add(25 * time.Second)
	for time.Now().Before(deadline) {
		res, e := client.Get(base + "/readyz")
		if e == nil {
			_ = res.Body.Close()
			if res.StatusCode == 200 || res.StatusCode == 204 {
				return stop, base
			}
		}
		select {
		case e := <-done:
			stopped = true
			_ = log.Close()
			t.Fatalf("production gateway exited before ready: %v", e)
		case <-time.After(100 * time.Millisecond):
		}
	}
	stop()
	t.Fatal("production gateway did not become ready")
	return stop, base
}
