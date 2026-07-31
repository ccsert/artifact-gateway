package repository

import (
	"context"
	"database/sql"
	"errors"
)

const userColumns = `id::text, name, secret_hash, role, state, created_at, updated_at, version::text`

func scanUser(row interface{ Scan(...any) error }, user *User) error {
	return row.Scan(&user.ID, &user.Name, &user.SecretHash, &user.Role, &user.State, &user.CreatedAt, &user.UpdatedAt, &user.Version)
}

func (s *PostgresStore) CreateUser(ctx context.Context, user User) (User, error) {
	err := scanUser(s.db.QueryRowContext(ctx, `INSERT INTO users (id, name, secret_hash, role) VALUES ($1,$2,$3,$4) RETURNING `+userColumns, user.ID, user.Name, user.SecretHash, user.Role), &user)
	if isUnique(err) {
		return User{}, ErrNameExists
	}
	if err != nil {
		return User{}, err
	}
	return user, nil
}

func (s *PostgresStore) ListUsers(ctx context.Context) ([]User, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+userColumns+` FROM users ORDER BY created_at, id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	users := []User{}
	for rows.Next() {
		var user User
		if err := scanUser(rows, &user); err != nil {
			return nil, err
		}
		users = append(users, user)
	}
	return users, rows.Err()
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
	err := scanUser(s.db.QueryRowContext(ctx, `SELECT `+userColumns+` FROM users WHERE name=$1`, name), &user)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, err
	}
	return user, nil
}

func (s *PostgresStore) UpdateUser(ctx context.Context, user User, expectedVersion string) (User, error) {
	var updated User
	err := scanUser(s.db.QueryRowContext(ctx, `UPDATE users SET role=$2, state=COALESCE(NULLIF($3,''), state), version=version+1, updated_at=now() WHERE id::text=$1 AND version::text=$4 RETURNING `+userColumns, user.ID, user.Role, user.State, expectedVersion), &updated)
	if errors.Is(err, sql.ErrNoRows) {
		if _, getErr := s.GetUser(ctx, user.ID); errors.Is(getErr, ErrNotFound) {
			return User{}, ErrNotFound
		}
		return User{}, ErrVersionConflict
	}
	if err != nil {
		return User{}, err
	}
	return updated, nil
}

func (s *PostgresStore) DeleteUser(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM users WHERE id::text=$1`, id)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return ErrNotFound
	}
	return nil
}
