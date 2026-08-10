package repository

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

const userIdentityColumns = `id::text,user_id::text,kind,issuer,subject,email,display_name,email_verified,last_login_at,created_at,updated_at`

type userIdentityScanner interface {
	Scan(...any) error
}

func scanUserIdentity(scanner userIdentityScanner, identity *UserIdentity) error {
	return scanner.Scan(
		&identity.ID, &identity.UserID, &identity.Kind, &identity.Issuer,
		&identity.Subject, &identity.Email, &identity.DisplayName,
		&identity.EmailVerified, &identity.LastLoginAt, &identity.CreatedAt,
		&identity.UpdatedAt,
	)
}

func (s *PostgresStore) ListUserIdentities(ctx context.Context, userID string) ([]UserIdentity, error) {
	if _, err := s.GetUser(ctx, userID); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+userIdentityColumns+` FROM user_identities WHERE user_id::text=$1 ORDER BY created_at,id`, userID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	items := make([]UserIdentity, 0)
	for rows.Next() {
		var identity UserIdentity
		if err := scanUserIdentity(rows, &identity); err != nil {
			return nil, err
		}
		items = append(items, identity)
	}
	return items, rows.Err()
}

func (s *PostgresStore) CreateUserIdentity(ctx context.Context, identity UserIdentity) (UserIdentity, error) {
	if _, err := s.GetUser(ctx, identity.UserID); err != nil {
		return UserIdentity{}, err
	}
	identity = normalizeUserIdentity(identity)
	if identity.ID == "" {
		identity.ID = uuid.NewString()
	}
	err := scanUserIdentity(s.db.QueryRowContext(ctx, `INSERT INTO user_identities (id,user_id,kind,issuer,subject,email,display_name,email_verified) VALUES ($1,$2,$3,$4,$5,$6,$7,$8) RETURNING `+userIdentityColumns,
		identity.ID, identity.UserID, identity.Kind, identity.Issuer, identity.Subject,
		identity.Email, identity.DisplayName, identity.EmailVerified,
	), &identity)
	if isUnique(err) {
		return UserIdentity{}, ErrIdentityExists
	}
	return identity, err
}

func (s *PostgresStore) DeleteUserIdentity(ctx context.Context, userID, identityID string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM user_identities WHERE id::text=$1 AND user_id::text=$2`, identityID, userID)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PostgresStore) GetUserByOIDCIdentity(ctx context.Context, issuer, subject string) (User, UserIdentity, error) {
	var identity UserIdentity
	err := scanUserIdentity(s.db.QueryRowContext(ctx, `SELECT `+userIdentityColumns+` FROM user_identities WHERE kind=$1 AND issuer=$2 AND subject=$3`, UserIdentityOIDC, normalizeOIDCIssuer(issuer), strings.TrimSpace(subject)), &identity)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, UserIdentity{}, ErrNotFound
	}
	if err != nil {
		return User{}, UserIdentity{}, err
	}
	user, err := s.GetUser(ctx, identity.UserID)
	return user, identity, err
}

func (s *PostgresStore) ResolveOIDCIdentity(ctx context.Context, provision OIDCIdentityProvision) (User, UserIdentity, bool, error) {
	provision.Issuer = normalizeOIDCIssuer(provision.Issuer)
	provision.Subject = strings.TrimSpace(provision.Subject)
	if provision.Issuer == "" || provision.Subject == "" {
		return User{}, UserIdentity{}, false, ErrNotFound
	}
	now := provision.OccurredAt.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return User{}, UserIdentity{}, false, err
	}
	defer func() { _ = tx.Rollback() }()

	var identity UserIdentity
	err = scanUserIdentity(tx.QueryRowContext(ctx, `SELECT `+userIdentityColumns+` FROM user_identities WHERE kind=$1 AND issuer=$2 AND subject=$3 FOR UPDATE`, UserIdentityOIDC, provision.Issuer, provision.Subject), &identity)
	if err == nil {
		user, err := updateOIDCLogin(ctx, tx, identity, provision, now)
		if err != nil {
			return User{}, UserIdentity{}, false, err
		}
		identity = refreshUserIdentity(identity, provision, now)
		if err = tx.Commit(); err != nil {
			return User{}, UserIdentity{}, false, err
		}
		return user, identity, false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return User{}, UserIdentity{}, false, err
	}
	if !provision.Provision {
		return User{}, UserIdentity{}, false, ErrNotFound
	}

	var user User
	if provision.MatchEmail && provision.EmailVerified && strings.TrimSpace(provision.Email) != "" {
		rows, err := tx.QueryContext(ctx, `SELECT `+userColumns+` FROM users WHERE lower(email)=lower($1) ORDER BY created_at,id LIMIT 2 FOR UPDATE`, strings.TrimSpace(provision.Email))
		if err != nil {
			return User{}, UserIdentity{}, false, err
		}
		matches := make([]User, 0, 2)
		for rows.Next() {
			var candidate User
			if err := scanUser(rows, &candidate); err != nil {
				_ = rows.Close()
				return User{}, UserIdentity{}, false, err
			}
			matches = append(matches, candidate)
		}
		if err := rows.Close(); err != nil {
			return User{}, UserIdentity{}, false, err
		}
		if len(matches) > 1 {
			return User{}, UserIdentity{}, false, ErrIdentityAmbiguous
		}
		if len(matches) == 1 {
			user = matches[0]
		}
	}

	created := false
	if user.ID == "" {
		role := provision.Role
		if role != "admin" && role != "writer" && role != "reader" {
			role = provision.DefaultRole
		}
		if role != "admin" && role != "writer" && role != "reader" {
			role = "reader"
		}
		name, err := postgresProvisionedUsername(ctx, tx, provision)
		if err != nil {
			return User{}, UserIdentity{}, false, err
		}
		user = User{
			ID: uuid.NewString(), Name: name, DisplayName: strings.TrimSpace(provision.DisplayName),
			Email: strings.TrimSpace(provision.Email), Role: role, State: UserActive,
			LastLoginAt: timePointer(now), SessionVersion: 1,
		}
		err = scanUser(tx.QueryRowContext(ctx, `INSERT INTO users (id,name,display_name,email,description,secret_hash,role,state,last_login_at,password_changed_at,session_version) VALUES ($1,$2,$3,$4,'','',$5,$6,$7,NULL,1) RETURNING `+userColumns,
			user.ID, user.Name, user.DisplayName, user.Email, user.Role, user.State, now,
		), &user)
		if isUnique(err) {
			return User{}, UserIdentity{}, false, ErrNameExists
		}
		if err != nil {
			return User{}, UserIdentity{}, false, err
		}
		created = true
	} else {
		var exists bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM user_identities WHERE user_id=$1 AND kind=$2 AND issuer=$3)`, user.ID, UserIdentityOIDC, provision.Issuer).Scan(&exists); err != nil {
			return User{}, UserIdentity{}, false, err
		}
		if exists {
			return User{}, UserIdentity{}, false, ErrIdentityExists
		}
		if err = scanUser(tx.QueryRowContext(ctx, `UPDATE users SET last_login_at=$2,updated_at=$2,version=version+1 WHERE id=$1 RETURNING `+userColumns, user.ID, now), &user); err != nil {
			return User{}, UserIdentity{}, false, err
		}
	}

	identity = refreshUserIdentity(UserIdentity{
		ID: uuid.NewString(), UserID: user.ID, Kind: UserIdentityOIDC,
		Issuer: provision.Issuer, Subject: provision.Subject, CreatedAt: now,
	}, provision, now)
	err = scanUserIdentity(tx.QueryRowContext(ctx, `INSERT INTO user_identities (id,user_id,kind,issuer,subject,email,display_name,email_verified,last_login_at,created_at,updated_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$10) RETURNING `+userIdentityColumns,
		identity.ID, identity.UserID, identity.Kind, identity.Issuer, identity.Subject,
		identity.Email, identity.DisplayName, identity.EmailVerified, identity.LastLoginAt, now,
	), &identity)
	if isUnique(err) {
		return User{}, UserIdentity{}, false, ErrIdentityExists
	}
	if err != nil {
		return User{}, UserIdentity{}, false, err
	}
	if err = tx.Commit(); err != nil {
		return User{}, UserIdentity{}, false, err
	}
	return user, identity, created, nil
}

func updateOIDCLogin(ctx context.Context, tx *sql.Tx, identity UserIdentity, provision OIDCIdentityProvision, now time.Time) (User, error) {
	identity = refreshUserIdentity(identity, provision, now)
	if _, err := tx.ExecContext(ctx, `UPDATE user_identities SET email=$2,display_name=$3,email_verified=$4,last_login_at=$5,updated_at=$5 WHERE id=$1`, identity.ID, identity.Email, identity.DisplayName, identity.EmailVerified, now); err != nil {
		return User{}, err
	}
	var user User
	err := scanUser(tx.QueryRowContext(ctx, `UPDATE users SET last_login_at=$2,updated_at=$2,version=version+1 WHERE id=$1 RETURNING `+userColumns, identity.UserID, now), &user)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	return user, err
}

func postgresProvisionedUsername(ctx context.Context, tx *sql.Tx, provision OIDCIdentityProvision) (string, error) {
	base := provisionedUsernameBase(provision.PreferredUsername, provision.Email)
	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM users WHERE lower(name)=lower($1))`, base).Scan(&exists); err != nil {
		return "", err
	}
	if !exists {
		return base, nil
	}
	return provisionedUsernameSuffix(base, provision.Issuer, provision.Subject), nil
}
