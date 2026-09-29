package repository

import (
	"context"
	"encoding/json"
	"strings"
)

// cargoProxyCrateSummary projects only validated cached index rows. The
// protocol may discover more from the upstream; management browse is bounded
// to metadata the Gateway has already verified and retained.
func cargoProxyCrateSummary(index CargoProxyIndex) (CargoCrateSummary, error) {
	rows, err := cargoProxyIndexRows(index.Name, index.Body)
	if err != nil {
		return CargoCrateSummary{}, err
	}
	summary := CargoCrateSummary{Name: index.Name}
	for _, row := range rows {
		var fields struct {
			Version string `json:"vers"`
		}
		if err := json.Unmarshal(row.Immutable, &fields); err != nil || fields.Version == "" {
			return CargoCrateSummary{}, ErrInvalidCargoIdentity
		}
		summary = addCargoCrateVersion(summary, fields.Version, "")
	}
	return summary, nil
}

func (s *MemoryStore) SearchCargoProxyCrates(ctx context.Context, repositoryID, query string, limit int, after string) ([]CargoCrateSummary, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 201 {
		limit = 10
	}
	afterKey, err := cargoSearchAfterKey(after)
	if err != nil {
		return nil, err
	}
	needle := strings.ToLower(strings.TrimSpace(query))
	s.mu.RLock()
	defer s.mu.RUnlock()
	byName := make(map[string]CargoCrateSummary)
	for key, index := range s.cargoProxyIndexes {
		if !strings.HasPrefix(key, repositoryID+"\x00") || index.Status != 200 || !strings.Contains(strings.ToLower(index.Name), needle) {
			continue
		}
		summary, err := cargoProxyCrateSummary(index)
		if err != nil {
			return nil, err
		}
		byName[strings.TrimPrefix(key, repositoryID+"\x00")] = summary
	}
	items, _ := sortedCargoCrateSummaries(byName, limit, afterKey)
	return items, nil
}

func (s *PostgresStore) SearchCargoProxyCrates(ctx context.Context, repositoryID, query string, limit int, after string) ([]CargoCrateSummary, error) {
	if limit <= 0 || limit > 201 {
		limit = 10
	}
	afterKey, err := cargoSearchAfterKey(after)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT name,body FROM native_cargo_proxy_indexes
		WHERE repository_id::text=$1 AND status=200 AND position(lower($2) in lower(name))>0
		AND collision_key>$3 ORDER BY collision_key LIMIT $4`, repositoryID, strings.TrimSpace(query), afterKey, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	items := make([]CargoCrateSummary, 0, limit)
	for rows.Next() {
		var index CargoProxyIndex
		if err := rows.Scan(&index.Name, &index.Body); err != nil {
			return nil, err
		}
		summary, err := cargoProxyCrateSummary(index)
		if err != nil {
			return nil, err
		}
		items = append(items, summary)
	}
	return items, rows.Err()
}
