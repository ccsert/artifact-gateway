package repository

import (
	"context"
	"strconv"
	"time"
)

func (s *MemoryStore) GetOIDCSettings(_ context.Context) (OIDCSettings, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.oidcSettings == nil {
		return OIDCSettings{}, ErrNotFound
	}
	return cloneOIDCSettings(*s.oidcSettings), nil
}

func (s *MemoryStore) ReplaceOIDCSettings(_ context.Context, settings OIDCSettings, expectedVersion string) (OIDCSettings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.oidcSettings == nil {
		if expectedVersion != "0" {
			return OIDCSettings{}, ErrVersionConflict
		}
		settings.Version = "1"
	} else {
		if s.oidcSettings.Version != expectedVersion {
			return OIDCSettings{}, ErrVersionConflict
		}
		version, _ := strconv.ParseInt(s.oidcSettings.Version, 10, 64)
		settings.Version = strconv.FormatInt(version+1, 10)
	}
	settings.UpdatedAt = time.Now().UTC()
	stored := cloneOIDCSettings(settings)
	s.oidcSettings = &stored
	return cloneOIDCSettings(stored), nil
}

func cloneOIDCSettings(settings OIDCSettings) OIDCSettings {
	settings.Scopes = append([]string{}, settings.Scopes...)
	settings.AdminSubjects = append([]string{}, settings.AdminSubjects...)
	settings.ReaderRoles = append([]string{}, settings.ReaderRoles...)
	settings.WriterRoles = append([]string{}, settings.WriterRoles...)
	settings.AdminRoles = append([]string{}, settings.AdminRoles...)
	return settings
}
