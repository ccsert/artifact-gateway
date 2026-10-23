//go:build integration

package helm

import (
	"bytes"
	"os"
	"testing"
	"time"

	"github.com/ccsert/artifact-gateway/internal/authorization"
	"github.com/ccsert/artifact-gateway/internal/objectstore"
	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

func TestPostgresRustFSHelmProxyInterruptedClosureRestartAndOffline(t *testing.T) {
	if os.Getenv("TEST_DATABASE_URL") == "" || os.Getenv("TEST_RUSTFS_ENDPOINT") == "" {
		t.Skip("owned PostgreSQL and RustFS required")
	}
	store, e := repository.NewPostgresStore(os.Getenv("TEST_DATABASE_URL"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = store.Close() })
	objects, e := objectstore.NewRustFSStore(os.Getenv("TEST_RUSTFS_ENDPOINT"), os.Getenv("TEST_RUSTFS_ACCESS_KEY"), os.Getenv("TEST_RUSTFS_SECRET_KEY"), "helm-proxy-interruption-"+uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	if e = objects.EnsureBucket(t.Context()); e != nil {
		t.Fatal(e)
	}
	chart := chartFixture(t, "owned", "1.2.3", "proxy restart")
	prov := provenanceFixture(t, chart)
	feed := newHelmFeed(t, chart, prov)
	repo, e := store.CreateHostedRepository(t.Context(), repository.HostedRepository{ID: uuid.NewString(), Name: "proxy-" + uuid.NewString(), Format: repository.FormatHelm, Type: repository.RepositoryTypeProxy, Endpoint: feed.server.URL + "/index.yaml"})
	if e != nil {
		t.Fatal(e)
	}
	compose := func(object objectstore.Store) *Module {
		auth := authorization.Authenticator{AdminToken: "helm-owned-admin", AdminActor: "operator"}
		m, e := New(Options{Store: store, Governance: store, Repositories: store, Objects: object, Authenticator: auth, Authorizer: authorization.RepositoryAuthorizer{Grants: store, Legacy: auth}, Audit: store, Anonymous: store, Capacities: store, Sources: store, Configuration: store, Groups: store, Upstream: helmSourceClient{feed.server.Client()}})
		if e != nil {
			t.Fatal(e)
		}
		return m
	}
	interrupted := &interruptedObjects{Store: objects}
	interrupted.fail.Store(true)
	m := compose(interrupted)
	path := indexChartURL(t, request(t, m, "GET", "/helm/"+repo.Name+"/index.yaml", nil, admin), "owned")
	if w := request(t, m, "GET", path, nil, admin); w.Code != 503 {
		t.Fatalf("interruption=%d %s", w.Code, w.Body.String())
	}
	if all, e := store.ListHelmPublications(t.Context(), repo.ID, 10); e != nil || len(all) != 0 {
		t.Fatalf("partial cache=%+v %v", all, e)
	}
	if e = store.Close(); e != nil {
		t.Fatal(e)
	}
	store, e = repository.NewPostgresStore(os.Getenv("TEST_DATABASE_URL"))
	if e != nil {
		t.Fatal(e)
	}
	m = compose(objects)
	for _, test := range []struct {
		path string
		body []byte
	}{{path, chart}, {path + ".prov", prov}} {
		w := request(t, m, "GET", test.path, nil, admin)
		if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), test.body) {
			t.Fatalf("restart recovery=%d %s", w.Code, w.Body.String())
		}
	}
	feed.server.Close()
	doc, e := store.GetHelmSourceDocument(t.Context(), repo.ID, repo.Version)
	if e != nil {
		t.Fatal(e)
	}
	doc.FetchedAt = time.Now().Add(-time.Minute)
	doc.ExpiresAt = time.Now().Add(-time.Second)
	if e = store.PutHelmSourceDocument(t.Context(), doc); e != nil {
		t.Fatal(e)
	}
	if w := request(t, m, "GET", "/helm/"+repo.Name+"/index.yaml", nil, admin); w.Code != 200 {
		t.Fatalf("offline index=%d %s", w.Code, w.Body.String())
	}
	p, e := store.GetHelmPublication(t.Context(), repo.ID, "owned", "1.2.3")
	if e != nil {
		t.Fatal(e)
	}
	corrupt := append([]byte{}, chart...)
	corrupt[len(corrupt)/2] ^= 1
	if e = objects.Put(t.Context(), p.ObjectKey, corrupt); e != nil {
		t.Fatal(e)
	}
	if w := request(t, m, "GET", path, nil, admin); w.Code != 503 || bytes.Equal(w.Body.Bytes(), corrupt) {
		t.Fatalf("corrupt cached object served=%d", w.Code)
	}
	maintenance, e := NewMaintenance(store, objects)
	if e != nil {
		t.Fatal(e)
	}
	if e = maintenance.Collect(t.Context(), time.Now().Add(2*time.Hour)); e != nil {
		t.Fatal(e)
	}
}
