package contracts

import "testing"

func TestConanPublishRecipeAcceptsEmptyPackageFields(t *testing.T) {
	schema := loadManagementRuntimeSpec(t).Components.Schemas["CreateConanPublishSession"].Value
	body := map[string]any{
		"kind": "recipe", "reference": "pkg/1.0/user/stable", "recipeRevision": "rrev",
		"packageId": "", "packageRevision": "", "objects": []any{},
	}
	if err := schema.VisitJSON(body); err != nil {
		t.Fatalf("recipe fields emitted by the Go request must satisfy the contract: %v", err)
	}
	for _, field := range []string{"packageId", "packageRevision"} {
		body[field] = "invalid/value"
		if err := schema.VisitJSON(body); err == nil {
			t.Fatalf("%s must remain one path segment", field)
		}
		body[field] = ""
	}
}
