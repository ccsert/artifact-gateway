package contracts

import "testing"

func TestNexusOCIGraphFailureSatisfiesPublishedContract(t *testing.T) {
	schema := loadManagementRuntimeSpec(t).Components.Schemas["NexusMigrationScanFailure"].Value
	if err := schema.VisitJSON(map[string]any{
		"stage": "oci_graph_validation", "sourceRepositories": []any{"images"},
		"sourceRepository": "images", "sourcePath": "v2/team/image/manifests/latest",
		"identity": "team/image:latest", "code": "capacity_evidence_expired",
		"occurredAt": "2026-10-20T01:00:00Z",
	}); err != nil {
		t.Fatalf("a durable OCI scan failure must satisfy the public response contract: %v", err)
	}
}

func TestNexusNPMClosureFailureAndFormatSatisfyPublishedContract(t *testing.T) {
	spec := loadManagementRuntimeSpec(t)
	schema := spec.Components.Schemas["NexusMigrationScanFailure"].Value
	if err := schema.VisitJSON(map[string]any{"stage": "npm_packument_validation", "sourceRepositories": []any{"npm-source"}, "sourceRepository": "npm-source", "sourcePath": "@scope/widget", "identity": "@scope/widget", "code": "npm_closure_incomplete", "occurredAt": "2026-10-22T01:00:00Z"}); err != nil {
		t.Fatalf("npm scan failure must satisfy public contract: %v", err)
	}
	for _, name := range []string{"NexusMigrationEntry", "MigrationSourceMapping"} {
		if err := spec.Components.Schemas[name].Value.Properties["format"].Value.VisitJSON("npm"); err != nil {
			t.Fatalf("%s does not admit native npm: %v", name, err)
		}
	}
}
