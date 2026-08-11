package repository

import (
	"context"
	"reflect"
	"testing"
)

func TestEvaluateRepositorySecurityPolicy(t *testing.T) {
	disabled := EvaluateRepositorySecurityPolicy(DefaultRepositorySecurityPolicy(), nil)
	if !disabled.Allowed || disabled.Enforced || !reflect.DeepEqual(disabled.Reasons, []string{"policy_disabled"}) {
		t.Fatalf("disabled evaluation=%#v", disabled)
	}

	policy := DefaultRepositorySecurityPolicy()
	policy.Enabled = true
	policy.RequireSignature = true
	policy.RequireVerifiedSignature = true
	policy.RequireSBOM = true
	policy.RequireProvenance = true
	policy.RequireVulnerabilityScan = true
	policy.AllowedLicenses = []string{"Apache-2.0"}
	missing := EvaluateRepositorySecurityPolicy(policy, nil)
	wantMissing := []string{"signature_required", "verified_signature_required", "sbom_required", "provenance_required", "vulnerability_scan_required", "license_required"}
	if missing.Allowed || missing.IntelligencePresent || !reflect.DeepEqual(missing.Reasons, wantMissing) {
		t.Fatalf("missing evaluation=%#v want reasons=%#v", missing, wantMissing)
	}

	policy.RequireSignature = false
	policy.RequireVerifiedSignature = false
	policy.RequireSBOM = false
	policy.RequireProvenance = false
	policy.RequireVulnerabilityScan = false
	policy.MaxAllowedSeverity = SecuritySeverityLow
	intelligence := ArtifactIntelligence{
		Licenses: []ArtifactLicense{{SPDXID: "MIT"}},
		Vulnerability: &ArtifactVulnerabilitySummary{
			Status: "affected", Medium: 1, High: 1, Critical: 1, Unknown: 1,
		},
	}
	denied := EvaluateRepositorySecurityPolicy(policy, &intelligence)
	wantDenied := []string{"license_not_allowed", "medium_vulnerabilities", "high_vulnerabilities", "critical_vulnerabilities", "unknown_vulnerabilities"}
	if denied.Allowed || !denied.IntelligencePresent || !reflect.DeepEqual(denied.Reasons, wantDenied) {
		t.Fatalf("denied evaluation=%#v want reasons=%#v", denied, wantDenied)
	}

	intelligence.Licenses = []ArtifactLicense{{SPDXID: "apache-2.0"}}
	intelligence.Vulnerability = &ArtifactVulnerabilitySummary{Status: "clean"}
	allowed := EvaluateRepositorySecurityPolicy(policy, &intelligence)
	if !allowed.Allowed || len(allowed.Reasons) != 0 {
		t.Fatalf("allowed evaluation=%#v", allowed)
	}
}

func TestMemoryRepositorySecurityPolicyUsesOptimisticVersion(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	repo, err := store.CreateHostedRepository(ctx, HostedRepository{ID: "security-repo", Name: "security-repo", Format: FormatRaw})
	if err != nil {
		t.Fatal(err)
	}
	initial, err := store.GetRepositorySecurityPolicy(ctx, repo.ID)
	if err != nil || initial.Version != "1" || initial.Enabled || initial.MaxAllowedSeverity != SecuritySeverityCritical {
		t.Fatalf("initial=%#v err=%v", initial, err)
	}
	initial.Enabled = true
	initial.AutoScanOnPublish = true
	initial.RequireSBOM = true
	initial.AllowedLicenses = []string{"MIT"}
	updated, err := store.ReplaceRepositorySecurityPolicy(ctx, repo.ID, initial, "1")
	if err != nil || updated.Version != "2" || !updated.AutoScanOnPublish || !updated.RequireSBOM {
		t.Fatalf("updated=%#v err=%v", updated, err)
	}
	updated.AllowedLicenses[0] = "changed"
	loaded, err := store.GetRepositorySecurityPolicy(ctx, repo.ID)
	if err != nil || !reflect.DeepEqual(loaded.AllowedLicenses, []string{"MIT"}) {
		t.Fatalf("loaded=%#v err=%v", loaded, err)
	}
	if _, err = store.ReplaceRepositorySecurityPolicy(ctx, repo.ID, loaded, "1"); err != ErrVersionConflict {
		t.Fatalf("stale replace err=%v", err)
	}
}
