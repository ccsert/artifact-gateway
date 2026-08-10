package app

import (
	"net/http"
	"time"

	adminopenapi "github.com/artifact-gateway/artifact-gateway/internal/admin/openapi"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

func (h generatedRepositoryAPIAdapter) ReconcileRepositoryArtifactIntelligence(w http.ResponseWriter, r *http.Request, repositoryID adminopenapi.RepositoryId, params adminopenapi.ReconcileRepositoryArtifactIntelligenceParams) {
	h.withRepositoryScope(w, r, repositoryID.String(), RepositoryAdmin, func(principal Principal, repo repository.HostedRepository) {
		limit := 100
		if params.Limit != nil {
			limit = *params.Limit
		}
		jobs, err := h.lifecycleJobs.RequeueFailedLifecycleJobs(r.Context(), repo.ID, repository.LifecycleJobIntelligence, limit)
		if err != nil {
			writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "reconcile artifact intelligence jobs failed")
			return
		}
		jobIDs := make([]string, 0, len(jobs))
		for _, job := range jobs {
			jobIDs = append(jobIDs, job.ID)
		}
		if h.audit != nil {
			_ = h.audit.RecordAudit(r.Context(), repository.AuditRecord{
				Repository:       repo.Name,
				GroupName:        repo.Name,
				Actor:            principal.Actor,
				Outcome:          repository.AuditResolved,
				OccurredAt:       time.Now().UTC(),
				Format:           "management",
				Resource:         "repositories/" + repo.ID + "/lifecycle-jobs:reconcile-intelligence",
				Operation:        "lifecycle.intelligence_reconcile",
				Status:           http.StatusOK,
				CacheDisposition: "bypass",
			})
		}
		writeNativeMavenJSON(w, http.StatusOK, adminopenapi.LifecycleJobReconciliation{
			RepositoryId:   repositoryID,
			Requeued:       len(jobs),
			RequeuedJobIds: jobIDs,
		})
	})
}
