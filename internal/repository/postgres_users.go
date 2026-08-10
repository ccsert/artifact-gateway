package repository

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

const userColumns = `id::text, name, display_name, email, description, secret_hash, role, state, last_login_at, password_changed_at, failed_login_attempts, locked_until, must_change_password, session_version, created_at, updated_at, version::text`

// Serializes role/state mutations so two concurrent requests cannot both
// remove an administrator after observing the other as active.
const userAdminMutationAdvisoryLock int64 = 0x757365725f61646d

func scanUser(row interface{ Scan(...any) error }, user *User) error {
	return row.Scan(
		&user.ID, &user.Name, &user.DisplayName, &user.Email, &user.Description,
		&user.SecretHash, &user.Role, &user.State, &user.LastLoginAt,
		&user.PasswordChangedAt, &user.FailedLoginAttempts, &user.LockedUntil,
		&user.MustChangePassword, &user.SessionVersion, &user.CreatedAt,
		&user.UpdatedAt, &user.Version,
	)
}

func (s *PostgresStore) CreateUser(ctx context.Context, user User) (User, error) {
	var passwordChangedAt *time.Time
	if user.SecretHash != "" {
		now := time.Now().UTC()
		passwordChangedAt = &now
	}
	err := scanUser(s.db.QueryRowContext(ctx, `INSERT INTO users (id, name, display_name, email, description, secret_hash, role, must_change_password, password_changed_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING `+userColumns, user.ID, user.Name, user.DisplayName, user.Email, user.Description, user.SecretHash, user.Role, user.MustChangePassword, passwordChangedAt), &user)
	if isUnique(err) {
		return User{}, ErrNameExists
	}
	if err != nil {
		return User{}, err
	}
	return user, nil
}

func (s *PostgresStore) ListUsers(ctx context.Context, query UserListQuery) (UserPage, error) {
	query.Search = strings.TrimSpace(query.Search)
	if query.Limit <= 0 || query.Limit > 500 {
		query.Limit = 100
	}
	if query.Offset < 0 {
		query.Offset = 0
	}
	const filter = ` WHERE ($1='' OR name ILIKE '%'||$1||'%' OR display_name ILIKE '%'||$1||'%' OR email ILIKE '%'||$1||'%') AND ($2='' OR role=$2) AND ($3='' OR state=$3)`
	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM users`+filter, query.Search, query.Role, query.State).Scan(&total); err != nil {
		return UserPage{}, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+userColumns+` FROM users`+filter+` ORDER BY created_at, id LIMIT $4 OFFSET $5`, query.Search, query.Role, query.State, query.Limit, query.Offset)
	if err != nil {
		return UserPage{}, err
	}
	defer func() { _ = rows.Close() }()
	users := []User{}
	for rows.Next() {
		var user User
		if err := scanUser(rows, &user); err != nil {
			return UserPage{}, err
		}
		users = append(users, user)
	}
	if err := rows.Err(); err != nil {
		return UserPage{}, err
	}
	return UserPage{Items: users, Total: total, Offset: query.Offset, Limit: query.Limit}, nil
}

func (s *PostgresStore) GetUser(ctx context.Context, id string) (User, error) {
	var user User
	err := scanUser(s.db.QueryRowContext(ctx, `SELECT `+userColumns+` FROM users WHERE id::text=$1`, id), &user)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, err
	}
	return user, nil
}

func (s *PostgresStore) GetUserByName(ctx context.Context, name string) (User, error) {
	var user User
	err := scanUser(s.db.QueryRowContext(ctx, `SELECT `+userColumns+` FROM users WHERE lower(name)=lower($1)`, name), &user)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, err
	}
	return user, nil
}

func (s *PostgresStore) UpdateUser(ctx context.Context, update UserUpdate, expectedVersion string) (User, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return User{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, userAdminMutationAdvisoryLock); err != nil {
		return User{}, err
	}
	var current User
	err = scanUser(tx.QueryRowContext(ctx, `SELECT `+userColumns+` FROM users WHERE id::text=$1 FOR UPDATE`, update.ID), &current)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, err
	}
	if current.Version != expectedVersion {
		return User{}, ErrVersionConflict
	}
	if removesLastActiveAdmin(current, update.Role, update.State) {
		var others int
		if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM users WHERE id<>$1 AND role='admin' AND state=$2`, update.ID, UserActive).Scan(&others); err != nil {
			return User{}, err
		}
		if others == 0 {
			return User{}, ErrLastActiveAdmin
		}
	}
	var updated User
	err = scanUser(tx.QueryRowContext(ctx, `UPDATE users SET role=CASE WHEN $2 THEN $3 ELSE role END, state=CASE WHEN $4 THEN $5 ELSE state END, display_name=CASE WHEN $6 THEN $7 ELSE display_name END, email=CASE WHEN $8 THEN $9 ELSE email END, description=CASE WHEN $10 THEN $11 ELSE description END, version=version+1, updated_at=now() WHERE id::text=$1 AND version::text=$12 RETURNING `+userColumns,
		update.ID,
		update.Role != nil, stringValue(update.Role),
		update.State != nil, stringValue(update.State),
		update.DisplayName != nil, stringValue(update.DisplayName),
		update.Email != nil, stringValue(update.Email),
		update.Description != nil, stringValue(update.Description),
		expectedVersion,
	), &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrVersionConflict
	}
	if err != nil {
		return User{}, err
	}
	if err = tx.Commit(); err != nil {
		return User{}, err
	}
	return updated, nil
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func (s *PostgresStore) DeleteUser(ctx context.Context, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, userAdminMutationAdvisoryLock); err != nil {
		return err
	}
	var current User
	err = scanUser(tx.QueryRowContext(ctx, `SELECT `+userColumns+` FROM users WHERE id::text=$1 FOR UPDATE`, id), &current)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if current.Role == "admin" && current.State == UserActive {
		var others int
		if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM users WHERE id<>$1 AND role='admin' AND state=$2`, id, UserActive).Scan(&others); err != nil {
			return err
		}
		if others == 0 {
			return ErrLastActiveAdmin
		}
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM users WHERE id::text=$1`, id); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *PostgresStore) RecordUserLoginSuccess(ctx context.Context, id string, occurredAt time.Time) (User, error) {
	var user User
	err := scanUser(s.db.QueryRowContext(ctx, `UPDATE users SET last_login_at=$2, failed_login_attempts=0, locked_until=NULL, version=version+1, updated_at=$2 WHERE id::text=$1 RETURNING `+userColumns, id, occurredAt.UTC()), &user)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	return user, err
}

func (s *PostgresStore) RecordUserLoginFailure(ctx context.Context, id string, occurredAt time.Time, maxAttempts int, lockout time.Duration) (User, error) {
	var user User
	lockedUntil := occurredAt.UTC().Add(lockout)
	err := scanUser(s.db.QueryRowContext(ctx, `UPDATE users SET failed_login_attempts=failed_login_attempts+1, locked_until=CASE WHEN $3>0 AND failed_login_attempts+1 >= $3 AND $4 THEN $5 ELSE locked_until END, version=version+1, updated_at=$2 WHERE id::text=$1 RETURNING `+userColumns, id, occurredAt.UTC(), maxAttempts, lockout > 0, lockedUntil), &user)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	return user, err
}

func (s *PostgresStore) UpdateUserPassword(ctx context.Context, id, secretHash, expectedVersion string, mustChange bool) (User, error) {
	var user User
	err := scanUser(s.db.QueryRowContext(ctx, `UPDATE users SET secret_hash=$2, password_changed_at=now(), must_change_password=$3, session_version=session_version+1, failed_login_attempts=0, locked_until=NULL, version=version+1, updated_at=now() WHERE id::text=$1 AND version::text=$4 RETURNING `+userColumns, id, secretHash, mustChange, expectedVersion), &user)
	if errors.Is(err, sql.ErrNoRows) {
		if _, getErr := s.GetUser(ctx, id); errors.Is(getErr, ErrNotFound) {
			return User{}, ErrNotFound
		}
		return User{}, ErrVersionConflict
	}
	return user, err
}

func (s *PostgresStore) RevokeUserSessions(ctx context.Context, id, expectedVersion string) (User, error) {
	var user User
	err := scanUser(s.db.QueryRowContext(ctx, `UPDATE users SET session_version=session_version+1, version=version+1, updated_at=now() WHERE id::text=$1 AND version::text=$2 RETURNING `+userColumns, id, expectedVersion), &user)
	if errors.Is(err, sql.ErrNoRows) {
		if _, getErr := s.GetUser(ctx, id); errors.Is(getErr, ErrNotFound) {
			return User{}, ErrNotFound
		}
		return User{}, ErrVersionConflict
	}
	return user, err
}
