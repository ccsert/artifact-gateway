//go:build integration

package helm

import (
	"bytes"
	"os"
	"testing"

	"github.com/ccsert/artifact-gateway/internal/authorization"
	"github.com/ccsert/artifact-gateway/internal/objectstore"
	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

func TestPostgresRustFSHelmInterruptedClosureRestartAndCorruptObject(t *testing.T) {
	if os.Getenv("TEST_DATABASE_URL") == "" || os.Getenv("TEST_RUSTFS_ENDPOINT") == "" {
		t.Skip("owned PostgreSQL and RustFS are required")
	}
	store, err := repository.NewPostgresStore(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	objects, err := objectstore.NewRustFSStore(os.Getenv("TEST_RUSTFS_ENDPOINT"), os.Getenv("TEST_RUSTFS_ACCESS_KEY"), os.Getenv("TEST_RUSTFS_SECRET_KEY"), "helm-interruption-"+uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	if err = objects.EnsureBucket(t.Context()); err != nil {
		t.Fatal(err)
	}
	repo, err := store.CreateHostedRepository(t.Context(), repository.HostedRepository{ID: uuid.NewString(), Name: "helm-test", Format: repository.FormatHelm, Type: repository.RepositoryTypeHosted})
	if err != nil {
		t.Fatal(err)
	}
	auth := authorization.Authenticator{AdminToken: "helm-owned-admin", AdminActor: "operator"}
	compose := func(objects objectstore.Store) *Module {
		module, e := New(Options{Store: store, Repositories: store, Objects: objects, Authenticator: auth, Authorizer: authorization.RepositoryAuthorizer{Grants: store, Legacy: auth}, Audit: store, Anonymous: store, Capacities: store})
		if e != nil {
			t.Fatal(e)
		}
		return module
	}
	interrupted := &interruptedObjects{Store: objects}
	interrupted.fail.Store(true)
	f := fixture{module: compose(interrupted), repo: repo}
	chart := chartFixture(t, "restart", "1.0.0", "durable restart")
	prov := provenanceFixture(t, chart)
	if w := upload(t, f, "restart", "1.0.0", chart, prov); w.Code != 503 {
		t.Fatalf("partial write=%d %s", w.Code, w.Body.String())
	}
	if rows, e := store.ListHelmPublications(t.Context(), repo.ID, 10); e != nil || len(rows) != 0 {
		t.Fatalf("partial closure indexed: %+v %v", rows, e)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = repository.NewPostgresStore(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	f.module = compose(objects)
	if w := upload(t, f, "restart", "1.0.0", chart, prov); w.Code != 201 {
		t.Fatalf("durable recovery=%d %s", w.Code, w.Body.String())
	}
	p, err := store.GetHelmPublication(t.Context(), repo.ID, "restart", "1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	original := request(t, f.module, "GET", assetPath(repo, p, false), nil, admin)
	if original.Code != 200 || !bytes.Equal(original.Body.Bytes(), chart) {
		t.Fatalf("restarted read=%d", original.Code)
	}
	corrupt := append([]byte(nil), chart...)
	corrupt[len(corrupt)/2] ^= 1
	if err = objects.Put(t.Context(), p.ObjectKey, corrupt); err != nil {
		t.Fatal(err)
	}
	if w := request(t, f.module, "GET", assetPath(repo, p, false), nil, admin); w.Code != 503 || bytes.Equal(w.Body.Bytes(), corrupt) {
		t.Fatalf("corrupt chart served=%d", w.Code)
	}
	if err = objects.PutVerifiedReader(t.Context(), p.ObjectKey, bytes.NewReader(chart), int64(len(chart)), p.Digest); err != nil {
		t.Fatal(err)
	}
	if w := request(t, f.module, "GET", assetPath(repo, p, true), nil, admin); w.Code != 200 || !bytes.Equal(w.Body.Bytes(), prov) {
		t.Fatalf("original provenance=%d", w.Code)
	}
}
