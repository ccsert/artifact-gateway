package app

import (
	"context"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

func TestCargoProxyLiveCratesIO(t *testing.T) {
	if os.Getenv("CARGO_LIVE_CRATES_IO") != "1" {
		t.Skip("opt-in live crates.io contract")
	}
	cargoPath, err := exec.LookPath("cargo")
	if err != nil {
		t.Fatal(err)
	}
	store := repository.NewMemoryStore()
	if _, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{
		ID: "live-crates-io", Name: "live-crates-io", Format: repository.FormatCargo,
		Type: repository.RepositoryTypeProxy, Endpoint: "https://index.crates.io",
		AllowedHosts: []string{"static.crates.io", "crates.io"}, AnonymousRead: true,
	}); err != nil {
		t.Fatal(err)
	}
	enableAnonymousAccess(t, store)
	server := httptest.NewServer(NewGatewayHandler(Dependencies{NativeCargoObjectStore: NewMemoryOCIObjectStore()},
		store, TestAdapter{}, testAuthenticator()))
	defer server.Close()
	home := t.TempDir()
	consumer := t.TempDir()
	if err := os.MkdirAll(filepath.Join(consumer, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	for path, body := range map[string]string{
		filepath.Join(home, "config.toml"):     "[source.crates-io]\nreplace-with=\"gateway\"\n[source.gateway]\nregistry=\"sparse+" + server.URL + "/cargo/live-crates-io/\"\n",
		filepath.Join(consumer, "Cargo.toml"):  "[package]\nname=\"cargo-live-proxy-check\"\nversion=\"0.1.0\"\nedition=\"2024\"\n[dependencies]\nitoa=\"=1.0.15\"\n",
		filepath.Join(consumer, "src/main.rs"): "fn main() { assert_eq!(itoa::Buffer::new().format(42), \"42\"); }\n",
	} {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, cargoPath, "check")
	command.Dir = consumer
	command.Env = append(os.Environ(), "CARGO_HOME="+home, "CARGO_TARGET_DIR="+filepath.Join(t.TempDir(), "target"),
		"NO_PROXY=127.0.0.1,localhost")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("cargo check: %v\n%s", err, output)
	}
	lockfile, err := os.ReadFile(filepath.Join(consumer, "Cargo.lock"))
	if err != nil || !strings.Contains(string(lockfile), "registry+https://github.com/rust-lang/crates.io-index") {
		t.Fatalf("lockfile source=%s err=%v", lockfile, err)
	}
}
