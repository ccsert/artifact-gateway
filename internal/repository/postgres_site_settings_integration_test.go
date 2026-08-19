//go:build integration

package repository

import (
	"context"
	"os"
	"testing"
)

func TestPostgresSiteSettingsVersionedReplacement(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is required for PostgreSQL integration tests")
	}
	store, err := NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	ctx := context.Background()
	current, err := store.GetSiteSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		latest, loadErr := store.GetSiteSettings(context.Background())
		if loadErr == nil {
			_, _ = store.ReplaceSiteSettings(context.Background(), current, latest.Version)
		}
	})
	updated, err := store.ReplaceSiteSettings(ctx, SiteSettings{
		SiteName: "Acme Packages", LogoURL: "/assets/acme.webp", BrandMark: "AC",
		EnabledThemeIDs: []string{"aerok-light"}, DefaultThemeID: "aerok-light",
	}, current.Version)
	if err != nil || updated.SiteName != "Acme Packages" || updated.Version == current.Version || updated.UpdatedAt.IsZero() {
		t.Fatalf("updated=%#v err=%v", updated, err)
	}
	loaded, err := store.GetSiteSettings(ctx)
	if err != nil || loaded.LogoURL != "/assets/acme.webp" || loaded.BrandMark != "AC" || loaded.DefaultThemeID != "aerok-light" || len(loaded.EnabledThemeIDs) != 1 {
		t.Fatalf("loaded=%#v err=%v", loaded, err)
	}
	if _, err := store.ReplaceSiteSettings(ctx, current, current.Version); err != ErrVersionConflict {
		t.Fatalf("stale replace error=%v", err)
	}
}
