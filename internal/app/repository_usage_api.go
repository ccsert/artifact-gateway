package app

import (
	"net/http"
	"time"

	adminopenapi "github.com/artifact-gateway/artifact-gateway/internal/admin/openapi"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

// ListRepositoryArtifactUsage serves the lifecycle download usage aggregates
// that RecordAudit folds in on every successful download. Usage is keyed by
// the artifact address clients resolved, so a V2 group download counts for
// the group while direct member downloads count for the member repository.
func (h generatedRepositoryAPIAdapter) ListRepositoryArtifactUsage(w http.ResponseWriter, r *http.Request, repositoryID adminopenapi.RepositoryId, params adminopenapi.ListRepositoryArtifactUsageParams) {
	h.withRepositoryBrowseScope(w, r, repositoryID.String(), func(_ Principal, repo repository.HostedRepository) {
		usage, ok := h.audit.(repository.ArtifactUsageStore)
		if !ok {
			writeHostedProblem(w, http.StatusNotImplemented, "not_supported", "artifact usage aggregation is unavailable")
			return
		}
		limit := 100
		if params.Limit != nil {
			limit = *params.Limit
		}
		if limit < 1 || limit > 500 {
			writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "limit must be between 1 and 500")
			return
		}
		stats, err := usage.ListArtifactUsage(r.Context(), repo.Name, limit)
		if err != nil {
			writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "list artifact usage failed")
			return
		}
		totals, err := usage.ArtifactUsageTotals(r.Context(), repo.Name)
		if err != nil {
			writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "sum artifact usage failed")
			return
		}
		items := make([]adminopenapi.ArtifactUsageStat, 0, len(stats))
		for _, stat := range stats {
			item := adminopenapi.ArtifactUsageStat{
				Format:            stat.Format,
				Resource:          stat.Resource,
				DownloadCount:     stat.DownloadCount,
				TotalBytes:        stat.TotalBytes,
				FirstDownloadedAt: stat.FirstDownloadedAt,
				LastDownloadedAt:  stat.LastDownloadedAt,
			}
			if stat.LastActor != "" {
				actor := stat.LastActor
				item.LastActor = &actor
			}
			items = append(items, item)
		}
		writeNativeMavenJSON(w, http.StatusOK, adminopenapi.RepositoryArtifactUsage{
			RepositoryId: repositoryID,
			Totals: adminopenapi.ArtifactUsageTotals{
				DownloadCount: totals.DownloadCount,
				TotalBytes:    totals.TotalBytes,
				Resources:     totals.Resources,
			},
			Items:       items,
			GeneratedAt: time.Now().UTC(),
		})
	})
}
