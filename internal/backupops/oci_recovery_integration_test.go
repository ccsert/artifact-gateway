package backupops

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/artifact-gateway/artifact-gateway/internal/backupmanifest"
	"github.com/google/uuid"
)

// Published by main CI run 37097080294 for the exact 80fc113 base. Tests pin
// the multi-platform digest, version and revision; they never resolve a tag.
// Update this fixture only after a reviewed compatible build is published.
const ociFixtureDigest = "sha256:bbd62298c63520b58e82b858479fb77c0bf4fce61a1c3eaaf11b9fb01e0d0e18"
const ociFixtureReference = "ghcr.io/ccsert/artifact-gateway@" + ociFixtureDigest
const ociFixtureVersion = "0.5.0-main.80fc113e1e8a"
const ociFixtureRevision = "80fc113e1e8a908388ed8dc7af5e87424bcb9768"

func integrationOCIRelease(t *testing.T, ctx context.Context, d Docker, release Release) Release {
	t.Helper()
	// Anonymous public-image pull through the already validated local socket.
	// An empty temporary config prevents Docker from reading host credentials.
	// The public image is shared cache, not a fixture-owned cleanup resource.
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
	platform := release.Identity.Artifact.Platform
	pull := exec.CommandContext(ctx, "docker", "--config", t.TempDir(), "--host", host, "pull", "--platform", platform, ociFixtureReference)
	pull.Env = sanitizedEnvironment(nil)
	if _, err = pull.Output(); err != nil {
		t.Fatal("anonymous pinned public OCI fixture unavailable; no daemon settings or credentials changed")
	}
	release.ImageReference = ociFixtureReference
	release.Identity = backupmanifest.GatewayIdentity{Version: ociFixtureVersion, Revision: ociFixtureRevision,
		Artifact: &backupmanifest.SoftwareArtifact{Kind: "oci-image", SHA256: ociFixtureDigest, Platform: platform}}
	release, err = d.ObserveRelease(ctx, release)
	if err != nil {
		t.Fatal("actual public OCI fixture identity rejected")
	}
	return release
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
