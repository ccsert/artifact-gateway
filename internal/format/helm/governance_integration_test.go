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

func TestPostgresRustFSHelmGovernanceHTTPRestartAndCorruptClosure(t *testing.T) {
	if os.Getenv("TEST_DATABASE_URL") == "" || os.Getenv("TEST_RUSTFS_ENDPOINT") == "" {
		t.Skip("owned PostgreSQL and RustFS are required")
	}
	store, err := repository.NewPostgresStore(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	objects, err := objectstore.NewRustFSStore(os.Getenv("TEST_RUSTFS_ENDPOINT"), os.Getenv("TEST_RUSTFS_ACCESS_KEY"), os.Getenv("TEST_RUSTFS_SECRET_KEY"), "helm-governance-"+uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	if err = objects.EnsureBucket(t.Context()); err != nil {
		t.Fatal(err)
	}
	repo, err := store.CreateHostedRepository(t.Context(), repository.HostedRepository{ID: uuid.NewString(), Name: "helm-governance-" + uuid.NewString(), Format: repository.FormatHelm, Type: repository.RepositoryTypeHosted})
	if err != nil {
		t.Fatal(err)
	}
	compose := func() *Module {
		t.Helper()
		auth := authorization.Authenticator{AdminToken: "helm-owned-admin", AdminActor: "operator"}
		m, err := New(Options{Store: store, Governance: store, Repositories: store, Objects: objects, Authenticator: auth, Authorizer: authorization.RepositoryAuthorizer{Grants: store, Legacy: auth}, Audit: store, Anonymous: store, Capacities: store})
		if err != nil {
			t.Fatal(err)
		}
		return m
	}
	f := fixture{module: compose(), repo: repo}
	chart := chartFixture(t, "owned", "1.2.3", "durable governed original")
	prov := provenanceFixture(t, chart)
	if w := upload(t, f, "owned", "1.2.3", chart, prov); w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	p, err := store.GetHelmPublication(t.Context(), repo.ID, "owned", "1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	governanceRequest(t, f, "owned", "1.2.3", "quarantine", "1", admin, 200)
	policyRequest(t, f, "1", true, 200)
	governanceRequest(t, f, "owned", "1.2.3", "delete", "2", admin, 200)
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = repository.NewPostgresStore(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	f.module = compose()
	for _, path := range []string{assetPath(repo, p, false), assetPath(repo, p, true)} {
		if w := request(t, f.module, "GET", path, nil, admin); w.Code != 404 {
			t.Fatalf("reopened tombstone=%d", w.Code)
		}
	}
	corrupt := append([]byte(nil), chart...)
	corrupt[len(corrupt)/2] ^= 1
	if err = objects.Put(t.Context(), p.ObjectKey, corrupt); err != nil {
		t.Fatal(err)
	}
	governanceRequest(t, f, "owned", "1.2.3", "restore", "3", admin, 502)
	retained, err := store.GetHelmPublication(t.Context(), repo.ID, p.Name, p.Version)
	if err != nil || retained.Governance.Version != "3" || !retained.Governance.Tombstoned || !retained.Governance.Quarantined {
		t.Fatalf("failed restore changed state=%+v %v", retained.Governance, err)
	}
	if err = objects.PutVerifiedReader(t.Context(), p.ObjectKey, bytes.NewReader(chart), p.Size, p.Digest); err != nil {
		t.Fatal(err)
	}
	governanceRequest(t, f, "owned", "1.2.3", "restore", "3", admin, 200)
	if w := request(t, f.module, "GET", assetPath(repo, p, false), nil, admin); w.Code != 404 {
		t.Fatalf("restore erased independent quarantine=%d", w.Code)
	}
	corrupt = append([]byte(nil), prov...)
	corrupt[len(corrupt)/2] ^= 1
	if err = objects.Put(t.Context(), p.ProvenanceKey, corrupt); err != nil {
		t.Fatal(err)
	}
	governanceRequest(t, f, "owned", "1.2.3", "release", "4", admin, 502)
	policyRequest(t, f, "2", false, 502)
	if err = objects.PutVerifiedReader(t.Context(), p.ProvenanceKey, bytes.NewReader(prov), p.ProvenanceSize, p.ProvenanceDigest); err != nil {
		t.Fatal(err)
	}
	governanceRequest(t, f, "owned", "1.2.3", "release", "4", admin, 200)
	for _, asset := range []struct {
		path string
		body []byte
	}{{assetPath(repo, p, false), chart}, {assetPath(repo, p, true), prov}} {
		w := request(t, f.module, "GET", asset.path, nil, admin)
		if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), asset.body) {
			t.Fatalf("durable retained bytes=%d", w.Code)
		}
	}
	capacity, err := store.GetRepositoryCapacity(t.Context(), repo.ID)
	if err != nil || capacity.UsedBytes != int64(len(chart)+len(prov)) || capacity.ObjectCount != 2 {
		t.Fatalf("retained quota=%+v %v", capacity, err)
	}
}
