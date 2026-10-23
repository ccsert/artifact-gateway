//go:build integration

package helm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/ccsert/artifact-gateway/internal/authorization"
	"github.com/ccsert/artifact-gateway/internal/objectstore"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	dist "github.com/ccsert/artifact-gateway/internal/helmdistribution"
	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

type lostReplicationReply struct {
	dist.Store
	cancel context.CancelFunc
}

func TestPostgresRustFSHelmReplicationRepositoryOnlyKeyAuthority(t *testing.T) {
	f := newReplicationDurableFixture(t)
	testHelmReplicationRepositoryOnlyKeyAuthority(t, f.module, f.store, f.objects, f.repo)
}

func TestPostgresRustFSHelmReplicationBlockedCurrentUserAuthority(t *testing.T) {
	f := newReplicationDurableFixture(t)
	testHelmReplicationBlockedCurrentUserAuthority(t, f.module, f.store, f.objects, f.repo)
}

func (s lostReplicationReply) CompleteHelmReplicationItem(ctx context.Context, lease dist.Lease, index int, proof dist.Commit, check dist.Check) (dist.Job, error) {
	j, err := s.Store.CompleteHelmReplicationItem(ctx, lease, index, proof, check)
	if err != nil {
		return j, err
	}
	s.cancel()
	return j, context.Canceled
}
func TestPostgresRustFSHelmReplicationRestartAndLostCommitResponse(t *testing.T) {
	for _, window := range []string{"after-target-chart-upload", "after-target-provenance-upload", "after-publication-commit"} {
		t.Run(window, func(t *testing.T) {
			f := newReplicationDurableFixture(t)
			f.module.distribution = f.store
			target, err := f.store.CreateHostedRepository(t.Context(), repository.HostedRepository{ID: uuid.NewString(), Name: "replication-target-" + uuid.NewString(), Format: repository.FormatHelm, Type: repository.RepositoryTypeHosted})
			if err != nil {
				t.Fatal(err)
			}
			body := chartFixture(t, "durable-app", "1.0.0", "restart closure")
			proof := provenanceFixture(t, body)
			payload, ct := multipartChart(t, "durable-app", "1.0.0", body, proof)
			w := request(t, f.module.Preview(), "PUT", "/api/v2/previews/helm/repositories/"+f.repo.ID+"/charts/durable-app/1.0.0", payload, map[string]string{"Authorization": "Bearer helm-owned-admin", "Content-Type": ct})
			if w.Code != 201 {
				t.Fatal(w.Body.String())
			}
			source, err := f.store.GetHelmPublication(t.Context(), f.repo.ID, "durable-app", "1.0.0")
			if err != nil {
				t.Fatal(err)
			}
			plan := dist.Plan{SourceRepositoryID: f.repo.ID, TargetRepositoryID: target.ID, Items: []dist.Identity{{Name: source.Name, Version: source.Version, Digest: source.Digest, Size: source.Size, ProvenanceDigest: source.ProvenanceDigest, ProvenanceSize: source.ProvenanceSize, SourceGovernanceVersion: source.Governance.Version}}}
			encoded, _ := json.Marshal(plan)
			w = request(t, f.module.Preview(), "POST", "/api/v2/previews/helm/replication-jobs", encoded, map[string]string{"Authorization": "Bearer helm-owned-admin", "Idempotency-Key": uuid.NewString()})
			var job dist.Job
			if w.Code != 202 || json.Unmarshal(w.Body.Bytes(), &job) != nil {
				t.Fatalf("job=%d %s", w.Code, w.Body.String())
			}
			crash, cancel := context.WithCancel(t.Context())
			defer cancel()
			if strings.HasPrefix(window, "after-target-") {
				f.module.objects = replicationCrashObjects{Store: f.objects, cancel: cancel, afterProvenance: window == "after-target-provenance-upload"}
			} else {
				f.module.distribution = lostReplicationReply{Store: f.store, cancel: cancel}
			}
			worker, err := f.module.ReplicationWorker("old-process")
			if err != nil {
				t.Fatal(err)
			}
			worker.LeaseDuration = time.Second
			if ran, err := worker.RunOnce(crash); !ran || !errors.Is(err, context.Canceled) {
				t.Fatalf("crash=%t %v", ran, err)
			}
			before, err := f.store.GetHelmReplication(t.Context(), job.ID)
			if err != nil || before.Items[0].TargetSessionID == "" {
				t.Fatalf("persisted checkpoint=%+v %v", before, err)
			}
			var committed repository.HelmPublication
			if strings.HasPrefix(window, "after-target-") {
				if _, err = f.store.GetHelmPublication(t.Context(), target.ID, source.Name, source.Version); !errors.Is(err, repository.ErrNotFound) {
					t.Fatalf("upload prematurely visible=%v", err)
				}
			} else {
				committed, err = f.store.GetHelmPublication(t.Context(), target.ID, source.Name, source.Version)
				if err != nil || before.Items[0].State != "succeeded" {
					t.Fatalf("lost response lost commit=%+v %v", before, err)
				}
			}
			_ = f.store.Close()
			reopened, err := repository.NewPostgresStore(os.Getenv("TEST_DATABASE_URL"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = reopened.Close() })
			module := newReplicationDurableModule(t, reopened, f.objects)
			module.distribution = reopened
			time.Sleep(time.Until(before.Lease.Until) + 30*time.Millisecond)
			recovered, err := module.ReplicationWorker("new-process")
			if err != nil {
				t.Fatal(err)
			}
			if ran, err := recovered.RunOnce(t.Context()); !ran || err != nil {
				t.Fatalf("recovery=%t %v", ran, err)
			}
			after, err := reopened.GetHelmReplication(t.Context(), job.ID)
			if err != nil || after.State != "succeeded" || after.Lease.Fence <= before.Lease.Fence {
				t.Fatalf("recovered=%+v %v", after, err)
			}
			publication, err := reopened.GetHelmPublication(t.Context(), target.ID, source.Name, source.Version)
			if err != nil || publication.ObjectKey == source.ObjectKey || publication.SessionID == source.SessionID || publication.ProvenanceKey == source.ProvenanceKey || publication.Governance.Tombstoned || !strings.Contains(publication.ObjectKey, target.ID) {
				t.Fatalf("target=%+v %v", publication, err)
			}
			if committed.SessionID != "" && (publication.SessionID != committed.SessionID || !publication.PublishedAt.Equal(committed.PublishedAt)) {
				t.Fatal("lost commit response republished item")
			}
			if err = f.objects.Delete(t.Context(), source.ProvenanceKey); err != nil {
				t.Fatal(err)
			}
			if err = f.objects.Delete(t.Context(), source.ObjectKey); err != nil {
				t.Fatal(err)
			}
			path := "/helm/" + target.Name + "/charts/durable-app/1.0.0/durable-app-1.0.0.tgz"
			for _, asset := range []struct {
				suffix string
				want   []byte
			}{{"", body}, {".prov", proof}} {
				w = request(t, module, "GET", path+asset.suffix, nil, admin)
				if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), asset.want) {
					t.Fatalf("target closure depends on source=%d", w.Code)
				}
			}
		})
	}
}

type replicationDurableFixture struct {
	module  *Module
	store   *repository.PostgresStore
	objects *objectstore.RustFSStore
	repo    repository.HostedRepository
}

func newReplicationDurableModule(t *testing.T, store *repository.PostgresStore, objects objectstore.Store) *Module {
	t.Helper()
	auth := authorization.Authenticator{AdminToken: "helm-owned-admin", AdminActor: "operator", APIKeys: store, ServiceAccounts: store, Users: store}
	m, err := New(Options{Distribution: store, Store: store, Governance: store, Repositories: store, Objects: objects, Authenticator: auth, Authorizer: authorization.RepositoryAuthorizer{Grants: store, Legacy: auth}, Audit: store, Anonymous: store, Capacities: store})
	if err != nil {
		t.Fatal(err)
	}
	return m
}
func newReplicationDurableFixture(t *testing.T) replicationDurableFixture {
	t.Helper()
	if os.Getenv("TEST_DATABASE_URL") == "" || os.Getenv("TEST_RUSTFS_ENDPOINT") == "" {
		t.Skip("owned PostgreSQL/RustFS required")
	}
	store, err := repository.NewPostgresStore(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	objects, err := objectstore.NewRustFSStore(os.Getenv("TEST_RUSTFS_ENDPOINT"), os.Getenv("TEST_RUSTFS_ACCESS_KEY"), os.Getenv("TEST_RUSTFS_SECRET_KEY"), "helm-replication-"+uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	if err = objects.EnsureBucket(t.Context()); err != nil {
		t.Fatal(err)
	}
	repo, err := store.CreateHostedRepository(t.Context(), repository.HostedRepository{ID: uuid.NewString(), Name: "helm-replication-" + uuid.NewString(), Format: repository.FormatHelm, Type: repository.RepositoryTypeHosted})
	if err != nil {
		t.Fatal(err)
	}
	return replicationDurableFixture{newReplicationDurableModule(t, store, objects), store, objects, repo}
}

type replicationCrashObjects struct {
	objectstore.Store
	cancel          context.CancelFunc
	afterProvenance bool
}

func (s replicationCrashObjects) PutVerifiedReader(ctx context.Context, key string, r io.Reader, size int64, digest string) error {
	err := s.Store.PutVerifiedReader(ctx, key, r, size, digest)
	if err == nil && (!s.afterProvenance || strings.Contains(key, "/provenance/")) {
		s.cancel()
	}
	return err
}
