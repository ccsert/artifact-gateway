package repository

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	dist "github.com/ccsert/artifact-gateway/internal/nugetdistribution"
	"github.com/google/uuid"
)

type nuGetReplicationContractStore interface {
	dist.Store
	NativeNuGetStore
	HostedRepositoryStore
	RepositoryCapacityStore
}
type replicationContractFixture struct {
	source, target HostedRepository
	publication    NuGetPublication
	job            dist.Job
	check          dist.Check
}

func replicationContractSetup(t *testing.T, store nuGetReplicationContractStore) replicationContractFixture {
	t.Helper()
	ctx := t.Context()
	var f replicationContractFixture
	var err error
	for _, r := range []*HostedRepository{&f.source, &f.target} {
		*r, err = store.CreateHostedRepository(ctx, HostedRepository{ID: uuid.NewString(), Name: "replication-" + uuid.NewString(), Format: FormatNuGet, Type: RepositoryTypeHosted})
		if err != nil {
			t.Fatal(err)
		}
	}
	session, _, err := store.ReserveNuGetPublication(ctx, NuGetSession{ID: uuid.NewString(), RepositoryID: f.source.ID, PackageID: "acme", Version: "1.0.0", Digest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Size: 17, Actor: "publisher", ExpiresAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	session.Nuspec = []byte("original nuspec")
	session.MetadataJSON = []byte(`{"id":"Acme","version":"1.0.0","dependencyGroups":[{"targetFramework":"net10.0","dependencies":[{"id":"dependency","range":"[1.0.0]"}]}]}`)
	if session, err = store.StageNuGetPublication(ctx, session); err != nil {
		t.Fatal(err)
	}
	f.publication, _, err = store.CommitNuGetPublication(ctx, session.ID, session.Actor)
	if err != nil {
		t.Fatal(err)
	}
	f.job, err = dist.NewJob(dist.Plan{SourceRepositoryID: f.source.ID, TargetRepositoryID: f.target.ID, Items: []dist.Identity{{PackageID: "acme", Version: "1.0.0", Digest: session.Digest, Size: session.Size, SourceGovernanceVersion: "1"}}}, "operator", "static_admin", "", uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	f.check = func(ctx context.Context, a dist.Authority) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if a.Actor != "operator" || a.Kind != "static_admin" || !a.Active || a.Source.ID != f.source.ID || a.Target.ID != f.target.ID {
			return dist.ErrAccessDenied
		}
		return nil
	}
	return f
}
func replicationContractStage(t *testing.T, store nuGetReplicationContractStore, f replicationContractFixture, claim dist.Job) NuGetSession {
	t.Helper()
	session, _, err := store.ReserveNuGetPublication(t.Context(), NuGetSession{ID: uuid.NewString(), RepositoryID: f.target.ID, PackageID: "acme", Version: "1.0.0", Digest: f.publication.Digest, Size: f.publication.Size, Actor: f.job.Actor, ExpiresAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.BindNuGetReplicationSession(t.Context(), claim.Lease, 0, session.ID, f.check); err != nil {
		t.Fatal(err)
	}
	session.Nuspec = f.publication.Nuspec
	session.MetadataJSON = append(append([]byte(nil), f.publication.MetadataJSON[:len(f.publication.MetadataJSON)-1]...), []byte(`,"listed":true}`)...)
	session, err = store.StageNuGetPublication(t.Context(), session)
	if err != nil {
		t.Fatal(err)
	}
	return session
}
func TestNuGetReplicationMemoryStoreContract(t *testing.T) {
	testNuGetReplicationStoreContract(t, NewMemoryStore())
}
func testNuGetReplicationStoreContract(t *testing.T, store nuGetReplicationContractStore) {
	t.Helper()
	f := replicationContractSetup(t, store)
	ctx := t.Context()
	j, replay, err := store.CreateNuGetReplication(ctx, f.job, f.check)
	if err != nil || replay || j.State != "queued" {
		t.Fatalf("create=%+v %t %v", j, replay, err)
	}
	same, replay, err := store.CreateNuGetReplication(ctx, f.job, f.check)
	if err != nil || !replay || same.ID != j.ID || same.Version != j.Version {
		t.Fatalf("replay=%+v %t %v", same, replay, err)
	}
	modified := dist.Clone(f.job)
	modified.Items[0].Size++
	p := dist.Plan{SourceRepositoryID: modified.SourceRepositoryID, TargetRepositoryID: modified.TargetRepositoryID, Items: []dist.Identity{modified.Items[0].Identity}}
	_, modified.PlanDigest, _ = dist.CanonicalPlan(p)
	if _, _, err = store.CreateNuGetReplication(ctx, modified, f.check); !errors.Is(err, dist.ErrConflict) {
		t.Fatalf("different plan accepted=%v", err)
	}
	deny := func(context.Context, dist.Authority) error { return dist.ErrAccessDenied }
	if _, _, err = store.CreateNuGetReplication(ctx, f.job, deny); !errors.Is(err, dist.ErrAccessDenied) {
		t.Fatalf("replay bypassed authority=%v", err)
	}
	var wg sync.WaitGroup
	claims := make(chan dist.Job, 12)
	failures := make(chan error, 12)
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, e := store.ClaimNuGetReplication(ctx, uuid.NewString(), time.Minute)
			if e == nil {
				claims <- c
			} else if !errors.Is(e, dist.ErrNotFound) {
				failures <- e
			}
		}()
	}
	wg.Wait()
	close(claims)
	close(failures)
	for e := range failures {
		t.Fatal(e)
	}
	if len(claims) != 1 {
		t.Fatalf("concurrent owners=%d", len(claims))
	}
	claim := <-claims
	session := replicationContractStage(t, store, f, claim)
	// Fencing must protect publication and the item checkpoint together.
	stale := claim.Lease
	stale.Token = uuid.NewString()
	if _, err = store.CompleteNuGetReplicationItem(ctx, stale, 0, dist.Commit{SessionID: session.ID}, f.check); !errors.Is(err, dist.ErrLeaseLost) {
		t.Fatalf("stale commit=%v", err)
	}
	if _, err = store.GetNuGetPublication(ctx, f.target.ID, "acme", "1.0.0"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("stale visible=%v", err)
	}
	if _, err = store.CompleteNuGetReplicationItem(ctx, claim.Lease, 0, dist.Commit{SessionID: session.ID}, deny); !errors.Is(err, dist.ErrAccessDenied) {
		t.Fatalf("revoked final commit=%v", err)
	}
	result, err := store.CompleteNuGetReplicationItem(ctx, claim.Lease, 0, dist.Commit{SessionID: session.ID}, f.check)
	if err != nil || result.Items[0].State != "succeeded" {
		t.Fatalf("commit=%+v %v", result, err)
	}
	ptarget, err := store.GetNuGetPublication(ctx, f.target.ID, "acme", "1.0.0")
	if err != nil || ptarget.SessionID == f.publication.SessionID || ptarget.ObjectKey == f.publication.ObjectKey || ptarget.Digest != f.publication.Digest || !ptarget.Governance.Listed {
		t.Fatalf("target=%+v err=%v", ptarget, err)
	}
	replayed, err := store.CompleteNuGetReplicationItem(ctx, claim.Lease, 0, dist.Commit{SessionID: session.ID}, f.check)
	if err != nil || replayed.Version != result.Version {
		t.Fatalf("lost reply=%+v err=%v", replayed, err)
	}
	finished, err := store.FinishNuGetReplication(ctx, claim.Lease)
	if err != nil || finished.State != "succeeded" || finished.Lease.Token != "" {
		t.Fatalf("finish=%+v %v", finished, err)
	}
	if _, _, err = store.RetryNuGetReplication(ctx, j.ID, finished.Version, []int{0}, "completed", f.check); !errors.Is(err, dist.ErrConflict) {
		t.Fatalf("completed retried=%v", err)
	}
}
