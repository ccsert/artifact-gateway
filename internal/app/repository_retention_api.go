package app

import (
	"encoding/csv"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	adminopenapi "github.com/artifact-gateway/artifact-gateway/internal/admin/openapi"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

// DryRunRepositoryRetention exposes the repository retention planner without
// tombstoning candidates or enqueuing a lifecycle job.
func (h generatedRepositoryAPIAdapter) DryRunRepositoryRetention(w http.ResponseWriter, r *http.Request, repositoryID adminopenapi.RepositoryId, params adminopenapi.DryRunRepositoryRetentionParams) {
	h.withRepositoryScope(w, r, repositoryID.String(), RepositoryAdmin, func(_ Principal, repo repository.HostedRepository) {
		if repo.Type != repository.RepositoryTypeHosted || !supportsRepositoryRetention(repo.Format) {
			writeHostedProblem(w, http.StatusConflict, "unsupported_operation", "retention dry-run is supported for Maven, OCI, Conan, and Raw hosted repositories")
			return
		}
		output := "json"
		if params.Output != nil {
			output = *params.Output
		}
		if output != "json" && output != "csv" {
			writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "output must be json or csv")
			return
		}
		pageSize := 100
		if params.PageSize != nil {
			pageSize = int(*params.PageSize)
			if pageSize < 1 || pageSize > 200 {
				writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "pageSize must be between 1 and 200")
				return
			}
		}
		policy, err := h.retentionPolicies.GetRepositoryRetentionPolicy(r.Context(), repo.ID)
		if err != nil {
			writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "get retention policy failed")
			return
		}
		candidates, err := (NativeRepositoryRetention{Store: h.sessions.store}).PlanRepositoryDetailed(r.Context(), repo.ID, repo.Format)
		if err != nil {
			writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "plan retention failed")
			return
		}
		if output == "csv" {
			if params.PageToken != nil && string(*params.PageToken) != "" {
				writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "pageToken cannot be used with CSV export")
				return
			}
			writeRetentionDryRunCSV(w, repo.Name, candidates)
			return
		}
		pageToken := ""
		if params.PageToken != nil {
			pageToken = string(*params.PageToken)
		}
		afterCoordinate, afterArtifactID, err := h.decodeRetentionDryRunCursor(pageToken, repo.ID, policy.Version)
		if err != nil {
			writeHostedProblem(w, http.StatusBadRequest, "invalid_page_token", "page token is invalid, expired, or belongs to another retention policy version")
			return
		}
		start := 0
		if afterCoordinate != "" {
			start = len(candidates)
			for index, candidate := range candidates {
				if candidate.Coordinate > afterCoordinate || (candidate.Coordinate == afterCoordinate && candidate.CursorID > afterArtifactID) {
					start = index
					break
				}
			}
		}
		end := start + pageSize
		if end > len(candidates) {
			end = len(candidates)
		}
		page := candidates[start:end]
		response := adminopenapi.RetentionDryRun{PolicyVersion: policy.Version, TotalCandidates: len(candidates), Summary: retentionDryRunSummary(candidates), Candidates: make([]struct {
			AgeDays     int                                               `json:"ageDays"`
			Coordinate  string                                            `json:"coordinate"`
			CreatedAt   time.Time                                         `json:"createdAt"`
			Digest      string                                            `json:"digest"`
			Format      adminopenapi.Format                               `json:"format"`
			Reasons     []adminopenapi.RetentionDryRunCandidatesReasons   `json:"reasons"`
			VersionType adminopenapi.RetentionDryRunCandidatesVersionType `json:"versionType"`
		}, 0, len(page))}
		for _, candidate := range page {
			response.Candidates = append(response.Candidates, struct {
				AgeDays     int                                               `json:"ageDays"`
				Coordinate  string                                            `json:"coordinate"`
				CreatedAt   time.Time                                         `json:"createdAt"`
				Digest      string                                            `json:"digest"`
				Format      adminopenapi.Format                               `json:"format"`
				Reasons     []adminopenapi.RetentionDryRunCandidatesReasons   `json:"reasons"`
				VersionType adminopenapi.RetentionDryRunCandidatesVersionType `json:"versionType"`
			}{Format: adminopenapi.Format(candidate.Format), AgeDays: candidate.AgeDays, Coordinate: candidate.Coordinate, CreatedAt: candidate.CreatedAt, Digest: candidate.Digest, Reasons: mapRetentionReasons(candidate.Reasons), VersionType: adminopenapi.RetentionDryRunCandidatesVersionType(candidate.VersionType)})
		}
		if end < len(candidates) {
			last := page[len(page)-1]
			nextPageToken := h.encodeRetentionDryRunCursor(repo.ID, policy.Version, last.Coordinate, last.CursorID)
			response.NextPageToken = &nextPageToken
		}
		writeNativeMavenJSON(w, http.StatusOK, response)
	})
}

func retentionDryRunSummary(candidates []RepositoryRetentionCandidate) adminopenapi.RetentionDryRunSummary {
	summary := adminopenapi.RetentionDryRunSummary{}
	for _, candidate := range candidates {
		for _, reason := range candidate.Reasons {
			switch reason {
			case "age":
				summary.ReasonCounts.Age++
			case "maximum_versions":
				summary.ReasonCounts.MaximumVersions++
			}
		}
		switch candidate.VersionType {
		case "release":
			summary.VersionTypeCounts.Release++
		case "snapshot":
			summary.VersionTypeCounts.Snapshot++
		case "version":
			summary.VersionTypeCounts.Version++
		case "asset":
			summary.VersionTypeCounts.Asset++
		}
		if summary.OldestCandidateAt == nil || candidate.CreatedAt.Before(*summary.OldestCandidateAt) {
			createdAt := candidate.CreatedAt
			summary.OldestCandidateAt = &createdAt
		}
	}
	return summary
}

func writeRetentionDryRunCSV(w http.ResponseWriter, repositoryName string, candidates []RepositoryRetentionCandidate) {
	var output strings.Builder
	writer := csv.NewWriter(&output)
	_ = writer.Write([]string{"format", "coordinate", "digest", "createdAt", "ageDays", "versionType", "reasons"})
	for _, candidate := range candidates {
		_ = writer.Write([]string{string(candidate.Format), csvSpreadsheetSafe(candidate.Coordinate), candidate.Digest, candidate.CreatedAt.UTC().Format(time.RFC3339Nano), strconv.Itoa(candidate.AgeDays), candidate.VersionType, strings.Join(candidate.Reasons, "|")})
	}
	writer.Flush()
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+repositoryName+`-retention.csv"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(output.String()))
}

func csvSpreadsheetSafe(value string) string {
	if value != "" && strings.ContainsRune("=+-@", rune(value[0])) {
		return "'" + value
	}
	return value
}

func mapRetentionReasons(reasons []string) []adminopenapi.RetentionDryRunCandidatesReasons {
	mapped := make([]adminopenapi.RetentionDryRunCandidatesReasons, 0, len(reasons))
	for _, reason := range reasons {
		mapped = append(mapped, adminopenapi.RetentionDryRunCandidatesReasons(reason))
	}
	return mapped
}

func (h generatedRepositoryAPIAdapter) ExecuteRepositoryRetention(w http.ResponseWriter, r *http.Request, repositoryID adminopenapi.RepositoryId, params adminopenapi.ExecuteRepositoryRetentionParams) {
	h.withRepositoryScope(w, r, repositoryID.String(), RepositoryAdmin, func(_ Principal, repo repository.HostedRepository) {
		if repo.Type != repository.RepositoryTypeHosted || !supportsRepositoryRetention(repo.Format) {
			writeHostedProblem(w, http.StatusConflict, "unsupported_operation", "retention policies are supported for Maven, OCI, Conan, and Raw hosted repositories")
			return
		}
		policy, err := h.retentionPolicies.GetRepositoryRetentionPolicy(r.Context(), repo.ID)
		if err != nil {
			writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "get retention policy failed")
			return
		}
		if params.IfMatch != nil && string(*params.IfMatch) != policy.Version {
			writeHostedProblem(w, http.StatusPreconditionFailed, "version_conflict", "retention policy changed after dry-run; run the preview again")
			return
		}
		if !policy.Enabled {
			writeHostedProblem(w, http.StatusConflict, "retention_disabled", "retention policy is disabled")
			return
		}
		job, _, err := (NativeRepositoryRetention{Store: h.sessions.store}).EnqueueRepository(r.Context(), repo.ID, string(params.IdempotencyKey))
		if errors.Is(err, repository.ErrIdempotencyConflict) {
			writeHostedProblem(w, http.StatusConflict, "idempotency_conflict", "Idempotency-Key conflicts with an existing retention job")
			return
		}
		if err != nil {
			writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "enqueue retention job failed")
			return
		}
		writeNativeMavenJSON(w, http.StatusAccepted, lifecycleJobResponse(job))
	})
}
