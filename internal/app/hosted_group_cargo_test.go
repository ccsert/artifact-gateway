package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
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
)

func TestCargoGroupIndexAndDownloadStayWithOwner(t *testing.T) {
	ctx := context.Background()
	proxyPublish, proxyArchive := cargoC0PublishFixture(t, "demo", "1.1.0", "demo")
	_ = proxyPublish
	sum := sha256.Sum256(proxyArchive)
	checksum := hex.EncodeToString(sum[:])
	proxyRow := `{"name":"demo","vers":"1.1.0","deps":[],"cksum":"` + checksum + `","features":{},"yanked":false}` + "\n"
	var upstream *httptest.Server
	upstream = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/config.json":
			_ = json.NewEncoder(w).Encode(map[string]string{"dl": upstream.URL + "/crates/{crate}/{version}/download", "api": upstream.URL})
		case "/de/mo/demo":
			_, _ = io.WriteString(w, proxyRow)
		case "/crates/demo/1.1.0/download":
			_, _ = w.Write(proxyArchive)
		case "/api/v1/crates":
			_, _ = io.WriteString(w, `{"crates":[{"name":"demo","max_version":"1.1.0","description":"proxy"}],"meta":{"total":1}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	store := repository.NewMemoryStore()
	hosted, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: "cargo-group-hosted", Name: "cargo-group-hosted", Format: repository.FormatCargo})
	if err != nil {
		t.Fatal(err)
	}
	proxy, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: "cargo-group-proxy", Name: "cargo-group-proxy",
		Format: repository.FormatCargo, Type: repository.RepositoryTypeProxy, Endpoint: upstream.URL})
	if err != nil {
		t.Fatal(err)
	}
	group := createV2Group(t, store, "cargo-group", repository.FormatCargo,
		repository.GroupMember{RepositoryID: hosted.ID, Position: 0}, repository.GroupMember{RepositoryID: proxy.ID, Position: 1})
	gateway := httptest.NewServer(NewGatewayHandler(Dependencies{NativeCargoObjectStore: NewMemoryOCIObjectStore(), CargoUpstreamClient: upstream.Client()},
		store, TestAdapter{}, testAuthenticator()))
	defer gateway.Close()
	request := func(method, path, token string, body []byte) (int, []byte) {
		t.Helper()
		req, err := http.NewRequest(method, gateway.URL+path, bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", token)
		res, err := gateway.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = res.Body.Close() }()
		data, err := io.ReadAll(res.Body)
		if err != nil {
			t.Fatal(err)
		}
		return res.StatusCode, data
	}
	hostedPublish, hostedArchive := cargoC0PublishFixture(t, "demo", "1.0.0", "demo")
	if status, data := request(http.MethodPut, "/cargo/"+hosted.Name+"/api/v1/crates/new", "admin-secret", hostedPublish); status != 200 {
		t.Fatalf("hosted publish=%d body=%s", status, data)
	}
	base := "/cargo/" + group.Name
	if status, data := request(http.MethodGet, base+"/config.json", "resolver-secret", nil); status != 200 || !bytes.Contains(data, []byte(gateway.URL+base)) {
		t.Fatalf("group config=%d body=%s", status, data)
	}
	if status, data := request(http.MethodGet, base+"/de/mo/demo", "resolver-secret", nil); status != 200 || bytes.Count(data, []byte("\n")) != 2 {
		t.Fatalf("group index=%d body=%s", status, data)
	}
	for _, fixture := range []struct {
		Version string
		Body    []byte
	}{{"1.0.0", hostedArchive}, {"1.1.0", proxyArchive}} {
		if status, data := request(http.MethodGet, base+"/api/v1/crates/demo/"+fixture.Version+"/download", "resolver-secret", nil); status != 200 || !bytes.Equal(data, fixture.Body) {
			t.Fatalf("group download %s=%d body=%q", fixture.Version, status, data)
		}
	}
	if status, data := request(http.MethodGet, base+"/api/v1/crates?q=demo", "resolver-secret", nil); status != 200 || !bytes.Contains(data, []byte(`"name":"demo"`)) {
		t.Fatalf("group search=%d body=%s", status, data)
	}
	group, err = store.ReplaceHostedGroupMembers(ctx, group.ID, []repository.GroupMember{{RepositoryID: proxy.ID, Position: 0}, {RepositoryID: hosted.ID, Position: 1}}, group.Version)
	if err != nil {
		t.Fatal(err)
	}
	if status, data := request(http.MethodGet, base+"/de/mo/demo", "resolver-secret", nil); status != 200 || bytes.Count(data, []byte("\n")) != 2 {
		t.Fatalf("reordered index=%d body=%s", status, data)
	}
	owner, err := store.GetCargoGroupVersion(ctx, group.ID, "demo", "1.0.0")
	if err != nil || owner.SourceRepositoryID != hosted.ID {
		t.Fatalf("owner after reorder=%+v err=%v", owner, err)
	}
	group, err = store.ReplaceHostedGroupMembers(ctx, group.ID, []repository.GroupMember{{RepositoryID: proxy.ID, Position: 0}}, group.Version)
	if err != nil {
		t.Fatal(err)
	}
	if status, _ := request(http.MethodGet, base+"/api/v1/crates/demo/1.0.0/download", "resolver-secret", nil); status != http.StatusServiceUnavailable {
		t.Fatalf("removed owner download=%d", status)
	}
}

func TestCargoGroupFirstReadRejectsUnseenProxyCollision(t *testing.T) {
	ctx := context.Background()
	proxyChecksum := sha256.Sum256([]byte("different upstream archive"))
	row := `{"name":"demo","vers":"1.0.0","deps":[],"cksum":"` + hex.EncodeToString(proxyChecksum[:]) + `","features":{},"yanked":false}` + "\n"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/de/mo/demo" {
			_, _ = io.WriteString(w, row)
			return
		}
		http.NotFound(w, r)
	}))
	defer upstream.Close()
	store := repository.NewMemoryStore()
	hosted, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: "cargo-unseen-hosted", Name: "cargo-unseen-hosted", Format: repository.FormatCargo})
	if err != nil {
		t.Fatal(err)
	}
	proxy, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: "cargo-unseen-proxy", Name: "cargo-unseen-proxy",
		Format: repository.FormatCargo, Type: repository.RepositoryTypeProxy, Endpoint: upstream.URL})
	if err != nil {
		t.Fatal(err)
	}
	group := createV2Group(t, store, "cargo-unseen-group", repository.FormatCargo,
		repository.GroupMember{RepositoryID: hosted.ID, Position: 0}, repository.GroupMember{RepositoryID: proxy.ID, Position: 1})
	gateway := httptest.NewServer(NewGatewayHandler(Dependencies{NativeCargoObjectStore: NewMemoryOCIObjectStore(), CargoUpstreamClient: upstream.Client()},
		store, TestAdapter{}, testAuthenticator()))
	defer gateway.Close()
	publish, _ := cargoC0PublishFixture(t, "demo", "1.0.0", "demo")
	request := func(method, path, token string, body []byte) *http.Response {
		t.Helper()
		req, err := http.NewRequest(method, gateway.URL+path, bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", token)
		response, err := gateway.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return response
	}
	response := request(http.MethodPut, "/cargo/"+hosted.Name+"/api/v1/crates/new", "admin-secret", publish)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("Hosted publish status=%d", response.StatusCode)
	}
	for _, path := range []string{"/de/mo/demo", "/api/v1/crates/demo/1.0.0/download"} {
		response := request(http.MethodGet, "/cargo/"+group.Name+path, "resolver-secret", nil)
		_ = response.Body.Close()
		if response.StatusCode != http.StatusConflict {
			t.Fatalf("unseen Proxy collision %s status=%d", path, response.StatusCode)
		}
	}
	if _, err := store.GetCargoGroupVersion(ctx, group.ID, "demo", "1.0.0"); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("conflicting coordinate acquired an owner: %v", err)
	}
}

func TestCargoGroupOfficialAlternateRegistry(t *testing.T) {
	cargoPath, err := exec.LookPath("cargo")
	if err != nil {
		if os.Getenv("CARGO_REQUIRED") == "1" {
			t.Fatal("Cargo 1.96.0 is required for the Group contract")
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
	packageDir := filepath.Join(t.TempDir(), "group-official-demo")
	write(filepath.Join(packageDir, "Cargo.toml"), "[package]\nname=\"group-official-demo\"\nversion=\"1.0.0\"\nedition=\"2024\"\nlicense=\"MIT\"\ndescription=\"Group test\"\n")
	write(filepath.Join(packageDir, "src/lib.rs"), "pub fn value() -> u8 { 42 }\n")
	packageTarget := filepath.Join(t.TempDir(), "package-target")
	packageCommand := exec.Command(cargoPath, "package", "--allow-dirty", "--no-verify")
	packageCommand.Dir = packageDir
	packageCommand.Env = append(os.Environ(), "CARGO_HOME="+t.TempDir(), "CARGO_TARGET_DIR="+packageTarget)
	if output, err := packageCommand.CombinedOutput(); err != nil {
		t.Fatalf("cargo package: %v\n%s", err, output)
	}
	archive, err := os.ReadFile(filepath.Join(packageTarget, "package/group-official-demo-1.0.0.crate"))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(archive)
	checksum := hex.EncodeToString(sum[:])
	row := `{"name":"group-official-demo","vers":"1.0.0","deps":[],"cksum":"` + checksum + `","features":{},"yanked":false}` + "\n"
	var upstream *httptest.Server
	upstream = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/config.json":
			_ = json.NewEncoder(w).Encode(map[string]string{"dl": upstream.URL + "/crates/{crate}/{version}/download", "api": upstream.URL})
		case "/gr/ou/group-official-demo":
			_, _ = io.WriteString(w, row)
		case "/crates/group-official-demo/1.0.0/download":
			_, _ = w.Write(archive)
		case "/api/v1/crates":
			_, _ = io.WriteString(w, `{"crates":[{"name":"group-official-demo","max_version":"1.0.0","description":"Group test"}],"meta":{"total":1}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	store := repository.NewMemoryStore()
	hosted, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: "cargo-group-official-hosted", Name: "cargo-group-official-hosted", Format: repository.FormatCargo})
	if err != nil {
		t.Fatal(err)
	}
	proxy, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: "cargo-group-official-proxy", Name: "cargo-group-official-proxy",
		Format: repository.FormatCargo, Type: repository.RepositoryTypeProxy, Endpoint: upstream.URL})
	if err != nil {
		t.Fatal(err)
	}
	group := createV2Group(t, store, "cargo-group-official", repository.FormatCargo,
		repository.GroupMember{RepositoryID: hosted.ID, Position: 0}, repository.GroupMember{RepositoryID: proxy.ID, Position: 1})
	gateway := httptest.NewServer(NewGatewayHandler(Dependencies{NativeCargoObjectStore: NewMemoryOCIObjectStore(), CargoUpstreamClient: upstream.Client()},
		store, TestAdapter{}, testAuthenticator()))
	defer gateway.Close()
	consumer := filepath.Join(t.TempDir(), "consumer")
	write(filepath.Join(consumer, "Cargo.toml"), "[package]\nname=\"consumer\"\nversion=\"0.1.0\"\nedition=\"2024\"\n[dependencies]\ngroup-official-demo={version=\"=1.0.0\",registry=\"fixture\"}\n")
	write(filepath.Join(consumer, "src/main.rs"), "fn main() { assert_eq!(group_official_demo::value(), 42); }\n")
	config := "[registries.fixture]\nindex=\"sparse+" + gateway.URL + "/cargo/" + group.Name + "/\"\ncredential-provider=\"cargo:token\"\n"
	home := t.TempDir()
	write(filepath.Join(home, "config.toml"), config)
	environment := append(os.Environ(), "CARGO_HOME="+home, "CARGO_TARGET_DIR="+filepath.Join(t.TempDir(), "target"),
		"CARGO_REGISTRIES_FIXTURE_TOKEN=resolver-secret", "HTTP_PROXY=", "HTTPS_PROXY=", "ALL_PROXY=", "NO_PROXY=127.0.0.1,localhost")
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
	run("search", "group-official-demo", "--registry", "fixture")
	lockfile, err := os.ReadFile(filepath.Join(consumer, "Cargo.lock"))
	if err != nil || !bytes.Contains(lockfile, []byte(gateway.URL+"/cargo/"+group.Name)) {
		t.Fatalf("alternate registry lockfile=%s err=%v", lockfile, err)
	}
	upstream.Close()
	freshHome := t.TempDir()
	write(filepath.Join(freshHome, "config.toml"), config)
	environment = append(os.Environ(), "CARGO_HOME="+freshHome, "CARGO_TARGET_DIR="+filepath.Join(t.TempDir(), "fresh-target"),
		"CARGO_REGISTRIES_FIXTURE_TOKEN=resolver-secret", "HTTP_PROXY=", "HTTPS_PROXY=", "ALL_PROXY=", "NO_PROXY=127.0.0.1,localhost")
	run("check", "--locked")
	run("check", "--locked", "--offline")
}
