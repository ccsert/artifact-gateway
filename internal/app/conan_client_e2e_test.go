package app

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

func TestConan2ClientListsRevisionThroughGateway(t *testing.T) {
	conan := os.Getenv("CONAN_BINARY")
	if conan == "" {
		t.Skip("set CONAN_BINARY to run the Conan 2 client fixture")
	}
	store := repository.NewMemoryStore()
	_, _ = store.CreateConanGroup(context.Background(), repository.Group{Name: "central", Anonymous: true, Members: []repository.Member{{Name: "hosted", Type: repository.MemberHosted, Anonymous: true}}})
	h := ConanHandler{Store: store, Authenticator: testAuthenticator(), Client: &conanAnonymousClient{}, Cache: NewConanCache(nil), Metrics: &Metrics{}}
	server := httptest.NewServer(h)
	defer server.Close()
	home := t.TempDir()
	run := func(args ...string) {
		command := exec.Command(conan, args...)
		command.Env = append(os.Environ(), "CONAN_HOME="+home)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("conan %v: %v\n%s", args, err, output)
		}
	}
	run("remote", "add", "--force", "gateway", server.URL+"/conan/central")
	run("list", "pkg/1.0@user/stable#*", "-r=gateway")
}

func TestConan2ClientDownloadsRevisionedRecipeThroughGateway(t *testing.T) {
	conan := os.Getenv("CONAN_BINARY")
	if conan == "" {
		t.Skip("set CONAN_BINARY to run the Conan 2 client fixture")
	}
	store := repository.NewMemoryStore()
	_, _ = store.CreateConanGroup(context.Background(), repository.Group{Name: "central", Anonymous: true, Members: []repository.Member{{Name: "hosted", Type: repository.MemberHosted, Anonymous: true}}})
	client := newConanDownloadClient()
	h := ConanHandler{Store: store, Authenticator: testAuthenticator(), Client: client, Cache: NewConanCache(nil), Metrics: &Metrics{}}
	var gatewayPaths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gatewayPaths = append(gatewayPaths, r.URL.Path)
		h.ServeHTTP(w, r)
	}))
	defer server.Close()
	home := t.TempDir()
	run := func(args ...string) {
		command := exec.Command(conan, args...)
		command.Env = append(os.Environ(), "CONAN_HOME="+home)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("conan %v: %v\n%s\ngateway=%v upstream=%v", args, err, output, gatewayPaths, client.paths)
		}
	}
	run("remote", "add", "--force", "gateway", server.URL+"/conan/central")
	run("download", "pkg/1.0@user/stable#aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "-r=gateway")
}

func TestConan2ClientDownloadsRevisionedPackageThroughGateway(t *testing.T) {
	conan := os.Getenv("CONAN_BINARY")
	if conan == "" {
		t.Skip("set CONAN_BINARY to run the Conan 2 client fixture")
	}
	store := repository.NewMemoryStore()
	_, _ = store.CreateConanGroup(context.Background(), repository.Group{Name: "central", Anonymous: true, Members: []repository.Member{{Name: "hosted", Type: repository.MemberHosted, Anonymous: true}}})
	client := newConanPackageClient()
	server := httptest.NewServer(ConanHandler{Store: store, Authenticator: testAuthenticator(), Client: client, Cache: NewConanCache(nil), Metrics: &Metrics{}})
	defer server.Close()
	home := t.TempDir()
	command := exec.Command(conan, "download", "pkg/1.0@user/stable#aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa:package-id#bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "-r=gateway")
	command.Env = append(os.Environ(), "CONAN_HOME="+home)
	add := exec.Command(conan, "remote", "add", "--force", "gateway", server.URL+"/conan/central")
	add.Env = command.Env
	if output, err := add.CombinedOutput(); err != nil {
		t.Fatalf("add remote: %v\n%s", err, output)
	}
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("download package: %v\n%s\npaths=%v", err, output, client.paths)
	}
}

func TestConan2ClientGatewayResolutionAndCacheMatrix(t *testing.T) {
	conan := os.Getenv("CONAN_BINARY")
	if conan == "" {
		t.Skip("set CONAN_BINARY to run the Conan 2 client fixture")
	}

	store := repository.NewMemoryStore()
	hosted := repository.Member{Name: "hosted", Type: repository.MemberHosted, Anonymous: true, Position: 0}
	proxy := repository.Member{Name: "proxy", Type: repository.MemberProxy, Anonymous: true, Position: 1, Endpoint: "https://proxy.example", AllowedHosts: []string{"proxy.example"}}
	_, _ = store.CreateConanGroup(context.Background(), repository.Group{Name: "central", Anonymous: true, Members: []repository.Member{hosted, proxy}})
	client := &conanMatrixClient{}
	handler := ConanHandler{Store: store, Authenticator: testAuthenticator(), Client: client, Cache: NewConanCache(nil), Metrics: &Metrics{}}
	server := httptest.NewServer(handler)
	defer server.Close()

	home := t.TempDir()
	run := func(args ...string) {
		command := exec.Command(conan, args...)
		command.Env = append(os.Environ(), "CONAN_HOME="+home)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("conan %v: %v\n%s\nupstream=%v", args, err, output, client.paths())
		}
	}
	run("remote", "add", "--force", "gateway", server.URL+"/conan/central")
	run("list", "pkg/1.0@user/stable#*", "-r=gateway")
	counts := client.counts()
	if counts.hostedCalls != 1 || counts.proxyCalls != 1 {
		t.Fatalf("first resolution hosted=%d proxy=%d, want fallback through both members once", counts.hostedCalls, counts.proxyCalls)
	}
	run("list", "pkg/1.0@user/stable#*", "-r=gateway")
	counts = client.counts()
	if counts.hostedCalls != 1 || counts.proxyCalls != 1 {
		t.Fatalf("cache hit reached upstream: hosted=%d proxy=%d", counts.hostedCalls, counts.proxyCalls)
	}
	run("download", "hosted/1.0@user/stable#aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "-r=gateway")
	counts = client.counts()
	if counts.hostedPriorityCalls == 0 || counts.proxyPriorityCalls != 0 {
		t.Fatalf("Hosted priority hosted=%d proxy=%d", counts.hostedPriorityCalls, counts.proxyPriorityCalls)
	}

	for range 2 {
		run("list", "missing/1.0@user/stable#*", "-r=gateway")
	}
	counts = client.counts()
	if counts.missingHostedCalls != 1 || counts.missingProxyCalls != 1 {
		t.Fatalf("negative cache reached upstream: hosted=%d proxy=%d", counts.missingHostedCalls, counts.missingProxyCalls)
	}
	run("list", "failure/1.0@user/stable#*", "-r=gateway")
	firstFailure := client.counts()
	if firstFailure.failureHostedCalls == 0 || firstFailure.failureProxyCalls == 0 {
		t.Fatalf("first upstream failure did not reach both members: %+v", firstFailure)
	}
	run("list", "failure/1.0@user/stable#*", "-r=gateway")
	secondFailure := client.counts()
	if secondFailure.failureHostedCalls <= firstFailure.failureHostedCalls || secondFailure.failureProxyCalls <= firstFailure.failureProxyCalls {
		t.Fatalf("upstream failure was cached: first=%+v second=%+v", firstFailure, secondFailure)
	}
	failureResponse, err := http.Get(server.URL + "/conan/v2/central/conans/failure/1.0/user/stable/revisions")
	if err != nil {
		t.Fatal(err)
	}
	_ = failureResponse.Body.Close()
	if failureResponse.StatusCode != http.StatusBadGateway {
		t.Fatalf("upstream failure status=%d", failureResponse.StatusCode)
	}
	counts = client.counts()
	if counts.failureHostedCalls <= secondFailure.failureHostedCalls || counts.failureProxyCalls <= secondFailure.failureProxyCalls {
		t.Fatalf("direct 502 did not reach upstream: before=%+v after=%+v", secondFailure, counts)
	}
	if audit := store.Audits[len(store.Audits)-1]; audit.Outcome != repository.AuditUpstreamError || audit.Resource != "failure/1.0/user/stable/revisions" {
		t.Fatalf("failure audit=%#v", audit)
	}
	_, _ = store.CreateConanGroup(context.Background(), repository.Group{Name: "private", Members: []repository.Member{{Name: "private-hosted", Type: repository.MemberHosted}}})
	run("remote", "add", "--force", "private", server.URL+"/conan/private")
	run("list", "pkg/1.0@user/stable#*", "-r=private", "-cc", "core:non_interactive=True")
	privateResponse, err := http.Get(server.URL + "/conan/v2/private/conans/pkg/1.0/user/stable/revisions")
	if err != nil {
		t.Fatal(err)
	}
	_ = privateResponse.Body.Close()
	if privateResponse.StatusCode != http.StatusUnauthorized {
		t.Fatalf("private anonymous status=%d", privateResponse.StatusCode)
	}
	_, _ = store.CreateConanGroup(context.Background(), repository.Group{Name: "denied-proxy", Anonymous: true, Members: []repository.Member{{Name: "denied", Type: repository.MemberProxy, Anonymous: true, Endpoint: "https://proxy.example", AllowedHosts: []string{"other.example"}}}})
	run("remote", "add", "--force", "denied-proxy", server.URL+"/conan/denied-proxy")
	beforeDeniedProxy := client.counts()
	run("list", "pkg/1.0@user/stable#*", "-r=denied-proxy", "-cc", "core:non_interactive=True")
	deniedProxyResponse, err := http.Get(server.URL + "/conan/v2/denied-proxy/conans/pkg/1.0/user/stable/revisions")
	if err != nil {
		t.Fatal(err)
	}
	_ = deniedProxyResponse.Body.Close()
	if deniedProxyResponse.StatusCode != http.StatusForbidden {
		t.Fatalf("denied Proxy status=%d", deniedProxyResponse.StatusCode)
	}
	afterDeniedProxy := client.counts()
	if afterDeniedProxy != beforeDeniedProxy {
		t.Fatalf("denied Proxy reached upstream: before=%+v after=%+v", beforeDeniedProxy, afterDeniedProxy)
	}
	if len(store.Audits) == 0 {
		t.Fatal("expected Conan audit records")
	}
}

func TestConan2ClientRejectsChecksumMismatch(t *testing.T) {
	conan := os.Getenv("CONAN_BINARY")
	if conan == "" {
		t.Skip("set CONAN_BINARY to run the Conan 2 client fixture")
	}
	store := repository.NewMemoryStore()
	_, _ = store.CreateConanGroup(context.Background(), repository.Group{Name: "central", Anonymous: true, Members: []repository.Member{{Name: "hosted", Type: repository.MemberHosted, Anonymous: true}}})
	client := &conanChecksumClient{}
	server := httptest.NewServer(ConanHandler{Store: store, Authenticator: testAuthenticator(), Client: client, Cache: NewConanCache(nil), Metrics: &Metrics{}})
	defer server.Close()
	home := t.TempDir()
	add := exec.Command(conan, "remote", "add", "--force", "gateway", server.URL+"/conan/central")
	add.Env = append(os.Environ(), "CONAN_HOME="+home)
	if output, err := add.CombinedOutput(); err != nil {
		t.Fatalf("add remote: %v\n%s", err, output)
	}
	for range 2 {
		command := exec.Command(conan, "download", "pkg/1.0@user/stable#aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "-r=gateway")
		command.Env = add.Env
		if output, err := command.CombinedOutput(); err == nil {
			t.Fatalf("checksum-mismatched download unexpectedly succeeded\n%s", output)
		}
	}
	if client.fileCalls != 2 {
		t.Fatalf("checksum mismatch was cached: file calls=%d", client.fileCalls)
	}
}

func TestConan2ClientAuthenticationAndAnonymousPolicyMatrix(t *testing.T) {
	conan := os.Getenv("CONAN_BINARY")
	if conan == "" {
		t.Skip("set CONAN_BINARY to run the Conan 2 client fixture")
	}
	store := repository.NewMemoryStore()
	for _, group := range []repository.Group{
		{Name: "authenticated", Members: []repository.Member{{Name: "hosted", Type: repository.MemberHosted}}},
		{Name: "group-only", Anonymous: true, Members: []repository.Member{{Name: "hosted", Type: repository.MemberHosted}}},
		{Name: "member-only", Members: []repository.Member{{Name: "hosted", Type: repository.MemberHosted, Anonymous: true}}},
		{Name: "public", Anonymous: true, Members: []repository.Member{{Name: "hosted", Type: repository.MemberHosted, Anonymous: true}}},
	} {
		if _, err := store.CreateConanGroup(context.Background(), group); err != nil {
			t.Fatal(err)
		}
	}
	authenticator := Authenticator{
		ResolverToken:     "resolver-secret",
		RepositoryReaders: map[string][]string{"reader": {"authenticated", "hosted"}, "denied": {"authenticated"}},
	}
	handler := ConanHandler{Store: store, Authenticator: authenticator, Client: &conanAnonymousClient{}, Cache: NewConanCache(nil), Metrics: &Metrics{}}
	var requests []string
	var requestMu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestMu.Lock()
		requests = append(requests, r.Method+" "+r.URL.Path)
		requestMu.Unlock()
		handler.ServeHTTP(w, r)
	}))
	defer server.Close()
	home := t.TempDir()
	run := func(env []string, args ...string) {
		command := exec.Command(conan, args...)
		command.Env = append(append(os.Environ(), "CONAN_HOME="+home), env...)
		if output, err := command.CombinedOutput(); err != nil {
			requestMu.Lock()
			seen := append([]string(nil), requests...)
			requestMu.Unlock()
			t.Fatalf("conan %v: %v\n%s\nrequests=%v", args, err, output, seen)
		}
	}
	runFails := func(args ...string) {
		command := exec.Command(conan, args...)
		command.Env = append(os.Environ(), "CONAN_HOME="+home)
		if output, err := command.CombinedOutput(); err == nil {
			t.Fatalf("conan %v unexpectedly succeeded\n%s", args, output)
		}
	}
	for _, group := range []string{"authenticated", "group-only", "member-only", "public"} {
		run(nil, "remote", "add", "--force", group, server.URL+"/conan/"+group)
	}
	run(nil, "remote", "login", "authenticated", "reader", "-p", "resolver-secret")
	requestMu.Lock()
	loginRequest := "GET /conan/authenticated/v2/users/authenticate"
	foundLogin := slices.Contains(requests, loginRequest)
	requestMu.Unlock()
	if !foundLogin {
		t.Fatalf("Conan login did not send %q: %v", loginRequest, requests)
	}
	run(nil, "list", "pkg/1.0@user/stable#*", "-r=authenticated")
	run(nil, "remote", "add", "--force", "denied-reader", server.URL+"/conan/authenticated")
	run(nil, "remote", "login", "denied-reader", "denied", "-p", "resolver-secret")
	runFails("download", "pkg/1.0@user/stable", "-r=denied-reader")
	for _, group := range []string{"group-only", "member-only"} {
		runFails("download", "pkg/1.0@user/stable", "-r="+group)
	}
	run(nil, "list", "pkg/1.0@user/stable#*", "-r=public")
}

func TestConan2ClientUsesAllowlistedHTTPSProxy(t *testing.T) {
	conan := os.Getenv("CONAN_BINARY")
	if conan == "" {
		t.Skip("set CONAN_BINARY to run the Conan 2 client fixture")
	}
	var upstreamCalls atomic.Int32
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls.Add(1)
		if r.URL.Path != "/conans/pkg/1.0/user/stable/revisions" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"revisions":[{"revision":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","time":"2024-01-01T00:00:00Z"}]}`))
	}))
	defer upstream.Close()
	host, port := rawTLSServerAddress(t, upstream.URL)
	withRawProxyNetwork(t, func(_ context.Context, network, name string) ([]net.IP, error) {
		if network != "ip" || name != "example.com" {
			t.Fatalf("lookup=%s %s", network, name)
		}
		return []net.IP{net.ParseIP("203.0.113.23")}, nil
	}, func(ctx context.Context, network, address string) (net.Conn, error) {
		if address != net.JoinHostPort("203.0.113.23", port) {
			t.Fatalf("dial=%s", address)
		}
		return (&net.Dialer{}).DialContext(ctx, network, net.JoinHostPort(host, port))
	})
	endpoint := "https://example.com:" + port
	store := repository.NewMemoryStore()
	_, _ = store.CreateConanGroup(context.Background(), repository.Group{Name: "allowed", Anonymous: true, Members: []repository.Member{{Name: "proxy", Type: repository.MemberProxy, Endpoint: endpoint, Anonymous: true, AllowedHosts: []string{"example.com"}}}})
	_, _ = store.CreateConanGroup(context.Background(), repository.Group{Name: "denied", Anonymous: true, Members: []repository.Member{{Name: "proxy", Type: repository.MemberProxy, Endpoint: endpoint, Anonymous: true, AllowedHosts: []string{"other.example"}}}})
	server := httptest.NewServer(ConanHandler{Store: store, Authenticator: testAuthenticator(), Client: UpstreamClient{HTTPClient: upstream.Client()}, Cache: NewConanCache(nil), Metrics: &Metrics{}})
	defer server.Close()
	home := t.TempDir()
	run := func(args ...string) {
		command := exec.Command(conan, args...)
		command.Env = append(os.Environ(), "CONAN_HOME="+home)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("conan %v: %v\n%s", args, err, output)
		}
	}
	runFails := func(args ...string) {
		command := exec.Command(conan, args...)
		command.Env = append(os.Environ(), "CONAN_HOME="+home)
		if output, err := command.CombinedOutput(); err == nil {
			t.Fatalf("conan %v unexpectedly succeeded\n%s", args, output)
		}
	}
	run("remote", "add", "--force", "allowed", server.URL+"/conan/allowed")
	run("list", "pkg/1.0@user/stable#*", "-r=allowed")
	if got := upstreamCalls.Load(); got != 1 {
		t.Fatalf("allowlisted Proxy calls=%d", got)
	}
	run("remote", "add", "--force", "denied", server.URL+"/conan/denied")
	runFails("download", "pkg/1.0@user/stable", "-r=denied")
	if got := upstreamCalls.Load(); got != 1 {
		t.Fatalf("denied Proxy reached upstream: calls=%d", got)
	}
}

type conanMatrixClient struct {
	mu                                      sync.Mutex
	seen                                    []string
	hostedCalls, proxyCalls                 int
	missingHostedCalls, missingProxyCalls   int
	hostedPriorityCalls, proxyPriorityCalls int
	failureHostedCalls, failureProxyCalls   int
}

type conanMatrixCounts struct {
	hostedCalls, proxyCalls                 int
	missingHostedCalls, missingProxyCalls   int
	hostedPriorityCalls, proxyPriorityCalls int
	failureHostedCalls, failureProxyCalls   int
}

func (c *conanMatrixClient) FetchConan(_ context.Context, _ string, member repository.Member, path string, _ http.Header) (*http.Response, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.seen = append(c.seen, path)
	if strings.HasPrefix(path, "hosted/") || strings.Contains(path, "/hosted/") {
		if member.Type == repository.MemberHosted {
			c.hostedPriorityCalls++
			recipeFiles := map[string][]byte{"conanfile.py": []byte("from conan import ConanFile\nclass Hosted(ConanFile): pass\n"), "conanmanifest.txt": []byte("manifest\n")}
			packageFiles := map[string][]byte{"conan_package.tgz": conanPackageArchive(), "conaninfo.txt": []byte("[settings]\n"), "conanmanifest.txt": []byte("manifest\n")}
			if strings.HasSuffix(path, "/search") {
				return conanJSON(`{"packages":{}}`), nil
			}
			if strings.HasSuffix(path, "/latest") {
				return conanJSON(`{"revision":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","time":"2024-01-01T00:00:00Z"}`), nil
			}
			if strings.HasSuffix(path, "/files") {
				if strings.Contains(path, "/packages/") {
					return conanFilesJSON(packageFiles), nil
				}
				return conanFilesJSON(recipeFiles), nil
			}
			name := path[strings.LastIndex(path, "/")+1:]
			files := recipeFiles
			if strings.Contains(path, "/packages/") {
				files = packageFiles
			}
			if body, ok := files[name]; ok {
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(body))}, nil
			}
			return conanJSON(`{"revisions":[{"revision":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","time":"2024-01-01T00:00:00Z"}]}`), nil
		}
		c.proxyPriorityCalls++
		return conanJSON(`{"revisions":[{"revision":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","time":"2024-01-01T00:00:00Z"}]}`), nil
	}
	if strings.HasPrefix(path, "failure/") || strings.Contains(path, "/failure/") {
		if member.Type == repository.MemberHosted {
			c.failureHostedCalls++
		} else {
			c.failureProxyCalls++
		}
		return nil, fmt.Errorf("fixture upstream unavailable")
	}
	if strings.HasPrefix(path, "missing/") || strings.Contains(path, "/missing/") {
		if member.Type == repository.MemberHosted {
			c.missingHostedCalls++
		} else {
			c.missingProxyCalls++
		}
		return &http.Response{StatusCode: http.StatusNotFound, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(""))}, nil
	}
	if member.Type == repository.MemberHosted {
		c.hostedCalls++
		return &http.Response{StatusCode: http.StatusNotFound, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(""))}, nil
	}
	c.proxyCalls++
	return conanJSON(`{"revisions":[{"revision":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","time":"2024-01-01T00:00:00Z"}]}`), nil
}

func (c *conanMatrixClient) paths() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.seen...)
}

func (c *conanMatrixClient) counts() conanMatrixCounts {
	c.mu.Lock()
	defer c.mu.Unlock()
	return conanMatrixCounts{
		hostedCalls:         c.hostedCalls,
		proxyCalls:          c.proxyCalls,
		missingHostedCalls:  c.missingHostedCalls,
		missingProxyCalls:   c.missingProxyCalls,
		hostedPriorityCalls: c.hostedPriorityCalls,
		proxyPriorityCalls:  c.proxyPriorityCalls,
		failureHostedCalls:  c.failureHostedCalls,
		failureProxyCalls:   c.failureProxyCalls,
	}
}

type conanPackageClient struct {
	*conanDownloadClient
	packageFiles map[string][]byte
}

func newConanPackageClient() *conanPackageClient {
	return &conanPackageClient{conanDownloadClient: newConanDownloadClient(), packageFiles: map[string][]byte{"conan_package.tgz": conanPackageArchive(), "conaninfo.txt": []byte("[settings]\n"), "conanmanifest.txt": []byte("manifest\n")}}
}
func conanPackageArchive() []byte {
	var output bytes.Buffer
	gzipWriter := gzip.NewWriter(&output)
	tarWriter := tar.NewWriter(gzipWriter)
	_ = tarWriter.WriteHeader(&tar.Header{Name: "package.txt", Mode: 0644, Size: int64(len("package binary"))})
	_, _ = tarWriter.Write([]byte("package binary"))
	_ = tarWriter.Close()
	_ = gzipWriter.Close()
	return output.Bytes()
}
func (c *conanPackageClient) FetchConan(ctx context.Context, method string, member repository.Member, path string, headers http.Header) (*http.Response, error) {
	c.paths = append(c.paths, path)
	if strings.Contains(path, "/packages/package-id/") {
		if strings.HasSuffix(path, "/revisions") {
			return conanJSON(`{"revisions":[{"revision":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","time":"2024-01-01T00:00:00Z"}]}`), nil
		}
		if strings.HasSuffix(path, "/files") {
			return conanFilesJSON(c.packageFiles), nil
		}
		name := path[strings.LastIndex(path, "/")+1:]
		if body, ok := c.packageFiles[name]; ok {
			return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(string(body)))}, nil
		}
	}
	return c.conanDownloadClient.FetchConan(ctx, method, member, path, headers)
}
func conanFilesJSON(files map[string][]byte) *http.Response {
	values := make([]string, 0, len(files))
	for name, body := range files {
		sum := sha256.Sum256(body)
		values = append(values, fmt.Sprintf(`%q:{"sha256":%q,"size":%d}`, name, hex.EncodeToString(sum[:]), len(body)))
	}
	return conanJSON(`{"files":{` + strings.Join(values, ",") + `}}`)
}

type conanDownloadClient struct {
	files, packageFiles map[string][]byte
	paths               []string
}

func newConanDownloadClient() *conanDownloadClient {
	return &conanDownloadClient{
		files:        map[string][]byte{"conanfile.py": []byte("from conan import ConanFile\nclass Pkg(ConanFile):\n    name = 'pkg'\n    version = '1.0'\n"), "conanmanifest.txt": []byte("manifest\n")},
		packageFiles: map[string][]byte{"conan_package.tgz": conanPackageArchive(), "conaninfo.txt": []byte("[settings]\n"), "conanmanifest.txt": []byte("manifest\n")},
	}
}
func (c *conanDownloadClient) FetchConan(_ context.Context, _ string, _ repository.Member, path string, _ http.Header) (*http.Response, error) {
	c.paths = append(c.paths, path)
	if strings.Contains(path, "/packages/packages/") {
		if strings.HasSuffix(path, "/latest") {
			return conanJSON(`{"revision":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","time":"2024-01-01T00:00:00Z"}`), nil
		}
		if strings.HasSuffix(path, "/revisions") {
			return conanJSON(`{"revisions":[{"revision":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","time":"2024-01-01T00:00:00Z"}]}`), nil
		}
		if strings.HasSuffix(path, "/files") {
			return conanFilesJSON(c.packageFiles), nil
		}
		name := path[strings.LastIndex(path, "/")+1:]
		if body, ok := c.packageFiles[name]; ok {
			return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(body))}, nil
		}
	}
	if strings.HasSuffix(path, "/search") {
		return conanJSON(`{"packages":{}}`), nil
	}
	if strings.HasSuffix(path, "/revisions") {
		return conanJSON(`{"revisions":[{"revision":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","time":"2024-01-01T00:00:00Z"}]}`), nil
	}
	if strings.HasSuffix(path, "/files") {
		files := make([]string, 0, len(c.files))
		for name, body := range c.files {
			sum := sha256.Sum256(body)
			files = append(files, fmt.Sprintf(`%q:{"sha256":%q,"size":%d}`, name, hex.EncodeToString(sum[:]), len(body)))
		}
		return conanJSON(`{"files":{` + strings.Join(files, ",") + `}}`), nil
	}
	name := path[strings.LastIndex(path, "/")+1:]
	if body, ok := c.files[name]; ok {
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(string(body)))}, nil
	}
	return &http.Response{StatusCode: http.StatusNotFound, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(""))}, nil
}
func conanJSON(body string) *http.Response {
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}
}
