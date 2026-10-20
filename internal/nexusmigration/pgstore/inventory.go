package pgstore

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"

	m "github.com/ccsert/artifact-gateway/internal/nexusmigration"
	"github.com/ccsert/artifact-gateway/internal/nexussource"
)

func (s *Store) InventoryScan(ctx context.Context, id string) (m.InventoryScan, error) {
	return readInventoryScan(ctx, s.db, id)
}

type inventoryQuerier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func readInventoryScan(ctx context.Context, q inventoryQuerier, id string) (m.InventoryScan, error) {
	var v m.InventoryScan
	var repos []byte
	err := q.QueryRowContext(ctx, `SELECT binding_sha256,repositories,repository_index,cursor,page_count,verified_pages,asset_count,metadata_bytes FROM nexus_migration_scans WHERE batch_id::text=$1`, id).Scan(&v.BindingSHA256, &repos, &v.RepositoryIndex, &v.Cursor, &v.PageCount, &v.VerifiedPages, &v.AssetCount, &v.MetadataBytes)
	if errors.Is(err, sql.ErrNoRows) {
		return v, m.ErrNotFound
	}
	if err != nil {
		return v, err
	}
	if err = json.Unmarshal(repos, &v.Repositories); err != nil {
		return v, err
	}
	if len(v.Repositories) == 0 || v.RepositoryIndex > len(v.Repositories) {
		return v, m.Failed("source_changed")
	}
	return v, nil
}

// All scan mutations take the same transaction fence and row lock as control
// and publication. Once pause/cancel returns no old worker advances a receipt.
func (s *Store) inventoryTransaction(ctx context.Context, b m.Batch) (*sql.Tx, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	bad := func(err error) (*sql.Tx, error) { _ = tx.Rollback(); return nil, err }
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('nexus-migration-lease:'||$1,0))`, b.ID); err != nil {
		return bad(err)
	}
	actual, err := scanBatch(tx.QueryRowContext(ctx, `SELECT `+batchColumns+` FROM nexus_migration_batches b WHERE id::text=$1 FOR UPDATE`, b.ID))
	if err != nil {
		return bad(err)
	}
	if actual.LeaseToken != b.LeaseToken || actual.LeaseToken == "" || actual.ScanComplete || (actual.State != "running" && actual.State != "pausing") {
		return bad(m.ErrLeaseLost)
	}
	var fresh bool
	if err = tx.QueryRowContext(ctx, `SELECT lease_expires_at>clock_timestamp() FROM nexus_migration_batches WHERE id::text=$1`, b.ID).Scan(&fresh); err != nil {
		return bad(err)
	}
	if !fresh {
		return bad(m.ErrLeaseLost)
	}
	if actual.CancelRequested || actual.PauseRequested {
		return bad(m.ErrStopped)
	}
	if m.InventoryBinding(actual, nil) != m.InventoryBinding(b, nil) {
		return bad(m.Failed("source_changed"))
	}
	return tx, nil
}

func (s *Store) BeginInventoryScan(ctx context.Context, b m.Batch, want m.InventoryScan) error {
	if len(want.Repositories) < 1 || len(want.Repositories) != len(b.Mappings) || want.BindingSHA256 != m.InventoryBinding(b, want.Repositories) {
		return m.Failed("source_changed")
	}
	tx, err := s.inventoryTransaction(ctx, b)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, `INSERT INTO nexus_migration_scans(batch_id,binding_sha256,repositories) VALUES($1,$2,$3) ON CONFLICT(batch_id) DO NOTHING`, b.ID, want.BindingSHA256, mustJSON(want.Repositories)); err != nil {
		return err
	}
	current, err := readInventoryScan(ctx, tx, b.ID)
	if err != nil {
		return err
	}
	if current.BindingSHA256 != want.BindingSHA256 {
		return m.Failed("source_changed")
	}
	return commitInventory(ctx, tx, b)
}

func cursorHash(cursor string) string {
	sum := sha256.Sum256([]byte(cursor))
	return hex.EncodeToString(sum[:])
}

func (s *Store) AppendInventoryPage(ctx context.Context, b m.Batch, expected m.InventoryScan, page nexussource.InventoryPage) error {
	if page.MetadataBytes < 0 || len(page.NextCursor) > 4096 || strings.ContainsAny(page.NextCursor, "\r\n\x00") {
		return m.Failed("source_invalid_response")
	}
	tx, err := s.inventoryTransaction(ctx, b)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	current, err := readInventoryScan(ctx, tx, b.ID)
	if err != nil {
		return err
	}
	if current.BindingSHA256 != m.InventoryBinding(b, current.Repositories) || current.BindingSHA256 != expected.BindingSHA256 {
		return m.Failed("source_changed")
	}
	if current.Enumerated() || current.PageCount != expected.PageCount || current.RepositoryIndex != expected.RepositoryIndex || current.Cursor != expected.Cursor {
		return m.ErrVersionConflict
	}
	count := int64(len(page.Assets) + len(page.UnsupportedAssets))
	if current.PageCount >= m.MaxInventoryPages || count > m.MaxInventoryAssets-current.AssetCount || page.MetadataBytes > m.MaxInventoryMetadataBytes-current.MetadataBytes {
		return m.Failed("source_limit_exceeded")
	}
	repo := current.Repositories[current.RepositoryIndex].Name
	insertPath := func(repository, path string) error {
		if repository != repo || path == "" {
			return m.Failed("source_invalid_response")
		}
		result, err := tx.ExecContext(ctx, `INSERT INTO nexus_migration_scan_paths(batch_id,source_repository,source_path) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, b.ID, repo, path)
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return m.Failed("source_duplicate_asset")
		}
		return nil
	}
	for _, a := range page.Assets {
		if err = insertPath(a.Repository, a.Path); err != nil {
			return err
		}
	}
	for _, a := range page.UnsupportedAssets {
		if err = insertPath(a.Repository, a.Path); err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO nexus_migration_scan_pages(batch_id,ordinal,source_repository,cursor,cursor_sha256,page_sha256,page) VALUES($1,$2,$3,$4,$5,$6,$7)`, b.ID, current.PageCount, repo, current.Cursor, cursorHash(current.Cursor), m.InventoryPageDigest(page), mustJSON(page))
	if err != nil {
		return err
	}
	if page.NextCursor != "" {
		var repeated bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM nexus_migration_scan_pages WHERE batch_id=$1 AND source_repository=$2 AND cursor_sha256=$3)`, b.ID, repo, cursorHash(page.NextCursor)).Scan(&repeated); err != nil {
			return err
		}
		if repeated {
			return m.Failed("source_pagination_cycle")
		}
	} else {
		current.RepositoryIndex++
	}
	_, err = tx.ExecContext(ctx, `UPDATE nexus_migration_scans SET repository_index=$2,cursor=$3,page_count=page_count+1,asset_count=asset_count+$4,metadata_bytes=metadata_bytes+$5 WHERE batch_id=$1`, b.ID, current.RepositoryIndex, page.NextCursor, count, page.MetadataBytes)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE nexus_migration_batches SET metadata_discovered=$2,version=version+1,updated_at=clock_timestamp() WHERE id=$1`, b.ID, current.AssetCount+count)
	if err != nil {
		return err
	}
	return commitInventory(ctx, tx, b)
}

func (s *Store) InventoryPage(ctx context.Context, id string, ordinal int64) (m.InventoryPage, error) {
	var out m.InventoryPage
	var page []byte
	err := s.db.QueryRowContext(ctx, `SELECT source_repository,cursor,page_sha256,page FROM nexus_migration_scan_pages WHERE batch_id::text=$1 AND ordinal=$2`, id, ordinal).Scan(&out.SourceRepository, &out.Cursor, &out.SHA256, &page)
	if errors.Is(err, sql.ErrNoRows) {
		return out, m.ErrNotFound
	}
	if err != nil {
		return out, err
	}
	if err = json.Unmarshal(page, &out.Page); err != nil {
		return out, err
	}
	if m.InventoryPageDigest(out.Page) != out.SHA256 {
		return out, m.Failed("source_changed")
	}
	return out, nil
}

func (s *Store) VerifyInventoryPage(ctx context.Context, b m.Batch, ordinal int64, hash string, metadataBytes int64) error {
	if metadataBytes < 0 {
		return m.Failed("source_invalid_response")
	}
	tx, err := s.inventoryTransaction(ctx, b)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	current, err := readInventoryScan(ctx, tx, b.ID)
	if err != nil {
		return err
	}
	if current.BindingSHA256 != m.InventoryBinding(b, current.Repositories) {
		return m.Failed("source_changed")
	}
	if metadataBytes > m.MaxInventoryMetadataBytes-current.MetadataBytes {
		return m.Failed("source_limit_exceeded")
	}
	if !current.Enumerated() || current.VerifiedPages != ordinal || ordinal >= current.PageCount {
		return m.ErrVersionConflict
	}
	var expected string
	if err = tx.QueryRowContext(ctx, `SELECT page_sha256 FROM nexus_migration_scan_pages WHERE batch_id=$1 AND ordinal=$2`, b.ID, ordinal).Scan(&expected); err != nil {
		return err
	}
	if expected != hash {
		return m.Failed("source_changed")
	}
	if _, err = tx.ExecContext(ctx, `UPDATE nexus_migration_scans SET verified_pages=verified_pages+1,metadata_bytes=metadata_bytes+$2 WHERE batch_id=$1`, b.ID, metadataBytes); err != nil {
		return err
	}
	return commitInventory(ctx, tx, b)
}

// A page with many paths may outlive its initial lease check. Recheck database
// time before committing the same transaction; an expired receipt is rolled back.
func commitInventory(ctx context.Context, tx *sql.Tx, b m.Batch) error {
	if err := validLease(ctx, tx, b.ID, b.LeaseToken); err != nil {
		return err
	}
	return tx.Commit()
}
