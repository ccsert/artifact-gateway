package app

import (
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/artifact-gateway/artifact-gateway/internal/egress"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
	"golang.org/x/mod/sumdb"
	"golang.org/x/mod/sumdb/dirhash"
	"golang.org/x/mod/sumdb/note"
)

func TestParseGoProxyPathRoutesSumDBMirroring(t *testing.T) {
	probe, ok := parseGoProxyPath("/go/go-proxy/sumdb/sum.golang.org/supported")
	if !ok || probe.kind != "sumdb" || probe.repository != "go-proxy" ||
		probe.sumdbName != "sum.golang.org" || probe.sumdbPath != "supported" {
		t.Fatalf("probe=%#v ok=%t", probe, ok)
	}
	lookup, ok := parseGoProxyPath("/go/go-proxy/sumdb/sum.golang.org/lookup/github.com/x%2Fy@v1.0.0")
	if !ok || lookup.sumdbName != "sum.golang.org" || lookup.sumdbPath != "lookup/github.com/x/y@v1.0.0" {
		t.Fatalf("lookup=%#v ok=%t", lookup, ok)
	}
	for _, rejected := range []string{
		"/go/go-proxy/sumdb",
		"/go/go-proxy/sumdb/",
		"/go/go-proxy/sumdb/sum.golang.org",
		"/go/go-proxy/sumdb/sum.golang.org/",
	} {
		if route, accepted := parseGoProxyPath(rejected); accepted {
			t.Fatalf("sumdb route %q accepted as %#v", rejected, route)
		}
	}
}

func newGoSumDBFixture(t *testing.T) (sumdb *httptest.Server, lookupBody string) {
	t.Helper()
	lookupBody = "1 sha256-af351ffd7f37b26b0580a4a156a1a0171a7d7f62\nsumdb.gateway.test/lookup/github.com/x/y@v1.0.0 0\n— github.com/x/y@v1.0.0 placeholder\n"
	// Mirroring always egresses over HTTPS, so the fixture is a TLS server
	// and the shared upstream client must trust its certificate.
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/latest":
			w.Header().Set("Content-Type", "text/plain; charset=UTF-8")
			_, _ = w.Write([]byte("1671168000\nsha256-tree-head\n— sumdb.gateway.test placeholder\n"))
		case strings.HasPrefix(r.URL.Path, "/lookup/"):
			w.Header().Set("Content-Type", "text/plain; charset=UTF-8")
			_, _ = w.Write([]byte(lookupBody))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(upstream.Close)
	return upstream, lookupBody
}

func TestNativeGoSumDBMirrorsOnlyAllowlistedChecksumDatabases(t *testing.T) {
	// Mirrored checksum database fetches are HTTPS egress; keep the developer
	// shell's HTTP(S)_PROXY from intercepting the loopback fixture.
	t.Setenv("NO_PROXY", "127.0.0.1,localhost")
	t.Setenv("no_proxy", "127.0.0.1,localhost")
	sumdbServer, lookupBody := newGoSumDBFixture(t)
	sumdbHost := strings.TrimPrefix(sumdbServer.URL, "https://")
	moduleUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	t.Cleanup(moduleUpstream.Close)

	store := repository.NewMemoryStore()
	if _, err := store.ReplaceAnonymousAccessPolicy(context.Background(), repository.AnonymousAccessPolicy{Enabled: true}, "1"); err != nil {
		t.Fatal(err)
	}
	_, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{
		ID: uuid.NewString(), Name: "go-mirroring", Format: repository.FormatGo,
		Type: repository.RepositoryTypeProxy, Endpoint: moduleUpstream.URL,
		AllowedHosts: []string{sumdbHost, "sum.golang.org+032de1593235d1b1fa4d8799931925c7efdf2c9d"}, AnonymousRead: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.CreateHostedRepository(context.Background(), repository.HostedRepository{
		ID: uuid.NewString(), Name: "go-without-mirroring", Format: repository.FormatGo,
		Type: repository.RepositoryTypeProxy, Endpoint: moduleUpstream.URL, AnonymousRead: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.CreateHostedRepository(context.Background(), repository.HostedRepository{
		ID: uuid.NewString(), Name: "go-hosted", Format: repository.FormatGo, AnonymousRead: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Pin resolution of the allowlisted checksum database host to a public
	// address while dialing the loopback fixture, mirroring how production
	// reaches a public checksum database through the pinned egress client.
	_, sumdbPort, _ := net.SplitHostPort(sumdbHost)
	previousHooks := goSumDBEgressHooks
	goSumDBEgressHooks = func() egress.Hooks {
		return egress.Hooks{
			LookupIP: func(ctx context.Context, network, host string) ([]net.IP, error) {
				return []net.IP{net.ParseIP("93.184.216.34")}, nil
			},
			DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
				if _, _, err := net.SplitHostPort(address); err != nil {
					return nil, err
				}
				return (&net.Dialer{}).DialContext(ctx, network, net.JoinHostPort("127.0.0.1", sumdbPort))
			},
			ProxyFromEnvironment: http.ProxyFromEnvironment,
		}
	}
	t.Cleanup(func() { goSumDBEgressHooks = previousHooks })
	handler := NewGatewayHandler(
		Dependencies{NativeGoObjectStore: NewMemoryOCIObjectStore()}, store,
		TestAdapter{}, testAuthenticator(), UpstreamClient{HTTPClient: sumdbServer.Client()},
	)

	request := func(path string) *httptest.ResponseRecorder {
		t.Helper()
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		return response
	}

	// The supported probe succeeds for an allowlisted checksum database and
	// never leaves the Gateway.
	probe := request("/go/go-mirroring/sumdb/" + sumdbHost + "/supported")
	if probe.Code != http.StatusOK || probe.Body.Len() != 0 {
		t.Fatalf("supported=%d %q", probe.Code, probe.Body.String())
	}
	// The verifier-key name form reduces to the same allowlisted host.
	keyProbe := request("/go/go-mirroring/sumdb/" + sumdbHost + "+032de1593235d1b1/supported")
	if keyProbe.Code != http.StatusOK {
		t.Fatalf("key supported=%d %s", keyProbe.Code, keyProbe.Body.String())
	}

	lookup := request("/go/go-mirroring/sumdb/" + sumdbHost + "/lookup/github.com/x/y@v1.0.0")
	if lookup.Code != http.StatusOK || lookup.Body.String() != lookupBody {
		t.Fatalf("lookup=%d %q", lookup.Code, lookup.Body.String())
	}
	if lookup.Header().Get("Content-Type") != "text/plain; charset=UTF-8" {
		t.Fatalf("lookup content type=%q", lookup.Header().Get("Content-Type"))
	}

	// An upstream miss passes through so the go command keeps its own
	// not-found semantics.
	miss := request("/go/go-mirroring/sumdb/" + sumdbHost + "/missing")
	if miss.Code != http.StatusNotFound {
		t.Fatalf("upstream miss=%d", miss.Code)
	}

	// Names outside the allowlist stay 404 so the go command falls back to
	// its direct connection, and the well-known name is not implicitly
	// reachable either.
	for _, path := range []string{
		"/go/go-mirroring/sumdb/sum.golang.org/supported",
		"/go/go-mirroring/sumdb/evil.example.test/latest",
		"/go/go-without-mirroring/sumdb/" + sumdbHost + "/supported",
		"/go/go-hosted/sumdb/" + sumdbHost + "/supported",
	} {
		if response := request(path); response.Code != http.StatusNotFound {
			t.Fatalf("%s = %d, want 404", path, response.Code)
		}
	}
}

// TestNativeGoRealClientVerifiesChecksumsThroughSumDBMirror drives the real go
// command with GOSUMDB pointed at a signed local checksum database. The go
// command must discover mirroring through the Gateway's supported probe,
// fetch the lookup and tile bytes through the Go Proxy repository, and verify
// the module ZIP and go.mod against the signed tree.
func TestNativeGoRealClientVerifiesChecksumsThroughSumDBMirror(t *testing.T) {
	if os.Getenv("ARTIFACT_GATEWAY_GO_CLI_E2E") == "" {
		t.Skip("set ARTIFACT_GATEWAY_GO_CLI_E2E=1 to run the real Go client acceptance test")
	}
	t.Setenv("NO_PROXY", "127.0.0.1,localhost,example.com")
	t.Setenv("no_proxy", "127.0.0.1,localhost,example.com")

	const (
		modulePath    = "example.com/Acme/sumdb"
		escapedModule = "example.com/!acme/sumdb"
		version       = "v1.0.0"
	)
	mod := []byte("module " + modulePath + "\n\ngo 1.26\n")
	archive := goModuleFixtureZip(t, modulePath, version, map[string]string{
		"go.mod":   string(mod),
		"sumdb.go": "package sumdb\n",
	})
	zipFile := filepath.Join(t.TempDir(), "module.zip")
	if err := os.WriteFile(zipFile, archive, 0o600); err != nil {
		t.Fatal(err)
	}
	zipHash, err := dirhash.HashZip(zipFile, dirhash.Hash1)
	if err != nil {
		t.Fatal(err)
	}
	goModHash, err := dirhash.Hash1([]string{"go.mod"}, func(string) (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(mod)), nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// httptest issues a certificate for example.com, so the checksum
	// database fixture answers under that name.
	const sumdbName = "example.com"
	signerKey, verifierKey, err := note.GenerateKey(rand.Reader, sumdbName)
	if err != nil {
		t.Fatal(err)
	}
	var sumdbRequests int32
	tree := sumdb.NewTestServer(signerKey, func(path, vers string) ([]byte, error) {
		return []byte(fmt.Sprintf("%s %s/go.mod %s\n%s %s %s\n", path, vers, goModHash, path, vers, zipHash)), nil
	})
	sumdbHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&sumdbRequests, 1)
		t.Logf("sumdb stub request: %s", r.URL.Path)
		sumdb.NewServer(tree).ServeHTTP(w, r)
	})

	// The checksum database fixture is a loopback TLS service; production
	// egress policy requires a public resolution, so the test pins resolution
	// to a public address while dialing the fixture. The module upstream
	// stays plain HTTP and needs no hook.
	sumdbServer := httptest.NewTLSServer(sumdbHandler)
	t.Cleanup(sumdbServer.Close)
	_, sumdbPort, err := net.SplitHostPort(strings.TrimPrefix(sumdbServer.URL, "https://"))
	if err != nil {
		t.Fatal(err)
	}
	previousHooks := goSumDBEgressHooks
	goSumDBEgressHooks = func() egress.Hooks {
		return egress.Hooks{
			LookupIP: func(ctx context.Context, network, host string) ([]net.IP, error) {
				return []net.IP{net.ParseIP("93.184.216.34")}, nil
			},
			DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
				if _, _, err := net.SplitHostPort(address); err != nil {
					return nil, err
				}
				return (&net.Dialer{}).DialContext(ctx, network, net.JoinHostPort("127.0.0.1", sumdbPort))
			},
			ProxyFromEnvironment: http.ProxyFromEnvironment,
		}
	}
	t.Cleanup(func() { goSumDBEgressHooks = previousHooks })

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/" + escapedModule + "/@v/list":
			_, _ = io.WriteString(w, version+"\n")
		case "/" + escapedModule + "/@v/" + version + ".info":
			_, _ = io.WriteString(w, `{"Version":"`+version+`","Time":"2026-08-09T09:00:00Z"}`)
		case "/" + escapedModule + "/@v/" + version + ".mod":
			_, _ = w.Write(mod)
		case "/" + escapedModule + "/@v/" + version + ".zip":
			_, _ = w.Write(archive)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(upstream.Close)

	store := repository.NewMemoryStore()
	if _, err = store.ReplaceAnonymousAccessPolicy(context.Background(), repository.AnonymousAccessPolicy{Enabled: true}, "1"); err != nil {
		t.Fatal(err)
	}
	repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{
		ID: uuid.NewString(), Name: "go-sumdb-mirror", Format: repository.FormatGo,
		Type: repository.RepositoryTypeProxy, Endpoint: upstream.URL,
		AllowedHosts: []string{sumdbName}, AnonymousRead: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	gateway := httptest.NewServer(NewGatewayHandler(
		Dependencies{NativeGoObjectStore: NewMemoryOCIObjectStore()}, store,
		TestAdapter{}, testAuthenticator(),
		UpstreamClient{HTTPClient: sumdbServer.Client()},
	))
	t.Cleanup(gateway.Close)

	temporary := t.TempDir()
	t.Cleanup(func() {
		_ = filepath.WalkDir(temporary, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return nil
			}
			if entry.IsDir() {
				return os.Chmod(path, 0o700)
			}
			return os.Chmod(path, 0o600)
		})
	})
	command := exec.CommandContext(context.Background(), "go", "mod", "download", "-json", modulePath+"@"+version)
	command.Dir = temporary
	// Replace, not append: a duplicated GOPATH would let the child reuse a
	// cached tree head signed by an earlier run's ephemeral key, and the
	// developer shell's HTTP(S)_PROXY must not intercept loopback fixtures.
	childEnv := make([]string, 0, len(os.Environ())+8)
	for _, entry := range os.Environ() {
		if key, _, found := strings.Cut(entry, "="); found {
			switch key {
			case "GOPATH", "GOSUMDB", "GOPROXY", "GONOPROXY", "GONOSUMDB", "GOPRIVATE", "GOFLAGS", "NO_PROXY", "no_proxy", "HTTP_PROXY", "HTTPS_PROXY", "http_proxy", "https_proxy", "all_proxy", "ALL_PROXY":
				continue
			}
		}
		childEnv = append(childEnv, entry)
	}
	command.Env = append(childEnv,
		"GOPROXY="+gateway.URL+"/repository/"+repo.Name,
		"GOSUMDB="+verifierKey,
		"GONOPROXY=none",
		"GONOSUMDB=",
		"GOPRIVATE=",
		"GOMODCACHE="+filepath.Join(temporary, "module-cache"),
		"GOCACHE="+filepath.Join(temporary, "build-cache"),
		// The sumdb cache must stay isolated: a tree head cached by an
		// earlier run fails verification against this run's ephemeral key.
		"GOPATH="+filepath.Join(temporary, "gopath"),
		"NO_PROXY=127.0.0.1,localhost,example.com",
		"no_proxy=127.0.0.1,localhost,example.com",
	)
	// In-process verification of the same mirror the go subprocess uses.
	verifier, verifierErr := note.NewVerifier(verifierKey)
	if verifierErr != nil {
		t.Fatal(verifierErr)
	}
	sumdbTarget, urlErr := url.Parse("https://" + sumdbName + "/latest")
	if urlErr != nil {
		t.Fatal(urlErr)
	}
	latestResponse, latestErr := UpstreamClient{HTTPClient: sumdbServer.Client()}.FetchGoSumDB(context.Background(), repo, sumdbTarget)
	if latestErr != nil {
		t.Fatalf("in-process latest: %v", latestErr)
	}
	latestBytes, _ := io.ReadAll(latestResponse.Body)
	_ = latestResponse.Body.Close()
	if _, openErr := note.Open(latestBytes, note.VerifierList(verifier)); openErr != nil {
		t.Fatalf("in-process note verification failed: %v\nnote: %s", openErr, latestBytes)
	}

	output, runErr := command.CombinedOutput()
	if runErr != nil {
		t.Fatalf("go mod download with mirrored checksum database failed: %v\n%s", runErr, output)
	}
	if !strings.Contains(string(output), `"Version": "`+version+`"`) {
		t.Fatalf("sumdb stub requests=%d go download output: %s", atomic.LoadInt32(&sumdbRequests), output)
	}
}
