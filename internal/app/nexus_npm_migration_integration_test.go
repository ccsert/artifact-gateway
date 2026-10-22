//go:build integration

package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ccsert/artifact-gateway/internal/egress"
	"github.com/ccsert/artifact-gateway/internal/nexusmigration"
	migrationpg "github.com/ccsert/artifact-gateway/internal/nexusmigration/pgstore"
	"github.com/ccsert/artifact-gateway/internal/nexussource"
	sourcepg "github.com/ccsert/artifact-gateway/internal/nexussource/pgstore"
	"github.com/ccsert/artifact-gateway/internal/objectstore"
	npmprotocol "github.com/ccsert/artifact-gateway/internal/protocol/npm"
	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/ccsert/artifact-gateway/internal/secrets"
	"github.com/ccsert/artifact-gateway/internal/testsupport"
	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
)

type npmMigrationRemote struct {
	t        *testing.T
	mu       sync.Mutex
	entries  []nexusmigration.Entry
	bodies   map[string][]byte
	requests map[string]int
	mutate   func(string, int, []byte) []byte
}

func (f *npmMigrationRemote) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	user, password, ok := r.BasicAuth()
	if r.Method != "GET" || !ok || user != "npm-readonly" || password != "synthetic-npm-source-secret-442" {
		f.t.Errorf("source read-only/credential boundary failed: method=%s", r.Method)
		w.WriteHeader(403)
		return
	}
	switch r.URL.Path {
	case "/service/rest/v1/repositories":
		_ = json.NewEncoder(w).Encode([]map[string]string{{"name": "source", "format": "npm", "type": "hosted"}})
	case "/service/rest/v1/repositories/npm/hosted/source":
		_, _ = io.WriteString(w, `{"name":"source","storage":{"writePolicy":"ALLOW_ONCE"}}`)
	case "/service/rest/v1/components":
		items := []map[string]any{}
		for _, e := range f.entries {
			for _, a := range e.Assets {
				if a.TargetPath == nexusmigration.NPMPackumentPath {
					continue
				}
				body := f.bodies[a.SourcePath]
				name, group := e.Identity, ""
				if strings.HasPrefix(name, "@") {
					group, name, _ = strings.Cut(name, "/")
				}
				items = append(items, map[string]any{"repository": "source", "format": "npm", "group": group, "name": name, "version": a.TargetPath, "assets": []map[string]any{{"repository": "source", "format": "npm", "path": a.SourcePath, "downloadUrl": "https://example.com/repository/source/" + a.SourcePath, "fileSize": len(body), "checksum": map[string]string{"sha256": fmt.Sprintf("%x", sha256.Sum256(body))}}}})
			}
		}
		offset, _ := strconv.Atoi(r.URL.Query().Get("continuationToken"))
		end := min(offset+2, len(items))
		var next any
		if end < len(items) {
			next = strconv.Itoa(end)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"items": items[offset:end], "continuationToken": next})
	default:
		if !strings.HasPrefix(r.URL.Path, "/repository/source/") {
			f.t.Errorf("source request outside native/read-only allowlist: %s", r.URL.Path)
			w.WriteHeader(403)
			return
		}
		p := strings.TrimPrefix(r.URL.Path, "/repository/source/")
		body, present := f.bodies[p]
		if !present {
			w.WriteHeader(404)
			return
		}
		f.requests[p]++
		body = bytes.Clone(body)
		if f.mutate != nil {
			body = f.mutate(p, f.requests[p], body)
		}
		etag := fmt.Sprintf("\"%x\"", sha256.Sum256(body))
		if expected := r.Header.Get("If-Match"); expected != "" && expected != etag {
			w.WriteHeader(412)
			return
		}
		w.Header().Set("ETag", etag)
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		if !strings.Contains(p, "/-/") {
			w.Header().Set("Content-Type", "application/json")
		}
		_, _ = w.Write(body)
	}
}

type npmMigrationPG struct {
	t           *testing.T
	databaseURL string
	db          *sql.DB
	store       *repository.PostgresStore
	objects     *objectstore.RustFSStore
	repo        repository.HostedRepository
	source      *nexussource.Service
	created     nexussource.Source
	remote      *npmMigrationRemote
	server      *httptest.Server
	service     *nexusmigration.Service
	publication *NexusMigrationPublication
}

func newNPMMigrationPG(t *testing.T) *npmMigrationPG {
	t.Helper()
	databaseURL, endpoint := os.Getenv("TEST_DATABASE_URL"), os.Getenv("TEST_RUSTFS_ENDPOINT")
	if databaseURL == "" || endpoint == "" {
		if os.Getenv("NPM_MIGRATION_REQUIRED") == "1" {
			t.Fatal("mandatory npm migration proof requires isolated PostgreSQL/RustFS")
		}
		t.Skip("isolated PostgreSQL/RustFS required")
	}
	if err := testsupport.ValidateIsolatedDatabaseURL(databaseURL); err != nil {
		t.Fatal(err)
	}
	t.Setenv(secrets.KeyEnv, "0123456789abcdef0123456789abcdef")
	store, err := repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	objects, err := objectstore.NewRustFSStore(endpoint, os.Getenv("TEST_RUSTFS_ACCESS_KEY"), os.Getenv("TEST_RUSTFS_SECRET_KEY"), "npm-migration-"+strings.ReplaceAll(uuid.NewString(), "-", ""))
	if err != nil {
		t.Fatal(err)
	}
	if err = objects.EnsureBucket(context.Background()); err != nil {
		t.Fatal(err)
	}
	repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "npm-mig-" + uuid.NewString(), Format: repository.FormatNPM})
	if err != nil {
		t.Fatal(err)
	}
	remote := &npmMigrationRemote{t: t, bodies: map[string][]byte{}, requests: map[string]int{}}
	for _, name := range []string{"widget", "@scope/widget"} {
		e, bodies := npmMigrationInventory(t, repo, name)
		remote.entries = append(remote.entries, e)
		for p, b := range bodies {
			remote.bodies[p] = b
		}
	}
	server := httptest.NewTLSServer(remote)
	t.Cleanup(server.Close)
	hooks := egress.DefaultHooks()
	hooks.ProxyFromEnvironment = func(*http.Request) (*url.URL, error) { return nil, nil }
	hooks.LookupIP = func(context.Context, string, string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("203.0.113.24")}, nil
	}
	hooks.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
	}
	source := nexussource.New(sourcepg.New(db), store, nexussource.Options{Client: server.Client(), EgressHooks: &hooks})
	created, err := source.Create(context.Background(), nexussource.Input{Name: "synthetic-npm-source", BaseURL: "https://example.com", Credentials: &nexussource.Credentials{Username: "npm-readonly", Password: "synthetic-npm-source-secret-442"}})
	if err != nil {
		t.Fatal(err)
	}
	f := &npmMigrationPG{t: t, databaseURL: databaseURL, db: db, store: store, objects: objects, repo: repo, source: source, created: created, remote: remote, server: server}
	f.reset(nexusmigration.Options{})
	t.Cleanup(func() {
		_, err := db.Exec(`UPDATE nexus_migration_batches SET state='cancelled',lease_token='',lease_expires_at=NULL,cancel_requested=true WHERE source_id=$1 AND state NOT IN ('completed','cancelled')`, created.ID)
		if err != nil {
			t.Errorf("owned npm batch cleanup: %v", err)
		}
	})
	return f
}
func npmMigrationEvidence() nexusmigration.CapacityEvidence {
	zero, free, download := int64(0), int64(1<<30), int64(16<<20)
	now := time.Now().UTC()
	return nexusmigration.CapacityEvidence{TargetID: "npm-fixture-target", PoolID: "npm-fixture-pool", ObservedAt: now.Add(-time.Second), ValidUntil: now.Add(30 * time.Minute), FreeBytes: &free, DownloadBytes: &download, UploadBytes: &zero, BackupBytes: &zero, RestoreBytes: &zero, HeadroomBytes: &zero}
}
func (f *npmMigrationPG) reset(options nexusmigration.Options) {
	options.TargetID, options.PoolID = "npm-fixture-target", "npm-fixture-pool"
	f.publication = NewNexusMigrationPublication(f.store, SharedObjectStores(f.objects), options.TargetID, options.PoolID).WithNPM(NewNexusNPMMigrationPublication(f.store, f.objects))
	f.service = nexusmigration.New(migrationpg.New(f.db), f.source, f.publication, options)
}
func (f *npmMigrationPG) start() nexusmigration.Batch {
	f.t.Helper()
	b, err := f.service.Start(context.Background(), nexusmigration.StartInput{SourceID: f.created.ID, SourceVersion: f.created.Version, PolicyVersion: nexusmigration.PolicyVersion, Mappings: []nexusmigration.Mapping{{SourceRepository: "source", TargetRepositoryID: f.repo.ID}}, CapacityEvidence: npmMigrationEvidence()}, "synthetic-admin", uuid.NewString())
	if err != nil {
		f.t.Fatal(err)
	}
	return b
}
func (f *npmMigrationPG) run(b nexusmigration.Batch) nexusmigration.Batch {
	f.t.Helper()
	if err := f.service.RunOnce(context.Background(), []string{"npm"}); err != nil {
		f.t.Fatal(err)
	}
	b, err := f.service.Get(context.Background(), b.ID)
	if err != nil {
		f.t.Fatal(err)
	}
	return b
}
func (f *npmMigrationPG) noVersions() {
	f.t.Helper()
	var count int
	if err := f.db.QueryRow(`SELECT count(*)FROM native_npm_versions WHERE repository_id=$1`, f.repo.ID).Scan(&count); err != nil || count != 0 {
		f.t.Fatalf("rejected migration leaked %d native versions: %v", count, err)
	}
	var audits int
	if err := f.db.QueryRow(`SELECT count(*)FROM resolver_audit_log WHERE repository=$1 AND operation='publish'`, f.repo.Name).Scan(&audits); err != nil || audits != 0 {
		f.t.Fatalf("rejected migration leaked %d publication audits: %v", audits, err)
	}
}
func (f *npmMigrationPG) reopen() {
	f.t.Helper()
	_ = f.store.Close()
	var err error
	f.store, err = repository.NewPostgresStore(f.databaseURL)
	if err != nil {
		f.t.Fatal(err)
	}
	f.t.Cleanup(func() { _ = f.store.Close() })
	f.reset(nexusmigration.Options{})
}

func TestPostgresRustFSNexusNPMMigrationCompleteClosureAndRestart(t *testing.T) {
	f := newNPMMigrationPG(t)
	b := f.run(f.start())
	if b.State != "completed" || b.VisibleIdentities != 2 || b.Counts.Committed != 2 {
		t.Fatalf("complete closure=%+v", b)
	}

	expectedBytes := int64(0)
	for p, body := range f.remote.bodies {
		if strings.Contains(p, "/-/") {
			expectedBytes += int64(len(body))
		}
	}
	if b.TotalBytes == nil || *b.TotalBytes != expectedBytes || b.VerifiedBytes != expectedBytes || b.TransferredBytes != expectedBytes {
		t.Fatalf("metadata was counted as migrated target bytes: total=%v verified=%d transferred=%d want=%d", b.TotalBytes, b.VerifiedBytes, b.TransferredBytes, expectedBytes)
	}
	f.reopen()
	entries, _, err := f.service.Entries(context.Background(), b.ID, 100, "")
	if err != nil || len(entries) != 2 {
		t.Fatalf("durable closure=%d %v", len(entries), err)
	}
	for _, e := range entries {
		for _, a := range e.Assets {
			if a.TargetPath == nexusmigration.NPMPackumentPath && (a.Uploaded || a.Verified) {
				t.Fatal("metadata acquired a target byte receipt")
			}
		}
		if state, err := f.publication.Readback(context.Background(), e); err != nil || state != nexusmigration.Identical {
			t.Fatalf("reopened byte readback=%s %v", state, err)
		}
	}
	handler := newTestGateway(t, GatewayOptions{Dependencies: Dependencies{MigrationService: f.service}, Store: f.store, Adapter: TestAdapter{}, Authenticator: testAuthenticator(), Storage: SharedObjectStores(f.objects)})
	request := httptest.NewRequest(http.MethodGet, "/api/v2/migrations/nexus/batches/"+b.ID+"/entries?limit=100", nil)
	request.Header.Set("Authorization", "Bearer admin-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != 200 || bytes.Contains(response.Body.Bytes(), []byte(nexusmigration.NPMPackumentPath)) || bytes.Contains(response.Body.Bytes(), []byte("npmPackument")) || bytes.Contains(response.Body.Bytes(), []byte("synthetic-npm-source-secret-442")) {
		t.Fatalf("management inventory leaked private metadata or failed: status=%d body=%s", response.Code, response.Body.String())
	}
	var page struct{ Items []nexusmigration.Entry }
	if err = json.Unmarshal(response.Body.Bytes(), &page); err != nil || len(page.Items) != 2 {
		t.Fatalf("public npm entries=%+v %v", page, err)
	}
	for _, e := range page.Items {
		if len(e.Assets) != 2 {
			t.Fatalf("public entry did not expose exactly two target byte assets: %+v", e)
		}
	}
	repeated := f.run(f.start())
	if repeated.State != "completed" || repeated.Counts.Skipped != 2 || repeated.Counts.Committed != 0 {
		t.Fatalf("idempotent complete closure=%+v", repeated)
	}
	raw, _ := json.Marshal(entries)
	if bytes.Contains(raw, []byte("synthetic-npm-source-secret-442")) || bytes.Contains(raw, []byte("original producer metadata")) {
		t.Fatal("private metadata/credential escaped management entries")
	}
	t.Logf("source packuments and four full tarballs verified; restart retained two package closures; manifest=%s sourceVerificationBytes=%d transferred=%d", b.ManifestSHA256, b.SourceVerificationBytes, b.TransferredBytes)
}
func TestPostgresRustFSNexusNPMMigrationRecoversUploadVerifyAndCommitAckLoss(t *testing.T) {
	for _, mode := range []string{"upload", "verify", "publish"} {
		t.Run(mode, func(t *testing.T) {
			f := newNPMMigrationPG(t)
			fired := false
			fault := func(context.Context, nexusmigration.Entry) error {
				if !fired {
					fired = true
					return nexusmigration.Failed("fixture_ack_lost")
				}
				return nil
			}
			options := nexusmigration.Options{}
			switch mode {
			case "upload":
				options.AfterUpload = fault
			case "verify":
				options.AfterVerify = fault
			case "publish":
				options.AfterPublish = fault
			}
			f.reset(options)
			b := f.run(f.start())
			if b.State != "failed" || !fired {
				t.Fatalf("fault not durably recorded=%+v", b)
			}
			if mode != "publish" {
				pkg, err := f.store.GetNPMPackage(context.Background(), f.repo.ID, "@scope/widget")
				if err == nil && len(pkg.Versions) > 0 {
					t.Fatal("partial staged package became visible")
				}
			}
			f.reopen()
			evidence := npmMigrationEvidence()
			var err error
			b, err = f.service.Control(context.Background(), b.ID, b.Version, "synthetic-admin", uuid.NewString(), "retry", &evidence)
			if err != nil {
				t.Fatal(err)
			}
			b = f.run(b)
			if b.State != "completed" || b.VisibleIdentities != 2 {
				t.Fatalf("recovery did not complete exact closure=%+v", b)
			}
			audits, err := f.store.ListAudits(context.Background(), repository.AuditQuery{Repository: f.repo.Name, Operation: "publish", Limit: 100})
			if err != nil || len(audits) != 2 {
				t.Fatalf("recovery duplicated atomic publication audits=%d %v", len(audits), err)
			}
		})
	}
}
func TestPostgresNexusNPMMigrationRejectsMissingWrongIntegrityIdentityAndSourceDrift(t *testing.T) {
	for _, mode := range []string{"missing-version", "wrong-sri", "wrong-package-json", "same-size-drift", "same-size-after-scan", "packument-drift", "packument-after-scan", "forbidden-url"} {
		t.Run(mode, func(t *testing.T) {
			f := newNPMMigrationPG(t)
			if mode == "missing-version" {
				f.remote.entries[0].Assets = f.remote.entries[0].Assets[:2]
			}
			if mode == "wrong-sri" || mode == "forbidden-url" || mode == "wrong-package-json" {
				raw := map[string]any{}
				_ = json.Unmarshal(f.remote.bodies["widget"], &raw)
				versions := raw["versions"].(map[string]any)
				v := versions["1.0.0"].(map[string]any)
				dist := v["dist"].(map[string]any)
				switch mode {
				case "wrong-sri":
					dist["integrity"] = "sha512-" + strings.Repeat("A", 86) + "=="
				case "forbidden-url":
					dist["tarball"] = "https://denied.invalid/credential@host.tgz"
				case "wrong-package-json":
					body := npmFixtureTarball(t, "different-name", "1.0.0")
					path := f.remote.entries[0].Assets[1].SourcePath
					f.remote.bodies[path] = body
					hs := map[string]string{}
					for algorithm, hasher := range npmImportFixtureHashes(body) {
						hs[algorithm] = hasher
					}
					dist["integrity"] = hs["integrity"]
					dist["shasum"] = hs["shasum"]
				}
				f.remote.bodies["widget"], _ = json.Marshal(raw)
			}
			if mode == "same-size-drift" {
				f.remote.mutate = func(p string, _ int, b []byte) []byte {
					if strings.Contains(p, "/-/") {
						b[0] ^= 1
					}
					return b
				}
			}

			if mode == "same-size-after-scan" {
				f.remote.mutate = func(p string, n int, b []byte) []byte {
					if strings.Contains(p, "/-/") && n >= 2 {
						b[0] ^= 1
					}
					return b
				}
			}
			if mode == "packument-after-scan" {
				f.remote.mutate = func(p string, n int, b []byte) []byte {
					if !strings.Contains(p, "/-/") && n >= 3 {
						b = bytes.ReplaceAll(b, []byte("original producer metadata"), []byte("changed! producer metadata"))
					}
					return b
				}
			}
			if mode == "packument-drift" {
				f.remote.mutate = func(p string, n int, b []byte) []byte {
					if p == "widget" && n >= 2 {
						b = bytes.ReplaceAll(b, []byte("original producer metadata"), []byte("changed! producer metadata"))
					}
					return b
				}
			}
			b := f.run(f.start())
			if b.State == "completed" || b.VisibleIdentities != 0 {
				t.Fatalf("invalid source closure accepted mode=%s %+v", mode, b)
			}
			f.noVersions()
			t.Logf("%s blocked at %s", mode, b.LastError)
		})
	}
}

func npmImportFixtureHashes(body []byte) map[string]string {
	hashers := npmprotocol.ImportHashers()
	for _, h := range hashers {
		_, _ = h.Write(body)
	}
	return map[string]string{"integrity": "sha512-" + base64.StdEncoding.EncodeToString(hashers["sha512"].Sum(nil)), "shasum": hex.EncodeToString(hashers["sha1"].Sum(nil))}
}

type interceptedNPMMigration struct {
	nexusmigration.Publication
	verifications atomic.Int32
	beforeFinal   func(context.Context) error
	guard         repository.NativePublicationGuard
}

func (p *interceptedNPMMigration) Verify(ctx context.Context, e nexusmigration.Entry) error {
	if err := p.Publication.Verify(ctx, e); err != nil {
		return err
	}
	if p.verifications.Add(1) == 2 && p.beforeFinal != nil {
		return p.beforeFinal(ctx)
	}
	return nil
}
func (p *interceptedNPMMigration) Publish(ctx context.Context, e nexusmigration.Entry) error {
	return p.Publication.Publish(repository.WithNativePublicationGuard(ctx, p.guard), e)
}
func TestPostgresNexusNPMMigrationLeaseAndSourceVersionFence(t *testing.T) {
	for _, mode := range []string{"cancel", "lease-expired", "source-version"} {
		t.Run(mode, func(t *testing.T) {
			f := newNPMMigrationPG(t)
			b := f.start()
			intercept := &interceptedNPMMigration{Publication: f.publication}
			intercept.beforeFinal = func(ctx context.Context) error {
				f.noVersions()
				switch mode {
				case "cancel":
					current, err := f.service.Get(ctx, b.ID)
					if err != nil {
						return err
					}
					_, err = f.service.Control(ctx, b.ID, current.Version, "synthetic-admin", uuid.NewString(), "cancel", nil)
					return err
				case "lease-expired":
					_, err := f.db.ExecContext(ctx, `UPDATE nexus_migration_batches SET lease_expires_at=clock_timestamp()-interval '1 second'WHERE id=$1`, b.ID)
					return err
				default:
					_, err := f.source.Update(ctx, f.created.ID, nexussource.Input{Name: f.created.Name, BaseURL: f.created.BaseURL}, f.created.Version)
					return err
				}
			}
			f.service = nexusmigration.New(migrationpg.New(f.db), f.source, intercept, nexusmigration.Options{TargetID: "npm-fixture-target", PoolID: "npm-fixture-pool", HeartbeatInterval: time.Hour})
			b = f.run(b)
			f.noVersions()
			if b.VisibleIdentities != 0 {
				t.Fatalf("stale owner became visible=%+v", b)
			}
			if mode == "lease-expired" {
				f.reset(nexusmigration.Options{})
				b = f.run(b)
				if b.State != "completed" || b.VisibleIdentities != 2 {
					t.Fatalf("new lease owner failed recovery=%+v", b)
				}
			}
		})
	}
}
func TestPostgresNexusNPMMigrationNativeTransactionLossCannotPublish(t *testing.T) {
	f := newNPMMigrationPG(t)
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	b := f.start()
	entered, release := make(chan int, 1), make(chan struct{})
	intercept := &interceptedNPMMigration{Publication: f.publication, guard: func(ctx context.Context, tx *sql.Tx) error {
		var pid int
		if err := tx.QueryRowContext(ctx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
			return err
		}
		entered <- pid
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}}
	service := nexusmigration.New(migrationpg.New(f.db), f.source, intercept, nexusmigration.Options{TargetID: "npm-fixture-target", PoolID: "npm-fixture-pool", HeartbeatInterval: time.Hour})
	done := make(chan error, 1)
	go func() { done <- service.RunOnce(ctx, []string{"npm"}) }()
	var pid int
	select {
	case pid = <-entered:
	case err := <-done:
		t.Fatalf("native transaction was not reached: %v", err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	var fenced bool
	if err := f.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_locks WHERE pid=$1 AND locktype='advisory'AND classid=((hashtextextended('nexus-migration-lease:'||$2,0)>>32)&4294967295)::oid AND objid=(hashtextextended('nexus-migration-lease:'||$2,0)&4294967295)::oid AND granted)`, pid, b.ID).Scan(&fenced); err != nil || !fenced {
		t.Fatalf("native backend does not own actual lease fence=%v %v", fenced, err)
	}
	var terminated bool
	if err := f.db.QueryRowContext(ctx, `SELECT pg_terminate_backend($1,1000)`, pid).Scan(&terminated); err != nil || !terminated {
		t.Fatalf("terminate owned transaction=%v %v", terminated, err)
	}
	current, err := service.Get(ctx, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Control(ctx, b.ID, current.Version, "synthetic-admin", uuid.NewString(), "cancel", nil); err != nil {
		t.Fatal(err)
	}
	close(release)
	select {
	case err = <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	f.noVersions()
}
func TestPostgresNexusNPMMigrationPreservesTargetTagsAndDeprecation(t *testing.T) {
	f := newNPMMigrationPG(t)
	b := f.run(f.start())
	if b.State != "completed" {
		t.Fatalf("seed=%+v", b)
	}
	if _, err := f.store.SetNPMDistTag(context.Background(), repository.NPMDistTagMutation{RepositoryID: f.repo.ID, PackageName: "widget", Tag: "latest", Version: "2.0.0-beta.1"}); err != nil {
		t.Fatal(err)
	}
	pkg, err := f.store.GetNPMPackage(context.Background(), f.repo.ID, "@scope/widget")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.UpdateNPMMetadata(context.Background(), repository.NPMMetadataMutation{RepositoryID: f.repo.ID, PackageName: pkg.Name, ExpectedRevision: pkg.Revision, Deprecations: map[string]string{"1.0.0": "target owner message"}}); err != nil {
		t.Fatal(err)
	}
	b = f.run(f.start())
	if b.State != "failed" || b.Counts.Conflict != 2 || b.VisibleIdentities != 0 {
		t.Fatalf("target metadata silently replaced=%+v", b)
	}
	plain, err := f.store.GetNPMPackage(context.Background(), f.repo.ID, "widget")
	if err != nil || plain.DistTags["latest"] != "2.0.0-beta.1" {
		t.Fatalf("target latest overwritten=%+v %v", plain, err)
	}
	scoped, err := f.store.GetNPMVersion(context.Background(), f.repo.ID, "@scope/widget", "1.0.0")
	if err != nil || scoped.Deprecated != "target owner message" {
		t.Fatalf("target deprecation overwritten=%+v %v", scoped, err)
	}
}
func TestPostgresNexusNPMMigrationQuarantineCannotBecomeVerifiedSkip(t *testing.T) {
	f := newNPMMigrationPG(t)
	b := f.run(f.start())
	if b.State != "completed" {
		t.Fatalf("seed=%+v", b)
	}
	v, err := f.store.GetNPMVersion(context.Background(), f.repo.ID, "widget", "1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.ReplaceArtifactQuarantine(context.Background(), repository.ArtifactQuarantine{RepositoryID: f.repo.ID, Format: repository.FormatNPM, Coordinate: "widget@1.0.0", Digest: v.Digest, State: repository.ArtifactQuarantineStateQuarantined, Reason: "migration fixture", UpdatedBy: "security-admin"}, "0"); err != nil {
		t.Fatal(err)
	}
	b = f.run(f.start())
	if b.State == "completed" || b.Counts.Skipped > 1 {
		t.Fatalf("quarantined bytes counted as verified=%+v", b)
	}
}

func TestPostgresMigrationTargetOnlyNPMClient(t *testing.T) {
	npmJS, node := os.Getenv("NPM_CLIENT_JS"), os.Getenv("NPM_CLIENT_NODE")
	if npmJS == "" || node == "" {
		if os.Getenv("NPM_MIGRATION_REQUIRED") == "1" {
			t.Fatal("pinned official npm client required")
		}
		t.Skip("set NPM_CLIENT_JS/NPM_CLIENT_NODE")
	}
	f := newNPMMigrationPG(t)
	b := f.run(f.start())
	if b.State != "completed" || b.VisibleIdentities != 2 {
		t.Fatalf("npm closure migration failed=%+v", b)
	}
	f.server.Close()
	f.reopen()
	handler := newTestGateway(t, GatewayOptions{Store: f.store, NPMMetadata: f.store, NPMRestore: f.store, Storage: SharedObjectStores(f.objects), Adapter: TestAdapter{}, Authenticator: testAuthenticator()})
	var mu sync.Mutex
	requested := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || !strings.HasPrefix(r.URL.Path, "/npm/"+f.repo.Name+"/") {
			t.Errorf("target-only npm requested outside migrated registry: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(403)
			return
		}
		mu.Lock()
		requested[r.URL.Path]++
		mu.Unlock()
		handler.ServeHTTP(w, r)
	}))
	defer server.Close()
	root := t.TempDir()
	registry := server.URL + "/npm/" + f.repo.Name + "/"
	config := filepath.Join(root, "npmrc")
	if err := os.WriteFile(config, []byte("registry="+registry+"\n"+strings.TrimPrefix(registry, "http:")+":_authToken=resolver-secret\n"), 0600); err != nil {
		t.Fatal(err)
	}
	commandIndex := 0
	command := func(dir, cache string, args ...string) []byte {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		base := []string{npmJS, "--userconfig", config, "--globalconfig", "/dev/null", "--cache", cache, "--registry", registry, "--ignore-scripts", "--no-audit", "--no-fund"}
		cmd := exec.CommandContext(ctx, node, append(base, args...)...)
		cmd.Dir = dir
		cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + root, "TMPDIR=" + root, "npm_config_update_notifier=false", "npm_config_fetch_retries=0"}
		out, err := cmd.CombinedOutput()
		commandIndex++
		if evidence := os.Getenv("NPM_MIGRATION_EVIDENCE"); evidence != "" {
			if writeErr := os.WriteFile(filepath.Join(evidence, fmt.Sprintf("official-client-%02d.log", commandIndex)), out, 0600); writeErr != nil {
				t.Fatal(writeErr)
			}
		}
		if err != nil {
			t.Fatalf("official npm %v failed: %v\n%s", args, err, out)
		}
		return out
	}
	if version := strings.TrimSpace(string(command(root, filepath.Join(root, "version-cache"), "--version"))); version != "11.6.2" {
		t.Fatalf("pinned npm 11.6.2 required, got %s", version)
	}
	install := filepath.Join(root, "install")
	if err := os.Mkdir(install, 0700); err != nil {
		t.Fatal(err)
	}
	packageJSON := []byte(`{"name":"migration-reader","version":"1.0.0","dependencies":{"old":"npm:@scope/widget@1.0.0","next":"npm:@scope/widget@next","plain":"npm:widget@latest"}}`)
	if err := os.WriteFile(filepath.Join(install, "package.json"), packageJSON, 0600); err != nil {
		t.Fatal(err)
	}
	output := command(install, filepath.Join(root, "empty-install-cache"), "install", "--package-lock=false")
	for alias, want := range map[string]string{"old": "1.0.0", "next": "2.0.0-beta.1", "plain": "1.0.0"} {
		body, err := os.ReadFile(filepath.Join(install, "node_modules", alias, "package.json"))
		if err != nil {
			t.Fatal(err)
		}
		var got struct{ Version string }
		if json.Unmarshal(body, &got) != nil || got.Version != want {
			t.Fatalf("installed %s identity=%s", alias, body)
		}
	}
	if !bytes.Contains(output, []byte("use next")) {
		t.Fatalf("source deprecation warning was lost: %s", output)
	}
	names := []string{"@scope/widget", "widget"}
	sort.Strings(names)
	for index, name := range names {
		for vi, version := range []string{"1.0.0", "2.0.0-beta.1"} {
			dest := filepath.Join(root, fmt.Sprintf("pack-%d-%d", index, vi))
			if err := os.Mkdir(dest, 0700); err != nil {
				t.Fatal(err)
			}
			command(dest, filepath.Join(dest, "cold-cache"), "pack", name+"@"+version, "--json", "--pack-destination", dest)
			files, err := filepath.Glob(filepath.Join(dest, "*.tgz"))
			if err != nil || len(files) != 1 {
				t.Fatalf("official pack output=%v %v", files, err)
			}
			got, err := os.ReadFile(files[0])
			if err != nil {
				t.Fatal(err)
			}
			var sourcePath string
			for _, e := range f.remote.entries {
				if e.Identity == name {
					for _, a := range e.Assets {
						if a.TargetPath == version {
							sourcePath = a.SourcePath
						}
					}
				}
			}
			want := f.remote.bodies[sourcePath]
			if !bytes.Equal(got, want) || sha256.Sum256(got) != sha256.Sum256(want) {
				t.Fatalf("official npm tarball bytes differ %s@%s", name, version)
			}
			mu.Lock()
			count := requested["/npm/"+f.repo.Name+"/"+sourcePath]
			mu.Unlock()
			if count == 0 {
				t.Fatal("official npm bypassed target-native tarball")
			}
			t.Logf("official npm source-off target-only %s@%s size=%d sha256=%x", name, version, len(got), sha256.Sum256(got))
		}
	}
	t.Logf("npm 11.6.2, cold install/cache per pack, source TLS listener stopped, reopened PostgreSQL store, all four original tarballs and deprecation/tag metadata verified; manifest=%s", b.ManifestSHA256)
}

func TestPostgresNexusNPMMigrationAtomicTargetConflictAndQuota(t *testing.T) {
	for _, mode := range []string{"concurrent-native-publication", "quota-after-verification"} {
		t.Run(mode, func(t *testing.T) {
			f := newNPMMigrationPG(t)
			b := f.start()
			competing := npmFixtureTarball(t, "@scope/widget", "1.0.0")
			intercept := &interceptedNPMMigration{Publication: f.publication, beforeFinal: func(ctx context.Context) error {
				if mode == "quota-after-verification" {
					_, err := f.store.ReplaceRepositoryCapacityQuota(ctx, f.repo.ID, 1)
					return err
				}
				handler := newTestGateway(t, GatewayOptions{Store: f.store, Adapter: TestAdapter{}, Authenticator: testAuthenticator(), Storage: SharedObjectStores(f.objects)})
				request := httptest.NewRequest(http.MethodPut, "/npm/"+f.repo.Name+"/@scope%2Fwidget", strings.NewReader(npmFixturePublishDocument(t, "@scope/widget", "1.0.0", "@scope/widget-1.0.0.tgz", competing)))
				request.Header.Set("Authorization", "Bearer resolver-secret")
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, request)
				if response.Code != http.StatusCreated {
					return fmt.Errorf("native competing publish status=%d", response.Code)
				}
				return nil
			}}
			f.service = nexusmigration.New(migrationpg.New(f.db), f.source, intercept, nexusmigration.Options{TargetID: "npm-fixture-target", PoolID: "npm-fixture-pool"})
			b = f.run(b)
			if b.State == "completed" {
				t.Fatalf("concurrent native decision was ignored=%+v", b)
			}
			if mode == "quota-after-verification" {
				f.noVersions()
				return
			}
			pkg, err := f.store.GetNPMPackage(context.Background(), f.repo.ID, "@scope/widget")
			if err != nil || len(pkg.Versions) != 1 || pkg.Versions[0].Digest != fmt.Sprintf("sha256:%x", sha256.Sum256(competing)) {
				t.Fatalf("migration replaced or extended conflicting native package=%+v %v", pkg, err)
			}
			body, err := f.objects.Get(context.Background(), pkg.Versions[0].ObjectKey)
			if err != nil || !bytes.Equal(body, competing) {
				t.Fatalf("competing native bytes were overwritten: %v", err)
			}
		})
	}
}
