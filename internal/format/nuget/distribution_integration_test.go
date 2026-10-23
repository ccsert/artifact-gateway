//go:build integration

package nuget

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	dist "github.com/ccsert/artifact-gateway/internal/nugetdistribution"
	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

type lostReplicationReply struct {
	dist.Store
	cancel context.CancelFunc
}

func (s lostReplicationReply) CompleteNuGetReplicationItem(ctx context.Context, lease dist.Lease, index int, proof dist.Commit, check dist.Check) (dist.Job, error) {
	j, err := s.Store.CompleteNuGetReplicationItem(ctx, lease, index, proof, check)
	if err != nil {
		return j, err
	}
	s.cancel()
	return j, context.Canceled
}
func TestPostgresRustFSNuGetReplicationRestartAndLostCommitResponse(t *testing.T) {
	for _, window := range []string{"after-target-upload", "after-publication-commit"} {
		t.Run(window, func(t *testing.T) {
			f := newDurableFixture(t)
			f.module.distribution = f.store
			target, err := f.store.CreateHostedRepository(t.Context(), repository.HostedRepository{ID: uuid.NewString(), Name: "replication-target-" + uuid.NewString(), Format: repository.FormatNuGet, Type: repository.RepositoryTypeHosted})
			if err != nil {
				t.Fatal(err)
			}
			body := packageFixture(t, "Durable.App", "1.0.0", `<dependencies><group targetFramework="net10.0"><dependency id="Durable.Dependency" version="[1.0.0]"/></group></dependencies>`)
			payload, ct := multipartPackage(t, body)
			w := request(t, f.module, "PUT", "/nuget/"+f.repo.Name+"/api/v2/package/", payload, map[string]string{"X-NuGet-ApiKey": "nuget-test-admin", "Content-Type": ct})
			if w.Code != 201 {
				t.Fatal(w.Body.String())
			}
			source, err := f.store.GetNuGetPublication(t.Context(), f.repo.ID, "durable.app", "1.0.0")
			if err != nil {
				t.Fatal(err)
			}
			source, err = f.store.TransitionNuGetGovernance(t.Context(), repository.NuGetGovernanceChange{RepositoryID: f.repo.ID, PackageID: source.PackageID, Version: source.Version, ExpectedDigest: source.Digest, ExpectedVersion: source.Governance.Version, Actor: "operator", Action: "unlist", Reason: "retain exact restore"})
			if err != nil {
				t.Fatal(err)
			}
			plan := dist.Plan{SourceRepositoryID: f.repo.ID, TargetRepositoryID: target.ID, Items: []dist.Identity{{PackageID: source.PackageID, Version: source.Version, Digest: source.Digest, Size: source.Size, SourceGovernanceVersion: source.Governance.Version}}}
			encoded, _ := json.Marshal(plan)
			w = request(t, f.module.Preview(), "POST", "/api/v2/previews/nuget/replication-jobs", encoded, map[string]string{"Authorization": "Bearer nuget-test-admin", "Idempotency-Key": uuid.NewString()})
			var job dist.Job
			if w.Code != 202 || json.Unmarshal(w.Body.Bytes(), &job) != nil {
				t.Fatalf("job=%d %s", w.Code, w.Body.String())
			}
			crash, cancel := context.WithCancel(t.Context())
			defer cancel()
			if window == "after-target-upload" {
				f.module.objects = &replicationHookObjects{Store: f.objects, hook: cancel}
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
			before, err := f.store.GetNuGetReplication(t.Context(), job.ID)
			if err != nil || before.Items[0].TargetSessionID == "" {
				t.Fatalf("persisted checkpoint=%+v %v", before, err)
			}
			var committed repository.NuGetPublication
			if window == "after-target-upload" {
				if _, err = f.store.GetNuGetPublication(t.Context(), target.ID, source.PackageID, source.Version); !errors.Is(err, repository.ErrNotFound) {
					t.Fatalf("upload prematurely visible=%v", err)
				}
			} else {
				committed, err = f.store.GetNuGetPublication(t.Context(), target.ID, source.PackageID, source.Version)
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
			module := newDurableModule(t, reopened, f.objects)
			module.distribution = reopened
			time.Sleep(time.Until(before.Lease.Until) + 30*time.Millisecond)
			recovered, err := module.ReplicationWorker("new-process")
			if err != nil {
				t.Fatal(err)
			}
			if ran, err := recovered.RunOnce(t.Context()); !ran || err != nil {
				t.Fatalf("recovery=%t %v", ran, err)
			}
			after, err := reopened.GetNuGetReplication(t.Context(), job.ID)
			if err != nil || after.State != "succeeded" || after.Lease.Fence <= before.Lease.Fence {
				t.Fatalf("recovered=%+v %v", after, err)
			}
			publication, err := reopened.GetNuGetPublication(t.Context(), target.ID, source.PackageID, source.Version)
			if err != nil || publication.ObjectKey == source.ObjectKey || publication.SessionID == source.SessionID || publication.Governance.Listed || !strings.Contains(publication.ObjectKey, target.ID) {
				t.Fatalf("target=%+v %v", publication, err)
			}
			if committed.SessionID != "" && (publication.SessionID != committed.SessionID || !publication.PublishedAt.Equal(committed.PublishedAt)) {
				t.Fatal("lost commit response republished item")
			}
			if err = f.objects.Delete(t.Context(), source.ObjectKey); err != nil {
				t.Fatal(err)
			}
			path := "/nuget/" + target.Name + "/v3/flat/durable.app/1.0.0/durable.app.1.0.0.nupkg"
			w = request(t, module, "GET", path, nil, bearer)
			if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), body) {
				t.Fatalf("target depends on source=%d", w.Code)
			}
		})
	}
}
