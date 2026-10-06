package app

import (
	"net/http"
	"strings"
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
		query := repository.ArtifactUsageQuery{Limit: limit}
		if params.Offset != nil {
			query.Offset = *params.Offset
		}
		if params.Q != nil {
			query.Query = strings.TrimSpace(*params.Q)
		}
		if err := query.Validate(repo.Name); err != nil {
			writeHostedProblem(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		page, err := usage.QueryArtifactUsage(r.Context(), repo.Name, query)
		if err != nil {
			writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "list artifact usage failed")
			return
		}
		items := make([]adminopenapi.ArtifactUsageStat, 0, len(page.Items))
		for _, stat := range page.Items {
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
				DownloadCount: page.Totals.DownloadCount,
				TotalBytes:    page.Totals.TotalBytes,
				Resources:     page.Totals.Resources,
			},
			TotalCount:  page.TotalCount,
			Items:       items,
			GeneratedAt: time.Now().UTC(),
		})
	})
}
