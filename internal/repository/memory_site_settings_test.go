package repository

import "testing"

func TestMemorySiteSettingsVersionedReplacement(t *testing.T) {
	store := NewMemoryStore()
	current, err := store.GetSiteSettings(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if current.Version != "1" || current.SiteName != "Artifact Gateway" || current.BrandMark != "AG" || current.DefaultThemeID != "gateway-dark" || len(current.EnabledThemeIDs) != 4 {
		t.Fatalf("defaults = %#v", current)
	}

	replaced, err := store.ReplaceSiteSettings(t.Context(), SiteSettings{
		SiteName: "Acme Packages", BrandMark: "AC", EnabledThemeIDs: []string{"aerok-light"}, DefaultThemeID: "aerok-light",
	}, current.Version)
	if err != nil {
		t.Fatal(err)
	}
	if replaced.Version != "2" || replaced.SiteName != "Acme Packages" || replaced.DefaultThemeID != "aerok-light" {
		t.Fatalf("replaced = %#v", replaced)
	}
	if _, err := store.ReplaceSiteSettings(t.Context(), replaced, current.Version); err != ErrVersionConflict {
		t.Fatalf("stale replace error = %v", err)
	}
}
