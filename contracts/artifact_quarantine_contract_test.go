package contracts

import (
	"path/filepath"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

func loadManagementRuntimeSpec(t *testing.T) *openapi3.T {
	t.Helper()
	loader := openapi3.NewLoader()
	spec, err := loader.LoadFromFile(filepath.Join("..", "api", "openapi", "management-runtime-v1.json"))
	if err != nil {
		t.Fatalf("load management OpenAPI: %v", err)
	}
	if err := spec.Validate(loader.Context); err != nil {
		t.Fatalf("validate management OpenAPI: %v", err)
	}
	return spec
}

func TestArtifactQuarantineAndDistributionManagementContract(t *testing.T) {
	spec := loadManagementRuntimeSpec(t)

	update := spec.Components.Schemas["ArtifactQuarantineUpdate"]
	if update == nil || update.Value == nil {
		t.Fatal("ArtifactQuarantineUpdate schema is missing")
	}
	reason := update.Value.Properties["reason"]
	if reason == nil || reason.Value == nil || reason.Value.MaxLength == nil || *reason.Value.MaxLength != 1024 {
		t.Fatalf("ArtifactQuarantineUpdate.reason maxLength=%v want 1024", reason)
	}
	problem := spec.Components.Schemas["Problem"]
	if problem == nil || problem.Value == nil || problem.Value.Properties["code"] == nil || problem.Value.Properties["code"].Value == nil {
		t.Fatal("Problem.code schema is missing")
	}
	invalidState := false
	for _, value := range problem.Value.Properties["code"].Value.Enum {
		invalidState = invalidState || value == "invalid_state"
	}
	if !invalidState {
		t.Fatal("Problem.code must include invalid_state for duplicate quarantine transitions")
	}

	quarantine := operation(t, spec, "/repositories/{repositoryId}/artifact-quarantine", "PUT")
	ifMatchRequired := false
	for _, parameter := range quarantine.Parameters {
		if parameter.Value != nil && parameter.Value.Name == "If-Match" && parameter.Value.In == "header" {
			ifMatchRequired = parameter.Value.Required
		}
	}
	if !ifMatchRequired {
		t.Fatal("artifact quarantine replacement must require If-Match")
	}
	for _, status := range []string{"200", "400", "401", "403", "404", "409", "412"} {
		requireResponse(t, quarantine, status)
	}
	etag := requireResponse(t, quarantine, "200").Headers["ETag"]
	if etag == nil || etag.Value == nil || !etag.Value.Required {
		t.Fatal("artifact quarantine 200 response must require ETag")
	}

	for _, path := range []string{
		"/repositories/{repositoryId}/promotions",
		"/repositories/{repositoryId}/replications",
	} {
		requireResponse(t, operation(t, spec, path, "POST"), "403")
	}

	plan := spec.Components.Schemas["ReplicationPlan"]
	if plan == nil || plan.Value == nil {
		t.Fatal("ReplicationPlan schema is missing")
	}
	coordinate := plan.Value.Properties["coordinate"]
	digest := plan.Value.Properties["digest"]
	if coordinate == nil || coordinate.Value == nil || coordinate.Value.MinLength != 1 || coordinate.Value.MaxLength == nil || *coordinate.Value.MaxLength != 1024 {
		t.Fatalf("ReplicationPlan.coordinate constraints=%v", coordinate)
	}
	if digest == nil || digest.Value == nil || digest.Value.Pattern != "^sha256:[a-f0-9]{64}$" {
		t.Fatalf("ReplicationPlan.digest constraints=%v", digest)
	}
	for _, property := range []string{"coordinate", "digest"} {
		if contains(plan.Value.Required, property) {
			t.Fatalf("ReplicationPlan.%s must remain optional for legacy rows", property)
		}
	}
}
