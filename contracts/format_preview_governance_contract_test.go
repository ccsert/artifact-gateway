package contracts

import (
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

func TestNuGetGovernanceContractRequiresExactIdentityAndIndependentReadPolicy(t *testing.T) {
	spec := loadManagementRuntimeSpec(t)
	governance := operation(t, spec, "/previews/nuget/repositories/{repositoryId}/packages/{packageId}/{version}/governance", "POST")
	schema := governance.RequestBody.Value.Content["application/json"].Schema.Value
	valid := map[string]any{
		"expectedDigest":            "sha256:" + strings.Repeat("a", 64),
		"expectedGovernanceVersion": "1",
		"action":                    "delete",
		"reason":                    "operator request",
	}
	if err := schema.VisitJSON(valid); err != nil {
		t.Fatal(err)
	}
	for _, missing := range []string{"expectedDigest", "expectedGovernanceVersion", "action", "reason"} {
		input := make(map[string]any, len(valid))
		for key, value := range valid {
			if key != missing {
				input[key] = value
			}
		}
		if err := schema.VisitJSON(input); err == nil {
			t.Fatalf("governance mutation may omit %s", missing)
		}
	}
	valid["action"] = "purge"
	if err := schema.VisitJSON(valid); err == nil {
		t.Fatal("governance preview must not declare physical reclamation")
	}
	requireResponse(t, governance, "405")
	policy := operation(t, spec, "/previews/nuget/repositories/{repositoryId}/quarantine-read-policy", "PUT")
	if err := policy.RequestBody.Value.Content["application/json"].Schema.Value.VisitJSON(map[string]any{"enabled": true}); err == nil {
		t.Fatal("quarantine read policy may change without its current version")
	}
	native := loadNativeHostedSpec(t)
	for _, method := range []string{"DELETE", "POST"} {
		op := operation(t, native, "/nuget/{repository}/api/v2/package/{packageId}/{version}", method)
		for _, status := range []string{"204", "401", "403", "404", "405", "409", "503"} {
			requireResponse(t, op, status)
		}
	}
}

func TestHelmProxyGroupContractPinsSourcesWithoutDeclaringFormalAdmission(t *testing.T) {
	spec := loadManagementRuntimeSpec(t)
	if err := spec.Components.Schemas["Format"].Value.VisitJSON("helm"); err == nil {
		t.Fatal("Helm preview entered the formal format enum")
	}
	create := operation(t, spec, "/previews/helm/repositories", "POST").RequestBody.Value.Content["application/json"].Schema.Value
	input := map[string]any{"name": "private-source", "type": "proxy", "endpoint": "https://charts.example.test/index.yaml", "upstreamAuth": map[string]any{"scheme": "unknown"}}
	if err := create.VisitJSON(input); err == nil {
		t.Fatal("unsupported Helm upstream credential scheme entered the contract")
	}
	auth := spec.Components.Schemas["HelmPreviewRepositoryCreate"].Value.Properties["upstreamAuth"].Value
	if !auth.Properties["secret"].Value.WriteOnly {
		t.Fatal("source secret must remain write-only")
	}
	replace := operation(t, spec, "/previews/helm/groups/{groupId}", "PUT").RequestBody.Value.Content["application/json"].Schema.Value
	if err := replace.VisitJSON(map[string]any{"name": "group", "members": []any{"11111111-1111-4111-8111-111111111111"}}); err == nil {
		t.Fatal("Group replacement may omit its current version")
	}
	native := loadNativeHostedSpec(t)
	for _, method := range []string{"GET", "HEAD"} {
		op := operation(t, native, "/helm/{repository}/snapshots/{snapshotId}/charts/{chartName}/{version}/{filename}", method)
		for _, status := range []string{"200", "206", "304", "401", "403", "404", "409", "412", "416", "502", "503"} {
			requireResponse(t, op, status)
		}
	}
}

func TestHelmChartContractDistinguishesIndexMetadataFromVerifiedBytes(t *testing.T) {
	for _, spec := range []*openapi3.T{loadManagementRuntimeSpec(t), loadNativeHostedSpec(t)} {
		schema := spec.Components.Schemas["HelmPreviewChart"].Value
		metadata := map[string]any{
			"repositoryId": "11111111-1111-4111-8111-111111111111",
			"name":         "example",
			"version":      "1.0.0",
			"digest":       "sha256:" + strings.Repeat("a", 64),
			"publishedAt":  "2026-10-22T00:00:00Z",
			"chartUrl":     "/helm/example/snapshots/frozen/charts/example/1.0.0/example-1.0.0.tgz",
			"cached":       false,
		}
		if err := schema.VisitJSON(metadata); err != nil {
			t.Fatalf("cold index metadata must not require unknown byte counts: %v", err)
		}
		for key, value := range map[string]any{
			"governanceVersion": "1",
			"size":              float64(1),
			"provenanceSize":    float64(0),
			"provenanceDigest":  "sha256:" + strings.Repeat("b", 64),
			"provenanceUrl":     "/helm/example/chart.tgz.prov",
		} {
			metadata[key] = value
			if err := schema.VisitJSON(metadata); err == nil {
				t.Fatalf("cold index metadata may pretend to know %s", key)
			}
			delete(metadata, key)
		}
		metadata["cached"] = true
		if err := schema.VisitJSON(metadata); err == nil {
			t.Fatal("verified cached receipt may omit its byte counts")
		}
		metadata["size"] = float64(512)
		metadata["provenanceSize"] = float64(0)
		if err := schema.VisitJSON(metadata); err == nil {
			t.Fatal("verified chart receipt may omit the governance version needed to pin replication")
		}
		metadata["governanceVersion"] = "1"
		if err := schema.VisitJSON(metadata); err != nil {
			t.Fatalf("unsigned verified receipt: %v", err)
		}
		metadata["size"] = float64(0)
		if err := schema.VisitJSON(metadata); err == nil {
			t.Fatal("cached chart may have a zero byte count")
		}
		metadata["size"] = float64(512)
		metadata["provenanceSize"] = float64(128)
		metadata["provenanceDigest"] = "sha256:" + strings.Repeat("b", 64)
		metadata["provenanceUrl"] = metadata["chartUrl"].(string) + ".prov"
		if err := schema.VisitJSON(metadata); err != nil {
			t.Fatalf("signed verified receipt: %v", err)
		}
		delete(metadata, "cached")
		if err := schema.VisitJSON(metadata); err == nil {
			t.Fatal("chart receipt may omit its explicit cache state")
		}
	}
}
