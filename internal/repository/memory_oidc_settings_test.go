package repository

import "testing"

func TestMemoryOIDCSettingsApplyProvisioningDefaults(t *testing.T) {
	store := NewMemoryStore()
	created, err := store.ReplaceOIDCSettings(t.Context(), OIDCSettings{}, "0")
	if err != nil {
		t.Fatal(err)
	}
	if created.ProvisioningMode != "disabled" || created.JITDefaultRole != "reader" {
		t.Fatalf("provisioning defaults=%+v", created)
	}
}
