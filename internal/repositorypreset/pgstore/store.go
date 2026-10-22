// Package pgstore owns the atomic first-install repository initialization.
package pgstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/ccsert/artifact-gateway/internal/repositorypreset"
	"github.com/google/uuid"
)

type Store struct{ db *sql.DB }

func New(db *sql.DB) *Store { return &Store{db: db} }

// Initialize changes an empty, pending catalog exactly once. The marker survives
// repository deletion, and the transaction prevents a partially installed set.
func (s *Store) Initialize(ctx context.Context) (repositorypreset.Result, error) {
	result := repositorypreset.Result{}
	if s == nil || s.db == nil {
		return result, errors.New("repository presets require a database")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return result, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := tx.QueryRowContext(ctx, `SELECT state, catalog_version, repositories_created, groups_created
		FROM installation_repository_presets WHERE singleton=true FOR UPDATE`).Scan(
		&result.State, &result.Version, &result.Repositories, &result.Groups); err != nil {
		return result, err
	}
	if result.State == "applied" || result.State == "skipped_existing" {
		return result, tx.Commit()
	}
	if result.State != "pending" || result.Version != repositorypreset.Version {
		return result, errors.New("repository preset initialization state is invalid")
	}
	// Also serialize with ordinary management writes, which do not lock the
	// installation marker. A repository created since migration wins over presets.
	if _, err := tx.ExecContext(ctx, `LOCK TABLE hosted_repositories, hosted_groups IN SHARE ROW EXCLUSIVE MODE`); err != nil {
		return result, err
	}
	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM hosted_repositories)
		OR EXISTS(SELECT 1 FROM hosted_groups)`).Scan(&exists); err != nil {
		return result, err
	}
	if exists {
		result.State = "skipped_existing"
	} else {
		catalog := repositorypreset.Standard()
		ids := make(map[string]string, len(catalog.Repositories))
		for _, repo := range catalog.Repositories {
			id := uuid.NewString()
			var egress any
			if repo.EgressProxy != nil {
				encoded, err := json.Marshal(repo.EgressProxy)
				if err != nil {
					return result, err
				}
				egress = string(encoded)
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO hosted_repositories
				(id, name, format, repo_type, endpoint, allowed_hosts, anonymous_read, egress_proxy)
				VALUES ($1,$2,$3,$4,$5,COALESCE($6::text[],'{}'::text[]),false,$7)`,
				id, repo.Name, repo.Format, repo.Type, repo.Endpoint, repo.AllowedHosts, egress); err != nil {
				return result, fmt.Errorf("create preset repository: %w", err)
			}
			ids[repo.Name] = id
		}
		for _, group := range catalog.Groups {
			id := uuid.NewString()
			if _, err := tx.ExecContext(ctx, `INSERT INTO hosted_groups (id,name,format,anonymous_read)
				VALUES ($1,$2,$3,false)`, id, group.Name, group.Format); err != nil {
				return result, fmt.Errorf("create preset group: %w", err)
			}
			for position, name := range group.Members {
				memberID, ok := ids[name]
				if !ok {
					return result, errors.New("preset group member is missing")
				}
				if _, err := tx.ExecContext(ctx, `INSERT INTO hosted_group_members (group_id,repository_id,position)
					VALUES ($1,$2,$3)`, id, memberID, position); err != nil {
					return result, err
				}
			}
		}
		result.State, result.Repositories, result.Groups = "applied", len(catalog.Repositories), len(catalog.Groups)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE installation_repository_presets
		SET state=$1, repositories_created=$2, groups_created=$3, finished_at=now()
		WHERE singleton=true`, result.State, result.Repositories, result.Groups); err != nil {
		return result, err
	}
	return result, tx.Commit()
}
