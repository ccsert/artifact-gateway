//go:build integration

package pgstore_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/ccsert/artifact-gateway/internal/app"
	"github.com/ccsert/artifact-gateway/internal/egress"
	m "github.com/ccsert/artifact-gateway/internal/nexusmigration"
	"github.com/ccsert/artifact-gateway/internal/nexussource"
	sourcepg "github.com/ccsert/artifact-gateway/internal/nexussource/pgstore"
	"github.com/ccsert/artifact-gateway/internal/ocigraph"
	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/ccsert/artifact-gateway/internal/secrets"
	"github.com/ccsert/artifact-gateway/internal/testsupport/ocifixture"
	"github.com/google/uuid"
)

type nexusOCISource struct {
	mu           sync.Mutex
	fixture      ocifixture.Fixture
	reads        int
	mutations    int
	anonymous    int
	unauthorized bool
}

func (f *nexusOCISource) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Method != "GET" {
		f.mutations++
		w.WriteHeader(405)
		return
	}
	username, password, ok := r.BasicAuth()
	if !ok || username != "fixture-read-only" || password != "fixture-nexus-source-secret" {
		f.anonymous++
		w.WriteHeader(401)
		return
	}
	if f.unauthorized {
		w.WriteHeader(401)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	switch r.URL.Path {
	case "/service/rest/v1/repositories":
		_ = json.NewEncoder(w).Encode([]map[string]string{{"name": "source", "format": "docker", "type": "hosted"}})
	case "/service/rest/v1/repositories/docker/hosted/source":
		_, _ = io.WriteString(w, `{"name":"source","docker":{"v1Enabled":false,"forceBasicAuth":true}}`)
	case "/service/rest/v1/assets":
		paths := make([]string, 0, len(f.fixture.Bytes))
		for path := range f.fixture.Bytes {
			paths = append(paths, path)
		}
		sort.Strings(paths)
		offset, _ := strconv.Atoi(r.URL.Query().Get("continuationToken"))
		if offset < 0 || offset > len(paths) {
			w.WriteHeader(400)
			return
		}
		end := min(len(paths), offset+2)
		items := []map[string]any{}
		for _, path := range paths[offset:end] {
			items = append(items, map[string]any{"repository": "source", "format": "docker", "path": "/" + path, "downloadUrl": "https://example.com/repository/source/" + path, "fileSize": len(f.fixture.Bytes[path]), "checksum": map[string]string{"sha256": f.fixture.Digests[path]}})
		}
		var next any
		if end < len(paths) {
			next = strconv.Itoa(end)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"items": items, "continuationToken": next})
	default:
		path := strings.TrimPrefix(r.URL.Path, "/repository/source/")
		body, ok := f.fixture.Bytes[path]
		if !ok {
			w.WriteHeader(404)
			return
		}
		if strings.Contains(path, "/manifests/") && !strings.Contains(r.Header.Get("Accept"), ocigraph.OCIIndex) {
			w.WriteHeader(406)
			return
		}
		f.reads++
		w.Header().Set("Content-Type", f.fixture.MediaTypes[path])
		w.Header().Set("ETag", `"`+f.fixture.Digests[path]+`"`)
		w.Header().Set("Docker-Content-Digest", "sha256:"+f.fixture.Digests[path])
		_, _ = w.Write(body)
	}
}

func newOCIMigrationFixture(t *testing.T) (*fixture, *nexusOCISource, ocifixture.Fixture) {
	return newOCIMigrationFixtureWithProducer(t, ocifixture.New(t.Name()+uuid.NewString()))
}

func newOCIMigrationFixtureWithProducer(t *testing.T, producer ocifixture.Fixture) (*fixture, *nexusOCISource, ocifixture.Fixture) {
	t.Helper()
	t.Setenv(secrets.KeyEnv, "0123456789abcdef0123456789abcdef")
	f := newFixture(t, "raw", map[string][]byte{})
	remote := &nexusOCISource{fixture: producer}
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
	f.source = nexussource.New(sourcepg.New(f.db), f.native, nexussource.Options{Client: server.Client(), EgressHooks: &hooks})
	source, err := f.source.Create(context.Background(), nexussource.Input{Name: "owned-oci-source", BaseURL: "https://example.com", Credentials: &nexussource.Credentials{Username: "fixture-read-only", Password: "fixture-nexus-source-secret"}})
	if err != nil {
		t.Fatal(err)
	}
	f.repo, err = f.native.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "oci-migration-" + uuid.NewString()[:8], Format: repository.FormatOCI})
	if err != nil {
		t.Fatal(err)
	}
	f.publication = app.NewNexusMigrationPublication(f.native, app.SharedObjectStores(f.objects), "owned-target", "owned-pool").WithOCI(app.NewNexusOCIMigrationPublication(f.native, f.objects))
	f.sourceID = source.ID
	f.closeSource = server.Close
	f.service = f.newService(m.Options{})
	f.input = m.StartInput{SourceID: source.ID, SourceVersion: source.Version, Mappings: []m.Mapping{{SourceRepository: "source", TargetRepositoryID: f.repo.ID}}, PolicyVersion: m.PolicyVersion, CapacityEvidence: f.evidence()}
	return f, remote, producer
}

func runOCIBatch(t *testing.T, f *fixture, b *m.Batch) {
	t.Helper()
	if err := f.service.RunOnce(context.Background(), []string{"oci"}); err != nil {
		t.Fatal(err)
	}
	current, err := f.service.Get(context.Background(), b.ID)
	if err != nil {
		t.Fatal(err)
	}
	*b = current
}

func TestPostgresNexusOCIMigrationCompleteIncrementalAndRestart(t *testing.T) {
	f, remote, producer := newOCIMigrationFixture(t)
	preview, err := f.source.PreviewMapping(context.Background(), f.sourceID, []nexussource.Mapping{{SourceRepository: "source", TargetName: f.repo.Name, Format: "oci", Type: "hosted"}})
	if err != nil || !preview.Valid {
		t.Fatalf("OCI mapping=%+v %v", preview, err)
	}
	b := f.start()
	runOCIBatch(t, f, b)
	requireState(t, *b, "completed")
	if !b.ScanComplete || b.Counts.Committed != 1 || b.Counts.Skipped != 2 || b.SourceVerificationBytes == 0 || b.MetadataDiscovered != 6 {
		t.Fatalf("inventory/closure progress=%+v", *b)
	}
	entries := f.entries(b.ID)
	if len(entries) != 3 {
		t.Fatalf("entries=%+v", entries)
	}
	var root m.Entry
	for _, entry := range entries {
		if entry.Identity == producer.Name+":"+producer.Reference {
			root = entry
		}
	}
	if len(root.Assets) != 6 {
		t.Fatalf("full platforms were flattened: %+v", root)
	}
	for _, a := range root.Assets {
		if a.MediaType == "" {
			t.Fatal("durable asset media type lost")
		}
	}
	// A new service/store owner consumes the persisted entry, not a process cache.
	reopened, err := repository.NewPostgresStore(f.connection)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reopened.Close() }()
	fresh := app.NewNexusOCIMigrationPublication(reopened, f.objects)
	if state, err := fresh.Readback(context.Background(), root); err != nil || state != m.Identical {
		t.Fatalf("post-restart closure=%s %v", state, err)
	}
	remote.mu.Lock()
	newTag := "v2/" + producer.Name + "/manifests/extra"
	rootPath := "v2/" + producer.Name + "/manifests/" + producer.Reference
	remote.fixture.Bytes[newTag] = bytes.Clone(producer.Bytes[rootPath])
	remote.fixture.MediaTypes[newTag] = producer.MediaTypes[rootPath]
	remote.fixture.Digests[newTag] = producer.Digests[rootPath]
	remote.mu.Unlock()
	f.service = f.newService(m.Options{})
	next := f.start()
	runOCIBatch(t, f, next)
	requireState(t, *next, "completed")
	if next.Counts.Committed != 1 || next.Counts.Skipped != 3 {
		t.Fatalf("incremental identity replay duplicated publication: %+v", *next)
	}
	capacity, err := f.native.GetRepositoryCapacity(context.Background(), f.repo.ID)
	if err != nil || capacity.ObjectCount != 6 {
		t.Fatalf("shared graph object refs duplicated: %+v %v", capacity, err)
	}
	remote.mu.Lock()
	mutations, anonymous := remote.mutations, remote.anonymous
	remote.mu.Unlock()
	if mutations != 0 || anonymous != 0 {
		t.Fatalf("source boundary mutations=%d anonymous=%d", mutations, anonymous)
	}
	f.closeSource()
	if state, err := fresh.Readback(context.Background(), root); err != nil || state != m.Identical {
		t.Fatalf("source shutdown broke target closure: %s %v", state, err)
	}
}

func TestPostgresNexusOCIMigrationUploadCrashAndSourceUnauthorized(t *testing.T) {
	for _, mode := range []string{"after_upload", "after_publish", "source_unauthorized"} {
		t.Run(mode, func(t *testing.T) {
			f, remote, producer := newOCIMigrationFixture(t)
			fail := true
			hook := func(context.Context, m.Entry) error {
				if fail {
					return errors.New("synthetic migration process lost")
				}
				return nil
			}
			options := m.Options{}
			if mode == "after_upload" {
				options.AfterUpload = hook
			} else if mode == "after_publish" {
				options.AfterPublish = hook
			} else {
				remote.mu.Lock()
				remote.unauthorized = true
				remote.mu.Unlock()
			}
			f.service = f.newService(options)
			b := f.start()
			runOCIBatch(t, f, b)
			if mode == "source_unauthorized" {
				if b.State == "completed" || b.LastError == "" {
					t.Fatalf("protected source silently became empty: %+v", *b)
				}
				remote.mu.Lock()
				anonymous := remote.anonymous
				remote.mu.Unlock()
				if anonymous != 0 {
					t.Fatal("source 401 retried anonymously")
				}
				return
			}
			if b.Counts.Committed != 0 || b.Counts.Failed == 0 {
				t.Fatalf("crash was marked committed %+v", *b)
			}
			fail = false
			f.service = f.newService(m.Options{})
			f.retry(b)
			runOCIBatch(t, f, b)
			requireState(t, *b, "completed")
			manifest, err := f.native.GetOCIManifest(context.Background(), f.repo.ID, producer.Name, producer.Reference)
			if err != nil || manifest.Digest != producer.Root {
				t.Fatalf("recovered source root drifted: %+v %v", manifest, err)
			}
			var audits int
			if err = f.db.QueryRow(`SELECT COUNT(*)FROM resolver_audit_log WHERE repository=$1 AND operation='nexus.migration.publish'`, f.repo.Name).Scan(&audits); err != nil || audits != 1 {
				t.Fatalf("crash/replay duplicate audit=%d %v", audits, err)
			}
		})
	}
}

func ociTargetReference(f *fixture, producer ocifixture.Fixture) string {
	return fmt.Sprintf("%s/%s@%s", f.repo.Name, producer.Name, producer.Root)
}
