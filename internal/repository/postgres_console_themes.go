package repository

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
)

func scanConsoleThemePackage(scanner interface{ Scan(...any) error }) (ConsoleThemePackage, error) {
	var theme ConsoleThemePackage
	var version int64
	if err := scanner.Scan(&theme.ID, &version, &theme.Payload, &theme.CreatedAt, &theme.UpdatedAt); err != nil {
		return ConsoleThemePackage{}, err
	}
	theme.Version = strconv.FormatInt(version, 10)
	return theme, nil
}

func (s *PostgresStore) ListConsoleThemePackages(ctx context.Context) ([]ConsoleThemePackage, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, version, payload, created_at, updated_at FROM console_theme_packages ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	themes := []ConsoleThemePackage{}
	for rows.Next() {
		theme, scanErr := scanConsoleThemePackage(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		themes = append(themes, theme)
	}
	return themes, rows.Err()
}

func (s *PostgresStore) GetConsoleThemePackage(ctx context.Context, id string) (ConsoleThemePackage, error) {
	theme, err := scanConsoleThemePackage(s.db.QueryRowContext(ctx, `SELECT id, version, payload, created_at, updated_at FROM console_theme_packages WHERE id=$1`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return ConsoleThemePackage{}, ErrConsoleThemeNotFound
	}
	return theme, err
}

func (s *PostgresStore) CreateConsoleThemePackage(ctx context.Context, theme ConsoleThemePackage) (ConsoleThemePackage, error) {
	created, err := scanConsoleThemePackage(s.db.QueryRowContext(ctx, `INSERT INTO console_theme_packages (id, payload) VALUES ($1, $2::jsonb) RETURNING id, version, payload, created_at, updated_at`, theme.ID, theme.Payload))
	if isUnique(err) {
		return ConsoleThemePackage{}, ErrConsoleThemeExists
	}
	return created, err
}

func (s *PostgresStore) ReplaceConsoleThemePackage(ctx context.Context, theme ConsoleThemePackage, expectedVersion string) (ConsoleThemePackage, error) {
	version, err := strconv.ParseInt(expectedVersion, 10, 64)
	if err != nil {
		return ConsoleThemePackage{}, ErrVersionConflict
	}
	replaced, err := scanConsoleThemePackage(s.db.QueryRowContext(ctx, `UPDATE console_theme_packages SET payload=$1::jsonb, version=version+1, updated_at=now() WHERE id=$2 AND version=$3 RETURNING id, version, payload, created_at, updated_at`, theme.Payload, theme.ID, version))
	if !errors.Is(err, sql.ErrNoRows) {
		return replaced, err
	}
	if _, getErr := s.GetConsoleThemePackage(ctx, theme.ID); errors.Is(getErr, ErrConsoleThemeNotFound) {
		return ConsoleThemePackage{}, ErrConsoleThemeNotFound
	} else if getErr != nil {
		return ConsoleThemePackage{}, getErr
	}
	return ConsoleThemePackage{}, ErrVersionConflict
}

func (s *PostgresStore) DeleteConsoleThemePackage(ctx context.Context, id, expectedVersion string) error {
	version, err := strconv.ParseInt(expectedVersion, 10, 64)
	if err != nil {
		return ErrVersionConflict
	}
	result, err := s.db.ExecContext(ctx, `DELETE FROM console_theme_packages WHERE id=$1 AND version=$2`, id, version)
	if err != nil {
		return err
	}
	deleted, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if deleted == 1 {
		return nil
	}
	if _, getErr := s.GetConsoleThemePackage(ctx, id); errors.Is(getErr, ErrConsoleThemeNotFound) {
		return ErrConsoleThemeNotFound
	} else if getErr != nil {
		return getErr
	}
	return ErrVersionConflict
}
