package backupops

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/backupmanifest"
	"github.com/google/uuid"
)

const registryFixtureImage = "registry:2.8.3@sha256:a3d8aaa63ed8681a604f1dea0aa03f100d5895b6a58ace528858a7b332415373"
const registryFixtureLabel = "artifact-gateway.recovery-test-owner"

type registryFixture struct {
	docker                                               Docker
	host, config, helpers, marker, owner, name, endpoint string
	containerID, imageID, imageTag                       string
}

func integrationOCIRelease(t *testing.T, ctx context.Context, d Docker, release Release) Release {
	t.Helper()
	// Build the checkout binary into an actual OCI image and obtain its real
	// repository digest from a disposable registry on the daemon's loopback.
	// This also works with Docker Desktop's VM; host-published ports do not.
	data, err := d.output(ctx, "context", "inspect", d.Context)
	var contexts []struct {
		Endpoints map[string]struct{ Host string }
	}
	if err != nil || json.Unmarshal(data, &contexts) != nil || len(contexts) != 1 {
		t.Fatal("local OCI fixture context unavailable")
	}
	host := contexts[0].Endpoints["docker"].Host
	if !strings.HasPrefix(host, "unix:///") {
		t.Fatal("remote OCI fixture daemon refused")
	}
	f := &registryFixture{docker: d, host: host, config: t.TempDir(), helpers: t.TempDir(), owner: uuid.NewString()}
	f.name = "ag-oci-fixture-" + strings.ReplaceAll(f.owner, "-", "")
	f.endpoint = fmt.Sprintf("127.0.0.1:%d", 49152+uuid.New().ID()%16384)
	f.imageTag = f.endpoint + "/" + f.name + ":fixture"
	f.marker = filepath.Join(f.helpers, "invoked")
	config, err := json.Marshal(map[string]any{"auths": map[string]any{f.endpoint: map[string]any{}, "gcr.io": map[string]any{}, "https://index.docker.io/v1/": map[string]any{}}})
	if err != nil || os.WriteFile(filepath.Join(f.config, "config.json"), config, 0600) != nil {
		t.Fatal("anonymous OCI fixture configuration unavailable")
	}
	// Docker auto-discovers a native credential helper for a wholly empty
	// config. Trap every platform's default helper to prove none is consulted.
	script := []byte("#!/bin/sh\n: > \"$AG_OCI_FIXTURE_HELPER_MARKER\"\nprintf '%s\\n' 'synthetic helper refuses credential access' >&2\nexit 1\n")
	for _, name := range []string{"pass", "docker-credential-pass", "docker-credential-secretservice", "docker-credential-osxkeychain", "docker-credential-wincred"} {
		if os.WriteFile(filepath.Join(f.helpers, name), script, 0700) != nil {
			t.Fatal("OCI fixture helper trap unavailable")
		}
	}
	for _, entry := range []struct{ kind, name string }{{"container", f.name}, {"image", f.imageTag}} {
		if _, err := d.output(ctx, entry.kind, "inspect", entry.name); err == nil {
			t.Fatal("OCI fixture resource already exists")
		}
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		f.cleanup(t, cleanupCtx)
	})
	_, createErr := f.output(ctx, "create", "--name", f.name, "--label", registryFixtureLabel+"="+f.owner,
		"--network", "host", "--read-only", "--cap-drop", "ALL", "--security-opt", "no-new-privileges:true",
		"--tmpfs", "/var/lib/registry:rw,noexec,nosuid,size=256m", "--env", "REGISTRY_HTTP_ADDR="+f.endpoint, registryFixtureImage)
	// Register observed identity even if a create/build response was lost.
	if c, ok := f.inspection(ctx, "container", f.name); ok && c.Config.Labels[registryFixtureLabel] == f.owner {
		f.containerID = c.ID
	}
	if createErr != nil || f.containerID == "" {
		t.Fatal("owned OCI fixture registry unavailable")
	}
	if _, err := f.output(ctx, "start", f.containerID); err != nil {
		t.Fatal("owned OCI fixture registry startup failed")
	}
	deadline := time.Now().Add(20 * time.Second)
	for {
		logs, err := f.output(ctx, "logs", f.containerID)
		if err == nil && strings.Contains(string(logs), "listening on "+f.endpoint) {
			break
		}
		if ctx.Err() != nil || time.Now().After(deadline) {
			t.Fatal("owned OCI registry did not bind daemon loopback")
		}
		select {
		case <-ctx.Done():
			t.Fatal("owned OCI registry readiness cancelled")
		case <-time.After(100 * time.Millisecond):
		}
	}
	dir := t.TempDir()
	binary, err := os.ReadFile(filepath.Join(release.Directory, "gateway"))
	if err != nil || os.WriteFile(filepath.Join(dir, "gateway"), binary, 0555) != nil {
		t.Fatal("OCI fixture binary staging failed")
	}
	dockerfile := fmt.Sprintf("FROM %s\nLABEL %s=%q org.opencontainers.image.version=%q org.opencontainers.image.revision=%q\nCOPY gateway /gateway\nENTRYPOINT [\"/gateway\"]\n", binaryRuntimeImage, registryFixtureLabel, f.owner, release.Identity.Version, release.Identity.Revision)
	if os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte(dockerfile), 0600) != nil {
		t.Fatal("OCI fixture Dockerfile staging failed")
	}
	_, buildErr := f.output(ctx, "build", "--tag", f.imageTag, dir)
	if image, ok := f.inspection(ctx, "image", f.imageTag); ok && image.Config.Labels[registryFixtureLabel] == f.owner {
		f.imageID = image.ID
	}
	if buildErr != nil || f.imageID == "" {
		t.Fatal("owned OCI fixture image build failed")
	}
	if _, err := f.output(ctx, "push", f.imageTag); err != nil {
		t.Fatal("owned OCI fixture anonymous push failed")
	}
	image, ok := f.inspection(ctx, "image", f.imageID)
	if !ok || len(image.RepoDigests) != 1 || !strings.HasPrefix(image.RepoDigests[0], f.endpoint+"/"+f.name+"@sha256:") {
		t.Fatal("owned OCI fixture actual repository digest unavailable")
	}
	release.ImageReference = image.RepoDigests[0]
	_, digest, _ := strings.Cut(release.ImageReference, "@")
	release.Identity.Artifact = &backupmanifest.SoftwareArtifact{Kind: "oci-image", SHA256: digest, Platform: release.Identity.Artifact.Platform}
	release, err = d.ObserveRelease(ctx, release)
	if err != nil {
		t.Fatal("actual owned OCI fixture identity rejected")
	}
	return release
}

func (f *registryFixture) output(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "docker", append([]string{"--config", f.config, "--host", f.host}, args...)...)
	cmd.Env = sanitizedEnvironment([]string{"PATH=" + f.helpers + string(os.PathListSeparator) + os.Getenv("PATH"), "AG_OCI_FIXTURE_HELPER_MARKER=" + f.marker})
	data, err := cmd.CombinedOutput()
	if _, markerErr := os.Stat(f.marker); !os.IsNotExist(markerErr) {
		return nil, fmt.Errorf("OCI fixture credential-helper isolation failed")
	}
	if err != nil || len(data) > 8<<20 {
		return nil, fmt.Errorf("owned OCI fixture Docker operation failed")
	}
	return data, nil
}

type registryFixtureInspection struct {
	ID, Name              string
	RepoDigests, RepoTags []string
	Config                struct {
		Image  string
		Env    []string
		Labels map[string]string
	}
	HostConfig struct {
		NetworkMode string
		Tmpfs       map[string]string
	}
	Mounts []json.RawMessage
}

func (f *registryFixture) inspection(ctx context.Context, kind, id string) (registryFixtureInspection, bool) {
	data, err := f.docker.output(ctx, kind, "inspect", id)
	var values []registryFixtureInspection
	if err != nil || json.Unmarshal(data, &values) != nil || len(values) != 1 {
		return registryFixtureInspection{}, false
	}
	return values[0], true
}

func (f *registryFixture) cleanup(t *testing.T, ctx context.Context) {
	t.Helper()
	// Only exact captured IDs with matching random names, labels and bindings
	// are removed. Registry data is tmpfs; no shared network/volume is adopted.
	// A timed-out create/build can lose its response and cancel its receipt
	// inspection. Reacquire only missing receipts with this independent cleanup
	// context, then apply the same full identity checks below before removal.
	if f.imageID == "" {
		if image, ok := f.inspection(ctx, "image", f.imageTag); ok && image.Config.Labels[registryFixtureLabel] == f.owner {
			f.imageID = image.ID
		}
	}
	if f.containerID == "" {
		if c, ok := f.inspection(ctx, "container", f.name); ok && c.Config.Labels[registryFixtureLabel] == f.owner {
			f.containerID = c.ID
		}
	}
	if f.imageID != "" {
		image, ok := f.inspection(ctx, "image", f.imageID)
		if !ok || image.ID != f.imageID || image.Config.Labels[registryFixtureLabel] != f.owner || len(image.RepoTags) != 1 || image.RepoTags[0] != f.imageTag {
			t.Error("OCI fixture image ownership changed; retained")
		} else if _, err := f.docker.output(ctx, "image", "rm", f.imageID); err != nil {
			t.Error("owned OCI fixture image cleanup failed")
		}
	}
	if f.containerID != "" {
		c, ok := f.inspection(ctx, "container", f.containerID)
		bound := false
		for _, e := range c.Config.Env {
			if e == "REGISTRY_HTTP_ADDR="+f.endpoint {
				bound = true
			}
		}
		if !ok || c.ID != f.containerID || c.Name != "/"+f.name || c.Config.Image != registryFixtureImage || c.Config.Labels[registryFixtureLabel] != f.owner || c.HostConfig.NetworkMode != "host" || len(c.HostConfig.Tmpfs) != 1 || c.HostConfig.Tmpfs["/var/lib/registry"] != "rw,noexec,nosuid,size=256m" || len(c.Mounts) != 0 || !bound {
			t.Error("OCI fixture registry ownership/binding changed; retained")
		} else if _, err := f.docker.output(ctx, "container", "rm", "--force", f.containerID); err != nil {
			t.Error("owned OCI fixture registry cleanup failed")
		}
	}
}

func fixtureContainerInspection(t *testing.T, ctx context.Context, d Docker, id string) struct {
	ID     string
	Config struct{ Image string }
	Mounts []json.RawMessage
} {
	t.Helper()
	var values []struct {
		ID     string
		Config struct{ Image string }
		Mounts []json.RawMessage
	}
	data, err := d.output(ctx, "container", "inspect", id)
	if err != nil || json.Unmarshal(data, &values) != nil || len(values) != 1 {
		t.Fatal("OCI fixture container inspection failed")
	}
	return values[0]
}

func fixtureBackupCommand(t *testing.T, ctx context.Context, action string, spec any, bundle string) (TransferReport, int, *OwnedTarget) {
	t.Helper()
	data, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "private-spec.json")
	if os.WriteFile(path, data, 0600) != nil {
		t.Fatal("private fixture spec unavailable")
	}
	var target *OwnedTarget
	var stageRoot string
	if action == "restore" {
		// Only this private fixture root may authorize later host-stage cleanup.
		stageRoot = t.TempDir()
		t.Setenv("TMPDIR", stageRoot)
		candidate, ok := spec.(TargetSpec)
		if !ok {
			t.Fatal("fixture restore requires an explicit target spec")
		}
		// Never adopt an existing project. Only a new random fixture project
		// can be captured after this public invocation.
		for _, entry := range []struct{ kind, suffix string }{{"network", "network"}, {"volume", "pgdata"}, {"volume", "objects"}, {"container", "postgres"}, {"container", "rustfs"}, {"container", "gateway"}} {
			if _, err := candidate.Docker.output(ctx, entry.kind, "inspect", candidate.Project+"-"+entry.suffix); err == nil {
				t.Fatal("public restore fixture project already exists")
			}
		}
	}
	var out, diagnostics bytes.Buffer
	code := RunCLI(ctx, []string{action, "--spec", path, "--bundle", bundle}, &out, &diagnostics)
	// Capture/register exact ownership receipts before any report assertion,
	// including an unexpectedly successful negative restore. A malformed or
	// private report must fail the test without leaking a successful target.
	if action == "restore" {
		candidate := spec.(TargetSpec)
		if _, err := candidate.Docker.output(ctx, "network", "inspect", candidate.Project+"-network"); err == nil {
			target = captureCreatedTarget(t, ctx, candidate, stageRoot)
		}
	}
	var result TransferReport
	if diagnostics.Len() != 0 || json.Unmarshal(out.Bytes(), &result) != nil {
		t.Fatalf("public backup command did not return one safe report: exit=%d", code)
	}
	privateValues := []string{path, bundle, "synthetic/orphan-before-backup"}
	switch value := spec.(type) {
	case SourceSpec:
		privateValues = append(privateValues, value.Release.Directory, value.Postgres.DSN, value.S3.AccessKey, value.S3.SecretKey)
	case TargetSpec:
		privateValues = append(privateValues, value.Release.Directory, value.PostgresPassword, value.AccessKey, value.SecretKey, value.RPCSecret, value.AdminToken, value.ResolverToken, value.ReaderToken, value.DeniedToken)
	}
	for _, private := range privateValues {
		if private == "" {
			continue
		}
		if strings.Contains(out.String(), private) {
			t.Fatal("private coordinate appeared in public backup report")
		}
	}
	return result, code, target
}

func verifyFixtureGatewayImage(t *testing.T, ctx context.Context, target *OwnedTarget, release Release) {
	t.Helper()
	c := fixtureContainerInspection(t, ctx, target.spec.Docker, target.resourceID("container", target.spec.Project+"-gateway"))
	if c.ID == "" || c.Config.Image != release.ImageReference || len(c.Mounts) != 0 {
		t.Fatal("OCI Gateway did not start from the approved digest without binary mounts")
	}
	var actual []struct{ Image string }
	data, err := target.spec.Docker.output(ctx, "container", "inspect", c.ID)
	if err != nil || json.Unmarshal(data, &actual) != nil || len(actual) != 1 || actual[0].Image != release.imageID {
		t.Fatal("OCI Gateway actual image ID differs from observed release")
	}
	port, err := target.port(ctx, "gateway", "8080/tcp")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := fixtureHTTP(t, ctx, "http://127.0.0.1:"+port, http.MethodGet, "/api/v2/diagnostics", target.spec.AdminToken, nil, 200, "")
	var diagnostics struct {
		Build struct{ Version, Revision string }
	}
	if json.Unmarshal(body, &diagnostics) != nil || diagnostics.Build.Version != release.Identity.Version || diagnostics.Build.Revision != release.Identity.Revision {
		t.Fatal("running OCI Gateway build identity differs from the backup")
	}
}

func verifyOCIIdentityRejections(t *testing.T, ctx context.Context, d Docker, bundle string, target TargetSpec, source SourceSpec) {
	t.Helper()
	for _, scenario := range []string{"digest", "version", "revision", "platform", "mutable tag", "image ID", "unavailable image"} {
		t.Run("OCI rejects "+scenario, func(t *testing.T) {
			wrong := target.Release
			artifact := *wrong.Identity.Artifact
			wrong.Identity.Artifact = &artifact
			switch scenario {
			case "digest":
				artifact.SHA256 = "sha256:" + strings.Repeat("0", 64)
			case "version":
				wrong.Identity.Version = "unapproved-version"
			case "revision":
				wrong.Identity.Revision = strings.Repeat("b", 40)
			case "platform":
				artifact.Platform = "linux/amd64"
				if target.Release.Identity.Artifact.Platform == artifact.Platform {
					artifact.Platform = "linux/arm64"
				}
			case "mutable tag":
				wrong.ImageReference = strings.Split(wrong.ImageReference, "@")[0] + ":fixture"
			case "image ID":
				wrong.ImageReference = wrong.imageID
			case "unavailable image":
				wrong.ImageReference = "127.0.0.1:1/unavailable@" + artifact.SHA256
			}
			candidate := target
			candidate.Project = "ag-restore-" + uuid.NewString()[:18]
			candidate.Release = wrong
			r, code, _ := fixtureBackupCommand(t, ctx, "restore", candidate, bundle)
			if code != 1 || r.Status != "failed" || r.Reason != "release_schema_mismatch" {
				t.Fatalf("OCI identity accepted or failed after target creation: %+v exit=%d", r, code)
			}
			for _, entry := range []struct{ kind, suffix string }{{"container", "postgres"}, {"container", "rustfs"}, {"container", "gateway"}, {"volume", "pgdata"}, {"volume", "objects"}, {"network", "network"}} {
				if _, err := d.output(ctx, entry.kind, "inspect", candidate.Project+"-"+entry.suffix); err == nil {
					t.Fatal("rejected OCI identity created target resource")
				}
			}
			src := source
			src.Release = wrong
			output := filepath.Join(t.TempDir(), "rejected-export")
			r, code, _ = fixtureBackupCommand(t, ctx, "export", src, output)
			if code != 1 || r.Status != "failed" || r.Reason != "source_spec_or_release_invalid" {
				t.Fatalf("OCI source identity accepted: %+v exit=%d", r, code)
			}
			if _, err := os.Lstat(output); !os.IsNotExist(err) {
				t.Fatal("rejected OCI source wrote backup output")
			}
		})
	}
}
