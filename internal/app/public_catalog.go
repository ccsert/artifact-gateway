package app

import (
	"encoding/json"
	"net/http"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

// publicRepositoryCatalogHandler intentionally exposes only repository metadata
// that is needed to discover anonymous browse targets. Configuration and
// management metadata remain authenticated-only.
type publicRepositoryCatalogHandler struct {
	repositories repository.HostedRepositoryStore
	anonymous    repository.AnonymousAccessPolicyStore
}

type publicRepositoryCatalogEntry struct {
	ID     string            `json:"id"`
	Name   string            `json:"name"`
	Format repository.Format `json:"format"`
	Type   string            `json:"type"`
}

type publicRepositoryCatalogResponse struct {
	Enabled bool                            `json:"enabled"`
	Items   []publicRepositoryCatalogEntry `json:"items"`
}

func (h publicRepositoryCatalogHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	policy, err := h.anonymous.GetAnonymousAccessPolicy(r.Context())
	if err != nil || !policy.Enabled {
		writeNativeMavenJSON(w, http.StatusOK, publicRepositoryCatalogResponse{Enabled: false, Items: []publicRepositoryCatalogEntry{}})
		return
	}

	items := make([]publicRepositoryCatalogEntry, 0)
	after := ""
	for {
		repositories, next, err := h.repositories.ListHostedRepositories(r.Context(), 200, after)
		if err != nil {
			writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "list public repositories failed")
			return
		}
		for _, repo := range repositories {
			if repo.State == repository.RepositoryActive && repo.AnonymousRead {
				items = append(items, publicRepositoryCatalogEntry{ID: repo.ID, Name: repo.Name, Format: repo.Format, Type: string(repo.Type)})
			}
		}
		if next == "" {
			break
		}
		after = next
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(publicRepositoryCatalogResponse{Enabled: true, Items: items})
}
