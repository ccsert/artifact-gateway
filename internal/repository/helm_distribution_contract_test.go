package repository

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	dist "github.com/ccsert/artifact-gateway/internal/helmdistribution"
	"github.com/google/uuid"
)

type helmReplicationContractStore interface {
	dist.Store
	NativeHelmStore
	HostedRepositoryStore
	RepositoryCapacityStore
}
type helmReplicationContractFixture struct {
	source, target HostedRepository
	publication    HelmPublication
	job            dist.Job
	check          dist.Check
}

func helmReplicationContractSetup(t *testing.T, store helmReplicationContractStore) helmReplicationContractFixture {
	t.Helper()
	ctx := t.Context()
	var f helmReplicationContractFixture
	var err error
	for _, r := range []*HostedRepository{&f.source, &f.target} {
		*r, err = store.CreateHostedRepository(ctx, HostedRepository{ID: uuid.NewString(), Name: "replication-" + uuid.NewString(), Format: FormatHelm, Type: RepositoryTypeHosted})
		if err != nil {
			t.Fatal(err)
		}
	}
	session, _, err := store.ReserveHelmPublication(ctx, HelmSession{ID: uuid.NewString(), RepositoryID: f.source.ID, Name: "acme", Version: "1.0.0", Digest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Size: 17, ProvenanceDigest: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", ProvenanceSize: 5, Actor: "publisher", ExpiresAt: time.Now().Add(time.Hour), MetadataJSON: []byte(`{"name":"acme","version":"1.0.0"}`), ChartYAML: []byte("name: acme\nversion: 1.0.0\n")})
	if err != nil {
		t.Fatal(err)
	}
	f.publication, _, err = store.CommitHelmPublication(ctx, session.ID, session.Actor)
	if err != nil {
		t.Fatal(err)
	}
	f.job, err = dist.NewJob(dist.Plan{SourceRepositoryID: f.source.ID, TargetRepositoryID: f.target.ID, Items: []dist.Identity{{Name: "acme", Version: "1.0.0", Digest: session.Digest, Size: session.Size, ProvenanceDigest: session.ProvenanceDigest, ProvenanceSize: session.ProvenanceSize, SourceGovernanceVersion: "1"}}}, "operator", "static_admin", "", uuid.NewString())
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
func helmReplicationContractStage(t *testing.T, store helmReplicationContractStore, f helmReplicationContractFixture, claim dist.Job) HelmSession {
	t.Helper()
	session, _, err := store.ReserveHelmPublication(t.Context(), HelmSession{ID: uuid.NewString(), RepositoryID: f.target.ID, Name: "acme", Version: "1.0.0", Digest: f.publication.Digest, Size: f.publication.Size, ProvenanceDigest: f.publication.ProvenanceDigest, ProvenanceSize: f.publication.ProvenanceSize, Actor: f.job.Actor, ExpiresAt: time.Now().Add(time.Hour), MetadataJSON: f.publication.MetadataJSON, ChartYAML: f.publication.ChartYAML})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.BindHelmReplicationSession(t.Context(), claim.Lease, 0, session.ID, f.check); err != nil {
		t.Fatal(err)
	}
	return session
}

func TestHelmReplicationMemoryStoreContract(t *testing.T) {
	testHelmReplicationStoreContract(t, NewMemoryStore())
}
func testHelmReplicationStoreContract(t *testing.T, store helmReplicationContractStore) {
	t.Helper()
	f := helmReplicationContractSetup(t, store)
	ctx := t.Context()
	j, replay, err := store.CreateHelmReplication(ctx, f.job, f.check)
	if err != nil || replay || j.State != "queued" {
		t.Fatalf("create=%+v %t %v", j, replay, err)
	}
	same, replay, err := store.CreateHelmReplication(ctx, f.job, f.check)
	if err != nil || !replay || same.ID != j.ID || same.Version != j.Version {
		t.Fatalf("replay=%+v %t %v", same, replay, err)
	}
	modified := dist.Clone(f.job)
	modified.Items[0].Size++
	p := dist.Plan{SourceRepositoryID: modified.SourceRepositoryID, TargetRepositoryID: modified.TargetRepositoryID, Items: []dist.Identity{modified.Items[0].Identity}}
	_, modified.PlanDigest, _ = dist.CanonicalPlan(p)
	if _, _, err = store.CreateHelmReplication(ctx, modified, f.check); !errors.Is(err, dist.ErrConflict) {
		t.Fatalf("different plan accepted=%v", err)
	}
	deny := func(context.Context, dist.Authority) error { return dist.ErrAccessDenied }
	if _, _, err = store.CreateHelmReplication(ctx, f.job, deny); !errors.Is(err, dist.ErrAccessDenied) {
		t.Fatalf("replay bypassed authority=%v", err)
	}
	var wg sync.WaitGroup
	claims := make(chan dist.Job, 12)
	failures := make(chan error, 12)
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, e := store.ClaimHelmReplication(ctx, uuid.NewString(), time.Minute)
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
	session := helmReplicationContractStage(t, store, f, claim)
	// Fencing must protect publication and the item checkpoint together.
	stale := claim.Lease
	stale.Token = uuid.NewString()
	if _, err = store.CompleteHelmReplicationItem(ctx, stale, 0, dist.Commit{SessionID: session.ID}, f.check); !errors.Is(err, dist.ErrLeaseLost) {
		t.Fatalf("stale commit=%v", err)
	}
	if _, err = store.GetHelmPublication(ctx, f.target.ID, "acme", "1.0.0"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("stale visible=%v", err)
	}
	if _, err = store.CompleteHelmReplicationItem(ctx, claim.Lease, 0, dist.Commit{SessionID: session.ID}, deny); !errors.Is(err, dist.ErrAccessDenied) {
		t.Fatalf("revoked final commit=%v", err)
	}
	result, err := store.CompleteHelmReplicationItem(ctx, claim.Lease, 0, dist.Commit{SessionID: session.ID}, f.check)
	if err != nil || result.Items[0].State != "succeeded" {
		t.Fatalf("commit=%+v %v", result, err)
	}
	ptarget, err := store.GetHelmPublication(ctx, f.target.ID, "acme", "1.0.0")
	if err != nil || ptarget.SessionID == f.publication.SessionID || ptarget.ObjectKey == f.publication.ObjectKey || ptarget.ProvenanceKey == f.publication.ProvenanceKey || ptarget.ProvenanceDigest != f.publication.ProvenanceDigest || ptarget.Digest != f.publication.Digest || ptarget.Governance.Tombstoned {
		t.Fatalf("target=%+v err=%v", ptarget, err)
	}
	replayed, err := store.CompleteHelmReplicationItem(ctx, claim.Lease, 0, dist.Commit{SessionID: session.ID}, f.check)
	if err != nil || replayed.Version != result.Version {
		t.Fatalf("lost reply=%+v err=%v", replayed, err)
	}
	finished, err := store.FinishHelmReplication(ctx, claim.Lease)
	if err != nil || finished.State != "succeeded" || finished.Lease.Token != "" {
		t.Fatalf("finish=%+v %v", finished, err)
	}
	if _, _, err = store.RetryHelmReplication(ctx, j.ID, finished.Version, []int{0}, "completed", f.check); !errors.Is(err, dist.ErrConflict) {
		t.Fatalf("completed retried=%v", err)
	}
}
