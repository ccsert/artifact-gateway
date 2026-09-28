//go:build integration

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
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

// This gate uses a fresh official Cargo client on both sides of a real store
// restart. The second client has no local registry cache and the upstream is
// closed, so every successful byte must come from PostgreSQL and RustFS.
func TestPostgresRustFSCargoProxyOfficialOfflineReplay(t *testing.T) {
	databaseURL, endpoint := os.Getenv("TEST_DATABASE_URL"), os.Getenv("TEST_RUSTFS_ENDPOINT")
	accessKey, secretKey := os.Getenv("TEST_RUSTFS_ACCESS_KEY"), os.Getenv("TEST_RUSTFS_SECRET_KEY")
	if databaseURL == "" || endpoint == "" || accessKey == "" || secretKey == "" {
		t.Skip("PostgreSQL and RustFS integration environment is required")
	}
	cargoPath, err := exec.LookPath("cargo")
	if err != nil {
		if os.Getenv("CARGO_REQUIRED") == "1" {
			t.Fatal("Cargo 1.96.0 is required")
		}
		t.Skip("Cargo is unavailable")
	}
	version, err := exec.Command(cargoPath, "--version").Output()
	if err != nil || !strings.HasPrefix(string(version), "cargo 1.96.0 ") {
		if os.Getenv("CARGO_REQUIRED") == "1" {
			t.Fatalf("Cargo 1.96.0 is required, got %q: %v", version, err)
		}
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
	write(filepath.Join(packageDir, "Cargo.toml"), "[package]\nname=\"proxy-official-demo\"\nversion=\"1.0.0\"\nedition=\"2024\"\nlicense=\"MIT\"\ndescription=\"Persistent Proxy test\"\n")
	write(filepath.Join(packageDir, "src/lib.rs"), "pub fn value() -> u8 { 42 }\n")
	write(filepath.Join(packageDir, "src/main.rs"), "fn main() { println!(\"{}\", proxy_official_demo::value()); }\n")
	packageTarget := filepath.Join(t.TempDir(), "package-target")
	packageCommand := exec.Command(cargoPath, "package", "--allow-dirty", "--no-verify")
	packageCommand.Dir = packageDir
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
	t.Cleanup(upstream.Close)
	ctx := context.Background()
	storeA, err := repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = storeA.Close() }()
	bucket := "cargo-official-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:20]
	objectsA, err := NewRustFSOCIObjectStore(endpoint, accessKey, secretKey, bucket)
	if err != nil {
		t.Fatal(err)
	}
	if err := objectsA.EnsureBucket(ctx); err != nil {
		t.Fatal(err)
	}
	policy, err := storeA.GetAnonymousAccessPolicy(ctx)
	if err != nil {
		t.Fatal(err)
	}
	enabled, err := storeA.ReplaceAnonymousAccessPolicy(ctx, repository.AnonymousAccessPolicy{Enabled: true}, policy.Version)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := storeA.ReplaceAnonymousAccessPolicy(context.Background(), policy, enabled.Version); err != nil {
			t.Errorf("restore anonymous access policy: %v", err)
		}
	}()
	repo, err := storeA.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "cargo-official-" + uuid.NewString()[:8],
		Format: repository.FormatCargo, Type: repository.RepositoryTypeProxy, Endpoint: upstream.URL, AnonymousRead: true})
	if err != nil {
		t.Fatal(err)
	}
	warm := httptest.NewServer(NewGatewayHandler(Dependencies{NativeCargoObjectStore: objectsA, CargoUpstreamClient: upstream.Client()},
		storeA, TestAdapter{}, testAuthenticator()))
	consumer := filepath.Join(t.TempDir(), "consumer")
	write(filepath.Join(consumer, "Cargo.toml"), "[package]\nname=\"consumer\"\nversion=\"0.1.0\"\nedition=\"2024\"\n[dependencies]\nproxy-official-demo=\"=1.0.0\"\n")
	write(filepath.Join(consumer, "src/main.rs"), "fn main() { assert_eq!(proxy_official_demo::value(), 42); }\n")
	config := func(server string) string {
		return "[source.crates-io]\nreplace-with=\"gateway\"\n[source.gateway]\nregistry=\"sparse+" + server + "/cargo/" + repo.Name + "/\"\n"
	}
	runCargo := func(home, target string, args ...string) {
		t.Helper()
		commandCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()
		command := exec.CommandContext(commandCtx, cargoPath, args...)
		command.Dir = consumer
		command.Env = append(os.Environ(), "CARGO_HOME="+home, "CARGO_TARGET_DIR="+target,
			"HTTP_PROXY=", "HTTPS_PROXY=", "ALL_PROXY=", "NO_PROXY=127.0.0.1,localhost")
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("cargo %s: %v\n%s", strings.Join(args, " "), err, output)
		}
	}
	home := t.TempDir()
	write(filepath.Join(home, "config.toml"), config(warm.URL))
	runCargo(home, filepath.Join(t.TempDir(), "online-target"), "check")
	runCargo(home, filepath.Join(t.TempDir(), "online-install-target"), "install", "proxy-official-demo", "--version", "1.0.0", "--root", filepath.Join(t.TempDir(), "online-install"))
	lockfile, err := os.ReadFile(filepath.Join(consumer, "Cargo.lock"))
	if err != nil || !bytes.Contains(lockfile, []byte("registry+https://github.com/rust-lang/crates.io-index")) || !bytes.Contains(lockfile, []byte("checksum = \""+checksum+"\"")) {
		t.Fatalf("source replacement lockfile=%s err=%v", lockfile, err)
	}
	warm.Close()
	upstream.Close()
	now := time.Now().UTC()
	index, err := storeA.GetCargoProxyIndex(ctx, repo.ID, "proxy-official-demo")
	if err != nil {
		t.Fatal(err)
	}
	index.FetchedAt, index.ExpiresAt = now.Add(-2*time.Hour), now.Add(-time.Hour)
	if err := storeA.PutCargoProxyIndex(ctx, index); err != nil {
		t.Fatal(err)
	}
	remoteConfig, err := storeA.GetCargoProxyConfig(ctx, repo.ID)
	if err != nil {
		t.Fatal(err)
	}
	remoteConfig.FetchedAt, remoteConfig.ExpiresAt = now.Add(-2*time.Hour), now.Add(-time.Hour)
	if err := storeA.PutCargoProxyConfig(ctx, remoteConfig); err != nil {
		t.Fatal(err)
	}
	storeB, err := repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = storeB.Close() }()
	objectsB, err := NewRustFSOCIObjectStore(endpoint, accessKey, secretKey, bucket)
	if err != nil {
		t.Fatal(err)
	}
	restarted := httptest.NewServer(NewGatewayHandler(Dependencies{NativeCargoObjectStore: objectsB, CargoUpstreamClient: upstream.Client()},
		storeB, TestAdapter{}, testAuthenticator()))
	defer restarted.Close()
	freshHome := t.TempDir()
	write(filepath.Join(freshHome, "config.toml"), config(restarted.URL))
	runCargo(freshHome, filepath.Join(t.TempDir(), "offline-target"), "check", "--locked")
	installRoot := filepath.Join(t.TempDir(), "install")
	runCargo(freshHome, filepath.Join(t.TempDir(), "install-target"), "install", "proxy-official-demo", "--version", "1.0.0", "--root", installRoot)
	if _, err := os.Stat(filepath.Join(installRoot, "bin", "proxy-official-demo")); err != nil {
		t.Fatalf("offline install did not use the persisted crate: %v", err)
	}
}
