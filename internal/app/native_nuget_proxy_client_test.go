//go:build integration && nugetclient

package app

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
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

	"github.com/ccsert/artifact-gateway/internal/authorization"
	nugetformat "github.com/ccsert/artifact-gateway/internal/format/nuget"
	"github.com/ccsert/artifact-gateway/internal/objectstore"
	nugetprotocol "github.com/ccsert/artifact-gateway/internal/protocol/nuget"
	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

func startNuGetProxyGatewayContainer(t *testing.T, c nugetOfficialClient, bucket, address string, extraEnv ...string) func() {
	t.Helper()
	binary, err := os.ReadFile(os.Getenv("NUGET_GATEWAY_LINUX_BIN"))
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(c.work, "gateway-linux"), binary, 0700); err != nil {
		t.Fatal(err)
	}
	_, port, _ := net.SplitHostPort(address)
	name := "ag288-gateway-" + uuid.NewString()
	owner := uuid.NewString()
	hostURL := func(raw string) string {
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatal("invalid owned runtime address")
		}
		u.Host = net.JoinHostPort("host.docker.internal", u.Port())
		return u.String()
	}
	listenHost := "0.0.0.0"
	if c.sharedHostNetwork {
		listenHost = "127.0.0.1"
	}
	env := []string{"GATEWAY_DATABASE_URL=" + hostURL(os.Getenv("TEST_DATABASE_URL")), "GATEWAY_RUSTFS_ENDPOINT=" + hostURL(os.Getenv("TEST_RUSTFS_ENDPOINT")), "GATEWAY_RUSTFS_ACCESS_KEY=" + os.Getenv("TEST_RUSTFS_ACCESS_KEY"), "GATEWAY_RUSTFS_SECRET_KEY=" + os.Getenv("TEST_RUSTFS_SECRET_KEY"), "GATEWAY_RUSTFS_BUCKET=" + bucket, "GATEWAY_ADMIN_TOKEN=" + c.admin, "GATEWAY_ADMIN_ACTOR=nuget-gate-operator", "GATEWAY_RESOLVER_TOKEN=" + c.reader, "GATEWAY_RESOLVER_ACTOR=nuget-gate-reader", "GATEWAY_NUGET_PREVIEW_ENABLED=true", "GATEWAY_NODE_ROLES=api", "GATEWAY_LISTEN_ADDRESS=" + net.JoinHostPort(listenHost, port), "GATEWAY_ACCESS_LOG=limited", "HOME=/work/gateway-home", "HTTP_PROXY=", "http_proxy="}
	env = append(env, extraEnv...)
	if err = os.MkdirAll(filepath.Join(c.work, "gateway-home"), 0700); err != nil {
		t.Fatal(err)
	}
	args := []string{"run", "--rm", "--name", name, "--label", "com.docker.compose.project=" + c.project, "--label", "artifact-gateway.nuget-fixture-owner=" + owner, "--user", strconv.Itoa(os.Getuid()) + ":" + strconv.Itoa(os.Getgid()), "--mount", "type=bind,src=" + c.work + ",dst=/work"}
	args = append(args, c.networkArgs(false)...)
	if !c.sharedHostNetwork {
		args = append(args, "--publish", "127.0.0.1:"+port+":"+port)
	}
	for _, value := range env {
		args = append(args, "--env", value)
	}
	args = append(args, c.image, "/work/gateway-linux")
	log, err := os.OpenFile(filepath.Join(c.evidence, "proxy-group-gateway.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("docker", args...)
	cmd.Stdout, cmd.Stderr = log, log
	if err = cmd.Start(); err != nil {
		_ = log.Close()
		t.Fatal("owned Gateway container did not start")
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	stopped := false
	stop := func() {
		if stopped {
			return
		}
		stopped = true
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		out, inspectErr := exec.CommandContext(ctx, "docker", "inspect", "--format", `{{index .Config.Labels "com.docker.compose.project"}}/{{index .Config.Labels "artifact-gateway.nuget-fixture-owner"}}`, name).Output()
		if inspectErr == nil && strings.TrimSpace(string(out)) == c.project+"/"+owner {
			if err := exec.CommandContext(ctx, "docker", "rm", "--force", name).Run(); err != nil {
				t.Error("owned Gateway container removal failed")
			}
		}
		select {
		case <-done:
		case <-ctx.Done():
			_ = cmd.Process.Kill()
			<-done
			t.Error("owned Gateway Docker command failed to join")
		}
		_ = log.Close()
	}
	t.Cleanup(stop)
	client := &http.Client{Timeout: time.Second}
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		response, err := client.Get("http://127.0.0.1:" + port + "/readyz")
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode == 200 || response.StatusCode == 204 {
				return stop
			}
		}
		select {
		case err := <-done:
			stopped = true
			_ = log.Close()
			t.Fatalf("owned Gateway runtime exited before ready: %v", err)
		case <-time.After(100 * time.Millisecond):
		}
	}
	stop()
	t.Fatal("owned Linux Gateway did not become ready")
	return stop
}

// The real Gateway uses its supported environment egress mode and an owned CA.
// This CONNECT endpoint maps exactly one fixture origin, forwards TLS untouched,
// joins every tunnel and never resolves/dials a user-provided destination.
type nugetOwnedSource struct {
	server, proxy                      *httptest.Server
	caFile, endpoint, proxyURL, secret string
	mu                                 sync.Mutex
	requests                           map[string]int
	schemes                            map[string]int
	leaked                             bool
	sockets                            map[net.Conn]bool
	wg                                 sync.WaitGroup
	closed                             bool
}

func newNuGetOwnedSource(t *testing.T, c nugetOfficialClient, packages [][]byte) *nugetOwnedSource {
	t.Helper()
	secret := "nuget-upstream-only-" + uuid.NewString()
	store := repository.NewMemoryStore()
	objects := objectstore.NewMemoryStore()
	repo, err := store.CreateHostedRepository(t.Context(), repository.HostedRepository{ID: uuid.NewString(), Name: "source-package-hive", Format: repository.FormatNuGet, Type: repository.RepositoryTypeHosted})
	if err != nil {
		t.Fatal(err)
	}
	auth := authorization.Authenticator{AdminToken: secret, AdminActor: "owned-upstream"}
	module, err := nugetformat.New(nugetformat.Options{Store: store, Repositories: store, Objects: objects, Authenticator: auth, Authorizer: authorization.RepositoryAuthorizer{Grants: store, Legacy: auth}, Audit: store})
	if err != nil {
		t.Fatal(err)
	}
	for _, body := range packages {
		var data bytes.Buffer
		writer := multipart.NewWriter(&data)
		part, err := writer.CreateFormFile("package", "source.nupkg")
		if err != nil {
			t.Fatal(err)
		}
		_, _ = part.Write(body)
		if err = writer.Close(); err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest("PUT", "https://private.nuget-gate.test/nuget/"+repo.Name+"/api/v2/package/", &data)
		r.Header.Set("Content-Type", writer.FormDataContentType())
		r.Header.Set("X-NuGet-ApiKey", secret)
		w := httptest.NewRecorder()
		module.ServeHTTP(w, r)
		if w.Code != 201 {
			t.Fatalf("owned source publication: %d %s", w.Code, w.Body.String())
		}
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), DNSNames: []string{"private.nuget-gate.test"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	caFile := filepath.Join(c.work, "source-ca.pem")
	if err = os.WriteFile(caFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); err != nil {
		t.Fatal(err)
	}
	f := &nugetOwnedSource{caFile: caFile, secret: secret, endpoint: "https://private.nuget-gate.test/nuget/" + repo.Name + "/v3/index.json", requests: map[string]int{}, schemes: map[string]int{}, sockets: map[net.Conn]bool{}}
	f.server = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.requests[r.URL.Path]++
		if _, password, ok := r.BasicAuth(); ok && (password == c.admin || password == c.reader) {
			f.leaked = true
		}
		scheme, _, _ := strings.Cut(r.Header.Get("Authorization"), " ")
		f.schemes[scheme]++
		for _, values := range r.Header {
			for _, value := range values {
				if strings.Contains(value, c.admin) || strings.Contains(value, c.reader) {
					f.leaked = true
				}
			}
		}
		f.mu.Unlock()
		serveNuGetOwnedSource(t, module, w, r)
	}))
	f.server.TLS = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}}
	f.server.StartTLS()
	address := strings.TrimPrefix(f.server.URL, "https://")
	f.proxy = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "CONNECT" || r.Host != "private.nuget-gate.test:443" || r.Header.Get("Authorization") != "" || r.Header.Get("Proxy-Authorization") != "" {
			http.Error(w, "unapproved fixture tunnel", 403)
			return
		}
		upstream, err := (&net.Dialer{}).DialContext(r.Context(), "tcp", address)
		if err != nil {
			http.Error(w, "fixture source unavailable", 502)
			return
		}
		client, buffer, err := w.(http.Hijacker).Hijack()
		if err != nil {
			_ = upstream.Close()
			return
		}
		f.mu.Lock()
		if f.closed {
			f.mu.Unlock()
			_ = client.Close()
			_ = upstream.Close()
			return
		}
		f.sockets[client], f.sockets[upstream] = true, true
		f.wg.Add(1)
		f.mu.Unlock()
		defer f.wg.Done()
		defer func() {
			_ = client.Close()
			_ = upstream.Close()
			f.mu.Lock()
			delete(f.sockets, client)
			delete(f.sockets, upstream)
			f.mu.Unlock()
		}()
		_, _ = buffer.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n")
		_ = buffer.Flush()
		done := make(chan struct{})
		go func() { _, _ = io.Copy(upstream, buffer); _ = upstream.Close(); close(done) }()
		_, _ = io.Copy(client, upstream)
		_ = client.Close()
		<-done
	}))
	f.proxyURL = f.proxy.URL
	t.Cleanup(f.close)
	return f
}

// The source fixture exposes a legitimate upstream unlisted version without
// adding a local Hosted governance API. Keep the wire ETag bound to those
// actual bytes so conditional/offline reads exercise the same representation.
func serveNuGetOwnedSource(t *testing.T, module *nugetformat.Module, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	if !strings.Contains(r.URL.Path, "/registration/group.entry/") {
		module.ServeHTTP(w, r)
		return
	}
	request := r.Clone(r.Context())
	request.Header = r.Header.Clone()
	request.Header.Del("If-None-Match")
	request.Header.Del("If-Modified-Since")
	recorder := httptest.NewRecorder()
	module.ServeHTTP(recorder, request)
	body := recorder.Body.Bytes()
	if recorder.Code == http.StatusOK {
		reader, err := gzip.NewReader(bytes.NewReader(body))
		if err != nil {
			t.Error("owned source registration is not gzip")
			w.WriteHeader(500)
			return
		}
		plain, err := io.ReadAll(reader)
		_ = reader.Close()
		if err != nil {
			t.Error("owned source registration could not be read")
			w.WriteHeader(500)
			return
		}
		var document any
		if json.Unmarshal(plain, &document) != nil {
			t.Error("owned source registration is not JSON")
			w.WriteHeader(500)
			return
		}
		var unlist func(any)
		unlist = func(value any) {
			switch value := value.(type) {
			case map[string]any:
				if value["version"] == "2.0.0" {
					value["listed"] = false
				}
				if catalog, ok := value["catalogEntry"].(map[string]any); ok && catalog["version"] == "2.0.0" {
					value["listed"] = false
				}
				for _, child := range value {
					unlist(child)
				}
			case []any:
				for _, child := range value {
					unlist(child)
				}
			}
		}
		unlist(document)
		plain, err = json.Marshal(document)
		if err != nil {
			t.Error("owned registration serialization failed")
			w.WriteHeader(500)
			return
		}
		var encoded bytes.Buffer
		writer := gzip.NewWriter(&encoded)
		_, _ = writer.Write(plain)
		_ = writer.Close()
		body = encoded.Bytes()
	}
	for name, values := range recorder.Header() {
		w.Header()[name] = values
	}
	w.Header().Del("Content-Length")
	sum := sha256.Sum256(body)
	etag := `"sha256:` + hex.EncodeToString(sum[:]) + `"`
	w.Header().Set("ETag", etag)
	if recorder.Code == http.StatusOK && r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.WriteHeader(recorder.Code)
	_, _ = w.Write(body)
}
func (f *nugetOwnedSource) close() {
	f.mu.Lock()
	if f.closed {
		f.mu.Unlock()
		return
	}
	f.closed = true
	for socket := range f.sockets {
		_ = socket.Close()
	}
	f.mu.Unlock()
	f.proxy.Close()
	f.server.Close()
	f.wg.Wait()
}

func TestNativeNuGetRealDotnetPrivateProxyGroupRestore(t *testing.T) {
	for _, key := range []string{"NUGET_GATEWAY_LINUX_BIN", "NUGET_DOTNET_IMAGE", "NUGET_COMPOSE_PROJECT", "NUGET_EVIDENCE_DIR", "TEST_DATABASE_URL", "TEST_RUSTFS_ENDPOINT"} {
		if os.Getenv(key) == "" {
			t.Skip("owned native-nuget-e2e fixture is required")
		}
	}
	c := nugetOfficialClient{work: t.TempDir(), image: os.Getenv("NUGET_DOTNET_IMAGE"), project: os.Getenv("NUGET_COMPOSE_PROJECT"), evidence: os.Getenv("NUGET_EVIDENCE_DIR"), admin: "nuget-proxy-admin-" + uuid.NewString(), reader: "nuget-proxy-reader-" + uuid.NewString(), sharedHostNetwork: runtime.GOOS == "linux"}
	if out := c.succeeds(t, "proxy-tools", "sdk-version", true, "--version"); strings.TrimSpace(out) != "10.0.100" {
		t.Fatalf("unapproved SDK: %q", out)
	}
	writeNuGetClientFile(t, c.work, "pack.config", `<configuration><packageSources><clear/><add key="owned" value="/work/feed"/></packageSources></configuration>`)
	if err := os.MkdirAll(filepath.Join(c.work, "feed"), 0700); err != nil {
		t.Fatal(err)
	}
	packages := map[string][]byte{}
	for _, item := range []struct{ dir, id, version, ns, dependency, expression string }{
		{"Leaf", "Source.Leaf", "1.0.0", "ProxyLeaf", "", `"proxy-leaf"`},
		{"Middle", "Source.Middle", "1.0.0", "ProxyMiddle", "Source.Leaf", `ProxyLeaf.Message.Value+":proxy-middle"`},
		{"Entry", "Group.Entry", "1.0.0", "GroupEntry", "Source.Middle", `ProxyMiddle.Message.Value+":private-hosted"`},
		{"Entry2", "Group.Entry", "2.0.0", "GroupEntry", "Source.Middle", `ProxyMiddle.Message.Value+":proxy-v2"`},
		{"Confusion", "Group.Entry", "1.0.0", "GroupEntry", "", `"must-never-win"`},
	} {
		dependency := ""
		if item.dependency != "" {
			dependency = `<ItemGroup><PackageReference Include="` + item.dependency + `" Version="[1.0.0]"/></ItemGroup>`
		}
		project := `<Project Sdk="Microsoft.NET.Sdk"><PropertyGroup><TargetFramework>net10.0</TargetFramework><PackageId>` + item.id + `</PackageId><Version>` + item.version + `</Version><NuGetAudit>false</NuGetAudit><Authors>Owned fixture</Authors><Description>Private NuGet source acceptance</Description></PropertyGroup>` + dependency + `</Project>`
		writeNuGetClientFile(t, c.work, item.dir+"/"+item.dir+".csproj", project)
		writeNuGetClientFile(t, c.work, item.dir+"/Message.cs", `namespace `+item.ns+`; public static class Message { public static string Value => `+item.expression+`; }`)
		c.succeeds(t, "proxy-pack", "pack-"+item.dir, true, "pack", "/work/"+item.dir+"/"+item.dir+".csproj", "--output", "/work/feed", "-p:RestoreConfigFile=/work/pack.config", "--disable-build-servers", "-p:NuGetAudit=false")
		body, err := os.ReadFile(filepath.Join(c.work, "feed", item.id+"."+item.version+".nupkg"))
		if err != nil {
			t.Fatal(err)
		}
		packages[item.dir] = body
	}
	upstream := newNuGetOwnedSource(t, c, [][]byte{packages["Leaf"], packages["Middle"], packages["Entry2"], packages["Confusion"]})
	listener, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	_ = listener.Close()
	_, port, _ := net.SplitHostPort(address)
	host := "http://127.0.0.1:" + port
	bucket := "nuget-proxy-client-" + uuid.NewString()
	extra := []string{"GATEWAY_SETTINGS_ENCRYPTION_KEY=" + strings.Repeat("79", 32), "HTTPS_PROXY=" + strings.Replace(upstream.proxyURL, "127.0.0.1", "host.docker.internal", 1), "https_proxy=" + strings.Replace(upstream.proxyURL, "127.0.0.1", "host.docker.internal", 1), "NO_PROXY=127.0.0.1,localhost,host.docker.internal", "no_proxy=127.0.0.1,localhost,host.docker.internal", "SSL_CERT_FILE=/work/source-ca.pem"}
	stop := startNuGetProxyGatewayContainer(t, c, bucket, address, extra...)
	var hosted struct{ ID, Name, SourceURL string }
	nugetClientHTTP(t, host, c.admin, "POST", "/api/v2/previews/nuget/repositories", `{"name":"private-hosted-group"}`, 201, &hosted)
	// This archive was packed before the intentionally conflicting upstream
	// archive overwrote feed/Group.Entry.1.0.0.nupkg. Preserve its original bytes.
	if err = os.WriteFile(filepath.Join(c.work, "hosted-entry.nupkg"), packages["Entry"], 0600); err != nil {
		t.Fatal(err)
	}
	pushSource := "http://host.docker.internal:" + port + "/nuget/" + hosted.Name + "/v3/index.json"
	writeNuGetClientFile(t, c.work, "proxy-push.config", `<configuration><packageSources><clear/><add key="gateway" value="`+xmlText(pushSource)+`" allowInsecureConnections="true"/></packageSources><packageSourceCredentials><gateway><add key="Username" value="token"/><add key="ClearTextPassword" value="`+xmlText(c.admin)+`"/><add key="ValidAuthenticationTypes" value="basic"/></gateway></packageSourceCredentials></configuration>`)
	c.succeeds(t, "proxy-push", "hosted-private-push", false, "nuget", "push", "/work/hosted-entry.nupkg", "--source", "gateway", "--api-key", c.admin, "--configfile", "/work/proxy-push.config")
	store, err := repository.NewPostgresStore(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	if _, err = store.ReplaceRepositoryGrants(t.Context(), hosted.ID, []repository.RepositoryGrant{{Principal: "nuget-gate-reader", Scopes: []string{"repositories:read"}}}, "1"); err != nil {
		t.Fatal(err)
	}
	type sourceConfig struct{ ID, Name, Version, SourceURL string }
	type groupConfig struct{ ID, Name, Version, SourceURL string }
	sources := []sourceConfig{}
	groups := []groupConfig{}
	for _, scheme := range []string{"basic", "bearer"} {
		username := ""
		if scheme == "basic" {
			username = `,"username":"upstream-reader"`
		}
		input := fmt.Sprintf(`{"name":"private-proxy-%s","type":"proxy","endpoint":%q,"egressMode":"environment","upstreamAuth":{"scheme":%q%s,"secret":%q}}`, scheme, upstream.endpoint, scheme, username, upstream.secret)
		var proxy sourceConfig
		nugetClientHTTP(t, host, c.admin, "POST", "/api/v2/previews/nuget/repositories", input, 201, &proxy)
		sources = append(sources, proxy)
		if _, err = store.ReplaceRepositoryGrants(t.Context(), proxy.ID, []repository.RepositoryGrant{{Principal: "nuget-gate-reader", Scopes: []string{"repositories:read"}}}, "1"); err != nil {
			t.Fatal(err)
		}
		var group groupConfig
		nugetClientHTTP(t, host, c.admin, "POST", "/api/v2/previews/nuget/groups", fmt.Sprintf(`{"name":"private-group-%s","members":[%q,%q]}`, scheme, hosted.ID, proxy.ID), 201, &group)
		groups = append(groups, group)
		target := "http://host.docker.internal:" + port + "/nuget/" + group.Name + "/v3/index.json"
		config := `<configuration><packageSources><clear/><add key="gateway" value="` + xmlText(target) + `" allowInsecureConnections="true"/></packageSources><packageSourceCredentials><gateway><add key="Username" value="local-reader"/><add key="ClearTextPassword" value="` + xmlText(c.reader) + `"/><add key="ValidAuthenticationTypes" value="basic"/></gateway></packageSourceCredentials></configuration>`
		writeNuGetClientFile(t, c.work, scheme+".config", config)
	}
	checks := map[string]map[string]any{}
	verify := func() {
		t.Helper()
		for _, source := range sources {
			for _, item := range []struct{ id, version, key string }{{"source.leaf", "1.0.0", "Leaf"}, {"source.middle", "1.0.0", "Middle"}, {"group.entry", "2.0.0", "Entry2"}} {
				p, err := store.GetNuGetPublication(t.Context(), source.ID, item.id, item.version)
				if err != nil {
					t.Fatal(err)
				}
				expected := packages[item.key]
				sum := sha256.Sum256(expected)
				if p.Digest != "sha256:"+hex.EncodeToString(sum[:]) || p.Size != int64(len(expected)) || p.SourceVersion != source.Version || !strings.HasPrefix(p.SourceURL, "https://private.nuget-gate.test/") {
					t.Fatal("proxy immutable identity or bound source changed")
				}
				objects, err := objectstore.NewRustFSStore(os.Getenv("TEST_RUSTFS_ENDPOINT"), os.Getenv("TEST_RUSTFS_ACCESS_KEY"), os.Getenv("TEST_RUSTFS_SECRET_KEY"), bucket)
				if err != nil {
					t.Fatal(err)
				}
				reader, size, err := objects.Open(t.Context(), p.ObjectKey)
				if err != nil {
					t.Fatal(err)
				}
				actual, err := io.ReadAll(reader)
				_ = reader.Close()
				if err != nil || size != p.Size || !bytes.Equal(actual, expected) {
					t.Fatal("complete cached object changed")
				}
				inspection, err := nugetprotocol.InspectPackage(bytes.NewReader(actual), size)
				if err != nil || strings.ToLower(inspection.Identity.ID) != item.id || strings.ToLower(inspection.Identity.NormalizedVersion) != item.version || !bytes.Equal(inspection.Nuspec, p.Nuspec) {
					t.Fatal("complete archive/nuspec identity changed")
				}
				key := source.Name + "/" + item.id + "@" + item.version
				checks[key] = map[string]any{"repositoryId": source.ID, "id": item.id, "version": item.version, "digest": p.Digest, "size": p.Size, "sourceVersion": p.SourceVersion, "originalNuspec": true}
				if err = os.MkdirAll(filepath.Join(c.evidence, "proxy-group-client-packages"), 0700); err != nil {
					t.Fatal(err)
				}
				if err = os.WriteFile(filepath.Join(c.evidence, "proxy-group-client-packages", source.Name+"-"+item.id+"."+item.version+".nupkg"), actual, 0600); err != nil {
					t.Fatal(err)
				}
			}
		}
		for _, group := range groups {
			owner, err := store.GetNuGetGroupAuthority(t.Context(), group.ID, "group.entry", "1.0.0")
			if err != nil || owner.RepositoryID != hosted.ID {
				t.Fatal("same-ID upstream confusion replaced the private Hosted member")
			}
			owner, err = store.GetNuGetGroupAuthority(t.Context(), group.ID, "group.entry", "2.0.0")
			if err != nil || owner.RepositoryID == hosted.ID || owner.Digest == "" {
				t.Fatal("different package version lost its Proxy authority")
			}
			request, err := http.NewRequestWithContext(t.Context(), "GET", host+"/nuget/"+group.Name+"/v3/registration/group.entry/2.0.0.json", nil)
			if err != nil {
				t.Fatal(err)
			}
			request.Header.Set("Authorization", "Bearer "+c.reader)
			request.Header.Set("Accept-Encoding", "gzip")
			response, err := (&http.Client{Timeout: 15 * time.Second}).Do(request)
			if err != nil {
				t.Fatal("unlisted target registration unavailable")
			}
			reader, decodeErr := gzip.NewReader(response.Body)
			if decodeErr != nil {
				_ = response.Body.Close()
				t.Fatal("unlisted target registration is not gzip")
			}
			body, readErr := io.ReadAll(io.LimitReader(reader, (8<<20)+1))
			_ = reader.Close()
			_ = response.Body.Close()
			var leaf struct {
				Listed  *bool
				Catalog struct{ Listed *bool } `json:"catalogEntry"`
			}
			if response.StatusCode != 200 || readErr != nil || json.Unmarshal(body, &leaf) != nil || leaf.Listed == nil || *leaf.Listed || leaf.Catalog.Listed == nil || *leaf.Catalog.Listed {
				t.Fatal("exact unlisted dependency was rewritten as listed")
			}
		}
	}
	restore := func(phase string) {
		t.Helper()
		for index, scheme := range []string{"basic", "bearer"} {
			for _, version := range []string{"1.0.0", "2.0.0"} {
				dir := "Consumer-" + scheme + "-" + version
				project := `<Project Sdk="Microsoft.NET.Sdk"><PropertyGroup><OutputType>Exe</OutputType><TargetFramework>net10.0</TargetFramework><NuGetAudit>false</NuGetAudit></PropertyGroup><ItemGroup><PackageReference Include="Group.Entry" Version="[` + version + `]"/></ItemGroup></Project>`
				writeNuGetClientFile(t, c.work, dir+"/Consumer.csproj", project)
				writeNuGetClientFile(t, c.work, dir+"/Program.cs", `System.Console.WriteLine(GroupEntry.Message.Value);`)
				clientPhase := "proxy-" + phase + "-" + scheme + "-" + version
				c.succeeds(t, clientPhase, "target-only-transitive-restore", false, "restore", "/work/"+dir+"/Consumer.csproj", "--configfile", "/work/"+scheme+".config", "--no-http-cache", "--force", "-p:NuGetAudit=false", "--disable-build-servers")
				out := c.succeeds(t, clientPhase, "offline-transitive-execution", true, "run", "--project", "/work/"+dir+"/Consumer.csproj", "--no-restore", "--disable-build-servers")
				want := "proxy-leaf:proxy-middle:private-hosted"
				if version == "2.0.0" {
					want = "proxy-leaf:proxy-middle:proxy-v2"
				}
				if !strings.Contains(out, want) || strings.Contains(out, "must-never-win") {
					t.Fatalf("wrong source execution (%s %d)", phase, index)
				}
			}
		}
		verify()
	}
	restore("cold")
	upstream.mu.Lock()
	coldRequests := 0
	for path, count := range upstream.requests {
		if strings.HasSuffix(path, ".nupkg") {
			coldRequests += count
		}
	}
	upstream.mu.Unlock()
	restore("warm")
	upstream.mu.Lock()
	warmRequests := 0
	for path, count := range upstream.requests {
		if strings.HasSuffix(path, ".nupkg") {
			warmRequests += count
		}
	}
	upstream.mu.Unlock()
	if coldRequests != 6 || warmRequests != coldRequests {
		t.Fatalf("cold downloads=%d warm=%d", coldRequests, warmRequests)
	}
	// Exercise real expiry through the persistent cache port, without a clock
	// hook or a sleep-based timing assumption, before removing the source.
	for _, source := range sources {
		for _, id := range []string{"group.entry", "source.middle", "source.leaf"} {
			for _, target := range []string{upstream.endpoint, strings.TrimSuffix(upstream.endpoint, "index.json") + "flat/" + id + "/index.json", strings.TrimSuffix(upstream.endpoint, "index.json") + "registration/" + id + "/index.json"} {
				doc, err := store.GetNuGetSourceDocument(t.Context(), source.ID, source.Version, target)
				if err != nil {
					t.Fatal(err)
				}
				doc.FetchedAt = time.Now().UTC().Add(-time.Minute)
				doc.ExpiresAt = doc.FetchedAt.Add(time.Second)
				if err = store.PutNuGetSourceDocument(t.Context(), doc); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	stop()
	upstream.close()
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	restartNuGetOwnedService(t, c.project, "postgres")
	restartNuGetOwnedService(t, c.project, "rustfs")
	store, err = repository.NewPostgresStore(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	stop = startNuGetProxyGatewayContainer(t, c, bucket, address, extra...)
	restore("source-stopped-restarted")
	stop()
	upstream.mu.Lock()
	leaked := upstream.leaked
	schemes := map[string]int{}
	for k, v := range upstream.schemes {
		schemes[k] = v
	}
	remainingSockets := len(upstream.sockets)
	upstream.mu.Unlock()
	if leaked || schemes["Basic"] == 0 || schemes["Bearer"] == 0 || remainingSockets != 0 {
		t.Fatal("source credential isolation or owned tunnel cleanup failed")
	}
	data, _ := json.MarshalIndent(map[string]any{"client": "official .NET SDK 10.0.100", "toolchainMode": "official-container", "gatewayRuntime": "linux-sdk-container", "targetOnly": true, "emptyOwnedClientCaches": true, "privateHosted": hosted.ID, "proxySources": sources, "groups": groups, "verifiedPackages": checks, "sameIDConfusionRejected": true, "differentVersionOwner": true, "unlistedExactVersionConsumed": "group.entry@2.0.0", "transitivePackagesPerRestore": 3, "coldRestores": 4, "warmRestores": 4, "sourceStoppedRestartedColdRestores": 4, "coldSourcePackageDownloads": coldRequests, "warmSourcePackageDownloads": warmRequests, "sourceCredentialSchemes": schemes, "localCredentialLeakedToSource": leaked, "remainingOwnedTunnels": remainingSockets, "restarted": []string{"gateway", "postgres", "rustfs"}}, "", "  ")
	if err = os.WriteFile(filepath.Join(c.evidence, "proxy-group-client-acceptance.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
}
