package repository

import (
	"context"
	"time"
)

func DefaultSiteSettings() SiteSettings {
	return SiteSettings{
		Version:         "1",
		SiteName:        "Artifact Gateway",
		BrandMark:       "AG",
		EnabledThemeIDs: []string{"gateway-dark", "gateway-light", "aerok-dark", "aerok-light"},
		DefaultThemeID:  "gateway-dark",
		UpdatedAt:       time.Unix(0, 0).UTC(),
	}
}

func (s *MemoryStore) GetSiteSettings(_ context.Context) (SiteSettings, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.siteSettings.Version == "" {
		return DefaultSiteSettings(), nil
	}
	settings := s.siteSettings
	settings.EnabledThemeIDs = append([]string(nil), settings.EnabledThemeIDs...)
	return settings, nil
}

func (s *MemoryStore) ReplaceSiteSettings(_ context.Context, settings SiteSettings, expectedVersion string) (SiteSettings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	current := s.siteSettings
	if current.Version == "" {
		current = DefaultSiteSettings()
	}
	if current.Version != expectedVersion {
		return SiteSettings{}, ErrVersionConflict
	}
	settings.Version = nextHostedGroupVersion(current.Version)
	settings.EnabledThemeIDs = append([]string(nil), settings.EnabledThemeIDs...)
	settings.UpdatedAt = time.Now().UTC()
	s.siteSettings = settings
	return settings, nil
}
