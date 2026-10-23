//go:build integration

package app

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
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
	"testing"
	"time"

	"github.com/ccsert/artifact-gateway/internal/egress"
	"github.com/ccsert/artifact-gateway/internal/nexusmigration"
	migrationpg "github.com/ccsert/artifact-gateway/internal/nexusmigration/pgstore"
	"github.com/ccsert/artifact-gateway/internal/nexussource"
	sourcepg "github.com/ccsert/artifact-gateway/internal/nexussource/pgstore"
	"github.com/ccsert/artifact-gateway/internal/objectstore"
	pypi "github.com/ccsert/artifact-gateway/internal/protocol/pypi"
	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/ccsert/artifact-gateway/internal/secrets"
	"github.com/ccsert/artifact-gateway/internal/testsupport"
	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
)

type pypiMigrationRemote struct {
	t        *testing.T
	mu       sync.Mutex
	files    map[string]pypi.File
	bodies   map[string][]byte
	requests map[string]int
	mutate   func(string, int, []byte) []byte
	denied   bool
	missing  string
}

func (f *pypiMigrationRemote) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	user, password, ok := r.BasicAuth()
	if r.Method != "GET" || !ok || user != "pypi-readonly" || password != "synthetic-pypi-source-secret-461" {
		f.t.Errorf("source boundary method=%s", r.Method)
		w.WriteHeader(403)
		return
	}
	if f.denied {
		w.WriteHeader(403)
		return
	}
	switch r.URL.Path {
	case "/service/rest/v1/repositories":
		_ = json.NewEncoder(w).Encode([]map[string]string{{"name": "source", "format": "pypi", "type": "hosted"}})
		return
	case "/service/rest/v1/repositories/pypi/hosted/source":
		_, _ = io.WriteString(w, `{"name":"source","storage":{"writePolicy":"ALLOW_ONCE"}}`)
		return
	case "/service/rest/v1/components":
		groups := map[string][]map[string]any{}
		for source, file := range f.files {
			if source == f.missing {
				continue
			}
			d, _ := pypi.FilenameIdentity(file.Filename)
			body := f.bodies[source]
			groups[d.Project+"@"+d.Version] = append(groups[d.Project+"@"+d.Version], map[string]any{"repository": "source", "format": "pypi", "path": source, "downloadUrl": "https://example.com/repository/source/" + source, "fileSize": len(body), "checksum": map[string]string{"sha256": fmt.Sprintf("%x", sha256.Sum256(body))}})
		}
		identities := make([]string, 0, len(groups))
		for id := range groups {
			identities = append(identities, id)
		}
		sort.Strings(identities)
		items := []map[string]any{}
		for _, id := range identities {
			name, version, _ := strings.Cut(id, "@")
			sort.Slice(groups[id], func(i, j int) bool { return groups[id][i]["path"].(string) < groups[id][j]["path"].(string) })
			items = append(items, map[string]any{"repository": "source", "format": "pypi", "name": name, "version": version, "assets": groups[id]})
		}
		offset, _ := strconv.Atoi(r.URL.Query().Get("continuationToken"))
		end := min(offset+2, len(items))
		var next any
		if end < len(items) {
			next = strconv.Itoa(end)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"items": items[offset:end], "continuationToken": next})
		return
	}
	if !strings.HasPrefix(r.URL.Path, "/repository/source/") {
		f.t.Errorf("source path outside boundary: %s", r.URL.Path)
		w.WriteHeader(403)
		return
	}
	p := strings.TrimPrefix(r.URL.Path, "/repository/source/")
	body := bytes.Clone(f.bodies[p])
	if strings.HasPrefix(p, "simple/") {
		project := strings.TrimSuffix(strings.TrimPrefix(p, "simple/"), "/")
		names := make([]string, 0, len(f.files))
		for name := range f.files {
			names = append(names, name)
		}
		sort.Strings(names)
		var b strings.Builder
		b.WriteString("<!DOCTYPE html><html><body>\n")
		for _, name := range names {
			file := f.files[name]
			d, _ := pypi.FilenameIdentity(file.Filename)
			if d.Project != project {
				continue
			}
			fmt.Fprintf(&b, `<a href="../../%s#sha256=%s" data-requires-python="%s"`, name, file.SHA256, html.EscapeString(file.RequiresPython))
			if file.Yanked {
				fmt.Fprintf(&b, ` data-yanked="%s"`, html.EscapeString(file.YankReason))
			}
			fmt.Fprintf(&b, ">%s</a>\n", file.Filename)
		}
		b.WriteString("</body></html>")
		body = []byte(b.String())
		w.Header().Set("Content-Type", "text/html")
	}
	if len(body) == 0 {
		w.WriteHeader(404)
		return
	}
	f.requests[p]++
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
	_, _ = w.Write(body)
}
func pypiMigrationWheel(t *testing.T, name, version, requires, dependency string) []byte {
	t.Helper()
	dist := strings.ReplaceAll(name, "-", "_")
	root := dist + "-" + version + ".dist-info/"
	metadata := "Metadata-Version: 2.1\nName: " + name + "\nVersion: " + version + "\nRequires-Python: " + requires + "\n"
	if dependency != "" {
		metadata += "Requires-Dist: " + dependency + "\n"
	}
	metadata += "\n"
	files := map[string]string{root + "METADATA": metadata, root + "WHEEL": "Wheel-Version: 1.0\nGenerator: ag461-owned-fixture\nRoot-Is-Purelib: true\nTag: py3-none-any\n", dist + "/__init__.py": "VALUE = '" + version + "'\n"}
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	var record strings.Builder
	for _, n := range names {
		sum := sha256.Sum256([]byte(files[n]))
		fmt.Fprintf(&record, "%s,sha256=%s,%d\n", n, base64.RawURLEncoding.EncodeToString(sum[:]), len(files[n]))
	}
	record.WriteString(root + "RECORD,,\n")
	files[root+"RECORD"] = record.String()
	names = append(names, root+"RECORD")
	var out bytes.Buffer
	z := zip.NewWriter(&out)
	for _, n := range names {
		w, err := z.Create(n)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = io.WriteString(w, files[n]); err != nil {
			t.Fatal(err)
		}
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}
func pypiMigrationSDist(t *testing.T, name, version string) []byte {
	t.Helper()
	var out bytes.Buffer
	gz := gzip.NewWriter(&out)
	tw := tar.NewWriter(gz)
	body := "Metadata-Version: 2.1\nName: " + name + "\nVersion: " + version + "\nRequires-Python: >=3.8\n\n"
	if err := tw.WriteHeader(&tar.Header{Name: name + "-" + version + "/PKG-INFO", Mode: 0o600, Size: int64(len(body))}); err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(tw, body); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

type pypiMigrationPG struct {
	t                                   *testing.T
	db                                  *sql.DB
	store                               *repository.PostgresStore
	objects                             *objectstore.RustFSStore
	repo                                repository.HostedRepository
	source                              *nexussource.Service
	created                             nexussource.Source
	remote                              *pypiMigrationRemote
	server                              *httptest.Server
	service                             *nexusmigration.Service
	publication                         *NexusMigrationPublication
	actor, keyID, keyToken, databaseURL string
}

func newPyPIMigrationPG(t *testing.T) *pypiMigrationPG {
	t.Helper()
	databaseURL, endpoint := os.Getenv("TEST_DATABASE_URL"), os.Getenv("TEST_RUSTFS_ENDPOINT")
	if databaseURL == "" || endpoint == "" {
		if os.Getenv("PYPI_MIGRATION_REQUIRED") == "1" {
			t.Fatal("mandatory PyPI proof needs owned PostgreSQL/RustFS")
		}
		t.Skip("owned PostgreSQL/RustFS required")
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
	objects, err := objectstore.NewRustFSStore(endpoint, os.Getenv("TEST_RUSTFS_ACCESS_KEY"), os.Getenv("TEST_RUSTFS_SECRET_KEY"), "pypi-migration-"+strings.ReplaceAll(uuid.NewString(), "-", ""))
	if err != nil {
		t.Fatal(err)
	}
	if err = objects.EnsureBucket(context.Background()); err != nil {
		t.Fatal(err)
	}
	repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "pypi-mig-" + uuid.NewString(), Format: repository.FormatPyPI})
	if err != nil {
		t.Fatal(err)
	}
	bootstrap := newTestGateway(t, GatewayOptions{Store: store, Adapter: TestAdapter{}, Authenticator: Authenticator{AdminToken: "owned-bootstrap", AdminActor: "owned-bootstrap", APIKeys: store}})
	req := httptest.NewRequest("POST", "/api/v2/api-keys", strings.NewReader(`{"name":"owned-pypi-import","roles":["admin"]}`))
	req.Header.Set("Authorization", "Bearer owned-bootstrap")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	bootstrap.ServeHTTP(rec, req)
	var key struct{ ID, Token string }
	if rec.Code != 201 || json.Unmarshal(rec.Body.Bytes(), &key) != nil || key.ID == "" {
		t.Fatalf("bootstrap managed admin key status=%d", rec.Code)
	}
	remote := &pypiMigrationRemote{t: t, files: map[string]pypi.File{}, bodies: map[string][]byte{}, requests: map[string]int{}}
	add := func(filename, requires string, yank bool, body []byte) {
		source := "packages/" + filename
		remote.bodies[source] = body
		reason := ""
		if yank {
			reason = "synthetic withdrawn release"
		}
		remote.files[source] = pypi.File{Filename: filename, SourcePath: source, SHA256: fmt.Sprintf("%x", sha256.Sum256(body)), RequiresPython: requires, Yanked: yank, YankReason: reason}
	}
	for _, version := range []string{"1.0.0", "2.0.0", "3.0.0"} {
		requires := ">=3.8"
		if version == "2.0.0" {
			requires = ">=99"
		}
		add("ag461_widget-"+version+"-py3-none-any.whl", requires, version == "3.0.0", pypiMigrationWheel(t, "ag461-widget", version, requires, "ag461-dependency==1.0.0"))
	}
	add("ag461-widget-1.0.0.tar.gz", ">=3.8", false, pypiMigrationSDist(t, "ag461-widget", "1.0.0"))
	add("ag461_dependency-1.0.0-py3-none-any.whl", ">=3.8", false, pypiMigrationWheel(t, "ag461-dependency", "1.0.0", ">=3.8", ""))
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
	created, err := source.Create(context.Background(), nexussource.Input{Name: "owned-pypi-source", BaseURL: "https://example.com", Credentials: &nexussource.Credentials{Username: "pypi-readonly", Password: "synthetic-pypi-source-secret-461"}})
	if err != nil {
		t.Fatal(err)
	}
	f := &pypiMigrationPG{t: t, db: db, store: store, objects: objects, repo: repo, source: source, created: created, remote: remote, server: server, actor: "api-key:" + key.ID, keyID: key.ID, keyToken: key.Token, databaseURL: databaseURL}
	f.reset(nexusmigration.Options{})
	t.Cleanup(func() {
		_, err := db.Exec(`UPDATE nexus_migration_batches SET state='cancelled',lease_token='',lease_expires_at=NULL,cancel_requested=true WHERE source_id=$1 AND state NOT IN ('completed','cancelled')`, created.ID)
		if err != nil {
			t.Errorf("owned PyPI batch cleanup: %v", err)
		}
	})
	return f
}
func pypiMigrationEvidence() nexusmigration.CapacityEvidence {
	zero, free, download := int64(0), int64(1<<30), int64(16<<20)
	now := time.Now().UTC()
	return nexusmigration.CapacityEvidence{TargetID: "pypi-fixture-target", PoolID: "pypi-fixture-pool", ObservedAt: now.Add(-time.Second), ValidUntil: now.Add(30 * time.Minute), FreeBytes: &free, DownloadBytes: &download, UploadBytes: &zero, BackupBytes: &zero, RestoreBytes: &zero, HeadroomBytes: &zero}
}
func (f *pypiMigrationPG) reset(options nexusmigration.Options) {
	options.TargetID, options.PoolID = "pypi-fixture-target", "pypi-fixture-pool"
	f.publication = NewNexusMigrationPublication(f.store, SharedObjectStores(f.objects), options.TargetID, options.PoolID).WithPyPI(NewNexusPyPIMigrationPublication(f.store, f.objects))
	f.service = nexusmigration.New(migrationpg.New(f.db), f.source, f.publication, options)
}
func (f *pypiMigrationPG) input() nexusmigration.StartInput {
	return nexusmigration.StartInput{SourceID: f.created.ID, SourceVersion: f.created.Version, PolicyVersion: nexusmigration.PolicyVersion, Mappings: []nexusmigration.Mapping{{SourceRepository: "source", TargetRepositoryID: f.repo.ID}}, CapacityEvidence: pypiMigrationEvidence()}
}
func (f *pypiMigrationPG) start() nexusmigration.Batch {
	f.t.Helper()
	b, err := f.service.Start(context.Background(), f.input(), f.actor, uuid.NewString())
	if err != nil {
		f.t.Fatal(err)
	}
	return b
}
func (f *pypiMigrationPG) run(b nexusmigration.Batch) nexusmigration.Batch {
	f.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := f.service.RunOnce(ctx, []string{"pypi"}); err != nil {
		f.t.Fatal(err)
	}
	b, err := f.service.Get(context.Background(), b.ID)
	if err != nil {
		f.t.Fatal(err)
	}
	return b
}
func (f *pypiMigrationPG) reopen() {
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
func (f *pypiMigrationPG) noPublication() {
	f.t.Helper()
	for _, q := range []string{`SELECT count(*) FROM native_pypi_files WHERE repository_id=$1`, `SELECT count(*) FROM resolver_audit_log WHERE repository=$1 AND operation='publish'`} {
		arg := f.repo.ID
		if strings.Contains(q, "resolver_audit") {
			arg = f.repo.Name
		}
		var n int
		if err := f.db.QueryRow(q, arg).Scan(&n); err != nil || n != 0 {
			f.t.Fatalf("partial publication n=%d err=%v", n, err)
		}
	}
}

func TestPostgresRustFSNexusPyPIMigrationClosureAndRestart(t *testing.T) {
	f := newPyPIMigrationPG(t)
	handler := newTestGateway(t, GatewayOptions{Dependencies: Dependencies{MigrationService: f.service}, Store: f.store, Adapter: TestAdapter{}, Authenticator: Authenticator{APIKeys: f.store}})
	raw, _ := json.Marshal(f.input())
	request := httptest.NewRequest("POST", "/api/v2/migrations/nexus/batches", bytes.NewReader(raw))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+f.keyToken)
	request.Header.Set("Idempotency-Key", uuid.NewString())
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	var started nexusmigration.Batch
	if response.Code != 202 || json.Unmarshal(response.Body.Bytes(), &started) != nil || started.ID == "" {
		t.Fatalf("managed admin HTTP start status=%d body=%s", response.Code, response.Body.String())
	}
	b := f.run(started)
	if b.State != "completed" || b.VisibleIdentities != 4 {
		t.Fatalf("complete closure=%+v", b)
	}
	var expected int64
	for _, body := range f.remote.bodies {
		expected += int64(len(body))
	}
	if b.TotalBytes == nil || *b.TotalBytes != expected || b.VerifiedBytes != expected || b.TransferredBytes != expected {
		t.Fatalf("byte receipts=%+v want=%d", b, expected)
	}
	f.reopen()
	entries, _, err := f.service.Entries(context.Background(), b.ID, 100, "")
	if err != nil || len(entries) != 4 {
		t.Fatalf("durable entries=%d %v", len(entries), err)
	}
	for _, e := range entries {
		e.Actor = f.actor
		if state, err := f.publication.Readback(context.Background(), e); err != nil || state != nexusmigration.Identical {
			t.Fatalf("reopened=%s %v", state, err)
		}
	}
	for source, expected := range f.remote.files {
		got, err := f.store.GetPyPIFile(context.Background(), f.repo.ID, expected.Filename)
		if err != nil || got.Yanked != expected.Yanked || got.YankReason != expected.YankReason || got.RequiresPython != expected.RequiresPython {
			t.Fatalf("selection %s=%+v %v", source, got, err)
		}
		data, err := f.objects.Get(context.Background(), got.ObjectKey)
		if err != nil || !bytes.Equal(data, f.remote.bodies[source]) {
			t.Fatalf("target bytes %s %v", source, err)
		}
	}
	repeated := f.run(f.start())
	if repeated.State != "completed" || repeated.Counts.Skipped != 4 {
		t.Fatalf("idempotent replay=%+v", repeated)
	}
	var audits int
	if err = f.db.QueryRow(`SELECT count(*) FROM resolver_audit_log WHERE repository=$1 AND operation='publish'`, f.repo.Name).Scan(&audits); err != nil || audits != 4 {
		t.Fatalf("duplicate audits=%d %v", audits, err)
	}
	t.Logf("five full files/four version closures persisted; source metadata excluded from target bytes=%d; restart/idempotency intact", expected)
}
func TestPostgresRustFSNexusPyPIMigrationRecovery(t *testing.T) {
	for _, phase := range []string{"upload", "verify", "publish", "transport-ack"} {
		t.Run(phase, func(t *testing.T) {
			f := newPyPIMigrationPG(t)
			fired := false
			fault := func(context.Context, nexusmigration.Entry) error {
				if !fired {
					fired = true
					return nexusmigration.Failed("fixture_ack_lost")
				}
				return nil
			}
			options := nexusmigration.Options{}
			switch phase {
			case "upload":
				options.AfterUpload = fault
			case "verify":
				options.AfterVerify = fault
			case "publish":
				options.AfterPublish = fault
			}
			f.reset(options)
			batch := f.start()
			if phase == "transport-ack" {
				f.service = nexusmigration.New(migrationpg.New(f.db), f.source, &pypiAckLossPublication{NexusMigrationPublication: f.publication, fired: &fired}, nexusmigration.Options{TargetID: "pypi-fixture-target", PoolID: "pypi-fixture-pool"})
			}
			b := f.run(batch)
			if b.State != "failed" || !fired {
				t.Fatalf("fault=%+v", b)
			}
			if phase == "publish" || phase == "transport-ack" {
				var rows int
				err := f.db.QueryRow(`SELECT count(*) FROM nexus_migration_entries WHERE batch_id=$1 AND state='committed' AND visible_identities=1`, b.ID).Scan(&rows)
				want := 1
				if phase == "transport-ack" {
					want = 4
				}
				if err != nil || rows != want {
					t.Fatalf("commit ACK loss lost atomic progress=%d want=%d %v", rows, want, err)
				}
				var audits int
				if err = f.db.QueryRow(`SELECT count(*) FROM resolver_audit_log WHERE repository=$1 AND operation='publish'`, f.repo.Name).Scan(&audits); err != nil || audits != want {
					t.Fatalf("atomic progress/audit mismatch=%d want=%d %v", audits, want, err)
				}
			}
			f.reopen()
			evidence := pypiMigrationEvidence()
			var err error
			b, err = f.service.Control(context.Background(), b.ID, b.Version, f.actor, uuid.NewString(), "retry", &evidence)
			if err != nil {
				t.Fatal(err)
			}
			b = f.run(b)
			if b.State != "completed" || b.VisibleIdentities != 4 {
				t.Fatalf("recovery=%+v", b)
			}
		})
	}
}
func TestPostgresNexusPyPIMigrationRejectsIncompleteAndDrift(t *testing.T) {
	for _, mode := range []string{"missing-file", "wrong-bytes", "wrong-archive", "selection-drift", "requires-python-mismatch", "foreign-url"} {
		t.Run(mode, func(t *testing.T) {
			f := newPyPIMigrationPG(t)
			switch mode {
			case "missing-file":
				f.remote.missing = "packages/ag461-widget-1.0.0.tar.gz"
			case "wrong-bytes":
				f.remote.mutate = func(p string, _ int, b []byte) []byte {
					if strings.HasPrefix(p, "packages/") {
						b[0] ^= 1
					}
					return b
				}
			case "wrong-archive":
				p := "packages/ag461_dependency-1.0.0-py3-none-any.whl"
				body := pypiMigrationWheel(t, "wrong-identity", "1.0.0", ">=3.8", "")
				f.remote.bodies[p] = body
				file := f.remote.files[p]
				file.SHA256 = fmt.Sprintf("%x", sha256.Sum256(body))
				f.remote.files[p] = file
			case "selection-drift":
				f.remote.mutate = func(p string, n int, b []byte) []byte {
					if strings.HasPrefix(p, "simple/") && n >= 2 {
						return bytes.ReplaceAll(b, []byte("&gt;=3.8"), []byte("&gt;=3.9"))
					}
					return b
				}
			case "requires-python-mismatch":
				p := "packages/ag461_dependency-1.0.0-py3-none-any.whl"
				file := f.remote.files[p]
				file.RequiresPython = ">=99"
				f.remote.files[p] = file
			case "foreign-url":
				f.remote.mutate = func(p string, _ int, b []byte) []byte {
					if strings.HasPrefix(p, "simple/") {
						return bytes.ReplaceAll(b, []byte("../../packages/"), []byte("https://foreign.invalid/packages/"))
					}
					return b
				}
			}
			b := f.run(f.start())
			if b.State == "completed" || b.VisibleIdentities != 0 {
				t.Fatalf("invalid source accepted=%+v", b)
			}
			f.noPublication()
		})
	}
}

func TestPostgresNexusPyPIMigrationCurrentAdministrator(t *testing.T) {
	for _, mode := range []string{"static-start", "unknown-start", "revoked", "expired", "demoted", "user-disabled", "user-demoted", "user-password-reset", "source-credential"} {
		t.Run(mode, func(t *testing.T) {
			f := newPyPIMigrationPG(t)
			if strings.HasSuffix(mode, "-start") {
				actor := "owned-bootstrap"
				if mode == "unknown-start" {
					actor = "user:missing"
				}
				if _, err := f.service.Start(context.Background(), f.input(), actor, uuid.NewString()); err == nil {
					t.Fatal("non-durable actor created PyPI batch")
				}
				var count int
				if err := f.db.QueryRow(`SELECT count(*) FROM nexus_migration_batches WHERE source_id=$1`, f.created.ID).Scan(&count); err != nil || count != 0 {
					t.Fatalf("rejected actor wrote batch=%d %v", count, err)
				}
				return
			}
			userID := ""
			if strings.HasPrefix(mode, "user-") {
				user, err := f.store.CreateUser(context.Background(), repository.User{ID: uuid.NewString(), Name: "pypi-owner-" + uuid.NewString(), Role: "admin"})
				if err != nil {
					t.Fatal(err)
				}
				userID = user.ID
				f.actor = "user:" + user.Name
			}
			fired := false
			f.reset(nexusmigration.Options{AfterVerify: func(ctx context.Context, _ nexusmigration.Entry) error {
				if fired {
					return nil
				}
				fired = true
				var err error
				switch mode {
				case "revoked":
					_, err = f.store.RevokeAPIKey(ctx, f.keyID)
				case "expired":
					_, err = f.db.ExecContext(ctx, `UPDATE api_keys SET expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`, f.keyID)
				case "demoted":
					_, err = f.db.ExecContext(ctx, `UPDATE api_keys SET roles=ARRAY['member']::text[] WHERE id=$1`, f.keyID)
				case "user-disabled":
					_, err = f.db.ExecContext(ctx, `UPDATE users SET state='disabled' WHERE id=$1`, userID)
				case "user-demoted":
					_, err = f.db.ExecContext(ctx, `UPDATE users SET role='member' WHERE id=$1`, userID)
				case "user-password-reset":
					_, err = f.db.ExecContext(ctx, `UPDATE users SET must_change_password=true WHERE id=$1`, userID)
				case "source-credential":
					f.remote.mu.Lock()
					f.remote.denied = true
					f.remote.mu.Unlock()
				}
				return err
			}})
			b := f.run(f.start())
			if b.State == "completed" || !fired || b.VisibleIdentities != 0 {
				t.Fatalf("revoked authority published=%+v", b)
			}
			f.noPublication()
		})
	}
}
func TestPostgresNexusPyPIMigrationLeaseAndSourceFence(t *testing.T) {
	for _, mode := range []string{"cancel", "lease-expired", "source-version"} {
		t.Run(mode, func(t *testing.T) {
			f := newPyPIMigrationPG(t)
			b := f.start()
			intercept := &interceptedNPMMigration{Publication: f.publication}
			intercept.beforeFinal = func(ctx context.Context) error {
				f.noPublication()
				switch mode {
				case "cancel":
					current, err := f.service.Get(ctx, b.ID)
					if err != nil {
						return err
					}
					_, err = f.service.Control(ctx, b.ID, current.Version, f.actor, uuid.NewString(), "cancel", nil)
					return err
				case "lease-expired":
					_, err := f.db.ExecContext(ctx, `UPDATE nexus_migration_batches SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`, b.ID)
					return err
				default:
					_, err := f.source.Update(ctx, f.created.ID, nexussource.Input{Name: f.created.Name, BaseURL: f.created.BaseURL}, f.created.Version)
					return err
				}
			}
			f.service = nexusmigration.New(migrationpg.New(f.db), f.source, intercept, nexusmigration.Options{TargetID: "pypi-fixture-target", PoolID: "pypi-fixture-pool", HeartbeatInterval: time.Hour})
			b = f.run(b)
			f.noPublication()
			if b.VisibleIdentities != 0 {
				t.Fatalf("stale owner visible=%+v", b)
			}
			if mode == "lease-expired" {
				f.reset(nexusmigration.Options{})
				b = f.run(b)
				if b.State != "completed" || b.VisibleIdentities != 4 {
					t.Fatalf("new owner recovery=%+v", b)
				}
			}
		})
	}
}
func TestPostgresNexusPyPIMigrationAtomicConflictQuotaAudit(t *testing.T) {
	for _, mode := range []string{"conflict", "quota", "audit-failure", "quarantine", "tombstone", "yank-change"} {
		t.Run(mode, func(t *testing.T) {
			f := newPyPIMigrationPG(t)
			// One two-file version makes partial publication observable inside the closure.
			for p, file := range f.remote.files {
				if !strings.HasPrefix(file.Filename, "ag461-widget-1.0.0") && !strings.HasPrefix(file.Filename, "ag461_widget-1.0.0") {
					delete(f.remote.files, p)
					delete(f.remote.bodies, p)
				}
			}

			if mode == "audit-failure" {
				name := "ag461_audit_" + strings.ReplaceAll(uuid.NewString(), "-", "")
				_, err := f.db.Exec(`CREATE FUNCTION ` + name + `() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN IF NEW.repository='` + f.repo.Name + `' AND NEW.operation='publish' THEN RAISE EXCEPTION 'owned audit failure'; END IF;RETURN NEW;END$$;CREATE TRIGGER ` + name + ` BEFORE INSERT ON resolver_audit_log FOR EACH ROW EXECUTE FUNCTION ` + name + `()`)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					_, err := f.db.Exec(`DROP TRIGGER IF EXISTS ` + name + ` ON resolver_audit_log;DROP FUNCTION IF EXISTS ` + name + `() `)
					if err != nil {
						t.Error(err)
					}
				})
			}
			fired := false
			f.reset(nexusmigration.Options{AfterVerify: func(ctx context.Context, e nexusmigration.Entry) error {
				if fired {
					return nil
				}
				fired = true
				if mode == "quota" {
					_, err := f.store.ReplaceRepositoryCapacityQuota(ctx, f.repo.ID, 1)
					return err
				}
				if mode == "conflict" {
					file := e.Assets[len(e.Assets)-1]
					d, _ := pypi.FilenameIdentity(file.TargetPath)
					_, err := f.store.PublishPyPIFile(ctx, repository.PyPIFile{RepositoryID: f.repo.ID, Project: d.Project, Version: d.Version, Filename: file.TargetPath, FileType: d.FileType, PythonVersion: d.PythonVersion, Digest: "sha256:" + strings.Repeat("f", 64), ObjectKey: "owned-conflicting-key", Size: file.Size})
					return err
				}
				if mode == "quarantine" {
					a := e.Assets[len(e.Assets)-1]
					_, err := f.store.ReplaceArtifactQuarantine(ctx, repository.ArtifactQuarantine{RepositoryID: f.repo.ID, Format: repository.FormatPyPI, Coordinate: e.Identity, Digest: "sha256:" + a.SHA256, State: repository.ArtifactQuarantineStateQuarantined, Reason: "owned migration fixture", UpdatedBy: f.actor}, "0")
					return err
				}
				if mode == "tombstone" {
					_, err := f.db.ExecContext(ctx, `INSERT INTO artifact_tombstones(repository_id,format,coordinate,tombstoned_at) VALUES($1,'pypi',$2,now())`, f.repo.ID, e.Identity)
					return err
				}
				return nil
			}})
			if mode == "yank-change" {
				f.reset(nexusmigration.Options{})
				seed := f.run(f.start())
				if seed.State != "completed" {
					t.Fatalf("seed=%+v", seed)
				}
				if _, err := f.db.Exec(`UPDATE native_pypi_files SET yanked=true,yank_reason='target owner choice',yank_revision=yank_revision+1 WHERE repository_id=$1`, f.repo.ID); err != nil {
					t.Fatal(err)
				}
			}
			b := f.run(f.start())
			if b.State == "completed" || b.VisibleIdentities != 0 {
				t.Fatalf("unsafe target accepted=%+v", b)
			}
			var rows, audits int
			if err := f.db.QueryRow(`SELECT count(*) FROM native_pypi_files WHERE repository_id=$1`, f.repo.ID).Scan(&rows); err != nil {
				t.Fatal(err)
			}
			if err := f.db.QueryRow(`SELECT count(*) FROM resolver_audit_log WHERE repository=$1 AND operation='publish'`, f.repo.Name).Scan(&audits); err != nil {
				t.Fatal(err)
			}
			want := 0
			if mode == "conflict" {
				want = 1
			}
			if mode == "yank-change" {
				want = 2
			}
			if rows != want || audits != map[bool]int{true: 1, false: 0}[mode == "yank-change"] {
				t.Fatalf("partial closure rows=%d audits=%d", rows, audits)
			}
		})
	}
}
func TestPostgresMigrationTargetOnlyPyPIClient(t *testing.T) {
	python := os.Getenv("PYPI_MIGRATION_PYTHON")
	if python == "" {
		if os.Getenv("PYPI_MIGRATION_REQUIRED") == "1" {
			t.Fatal("pinned pip required")
		}
		t.Skip("set PYPI_MIGRATION_PYTHON")
	}
	f := newPyPIMigrationPG(t)
	b := f.run(f.start())
	if b.State != "completed" {
		t.Fatalf("migration=%+v", b)
	}
	f.server.Close()
	handler := newTestGateway(t, GatewayOptions{Store: f.store, Adapter: TestAdapter{}, Authenticator: testAuthenticator(), Storage: SharedObjectStores(f.objects)})
	var mu sync.Mutex
	requests := map[string]int{}
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests[r.URL.Path]++
		mu.Unlock()
		handler.ServeHTTP(w, r)
	}))
	defer target.Close()
	root := t.TempDir()
	cache := filepath.Join(root, "empty-cache")
	downloads := filepath.Join(root, "downloads")
	installed := filepath.Join(root, "installed")
	if err := os.MkdirAll(cache, 0o700); err != nil {
		t.Fatal(err)
	}
	index := strings.Replace(target.URL, "http://", "http://build-agent:resolver-secret@", 1) + "/pypi/" + f.repo.Name + "/simple/"
	env := []string{}
	for _, v := range os.Environ() {
		key, _, _ := strings.Cut(v, "=")
		if strings.HasPrefix(key, "PIP_") || strings.HasPrefix(key, "PYTHON") || strings.HasSuffix(key, "_PROXY") || strings.HasSuffix(key, "_proxy") || key == "HOME" || key == "XDG_CONFIG_HOME" || key == "XDG_CACHE_HOME" {
			continue
		}
		env = append(env, v)
	}
	env = append(env, "HOME="+root, "PIP_CONFIG_FILE="+os.DevNull, "PIP_CACHE_DIR="+cache, "PIP_DISABLE_PIP_VERSION_CHECK=1", "PYTHONNOUSERSITE=1", "XDG_CONFIG_HOME="+root, "XDG_CACHE_HOME="+cache)
	run := func(label string, args ...string) string {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, python, args...)
		cmd.Dir = root
		cmd.Env = env
		raw, err := cmd.CombinedOutput()
		output := strings.ReplaceAll(string(raw), "resolver-secret", "[redacted]")
		if dir := os.Getenv("PYPI_MIGRATION_EVIDENCE"); dir != "" {
			if e := os.WriteFile(filepath.Join(dir, label+".log"), []byte(output), 0o600); e != nil {
				t.Fatal(e)
			}
		}
		if err != nil {
			t.Fatalf("%s: %v\n%s", label, err, output)
		}
		return output
	}
	version := run("pip-version", "-c", "import importlib.metadata; print(importlib.metadata.version('pip'))")
	if strings.TrimSpace(version) != "26.2" {
		t.Fatalf("unexpected pip=%s", version)
	}
	run("pip-target-download", "-m", "pip", "--isolated", "--disable-pip-version-check", "--no-cache-dir", "--timeout", "5", "--retries", "0", "download", "--only-binary=:all:", "--index-url", index, "--dest", downloads, "ag461-widget")
	files, err := os.ReadDir(downloads)
	if err != nil || len(files) != 2 {
		t.Fatalf("dependency closure count=%d %v", len(files), err)
	}
	for _, file := range files {
		body, err := os.ReadFile(filepath.Join(downloads, file.Name()))
		source := "packages/" + file.Name()
		if err != nil || !bytes.Equal(body, f.remote.bodies[source]) || !strings.Contains(file.Name(), "-1.0.0-") {
			t.Fatalf("pip selected wrong/corrupt distribution %s", file.Name())
		}
		sum := sha256.Sum256(body)
		t.Logf("pip cold bytes %s sha256=%s", file.Name(), hex.EncodeToString(sum[:]))
	}
	run("pip-offline-install", "-m", "pip", "--isolated", "--disable-pip-version-check", "--no-cache-dir", "install", "--no-index", "--find-links", downloads, "--target", installed, "ag461-widget==1.0.0")
	run("pip-offline-import", "-c", "import sys; sys.path.insert(0, "+strconv.Quote(installed)+"); import ag461_widget, ag461_dependency; assert ag461_widget.VALUE == ag461_dependency.VALUE == '1.0.0'; print('offline dependency import passed')")
	sdist := "ag461-widget-1.0.0.tar.gz"
	request, _ := http.NewRequest("GET", target.URL+"/pypi/"+f.repo.Name+"/packages/"+sdist, nil)
	request.SetBasicAuth("build-agent", "resolver-secret")
	response, err := target.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	data, readErr := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if readErr != nil || response.StatusCode != 200 || !bytes.Equal(data, f.remote.bodies["packages/"+sdist]) {
		t.Fatal("sdist original bytes not retained")
	}
	mu.Lock()
	defer mu.Unlock()
	for _, name := range []string{"ag461_widget-1.0.0-py3-none-any.whl", "ag461_dependency-1.0.0-py3-none-any.whl", sdist} {
		if requests["/pypi/"+f.repo.Name+"/packages/"+name] < 1 {
			t.Fatalf("missing actual native GET %s", name)
		}
	}
	t.Log("source closed; pinned pip cold resolution skipped yanked 3.0 and Python-ineligible 2.0, fetched widget+dependency 1.0, installed/imported offline; sdist bytes verified separately without building source")
}

func TestPostgresNexusPyPIMigrationNativeTransactionLossCannotPublish(t *testing.T) {
	f := newPyPIMigrationPG(t)
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
	service := nexusmigration.New(migrationpg.New(f.db), f.source, intercept, nexusmigration.Options{TargetID: "pypi-fixture-target", PoolID: "pypi-fixture-pool", HeartbeatInterval: time.Hour})
	done := make(chan error, 1)
	go func() { done <- service.RunOnce(ctx, []string{"pypi"}) }()
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
	if _, err = service.Control(ctx, b.ID, current.Version, f.actor, uuid.NewString(), "cancel", nil); err != nil {
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
	f.noPublication()
}

type pypiAckLossPublication struct {
	*NexusMigrationPublication
	fired *bool
}

func (p *pypiAckLossPublication) Publish(ctx context.Context, e nexusmigration.Entry) error {
	if err := p.NexusMigrationPublication.Publish(ctx, e); err != nil {
		return err
	}
	if !*p.fired {
		*p.fired = true
		return nexusmigration.Failed("fixture_commit_ack_lost")
	}
	return nil
}
