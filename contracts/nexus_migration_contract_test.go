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
