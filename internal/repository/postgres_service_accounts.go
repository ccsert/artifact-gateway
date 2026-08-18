package repository

import (
	"context"
	"database/sql"
	"errors"
)

const serviceAccountColumns = `id::text, name, description, state, created_at, updated_at, version::text`

func scanServiceAccount(row interface{ Scan(...any) error }, account *ServiceAccount) error {
	return row.Scan(
		&account.ID,
		&account.Name,
		&account.Description,
		&account.State,
		&account.CreatedAt,
		&account.UpdatedAt,
		&account.Version,
	)
}

func (s *PostgresStore) CreateServiceAccount(ctx context.Context, account ServiceAccount) (ServiceAccount, error) {
	err := scanServiceAccount(
		s.db.QueryRowContext(
			ctx,
			`INSERT INTO service_accounts (id, name, description) VALUES ($1,$2,$3) RETURNING `+serviceAccountColumns,
			account.ID,
			account.Name,
			account.Description,
		),
		&account,
	)
	if isUnique(err) {
		return ServiceAccount{}, ErrNameExists
	}
	if err != nil {
		return ServiceAccount{}, err
	}
	return account, nil
}

func (s *PostgresStore) ListServiceAccounts(ctx context.Context, limit int, afterID string) ([]ServiceAccount, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+serviceAccountColumns+` FROM service_accounts WHERE ($1='' OR id>$1::uuid) ORDER BY id LIMIT $2`, afterID, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	accounts := []ServiceAccount{}
	for rows.Next() {
		var account ServiceAccount
		if err := scanServiceAccount(rows, &account); err != nil {
			return nil, err
		}
		accounts = append(accounts, account)
	}
	return accounts, rows.Err()
}

func (s *PostgresStore) GetServiceAccount(ctx context.Context, id string) (ServiceAccount, error) {
	var account ServiceAccount
	err := scanServiceAccount(
		s.db.QueryRowContext(ctx, `SELECT `+serviceAccountColumns+` FROM service_accounts WHERE id::text=$1`, id),
		&account,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return ServiceAccount{}, ErrNotFound
	}
	if err != nil {
		return ServiceAccount{}, err
	}
	return account, nil
}

func (s *PostgresStore) UpdateServiceAccount(ctx context.Context, update ServiceAccountUpdate, expectedVersion string) (ServiceAccount, error) {
	var account ServiceAccount
	description := ""
	if update.Description != nil {
		description = *update.Description
	}
	state := ServiceAccountState("")
	if update.State != nil {
		state = *update.State
	}
	err := scanServiceAccount(
		s.db.QueryRowContext(ctx, `UPDATE service_accounts SET description=CASE WHEN $2 THEN $3 ELSE description END, state=CASE WHEN $4 THEN $5 ELSE state END, updated_at=now(), version=version+1 WHERE id::text=$1 AND version::text=$6 RETURNING `+serviceAccountColumns,
			update.ID,
			update.Description != nil,
			description,
			update.State != nil,
			state,
			expectedVersion,
		),
		&account,
	)
	if errors.Is(err, sql.ErrNoRows) {
		if _, getErr := s.GetServiceAccount(ctx, update.ID); errors.Is(getErr, ErrNotFound) {
			return ServiceAccount{}, ErrNotFound
		}
		return ServiceAccount{}, ErrVersionConflict
	}
	if err != nil {
		return ServiceAccount{}, err
	}
	return account, nil
}

func (s *PostgresStore) CreateServiceAccountCredential(ctx context.Context, credential APIKey) (APIKey, error) {
	err := s.db.QueryRowContext(
		ctx,
		`INSERT INTO api_keys (id, service_account_id, name, secret_hash, roles, expires_at)
		 SELECT $1, id, $3, $4, $5, $6 FROM service_accounts WHERE id::text=$2 AND state='active'
		 RETURNING created_at`,
		credential.ID,
		credential.ServiceAccountID,
		credential.Name,
		credential.SecretHash,
		credential.Roles,
		credential.ExpiresAt,
	).Scan(&credential.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		account, getErr := s.GetServiceAccount(ctx, credential.ServiceAccountID)
		if errors.Is(getErr, ErrNotFound) {
			return APIKey{}, ErrNotFound
		}
		if getErr != nil {
			return APIKey{}, getErr
		}
		if account.State != ServiceAccountActive {
			return APIKey{}, ErrServiceAccountDisabled
		}
		return APIKey{}, ErrVersionConflict
	}
	if err != nil {
		return APIKey{}, err
	}
	return credential, nil
}

func (s *PostgresStore) ListServiceAccountCredentials(ctx context.Context, accountID string, limit int, afterID string) ([]APIKey, error) {
	if _, err := s.GetServiceAccount(ctx, accountID); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id::text, service_account_id::text, name, array_to_json(roles), created_at, revoked_at, expires_at, last_used_at FROM api_keys WHERE service_account_id=$1::uuid AND ($2='' OR id>$2::uuid) ORDER BY id LIMIT $3`, accountID, afterID, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	credentials := []APIKey{}
	for rows.Next() {
		var key APIKey
		var roles []byte
		if err := rows.Scan(&key.ID, &key.ServiceAccountID, &key.Name, &roles, &key.CreatedAt, &key.RevokedAt, &key.ExpiresAt, &key.LastUsedAt); err != nil {
			return nil, err
		}
		if err := scanAPIKeyRoles(roles, &key); err != nil {
			return nil, err
		}
		credentials = append(credentials, key)
	}
	return credentials, rows.Err()
}

func (s *PostgresStore) RevokeServiceAccountCredential(ctx context.Context, accountID, credentialID string) (APIKey, error) {
	var key APIKey
	var roles []byte
	err := s.db.QueryRowContext(ctx, `UPDATE api_keys SET revoked_at=COALESCE(revoked_at, now()) WHERE id::text=$1 AND service_account_id::text=$2 RETURNING id::text, service_account_id::text, name, array_to_json(roles), created_at, revoked_at, expires_at, last_used_at`, credentialID, accountID).Scan(&key.ID, &key.ServiceAccountID, &key.Name, &roles, &key.CreatedAt, &key.RevokedAt, &key.ExpiresAt, &key.LastUsedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return APIKey{}, ErrNotFound
	}
	if err != nil {
		return APIKey{}, err
	}
	if err := scanAPIKeyRoles(roles, &key); err != nil {
		return APIKey{}, err
	}
	return key, nil
}
