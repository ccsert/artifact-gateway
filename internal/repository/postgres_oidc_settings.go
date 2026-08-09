package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

const oidcSettingsColumns = `version::text, enabled, issuer, audience, jwks_url, client_id, client_secret, redirect_url, scopes, admin_subjects, reader_roles, writer_roles, admin_roles, updated_at`

func (s *PostgresStore) GetOIDCSettings(ctx context.Context) (OIDCSettings, error) {
	var settings OIDCSettings
	err := scanOIDCSettings(s.db.QueryRowContext(ctx, `SELECT `+oidcSettingsColumns+` FROM oidc_settings WHERE singleton=true`), &settings)
	if errors.Is(err, sql.ErrNoRows) {
		return OIDCSettings{}, ErrNotFound
	}
	return settings, err
}

func (s *PostgresStore) ReplaceOIDCSettings(ctx context.Context, settings OIDCSettings, expectedVersion string) (OIDCSettings, error) {
	scopes, adminSubjects, readerRoles, writerRoles, adminRoles, err := encodeOIDCSettingsLists(settings)
	if err != nil {
		return OIDCSettings{}, err
	}
	if expectedVersion == "0" {
		err = scanOIDCSettings(s.db.QueryRowContext(ctx, `INSERT INTO oidc_settings
			(singleton, enabled, issuer, audience, jwks_url, client_id, client_secret, redirect_url, scopes, admin_subjects, reader_roles, writer_roles, admin_roles)
			VALUES (true,$1,$2,$3,$4,$5,$6,$7,$8::jsonb,$9::jsonb,$10::jsonb,$11::jsonb,$12::jsonb)
			ON CONFLICT (singleton) DO NOTHING RETURNING `+oidcSettingsColumns,
			settings.Enabled, settings.Issuer, settings.Audience, settings.JWKSURL, settings.ClientID, settings.ClientSecret, settings.RedirectURL,
			scopes, adminSubjects, readerRoles, writerRoles, adminRoles), &settings)
	} else {
		err = scanOIDCSettings(s.db.QueryRowContext(ctx, `UPDATE oidc_settings SET
			enabled=$1, issuer=$2, audience=$3, jwks_url=$4, client_id=$5, client_secret=$6, redirect_url=$7,
			scopes=$8::jsonb, admin_subjects=$9::jsonb, reader_roles=$10::jsonb, writer_roles=$11::jsonb, admin_roles=$12::jsonb,
			version=version+1, updated_at=now()
			WHERE singleton=true AND version::text=$13 RETURNING `+oidcSettingsColumns,
			settings.Enabled, settings.Issuer, settings.Audience, settings.JWKSURL, settings.ClientID, settings.ClientSecret, settings.RedirectURL,
			scopes, adminSubjects, readerRoles, writerRoles, adminRoles, expectedVersion), &settings)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return OIDCSettings{}, ErrVersionConflict
	}
	return settings, err
}

type oidcSettingsScanner interface {
	Scan(...any) error
}

func scanOIDCSettings(scanner oidcSettingsScanner, settings *OIDCSettings) error {
	var scopes, adminSubjects, readerRoles, writerRoles, adminRoles []byte
	if err := scanner.Scan(
		&settings.Version, &settings.Enabled, &settings.Issuer, &settings.Audience, &settings.JWKSURL,
		&settings.ClientID, &settings.ClientSecret, &settings.RedirectURL, &scopes, &adminSubjects,
		&readerRoles, &writerRoles, &adminRoles, &settings.UpdatedAt,
	); err != nil {
		return err
	}
	for _, target := range []struct {
		raw   []byte
		value *[]string
	}{
		{scopes, &settings.Scopes}, {adminSubjects, &settings.AdminSubjects}, {readerRoles, &settings.ReaderRoles},
		{writerRoles, &settings.WriterRoles}, {adminRoles, &settings.AdminRoles},
	} {
		if err := json.Unmarshal(target.raw, target.value); err != nil {
			return err
		}
	}
	return nil
}

func encodeOIDCSettingsLists(settings OIDCSettings) ([]byte, []byte, []byte, []byte, []byte, error) {
	values := [][]string{settings.Scopes, settings.AdminSubjects, settings.ReaderRoles, settings.WriterRoles, settings.AdminRoles}
	encoded := make([][]byte, len(values))
	for index, value := range values {
		if value == nil {
			value = []string{}
		}
		data, err := json.Marshal(value)
		if err != nil {
			return nil, nil, nil, nil, nil, err
		}
		encoded[index] = data
	}
	return encoded[0], encoded[1], encoded[2], encoded[3], encoded[4], nil
}
