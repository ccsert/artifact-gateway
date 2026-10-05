package backupops

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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

// Explicit opt-in: creates only randomly named, owned local Docker resources.
// No checkout .env, existing project, persistent credential, or production data.
func TestLocalRecoveryIntegration(t *testing.T) {
	if os.Getenv("BACKUPOPS_DOCKER_TEST") != "1" {
		t.Skip("set BACKUPOPS_DOCKER_TEST=1 and BACKUPOPS_DOCKER_CONTEXT for real PG/S3 recovery")
	}
	for _, profile := range []string{"binary", "oci-image"} {
		t.Run(profile, func(t *testing.T) { runLocalRecoveryIntegration(t, profile) })
	}
}

func runLocalRecoveryIntegration(t *testing.T, profile string) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	d := Docker{Context: os.Getenv("BACKUPOPS_DOCKER_CONTEXT")}
	if err := d.Validate(ctx); err != nil {
		t.Fatal(err)
	}
	release := integrationRelease(t)
	if profile == "oci-image" {
		release = integrationOCIRelease(t, ctx, d, release)
	}
	spec := func() TargetSpec {
		return TargetSpec{Project: "ag-restore-" + uuid.NewString()[:18], Docker: d, Release: release,
			PostgresPassword: uuid.NewString(), AccessKey: "synthetic-test", SecretKey: uuid.NewString(), RPCSecret: uuid.NewString(),
			AdminToken: uuid.NewString(), ResolverToken: uuid.NewString(), RuntimeEnvironment: map[string]string{"GATEWAY_SETTINGS_ENCRYPTION_KEY": recoveryEmailKey}}
	}
	create := func(s TargetSpec) *OwnedTarget {
		t.Helper()
		x, err := NewTarget(ctx, s)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			cleanupCtx, c := context.WithTimeout(context.Background(), 30*time.Second)
			defer c()
			if err := x.Cleanup(cleanupCtx); err != nil {
				t.Error(err)
			}
		})
		return x
	}
	source := create(spec())
	sentinel := create(spec())
	migrateFixture(t, ctx, source.database, release.Directory)
	legacy := create(spec())
	legacyDir := t.TempDir()
	copyPrivateFixture(t, filepath.Join(release.Directory, "migrations"), filepath.Join(legacyDir, "migrations"))
	entries, e := os.ReadDir(filepath.Join(legacyDir, "migrations"))
	if e != nil {
		t.Fatal(e)
	}
	for _, entry := range entries {
		if entry.Name() > "000124_remove_legacy_roles.sql" {
			if e = os.Remove(filepath.Join(legacyDir, "migrations", entry.Name())); e != nil {
				t.Fatal(e)
			}
		}
	}
	migrateFixture(t, ctx, legacy.database, legacyDir)
	if e = legacy.database.WalkReferences(ctx, func(string) error { t.Fatal("empty v0.4.2 reference"); return nil }); e != nil {
		t.Fatal(e)
	}
	lc, e := legacy.database.connect(ctx)
	if e != nil {
		t.Fatal(e)
	}
	_, e = lc.Exec(ctx, "CREATE TABLE native_cargo_names(name text)")
	_ = lc.Close(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if legacy.database.WalkReferences(ctx, func(string) error { return nil }) == nil {
		t.Fatal("partial Cargo schema accepted")
	}

	conn, err := sentinel.database.connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = conn.Exec(ctx, "CREATE TABLE protected_marker(value text); INSERT INTO protected_marker VALUES ('synthetic-sentinel')")
	_ = conn.Close(ctx)
	if err != nil {
		t.Fatal(err)
	}
	marker := []byte("synthetic-sentinel-bytes")
	sum, _, _ := hashReader(bytes.NewReader(marker))
	if err = sentinel.objects.Put(ctx, "synthetic/sentinel", bytes.NewReader(marker), int64(len(marker)), sum); err != nil {
		t.Fatal(err)
	}
	sentinelBefore, err := sentinel.Metadata(ctx)
	if err != nil {
		t.Fatal(err)
	}
	url, err := source.StartGateway(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = waitGateway(ctx, url); err != nil {
		t.Fatal(err)
	}
	if profile == "oci-image" {
		verifyFixtureGatewayImage(t, ctx, source, release)
	}
	api := func(method, path, token string, body []byte, status int, match string) ([]byte, http.Header) {
		t.Helper()
		return fixtureHTTP(t, ctx, url, method, path, token, body, status, match)
	}
	var repos []struct{ ID, Name string }
	for i, name := range []string{"recovery-first", "recovery-second"} {
		data, _ := api("POST", "/api/v2/repositories", source.spec.AdminToken, []byte(`{"name":"`+name+`","format":"raw"}`), 201, "")
		var repo struct{ ID, Name string }
		if json.Unmarshal(data, &repo) != nil || repo.ID == "" {
			t.Fatal("repository identity missing")
		}
		repos = append(repos, repo)
		api("PUT", "/raw/"+name+"/releases/candidate.txt", source.spec.AdminToken, []byte([]string{"original-first", "original-second"}[i]), 201, "")
		api("PUT", "/api/v2/repositories/"+repo.ID+"/grants", source.spec.AdminToken, []byte(`[{"principal":"recovery-reader","scopes":["repositories:read"]}]`), 200, "1")
	}
	groupBody := `{"name":"recovery-group","format":"raw","members":[{"repositoryId":"` + repos[0].ID + `","position":0},{"repositoryId":"` + repos[1].ID + `","position":1}]}`
	groupBefore, _ := api("POST", "/api/v2/groups", source.spec.AdminToken, []byte(groupBody), 201, "")
	var group struct{ ID string }
	if json.Unmarshal(groupBefore, &group) != nil || group.ID == "" {
		t.Fatal("group identity missing")
	}
	token := func(actor string) string {
		t.Helper()
		data, _ := fixtureHTTP(t, ctx, url, "GET", "/auth/token", "Basic "+base64.StdEncoding.EncodeToString([]byte(actor+":"+source.spec.ResolverToken)), nil, 200, "")
		var v struct{ Token string }
		if json.Unmarshal(data, &v) != nil || v.Token == "" {
			t.Fatal("test token missing")
		}
		return v.Token
	}
	reader, denied := token("recovery-reader"), token("recovery-denied")
	body, _ := api("GET", "/raw/recovery-group/releases/candidate.txt", reader, nil, 200, "")
	if string(body) != "original-first" {
		t.Fatal("group did not select first source")
	}
	api("GET", "/raw/recovery-group/releases/candidate.txt", denied, nil, 403, "")
	emailFixture := seedRecoveryEmail(t, ctx, source, url, repos[0].ID, repos[1].ID)
	// Flush audit/runtime writes before recording the offline boundary.
	writerID := source.resourceID("container", source.spec.Project+"-gateway")
	if _, err = d.output(ctx, "stop", writerID); err != nil {
		t.Fatal(err)
	}
	pgPort, err := source.port(ctx, "postgres", "5432/tcp")
	if err != nil {
		t.Fatal(err)
	}
	s3Port, err := source.port(ctx, "rustfs", "9000/tcp")
	if err != nil {
		t.Fatal(err)
	}
	ss := SourceSpec{BackupID: "synthetic-recovery", ScopeID: "synthetic-local", InventoryDeclaredComplete: true, Release: release, Docker: d, Postgres: source.database,
		S3: S3Settings{Endpoint: "http://127.0.0.1:" + s3Port, Bucket: "gateway-cache", AccessKey: source.spec.AccessKey, SecretKey: source.spec.SecretKey}, Writers: []WriterSpec{{ID: "synthetic-gateway", Kind: "docker-container", Reference: writerID}}}
	_ = pgPort
	observed, err := newSource(ctx, ss)
	if err != nil {
		t.Fatal(err)
	}
	var refs []string
	if err = observed.WalkReferences(ctx, func(key string) error { refs = append(refs, key); return nil }); err != nil {
		t.Fatal(err)
	}
	if len(refs) != 2 {
		t.Fatalf("real schema references=%d, want 2", len(refs))
	}
	auditConn, e := source.database.connect(ctx)
	if e != nil {
		t.Fatal(e)
	}
	var auditMax int64
	var auditBefore string
	if e = auditConn.QueryRow(ctx, "SELECT COALESCE(max(id),0),COALESCE(jsonb_agg(to_jsonb(a) ORDER BY id)::text,'[]') FROM resolver_audit_log a").Scan(&auditMax, &auditBefore); e != nil || auditMax == 0 {
		t.Fatal("synthetic audit evidence missing")
	}
	_ = auditConn.Close(ctx)
	orphan := []byte("unreferenced-backup-bytes")
	orphanDigest, _, _ := hashReader(bytes.NewReader(orphan))
	if err = source.objects.Put(ctx, "synthetic/orphan-before-backup", bytes.NewReader(orphan), int64(len(orphan)), orphanDigest); err != nil {
		t.Fatal(err)
	}
	backupObjects := fixtureObjectFingerprint(t, ctx, source.objects)
	bundle := filepath.Join(t.TempDir(), "bundle")
	exported, code, _ := fixtureBackupCommand(t, ctx, "export", ss, bundle)
	if code != 0 || exported.Status != "exported" || exported.VerifiedObjects != 3 || exported.VerifiedBytes != 54 || exported.Gateway == nil || !sameSoftware(*exported.Gateway, release.Identity) {
		t.Fatalf("export: %+v exit=%d", exported, code)
	}
	// Deliberate mutation is outside the stopped backup interval. This also proves
	// restore never copies live source stores, because the later bytes stay absent.
	mutation := []byte("post-backup-mutation")
	mutationSum, _, _ := hashReader(bytes.NewReader(mutation))
	if err = source.objects.Put(ctx, "synthetic/after-backup", bytes.NewReader(mutation), int64(len(mutation)), mutationSum); err != nil {
		t.Fatal(err)
	}
	conn, err = source.database.connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = conn.Exec(ctx, "CREATE TABLE post_backup_mutation(value text); INSERT INTO post_backup_mutation VALUES ('later')")
	_ = conn.Close(ctx)
	if err != nil {
		t.Fatal(err)
	}
	sourceObjectsBefore := fixtureObjectFingerprint(t, ctx, source.objects)
	sourceBefore, err := source.Metadata(ctx)
	if err != nil {
		t.Fatal(err)
	}
	targetSpec := spec()
	targetSpec.ResolverToken = source.spec.ResolverToken
	targetSpec.ReaderToken = reader
	targetSpec.DeniedToken = denied
	n := int64(len("original-first"))
	digest, _, _ := hashReader(strings.NewReader("original-first"))
	targetSpec.ReadChecks = []ReadCheck{{Kind: "grant-allow", Format: "raw", Path: "/raw/recovery-group/releases/candidate.txt", Credential: "reader", Principal: "recovery-reader", Status: 200, Size: &n, SHA256: digest},
		{Kind: "grant-deny", Format: "raw", Path: "/raw/recovery-group/releases/candidate.txt", Credential: "denied", Principal: "recovery-denied", Status: 403}}
	t.Setenv("COMPOSE_PROJECT_NAME", sentinel.spec.Project)
	t.Setenv("DOCKER_HOST", "tcp://127.0.0.1:1")
	restored, code, target := fixtureBackupCommand(t, ctx, "restore", targetSpec, bundle)
	if code != 0 {
		t.Fatalf("restore: %+v exit=%d", restored, code)
	}
	if target == nil {
		t.Fatal("successful public restore did not create a captured target")
	}
	if restored.Software != "verified" || restored.Database != "restored" || restored.GrantMetadata != "verified" || restored.GroupMetadata != "verified" || restored.AuditMetadata != "verified" || restored.Metadata != "verified" || restored.Objects != "verified" || restored.Readiness != "verified" || restored.Protocol != "verified" || restored.Authorization != "verified" || restored.VerifiedObjects != 3 || restored.VerifiedBytes != 54 || restored.Gateway == nil || !sameSoftware(*restored.Gateway, release.Identity) {
		t.Fatalf("incomplete real recovery report: %+v", restored)
	}
	if fixtureObjectFingerprint(t, ctx, target.objects) != backupObjects {
		t.Fatal("restored full bucket differs from backup, including unreferenced bytes")
	}
	if profile == "oci-image" {
		verifyFixtureGatewayImage(t, ctx, target, release)
		verifyOCIIdentityRejections(t, ctx, d, bundle, targetSpec, ss)
	}
	port, err := target.port(ctx, "gateway", "8080/tcp")
	if err != nil {
		t.Fatal(err)
	}
	url = "http://127.0.0.1:" + port
	groupAfter, _ := api("GET", "/api/v2/groups/"+group.ID, targetSpec.AdminToken, nil, 200, "")
	var beforeJSON, afterJSON any
	_ = json.Unmarshal(groupBefore, &beforeJSON)
	_ = json.Unmarshal(groupAfter, &afterJSON)
	beforeBytes, _ := json.Marshal(beforeJSON)
	afterBytes, _ := json.Marshal(afterJSON)
	if !bytes.Equal(beforeBytes, afterBytes) {
		t.Fatal("group identity/members/order changed")
	}
	_, headers := api("GET", "/api/v2/repositories/"+repos[0].ID+"/grants", targetSpec.AdminToken, nil, 200, "")
	if headers.Get("ETag") != "2" {
		t.Fatal("grant version changed")
	}
	conn, err = target.database.connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var auditAfter string
	if err = conn.QueryRow(ctx, "SELECT COALESCE(jsonb_agg(to_jsonb(a) ORDER BY id)::text,'[]') FROM resolver_audit_log a WHERE id <= $1", auditMax).Scan(&auditAfter); err != nil || auditBefore != auditAfter {
		t.Fatal("restored historical audit identity/actor/group/member/outcome changed")
	}
	var absent bool
	err = conn.QueryRow(ctx, "SELECT to_regclass('public.post_backup_mutation') IS NULL").Scan(&absent)
	_ = conn.Close(ctx)
	if err != nil || !absent {
		t.Fatal("post-backup database mutation recovered")
	}
	if body, _, err := target.objects.Open(ctx, "synthetic/after-backup"); err == nil {
		_ = body.Close()
		t.Fatal("post-backup object recovered")
	}
	verifyRecoveryEmail(t, ctx, target, url, denied, emailFixture, bundle)
	for _, scenario := range []string{"existing source", "existing sentinel", "wrong release", "corrupt object", "corrupt dump", "corrupt archive with matching digest", "unsupported manifest", "semantic failure", "postgres start failure", "gateway start failure", "lost create response"} {
		t.Run(scenario, func(t *testing.T) {
			candidate := spec()
			candidate.ResolverToken = targetSpec.ResolverToken
			candidate.ReaderToken = reader
			candidate.ReadChecks = targetSpec.ReadChecks
			candidate.DeniedToken = denied
			switch scenario {
			case "existing source":
				candidate.Project = source.spec.Project
			case "existing sentinel":
				candidate.Project = sentinel.spec.Project
			case "wrong release":
				candidate.Release.Identity.Version = "different"
			case "semantic failure":
				candidate.ReadChecks = append([]ReadCheck(nil), targetSpec.ReadChecks...)
				candidate.ReadChecks[0].SHA256 = "sha256:" + strings.Repeat("0", 64)
			}
			input := bundle
			if strings.HasPrefix(scenario, "corrupt") || scenario == "unsupported manifest" {
				input = filepath.Join(t.TempDir(), "damaged")
				copyPrivateFixture(t, bundle, input)
				path := "objects/000000000000.data"
				if scenario == "corrupt dump" || scenario == "corrupt archive with matching digest" {
					path = "database.dump"
				}
				if scenario == "unsupported manifest" {
					path = "manifest.json"
				}
				content := []byte("damaged")
				if scenario == "corrupt archive with matching digest" {
					content, err = os.ReadFile(filepath.Join(input, path))
					if err != nil || len(content) < 256 {
						t.Fatal("fixture archive unavailable")
					}
					content = content[:len(content)-128]
					m, e := LoadManifest(input)
					if e != nil {
						t.Fatal(e)
					}
					m.Database.File.SHA256, _, _ = hashReader(bytes.NewReader(content))
					size := int64(len(content))
					m.Database.File.Size = &size
					data, _ := json.Marshal(m)
					if e = os.WriteFile(filepath.Join(input, "manifest.json"), data, 0600); e != nil {
						t.Fatal(e)
					}
				}
				if scenario == "unsupported manifest" {
					m, e := LoadManifest(input)
					if e != nil {
						t.Fatal(e)
					}
					m.SchemaVersion = 999
					content, _ = json.Marshal(m)
				}
				if err := os.WriteFile(filepath.Join(input, path), content, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "postgres start failure" || scenario == "gateway start failure" || scenario == "lost create response" {
				injectDockerFailure(t, candidate, scenario)
			}
			r, err := RestoreNew(ctx, input, candidate)
			if scenario == "corrupt archive with matching digest" && r.Reason != "database_input_invalid" {
				t.Fatalf("corrupt archive was not rejected before target creation: %+v", r)
			}
			if err == nil || r.Status != "failed" {
				t.Fatalf("unsafe acceptance: %+v", r)
			}
			if scenario != "existing source" && scenario != "existing sentinel" {
				for _, entry := range []struct{ kind, suffix string }{{"container", "postgres"}, {"container", "rustfs"}, {"container", "gateway"}, {"volume", "pgdata"}, {"volume", "objects"}, {"network", "network"}} {
					if _, e := d.output(ctx, entry.kind, "inspect", candidate.Project+"-"+entry.suffix); e == nil {
						t.Fatal("failure retained target resource")
					}
				}
			}
		})
	}
	for _, x := range []*OwnedTarget{source, sentinel} {
		if err = x.checkOwned(ctx); err != nil {
			t.Fatal("protected identity/mount/network changed")
		}
		if err = x.database.connectHealth(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if fixtureObjectFingerprint(t, ctx, source.objects) != sourceObjectsBefore {
		t.Fatal("source full-byte inventory changed by restore")
	}
	sourceAfter, err := source.Metadata(ctx)
	if err != nil || !bytes.Equal(sourceBefore, sourceAfter) {
		t.Fatal("source metadata changed by restore")
	}
	sentinelAfter, err := sentinel.Metadata(ctx)
	if err != nil || !bytes.Equal(sentinelBefore, sentinelAfter) {
		t.Fatal("sentinel metadata changed by restore")
	}
	checkBody, _, err := sentinel.objects.Open(ctx, "synthetic/sentinel")
	if err != nil {
		t.Fatal(err)
	}
	actual, _, err := hashReader(checkBody)
	_ = checkBody.Close()
	if err != nil || actual != sum {
		t.Fatal("sentinel bytes changed")
	}
	if err = source.objects.WalkObjects(ctx, func(key string, size int64) error {
		b, n, e := source.objects.Open(ctx, key)
		if e != nil {
			return e
		}
		defer func() { _ = b.Close() }()
		_, count, e := hashReader(b)
		if e != nil || count != size || n != size {
			return io.ErrUnexpectedEOF
		}
		return nil
	}); err != nil {
		t.Fatal("source object health changed")
	}
	data, _ := json.Marshal(restored)
	t.Logf("real PG/S3 recovery report: %s; protected source/sentinel identities, metadata, bytes and health unchanged", data)
}

func fixtureHTTP(t *testing.T, ctx context.Context, endpoint, method, path, token string, body []byte, status int, match string) ([]byte, http.Header) {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, method, endpoint+path, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		if strings.HasPrefix(token, "Basic ") {
			req.Header.Set("Authorization", token)
		} else {
			req.Header.Set("Authorization", "Bearer "+token)
		}
	}
	if len(body) > 0 && method != "GET" {
		if strings.HasPrefix(path, "/raw/") {
			req.Header.Set("Content-Type", "application/octet-stream")
		} else {
			req.Header.Set("Content-Type", "application/json")
		}
	}
	req.Header.Set("Idempotency-Key", uuid.NewString())
	if match != "" {
		req.Header.Set("If-Match", match)
	}
	client := &http.Client{Timeout: 20 * time.Second}
	res, err := client.Do(req)
	if err != nil {
		t.Fatal("fixture request unavailable")
	}
	defer func() { _ = res.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil || res.StatusCode != status {
		t.Fatalf("fixture %s %s status=%d expected=%d", method, path, res.StatusCode, status)
	}
	return data, res.Header
}

func integrationRelease(t *testing.T) Release {
	t.Helper()
	dir := t.TempDir()
	revision := strings.Repeat("a", 40)
	prefix := "github.com/artifact-gateway/artifact-gateway/internal/buildinfo."
	cmd := exec.Command("go", "build", "-buildvcs=false", "-ldflags", "-X "+prefix+"injectedVersion=synthetic-recovery -X "+prefix+"injectedRevision="+revision, "-o", filepath.Join(dir, "gateway"), "./cmd/gateway")
	cmd.Dir = "../.."
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH="+runtime.GOARCH)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fixture binary: %v %s", err, out)
	}
	copyPrivateFixture(t, "../../migrations", filepath.Join(dir, "migrations"))
	sum, _, err := describeBinary(filepath.Join(dir, "gateway"))
	if err != nil {
		t.Fatal(err)
	}
	return Release{Directory: dir, Identity: backupmanifest.GatewayIdentity{Version: "synthetic-recovery", Revision: revision, Artifact: &backupmanifest.SoftwareArtifact{Kind: "binary", SHA256: sum, Platform: "linux/" + runtime.GOARCH}}}
}

func copyPrivateFixture(t *testing.T, source, target string) {
	t.Helper()
	if err := os.Mkdir(target, 0700); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(source)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		a, b := filepath.Join(source, e.Name()), filepath.Join(target, e.Name())
		if e.IsDir() {
			copyPrivateFixture(t, a, b)
		} else {
			data, err := os.ReadFile(a)
			if err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(b, data, 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func migrateFixture(t *testing.T, ctx context.Context, p Postgres, releaseDir string) {
	t.Helper()
	conn, err := p.connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(ctx) }()
	if _, err = conn.Exec(ctx, "CREATE TABLE artifact_gateway_schema_migrations(filename text PRIMARY KEY,checksum text NOT NULL,applied_at timestamptz NOT NULL DEFAULT now())"); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(releaseDir, "migrations"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(releaseDir, "migrations", e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		h := sha256.Sum256(data)
		up, _, _ := strings.Cut(string(data), "-- +goose Down")
		tx, err := conn.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, up); err == nil {
			_, err = tx.Exec(ctx, "INSERT INTO artifact_gateway_schema_migrations(filename,checksum) VALUES($1,$2)", e.Name(), hex.EncodeToString(h[:]))
		}
		if err != nil {
			_ = tx.Rollback(ctx)
			t.Fatalf("migration %s: %v", e.Name(), err)
		}
		if err = tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
	}
}

func captureCreatedTarget(t *testing.T, ctx context.Context, s TargetSpec, stageRoot string) *OwnedTarget {
	t.Helper()
	x := &OwnedTarget{spec: s, checked: true}
	// Register cleanup before collecting receipts or deriving endpoints. Any
	// later fatal assertion still rechecks and removes only recorded owned IDs.
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := x.Cleanup(cleanupCtx); err != nil {
			t.Error(err)
		}
	})
	inspectionFailed := false
	for _, entry := range []struct{ kind, suffix string }{{"network", "network"}, {"volume", "pgdata"}, {"volume", "objects"}, {"container", "postgres"}, {"container", "rustfs"}, {"container", "gateway"}} {
		name := s.Project + "-" + entry.suffix
		data, err := s.Docker.output(ctx, entry.kind, "inspect", name)
		if err != nil {
			inspectionFailed = true
			continue
		}
		var items []struct {
			ID     string `json:"Id"`
			Name   string
			Labels map[string]string
			Config struct{ Labels map[string]string }
			Mounts []struct {
				Type, Source, Destination string
				RW                        bool
			}
		}
		if json.Unmarshal(data, &items) != nil || len(items) != 1 {
			inspectionFailed = true
			continue
		}
		item := items[0]
		owner := item.Labels["artifact-gateway.restore-owner"]
		if entry.kind == "container" {
			owner = item.Config.Labels["artifact-gateway.restore-owner"]
		}
		if entry.kind == "network" && strings.TrimPrefix(item.Name, "/") == name {
			x.owner = owner
		}
		if owner == "" || owner != x.owner || strings.TrimPrefix(item.Name, "/") != name {
			inspectionFailed = true
			continue
		}
		id := item.ID
		if entry.kind == "volume" {
			id = item.Name
		}
		x.resources = append(x.resources, ownedResource{entry.kind, id, name})
		if entry.kind == "container" && entry.suffix == "gateway" {
			if s.Release.Identity.Artifact.Kind == "binary" {
				if len(item.Mounts) != 1 {
					inspectionFailed = true
					continue
				}
				m := item.Mounts[0]
				dir := filepath.Dir(m.Source)
				rel, err := filepath.Rel(stageRoot, dir)
				info, statErr := os.Lstat(dir)
				// The mount cannot authorize deletion of an arbitrary host path.
				// Only a real private direct child of our fresh TMPDIR is owned.
				if m.Type != "bind" || m.RW || m.Destination != "/gateway" || filepath.Base(m.Source) != "gateway" || err != nil || strings.ContainsAny(rel, `/\`) || !strings.HasPrefix(rel, "ag-restore-release-") || statErr != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
					inspectionFailed = true
					continue
				}
				x.directory = dir
			} else if len(item.Mounts) != 0 {
				// OCI never derives a host cleanup path from an unexpected mount.
				inspectionFailed = true
			}
		}
	}
	if inspectionFailed {
		t.Fatal("created receipt unavailable or ownership changed; unverified resources retained")
	}
	port, err := x.port(ctx, "postgres", "5432/tcp")
	if err != nil {
		t.Fatal(err)
	}
	x.database = Postgres{DSN: "postgres://gateway:" + escapePassword(s.PostgresPassword) + "@127.0.0.1:" + port + "/gateway?sslmode=disable", ToolsContainer: x.resourceID("container", s.Project+"-postgres"), DockerContext: s.Docker.Context}
	port, err = x.port(ctx, "rustfs", "9000/tcp")
	if err != nil {
		t.Fatal(err)
	}
	x.objects, err = newS3(S3Settings{Endpoint: "http://127.0.0.1:" + port, Bucket: "gateway-cache", AccessKey: s.AccessKey, SecretKey: s.SecretKey})
	if err != nil {
		t.Fatal(err)
	}
	return x
}

func (p Postgres) connectHealth(ctx context.Context) error {
	c, e := p.connect(ctx)
	if e != nil {
		return e
	}
	defer func() { _ = c.Close(ctx) }()
	return c.Ping(ctx)
}

func fixtureObjectFingerprint(t *testing.T, ctx context.Context, s *s3Objects) string {
	t.Helper()
	var inventory strings.Builder
	if err := s.WalkObjects(ctx, func(key string, size int64) error {
		body, n, e := s.Open(ctx, key)
		if e != nil {
			return e
		}
		sum, count, e := hashReader(body)
		_ = body.Close()
		if e != nil || count != size || n != size {
			return io.ErrUnexpectedEOF
		}
		inventory.WriteString(key + ":" + sum + "\n")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	sum, _, e := hashReader(strings.NewReader(inventory.String()))
	if e != nil {
		t.Fatal(e)
	}
	return sum
}

// Execute real Docker create operations; inject only the selected invocation's
// start failure or lost create response. All inspect/cleanup operations and
// protected source/sentinel resources continue through the real local CLI.
func injectDockerFailure(t *testing.T, spec TargetSpec, scenario string) {
	t.Helper()
	docker, err := exec.LookPath("docker")
	if err != nil {
		t.Fatal(err)
	}
	quote := func(v string) string { return "'" + strings.ReplaceAll(v, "'", "'\"'\"'") + "'" }
	suffix := "postgres"
	if scenario == "gateway start failure" {
		suffix = "gateway"
	}
	name := spec.Project + "-" + suffix
	script := "#!/bin/sh\n"
	if scenario == "lost create response" {
		script += fmt.Sprintf(`if [ "$1" = "--context" ] && [ "$3" = "create" ]; then
  for arg in "$@"; do
    if [ "$arg" = %s ]; then
      %s "$@" >/dev/null || exit 1
      exit 1
    fi
  done
fi
`, quote(name), quote(docker))
	} else {
		script += fmt.Sprintf(`if [ "$1" = "--context" ] && [ "$3" = "start" ] && [ "$#" = "4" ]; then
  name=$(%s --context "$2" container inspect --format '{{.Name}}' "$4")
  if [ "$name" = %s ]; then exit 1; fi
fi
`, quote(docker), quote("/"+name))
	}
	script += "exec " + quote(docker) + " \"$@\"\n"
	dir := t.TempDir()
	if err = os.WriteFile(filepath.Join(dir, "docker"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}
