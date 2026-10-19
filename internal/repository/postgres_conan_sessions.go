package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

func (s *PostgresStore) CreateConanPublishSession(ctx context.Context, session ConanPublishSession) (ConanPublishSession, error) {
	objects, err := json.Marshal(session.Objects)
	if err != nil {
		return ConanPublishSession{}, err
	}
	if session.State == "" {
		session.State = "open"
	}
	session.Native, session.Version = false, 1
	_, err = s.db.ExecContext(ctx, `INSERT INTO native_conan_publish_sessions (id,repository_id,publisher,kind,reference,recipe_revision,package_id,package_revision,state,expires_at,objects,version) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,1)`, session.ID, session.RepositoryID, session.Publisher, session.Kind, session.Reference, session.RecipeRevision, session.PackageID, session.PackageRevision, session.State, session.ExpiresAt, objects)
	return session, err
}

func (s *PostgresStore) GetConanPublishSession(ctx context.Context, id string) (ConanPublishSession, error) {
	var session ConanPublishSession
	var objects []byte
	err := s.db.QueryRowContext(ctx, `SELECT `+conanSessionColumns+` FROM native_conan_publish_sessions WHERE id::text=$1`, id).Scan(&session.ID, &session.RepositoryID, &session.Publisher, &session.Kind, &session.Reference, &session.RecipeRevision, &session.PackageID, &session.PackageRevision, &session.State, &session.ExpiresAt, &objects, &session.Native, &session.Version)
	if errors.Is(err, sql.ErrNoRows) {
		return ConanPublishSession{}, ErrNotFound
	}
	if err != nil {
		return ConanPublishSession{}, err
	}
	if err = json.Unmarshal(objects, &session.Objects); err != nil {
		return ConanPublishSession{}, err
	}
	return session, nil
}

func (s *PostgresStore) ListConanPublishUploads(ctx context.Context, sessionID string) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT object_name,object_key FROM native_conan_publish_uploads WHERE session_id::text=$1`, sessionID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	uploads := map[string]string{}
	for rows.Next() {
		var name, key string
		if err := rows.Scan(&name, &key); err != nil {
			return nil, err
		}
		uploads[name] = key
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if _, err = s.GetConanPublishSession(ctx, sessionID); err != nil {
		return nil, err
	}
	return uploads, nil
}
