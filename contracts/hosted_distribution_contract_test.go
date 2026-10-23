package contracts

import (
	"maps"
	"strings"
	"testing"
)

func TestHelmReplicationContractBindsCompleteProvenance(t *testing.T) {
	spec := loadManagementRuntimeSpec(t)
	schema := spec.Components.Schemas["HelmPreviewReplicationIdentity"].Value
	unsigned := map[string]any{"name": "widget", "version": "1.0.0", "digest": "sha256:" + strings.Repeat("a", 64), "size": float64(512), "sourceGovernanceVersion": "1"}
	if err := schema.VisitJSON(unsigned); err != nil {
		t.Fatal(err)
	}
	signed := maps.Clone(unsigned)
	signed["provenanceDigest"], signed["provenanceSize"] = "sha256:"+strings.Repeat("b", 64), float64(128)
	if err := schema.VisitJSON(signed); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"provenanceDigest", "provenanceSize", "sourceGovernanceVersion"} {
		partial := maps.Clone(signed)
		delete(partial, field)
		if err := schema.VisitJSON(partial); err == nil {
			t.Fatalf("replication permits partial closure without %s", field)
		}
	}
	for key, value := range map[string]any{"size": float64(67108865), "provenanceSize": float64(1048577), "sourceGovernanceVersion": "0", "listed": true} {
		invalid := maps.Clone(signed)
		invalid[key] = value
		if err := schema.VisitJSON(invalid); err == nil {
			t.Fatalf("invalid replication identity accepted: %s", key)
		}
	}
}

func TestRubyGemsYankRecoveryContractKeepsCASAndNativeAuthority(t *testing.T) {
	spec := loadManagementRuntimeSpec(t)
	op := operation(t, spec, "/previews/rubygems/repositories/{repositoryId}/packages/{gemName}/{gemVersion}/{platform}/governance", "POST")
	schema := op.RequestBody.Value.Content["application/json"].Schema.Value
	valid := map[string]any{"expectedDigest": "sha256:" + strings.Repeat("a", 64), "expectedGovernanceVersion": "1", "action": "restore", "reason": "verified recovery"}
	if err := schema.VisitJSON(valid); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"expectedDigest", "expectedGovernanceVersion", "reason"} {
		invalid := maps.Clone(valid)
		delete(invalid, field)
		if err := schema.VisitJSON(invalid); err == nil {
			t.Fatalf("unconditional recovery accepted without %s", field)
		}
	}
	native := operation(t, loadNativeHostedSpec(t), "/rubygems/{repository}/api/v1/gems/yank", "DELETE")
	if native.Security == nil || !hasSecurityScheme(*native.Security, "rubyGemsApiKey") {
		t.Fatal("native yank is missing the official gem client's API-key authentication")
	}
	for _, code := range []string{"200", "400", "401", "403", "404", "409", "413", "415", "500"} {
		requireResponse(t, native, code)
	}
}
