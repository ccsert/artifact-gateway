//go:build integration

package pgstore_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ccsert/artifact-gateway/internal/app"
	"github.com/ccsert/artifact-gateway/internal/egress"
	m "github.com/ccsert/artifact-gateway/internal/nexusmigration"
	"github.com/ccsert/artifact-gateway/internal/nexusmigration/pgstore"
	"github.com/ccsert/artifact-gateway/internal/nexussource"
	sourcepg "github.com/ccsert/artifact-gateway/internal/nexussource/pgstore"
	"github.com/ccsert/artifact-gateway/internal/objectstore"
	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/ccsert/artifact-gateway/internal/testsupport"
	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
)

type sourceFixture struct {
	mu                sync.Mutex
	files             map[string][]byte
	format            string
	gets              map[string]int
	change            func(string, int, []byte) ([]byte, int, string)
	failScan          bool
	forceProbe        bool
	stream            bool
	metadataSizes     map[string]int64
	duplicateMetadata bool
}

func (f *sourceFixture) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		w.WriteHeader(500)
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	switch r.URL.Path {
	case "/service/rest/v1/repositories":
		_ = json.NewEncoder(w).Encode([]map[string]string{{"name": "source", "format": f.format, "type": "hosted"}})
	case "/service/rest/v1/repositories/raw/hosted/source":
		_, _ = io.WriteString(w, `{"name":"source"}`)
	case "/service/rest/v1/repositories/maven/hosted/source":
		_, _ = io.WriteString(w, `{"name":"source","maven":{"versionPolicy":"RELEASE"}}`)
	case "/service/rest/v1/assets":
		if f.failScan {
			w.WriteHeader(503)
			return
		}
		paths := make([]string, 0, len(f.files))
		for p := range f.files {
			paths = append(paths, p)
		}
		sort.Strings(paths)
		offset, _ := strconv.Atoi(r.URL.Query().Get("continuationToken"))
		end := offset + 2
		if end > len(paths) {
			end = len(paths)
		}
		items := []map[string]any{}
		for _, p := range paths[offset:end] {
			sum := sha256.Sum256(f.files[p])
			checksums := map[string]string{"sha256": hex.EncodeToString(sum[:])}
			if f.forceProbe {
				checksums = map[string]string{}
			}
			size := int64(len(f.files[p]))
			if override, ok := f.metadataSizes[p]; ok {
				size = override
			}
			items = append(items, map[string]any{"repository": "source", "format": f.format, "path": p, "downloadUrl": "https://example.com/repository/source/" + p, "fileSize": size, "checksum": checksums})
		}
		if f.duplicateMetadata && len(items) > 0 {
			items = append(items, items[0])
		}
		var next any
		if end < len(paths) {
			next = strconv.Itoa(end)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"items": items, "continuationToken": next})
	default:
		p := strings.TrimPrefix(r.URL.Path, "/repository/source/")
		data, ok := f.files[p]
		if !ok {
			w.WriteHeader(404)
			return
		}
		f.gets[p]++
		sum := sha256.Sum256(data)
		etag := `"` + hex.EncodeToString(sum[:]) + `"`
		status := 200
		if f.change != nil {
			data, status, etag = f.change(p, f.gets[p], data)
		}
		if status != 200 {
			w.WriteHeader(status)
			return
		}
		w.Header().Set("ETag", etag)
		if condition := r.Header.Get("If-Match"); condition != "" && condition != etag {
			w.WriteHeader(412)
			return
		}
		if f.stream {
			w.(http.Flusher).Flush()
		}
		_, _ = w.Write(data)
	}
}

type fixture struct {
	closeSource func()
	t           *testing.T
	db          *sql.DB
	native      *repository.PostgresStore
	objects     *objectstore.RustFSStore
	ledger      *pgstore.Store
	source      *nexussource.Service
	sourceID    string
	remote      *sourceFixture
	repo        repository.HostedRepository
	publication *app.NexusMigrationPublication
	service     *m.Service
	input       m.StartInput
	connection  string
}

func newFixture(t *testing.T, format string, files map[string][]byte) *fixture {
	t.Helper()
	connection, endpoint := os.Getenv("TEST_DATABASE_URL"), os.Getenv("TEST_RUSTFS_ENDPOINT")
	if connection == "" || endpoint == "" {
		t.Skip("isolated PostgreSQL and RustFS required")
	}
	if err := testsupport.ValidateIsolatedDatabaseURL(connection); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("pgx", connection)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	native, err := repository.NewPostgresStore(connection)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = native.Close() })
	bucket := "migration-" + strings.ReplaceAll(uuid.NewString(), "-", "")
	objects, err := objectstore.NewRustFSStore(endpoint, os.Getenv("TEST_RUSTFS_ACCESS_KEY"), os.Getenv("TEST_RUSTFS_SECRET_KEY"), bucket)
	if err != nil {
		t.Fatal(err)
	}
	if err = objects.EnsureBucket(context.Background()); err != nil {
		t.Fatal(err)
	}
	remote := &sourceFixture{files: files, format: format, gets: map[string]int{}}
	server := httptest.NewTLSServer(remote)
	t.Cleanup(server.Close)
	hooks := egress.DefaultHooks()
	hooks.ProxyFromEnvironment = func(*http.Request) (*url.URL, error) { return nil, nil }
	hooks.LookupIP = func(context.Context, string, string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("203.0.113.10")}, nil
	}
	hooks.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
	}
	source := nexussource.New(sourcepg.New(db), native, nexussource.Options{Client: server.Client(), EgressHooks: &hooks})
	created, err := source.Create(context.Background(), nexussource.Input{Name: "owned-migration-fixture", BaseURL: "https://example.com"})
	if err != nil {
		t.Fatal(err)
	}
	targetFormat := repository.FormatRaw
	if format == "maven2" {
		targetFormat = repository.FormatMaven
	}
	repo, err := native.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "mig-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:20], Format: targetFormat})
	if err != nil {
		t.Fatal(err)
	}
	publication := app.NewNexusMigrationPublication(native, app.SharedObjectStores(objects), "owned-target", "owned-pool")
	ledger := pgstore.New(db)
	f := &fixture{closeSource: server.Close, t: t, db: db, native: native, objects: objects, ledger: ledger, source: source, sourceID: created.ID, remote: remote, repo: repo, publication: publication, connection: connection}
	f.service = f.newService(m.Options{})
	f.input = m.StartInput{SourceID: created.ID, SourceVersion: created.Version, Mappings: []m.Mapping{{SourceRepository: "source", TargetRepositoryID: repo.ID}}, PolicyVersion: m.PolicyVersion, CapacityEvidence: f.evidence()}
	return f
}
func (f *fixture) evidence() m.CapacityEvidence {
	zero, free, download := int64(0), int64(1<<30), int64(1<<20)
	now := time.Now().UTC()
	return m.CapacityEvidence{TargetID: "owned-target", PoolID: "owned-pool", ObservedAt: now.Add(-time.Second), ValidUntil: now.Add(20 * time.Minute), FreeBytes: &free, DownloadBytes: &download, UploadBytes: &zero, BackupBytes: &zero, RestoreBytes: &zero, HeadroomBytes: &zero}
}
func (f *fixture) newService(options m.Options) *m.Service {
	options.TargetID = "owned-target"
	options.PoolID = "owned-pool"
	return m.New(f.ledger, f.source, f.publication, options)
}
func (f *fixture) start() *m.Batch {
	f.t.Helper()
	b, err := f.service.Start(context.Background(), f.input, "synthetic-admin", uuid.NewString())
	if err != nil {
		f.t.Fatal(err)
	}
	return &b
}
func (f *fixture) run(b *m.Batch) {
	f.t.Helper()
	if err := f.service.RunOnce(context.Background(), []string{"raw", "maven"}); err != nil {
		f.t.Fatal(err)
	}
	got, err := f.service.Get(context.Background(), b.ID)
	if err != nil {
		f.t.Fatal(err)
	}
	*b = got
}
func (f *fixture) entries(id string) []m.Entry {
	f.t.Helper()
	entries, _, err := f.service.Entries(context.Background(), id, 100, "")
	if err != nil {
		f.t.Fatal(err)
	}
	return entries
}
func (f *fixture) retry(b *m.Batch) {
	f.t.Helper()
	ev := f.evidence()
	out, err := f.service.Control(context.Background(), b.ID, b.Version, "synthetic-admin", uuid.NewString(), "retry", &ev)
	if err != nil {
		f.t.Fatal(err)
	}
	*b = out
}
func requireState(t *testing.T, b m.Batch, state string) {
	t.Helper()
	if b.State != state {
		t.Fatalf("batch state=%s want=%s error=%s counts=%+v", b.State, state, b.LastError, b.Counts)
	}
}

func TestPostgresMigrationFullThenCompleteRescanOnlyAddsNewAndReportsDeleted(t *testing.T) {
	f := newFixture(t, "raw", map[string][]byte{"a.bin": []byte("first"), "b.bin": []byte("second")})
	b := f.start()
	f.run(b)
	requireState(t, *b, "completed")
	if !b.ScanComplete || b.TotalBytes == nil || *b.TotalBytes != 11 || b.VerifiedBytes != 11 || b.VisibleIdentities != 2 || b.Counts.Committed != 2 {
		t.Fatalf("incorrect first counters: %+v", b)
	}
	f.remote.mu.Lock()
	delete(f.remote.files, "a.bin")
	f.remote.files["c.bin"] = []byte("third")
	f.remote.mu.Unlock()
	second := f.start()
	f.run(second)
	requireState(t, *second, "completed")
	if second.Counts.Skipped != 1 || second.Counts.Committed != 1 || second.Counts.SourceDeleted != 1 {
		t.Fatalf("incremental counters=%+v", second.Counts)
	}
	if _, err := f.native.GetRawAsset(context.Background(), f.repo.ID, "a.bin"); err != nil {
		t.Fatal("source deletion removed target", err)
	}
	keys, err := f.objects.List(context.Background(), "native/raw/sha256/")
	if err != nil || len(keys) != 3 {
		t.Fatalf("objects=%d err=%v", len(keys), err)
	}
}
func TestPostgresMigrationCrashWindowsRecoverAgainstRealObjectsAndVisibleIdentity(t *testing.T) {
	for _, phase := range []string{"uploaded", "verified", "published"} {
		t.Run(phase, func(t *testing.T) {
			f := newFixture(t, "raw", map[string][]byte{"asset.bin": []byte("durable-content")})
			var once sync.Once
			hook := func(context.Context, m.Entry) error {
				err := error(nil)
				once.Do(func() { err = m.ErrLeaseLost })
				return err
			}
			options := m.Options{}
			switch phase {
			case "uploaded":
				options.AfterUpload = hook
			case "verified":
				options.AfterVerify = hook
			case "published":
				options.AfterPublish = hook
			}
			f.service = f.newService(options)
			b := f.start()
			f.run(b)
			requireState(t, *b, "failed")
			// Restart the service and use a fresh database connection, rather than an
			// in-memory checkpoint or the previous worker instance.
			restarted, err := sql.Open("pgx", f.connection)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = restarted.Close() }()
			f.ledger = pgstore.New(restarted)
			f.service = f.newService(m.Options{})
			f.retry(b)
			f.run(b)
			requireState(t, *b, "completed")
			if b.VisibleIdentities != 1 || b.VerifiedBytes != int64(len("durable-content")) {
				t.Fatalf("recovery counters=%+v", b)
			}
			var visible int
			if err = f.db.QueryRow(`SELECT count(*) FROM native_raw_assets WHERE repository_id=$1`, f.repo.ID).Scan(&visible); err != nil || visible != 1 {
				t.Fatalf("visible=%d err=%v", visible, err)
			}
			entry := f.entries(b.ID)[0]
			if phase == "published" && entry.State != "skipped" {
				t.Fatalf("lost commit response was not reconciled: %+v", entry)
			}
		})
	}
}
func TestPostgresMigrationTwoWorkersAndExpiredLeaseCannotPublishFromOldOwner(t *testing.T) {
	f := newFixture(t, "raw", map[string][]byte{"lease.bin": []byte("lease-content")})
	entered, release := make(chan struct{}), make(chan struct{})
	f.service = f.newService(m.Options{AfterVerify: func(context.Context, m.Entry) error { close(entered); <-release; return nil }, HeartbeatInterval: time.Hour})
	b := f.start()
	done := make(chan error, 1)
	go func() { done <- f.service.RunOnce(context.Background(), []string{"raw"}) }()
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("worker did not reach verification")
	}
	other := f.newService(m.Options{})
	if err := other.RunOnce(context.Background(), []string{"raw"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`UPDATE nexus_migration_batches SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`, b.ID); err != nil {
		t.Fatal(err)
	}
	second := make(chan error, 1)
	go func() { second <- other.RunOnce(context.Background(), []string{"raw"}) }()
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := <-second; err != nil {
		t.Fatal(err)
	}
	got, err := other.Get(context.Background(), b.ID)
	if err != nil {
		t.Fatal(err)
	}
	requireState(t, got, "completed")
	var epoch, count int
	if err = f.db.QueryRow(`SELECT lease_epoch FROM nexus_migration_batches WHERE id=$1`, b.ID).Scan(&epoch); err != nil || epoch != 2 {
		t.Fatalf("epoch=%d err=%v", epoch, err)
	}
	if err = f.db.QueryRow(`SELECT count(*) FROM native_raw_assets WHERE repository_id=$1`, f.repo.ID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("visible=%d err=%v", count, err)
	}
}
func TestPostgresMigrationCommittedTargetSurvivesCancellationBeforeCheckpoint(t *testing.T) {
	f := newFixture(t, "raw", map[string][]byte{"fence.bin": []byte("visible-once")})
	entered, release := make(chan struct{}), make(chan struct{})
	f.service = f.newService(m.Options{AfterPublish: func(context.Context, m.Entry) error { close(entered); <-release; return nil }, HeartbeatInterval: time.Hour})
	b := f.start()
	done := make(chan error, 1)
	go func() { done <- f.service.RunOnce(context.Background(), []string{"raw"}) }()
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("worker did not reach publication")
	}
	other := f.newService(m.Options{})
	current, err := other.Get(context.Background(), b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = other.Control(context.Background(), b.ID, current.Version, "synthetic-admin", uuid.NewString(), "cancel", nil); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	got, err := other.Get(context.Background(), b.ID)
	if err != nil {
		t.Fatal(err)
	}
	requireState(t, got, "cancelled")
	if _, err = f.native.GetRawAsset(context.Background(), f.repo.ID, "fence.bin"); err != nil {
		t.Fatal("cancel removed committed target")
	}
}
func TestPostgresMigrationSourceChangesAndMissingNeverBecomeSuccessful(t *testing.T) {
	for _, kind := range []string{"changed", "missing", "same-size-wrong-bytes"} {
		t.Run(kind, func(t *testing.T) {
			f := newFixture(t, "raw", map[string][]byte{"source.bin": []byte("right")})
			f.remote.forceProbe = true
			f.remote.change = func(_ string, n int, data []byte) ([]byte, int, string) {
				sum := sha256.Sum256(data)
				tag := `"` + hex.EncodeToString(sum[:]) + `"`
				if n == 1 {
					return data, 200, tag
				}
				switch kind {
				case "missing":
					return nil, 404, ""
				case "changed":
					return []byte("wrong"), 200, `"different"`
				default:
					return []byte("wrong"), 200, tag
				}
			}
			b := f.start()
			f.run(b)
			requireState(t, *b, "failed")
			if b.VisibleIdentities != 0 || b.VerifiedBytes != 0 {
				t.Fatal("source failure counted as success")
			}
			if _, err := f.native.GetRawAsset(context.Background(), f.repo.ID, "source.bin"); !errors.Is(err, repository.ErrNotFound) {
				t.Fatalf("source failure published: %v", err)
			}
		})
	}
}
func TestPostgresMigrationUnknownExpiredCapacityAndChangedSourceRequireNewEvidenceOrBatch(t *testing.T) {
	f := newFixture(t, "raw", map[string][]byte{"capacity.bin": []byte("capacity")})
	unknown := f.input
	unknown.CapacityEvidence.FreeBytes = nil
	if _, err := f.service.Start(context.Background(), unknown, "synthetic-admin", uuid.NewString()); err == nil {
		t.Fatal("unknown capacity was accepted")
	}
	b := f.start()
	if _, err := f.db.Exec(`UPDATE nexus_migration_batches SET capacity_evidence=jsonb_set(capacity_evidence,'{validUntil}',to_jsonb($2::text)) WHERE id=$1`, b.ID, time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	f.run(b)
	requireState(t, *b, "blocked")
	keys, err := f.objects.List(context.Background(), "native/")
	if err != nil || len(keys) != 0 {
		t.Fatal("expired evidence allowed object writes")
	}
	ev := f.evidence()
	resumed, err := f.service.Control(context.Background(), b.ID, b.Version, "synthetic-admin", uuid.NewString(), "resume", &ev)
	if err != nil {
		t.Fatal(err)
	}
	*b = resumed
	f.run(b)
	requireState(t, *b, "completed")
	third := f.start()
	f.remote.mu.Lock()
	f.remote.failScan = true
	f.remote.mu.Unlock()
	f.run(third)
	requireState(t, *third, "failed")
	if third.ScanComplete || third.TotalBytes != nil || third.EntryCount != 0 {
		t.Fatal("partial/failed scan became a complete inventory")
	}
	f.remote.mu.Lock()
	f.remote.failScan = false
	f.remote.mu.Unlock()
	_, err = f.source.Update(context.Background(), f.sourceID, nexussource.Input{Name: "source-renamed", BaseURL: "https://example.com"}, "1")
	if err != nil {
		t.Fatal(err)
	}
	ev = f.evidence()
	if _, err = f.service.Control(context.Background(), third.ID, third.Version, "synthetic-admin", uuid.NewString(), "retry", &ev); err == nil {
		t.Fatal("old source version accepted on retry")
	}
}
func TestPostgresMigrationMavenGAVIsAtomicAndNativeColdReadsMatchManifest(t *testing.T) {
	pom := []byte(`<project><modelVersion>4.0.0</modelVersion><groupId>org.example</groupId><artifactId>widget</artifactId><version>1.0</version></project>`)
	files := map[string][]byte{"org/example/widget/1.0/widget-1.0.pom": pom, "org/example/widget/1.0/widget-1.0.jar": []byte("synthetic-jar"), "org/example/widget/1.0/widget-1.0-sources.jar": []byte("synthetic-sources")}
	f := newFixture(t, "maven2", files)
	calls := 0
	f.service = f.newService(m.Options{AfterUpload: func(context.Context, m.Entry) error {
		calls++
		if calls == 2 {
			return m.ErrLeaseLost
		}
		return nil
	}})
	b := f.start()
	f.run(b)
	requireState(t, *b, "failed")
	if _, err := f.native.GetMavenArtifactByCoordinate(context.Background(), f.repo.ID, "org.example:widget:1.0"); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("partially uploaded GAV visible: %v", err)
	}
	f.service = f.newService(m.Options{})
	f.retry(b)
	f.run(b)
	requireState(t, *b, "completed")
	if b.VisibleIdentities != 1 {
		t.Fatalf("GAV counted per file: %+v", b)
	}
	handler, err := app.NewGatewayHandler(app.GatewayOptions{Dependencies: app.Dependencies{MigrationService: f.service, SourceService: f.source}, Store: f.native, NPMMetadata: f.native, NPMRestore: f.native, Storage: app.SharedObjectStores(f.objects), Adapter: app.TestAdapter{}, Authenticator: app.Authenticator{AdminToken: "owned-admin", ResolverToken: "owned-reader", RepositoryReaders: map[string][]string{"maven": {f.repo.Name}}}})
	if err != nil {
		t.Fatal(err)
	}
	target := httptest.NewServer(handler)
	defer target.Close()
	f.remote.mu.Lock()
	f.remote.files = map[string][]byte{}
	f.remote.mu.Unlock()
	for p, want := range files {
		req, _ := http.NewRequest(http.MethodGet, target.URL+"/repository/maven/"+f.repo.Name+"/"+p, nil)
		req.SetBasicAuth("maven", "owned-reader")
		resp, err := target.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil || resp.StatusCode != 200 || !bytes.Equal(body, want) {
			t.Fatalf("target-only cold GET %s=%d bytes=%d err=%v", p, resp.StatusCode, len(body), err)
		}
		sum := sha256.Sum256(want)
		if fmt.Sprintf("%x", sha256.Sum256(body)) != hex.EncodeToString(sum[:]) {
			t.Fatal("full target SHA-256 differs")
		}
	}
}
