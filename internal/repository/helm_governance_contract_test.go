package repository

import (
	"bytes"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

type helmGovernanceContractStore interface {
	helmContractStore
	HelmGovernanceStore
	AuditStore
}

func seedHelmGovernance(t *testing.T, store helmContractStore, kind RepositoryType) (HostedRepository, HelmPublication) {
	t.Helper()
	repo := HostedRepository{ID: uuid.NewString(), Name: "helm-governance-" + uuid.NewString(), Format: FormatHelm, Type: kind}
	if kind == RepositoryTypeProxy {
		repo.Endpoint = "https://owned.example.invalid/index.yaml"
	}
	repo, err := store.CreateHostedRepository(t.Context(), repo)
	if err != nil {
		t.Fatal(err)
	}
	in := HelmSession{ID: uuid.NewString(), RepositoryID: repo.ID, Name: "governed", Version: "1.0.0", Digest: "sha256:" + fmt.Sprintf("%064x", 457), Size: 60, ProvenanceDigest: "sha256:" + fmt.Sprintf("%064x", 458), ProvenanceSize: 10, Actor: "publisher", MetadataJSON: []byte(`{"name":"governed","version":"1.0.0"}`), ChartYAML: []byte("original chart metadata"), ExpiresAt: time.Now().UTC().Add(time.Hour)}
	if kind == RepositoryTypeProxy {
		in.SourceVersion = repo.Version
	}
	in, _, err = store.ReserveHelmPublication(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}
	p, _, err := store.CommitHelmPublication(t.Context(), in.ID, in.Actor)
	if err != nil {
		t.Fatal(err)
	}
	return repo, p
}

func TestHelmMemoryGovernanceStoreContract(t *testing.T) {
	testHelmGovernanceStoreContract(t, NewMemoryStore())
}

func testHelmGovernanceStoreContract(t *testing.T, store helmGovernanceContractStore) {
	t.Helper()
	for _, kind := range []RepositoryType{RepositoryTypeHosted, RepositoryTypeProxy} {
		t.Run(string(kind), func(t *testing.T) {
			repo, original := seedHelmGovernance(t, store, kind)
			ctx := t.Context()
			in := HelmGovernanceChange{RepositoryID: repo.ID, Name: original.Name, Version: original.Version, ExpectedDigest: original.Digest, ExpectedVersion: "1", Action: "quarantine", Actor: "operator", Reason: "Confirmed fixture quarantine"}
			var wg sync.WaitGroup
			results := make(chan error, 12)
			for range 12 {
				wg.Go(func() {
					_, err := store.TransitionHelmGovernance(ctx, in)
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
			p, err := store.GetHelmPublication(ctx, repo.ID, original.Name, original.Version)
			if err != nil || !p.Governance.Quarantined || p.Governance.Version != "2" || p.Governance.UpdatedBy != "operator" {
				t.Fatalf("durable quarantine=%+v %v", p.Governance, err)
			}
			audits, err := store.ListAudits(ctx, AuditQuery{Repository: repo.Name, Operation: "helm.governance.quarantine", Limit: 100})
			if err != nil || len(audits) != 1 || audits[0].Evidence["digest"] != p.Digest || audits[0].Evidence["provenanceDigest"] != p.ProvenanceDigest || audits[0].Evidence["governanceVersion"] != "2" || audits[0].Evidence["reason"] != in.Reason {
				t.Fatalf("atomic audit=%+v %v", audits, err)
			}
			in.ExpectedVersion = "2"
			if replay, err := store.TransitionHelmGovernance(ctx, in); err != nil || replay.Governance != p.Governance {
				t.Fatalf("no-op=%+v %v", replay.Governance, err)
			}
			in.ExpectedDigest = "sha256:" + fmt.Sprintf("%064x", 459)
			if _, err := store.TransitionHelmGovernance(ctx, in); !errors.Is(err, ErrVersionConflict) {
				t.Fatalf("different digest=%v", err)
			}
			in.ExpectedDigest = p.Digest
			for _, action := range []string{"delete", "release", "restore", "quarantine"} {
				in.Action, in.ExpectedVersion = action, p.Governance.Version
				if p, err = store.TransitionHelmGovernance(ctx, in); err != nil {
					t.Fatalf("%s=%v", action, err)
				}
			}
			if p.Governance.Tombstoned || !p.Governance.Quarantined || p.Digest != original.Digest || p.ObjectKey != original.ObjectKey || p.ProvenanceKey != original.ProvenanceKey || p.SessionID != original.SessionID || p.Publisher != original.Publisher || !p.PublishedAt.Equal(original.PublishedAt) || !bytes.Equal(p.MetadataJSON, original.MetadataJSON) || !bytes.Equal(p.ChartYAML, original.ChartYAML) {
				t.Fatalf("governance replaced immutable closure=%+v", p)
			}
			policy, err := store.ReplaceHelmQuarantineReadPolicy(ctx, repo.ID, RepositoryQuarantineReadPolicy{Enabled: true}, "1", "operator")
			if err != nil || !policy.Enabled || policy.Version != "2" {
				t.Fatalf("independent policy=%+v %v", policy, err)
			}
			if _, err = store.ReplaceHelmQuarantineReadPolicy(ctx, repo.ID, RepositoryQuarantineReadPolicy{}, "1", "operator"); !errors.Is(err, ErrVersionConflict) {
				t.Fatalf("stale policy=%v", err)
			}
			if policy, err = store.ReplaceHelmQuarantineReadPolicy(ctx, repo.ID, policy, "2", "operator"); err != nil || policy.Version != "2" {
				t.Fatalf("no-op policy=%+v %v", policy, err)
			}
			in.Action, in.ExpectedVersion = "delete", p.Governance.Version
			if _, err = store.TransitionHelmGovernance(ctx, in); err != nil {
				t.Fatal(err)
			}
			capacity, err := store.GetRepositoryCapacity(ctx, repo.ID)
			if err != nil || capacity.UsedBytes != 70 || capacity.ObjectCount != 2 {
				t.Fatalf("retained quota=%+v %v", capacity, err)
			}
			if _, err = store.ReplaceRepositoryCapacityQuota(ctx, repo.ID, 70); err != nil {
				t.Fatal(err)
			}
			next := HelmSession{ID: uuid.NewString(), RepositoryID: repo.ID, Name: "another", Version: "1.0.0", Digest: original.Digest, Size: 1, Actor: "publisher", MetadataJSON: []byte(`{"name":"another","version":"1.0.0"}`), ChartYAML: []byte("original"), ExpiresAt: time.Now().UTC().Add(time.Hour)}
			if kind == RepositoryTypeProxy {
				next.SourceVersion = repo.Version
			}
			if _, _, err = store.ReserveHelmPublication(ctx, next); !errors.Is(err, ErrQuotaExceeded) {
				t.Fatalf("tombstone bypassed quota=%v", err)
			}
		})
	}
}
