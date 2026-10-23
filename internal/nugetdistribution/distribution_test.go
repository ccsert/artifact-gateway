package nugetdistribution_test

import (
	"errors"
	"strings"
	"testing"

	dist "github.com/ccsert/artifact-gateway/internal/nugetdistribution"
	"github.com/google/uuid"
)

func TestNuGetReplicationCanonicalPlanIdentityAndBounds(t *testing.T) {
	p := dist.Plan{SourceRepositoryID: uuid.NewString(), TargetRepositoryID: uuid.NewString(), Items: []dist.Identity{{PackageID: "z", Version: "1.0.0", Digest: "sha256:" + strings.Repeat("a", 64), Size: 1, SourceGovernanceVersion: "1"}, {PackageID: "a", Version: "1.0.0", Digest: "sha256:" + strings.Repeat("b", 64), Size: 1, SourceGovernanceVersion: "1"}}}
	normalized, d, err := dist.CanonicalPlan(p)
	if err != nil || normalized.Items[0].PackageID != "a" {
		t.Fatalf("canonical=%+v %v", normalized, err)
	}
	reverse := p
	reverse.Items = []dist.Identity{p.Items[1], p.Items[0]}
	_, other, err := dist.CanonicalPlan(reverse)
	if err != nil || other != d || p.Items[0].PackageID != "z" {
		t.Fatal("order changed plan identity or mutated caller")
	}
	for _, field := range []string{"digest", "governance", "size", "duplicate"} {
		bad := p
		bad.Items = append([]dist.Identity(nil), p.Items...)
		switch field {
		case "digest":
			bad.Items[0].Digest = "sha256:" + strings.Repeat("A", 64)
		case "governance":
			bad.Items[0].SourceGovernanceVersion = "01"
		case "size":
			bad.Items[0].Size = 0
		case "duplicate":
			bad.Items = append(bad.Items, bad.Items[0])
		}
		if _, _, err = dist.CanonicalPlan(bad); !errors.Is(err, dist.ErrInvalid) {
			t.Fatalf("accepted %s", field)
		}
	}
	if _, err = dist.NewJob(p, "operator", "unknown", "", "key"); !errors.Is(err, dist.ErrInvalid) {
		t.Fatal("unknown durable subject accepted")
	}
	if _, err = dist.NewJob(p, "service-account:"+uuid.NewString(), "service_account_credential", "", "key"); !errors.Is(err, dist.ErrInvalid) {
		t.Fatal("service actor without revocable credential accepted")
	}
	a, _ := dist.NewJob(p, "operator", "static_admin", "", "key")
	b, _ := dist.NewJob(reverse, "operator", "static_admin", "", "key")
	if a.ID != b.ID || a.PlanDigest != b.PlanDigest || !dist.ValidNewJob(a) {
		t.Fatal("canonical idempotency mismatch")
	}
	a.Items[0].State = "succeeded"
	if dist.ValidNewJob(a) {
		t.Fatal("client supplied checkpoint accepted")
	}
}
