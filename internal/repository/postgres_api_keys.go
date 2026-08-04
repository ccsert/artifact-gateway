package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

func (s *PostgresStore) CreateAPIKey(ctx context.Context, key APIKey) (APIKey, error) {
	err := s.db.QueryRowContext(ctx, `INSERT INTO api_keys (id, name, secret_hash, roles, expires_at) VALUES ($1, $2, $3, $4, $5) RETURNING created_at`, key.ID, key.Name, key.SecretHash, key.Roles, key.ExpiresAt).Scan(&key.CreatedAt)
	if err != nil {
		return APIKey{}, err
	}
	return key, nil
}

func (s *PostgresStore) ListAPIKeys(ctx context.Context) ([]APIKey, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id::text, name, array_to_json(roles), created_at, revoked_at, expires_at, last_used_at FROM api_keys ORDER BY created_at, id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	keys := []APIKey{}
	for rows.Next() {
		var key APIKey
		var roles []byte
		if err := rows.Scan(&key.ID, &key.Name, &roles, &key.CreatedAt, &key.RevokedAt, &key.ExpiresAt, &key.LastUsedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(roles, &key.Roles); err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	return keys, rows.Err()
}

func (s *PostgresStore) FindActiveAPIKeyByHash(ctx context.Context, hash string) (APIKey, error) {
	var key APIKey
	var roles []byte
	err := s.db.QueryRowContext(ctx, `UPDATE api_keys SET last_used_at=now() WHERE secret_hash=$1 AND revoked_at IS NULL AND (expires_at IS NULL OR expires_at>now()) RETURNING id::text,name,secret_hash,array_to_json(roles),created_at,expires_at,last_used_at`, hash).Scan(&key.ID, &key.Name, &key.SecretHash, &roles, &key.CreatedAt, &key.ExpiresAt, &key.LastUsedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return APIKey{}, ErrNotFound
	}
	if err != nil {
		return APIKey{}, err
	}
	if err := json.Unmarshal(roles, &key.Roles); err != nil {
		return APIKey{}, err
	}
	return key, nil
}

func (s *PostgresStore) RevokeAPIKey(ctx context.Context, id string) (APIKey, error) {
	var key APIKey
	var roles []byte
	err := s.db.QueryRowContext(ctx, `UPDATE api_keys SET revoked_at = COALESCE(revoked_at, now()) WHERE id::text = $1 RETURNING id::text, name, array_to_json(roles), created_at, revoked_at, expires_at, last_used_at`, id).Scan(&key.ID, &key.Name, &roles, &key.CreatedAt, &key.RevokedAt, &key.ExpiresAt, &key.LastUsedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return APIKey{}, ErrNotFound
	}
	if err != nil {
		return APIKey{}, err
	}
	if err := json.Unmarshal(roles, &key.Roles); err != nil {
		return APIKey{}, err
	}
	return key, nil
}
