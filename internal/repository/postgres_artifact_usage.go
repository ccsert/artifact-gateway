package repository

import (
	"context"
	"database/sql"
	"time"
)

func (s *PostgresStore) QueryArtifactUsage(ctx context.Context, repository string, query ArtifactUsageQuery) (ArtifactUsagePage, error) {
	var page ArtifactUsagePage
	if err := query.Validate(repository); err != nil {
		return page, err
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return page, err
	}
	defer func() { _ = tx.Rollback() }()
	// Aggregate in SQL without returning every address to the application.
	err = tx.QueryRowContext(ctx, `SELECT COALESCE(SUM(download_count), 0), COALESCE(SUM(total_bytes), 0), COUNT(*),
		COUNT(*) FILTER (WHERE strpos(resource, $2) > 0)
		FROM artifact_usage_stats WHERE repository = $1`, repository, query.Query).
		Scan(&page.Totals.DownloadCount, &page.Totals.TotalBytes, &page.Totals.Resources, &page.TotalCount)
	if err != nil {
		return page, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT repository, format, resource, download_count, total_bytes, first_downloaded_at, last_downloaded_at, last_actor
		FROM artifact_usage_stats WHERE repository = $1 AND strpos(resource, $2) > 0
		ORDER BY resource COLLATE "C" ASC, format COLLATE "C" ASC LIMIT $3 OFFSET $4`, repository, query.Query, query.Limit, query.Offset)
	if err != nil {
		return page, err
	}
	page.Items = make([]ArtifactUsageStat, 0, query.Limit)
	for rows.Next() {
		var stat ArtifactUsageStat
		if err := rows.Scan(&stat.Repository, &stat.Format, &stat.Resource, &stat.DownloadCount, &stat.TotalBytes, &stat.FirstDownloadedAt, &stat.LastDownloadedAt, &stat.LastActor); err != nil {
			_ = rows.Close()
			return page, err
		}
		page.Items = append(page.Items, stat)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return page, err
	}
	return page, tx.Commit()
}

const artifactUsageUpsertSQL = `INSERT INTO artifact_usage_stats
	(repository, format, resource, download_count, total_bytes, first_downloaded_at, last_downloaded_at, last_actor)
VALUES ($1, $2, $3, 1, $4, $5, $5, $6)
ON CONFLICT (repository, format, resource) DO UPDATE SET
	download_count = artifact_usage_stats.download_count + 1,
	total_bytes = artifact_usage_stats.total_bytes + EXCLUDED.total_bytes,
	first_downloaded_at = LEAST(artifact_usage_stats.first_downloaded_at, EXCLUDED.first_downloaded_at),
	last_downloaded_at = GREATEST(artifact_usage_stats.last_downloaded_at, EXCLUDED.last_downloaded_at),
	last_actor = CASE WHEN EXCLUDED.last_downloaded_at >= artifact_usage_stats.last_downloaded_at
		THEN EXCLUDED.last_actor ELSE artifact_usage_stats.last_actor END`

// RecordArtifactUsage folds one download increment into the durable
// aggregate. RecordAudit calls it for download audits; tests and seeds may
// call it directly.
func (s *PostgresStore) RecordArtifactUsage(ctx context.Context, stat ArtifactUsageStat) error {
	if stat.LastDownloadedAt.IsZero() {
		stat.LastDownloadedAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, artifactUsageUpsertSQL,
		stat.Repository, stat.Format, stat.Resource, stat.TotalBytes, stat.LastDownloadedAt, stat.LastActor)
	return err
}

func (s *PostgresStore) ListArtifactUsage(ctx context.Context, repository string, limit int) ([]ArtifactUsageStat, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `SELECT repository, format, resource, download_count, total_bytes, first_downloaded_at, last_downloaded_at, last_actor
		FROM artifact_usage_stats
		WHERE ($1 = '' OR repository = $1)
		ORDER BY download_count DESC, resource ASC
		LIMIT $2`, repository, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	stats := make([]ArtifactUsageStat, 0, limit)
	for rows.Next() {
		var stat ArtifactUsageStat
		if err := rows.Scan(&stat.Repository, &stat.Format, &stat.Resource, &stat.DownloadCount, &stat.TotalBytes, &stat.FirstDownloadedAt, &stat.LastDownloadedAt, &stat.LastActor); err != nil {
			return nil, err
		}
		stats = append(stats, stat)
	}
	return stats, rows.Err()
}

func (s *PostgresStore) WalkArtifactUsage(ctx context.Context, repository string, visit func(ArtifactUsageStat) error) error {
	rows, err := s.db.QueryContext(ctx, `SELECT repository, format, resource, download_count, total_bytes, first_downloaded_at, last_downloaded_at, last_actor
		FROM artifact_usage_stats WHERE repository = $1`, repository)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var stat ArtifactUsageStat
		if err := rows.Scan(&stat.Repository, &stat.Format, &stat.Resource, &stat.DownloadCount, &stat.TotalBytes, &stat.FirstDownloadedAt, &stat.LastDownloadedAt, &stat.LastActor); err != nil {
			return err
		}
		if err := visit(stat); err != nil {
			return err
		}
	}
	return rows.Err()
}

func (s *PostgresStore) ArtifactUsageTotals(ctx context.Context, repository string) (ArtifactUsageTotals, error) {
	var totals ArtifactUsageTotals
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(download_count), 0), COALESCE(SUM(total_bytes), 0), COUNT(*)
		FROM artifact_usage_stats
		WHERE ($1 = '' OR repository = $1)`, repository).
		Scan(&totals.DownloadCount, &totals.TotalBytes, &totals.Resources)
	return totals, err
}
