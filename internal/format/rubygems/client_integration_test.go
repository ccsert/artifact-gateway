//go:build integration

package rubygems

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
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

type rubyNativeObservation struct {
	Phase    string             `json:"phase"`
	Method   string             `json:"method"`
	Path     string             `json:"path"`
	Status   int                `json:"status"`
	Size     int                `json:"size"`
	SHA256   string             `json:"sha256,omitempty"`
	Metadata *protocol.Metadata `json:"metadata,omitempty"`
	Error    string             `json:"error,omitempty"`
}

type rubyObservedResponse struct {
	http.ResponseWriter
	status int
	body   bytes.Buffer
}

func (w *rubyObservedResponse) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
	w.ResponseWriter.WriteHeader(status)
}
func (w *rubyObservedResponse) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	n, err := w.ResponseWriter.Write(data)
	_, _ = w.body.Write(data[:n])
	return n, err
}
func (w *rubyObservedResponse) Flush() {
	_ = http.NewResponseController(w.ResponseWriter).Flush()
}
func (w *rubyObservedResponse) Unwrap() http.ResponseWriter { return w.ResponseWriter }

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
	observations := []rubyNativeObservation{}
	phaseProofs := []map[string]any{}
	isolation := []map[string]any{}
	phaseStarts := map[string]int{}
	phaseRoots := map[string]string{}
	phaseLocks := map[string]string{}
	governanceEvidence := map[string]any{}
	activePhase := ""
	gemfileBody := ""
	var observing sync.WaitGroup
	t.Cleanup(func() {
		observing.Wait()
		mu.Lock()
		defer mu.Unlock()
		b, _ := json.MarshalIndent(map[string]any{"clientCommands": commands, "nativeRequestPaths": paths, "nativeResponses": observations, "phaseOriginalClosureProofs": phaseProofs, "consumerIsolation": isolation, "governance": governanceEvidence}, "", "  ")
		_ = os.WriteFile(filepath.Join(evidence, "official-client-evidence.json"), append(b, '\n'), 0600)
	})
	runExpected := func(phase, name string, offline, wantSuccess bool, args ...string) string {
		t.Helper()
		consumer := phase != "pack" && phase != "push"
		root, initialized := phaseRoots[phase]
		if !initialized {
			root = filepath.Join(work, "consumers", phase)
			if !consumer {
				root = filepath.Join(work, "producer")
			} else if _, err := os.Stat(root); !os.IsNotExist(err) {
				t.Fatalf("consumer phase must start with an absent independent root: %s %v", phase, err)
			}
			for _, dir := range []string{"project", "home", "gems", "specs", "bundle", "bundle-user", "bundle-cache"} {
				if e := os.MkdirAll(filepath.Join(root, dir), 0700); e != nil {
					t.Fatal(e)
				}
			}
			if consumer {
				entries, err := os.ReadDir(filepath.Join(root, "project"))
				if err != nil || len(entries) != 0 {
					t.Fatalf("consumer project is not empty: %s %v", phase, err)
				}
				isolation = append(isolation, map[string]any{"phase": phase, "independentEmptyRoot": true, "cwdInitiallyEmpty": true, "emptyHomeAndCaches": true, "producerDirectoryMounted": false, "containerCwd": "/work/project", "targetOnlySource": true})
			}
			ownedFile(t, root, "home/gemrc", "---\n:sources: []\n:update_sources: false\n")
			if strings.HasPrefix(phase, "bundle-") {
				if gemfileBody == "" {
					t.Fatal("target-only Gemfile required")
				}
				ownedFile(t, root, "project/Gemfile", gemfileBody)
				if lock := phaseLocks[phase]; lock != "" {
					ownedFile(t, root, "project/Gemfile.lock", lock)
				}
			}
			phaseRoots[phase] = root
		}
		mu.Lock()
		activePhase = phase
		if _, exists := phaseStarts[phase]; !exists {
			phaseStarts[phase] = len(observations)
		}
		mu.Unlock()
		container := project + "-cli-" + strings.ToLower(uuid.NewString())
		dockerArgs := []string{"run", "--rm", "--name", container, "--label", "com.artifact-gateway.rubygems-e2e.run=" + project, "--user", strconv.Itoa(os.Getuid()) + ":" + strconv.Itoa(os.Getgid()), "--mount", "type=bind,src=" + root + ",dst=/work", "--workdir", "/work/project", "--env", "HOME=/work/home", "--env", "GEMRC=/work/home/gemrc", "--env", "GEM_HOME=/work/gems", "--env", "GEM_PATH=/work/gems", "--env", "GEM_SPEC_CACHE=/work/specs", "--env", "BUNDLE_PATH=/work/bundle", "--env", "BUNDLE_USER_HOME=/work/bundle-user", "--env", "BUNDLE_USER_CACHE=/work/bundle-cache", "--env", "BUNDLE_IGNORE_CONFIG=1", "--env", "BUNDLE_DISABLE_SHARED_GEMS=true", "--env", "BUNDLE_RETRY=0", "--env", "BUNDLE_TIMEOUT=30"}
		if !consumer || strings.HasPrefix(phase, "yank-") {
			dockerArgs = append(dockerArgs, "--env", "GEM_HOST_API_KEY="+admin)
		}
		if strings.HasPrefix(phase, "bundle-") {
			dockerArgs = append(dockerArgs, "--env", "BUNDLE_GEMFILE=/work/project/Gemfile")
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
		observing.Wait()
		safe := strings.ReplaceAll(strings.ReplaceAll(string(output), admin, "[owned-admin-key]"), reader, "[owned-reader-key]")
		safeArgs := strings.ReplaceAll(strings.ReplaceAll(strings.Join(args, " "), admin, "[owned-admin-key]"), reader, "[owned-reader-key]")
		commands = append(commands, map[string]any{"phase": phase, "command": safeArgs, "output": safe, "exitSuccess": err == nil, "expectedSuccess": wantSuccess, "networkNone": offline, "consumerOnlyMount": consumer, "containerCwd": "/work/project"})
		if wantSuccess && err != nil {
			t.Fatalf("official %s: %v\n%s", name, err, safe)
		}
		if !wantSuccess {
			var exit *exec.ExitError
			if ctx.Err() != nil || !errors.As(err, &exit) || exit.ExitCode() <= 0 {
				t.Fatalf("official %s expected native rejection, got %v\n%s", name, err, safe)
			}
			commands[len(commands)-1]["expectedNonzeroExitCode"] = exit.ExitCode()
		}
		return safe
	}
	run := func(phase, name string, offline bool, args ...string) string {
		return runExpected(phase, name, offline, true, args...)
	}
	versions := strings.TrimSpace(run("pack", "versions", true, "ruby", "-e", `require "rubygems"; require "bundler"; puts [RUBY_VERSION,Gem::VERSION,Bundler::VERSION].join(" ")`))
	if versions != "3.4.4 3.6.7 2.6.7" {
		t.Fatalf("fixed official toolchain required, got %q", versions)
	}
	proof := []map[string]any{}
	originalMetadata := map[string]protocol.Metadata{}
	producer := filepath.Join(work, "producer", "project")
	for _, spec := range []struct{ name, version, platform, dep, expr string }{{"owned_leaf", "1.0.0", "ruby", "", "'leaf'"}, {"owned_middle", "1.1.0", "ruby", "s.add_runtime_dependency('owned_leaf','>= 1.0','< 2.0')", "require 'owned_leaf'; 'middle-' + OWNED_LEAF"}, {"owned_top", "1.2.0", "ruby", "s.add_runtime_dependency('owned_middle','~> 1.1')", "require 'owned_middle'; 'top-' + OWNED_MIDDLE"}, {"owned_top", "1.3.0.pre", "ruby", "s.add_runtime_dependency('owned_middle','>= 1.1','< 2.0')", "require 'owned_middle'; 'pre-' + OWNED_MIDDLE"}, {"owned_platform", "1.0.0", "x86_64-linux", "s.add_runtime_dependency('owned_leaf','= 1.0.0')", "'platform'"}, {"owned_variant", "1.0.0", "ruby", "", "'variant'"}, {"owned_variant", "1.0.0", "java", "", "'variant'"}} {
		file := spec.name + "-" + spec.version + "-" + spec.platform + ".gemspec"
		content := "Gem::Specification.new do |s|\ns.name=" + strconv.Quote(spec.name) + ";s.version=" + strconv.Quote(spec.version) + ";s.platform=" + strconv.Quote(spec.platform) + ";s.summary='Owned fixture';s.authors=['Gateway'];s.licenses=['MIT'];s.files=['lib/" + spec.name + ".rb'];s.require_paths=['lib'];s.required_ruby_version='>= 3.0';s.metadata={'source_code_uri'=>'https://example.invalid/owned','changelog_uri'=>'https://example.invalid/tail'};" + spec.dep + "\ns.description='Benign tail after dependency fields';s.post_install_message='owned metadata tail';\nend\n"
		ownedFile(t, producer, file, content)
		ownedFile(t, producer, "lib/"+spec.name+".rb", strings.ToUpper(spec.name)+" = begin "+spec.expr+" end\n")
		run("pack", "build-"+spec.name, true, "gem", "build", file)
		full := spec.name + "-" + spec.version
		if spec.platform != "ruby" {
			full += "-" + spec.platform
		}
		data, e := os.ReadFile(filepath.Join(producer, full+".gem"))
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
		originalMetadata[full+".gem"] = in.Metadata
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
			observing.Add(1)
			defer observing.Done()
			mu.Lock()
			phase := activePhase
			paths = append(paths, r.Method+" "+r.URL.Path)
			mu.Unlock()
			observed := &rubyObservedResponse{ResponseWriter: w}
			proxy.ServeHTTP(observed, r)
			entry := rubyNativeObservation{Phase: phase, Method: r.Method, Path: r.URL.Path, Status: observed.status, Size: observed.body.Len()}
			if r.Method == "GET" && strings.HasSuffix(r.URL.Path, ".gem") {
				sum := sha256.Sum256(observed.body.Bytes())
				entry.SHA256 = hex.EncodeToString(sum[:])
				parsed, err := protocol.Inspect(bytes.NewReader(observed.body.Bytes()))
				if err != nil {
					entry.Error = err.Error()
				} else {
					entry.Metadata = &parsed.Metadata
				}
			}
			mu.Lock()
			observations = append(observations, entry)
			mu.Unlock()
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
	verifyPhase := func(phase string, filenames, requiredPaths []string) {
		t.Helper()
		observing.Wait()
		mu.Lock()
		start, exists := phaseStarts[phase]
		if !exists {
			mu.Unlock()
			t.Fatalf("consumer phase was never started: %s", phase)
		}
		window := append([]rubyNativeObservation{}, observations[start:]...)
		mu.Unlock()
		expected := map[string]map[string]any{}
		for _, filename := range filenames {
			for _, record := range proof {
				if record["filename"] == filename {
					expected[filename] = record
				}
			}
			if expected[filename] == nil {
				t.Fatalf("unknown original required by %s: %s", phase, filename)
			}
		}
		seen := map[string]int{}
		for _, response := range window {
			if response.Phase != phase {
				t.Fatalf("observer window crossed consumer phases: %s contains %s", phase, response.Phase)
			}
			if response.Method != "GET" || !strings.HasSuffix(response.Path, ".gem") {
				continue
			}
			filename := strings.TrimPrefix(response.Path, "/rubygems/"+repo.Name+"/gems/")
			original := expected[filename]
			if original == nil || response.Status != http.StatusOK || response.Size != original["size"] || response.SHA256 != original["sha256"] || response.Error != "" || response.Metadata == nil || !reflect.DeepEqual(*response.Metadata, originalMetadata[filename]) {
				t.Fatalf("phase %s did not receive a complete matching original: %+v", phase, response)
			}
			seen[filename]++
		}
		for _, filename := range filenames {
			if seen[filename] == 0 {
				t.Fatalf("phase %s made no full original GET for %s", phase, filename)
			}
		}
		for _, required := range requiredPaths {
			method, path, explicitMethod := strings.Cut(required, " ")
			if !explicitMethod {
				method, path = http.MethodGet, required
			}
			found := false
			for _, response := range window {
				if response.Method == method && response.Status == http.StatusOK && response.Path == "/rubygems/"+repo.Name+"/"+path {
					found = true
				}
			}
			if !found {
				t.Fatalf("phase %s did not exercise native branch %s %s", phase, method, path)
			}
		}
		phaseProofs = append(phaseProofs, map[string]any{"phase": phase, "observerWindowStart": start, "observerWindowLength": len(window), "originals": expected, "verifiedFullOriginalGETs": seen, "identityAndDependenciesMatchOriginals": true, "requiredNativePaths": requiredPaths})
	}
	stable := []string{"owned_top-1.2.0.gem", "owned_middle-1.1.0.gem", "owned_leaf-1.0.0.gem"}
	compact := []string{"versions", "info/owned_top", "info/owned_middle", "info/owned_leaf"}
	legacy := []string{"specs.4.8.gz", "quick/Marshal.4.8/owned_top-1.2.0.gemspec.rz"}
	// Fixed RubyGems 3.6.7 Source#dependency_resolver_set probes versions with
	// HEAD and selects APISet's compact info. Only the explicit Bundler
	// --full-index phase is required to exercise the legacy specs GET.
	gemCompact := []string{"HEAD versions", "info/owned_top", "info/owned_middle", "info/owned_leaf", "quick/Marshal.4.8/owned_top-1.2.0.gemspec.rz"}
	install := func(phase string) {
		t.Helper()
		run(phase, "gem-install", false, "gem", "install", "owned_top", "--version", "1.2.0", "--remote", "--clear-sources", "--source", readSource, "--no-document", "--verbose")
		out := run(phase, "execute-transitive", true, "ruby", "-e", `require 'owned_top'; abort OWNED_TOP unless OWNED_TOP == 'top-middle-leaf'; puts OWNED_TOP`)
		if !strings.Contains(out, "top-middle-leaf") {
			t.Fatal(out)
		}
		verifyPhase(phase, stable, gemCompact)
	}
	install("gem-cold-before")
	gemfileBody = "source " + strconv.Quote(readSource) + "\ngem 'owned_top', '= 1.2.0'\n"
	run("bundle-cold-before", "bundle-install", false, "bundle", "_2.6.7_", "install", "--jobs", "1", "--retry", "0")
	run("bundle-cold-before", "bundle-execute", true, "bundle", "_2.6.7_", "exec", "ruby", "-e", `require 'owned_top'; abort unless OWNED_TOP == 'top-middle-leaf'; puts OWNED_TOP`)
	verifyPhase("bundle-cold-before", stable, compact)
	// Explicit full-index proves the legacy list and generated Gem::Specification
	// path, independently of the compact index used by the previous install.
	run("bundle-full-index", "legacy-bundle-install", false, "bundle", "_2.6.7_", "install", "--full-index", "--jobs", "1", "--retry", "0")
	verifyPhase("bundle-full-index", stable, legacy)
	run("pre-cold", "prerelease-install", false, "gem", "install", "owned_top", "--version", "1.3.0.pre", "--prerelease", "--remote", "--clear-sources", "--source", readSource, "--no-document")
	verifyPhase("pre-cold", []string{"owned_top-1.3.0.pre.gem", "owned_middle-1.1.0.gem", "owned_leaf-1.0.0.gem"}, []string{"HEAD versions", "info/owned_top", "info/owned_middle", "info/owned_leaf", "quick/Marshal.4.8/owned_top-1.3.0.pre.gemspec.rz"})
	run("platform-cold", "platform-fetch", false, "gem", "fetch", "owned_platform", "--version", "1.0.0", "--platform", "x86_64-linux", "--clear-sources", "--source", readSource)
	verifyPhase("platform-cold", []string{"owned_platform-1.0.0-x86_64-linux.gem"}, nil)
	audits, e := repos.ListAudits(t.Context(), repository.AuditQuery{Repository: repo.Name, Operation: "rubygems.publish", Limit: 100})
	if e != nil || len(audits) != len(proof) {
		t.Fatalf("durable idempotent audit %d %v", len(audits), e)
	}
	// Native yank uses the same fixed official client and an independently
	// empty client root. No target consumer can see the producer's gem files.
	lockBytes, err := os.ReadFile(filepath.Join(phaseRoots["bundle-cold-before"], "project/Gemfile.lock"))
	if err != nil {
		t.Fatal(err)
	}
	phaseLocks["bundle-yanked-before"] = string(lockBytes)
	phaseLocks["bundle-yanked-after"] = string(lockBytes)
	run("yank-variant-ruby", "yank-default-ruby", false, "gem", "yank", "owned_variant", "--version", "1.0.0", "--host", strings.TrimSuffix(source, "/"))
	if w := request(server.Config.Handler, "GET", "/rubygems/"+repo.Name+"/gems/owned_variant-1.0.0.gem", nil, admin); w.Code != 404 {
		t.Fatal("default yank ruby", w.Code)
	}
	if w := request(server.Config.Handler, "GET", "/rubygems/"+repo.Name+"/gems/owned_variant-1.0.0-java.gem", nil, admin); w.Code != 200 {
		t.Fatal("default yank changed java", w.Code)
	}
	run("yank-variant-java", "yank-explicit-java", false, "gem", "yank", "owned_variant", "--version", "1.0.0", "--platform", "java", "--host", strings.TrimSuffix(source, "/"))
	run("yank-leaf", "yank-transitive-leaf", false, "gem", "yank", "owned_leaf", "--version", "1.0.0", "--host", strings.TrimSuffix(source, "/"))
	run("yank-leaf-replay", "yank-idempotent-retry", false, "gem", "yank", "owned_leaf", "--version", "1.0.0", "--host", strings.TrimSuffix(source, "/"))
	assertRejectedPhase := func(phase string) {
		t.Helper()
		observing.Wait()
		mu.Lock()
		defer mu.Unlock()
		start, exists := phaseStarts[phase]
		if !exists {
			t.Fatal("missing denial phase")
		}
		observed := 0
		for _, response := range observations[start:] {
			if response.Phase != phase {
				continue
			}
			observed++
			if strings.HasSuffix(response.Path, "/owned_leaf-1.0.0.gem") && (response.Status == 200 || response.Status == 206 || response.Status == 304) {
				t.Fatalf("yanked leaf delivered: %+v", response)
			}
		}
		if observed == 0 {
			t.Fatal("client failed before reaching target Gateway")
		}
		governanceEvidence[phase] = map[string]any{"targetRequests": observed, "yankedOriginalUnavailable": true, "emptyCache": true, "targetOnly": true, "producerDirectoryMounted": false}
	}
	deniedConsumers := func(suffix string) {
		t.Helper()
		phase := "gem-yanked-" + suffix
		out := runExpected(phase, "yanked-gem-install", false, false, "gem", "install", "owned_top", "--version", "1.2.0", "--remote", "--clear-sources", "--source", readSource, "--no-document", "--verbose")
		if !strings.Contains(out, "owned_leaf") {
			t.Fatal("missing rejected dependency identity", out)
		}
		assertRejectedPhase(phase)
		phase = "bundle-yanked-" + suffix
		out = runExpected(phase, "locked-yanked-bundle-install", false, false, "bundle", "_2.6.7_", "install", "--jobs", "1", "--retry", "0")
		if !strings.Contains(out, "owned_leaf") {
			t.Fatal("missing rejected locked dependency identity", out)
		}
		assertRejectedPhase(phase)
	}
	deniedConsumers("before")
	governanceEvidence["officialYankRubyAndJavaIsolated"] = true
	governanceEvidence["lockedConsumerSource"] = "Gemfile.lock from successful owned cold install; no installed gems copied"
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
	deniedConsumers("after")
	statePath := governancePath(repo.ID, "owned_leaf", "1.0.0", "ruby")
	w := request(server.Config.Handler, "GET", statePath, nil, "Bearer "+admin)
	var state governanceReceipt
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &state) != nil || !state.Yanked || state.GovernanceVersion != "2" {
		t.Fatalf("restarted governance %d %s", w.Code, w.Body.String())
	}
	g := governanceChange(t, server.Config.Handler, repo.ID, "owned_leaf", "1.0.0", "ruby", state.Digest, state.GovernanceVersion, "restore", "Bearer "+admin, 200)
	if g.Yanked || g.GovernanceVersion != "3" {
		t.Fatal(g)
	}
	governanceEvidence["restartRetainedYank"] = true
	governanceEvidence["verifiedRestoreGovernanceVersion"] = g.GovernanceVersion
	install("gem-cold-after")
	run("bundle-cold-after", "restarted-bundle-install", false, "bundle", "_2.6.7_", "install", "--jobs", "1", "--retry", "0")
	run("bundle-cold-after", "restarted-bundle-execute", true, "bundle", "_2.6.7_", "exec", "ruby", "-e", `require 'owned_top'; abort unless OWNED_TOP == 'top-middle-leaf'; puts OWNED_TOP`)
	verifyPhase("bundle-cold-after", stable, compact)
	governanceEvidence["restoredColdClientsOriginalClosureVerified"] = true
	for _, op := range []string{"rubygems.yank", "rubygems.restore"} {
		auditRows, err := repos.ListAudits(t.Context(), repository.AuditQuery{Repository: repo.Name, Operation: op, Limit: 100})
		want := 3
		if op == "rubygems.restore" {
			want = 1
		}
		if err != nil || len(auditRows) != want {
			t.Fatalf("governance audit %s: %d %v", op, len(auditRows), err)
		}
		governanceEvidence[op+"AuditCount"] = len(auditRows)
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
