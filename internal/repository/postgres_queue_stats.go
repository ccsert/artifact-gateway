package repository

import "context"

func (s *PostgresStore) BackgroundOperationQueueStats(ctx context.Context) ([]BackgroundOperationQueueStat, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT operation_kind,format,state,count(*)::bigint,min(created_at)
		FROM (
			SELECT CASE kind
				WHEN 'promotion' THEN 'promotion'
				WHEN 'replication' THEN 'replication'
				ELSE 'lifecycle'
			END AS operation_kind,payload->>'format' AS format,state,created_at
			FROM lifecycle_jobs
			WHERE state IN ('pending','retrying','running','failed')
			UNION ALL
			SELECT 'replication',format::text,
				CASE WHEN state='failed' AND attempts<max_attempts THEN 'retrying' ELSE state END,
				created_at
			FROM replication_plans
			WHERE state IN ('pending','retrying','running','failed')
		) queued
		WHERE format IS NOT NULL AND format<>''
		GROUP BY operation_kind,format,state
		ORDER BY operation_kind,format,state`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	stats := make([]BackgroundOperationQueueStat, 0)
	for rows.Next() {
		var stat BackgroundOperationQueueStat
		if err := rows.Scan(&stat.Kind, &stat.Format, &stat.State, &stat.Count, &stat.OldestCreatedAt); err != nil {
			return nil, err
		}
		stats = append(stats, stat)
	}
	return stats, rows.Err()
}
