package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
)

func (s *PostgresStore) GetSiteSettings(ctx context.Context) (SiteSettings, error) {
	var settings SiteSettings
	var version int64
	var enabledThemeIDs []byte
	err := s.db.QueryRowContext(ctx, `SELECT version, site_name, logo_url, brand_mark, to_json(enabled_theme_ids), default_theme_id, updated_at FROM site_settings WHERE singleton=true`).Scan(
		&version, &settings.SiteName, &settings.LogoURL, &settings.BrandMark, &enabledThemeIDs, &settings.DefaultThemeID, &settings.UpdatedAt,
	)
	if err != nil {
		return SiteSettings{}, err
	}
	if err := json.Unmarshal(enabledThemeIDs, &settings.EnabledThemeIDs); err != nil {
		return SiteSettings{}, err
	}
	settings.Version = strconv.FormatInt(version, 10)
	return settings, nil
}

func (s *PostgresStore) ReplaceSiteSettings(ctx context.Context, settings SiteSettings, expectedVersion string) (SiteSettings, error) {
	version, err := strconv.ParseInt(expectedVersion, 10, 64)
	if err != nil {
		return SiteSettings{}, ErrVersionConflict
	}
	enabledThemeIDs, err := json.Marshal(settings.EnabledThemeIDs)
	if err != nil {
		return SiteSettings{}, err
	}
	var nextVersion int64
	err = s.db.QueryRowContext(ctx, `UPDATE site_settings
		SET site_name=$1, logo_url=$2, brand_mark=$3,
			enabled_theme_ids=ARRAY(SELECT jsonb_array_elements_text($4::jsonb)),
			default_theme_id=$5, version=version+1, updated_at=now()
		WHERE singleton=true AND version=$6
		RETURNING version, updated_at`,
		settings.SiteName, settings.LogoURL, settings.BrandMark, enabledThemeIDs, settings.DefaultThemeID, version,
	).Scan(&nextVersion, &settings.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return SiteSettings{}, ErrVersionConflict
	}
	if err != nil {
		return SiteSettings{}, err
	}
	settings.Version = strconv.FormatInt(nextVersion, 10)
	return settings, nil
}
