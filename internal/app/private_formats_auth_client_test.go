//go:build integration && privateformatsclient

package app

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto"
	"crypto/sha256"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/armor"
	"github.com/ProtonMail/go-crypto/openpgp/clearsign"
	"github.com/ProtonMail/go-crypto/openpgp/packet"
	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

func privateClientFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func privateClientCommand(t *testing.T, dir string, env []string, binary string, args ...string) []byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, binary, args...)
	command.Dir, command.Env = dir, env
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("official %s failed: %v\n%s", filepath.Base(binary), err, output)
	}
	return output
}

func privateClientEnvironment(work string) []string {
	return []string{"PATH=" + os.Getenv("PATH"), "HOME=" + work, "TMPDIR=" + work, "LANG=C", "NO_PROXY=127.0.0.1,localhost,::1", "PIP_CONFIG_FILE=/dev/null"}
}

func privateCargoClientAssets(t *testing.T) map[string]redirectCacheAsset {
	t.Helper()
	f := privateNativeFixture(t, repository.FormatCargo)
	var body bytes.Buffer
	gz := gzip.NewWriter(&body)
	tarball := tar.NewWriter(gz)
	for _, file := range []struct{ path, content string }{{"Cargo.toml", "[package]\nname=\"demo\"\nversion=\"1.0.0\"\nedition=\"2024\"\n"}, {"src/lib.rs", "pub fn value() -> u8 { 42 }\n"}} {
		if err := tarball.WriteHeader(&tar.Header{Name: "demo-1.0.0/" + file.path, Mode: 0o644, Size: int64(len(file.content))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tarball.Write([]byte(file.content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tarball.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	f.assets[f.object] = redirectCacheAsset{body: body.Bytes()}
	f.assets[f.metadata] = redirectCacheAsset{body: []byte(fmt.Sprintf(`{"name":"demo","vers":"1.0.0","deps":[],"cksum":"%x","features":{},"yanked":false}`+"\n", sha256.Sum256(body.Bytes())))}
	return f.assets
}

func privateAPTClientAssets(t *testing.T) map[string]redirectCacheAsset {
	t.Helper()
	f := privateNativeFixture(t, repository.FormatAPT)
	packages := []byte(fmt.Sprintf("Package: private-demo\nVersion: 1.0.0\nArchitecture: amd64\nMaintainer: Fixture <fixture@example.invalid>\nDescription: private fixture\nFilename: pool/private-demo.deb\nSize: %d\nSHA256: %x\n\n", len(f.body), sha256.Sum256(f.body)))
	release := []byte(fmt.Sprintf("Origin: Owned private source\nLabel: Private fixture\nSuite: stable\nCodename: stable\nDate: %s\nArchitectures: amd64\nComponents: main\nSHA256:\n %x %d main/binary-amd64/Packages\n", time.Now().UTC().Format(time.RFC1123Z), sha256.Sum256(packages), len(packages)))
	config := &packet.Config{RSABits: 2048, DefaultHash: crypto.SHA256}
	entity, err := openpgp.NewEntity("Owned private fixture", "", "fixture@example.invalid", config)
	if err != nil {
		t.Fatal(err)
	}
	var signed, public bytes.Buffer
	writer, err := clearsign.Encode(&signed, entity.PrivateKey, config)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = writer.Write(release); err != nil {
		t.Fatal(err)
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	key, err := armor.Encode(&public, openpgp.PublicKeyType, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = entity.Serialize(key); err != nil {
		t.Fatal(err)
	}
	if err = key.Close(); err != nil {
		t.Fatal(err)
	}
	f.assets[f.metadata] = redirectCacheAsset{body: release}
	f.assets["/dists/stable/InRelease"] = redirectCacheAsset{body: signed.Bytes()}
	f.assets["/dists/stable/main/binary-amd64/Packages"] = redirectCacheAsset{body: packages}
	f.assets["fixture-public-key"] = redirectCacheAsset{body: public.Bytes()}
	return f.assets
}

// Every case uses an isolated official client cache and only Gateway URLs. The
// source fixture is HTTPS and separately authenticated by the repository's
// configured credential; clients never receive that source secret.
func TestPostgresRustFSPrivateFormatOfficialClients(t *testing.T) {
	for _, name := range []string{"PRIVATE_FORMATS_NPM", "PRIVATE_FORMATS_PYTHON", "CONAN_BINARY", "TEST_PRIVATE_FORMATS_NETWORK", "TEST_PRIVATE_FORMATS_PROJECT", "TEST_PRIVATE_FORMATS_DOCKER_HOST"} {
		if os.Getenv(name) == "" {
			t.Fatalf("required official client fixture input %s is missing", name)
		}
	}
	cargo, err := exec.LookPath("cargo")
	if err != nil {
		t.Fatal("Cargo 1.96.0 is required")
	}
	version := privateClientCommand(t, t.TempDir(), privateClientEnvironment(t.TempDir()), cargo, "--version")
	if !bytes.HasPrefix(version, []byte("cargo 1.96.0 ")) {
		t.Fatalf("unexpected official Cargo version %s", version)
	}
	for _, format := range []repository.Format{repository.FormatNPM, repository.FormatPyPI, repository.FormatCargo, repository.FormatRaw, repository.FormatConan, repository.FormatAPT} {
		for _, scheme := range []repository.UpstreamAuthScheme{repository.UpstreamAuthSchemeBasic, repository.UpstreamAuthSchemeBearer} {
			for _, group := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/group=%v", format, scheme, group), func(t *testing.T) {
					backend := newPrivateProxyPostgresBackend(t)
					if format == repository.FormatCargo {
						backend.assets = privateCargoClientAssets(t)
					}
					if format == repository.FormatAPT {
						backend.assets = privateAPTClientAssets(t)
					}
					backend.client = func(t *testing.T, handler http.Handler, name, round string, assets map[string]redirectCacheAsset) {
						privateOfficialFormatClient(t, handler, format, name, round, assets)
					}
					switch format {
					case repository.FormatRaw:
						privateRawCredentialsContract(t, scheme, group, backend)
					case repository.FormatConan:
						privateConanCredentialsContract(t, scheme, group, backend)
					default:
						privateNativeCredentialsContract(t, format, scheme, group, backend)
					}
				})
			}
		}
	}
}

func privateOfficialFormatClient(t *testing.T, handler http.Handler, format repository.Format, name, round string, assets map[string]redirectCacheAsset) {
	t.Helper()
	server := httptest.NewUnstartedServer(handler)
	if format == repository.FormatAPT {
		_ = server.Listener.Close()
		var err error
		server.Listener, err = net.Listen("tcp", "0.0.0.0:0")
		if err != nil {
			t.Fatal(err)
		}
	}
	server.Start()
	defer server.Close()
	work := t.TempDir()
	env := privateClientEnvironment(work)
	registry := server.URL + "/" + string(format) + "/" + name + "/"
	verify := func(path string, expected []byte) {
		t.Helper()
		actual, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(actual, expected) {
			t.Fatalf("%s %s complete client bytes differ: %v", format, round, err)
		}
	}
	switch format {
	case repository.FormatNPM:
		privateClientFile(t, filepath.Join(work, ".npmrc"), []byte("//"+strings.TrimPrefix(registry, "http://")+":_authToken=resolver-secret\n"))
		env = append(env, "npm_config_cache="+filepath.Join(work, "cache"), "npm_config_update_notifier=false")
		privateClientCommand(t, work, env, os.Getenv("PRIVATE_FORMATS_NPM"), "pack", "private-demo@1.0.0", "--registry", registry, "--ignore-scripts", "--json")
		verify(filepath.Join(work, "private-demo-1.0.0.tgz"), assets["/private-demo/-/private-demo-1.0.0.tgz"].body)
	case repository.FormatPyPI:
		privateClientFile(t, filepath.Join(work, ".netrc"), []byte("machine 127.0.0.1 login build-agent password resolver-secret\n"))
		privateClientCommand(t, work, env, os.Getenv("PRIVATE_FORMATS_PYTHON"), "-m", "pip", "--isolated", "download", "--disable-pip-version-check", "--no-cache-dir", "--no-deps", "--keyring-provider", "disabled", "--index-url", registry+"simple/", "--trusted-host", "127.0.0.1", "--dest", work, "private-demo==1.0.0")
		verify(filepath.Join(work, "private_demo-1.0.0-py3-none-any.whl"), assets["/packages/private_demo-1.0.0-py3-none-any.whl"].body)
	case repository.FormatCargo:
		privateClientFile(t, filepath.Join(work, "cargo-home/config.toml"), []byte("[registries.private]\nindex=\"sparse+"+registry+"\"\ncredential-provider=\"cargo:token\"\n"))
		privateClientFile(t, filepath.Join(work, "Cargo.toml"), []byte("[package]\nname=\"private-consumer\"\nversion=\"0.1.0\"\nedition=\"2024\"\n[dependencies]\ndemo={ version=\"=1.0.0\", registry=\"private\" }\n"))
		privateClientFile(t, filepath.Join(work, "src/lib.rs"), []byte("pub fn value() -> u8 { demo::value() }\n"))
		env = append(env, "CARGO_HOME="+filepath.Join(work, "cargo-home"), "CARGO_TARGET_DIR="+filepath.Join(work, "target"), "CARGO_REGISTRIES_PRIVATE_TOKEN=resolver-secret")
		privateClientCommand(t, work, env, "cargo", "fetch")
		archives, err := filepath.Glob(filepath.Join(work, "cargo-home/registry/cache/*/demo-1.0.0.crate"))
		if err != nil || len(archives) != 1 {
			t.Fatalf("Cargo did not retrieve one complete crate: %v", err)
		}
		verify(archives[0], assets["/api/v1/crates/demo/1.0.0/download"].body)
	case repository.FormatRaw:
		body := privateClientCommand(t, work, env, "curl", "--disable", "--fail", "--silent", "--show-error", "--noproxy", "*", "--header", "Authorization: Bearer resolver-secret", registry+"private.bin")
		if !bytes.Equal(body, assets["/private.bin"].body) {
			t.Fatal("curl complete Raw bytes differ")
		}
	case repository.FormatConan:
		env = append(env, "CONAN_HOME="+filepath.Join(work, "conan-home"), "CONAN_NON_INTERACTIVE=1")
		conan := os.Getenv("CONAN_BINARY")
		privateClientCommand(t, work, env, conan, "remote", "add", "--force", "gateway", strings.TrimSuffix(registry, "/"))
		privateClientCommand(t, work, env, conan, "remote", "login", "gateway", "build-agent", "-p", "resolver-secret")
		privateClientCommand(t, work, env, conan, "download", "pkg/1.0@user/stable#aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "-r=gateway")
		found := 0
		if err := filepath.WalkDir(filepath.Join(work, "conan-home"), func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !entry.IsDir() && entry.Name() == "conanfile.py" {
				verify(path, assets["/conanfile.py"].body)
				found++
			}
			return nil
		}); err != nil || found == 0 {
			t.Fatalf("Conan did not download a verified recipe: %v", err)
		}
	case repository.FormatAPT:
		privateAPTOfficialClient(t, work, registry, env, assets)
	}
	t.Logf("official %s %s: fresh client cache, complete source bytes verified through authenticated Gateway", format, round)
}

func privateAPTOfficialClient(t *testing.T, work, registry string, env []string, assets map[string]redirectCacheAsset) {
	t.Helper()
	_, port, err := net.SplitHostPort(strings.TrimPrefix(strings.SplitN(registry, "/apt/", 2)[0], "http://"))
	if err != nil {
		t.Fatal(err)
	}
	target := "http://host.docker.internal:" + port + "/apt/" + strings.SplitN(registry, "/apt/", 2)[1]
	privateClientFile(t, filepath.Join(work, "source.asc"), assets["fixture-public-key"].body)
	if err := os.Chmod(filepath.Join(work, "source.asc"), 0o644); err != nil {
		t.Fatal(err)
	}
	privateClientFile(t, filepath.Join(work, "auth.conf"), []byte("machine "+target+"\nlogin build-agent\npassword resolver-secret\n"))
	privateClientFile(t, filepath.Join(work, "sources.list"), []byte("deb [arch=amd64 signed-by=/work/source.asc] "+target+" stable main\n"))
	project := os.Getenv("TEST_PRIVATE_FORMATS_PROJECT")
	container := project + "-apt-" + uuid.NewString()
	command := `mkdir -p /work/lists/partial /work/cache/archives/partial
opts='-o Dir::Etc::sourcelist=/work/sources.list -o Dir::Etc::sourceparts=- -o Dir::Etc::netrc=/work/auth.conf -o Dir::State::lists=/work/lists -o Dir::Cache=/work/cache -o APT::Architecture=amd64 -o Acquire::Retries=0 -o APT::Sandbox::User=root'
apt-get $opts update
cd /work
apt-get $opts download private-demo=1.0.0
`
	privateClientCommand(t, work, append(env, "DOCKER_HOST="+os.Getenv("TEST_PRIVATE_FORMATS_DOCKER_HOST")), "docker", "run", "--rm", "--name", container, "--label", "io.artifact-gateway.private-formats="+project, "--network", os.Getenv("TEST_PRIVATE_FORMATS_NETWORK"), "--add-host", "host.docker.internal:host-gateway", "--volume", work+":/work", "debian:bookworm-slim@sha256:abd67ffcfa541b485a3dff59865ab629aa048a6c613e639d36e7456b0b229241", "/bin/sh", "-ec", command)
	files, err := filepath.Glob(filepath.Join(work, "private-demo_1.0.0_*.deb"))
	if err != nil || len(files) != 1 {
		t.Fatalf("APT did not download one authenticated package: %v", err)
	}
	actual, err := os.ReadFile(files[0])
	if err != nil || !bytes.Equal(actual, assets["/pool/private-demo.deb"].body) {
		t.Fatal("APT complete deb bytes differ")
	}
}
