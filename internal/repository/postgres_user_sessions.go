package repository

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/google/uuid"
)

const userSessionColumns = `id::text,user_id::text,kind,ip_address,user_agent,created_at,expires_at,revoked_at`

func scanUserSession(scanner interface{ Scan(...any) error }, session *UserSession) error {
	return scanner.Scan(
		&session.ID, &session.UserID, &session.Kind, &session.IPAddress,
		&session.UserAgent, &session.CreatedAt, &session.ExpiresAt, &session.RevokedAt,
	)
}

func (s *PostgresStore) CreateUserSession(ctx context.Context, session UserSession) (UserSession, error) {
	if _, err := s.GetUser(ctx, session.UserID); err != nil {
		return UserSession{}, err
	}
	if session.ID == "" {
		session.ID = uuid.NewString()
	}
	if session.CreatedAt.IsZero() {
		session.CreatedAt = time.Now().UTC()
	}
	if !validUserSession(session) {
		return UserSession{}, ErrInvalidUserSession
	}
	err := scanUserSession(s.db.QueryRowContext(ctx, `INSERT INTO user_sessions (id,user_id,kind,ip_address,user_agent,created_at,expires_at,revoked_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8) RETURNING `+userSessionColumns,
		session.ID, session.UserID, session.Kind, session.IPAddress, session.UserAgent,
		session.CreatedAt, session.ExpiresAt, session.RevokedAt,
	), &session)
	return session, err
}

func (s *PostgresStore) GetUserSession(ctx context.Context, userID, sessionID string) (UserSession, error) {
	var session UserSession
	err := scanUserSession(s.db.QueryRowContext(ctx, `SELECT `+userSessionColumns+` FROM user_sessions WHERE id::text=$1 AND user_id::text=$2`, sessionID, userID), &session)
	if errors.Is(err, sql.ErrNoRows) {
		return UserSession{}, ErrNotFound
	}
	return session, err
}

func (s *PostgresStore) ListUserSessions(ctx context.Context, userID string, includeInactive bool) ([]UserSession, error) {
	if _, err := s.GetUser(ctx, userID); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+userSessionColumns+` FROM user_sessions WHERE user_id::text=$1 AND ($2 OR (revoked_at IS NULL AND expires_at>now())) ORDER BY created_at DESC,id`, userID, includeInactive)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	items := make([]UserSession, 0)
	for rows.Next() {
		var session UserSession
		if err := scanUserSession(rows, &session); err != nil {
			return nil, err
		}
		items = append(items, session)
	}
	return items, rows.Err()
}

func (s *PostgresStore) RevokeUserSession(ctx context.Context, userID, sessionID string) (UserSession, error) {
	var session UserSession
	err := scanUserSession(s.db.QueryRowContext(ctx, `UPDATE user_sessions SET revoked_at=COALESCE(revoked_at,now()) WHERE id::text=$1 AND user_id::text=$2 RETURNING `+userSessionColumns, sessionID, userID), &session)
	if errors.Is(err, sql.ErrNoRows) {
		return UserSession{}, ErrNotFound
	}
	return session, err
}

func (s *PostgresStore) RevokeAllUserSessionRecords(ctx context.Context, userID string, occurredAt time.Time) error {
	if _, err := s.GetUser(ctx, userID); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `UPDATE user_sessions SET revoked_at=$2 WHERE user_id::text=$1 AND revoked_at IS NULL`, userID, occurredAt.UTC())
	return err
}

func (s *PostgresStore) PruneExpiredUserSessions(ctx context.Context, before time.Time, limit int) (int, error) {
	if limit <= 0 {
		return 0, nil
	}
	result, err := s.db.ExecContext(ctx, `WITH expired AS (
		SELECT id FROM user_sessions WHERE expires_at<=$1 ORDER BY expires_at,id FOR UPDATE SKIP LOCKED LIMIT $2
	) DELETE FROM user_sessions USING expired WHERE user_sessions.id=expired.id`, before.UTC(), limit)
	if err != nil {
		return 0, err
	}
	affected, err := result.RowsAffected()
	return int(affected), err
}
