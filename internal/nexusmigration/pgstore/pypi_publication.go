package pgstore

import (
	"context"
	"database/sql"

	m "github.com/ccsert/artifact-gateway/internal/nexusmigration"
	"github.com/ccsert/artifact-gateway/internal/repository"
)

// PublicationEntryContext binds the PyPI closure checkpoint to the same native
// transaction that owns publication and audit. Existing formats retain their
// established recovery contract.
func (s *Store) PublicationEntryContext(ctx context.Context, e m.Entry, token string) context.Context {
	ctx = s.PublicationContext(ctx, e.BatchID, token)
	if e.Format != "pypi" {
		return ctx
	}
	return repository.WithNativePublicationCompletion(ctx, func(ctx context.Context, tx *sql.Tx) error {
		if err := validLease(ctx, tx, e.BatchID, token); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `UPDATE nexus_migration_entries SET state='committed',assets=$3,verified_bytes=$4,visible_identities=1,last_error='',updated_at=clock_timestamp() WHERE id::text=$1 AND batch_id::text=$2 AND format='pypi' AND identity=$5 AND target_repository_id::text=$6 AND (SELECT jsonb_agg(a-'uploaded'-'verified') FROM jsonb_array_elements(assets) a)=(SELECT jsonb_agg(a-'uploaded'-'verified') FROM jsonb_array_elements($3::jsonb) a)`, e.ID, e.BatchID, encodeAssets(e.Assets), e.VerifiedBytes, e.Identity, e.TargetRepositoryID)
		return changed(result, err)
	})
}
