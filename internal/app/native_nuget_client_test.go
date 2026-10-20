//go:build integration && nugetclient

package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	nugetprotocol "github.com/ccsert/artifact-gateway/internal/protocol/nuget"
	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

type nugetOfficialClient struct {
	work, image, project, evidence string
	admin, reader                  string
}

func (c nugetOfficialClient) command(t *testing.T, phase, name string, offline bool, args ...string) (string, error) {
	t.Helper()
	for _, dir := range []string{"home-" + phase, "cli-" + phase, "packages-" + phase, "http-" + phase, "plugins-" + phase} {
		if err := os.MkdirAll(filepath.Join(c.work, dir), 0700); err != nil {
			t.Fatal(err)
		}
	}
	dockerArgs := []string{"run", "--rm", "--label", "com.docker.compose.project=" + c.project, "--user", strconv.Itoa(os.Getuid()) + ":" + strconv.Itoa(os.Getgid()), "--mount", "type=bind,src=" + c.work + ",dst=/work", "--workdir", "/work", "--env", "HOME=/work/home-" + phase, "--env", "DOTNET_CLI_HOME=/work/cli-" + phase, "--env", "NUGET_PACKAGES=/work/packages-" + phase, "--env", "NUGET_HTTP_CACHE_PATH=/work/http-" + phase, "--env", "NUGET_PLUGINS_CACHE_PATH=/work/plugins-" + phase, "--env", "DOTNET_CLI_TELEMETRY_OPTOUT=1", "--env", "DOTNET_SKIP_FIRST_TIME_EXPERIENCE=1", "--env", "DOTNET_NOLOGO=1", "--env", "DOTNET_MULTILEVEL_LOOKUP=0", "--env", "NUGET_CREDENTIALPROVIDERS_PATH=/work/no-providers"}
	if offline {
		dockerArgs = append(dockerArgs, "--network", "none")
	} else {
		dockerArgs = append(dockerArgs, "--add-host", "host.docker.internal:host-gateway")
	}
	dockerArgs = append(dockerArgs, c.image, "dotnet")
	dockerArgs = append(dockerArgs, args...)
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "docker", dockerArgs...).CombinedOutput()
	safe := strings.ReplaceAll(strings.ReplaceAll(string(out), c.admin, "[private-test-credential]"), c.reader, "[private-test-credential]")
	log, openErr := os.OpenFile(filepath.Join(c.evidence, "client.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if openErr != nil {
		t.Fatal(openErr)
	}
	_, _ = fmt.Fprintf(log, "\n[%s/%s]\n%s", phase, name, safe)
	_ = log.Close()
	return safe, err
}

func (c nugetOfficialClient) succeeds(t *testing.T, phase, name string, offline bool, args ...string) string {
	t.Helper()
	out, err := c.command(t, phase, name, offline, args...)
	if err != nil {
		t.Fatalf("official client %s: %v\n%s", name, err, out)
	}
	return out
}

func writeNuGetClientFile(t *testing.T, root, path, body string) {
	t.Helper()
	target := filepath.Join(root, path)
	if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
}

func xmlText(s string) string {
	var out bytes.Buffer
	_ = xml.EscapeText(&out, []byte(s))
	return out.String()
}

func startNuGetGatewayBinary(t *testing.T, c nugetOfficialClient, bucket, address string) func() {
	t.Helper()
	file, err := os.OpenFile(filepath.Join(c.evidence, "gateway.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Getenv("NUGET_GATEWAY_BIN"))
	for _, e := range os.Environ() {
		if !strings.HasPrefix(e, "GATEWAY_") {
			cmd.Env = append(cmd.Env, e)
		}
	}
	cmd.Env = append(cmd.Env, "GATEWAY_DATABASE_URL="+os.Getenv("TEST_DATABASE_URL"), "GATEWAY_RUSTFS_ENDPOINT="+os.Getenv("TEST_RUSTFS_ENDPOINT"), "GATEWAY_RUSTFS_ACCESS_KEY="+os.Getenv("TEST_RUSTFS_ACCESS_KEY"), "GATEWAY_RUSTFS_SECRET_KEY="+os.Getenv("TEST_RUSTFS_SECRET_KEY"), "GATEWAY_RUSTFS_BUCKET="+bucket, "GATEWAY_ADMIN_TOKEN="+c.admin, "GATEWAY_ADMIN_ACTOR=nuget-gate-operator", "GATEWAY_RESOLVER_TOKEN="+c.reader, "GATEWAY_RESOLVER_ACTOR=nuget-gate-reader", "GATEWAY_NUGET_PREVIEW_ENABLED=true", "GATEWAY_NODE_ROLES=api", "GATEWAY_LISTEN_ADDRESS="+address, "GATEWAY_ACCESS_LOG=limited")
	cmd.Stdout, cmd.Stderr = file, file
	if err = cmd.Start(); err != nil {
		_ = file.Close()
		t.Fatal(err)
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
		_ = file.Close()
	}
	t.Cleanup(stop)
	_, port, _ := net.SplitHostPort(address)
	url := "http://127.0.0.1:" + port + "/readyz"
	client := &http.Client{Timeout: time.Second}
	deadline := time.Now().Add(25 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := client.Get(url)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == 204 || resp.StatusCode == 200 {
				return stop
			}
		}
		select {
		case err := <-done:
			stopped = true
			_ = file.Close()
			t.Fatalf("real gateway exited before ready: %v", err)
		case <-time.After(100 * time.Millisecond):
		}
	}
	stop()
	t.Fatal("real NuGet gateway did not become ready")
	return stop
}

func nugetClientHTTP(t *testing.T, base, token, method, path, body string, want int, target any) {
	t.Helper()
	r, err := http.NewRequest(method, base+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("Idempotency-Key", uuid.NewString())
	r.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode != want {
		t.Fatalf("%s %s=%d %s", method, path, resp.StatusCode, data)
	}
	if target != nil && json.Unmarshal(data, target) != nil {
		t.Fatalf("invalid response %s", data)
	}
}

func restartNuGetOwnedService(t *testing.T, project, service string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "docker", "ps", "-q", "--filter", "label=com.docker.compose.project="+project, "--filter", "label=com.docker.compose.service="+service).Output()
	if err != nil {
		t.Fatal(err)
	}
	ids := strings.Fields(string(out))
	if len(ids) != 1 {
		t.Fatalf("owned %s containers=%d", service, len(ids))
	}
	if out, err = exec.CommandContext(ctx, "docker", "restart", ids[0]).CombinedOutput(); err != nil {
		t.Fatalf("owned %s restart: %v %s", service, err, out)
	}
	if service == "postgres" {
		for {
			out, err = exec.CommandContext(ctx, "docker", "inspect", "--format", "{{.State.Health.Status}}", ids[0]).Output()
			if err == nil && strings.TrimSpace(string(out)) == "healthy" {
				break
			}
			select {
			case <-ctx.Done():
				t.Fatal("owned PostgreSQL did not recover")
			case <-time.After(100 * time.Millisecond):
			}
		}
	}
	if service == "rustfs" {
		client := &http.Client{Timeout: time.Second}
		for {
			request, _ := http.NewRequestWithContext(ctx, http.MethodGet, os.Getenv("TEST_RUSTFS_ENDPOINT")+"/health", nil)
			response, err := client.Do(request)
			if err == nil {
				_ = response.Body.Close()
				if response.StatusCode == 200 {
					break
				}
			}
			select {
			case <-ctx.Done():
				t.Fatal("owned RustFS did not recover")
			case <-time.After(100 * time.Millisecond):
			}
		}
	}
}

func TestNativeNuGetRealDotnetPushAndColdTransitiveRestore(t *testing.T) {
	for _, key := range []string{"NUGET_GATEWAY_BIN", "NUGET_DOTNET_IMAGE", "NUGET_COMPOSE_PROJECT", "NUGET_EVIDENCE_DIR", "TEST_DATABASE_URL", "TEST_RUSTFS_ENDPOINT"} {
		if os.Getenv(key) == "" {
			t.Skip("owned native-nuget-e2e fixture is required")
		}
	}
	c := nugetOfficialClient{work: t.TempDir(), image: os.Getenv("NUGET_DOTNET_IMAGE"), project: os.Getenv("NUGET_COMPOSE_PROJECT"), evidence: os.Getenv("NUGET_EVIDENCE_DIR"), admin: "nuget-gate-admin-" + uuid.NewString(), reader: "nuget-gate-read-" + uuid.NewString()}
	if out := c.succeeds(t, "tools", "sdk-version", true, "--version"); strings.TrimSpace(out) != "10.0.100" {
		t.Fatalf("unapproved SDK: %q", out)
	}
	listener, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	_ = listener.Close()
	_, port, _ := net.SplitHostPort(address)
	bucket := "nuget-client-" + uuid.NewString()
	stop := startNuGetGatewayBinary(t, c, bucket, address)
	host := "http://127.0.0.1:" + port
	var repo struct {
		ID, Name, SourceURL string
		Preview             bool
	}
	nugetClientHTTP(t, host, c.admin, "POST", "/api/v2/previews/nuget/repositories", `{"name":"nuget-official"}`, 201, &repo)
	if !repo.Preview || repo.ID == "" {
		t.Fatalf("not an explicit preview: %+v", repo)
	}
	store, err := repository.NewPostgresStore(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	if _, err = store.ReplaceRepositoryGrants(t.Context(), repo.ID, []repository.RepositoryGrant{{Principal: "nuget-gate-reader", Scopes: []string{"repositories:read"}, ResourcePrefix: "hosted."}}, "1"); err != nil {
		t.Fatal(err)
	}
	source := "http://host.docker.internal:" + port + "/nuget/nuget-official/v3/index.json"
	config := func(secret string) string {
		return `<?xml version="1.0"?><configuration><packageSources><clear/><add key="gateway" value="` + xmlText(source) + `" allowInsecureConnections="true"/></packageSources><packageSourceCredentials><gateway><add key="Username" value="token"/><add key="ClearTextPassword" value="` + xmlText(secret) + `"/><add key="ValidAuthenticationTypes" value="basic"/></gateway></packageSourceCredentials></configuration>`
	}
	writeNuGetClientFile(t, c.work, "push.config", config(c.admin))
	writeNuGetClientFile(t, c.work, "read.config", config(c.reader))
	writeNuGetClientFile(t, c.work, "offline.config", `<configuration><packageSources><clear/></packageSources></configuration>`)
	writeNuGetClientFile(t, c.work, "pack.config", `<configuration><packageSources><clear/><add key="owned-fixtures" value="/work/feed"/></packageSources></configuration>`)
	if err = os.MkdirAll(filepath.Join(c.work, "feed"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct{ id, dependency, expression string }{{"Leaf", "", `"leaf"`}, {"Middle", "Leaf", `HostedLeaf.Message.Value+":middle"`}, {"App", "Middle", `HostedMiddle.Message.Value+":app"`}} {
		dependency := ""
		if item.dependency != "" {
			dependency = `<ItemGroup><PackageReference Include="Hosted.` + item.dependency + `" Version="1.0.0" /></ItemGroup>`
		}
		project := `<Project Sdk="Microsoft.NET.Sdk"><PropertyGroup><TargetFramework>net10.0</TargetFramework><PackageId>Hosted.` + item.id + `</PackageId><Version>1.0.0</Version><Authors>Gateway owned fixture</Authors><Description>NuGet Hosted acceptance</Description><NuGetAudit>false</NuGetAudit></PropertyGroup>` + dependency + `</Project>`
		writeNuGetClientFile(t, c.work, item.id+"/"+item.id+".csproj", project)
		writeNuGetClientFile(t, c.work, item.id+"/Message.cs", `namespace Hosted`+item.id+`; public static class Message { public static string Value => `+item.expression+`; }`)
		c.succeeds(t, "pack", "pack-"+item.id, true, "pack", "/work/"+item.id+"/"+item.id+".csproj", "--output", "/work/feed", "-p:RestoreConfigFile=/work/pack.config", "--disable-build-servers", "-p:NuGetAudit=false")
		packageName := "Hosted." + item.id + ".1.0.0.nupkg"
		body, err := os.ReadFile(filepath.Join(c.work, "feed", packageName))
		if err != nil {
			t.Fatal(err)
		}
		if err = os.MkdirAll(filepath.Join(c.evidence, "client-packages"), 0700); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(c.evidence, "client-packages", packageName), body, 0600); err != nil {
			t.Fatal(err)
		}
		if _, err = nugetprotocol.InspectPackage(bytes.NewReader(body), int64(len(body))); err != nil {
			t.Fatalf("official package %s rejected by byte validator: %v", packageName, err)
		}
	}
	writeNuGetClientFile(t, c.work, "Consumer/Consumer.csproj", `<Project Sdk="Microsoft.NET.Sdk"><PropertyGroup><OutputType>Exe</OutputType><TargetFramework>net10.0</TargetFramework><NuGetAudit>false</NuGetAudit></PropertyGroup><ItemGroup><PackageReference Include="Hosted.App" Version="1.0.0"/></ItemGroup></Project>`)
	writeNuGetClientFile(t, c.work, "Consumer/Program.cs", `System.Console.WriteLine(HostedApp.Message.Value);`)
	push := func(id string) {
		c.succeeds(t, "push", "push-"+id, false, "nuget", "push", "/work/feed/Hosted."+id+".1.0.0.nupkg", "--source", "gateway", "--api-key", c.admin, "--configfile", "/work/push.config", "--no-symbols", "--allow-insecure-connections")
	}
	push("App")
	args := []string{"restore", "/work/Consumer/Consumer.csproj", "--configfile", "/work/read.config", "--no-http-cache", "--force", "-p:NuGetAudit=false", "--disable-build-servers"}
	if out, err := c.command(t, "missing", "restore-missing-transitive", false, args...); err == nil || !strings.Contains(out, "NU1101") || !strings.Contains(out, "Hosted.Middle") {
		t.Fatalf("missing dependency was not surfaced: %v %s", err, out)
	}
	push("Middle")
	push("Leaf")
	push("App")
	verify := func(phase string) {
		c.succeeds(t, phase, "target-only-cold-restore", false, args...)
		assets, err := os.ReadFile(filepath.Join(c.work, "Consumer/obj/project.assets.json"))
		if err != nil {
			t.Fatal(err)
		}
		var document struct {
			Libraries map[string]json.RawMessage `json:"libraries"`
			Project   struct {
				Restore struct {
					Sources map[string]json.RawMessage `json:"sources"`
				} `json:"restore"`
			} `json:"project"`
		}
		if json.Unmarshal(assets, &document) != nil || len(document.Project.Restore.Sources) != 1 || document.Project.Restore.Sources[source] == nil {
			t.Fatalf("restore sources escaped target: %s", assets)
		}
		for _, id := range []string{"App", "Middle", "Leaf"} {
			if document.Libraries["Hosted."+id+"/1.0.0"] == nil {
				t.Fatalf("transitive graph missing %s", id)
			}
			original, e := os.ReadFile(filepath.Join(c.work, "feed", "Hosted."+id+".1.0.0.nupkg"))
			if e != nil {
				t.Fatal(e)
			}
			cached, e := os.ReadFile(filepath.Join(c.work, "packages-"+phase, "hosted."+strings.ToLower(id), "1.0.0", "hosted."+strings.ToLower(id)+".1.0.0.nupkg"))
			if e != nil || !bytes.Equal(cached, original) {
				t.Fatalf("official restored package bytes differ %s: %v", id, e)
			}
		}
		if out := c.succeeds(t, phase, "execute-transitive-result", true, "run", "--project", "/work/Consumer/Consumer.csproj", "--no-restore", "--disable-build-servers"); !strings.Contains(out, "leaf:middle:app") {
			t.Fatalf("transitive execution=%s", out)
		}
	}
	verify("cold")
	// Restart the real process and both owned persistence services, then use
	// completely new global package, HTTP, plugin and SDK-home directories.
	stop()
	restartNuGetOwnedService(t, c.project, "postgres")
	restartNuGetOwnedService(t, c.project, "rustfs")
	stop = startNuGetGatewayBinary(t, c, bucket, address)
	verify("restarted")
	stop()
	c.succeeds(t, "restarted", "offline-restore-with-stopped-target", true, "restore", "/work/Consumer/Consumer.csproj", "--configfile", "/work/offline.config", "--no-http-cache", "--force", "-p:NuGetAudit=false", "--disable-build-servers")
	if out := c.succeeds(t, "restarted", "offline-execute-with-stopped-target", true, "run", "--project", "/work/Consumer/Consumer.csproj", "--no-restore", "--disable-build-servers"); !strings.Contains(out, "leaf:middle:app") {
		t.Fatalf("offline execution=%s", out)
	}
	publications, err := store.ListNuGetPublications(t.Context(), repo.ID, "", 10, "")
	if err != nil || len(publications) != 3 {
		t.Fatalf("durable publications=%+v %v", publications, err)
	}
	checks := map[string]string{}
	for _, p := range publications {
		id := map[string]string{"hosted.app": "App", "hosted.middle": "Middle", "hosted.leaf": "Leaf"}[p.PackageID]
		if id == "" {
			t.Fatalf("unexpected publication %s", p.PackageID)
		}
		body, err := os.ReadFile(filepath.Join(c.work, "feed", "Hosted."+id+".1.0.0.nupkg"))
		if err != nil || p.Digest != nugetClientDigest(body) {
			t.Fatalf("publication digest %s differs: %v", p.PackageID, err)
		}
		checks[p.PackageID+"@"+p.Version] = p.Digest
	}
	encoded, _ := json.MarshalIndent(map[string]any{"repositoryId": repo.ID, "targetSource": source, "packageDigests": checks, "client": "10.0.100", "targetOnly": true, "transitivePackages": 3, "coldRestores": 2, "restarted": []string{"gateway", "postgres", "rustfs"}, "offlineRestoreWithStoppedTarget": true, "offlineExecution": "leaf:middle:app", "missingDependencyRejected": true, "sameContentPushReplay": true}, "", "  ")
	if err = os.WriteFile(filepath.Join(c.evidence, "client-acceptance.json"), encoded, 0600); err != nil {
		t.Fatal(err)
	}
}

func nugetClientDigest(body []byte) string {
	sum := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(sum[:])
}
