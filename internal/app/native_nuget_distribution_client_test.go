//go:build integration && nugetclient

package app

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	dist "github.com/ccsert/artifact-gateway/internal/nugetdistribution"
	"github.com/ccsert/artifact-gateway/internal/objectstore"
	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

func TestNativeNuGetRealDotnetHostedReplicationAndSourceIndependentRestore(t *testing.T) {
	for _, key := range []string{"NUGET_GATEWAY_BIN", "NUGET_DOTNET_IMAGE", "NUGET_COMPOSE_PROJECT", "NUGET_EVIDENCE_DIR", "TEST_DATABASE_URL", "TEST_RUSTFS_ENDPOINT"} {
		if os.Getenv(key) == "" {
			t.Skip("owned native-nuget-e2e fixture is required")
		}
	}
	c := nugetOfficialClient{work: t.TempDir(), image: os.Getenv("NUGET_DOTNET_IMAGE"), project: os.Getenv("NUGET_COMPOSE_PROJECT"), evidence: filepath.Join(os.Getenv("NUGET_EVIDENCE_DIR"), "hosted-replication"), admin: "replication-admin-" + uuid.NewString(), reader: "replication-reader-" + uuid.NewString()}
	if err := os.MkdirAll(c.evidence, 0700); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(c.succeeds(t, "tools", "sdk-version", true, "--version")); got != "10.0.100" {
		t.Fatalf("unapproved SDK %q", got)
	}
	allocate := func() (string, string) {
		listener, err := net.Listen("tcp", "0.0.0.0:0")
		if err != nil {
			t.Fatal(err)
		}
		address := listener.Addr().String()
		_ = listener.Close()
		_, port, _ := net.SplitHostPort(address)
		return address, port
	}
	address, port := allocate()
	workerAddress, _ := allocate()
	bucket := "nuget-replication-" + uuid.NewString()
	stopSource := startNuGetGatewayBinaryWithRoles(t, c, bucket, address, "api")
	host := "http://127.0.0.1:" + port
	var source, target struct{ ID, Name string }
	nugetClientHTTP(t, host, c.admin, "POST", "/api/v2/previews/nuget/repositories", `{"name":"replication-source"}`, 201, &source)
	nugetClientHTTP(t, host, c.admin, "POST", "/api/v2/previews/nuget/repositories", `{"name":"replication-target"}`, 201, &target)
	sourceURL := "http://host.docker.internal:" + port + "/nuget/replication-source/v3/index.json"
	targetURL := "http://host.docker.internal:" + port + "/nuget/replication-target/v3/index.json"
	config := func(url, secret string) string {
		return `<?xml version="1.0"?><configuration><packageSources><clear/><add key="gateway" value="` + xmlText(url) + `" allowInsecureConnections="true"/></packageSources><packageSourceCredentials><gateway><add key="Username" value="token"/><add key="ClearTextPassword" value="` + xmlText(secret) + `"/><add key="ValidAuthenticationTypes" value="basic"/></gateway></packageSourceCredentials></configuration>`
	}
	writeNuGetClientFile(t, c.work, "push.config", config(sourceURL, c.admin))
	writeNuGetClientFile(t, c.work, "read.config", config(targetURL, c.reader))
	writeNuGetClientFile(t, c.work, "pack.config", `<configuration><packageSources><clear/><add key="owned" value="/work/feed"/></packageSources></configuration>`)
	if err := os.MkdirAll(filepath.Join(c.work, "feed"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, v := range []struct{ id, dep, expression string }{{"Leaf", "", `"leaf"`}, {"Middle", "Leaf", `ReplicatedLeaf.Message.Value+":middle"`}, {"App", "Middle", `ReplicatedMiddle.Message.Value+":app"`}} {
		deps := ""
		if v.dep != "" {
			deps = `<ItemGroup><PackageReference Include="Replicated.` + v.dep + `" Version="1.0.0"/></ItemGroup>`
		}
		writeNuGetClientFile(t, c.work, v.id+"/"+v.id+".csproj", `<Project Sdk="Microsoft.NET.Sdk"><PropertyGroup><TargetFramework>net10.0</TargetFramework><PackageId>Replicated.`+v.id+`</PackageId><Version>1.0.0</Version><Authors>Owned replication fixture</Authors><Description>Durable independent target acceptance</Description><NuGetAudit>false</NuGetAudit></PropertyGroup>`+deps+`</Project>`)
		writeNuGetClientFile(t, c.work, v.id+"/Message.cs", `namespace Replicated`+v.id+`;public static class Message{public static string Value=>`+v.expression+`;}`)
		c.succeeds(t, "pack", "pack-"+v.id, true, "pack", "/work/"+v.id+"/"+v.id+".csproj", "--output", "/work/feed", "-p:RestoreConfigFile=/work/pack.config", "--disable-build-servers", "-p:NuGetAudit=false")
		c.succeeds(t, "push", "push-"+v.id, false, "nuget", "push", "/work/feed/Replicated."+v.id+".1.0.0.nupkg", "--source", "gateway", "--api-key", c.admin, "--configfile", "/work/push.config", "--no-symbols", "--allow-insecure-connections")
	}
	store, err := repository.NewPostgresStore(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	objects, err := objectstore.NewRustFSStore(os.Getenv("TEST_RUSTFS_ENDPOINT"), os.Getenv("TEST_RUSTFS_ACCESS_KEY"), os.Getenv("TEST_RUSTFS_SECRET_KEY"), bucket)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.ReplaceRepositoryGrants(t.Context(), target.ID, []repository.RepositoryGrant{{Principal: "nuget-gate-reader", Scopes: []string{"repositories:read"}, ResourcePrefix: "replicated."}}, "1"); err != nil {
		t.Fatal(err)
	}
	app, err := store.GetNuGetPublication(t.Context(), source.ID, "replicated.app", "1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.TransitionNuGetGovernance(t.Context(), repository.NuGetGovernanceChange{RepositoryID: source.ID, PackageID: app.PackageID, Version: app.Version, ExpectedDigest: app.Digest, ExpectedVersion: app.Governance.Version, Action: "unlist", Actor: "nuget-gate-operator", Reason: "replicate exact unlisted graph"}); err != nil {
		t.Fatal(err)
	}
	plan := dist.Plan{SourceRepositoryID: source.ID, TargetRepositoryID: target.ID}
	for _, id := range []string{"replicated.app", "replicated.middle", "replicated.leaf"} {
		p, e := store.GetNuGetPublication(t.Context(), source.ID, id, "1.0.0")
		if e != nil {
			t.Fatal(e)
		}
		plan.Items = append(plan.Items, dist.Identity{PackageID: id, Version: p.Version, Digest: p.Digest, Size: p.Size, SourceGovernanceVersion: p.Governance.Version})
	}
	encoded, _ := json.Marshal(plan)
	var job dist.Job
	nugetClientHTTP(t, host, c.admin, "POST", "/api/v2/previews/nuget/replication-jobs", string(encoded), 202, &job)
	time.Sleep(150 * time.Millisecond)
	nugetClientHTTP(t, host, c.admin, "GET", "/api/v2/previews/nuget/replication-jobs/"+job.ID, "", 200, &job)
	if job.State != "queued" {
		t.Fatalf("API role executed job %+v", job)
	}
	stopWorker := startNuGetGatewayBinaryWithRoles(t, c, bucket, workerAddress, "worker")
	_, workerPort, _ := net.SplitHostPort(workerAddress)
	req, _ := http.NewRequest("POST", "http://127.0.0.1:"+workerPort+"/api/v2/previews/nuget/replication-jobs", strings.NewReader(string(encoded)))
	req.Header.Set("Authorization", "Bearer "+c.admin)
	response, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != 404 {
		t.Fatalf("worker exposes management API=%d", response.StatusCode)
	}
	deadline := time.Now().Add(40 * time.Second)
	for job.State == "queued" || job.State == "running" {
		if time.Now().After(deadline) {
			t.Fatalf("worker did not complete %+v", job)
		}
		time.Sleep(100 * time.Millisecond)
		nugetClientHTTP(t, host, c.admin, "GET", "/api/v2/previews/nuget/replication-jobs/"+job.ID, "", 200, &job)
	}
	if job.State != "succeeded" {
		t.Fatalf("replication failed=%+v", job)
	}
	ownedTargets := map[string]string{}
	packageHashes := map[string]string{}
	for _, item := range job.Items {
		src, e := store.GetNuGetPublication(t.Context(), source.ID, item.PackageID, item.Version)
		if e != nil {
			t.Fatal(e)
		}
		dst, e := store.GetNuGetPublication(t.Context(), target.ID, item.PackageID, item.Version)
		if e != nil || src.ObjectKey == dst.ObjectKey || src.SessionID == dst.SessionID || dst.Governance.Listed != (item.PackageID != "replicated.app") {
			t.Fatalf("target identity/governance=%+v %v", dst, e)
		}
		ownedTargets[item.PackageID] = dst.SessionID
		packageHashes[item.PackageID] = dst.Digest
		if e = objects.Delete(t.Context(), src.ObjectKey); e != nil {
			t.Fatal(e)
		}
	}
	stopWorker()
	stopSource()
	restartNuGetOwnedService(t, c.project, "postgres")
	restartNuGetOwnedService(t, c.project, "rustfs")
	_ = startNuGetGatewayBinaryWithRoles(t, c, bucket, address, "api")
	writeNuGetClientFile(t, c.work, "Consumer/Consumer.csproj", `<Project Sdk="Microsoft.NET.Sdk"><PropertyGroup><OutputType>Exe</OutputType><TargetFramework>net10.0</TargetFramework><NuGetAudit>false</NuGetAudit></PropertyGroup><ItemGroup><PackageReference Include="Replicated.App" Version="1.0.0"/></ItemGroup></Project>`)
	writeNuGetClientFile(t, c.work, "Consumer/Program.cs", `System.Console.WriteLine(ReplicatedApp.Message.Value);`)
	for _, phase := range []string{"target-cold", "target-cold-repeat"} {
		for _, dir := range []string{"packages-" + phase, "http-" + phase, "plugins-" + phase} {
			if entries, e := os.ReadDir(filepath.Join(c.work, dir)); e == nil && len(entries) != 0 {
				t.Fatalf("client cache was not empty %s", dir)
			}
		}
		c.succeeds(t, phase, "source-independent-target-only-restore", false, "restore", "/work/Consumer/Consumer.csproj", "--configfile", "/work/read.config", "--no-http-cache", "--force", "-p:NuGetAudit=false", "--disable-build-servers")
		assets, e := os.ReadFile(filepath.Join(c.work, "Consumer/obj/project.assets.json"))
		if e != nil {
			t.Fatal(e)
		}
		var doc struct {
			Libraries map[string]json.RawMessage
			Project   struct {
				Restore struct{ Sources map[string]json.RawMessage }
			}
		}
		if json.Unmarshal(assets, &doc) != nil || len(doc.Project.Restore.Sources) != 1 || doc.Project.Restore.Sources[targetURL] == nil {
			t.Fatalf("escaped target-only restore: %s", assets)
		}
		for _, id := range []string{"App", "Middle", "Leaf"} {
			if doc.Libraries["Replicated."+id+"/1.0.0"] == nil {
				t.Fatalf("transitive dependency missing %s", id)
			}
			original, e := os.ReadFile(filepath.Join(c.work, "feed", "Replicated."+id+".1.0.0.nupkg"))
			if e != nil {
				t.Fatal(e)
			}
			restored, e := os.ReadFile(filepath.Join(c.work, "packages-"+phase, "replicated."+strings.ToLower(id), "1.0.0", "replicated."+strings.ToLower(id)+".1.0.0.nupkg"))
			sum := sha256.Sum256(restored)
			if e != nil || !bytes.Equal(original, restored) || packageHashes["replicated."+strings.ToLower(id)] != "sha256:"+hex.EncodeToString(sum[:]) {
				t.Fatalf("restored target bytes differ %s %v", id, e)
			}
		}
		if out := c.succeeds(t, phase, "execute-transitive-assembly", true, "run", "--project", "/work/Consumer/Consumer.csproj", "--no-restore", "--disable-build-servers"); !strings.Contains(out, "leaf:middle:app") {
			t.Fatal(out)
		}
	}
	var discovery struct{ TotalHits int }
	nugetClientHTTP(t, host, c.admin, "GET", "/nuget/replication-target/v3/query?q=Replicated.App", "", 200, &discovery)
	if discovery.TotalHits != 0 {
		t.Fatal("replication relisted an unlisted package")
	}
	evidence := map[string]any{"sdk": "10.0.100", "job": job, "sourceProcessStopped": true, "workerProcessStopped": true, "sourceObjectBytesRemoved": true, "postgresAndRustFSRestarted": true, "apiRoleDoesNotExecute": true, "workerRoleDoesNotExposeManagement": true, "targetOnlySource": targetURL, "phases": []string{"target-cold", "target-cold-repeat"}, "targetSessionIds": ownedTargets, "packageSHA256": packageHashes, "transitiveResult": "leaf:middle:app"}
	data, _ := json.MarshalIndent(evidence, "", "  ")
	if err = os.WriteFile(filepath.Join(c.evidence, "acceptance.json"), append(data, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	fmt.Fprintln(os.Stdout, "NuGet durable replication: official target-only three-level restore passed after source bytes removal and PG/S3/API restart")
}
