package capacityplan

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"
)

var testNow = time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)

func number(n int64) *int64 { return &n }

func fixture() Plan {
	digest := "sha256:" + strings.Repeat("a", 64)
	return Plan{
		SchemaVersion: 1, InventoryID: "synthetic-batch", Complete: true, TargetID: "target-a",
		References: []Reference{{RepositoryID: "repo-a", LogicalKey: "blob-a", ObjectKey: "native/oci/blobs/a", Digest: digest, Size: number(10)}},
		Snapshot: Snapshot{
			TargetID: "target-a", ObservedAt: testNow.Add(-time.Minute), ValidUntil: testNow.Add(time.Minute), StoragePoolID: "pool-a", FreeBytes: number(100),
			Repositories: []RepositoryCapacity{{RepositoryID: "repo-a", UsedBytes: number(5), QuotaBytes: number(100)}},
			Objects:      []Presence{{Key: "native/oci/blobs/a", State: "absent"}},
			References:   []Membership{{RepositoryID: "repo-a", Key: "blob-a", State: "absent"}},
		},
		Peak: PeakBudget{StoragePoolID: "pool-a", DownloadBytes: number(11), UploadBytes: number(12), BackupBytes: number(13), RestoreBytes: number(14), HeadroomBytes: number(15)},
	}
}

func TestSnapshotBudgetSeparatesLogicalGrowthAndPeakComponents(t *testing.T) {
	r := Evaluate(fixture(), testNow)
	if r.Status != Sufficient || len(r.Repositories) != 1 || *r.Repositories[0].AdditionalBytes != 10 || *r.Repositories[0].ProjectedBytes != 15 || *r.Storage.AdditionalBytes != 10 || *r.Storage.RequiredFreeBytes != 75 {
		t.Fatalf("report=%+v", r)
	}
}

func TestSharedOCIObjectStillCountsInEachRepository(t *testing.T) {
	p := fixture()
	ref := p.References[0]
	p.References = append(p.References, ref) // duplicate tag-derived reference
	ref.RepositoryID = "repo-b"
	p.References = append(p.References, ref)
	p.Snapshot.Repositories = append(p.Snapshot.Repositories, RepositoryCapacity{RepositoryID: "repo-b", UsedBytes: number(0), QuotaBytes: number(9)})
	p.Snapshot.References = append(p.Snapshot.References, Membership{RepositoryID: "repo-b", Key: "blob-a", State: "absent"})
	r := Evaluate(p, testNow)
	if r.Status != Insufficient || len(r.Repositories) != 2 || *r.Storage.AdditionalBytes != 10 || *r.Repositories[0].AdditionalBytes != 10 || *r.Repositories[1].AdditionalBytes != 10 || r.Repositories[1].Status != Insufficient {
		t.Fatalf("shared object report=%+v", r)
	}
}

func TestDigestAloneDoesNotDeduplicateSeparatePhysicalKeys(t *testing.T) {
	p := fixture()
	ref := p.References[0]
	ref.LogicalKey, ref.ObjectKey = "manifest-b", "native/oci/manifests/repo-a/other/a"
	p.References = append(p.References, ref)
	p.Snapshot.Objects = append(p.Snapshot.Objects, Presence{Key: ref.ObjectKey, State: "absent"})
	p.Snapshot.References = append(p.Snapshot.References, Membership{RepositoryID: "repo-a", Key: ref.LogicalKey, State: "absent"})
	r := Evaluate(p, testNow)
	if r.Status != Sufficient || *r.Storage.AdditionalBytes != 20 || *r.Repositories[0].AdditionalBytes != 20 {
		t.Fatalf("distinct physical keys report=%+v", r)
	}
}

func TestVerifiedPhysicalBytesDoNotEraseNewLogicalMembership(t *testing.T) {
	p := fixture()
	p.Snapshot.Objects[0] = Presence{Key: p.References[0].ObjectKey, State: "verified", Digest: p.References[0].Digest, Size: number(10)}
	r := Evaluate(p, testNow)
	if r.Status != Sufficient || *r.Storage.AdditionalBytes != 0 || *r.Repositories[0].AdditionalBytes != 10 {
		t.Fatalf("existing physical object report=%+v", r)
	}
	p.Snapshot.References[0] = Membership{RepositoryID: "repo-a", Key: "blob-a", State: "verified", Digest: p.References[0].Digest, Size: number(10)}
	p.Snapshot.Repositories[0].UsedBytes = number(15)
	r = Evaluate(p, testNow)
	if r.Status != Sufficient || *r.Repositories[0].AdditionalBytes != 0 || *r.Repositories[0].ProjectedBytes != 15 {
		t.Fatalf("existing logical reference report=%+v", r)
	}
}

func TestUnknownEvidenceNeverBecomesZeroOrSufficient(t *testing.T) {
	cases := map[string]func(*Plan){
		"incomplete inventory":      func(p *Plan) { p.Complete = false },
		"missing inventory entries": func(p *Plan) { p.References = nil },
		"missing identity":          func(p *Plan) { p.InventoryID = "" },
		"unsupported version":       func(p *Plan) { p.SchemaVersion = 2 },
		"target changed":            func(p *Plan) { p.Snapshot.TargetID = "target-b" },
		"pool changed":              func(p *Plan) { p.Peak.StoragePoolID = "pool-b" },
		"expired":                   func(p *Plan) { p.Snapshot.ValidUntil = testNow },
		"future":                    func(p *Plan) { p.Snapshot.ObservedAt = testNow.Add(time.Second) },
		"missing free":              func(p *Plan) { p.Snapshot.FreeBytes = nil },
		"missing used":              func(p *Plan) { p.Snapshot.Repositories[0].UsedBytes = nil },
		"missing quota":             func(p *Plan) { p.Snapshot.Repositories[0].QuotaBytes = nil },
		"negative used":             func(p *Plan) { p.Snapshot.Repositories[0].UsedBytes = number(-1) },
		"negative quota":            func(p *Plan) { p.Snapshot.Repositories[0].QuotaBytes = number(-1) },
		"missing size":              func(p *Plan) { p.References[0].Size = nil },
		"missing backup budget":     func(p *Plan) { p.Peak.BackupBytes = nil },
		"missing headroom":          func(p *Plan) { p.Peak.HeadroomBytes = nil },
		"missing physical presence": func(p *Plan) { p.Snapshot.Objects = nil },
		"missing membership":        func(p *Plan) { p.Snapshot.References = nil },
		"unverified physical":       func(p *Plan) { p.Snapshot.Objects[0].State = "present" },
		"bad physical digest": func(p *Plan) {
			p.Snapshot.Objects[0] = Presence{Key: p.References[0].ObjectKey, State: "verified", Digest: "sha256:" + strings.Repeat("b", 64), Size: number(10)}
		},
		"bad physical size": func(p *Plan) {
			p.Snapshot.Objects[0] = Presence{Key: p.References[0].ObjectKey, State: "verified", Digest: p.References[0].Digest, Size: number(9)}
		},
		"negative free":      func(p *Plan) { p.Snapshot.FreeBytes = number(-1) },
		"negative temporary": func(p *Plan) { p.Peak.UploadBytes = number(-1) },
		"conflicting duplicate object": func(p *Plan) {
			ref := p.References[0]
			ref.Digest = "sha256:" + strings.Repeat("b", 64)
			ref.LogicalKey = "blob-b"
			p.References = append(p.References, ref)
		},
		"duplicate snapshot object":     func(p *Plan) { p.Snapshot.Objects = append(p.Snapshot.Objects, p.Snapshot.Objects[0]) },
		"duplicate snapshot capacity":   func(p *Plan) { p.Snapshot.Repositories = append(p.Snapshot.Repositories, p.Snapshot.Repositories[0]) },
		"duplicate snapshot membership": func(p *Plan) { p.Snapshot.References = append(p.Snapshot.References, p.Snapshot.References[0]) },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			p := fixture()
			mutate(&p)
			r := Evaluate(p, testNow)
			if r.Status != Unknown {
				t.Fatalf("unknown evidence was accepted: %+v", r)
			}
			if len(r.Reasons) == 0 && len(r.Storage.Reasons) == 0 && (len(r.Repositories) == 0 || len(r.Repositories[0].Reasons) == 0) {
				t.Fatal("unknown report has no reason")
			}
		})
	}
}

func TestLogicalAndPhysicalLimitsFailIndependently(t *testing.T) {
	const gib = int64(1 << 30)
	p := fixture()
	p.References[0].Size = number(176888228086) // ceil(164.74 GiB)
	p.Snapshot.Repositories[0].QuotaBytes = number(16 * gib)
	p.Snapshot.FreeBytes = number(300 * gib)
	r := Evaluate(p, testNow)
	if r.Status != Insufficient || r.Repositories[0].Status != Insufficient || r.Storage.Status != Sufficient {
		t.Fatalf("logical quota report=%+v", r)
	}
	p.References[0].Size = number(225 * gib)
	p.Snapshot.Repositories[0].QuotaBytes = number(0) // existing API: quota disabled
	p.Snapshot.FreeBytes = number(178 * gib)
	r = Evaluate(p, testNow)
	if r.Status != Insufficient || r.Repositories[0].Status != Sufficient || r.Storage.Status != Insufficient {
		t.Fatalf("physical space report=%+v", r)
	}
}

func TestPeakIncludesTemporaryAndBackupEvenWhenObjectsAlreadyExist(t *testing.T) {
	p := fixture()
	p.Snapshot.Objects[0] = Presence{Key: p.References[0].ObjectKey, State: "verified", Digest: p.References[0].Digest, Size: number(10)}
	p.Snapshot.FreeBytes = number(64)
	r := Evaluate(p, testNow)
	if r.Status != Insufficient || *r.Storage.AdditionalBytes != 0 || *r.Storage.RequiredFreeBytes != 65 {
		t.Fatalf("peak report=%+v", r)
	}
	p.Snapshot.FreeBytes = number(65)
	if r = Evaluate(p, testNow); r.Status != Sufficient {
		t.Fatalf("exact boundary=%+v", r)
	}
}

func TestOverflowAndConflictingLogicalIdentitiesAreUnknown(t *testing.T) {
	p := fixture()
	p.Peak.BackupBytes = number(math.MaxInt64)
	if r := Evaluate(p, testNow); r.Status != Insufficient || r.Storage.RequiredFreeBytes != nil {
		t.Fatalf("peak overflow=%+v", r)
	}
	p = fixture()
	p.Snapshot.Repositories[0].UsedBytes = number(math.MaxInt64)
	p.Snapshot.Repositories[0].QuotaBytes = number(0)
	if r := Evaluate(p, testNow); r.Status != Unknown || r.Repositories[0].ProjectedBytes != nil {
		t.Fatalf("logical overflow=%+v", r)
	}
	p = fixture()
	ref := p.References[0]
	ref.ObjectKey = "another-object"
	p.References = append(p.References, ref)
	if r := Evaluate(p, testNow); r.Status != Unknown {
		t.Fatalf("conflicting logical identity=%+v", r)
	}
}

func TestVerifiedMembershipCannotExceedRecordedLogicalUsage(t *testing.T) {
	p := fixture()
	p.Snapshot.Objects[0] = Presence{Key: p.References[0].ObjectKey, State: "verified", Digest: p.References[0].Digest, Size: number(10)}
	p.Snapshot.References[0] = Membership{RepositoryID: "repo-a", Key: "blob-a", State: "verified", Digest: p.References[0].Digest, Size: number(10)}
	r := Evaluate(p, testNow)
	if r.Status != Unknown || r.Repositories[0].ProjectedBytes != nil || !strings.Contains(strings.Join(r.Repositories[0].Reasons, ","), "verified_membership_exceeds_usage") {
		t.Fatalf("contradictory logical evidence=%+v", r)
	}
}

func TestKnownMinimumPreservesFailureWhenOtherQuantitiesAreUnknown(t *testing.T) {
	p := fixture()
	p.Snapshot.FreeBytes = number(9)
	p.Peak.HeadroomBytes = nil
	r := Evaluate(p, testNow)
	if r.Status != Insufficient || r.Storage.RequiredFreeBytes != nil || r.Storage.KnownMinimumFreeBytes == nil || *r.Storage.KnownMinimumFreeBytes != 60 {
		t.Fatalf("known physical minimum lost=%+v", r)
	}
	p = fixture()
	ref := p.References[0]
	ref.LogicalKey, ref.ObjectKey = "unknown-blob", "unknown-object"
	p.References = append(p.References, ref)
	p.Snapshot.Repositories[0].QuotaBytes = number(9)
	r = Evaluate(p, testNow)
	if r.Status != Insufficient || r.Repositories[0].AdditionalBytes != nil || r.Repositories[0].ProjectedBytes != nil || *r.Repositories[0].KnownMinimumProjectedBytes != 15 || r.Storage.AdditionalBytes != nil {
		t.Fatalf("known logical minimum lost=%+v", r)
	}
}

func TestUnknownNumbersSerializeAsNullAndEvaluationIsReadOnly(t *testing.T) {
	p := fixture()
	p.Snapshot.FreeBytes = nil
	before, _ := json.Marshal(p)
	r := Evaluate(p, testNow)
	after, _ := json.Marshal(p)
	encoded, err := json.Marshal(r)
	if err != nil || string(before) != string(after) || !strings.Contains(string(encoded), `"freeBytes":null`) {
		t.Fatalf("input mutated or unknown coerced: %s; %v", encoded, err)
	}
}

func TestUnknownUsageRetainsKnownLogicalLowerBound(t *testing.T) {
	p := fixture()
	p.Snapshot.Repositories[0].UsedBytes = nil
	p.Snapshot.Repositories[0].QuotaBytes = number(9)
	r := Evaluate(p, testNow)
	repo := r.Repositories[0]
	if r.Status != Insufficient || repo.ProjectedBytes != nil || repo.KnownMinimumProjectedBytes == nil || *repo.KnownMinimumProjectedBytes != 10 {
		t.Fatalf("known new bytes lost with unknown usage: %+v", repo)
	}
	p.Snapshot.Repositories[0].QuotaBytes = number(10)
	if r = Evaluate(p, testNow); r.Status != Unknown || r.Repositories[0].ProjectedBytes != nil {
		t.Fatalf("a fitting minimum cannot establish a complete total: %+v", r)
	}

	ref := p.References[0]
	ref.LogicalKey, ref.ObjectKey = "existing-blob", "existing-object"
	ref.Size = number(20)
	p.References = append(p.References, ref)
	p.Snapshot.Objects = append(p.Snapshot.Objects, Presence{Key: ref.ObjectKey, State: "verified", Digest: ref.Digest, Size: number(20)})
	p.Snapshot.References = append(p.Snapshot.References, Membership{RepositoryID: ref.RepositoryID, Key: ref.LogicalKey, State: "verified", Digest: ref.Digest, Size: number(20)})
	p.Snapshot.Repositories[0].QuotaBytes = number(29)
	r = Evaluate(p, testNow)
	if r.Status != Insufficient || r.Repositories[0].KnownMinimumProjectedBytes == nil || *r.Repositories[0].KnownMinimumProjectedBytes != 30 || r.Repositories[0].UsedBytes != nil || r.Repositories[0].ProjectedBytes != nil {
		t.Fatalf("verified membership lower bound lost: %+v", r.Repositories[0])
	}
}

func TestUnknownUsagePreservesOverflowingVerifiedLowerBound(t *testing.T) {
	p := fixture()
	p.Snapshot.Repositories[0].UsedBytes = nil
	p.Snapshot.Repositories[0].QuotaBytes = number(math.MaxInt64)
	p.References[0].Size = number(math.MaxInt64)
	ref := p.References[0]
	ref.LogicalKey, ref.ObjectKey, ref.Size = "second-existing", "second-object", number(1)
	p.References = append(p.References, ref)
	p.Snapshot.Objects, p.Snapshot.References = nil, nil
	for _, ref := range p.References {
		p.Snapshot.Objects = append(p.Snapshot.Objects, Presence{Key: ref.ObjectKey, State: "verified", Digest: ref.Digest, Size: ref.Size})
		p.Snapshot.References = append(p.Snapshot.References, Membership{RepositoryID: ref.RepositoryID, Key: ref.LogicalKey, State: "verified", Digest: ref.Digest, Size: ref.Size})
	}
	r := Evaluate(p, testNow)
	if r.Status != Insufficient || r.Repositories[0].ProjectedBytes != nil || r.Repositories[0].KnownMinimumProjectedBytes != nil || r.Storage.Status != Sufficient {
		t.Fatalf("verified lower bound overflow lost: %+v", r)
	}
	p.Snapshot.Repositories[0].QuotaBytes = number(0)
	if r = Evaluate(p, testNow); r.Status != Unknown {
		t.Fatalf("disabled quota must not establish unknown complete usage: %+v", r)
	}
	p.Snapshot.Repositories[0].UsedBytes = number(math.MaxInt64)
	if r = Evaluate(p, testNow); r.Status != Unknown || !strings.Contains(strings.Join(r.Repositories[0].Reasons, ","), "verified_membership_exceeds_usage") {
		t.Fatalf("verified lower bound contradicts known usage: %+v", r)
	}
}
