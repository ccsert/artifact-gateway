package backupops

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/backupmanifest"
	"github.com/google/uuid"
)

const alertUpgradeBaseline = "ea60aea333b29bb60d4ac1b8e2b2a8720563726f"

// A source-built formal v0.5.0 baseline, not a published-registry image test.
// Rollback consumes the pre-upgrade snapshot with matching old software, never
// assumes an old binary can read the expanded candidate database.
func TestLocalAlertReleaseUpgrade(t *testing.T) {
	if os.Getenv("BACKUPOPS_DOCKER_TEST") != "1" {
		t.Skip("explicit BACKUPOPS_DOCKER_TEST and local context required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	d := Docker{Context: os.Getenv("BACKUPOPS_DOCKER_CONTEXT")}
	old := formalUpgradeRelease(t)
	candidate := integrationRelease(t)
	makeSpec := func(release Release) TargetSpec {
		return TargetSpec{Project: "ag-restore-" + uuid.NewString()[:18], Docker: d, Release: release, PostgresPassword: uuid.NewString(), AccessKey: "synthetic-test", SecretKey: uuid.NewString(), RPCSecret: uuid.NewString(), AdminToken: uuid.NewString(), ResolverToken: uuid.NewString(), RuntimeEnvironment: map[string]string{"GATEWAY_SETTINGS_ENCRYPTION_KEY": recoveryEmailKey}}
	}
	source, err := NewTarget(ctx, makeSpec(old))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, c := context.WithTimeout(context.Background(), 30*time.Second)
		defer c()
		if e := source.Cleanup(cleanup); e != nil {
			t.Error(e)
		}
	})
	runFixtureMigrations(t, ctx, source, old.Directory)
	baselineLedger, err := source.Ledger(ctx)
	if err != nil {
		t.Fatal(err)
	}
	oldMigrations := fixtureLedger(t, baselineLedger)
	if len(oldMigrations) != 136 {
		t.Fatal("formal v0.5.0 migration ledger missing")
	}
	url, err := source.StartGateway(ctx)
	if err != nil || waitGateway(ctx, url) != nil {
		t.Fatal("formal baseline startup failed")
	}
	var ids []string
	for i, name := range []string{"upgrade-first", "upgrade-second"} {
		data, _ := fixtureHTTP(t, ctx, url, "POST", "/api/v2/repositories", source.spec.AdminToken, []byte(`{"name":"`+name+`","format":"raw"}`), 201, "")
		var repo struct{ ID string }
		if json.Unmarshal(data, &repo) != nil || repo.ID == "" {
			t.Fatal("baseline repository missing")
		}
		ids = append(ids, repo.ID)
		fixtureHTTP(t, ctx, url, "PUT", "/raw/"+name+"/fixture.txt", source.spec.AdminToken, []byte([]string{"original-first", "original-second"}[i]), 201, "")
		fixtureHTTP(t, ctx, url, "PUT", "/api/v2/repositories/"+repo.ID+"/grants", source.spec.AdminToken, []byte(`[{"principal":"upgrade-reader","scopes":["repositories:read"]}]`), 200, "1")
	}
	group, _ := fixtureHTTP(t, ctx, url, "POST", "/api/v2/groups", source.spec.AdminToken, []byte(`{"name":"upgrade-group","format":"raw","members":[{"repositoryId":"`+ids[0]+`","position":0},{"repositoryId":"`+ids[1]+`","position":1}]}`), 201, "")
	var groupID struct{ ID string }
	if json.Unmarshal(group, &groupID) != nil || groupID.ID == "" {
		t.Fatal("baseline Group missing")
	}
	stopFixtureGateway(t, ctx, source)
	sourceObjects := fixtureObjectFingerprint(t, ctx, source.objects)
	s3port, err := source.port(ctx, "rustfs", "9000/tcp")
	if err != nil {
		t.Fatal(err)
	}
	preUpgrade := SourceSpec{BackupID: "synthetic-v050-upgrade", ScopeID: "synthetic-local", InventoryDeclaredComplete: true, Release: old, Docker: d, Postgres: source.database, S3: S3Settings{Endpoint: "http://127.0.0.1:" + s3port, Bucket: "gateway-cache", AccessKey: source.spec.AccessKey, SecretKey: source.spec.SecretKey}, Writers: []WriterSpec{{ID: "synthetic-api", Kind: "docker-container", Reference: source.resourceID("container", source.spec.Project+"-gateway")}}}
	bundle := filepath.Join(t.TempDir(), "pre-upgrade")
	exported, code, _ := fixtureBackupCommand(t, ctx, "export", preUpgrade, bundle)
	if code != 0 || exported.Status != "exported" || exported.VerifiedObjects != 2 {
		t.Fatalf("pre-upgrade export: %+v exit=%d", exported, code)
	}
	// All fixture writers are stopped. Exactly one real migration runner executes.
	runFixtureMigrations(t, ctx, source, candidate.Directory)
	forward, err := source.Ledger(ctx)
	if err != nil {
		t.Fatal(err)
	}
	newMigrations := fixtureLedger(t, forward)
	if len(newMigrations) != 144 {
		t.Fatalf("candidate ledger entries = %d, want 144", len(newMigrations))
	}
	for name, sum := range oldMigrations {
		if newMigrations[name] != sum {
			t.Fatal("applied baseline checksum changed")
		}
	}
	for _, name := range []string{"000136_email_test_delivery.sql", "000137_repository_quota_alert_rules.sql", "000138_repository_quota_alert_events.sql", "000139_quota_delivery_permission.sql", "000141_maven_snapshot_import.sql", "000142_maven_snapshot_takeover.sql", "000143_artifact_usage_paging.sql", "000144_oci_bearer_configuration.sql"} {
		if newMigrations[name] == "" {
			t.Fatalf("candidate migration %s missing", name)
		}
	}
	before, err := source.Metadata(ctx)
	if err != nil {
		t.Fatal(err)
	}
	runFixtureMigrations(t, ctx, source, candidate.Directory)
	after, err := source.Metadata(ctx)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("second migration run was not a complete metadata no-op")
	}
	url = replaceFixtureGateway(t, ctx, source, candidate)
	assertUpgradeCore(t, ctx, source, url, ids, groupID.ID, group)
	seed := seedRecoveryEmail(t, ctx, source, url, ids[0], ids[1])
	data, _ := fixtureHTTP(t, ctx, url, "GET", "/api/v2/repository-quota-alert-rules/"+seed.Rules[1].ID+"/events", source.spec.AdminToken, nil, 200, "")
	if !bytes.Contains(data, []byte(seed.Events[1][0].Snapshot.ID)) {
		t.Fatal("candidate new event API readback missing")
	}
	stopFixtureGateway(t, ctx, source)
	protected, err := source.Metadata(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// Correct data rollback is a new isolated target from the consistent old DB +
	// object snapshot, with old software/config/key. No down migration or overwrite.
	rollbackSpec := makeSpec(old)
	restored, code, rollback := fixtureBackupCommand(t, ctx, "restore", rollbackSpec, bundle)
	if code != 0 || rollback == nil || restored.Metadata != "verified" || restored.Objects != "verified" || restored.Software != "verified" {
		t.Fatalf("v0.5.0 snapshot rollback: %+v exit=%d", restored, code)
	}
	port, err := rollback.port(ctx, "gateway", "8080/tcp")
	if err != nil {
		t.Fatal(err)
	}
	assertUpgradeCore(t, ctx, rollback, "http://127.0.0.1:"+port, ids, groupID.ID, group)
	rollbackLedger, err := rollback.Ledger(ctx)
	if err != nil || !bytes.Equal(rollbackLedger, baselineLedger) {
		t.Fatal("rollback kept candidate schema")
	}
	stopFixtureGateway(t, ctx, rollback)
	runFixtureMigrations(t, ctx, rollback, candidate.Directory)
	url = replaceFixtureGateway(t, ctx, rollback, candidate)
	assertUpgradeCore(t, ctx, rollback, url, ids, groupID.ID, group)
	data, _ = fixtureHTTP(t, ctx, url, "GET", "/api/v2/email-targets", rollback.spec.AdminToken, nil, 200, "")
	if string(bytes.TrimSpace(data)) != "[]" {
		t.Fatal("post-upgrade target leaked into pre-upgrade snapshot")
	}
	stillProtected, err := source.Metadata(ctx)
	if err != nil || !bytes.Equal(protected, stillProtected) || fixtureObjectFingerprint(t, ctx, source.objects) != sourceObjects || fixtureObjectFingerprint(t, ctx, rollback.objects) != sourceObjects {
		t.Fatal("rollback/rollforward changed source or object format")
	}
	t.Logf("formal v0.5.0 %s -> candidate: 136 old checksums preserved, 144 candidate entries including artifact usage paging and OCI bearer configuration verified, replay complete no-op; object bytes/Group order/repository IDs/grants preserved; new alert API readback passed; isolated pre-upgrade snapshot rollback and rollforward passed; expanded-DB old-binary rollback NOT tested/supported by this gate", alertUpgradeBaseline)
}

func formalUpgradeRelease(t *testing.T) Release {
	t.Helper()
	check := exec.Command("git", "rev-parse", "v0.5.0^{commit}")
	check.Dir = "../.."
	out, err := check.Output()
	if err != nil || strings.TrimSpace(string(out)) != alertUpgradeBaseline {
		t.Fatal("fixed formal v0.5.0 baseline unavailable")
	}
	source := t.TempDir()
	archive := filepath.Join(t.TempDir(), "source.tar")
	cmd := exec.Command("git", "archive", "--format=tar", "--output", archive, alertUpgradeBaseline)
	cmd.Dir = "../.."
	if err = cmd.Run(); err != nil {
		t.Fatal("formal source archive failed")
	}
	if err = exec.Command("tar", "-xf", archive, "-C", source).Run(); err != nil {
		t.Fatal(err)
	}
	version, err := os.ReadFile(filepath.Join(source, "VERSION"))
	if err != nil || strings.TrimSpace(string(version)) != "0.5.0" {
		t.Fatal("baseline VERSION mismatch")
	}
	releaseDir := t.TempDir()
	prefix := "github.com/artifact-gateway/artifact-gateway/internal/buildinfo."
	cmd = exec.Command("go", "build", "-mod=readonly", "-buildvcs=false", "-ldflags", "-X "+prefix+"injectedVersion=0.5.0 -X "+prefix+"injectedRevision="+alertUpgradeBaseline, "-o", filepath.Join(releaseDir, "gateway"), "./cmd/gateway")
	cmd.Dir = source
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH="+runtime.GOARCH)
	if out, err = cmd.CombinedOutput(); err != nil {
		t.Fatalf("formal baseline build: %v %s", err, out)
	}
	copyPrivateFixture(t, filepath.Join(source, "migrations"), filepath.Join(releaseDir, "migrations"))
	sum, _, err := describeBinary(filepath.Join(releaseDir, "gateway"))
	if err != nil {
		t.Fatal(err)
	}
	return Release{Directory: releaseDir, Identity: backupmanifest.GatewayIdentity{Version: "0.5.0", Revision: alertUpgradeBaseline, Artifact: &backupmanifest.SoftwareArtifact{Kind: "binary", SHA256: sum, Platform: "linux/" + runtime.GOARCH}}}
}

func runFixtureMigrations(t *testing.T, ctx context.Context, target *OwnedTarget, dir string) {
	t.Helper()
	id := target.resourceID("container", target.spec.Project+"-postgres")
	if id == "" {
		t.Fatal("owned migration target missing")
	}
	stage := "/tmp/ag-migrations-" + uuid.NewString()
	if _, err := target.spec.Docker.output(ctx, "cp", filepath.Join(dir, "migrations"), id+":"+stage); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, e := target.spec.Docker.output(ctx, "exec", id, "rm", "-rf", stage); e != nil {
			t.Error(e)
		}
	}()
	runner, err := os.ReadFile("../../scripts/run-migrations.sh")
	if err != nil {
		t.Fatal(err)
	}
	cmd, err := target.database.command(ctx, "sh", "-s")
	if err != nil {
		t.Fatal(err)
	}
	// docker exec needs the path forwarded too; the credential values still stay
	// in its process environment, matching the existing backup tool boundary.
	for i, arg := range cmd.Args {
		if arg == id {
			cmd.Args = append(cmd.Args[:i], append([]string{"--env", "MIGRATION_DIR"}, cmd.Args[i:]...)...)
			break
		}
	}
	cmd.Env = append(cmd.Env, "MIGRATION_DIR="+stage)
	cmd.Stdin = bytes.NewReader(runner)
	cmd.Stderr = nil
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("single migration runner: %v %s", err, out)
	}
}

func stopFixtureGateway(t *testing.T, ctx context.Context, target *OwnedTarget) {
	t.Helper()
	id := target.resourceID("container", target.spec.Project+"-gateway")
	if id == "" {
		t.Fatal("owned Gateway missing")
	}
	if _, err := target.spec.Docker.output(ctx, "stop", id); err != nil {
		t.Fatal(err)
	}
}

func replaceFixtureGateway(t *testing.T, ctx context.Context, target *OwnedTarget, release Release) string {
	t.Helper()
	for i, r := range target.resources {
		if r.Kind == "container" && r.Name == target.spec.Project+"-gateway" {
			if target.checkResource(ctx, r) != nil {
				t.Fatal("Gateway ownership changed")
			}
			if _, err := target.spec.Docker.output(ctx, "rm", r.ID); err != nil {
				t.Fatal(err)
			}
			target.resources = append(target.resources[:i], target.resources[i+1:]...)
			break
		}
	}
	if target.directory != "" {
		if err := os.RemoveAll(target.directory); err != nil {
			t.Fatal(err)
		}
		target.directory = ""
	}
	target.spec.Release = release
	endpoint, err := target.StartGateway(ctx)
	if err != nil || waitGateway(ctx, endpoint) != nil {
		t.Fatal("replacement Gateway startup failed")
	}
	return endpoint
}

func assertUpgradeCore(t *testing.T, ctx context.Context, target *OwnedTarget, endpoint string, ids []string, groupID string, group []byte) {
	t.Helper()
	data, _ := fixtureHTTP(t, ctx, endpoint, "GET", "/api/v2/groups/"+groupID, target.spec.AdminToken, nil, 200, "")
	if !bytes.Equal(bytes.TrimSpace(data), bytes.TrimSpace(group)) {
		t.Fatal("upgrade changed Group identity/member order")
	}
	for _, id := range ids {
		fixtureHTTP(t, ctx, endpoint, "GET", "/api/v2/repositories/"+id, target.spec.AdminToken, nil, 200, "")
		data, headers := fixtureHTTP(t, ctx, endpoint, "GET", "/api/v2/repositories/"+id+"/grants", target.spec.AdminToken, nil, 200, "")
		if headers.Get("ETag") != "2" || !bytes.Contains(data, []byte("upgrade-reader")) {
			t.Fatal("upgrade changed grant identity/version")
		}
	}
	data, _ = fixtureHTTP(t, ctx, endpoint, "GET", "/raw/upgrade-group/fixture.txt", target.spec.AdminToken, nil, 200, "")
	if string(data) != "original-first" {
		t.Fatal("upgrade changed object bytes or selected owner")
	}
}

func fixtureLedger(t *testing.T, data []byte) map[string]string {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) < 2 || lines[0] != "filename\tsha256" {
		t.Fatal("migration TSV ledger missing")
	}
	ledger := make(map[string]string)
	for _, line := range lines[1:] {
		name, sum, ok := strings.Cut(line, "\t")
		if !ok {
			t.Fatal("invalid migration ledger")
		}
		ledger[name] = sum
	}
	return ledger
}
