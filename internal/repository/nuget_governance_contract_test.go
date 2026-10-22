package repository

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

type nuGetGovernanceContractStore interface {
	nuGetContractStore
	AuditStore
}

func seedNuGetGovernance(t *testing.T, store nuGetContractStore) (HostedRepository, NuGetPublication) {
	t.Helper()
	repo, err := store.CreateHostedRepository(t.Context(), HostedRepository{ID: uuid.NewString(), Name: "nuget-governance-" + uuid.NewString(), Format: FormatNuGet, Type: RepositoryTypeHosted})
	if err != nil {
		t.Fatal(err)
	}
	in := NuGetSession{ID: uuid.NewString(), RepositoryID: repo.ID, PackageID: "acme.governed", Version: "1.0.0", Digest: "sha256:" + fmt.Sprintf("%064x", 449), Size: 60, Actor: "publisher", ExpiresAt: time.Now().UTC().Add(time.Hour)}
	in, _, err = store.ReserveNuGetPublication(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}
	in.MetadataJSON, in.Nuspec = []byte(`{"id":"Acme.Governed","version":"1.0.0"}`), []byte("original nuspec")
	if _, err = store.StageNuGetPublication(t.Context(), in); err != nil {
		t.Fatal(err)
	}
	p, _, err := store.CommitNuGetPublication(t.Context(), in.ID, in.Actor)
	if err != nil {
		t.Fatal(err)
	}
	return repo, p
}

func TestNuGetMemoryGovernanceStoreContract(t *testing.T) {
	testNuGetGovernanceStoreContract(t, NewMemoryStore())
}

func testNuGetGovernanceStoreContract(t *testing.T, store nuGetGovernanceContractStore) {
	t.Helper()
	repo, p := seedNuGetGovernance(t, store)
	ctx := t.Context()
	in := NuGetGovernanceChange{RepositoryID: repo.ID, PackageID: p.PackageID, Version: p.Version, ExpectedDigest: p.Digest, ExpectedVersion: "1", Action: "quarantine", Actor: "operator", Reason: "Confirmed fixture decision"}
	var wg sync.WaitGroup
	results := make(chan error, 12)
	for range 12 {
		wg.Go(func() {
			_, err := store.TransitionNuGetGovernance(ctx, in)
			results <- err
		})
	}
	wg.Wait()
	close(results)
	winners := 0
	for err := range results {
		if err == nil {
			winners++
		} else if !errors.Is(err, ErrVersionConflict) {
			t.Fatalf("CAS mutation=%v", err)
		}
	}
	if winners != 1 {
		t.Fatalf("concurrent winners=%d", winners)
	}
	p, err := store.GetNuGetPublication(ctx, repo.ID, p.PackageID, p.Version)
	if err != nil || !p.Governance.Quarantined || p.Governance.Version != "2" || p.Governance.UpdatedBy != "operator" {
		t.Fatalf("durable quarantine=%+v %v", p.Governance, err)
	}
	audits, err := store.ListAudits(ctx, AuditQuery{Repository: repo.Name, Operation: "nuget.governance.quarantine", Limit: 100})
	if err != nil || len(audits) != 1 || audits[0].Evidence["digest"] != p.Digest || audits[0].Evidence["governanceVersion"] != "2" {
		t.Fatalf("CAS atomic audit=%+v %v", audits, err)
	}
	in.ExpectedVersion = "2"
	if replay, err := store.TransitionNuGetGovernance(ctx, in); err != nil || replay.Governance.Version != "2" {
		t.Fatalf("no-op replay=%+v %v", replay.Governance, err)
	}
	in.ExpectedDigest = "sha256:" + fmt.Sprintf("%064x", 450)
	if _, err := store.TransitionNuGetGovernance(ctx, in); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("wrong immutable digest=%v", err)
	}
	in.ExpectedDigest = p.Digest
	for _, action := range []string{"delete", "release", "restore", "unlist", "relist"} {
		in.Action, in.ExpectedVersion = action, p.Governance.Version
		if p, err = store.TransitionNuGetGovernance(ctx, in); err != nil {
			t.Fatalf("%s=%v", action, err)
		}
	}
	if p.Governance.Tombstoned || p.Governance.Quarantined || !p.Governance.Listed || p.Digest != in.ExpectedDigest {
		t.Fatalf("restored state=%+v", p)
	}
	capacity, err := store.GetRepositoryCapacity(ctx, repo.ID)
	if err != nil || capacity.UsedBytes != 60 || capacity.ObjectCount != 1 {
		t.Fatalf("retained quota accounting=%+v %v", capacity, err)
	}
	if _, err = store.ReplaceRepositoryCapacityQuota(ctx, repo.ID, 60); err != nil {
		t.Fatal(err)
	}
	in.Action, in.ExpectedVersion = "delete", p.Governance.Version
	if _, err = store.TransitionNuGetGovernance(ctx, in); err != nil {
		t.Fatal(err)
	}
	next := NuGetSession{ID: uuid.NewString(), RepositoryID: repo.ID, PackageID: "another", Version: "1.0.0", Digest: in.ExpectedDigest, Size: 1, Actor: "publisher", ExpiresAt: time.Now().UTC().Add(time.Hour)}
	if _, _, err = store.ReserveNuGetPublication(ctx, next); !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("tombstone bypassed quota=%v", err)
	}
	policy, err := store.ReplaceNuGetQuarantineReadPolicy(ctx, repo.ID, RepositoryQuarantineReadPolicy{Enabled: true}, "1", "operator")
	if err != nil || !policy.Enabled || policy.Version != "2" {
		t.Fatalf("read policy=%+v %v", policy, err)
	}
	if _, err = store.ReplaceNuGetQuarantineReadPolicy(ctx, repo.ID, RepositoryQuarantineReadPolicy{}, "1", "operator"); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("policy stale version=%v", err)
	}
}
