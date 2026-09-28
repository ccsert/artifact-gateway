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
	"sync"
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

func TestCargoProxyVerifiedCacheAndOfflineReplay(t *testing.T) {
	ctx := context.Background()
	archive := []byte("test crate archive")
	sum := sha256.Sum256(archive)
	checksum := hex.EncodeToString(sum[:])
	row := `{"name":"demo","vers":"1.0.0","deps":[],"cksum":"` + checksum + `","features":{},"yanked":false}` + "\n"
	var mu sync.Mutex
	indexBody := row
	archiveBody := archive
	requestCount := 0
	var upstream *httptest.Server
	upstream = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		requestCount++
		switch r.URL.Path {
		case "/config.json":
			_ = json.NewEncoder(w).Encode(map[string]string{"dl": upstream.URL + "/crates/{crate}/{version}/download", "api": upstream.URL})
		case "/de/mo/demo":
			w.Header().Set("ETag", `"index-1"`)
			_, _ = io.WriteString(w, indexBody)
		case "/crates/demo/1.0.0/download":
			_, _ = w.Write(archiveBody)
		default:
			http.NotFound(w, r)
		}
	}))
	store := repository.NewMemoryStore()
	u, _ := url.Parse(upstream.URL)
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: "cargo-proxy", Name: "cargo-proxy",
		Format: repository.FormatCargo, Type: repository.RepositoryTypeProxy, Endpoint: upstream.URL, AllowedHosts: []string{u.Hostname()}})
	if err != nil {
		t.Fatal(err)
	}
	objects := NewMemoryOCIObjectStore()
	gateway := httptest.NewServer(NewGatewayHandler(Dependencies{NativeCargoObjectStore: objects, CargoUpstreamClient: upstream.Client()},
		store, TestAdapter{}, testAuthenticator()))
	defer gateway.Close()
	get := func(path string) (int, []byte) {
		t.Helper()
		req, err := http.NewRequest(http.MethodGet, gateway.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "resolver-secret")
		res, err := gateway.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		body, err := io.ReadAll(res.Body)
		if err != nil {
			t.Fatal(err)
		}
		return res.StatusCode, body
	}
	base := "/cargo/" + repo.Name
	if status, body := get(base + "/config.json"); status != 200 || !bytes.Contains(body, []byte(gateway.URL+base)) {
		t.Fatalf("config status=%d body=%s", status, body)
	}
	if status, body := get(base + "/de/mo/demo"); status != 200 || string(body) != row {
		t.Fatalf("index status=%d body=%s", status, body)
	}
	mu.Lock()
	archiveBody = []byte("corrupt")
	mu.Unlock()
	if status, _ := get(base + "/api/v1/crates/demo/1.0.0/download"); status != http.StatusBadGateway {
		t.Fatalf("corrupt upstream archive status=%d", status)
	}
	if _, err := store.GetCargoProxyCrate(ctx, repo.ID, "demo", "1.0.0"); err == nil {
		t.Fatal("corrupt archive became visible")
	}
	mu.Lock()
	archiveBody = archive
	mu.Unlock()
	if status, body := get(base + "/api/v1/crates/demo/1.0.0/download"); status != 200 || !bytes.Equal(body, archive) {
		t.Fatalf("verified download status=%d body=%q", status, body)
	}
	mu.Lock()
	before := requestCount
	mu.Unlock()
	upstream.Close()
	now := time.Now().UTC()
	config, _ := store.GetCargoProxyConfig(ctx, repo.ID)
	config.FetchedAt, config.ExpiresAt = now.Add(-2*time.Hour), now.Add(-time.Hour)
	if err := store.PutCargoProxyConfig(ctx, config); err != nil {
		t.Fatal(err)
	}
	index, _ := store.GetCargoProxyIndex(ctx, repo.ID, "demo")
	index.FetchedAt, index.ExpiresAt = now.Add(-2*time.Hour), now.Add(-time.Hour)
	if err := store.PutCargoProxyIndex(ctx, index); err != nil {
		t.Fatal(err)
	}
	if status, body := get(base + "/de/mo/demo"); status != 200 || string(body) != row {
		t.Fatalf("offline index status=%d body=%s", status, body)
	}
	if status, body := get(base + "/api/v1/crates/demo/1.0.0/download"); status != 200 || !bytes.Equal(body, archive) {
		t.Fatalf("offline archive status=%d body=%q", status, body)
	}
	mu.Lock()
	after := requestCount
	mu.Unlock()
	if after != before {
		t.Fatalf("upstream request count after close: %d -> %d", before, after)
	}
}

func TestCargoProxyIndexRejectsImmutableRewrite(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: "cargo-proxy", Name: "cargo-proxy",
		Format: repository.FormatCargo, Type: repository.RepositoryTypeProxy, Endpoint: "https://index.crates.io"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	checksum := strings.Repeat("a", 64)
	row := `{"name":"demo","vers":"1.0.0","cksum":"` + checksum + `","yanked":false}` + "\n"
	index := repository.CargoProxyIndex{RepositoryID: repo.ID, Name: "demo", Status: 200, Body: []byte(row), FetchedAt: now, ExpiresAt: now.Add(time.Hour)}
	if err := store.PutCargoProxyIndex(ctx, index); err != nil {
		t.Fatal(err)
	}
	index.Body = []byte(strings.Replace(row, `"yanked":false`, `"yanked":true`, 1))
	if err := store.PutCargoProxyIndex(ctx, index); err != nil {
		t.Fatalf("yank: %v", err)
	}
	index.Body = []byte(strings.Replace(row, checksum, strings.Repeat("b", 64), 1))
	if err := store.PutCargoProxyIndex(ctx, index); err != repository.ErrUpstreamChanged {
		t.Fatalf("immutable checksum rewrite: %v", err)
	}
	stored, err := store.GetCargoProxyIndex(ctx, repo.ID, "demo")
	if err != nil || !bytes.Contains(stored.Body, []byte(`"yanked":true`)) {
		t.Fatalf("stored index=%s err=%v", stored.Body, err)
	}
}

func TestCargoProxyOfficialSourceReplacementOnlineAndOffline(t *testing.T) {
	cargoPath, err := exec.LookPath("cargo")
	if err != nil {
		t.Skip("Cargo is unavailable")
	}
	version, err := exec.Command(cargoPath, "--version").Output()
	if err != nil || !strings.HasPrefix(string(version), "cargo 1.96.0 ") {
		t.Skipf("Cargo 1.96.0 is unavailable: %s", version)
	}
	write := func(path, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	packageDir := filepath.Join(t.TempDir(), "proxy-official-demo")
	write(filepath.Join(packageDir, "Cargo.toml"), "[package]\nname=\"proxy-official-demo\"\nversion=\"1.0.0\"\nedition=\"2024\"\nlicense=\"MIT\"\ndescription=\"Proxy test\"\n")
	write(filepath.Join(packageDir, "src/lib.rs"), "pub fn value() -> u8 { 42 }\n")
	packageCommand := exec.Command(cargoPath, "package", "--allow-dirty", "--no-verify")
	packageCommand.Dir = packageDir
	packageTarget := filepath.Join(t.TempDir(), "package-target")
	packageCommand.Env = append(os.Environ(), "CARGO_HOME="+t.TempDir(), "CARGO_TARGET_DIR="+packageTarget)
	if output, err := packageCommand.CombinedOutput(); err != nil {
		t.Fatalf("cargo package: %v\n%s", err, output)
	}
	archive, err := os.ReadFile(filepath.Join(packageTarget, "package/proxy-official-demo-1.0.0.crate"))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(archive)
	checksum := hex.EncodeToString(sum[:])
	row := `{"name":"proxy-official-demo","vers":"1.0.0","deps":[],"cksum":"` + checksum + `","features":{},"yanked":false}` + "\n"
	var upstream *httptest.Server
	upstream = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/config.json":
			_ = json.NewEncoder(w).Encode(map[string]string{"dl": upstream.URL + "/crates/{crate}/{version}/download"})
		case "/pr/ox/proxy-official-demo":
			_, _ = io.WriteString(w, row)
		case "/crates/proxy-official-demo/1.0.0/download":
			_, _ = w.Write(archive)
		default:
			http.NotFound(w, r)
		}
	}))
	store := repository.NewMemoryStore()
	if _, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: "proxy-official", Name: "proxy-official",
		Format: repository.FormatCargo, Type: repository.RepositoryTypeProxy, Endpoint: upstream.URL, AnonymousRead: true}); err != nil {
		t.Fatal(err)
	}
	enableAnonymousAccess(t, store)
	gateway := httptest.NewServer(NewGatewayHandler(Dependencies{NativeCargoObjectStore: NewMemoryOCIObjectStore(), CargoUpstreamClient: upstream.Client()},
		store, TestAdapter{}, testAuthenticator()))
	defer gateway.Close()
	home := t.TempDir()
	write(filepath.Join(home, "config.toml"), "[source.crates-io]\nreplace-with=\"gateway\"\n[source.gateway]\nregistry=\"sparse+"+gateway.URL+"/cargo/proxy-official/\"\n")
	consumer := filepath.Join(t.TempDir(), "consumer")
	write(filepath.Join(consumer, "Cargo.toml"), "[package]\nname=\"consumer\"\nversion=\"0.1.0\"\nedition=\"2024\"\n[dependencies]\nproxy-official-demo=\"=1.0.0\"\n")
	write(filepath.Join(consumer, "src/main.rs"), "fn main() { assert_eq!(proxy_official_demo::value(), 42); }\n")
	environment := append(os.Environ(), "CARGO_HOME="+home, "CARGO_TARGET_DIR="+filepath.Join(t.TempDir(), "consumer-target"),
		"HTTP_PROXY=", "HTTPS_PROXY=", "ALL_PROXY=", "NO_PROXY=127.0.0.1,localhost")
	run := func(args ...string) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		command := exec.CommandContext(ctx, cargoPath, args...)
		command.Dir, command.Env = consumer, environment
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("cargo %s: %v\n%s", strings.Join(args, " "), err, output)
		}
	}
	run("check")
	lockfile, err := os.ReadFile(filepath.Join(consumer, "Cargo.lock"))
	if err != nil || !bytes.Contains(lockfile, []byte("registry+https://github.com/rust-lang/crates.io-index")) {
		t.Fatalf("source replacement lockfile=%s err=%v", lockfile, err)
	}
	upstream.Close()
	// A fresh Cargo home forces every index and archive byte through Gateway's
	// persisted cache while the upstream server is unavailable.
	freshHome := t.TempDir()
	write(filepath.Join(freshHome, "config.toml"), "[source.crates-io]\nreplace-with=\"gateway\"\n[source.gateway]\nregistry=\"sparse+"+gateway.URL+"/cargo/proxy-official/\"\n")
	environment = append(os.Environ(), "CARGO_HOME="+freshHome, "CARGO_TARGET_DIR="+filepath.Join(t.TempDir(), "fresh-target"),
		"HTTP_PROXY=", "HTTPS_PROXY=", "ALL_PROXY=", "NO_PROXY=127.0.0.1,localhost")
	run("check", "--locked")
	run("check", "--locked", "--offline")
}

func TestCargoProxyNegativeAndConditionalIndexCache(t *testing.T) {
	ctx := context.Background()
	var mu sync.Mutex
	status, hits, condition := http.StatusNotFound, 0, ""
	checksum := strings.Repeat("a", 64)
	row := `{"name":"demo","vers":"1.0.0","cksum":"` + checksum + `","yanked":false}` + "\n"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/de/mo/demo" {
			http.NotFound(w, r)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		hits++
		condition = r.Header.Get("If-None-Match")
		if status == http.StatusOK && condition == `"v1"` {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		if status != http.StatusOK {
			w.WriteHeader(status)
			return
		}
		w.Header().Set("ETag", `"v1"`)
		_, _ = io.WriteString(w, row)
	}))
	defer upstream.Close()
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: "cargo-negative", Name: "cargo-negative",
		Format: repository.FormatCargo, Type: repository.RepositoryTypeProxy, Endpoint: upstream.URL})
	if err != nil {
		t.Fatal(err)
	}
	h := newNativeCargoHandler(store, NewMemoryOCIObjectStore(), testAuthenticator())
	h.upstream = UpstreamClient{HTTPClient: upstream.Client()}
	request := httptest.NewRequest(http.MethodGet, "http://gateway/cargo/cargo-negative/de/mo/demo", nil)
	first, err := h.proxyIndex(request, repo, "demo")
	if err != nil || first.Status != 404 {
		t.Fatalf("negative=%+v err=%v", first, err)
	}
	if _, err := h.proxyIndex(request, repo, "demo"); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	gotHits := hits
	status = http.StatusOK
	mu.Unlock()
	if gotHits != 1 {
		t.Fatalf("negative cache hits=%d", gotHits)
	}
	first.FetchedAt, first.ExpiresAt = time.Now().Add(-2*time.Minute), time.Now().Add(-time.Minute)
	if err := store.PutCargoProxyIndex(ctx, first); err != nil {
		t.Fatal(err)
	}
	positive, err := h.proxyIndex(request, repo, "demo")
	if err != nil || positive.Status != 200 || string(positive.Body) != row {
		t.Fatalf("positive=%+v err=%v", positive, err)
	}
	positive.FetchedAt, positive.ExpiresAt = time.Now().Add(-2*time.Minute), time.Now().Add(-time.Minute)
	if err := store.PutCargoProxyIndex(ctx, positive); err != nil {
		t.Fatal(err)
	}
	refreshed, err := h.proxyIndex(request, repo, "demo")
	mu.Lock()
	gotCondition := condition
	gotHits = hits
	mu.Unlock()
	if err != nil || gotCondition != `"v1"` || gotHits != 3 || string(refreshed.Body) != row || !refreshed.ExpiresAt.After(time.Now()) {
		t.Fatalf("conditional index=%+v condition=%q hits=%d err=%v", refreshed, gotCondition, gotHits, err)
	}
}
