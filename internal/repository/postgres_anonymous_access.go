package repository

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
)

func (s *PostgresStore) GetAnonymousAccessPolicy(ctx context.Context) (AnonymousAccessPolicy, error) {
	var policy AnonymousAccessPolicy
	var version int64
	err := s.db.QueryRowContext(ctx, `SELECT version, enabled FROM anonymous_access_policy WHERE singleton=true`).Scan(&version, &policy.Enabled)
	if err != nil {
		return AnonymousAccessPolicy{}, err
	}
	policy.Version = strconv.FormatInt(version, 10)
	return policy, nil
}

func (s *PostgresStore) ReplaceAnonymousAccessPolicy(ctx context.Context, policy AnonymousAccessPolicy, expectedVersion string) (AnonymousAccessPolicy, error) {
	version, err := strconv.ParseInt(expectedVersion, 10, 64)
	if err != nil {
		return AnonymousAccessPolicy{}, ErrVersionConflict
	}
	var nextVersion int64
	err = s.db.QueryRowContext(ctx, `UPDATE anonymous_access_policy SET enabled=$1, version=version+1 WHERE singleton=true AND version=$2 RETURNING version`, policy.Enabled, version).Scan(&nextVersion)
	if errors.Is(err, sql.ErrNoRows) {
		return AnonymousAccessPolicy{}, ErrVersionConflict
	}
	if err != nil {
		return AnonymousAccessPolicy{}, err
	}
	policy.Version = strconv.FormatInt(nextVersion, 10)
	return policy, nil
}
