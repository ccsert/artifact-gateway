package repository

import (
	"context"
	"strings"
)

func (s *MemoryStore) SearchCargoCrates(ctx context.Context, repositoryID, query string, limit int, after string, includeYanked bool) ([]CargoCrateSummary, int, error) {
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	if limit <= 0 || limit > 201 {
		limit = 10
	}
	afterKey, err := cargoSearchAfterKey(after)
	if err != nil {
		return nil, 0, err
	}
	needle := strings.ToLower(strings.TrimSpace(query))
	s.mu.RLock()
	defer s.mu.RUnlock()
	byName := make(map[string]CargoCrateSummary)
	for _, publication := range s.cargoPublications {
		if publication.RepositoryID != repositoryID || (publication.Yanked && !includeYanked) || !strings.Contains(strings.ToLower(publication.Name), needle) {
			continue
		}
		identity, err := normalizeCargoIdentityClaim(publication.CargoIdentityClaim)
		if err != nil {
			return nil, 0, err
		}
		summary := byName[identity.CollisionKey]
		summary.Name = publication.Name
		byName[identity.CollisionKey] = addCargoCrateVersion(summary, publication.Version, publication.Description)
	}
	items, total := sortedCargoCrateSummaries(byName, limit, afterKey)
	return items, total, nil
}
