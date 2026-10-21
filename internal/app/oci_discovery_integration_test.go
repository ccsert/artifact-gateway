//go:build integration

package app

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ccsert/artifact-gateway/internal/objectstore"
	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

type postgresDiscoveryFixture struct {
	store   *repository.PostgresStore
	objects *objectstore.RustFSStore
	control *PostgresCacheControlStore
}

// This fixture owns its global policy view; all identity, grants, repositories,
// bindings and content use the actual PostgreSQL/RustFS ports. No shared global
// policy row is modified in an integration database used by other packages.
type ociAnonymousPostgresFixture struct{ *repository.PostgresStore }

func (*ociAnonymousPostgresFixture) GetAnonymousAccessPolicy(context.Context) (repository.AnonymousAccessPolicy, error) {
	return repository.AnonymousAccessPolicy{Enabled: true, Version: "1"}, nil
}

func TestPostgresRustFSOCIDiscoveryExplicitCredentialsNeverBecomeAnonymous(t *testing.T) {
	f := newPostgresDiscoveryFixture(t)
	testOCIExplicitCredentials(t, &ociAnonymousPostgresFixture{f.store}, f.store, f.objects, f.control)
}

func TestPostgresRustFSOCIDiscoveryDescriptorAnnotationsFromVerifiedManifest(t *testing.T) {
	f := newPostgresDiscoveryFixture(t)
	testOCIReferrerAnnotations(t, f.store, f.store, f.objects, f.control)
}

type catalogVersionSwitchPostgresStore struct {
	*repository.PostgresStore
	afterRepositoryPage func()
}

func (s *catalogVersionSwitchPostgresStore) ListHostedRepositories(ctx context.Context, size int, after string) ([]repository.HostedRepository, string, error) {
	items, next, err := s.PostgresStore.ListHostedRepositories(ctx, size, after)
	if s.afterRepositoryPage != nil {
		action := s.afterRepositoryPage
		s.afterRepositoryPage = nil
		action()
	}
	return items, next, err
}

func TestPostgresRustFSOCIDiscoveryCatalogConflictingVersions(t *testing.T) {
	var calls atomic.Int32
	fixture := newProxyRedirectFixture(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		name := "team/old-source"
		if strings.HasPrefix(r.URL.Path, "/new/") {
			name = "team/new-source"
		}
		_, _ = fmt.Fprintf(w, `{"repositories":[%q]}`, name)
	})
	f := newPostgresDiscoveryFixture(t)
	repo := f.proxy(t, "https://"+redirectOrigin)
	actor := "version-reader-" + uuid.NewString()[:8]
	catalogReader(t, f.store, repo, actor, "team/")
	token := testAuthenticator().IssueToken(actor)
	if _, _, err := f.store.CreateHostedGroupIdempotently(t.Context(), repository.HostedGroup{ID: uuid.NewString(), Name: "z-group-" + uuid.NewString()[:8], Format: repository.FormatOCI, Members: []repository.GroupMember{{RepositoryID: repo.ID}}}, "fixture-admin", uuid.NewString(), "fixture"); err != nil {
		t.Fatal(err)
	}
	controlled := &catalogVersionSwitchPostgresStore{PostgresStore: f.store, afterRepositoryPage: func() {
		repo.Endpoint += "/new"
		var err error
		repo, err = f.store.UpdateHostedRepository(t.Context(), repo, repo.Version)
		if err != nil {
			t.Fatal(err)
		}
	}}
	handler := newTestGateway(t, GatewayOptions{Store: controlled, OCIDiscoveryStore: f.store, Adapter: TestAdapter{}, Authenticator: testAuthenticator(), Storage: ObjectStores{repository.FormatOCI: f.objects}, Caches: GatewayCaches{OCI: NewDefaultOCICache(f.control, []string{redirectOrigin, redirectCDN})}, Upstream: fixture.client})
	changed := discoveryRequest(t, handler, "/v2/_catalog", token)
	if changed.Code != 409 || !strings.Contains(changed.Body.String(), "DISCOVERY_CHANGED") || strings.Contains(changed.Body.String(), "team/") {
		t.Fatalf("mixed durable source versions became successful: %d %s", changed.Code, changed.Body.String())
	}
	before := calls.Load()
	fresh := discoveryRequest(t, handler, "/v2/_catalog", token)
	if fresh.Code != 200 || strings.Contains(fresh.Body.String(), "team/old-source") || !strings.Contains(fresh.Body.String(), repo.Name+"/team/new-source") || calls.Load()-before != 1 {
		t.Fatalf("fresh durable Catalog=%d %s source calls=%d", fresh.Code, fresh.Body.String(), calls.Load()-before)
	}
}

func TestPostgresRustFSOCIDiscoveryRejectInFlightSourceChange(t *testing.T) {
	f := newPostgresDiscoveryFixture(t)
	testOCIReferrersRejectInFlightSourceChange(t, f.store, f.store, f.objects)
}

func TestPostgresRustFSOCIDiscoveryPrincipalAuthorityPartition(t *testing.T) {
	for _, adminFirst := range []bool{false, true} {
		t.Run(fmt.Sprint("adminFirst=", adminFirst), func(t *testing.T) {
			f := newPostgresDiscoveryFixture(t)
			testOCIPrincipalAuthority(t, f.store, f.store, f.objects, f.control, adminFirst)
		})
	}
}

func newPostgresDiscoveryFixture(t *testing.T) *postgresDiscoveryFixture {
	t.Helper()
	for _, key := range []string{"TEST_DATABASE_URL", "TEST_RUSTFS_ENDPOINT", "TEST_RUSTFS_ACCESS_KEY", "TEST_RUSTFS_SECRET_KEY"} {
		if os.Getenv(key) == "" {
			if os.Getenv("OCI_DISCOVERY_REQUIRED") == "1" {
				t.Fatalf("owned %s is required", key)
			}
			t.Skip("isolated PostgreSQL and RustFS are required")
		}
	}
	objects, err := objectstore.NewRustFSStore(os.Getenv("TEST_RUSTFS_ENDPOINT"), os.Getenv("TEST_RUSTFS_ACCESS_KEY"), os.Getenv("TEST_RUSTFS_SECRET_KEY"), "oci-discovery-"+strings.ReplaceAll(uuid.NewString(), "-", ""))
	if err != nil {
		t.Fatal(err)
	}
	if err := objects.EnsureBucket(t.Context()); err != nil {
		t.Fatal(err)
	}
	f := &postgresDiscoveryFixture{objects: objects}
	f.open(t)
	t.Cleanup(func() { f.close(t) })
	return f
}

func (f *postgresDiscoveryFixture) open(t *testing.T) {
	t.Helper()
	var err error
	f.store, err = repository.NewPostgresStore(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	f.control, err = NewPostgresCacheControlStore(f.objects, os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		_ = f.store.Close()
		t.Fatal(err)
	}
}

func (f *postgresDiscoveryFixture) close(t *testing.T) {
	t.Helper()
	if f.store != nil {
		if err := f.store.Close(); err != nil {
			t.Error(err)
		}
		f.store = nil
	}
	if f.control != nil {
		if err := f.control.Close(); err != nil {
			t.Error(err)
		}
		f.control = nil
	}
}

func (f *postgresDiscoveryFixture) handler(t *testing.T, upstream OCIClient) http.Handler {
	t.Helper()
	auth := testAuthenticator()
	auth.APIKeys = f.store
	return newTestGateway(t, GatewayOptions{Store: f.store, OCIDiscoveryStore: f.store, Adapter: TestAdapter{}, Authenticator: auth, Storage: ObjectStores{repository.FormatOCI: f.objects}, Caches: GatewayCaches{OCI: NewDefaultOCICache(f.control, []string{redirectOrigin, redirectCDN})}, Upstream: upstream})
}

func (f *postgresDiscoveryFixture) proxy(t *testing.T, endpoint string) repository.HostedRepository {
	t.Helper()
	repo, err := f.store.CreateHostedRepository(t.Context(), repository.HostedRepository{ID: uuid.NewString(), Name: "discovery-" + uuid.NewString()[:8], Format: repository.FormatOCI, Type: repository.RepositoryTypeProxy, Endpoint: endpoint, AllowedHosts: []string{redirectOrigin, redirectCDN}})
	if err != nil {
		t.Fatal(err)
	}
	return repo
}

func TestPostgresRustFSOCIDiscoveryRestartAndOffline(t *testing.T) {
	artifact := newDiscoveryArtifactFixture(t, `{"SPDXID":"durable-offline"}`)
	var offline atomic.Bool
	var requests atomic.Int32
	upstream := newProxyRedirectFixture(t, func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if offline.Load() {
			w.WriteHeader(503)
			return
		}
		if r.URL.Path == "/v2/_catalog" {
			_, _ = io.WriteString(w, `{"repositories":["team/app","team/other"]}`)
			return
		}
		artifact.serve(w, r)
	})
	f := newPostgresDiscoveryFixture(t)
	repo := f.proxy(t, "https://"+redirectOrigin)
	actor := "offline-reader-" + uuid.NewString()[:8]
	catalogReader(t, f.store, repo, actor, "team/")
	token := testAuthenticator().IssueToken(actor)
	handler := f.handler(t, upstream.client)
	first := discoveryRequest(t, handler, "/v2/_catalog?n=1", token)
	link, err := ociDiscoveryNext(first.Header().Values("Link"))
	if first.Code != 200 || err != nil || link == "" {
		t.Fatalf("durable catalog=%d %s %v", first.Code, first.Body.String(), err)
	}
	path := "/v2/" + repo.Name + "/team/app/referrers/" + artifact.subject.Digest
	refs := discoveryRequest(t, handler, path, token)
	if refs.Code != 200 || !strings.Contains(refs.Body.String(), artifact.refs[0].Digest) {
		t.Fatalf("durable referrers=%d %s", refs.Code, refs.Body.String())
	}
	f.close(t)
	f.open(t)
	handler = f.handler(t, upstream.client)
	offline.Store(true)
	before := requests.Load()
	next := discoveryRequest(t, handler, link, token)
	if next.Code != 200 || !strings.Contains(next.Body.String(), repo.Name+"/team/other") {
		t.Fatalf("reopened catalog=%d %s", next.Code, next.Body.String())
	}
	refs = discoveryRequest(t, handler, path, token)
	if refs.Code != 200 || !strings.Contains(refs.Body.String(), artifact.refs[0].Digest) {
		t.Fatalf("offline referrers=%d %s", refs.Code, refs.Body.String())
	}
	for digest, object := range artifact.objects {
		resource := "blobs"
		if object.media == "application/vnd.oci.image.manifest.v1+json" {
			resource = "manifests"
		}
		got := discoveryRequest(t, handler, "/v2/"+repo.Name+"/team/app/"+resource+"/"+digest, token)
		if got.Code != 200 || got.Body.String() != string(object.body) || got.Header().Get("Docker-Content-Digest") != digest {
			t.Fatalf("offline complete original object %s=%d %s", digest, got.Code, got.Body.String())
		}
	}
	if requests.Load() != before {
		t.Fatalf("offline read contacted upstream %d/%d", requests.Load(), before)
	}
	if _, err := f.store.UpdateHostedRepository(context.Background(), repo, repo.Version); err != nil {
		t.Fatal(err)
	}
	changed := discoveryRequest(t, handler, path, token)
	if changed.Code != 409 || requests.Load() != before {
		t.Fatalf("reopened changed source=%d %s", changed.Code, changed.Body.String())
	}
}
