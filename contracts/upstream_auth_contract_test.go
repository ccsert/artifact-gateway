package contracts

import "testing"

func TestOCIIssuerBindingAcceptsExplicitClear(t *testing.T) {
	spec := loadManagementRuntimeSpec(t)
	schema := spec.Components.Schemas["OCIBearer"].Value
	for _, value := range []any{nil, map[string]any{"realm": "https://issuer.example.com/token", "service": "private-registry"}} {
		if err := schema.VisitJSON(value); err != nil {
			t.Fatalf("explicit clear and a complete binding must satisfy the published contract: %v", err)
		}
	}
	for _, value := range []any{true, "null", map[string]any{"realm": "https://issuer.example.com/token"}, map[string]any{"realm": "https://issuer.example.com/token", "service": "private-registry", "secret": "forbidden"}} {
		if err := schema.VisitJSON(value); err == nil {
			t.Fatalf("invalid issuer binding passed the contract: %T", value)
		}
	}
	patch := spec.Components.Schemas["UpdateRepository"].Value
	if err := patch.VisitJSON(map[string]any{"ociBearer": nil}); err != nil {
		t.Fatalf("the generated update contract must permit explicit issuer removal: %v", err)
	}
	code := spec.Components.Schemas["Problem"].Value.Properties["code"].Value
	if err := code.VisitJSON("upstream_credential_rebind_required"); err != nil {
		t.Fatalf("credential destination rejection must have a finite public code: %v", err)
	}
}
