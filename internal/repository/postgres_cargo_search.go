package repository

import "context"

const cargoSearchPredicate = `n.repository_id::text=$1 AND position(lower($2) in lower(n.name))>0
	AND EXISTS (SELECT 1 FROM native_cargo_publications visible
		WHERE visible.repository_id=n.repository_id AND visible.collision_key=n.collision_key
		AND ($3 OR NOT visible.yanked))`

func (s *PostgresStore) SearchCargoCrates(ctx context.Context, repositoryID, query string, limit int, after string, includeYanked bool) ([]CargoCrateSummary, int, error) {
	if limit <= 0 || limit > 201 {
		limit = 10
	}
	afterKey, err := cargoSearchAfterKey(after)
	if err != nil {
		return nil, 0, err
	}
	var total int
	err = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM native_cargo_names n WHERE `+cargoSearchPredicate,
		repositoryID, query, includeYanked).Scan(&total)
	if err != nil {
		return nil, 0, err
	}
	rows, err := s.db.QueryContext(ctx, `WITH selected AS (
		SELECT n.repository_id,n.collision_key,n.name,n.normalized_name
		FROM native_cargo_names n WHERE `+cargoSearchPredicate+` AND n.collision_key>$5
		ORDER BY n.collision_key LIMIT $4
	) SELECT n.collision_key,n.name,r.version,p.description
	FROM selected n JOIN native_cargo_publications p USING (repository_id,collision_key)
	JOIN native_cargo_identity_reservations r USING (repository_id,collision_key,version_key)
	WHERE ($3 OR NOT p.yanked) ORDER BY n.collision_key`, repositoryID, query, includeYanked, limit, afterKey)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = rows.Close() }()
	byName := make(map[string]CargoCrateSummary)
	for rows.Next() {
		var key, name, version, description string
		if err := rows.Scan(&key, &name, &version, &description); err != nil {
			return nil, 0, err
		}
		summary := byName[key]
		summary.Name = name
		byName[key] = addCargoCrateVersion(summary, version, description)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	items, _ := sortedCargoCrateSummaries(byName, limit, afterKey)
	return items, total, nil
}
