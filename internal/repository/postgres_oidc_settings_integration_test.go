//go:build integration

package repository

import (
	"context"
	"os"
	"slices"
	"testing"
)

func TestPostgresOIDCSettingsVersionedReplacement(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is required for PostgreSQL integration tests")
	}
	store, err := NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := store.db.ExecContext(ctx, `DELETE FROM oidc_settings WHERE singleton=true`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = store.db.ExecContext(context.Background(), `DELETE FROM oidc_settings WHERE singleton=true`)
		_ = store.Close()
	})

	created, err := store.ReplaceOIDCSettings(ctx, OIDCSettings{
		Enabled: true, Issuer: "https://identity.example.test/realms/gateway",
		Audience: "artifact-gateway-api", ClientID: "artifact-gateway-console",
		ClientSecret: "ciphertext", RedirectURL: "https://gateway.example.test/auth/oidc/callback",
		Scopes: []string{"openid", "profile"}, ReaderRoles: []string{"artifact-reader"},
		WriterRoles: []string{"artifact-writer"}, AdminRoles: []string{"artifact-admin"},
	}, "0")
	if err != nil || created.Version != "1" || created.UpdatedAt.IsZero() || created.ProvisioningMode != "disabled" || created.JITDefaultRole != "reader" {
		t.Fatalf("created=%#v err=%v", created, err)
	}

	loaded, err := store.GetOIDCSettings(ctx)
	if err != nil || loaded.ClientSecret != "ciphertext" || !slices.Equal(loaded.Scopes, []string{"openid", "profile"}) || !slices.Equal(loaded.AdminRoles, []string{"artifact-admin"}) {
		t.Fatalf("loaded=%#v err=%v", loaded, err)
	}

	loaded.Enabled = false
	updated, err := store.ReplaceOIDCSettings(ctx, loaded, "1")
	if err != nil || updated.Version != "2" || updated.Enabled {
		t.Fatalf("updated=%#v err=%v", updated, err)
	}
	if _, err := store.ReplaceOIDCSettings(ctx, loaded, "1"); err != ErrVersionConflict {
		t.Fatalf("conflict err=%v", err)
	}
}
