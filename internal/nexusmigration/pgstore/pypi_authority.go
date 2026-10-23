package pgstore

import (
	"context"
	"database/sql"
	"errors"

	m "github.com/ccsert/artifact-gateway/internal/nexusmigration"
	"github.com/ccsert/artifact-gateway/internal/repository"
)

// Only PyPI adds durable owner admission. The actor kind is server-authenticated
// and persisted; all existing format paths retain their prior admission rules.
func checkPyPIMigrationActor(ctx context.Context, tx *sql.Tx, actor, kind, ownerID string, mappings []m.Mapping) error {
	var requiresOwner bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM hosted_repositories r JOIN jsonb_array_elements($1::jsonb) mp ON r.id::text=mp->>'targetRepositoryId' WHERE r.format='pypi')`, mustJSON(mappings)).Scan(&requiresOwner)
	if err != nil || !requiresOwner {
		return err
	}
	if err = repository.CheckPyPIImportActorTx(ctx, tx, actor, kind, ownerID); errors.Is(err, repository.ErrDisabled) {
		return m.Failed("pypi_actor_not_current_admin")
	}
	return err
}
