package repository

import "context"

const cargoSearchVisibilityPredicate = `EXISTS (SELECT 1 FROM native_cargo_publications visible
		WHERE visible.repository_id=n.repository_id AND visible.collision_key=n.collision_key
		AND visible.collected_at IS NULL AND ($3 OR NOT visible.yanked)
		AND NOT EXISTS (SELECT 1 FROM artifact_tombstones t WHERE t.repository_id=visible.repository_id AND t.format='cargo'
			AND t.coordinate=n.name || '@' || (SELECT r.version FROM native_cargo_identity_reservations r
				WHERE r.repository_id=visible.repository_id AND r.collision_key=visible.collision_key AND r.version_key=visible.version_key)))`

func (s *PostgresStore) SearchCargoCrates(ctx context.Context, repositoryID, query string, limit int, after string, includeYanked bool, matches ...ArtifactSearchMatch) ([]CargoCrateSummary, int, error) {
	if limit <= 0 || limit > 201 {
		limit = 10
	}
	afterKey, err := cargoSearchAfterKey(after)
	if err != nil {
		return nil, 0, err
	}
	predicate := `n.repository_id=$1::uuid AND ` + searchTextSQL("n.name", "$2", []ArtifactSearchMatch{searchMatch(matches, ArtifactSearchContains)}) + ` AND ` + cargoSearchVisibilityPredicate
	pattern := searchTextPattern(query, []ArtifactSearchMatch{searchMatch(matches, ArtifactSearchContains)})
	var total int
	err = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM native_cargo_names n WHERE `+predicate,
		repositoryID, pattern, includeYanked).Scan(&total)
	if err != nil {
		return nil, 0, err
	}
	rows, err := s.db.QueryContext(ctx, `WITH selected AS (
		SELECT n.repository_id,n.collision_key,n.name,n.normalized_name
		FROM native_cargo_names n WHERE `+predicate+` AND n.collision_key>$5
		ORDER BY n.collision_key LIMIT $4
	) SELECT n.collision_key,n.name,r.version,p.description
	FROM selected n JOIN native_cargo_publications p USING (repository_id,collision_key)
	JOIN native_cargo_identity_reservations r USING (repository_id,collision_key,version_key)
	WHERE p.collected_at IS NULL AND ($3 OR NOT p.yanked)
	  AND NOT EXISTS (SELECT 1 FROM artifact_tombstones t WHERE t.repository_id=p.repository_id AND t.format='cargo' AND t.coordinate=n.name || '@' || r.version)
	ORDER BY n.collision_key`, repositoryID, pattern, includeYanked, limit, afterKey)
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
