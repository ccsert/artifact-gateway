package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	adminopenapi "github.com/artifact-gateway/artifact-gateway/internal/admin/openapi"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

func (h generatedRepositoryAPIAdapter) ListRepositoryLifecycleJobs(w http.ResponseWriter, r *http.Request, repositoryID adminopenapi.RepositoryId) {
	h.withRepositoryScope(w, r, repositoryID.String(), RepositoryAdmin, func(Principal, repository.HostedRepository) {
		jobs, err := h.lifecycleJobs.ListLifecycleJobs(r.Context(), repositoryID.String(), 100)
		if err != nil {
			writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "list lifecycle jobs failed")
			return
		}
		items := make([]adminopenapi.LifecycleJob, 0, len(jobs))
		for _, job := range jobs {
			items = append(items, lifecycleJobResponse(job))
		}
		writeNativeMavenJSON(w, http.StatusOK, adminopenapi.LifecycleJobList(items))
	})
}

func (h generatedRepositoryAPIAdapter) CreateRepositoryArtifactScan(w http.ResponseWriter, r *http.Request, repositoryID adminopenapi.RepositoryId, _ adminopenapi.CreateRepositoryArtifactScanParams) {
	h.withRepositoryScope(w, r, repositoryID.String(), RepositoryIntelligence, func(principal Principal, repo repository.HostedRepository) {
		artifactScanEnqueueHandler{
			jobs: h.lifecycleJobs, audit: h.audit,
			scanner: h.artifactScanner, formats: h.artifactScanFormats,
		}.serve(w, r, principal, repo)
	})
}

func (h generatedRepositoryAPIAdapter) ListLifecycleJobs(w http.ResponseWriter, r *http.Request, params adminopenapi.ListLifecycleJobsParams) {
	if _, ok := h.authorize(w, r); !ok {
		return
	}
	limit := 500
	if params.Limit != nil {
		limit = *params.Limit
	}
	if limit < 1 || limit > 500 {
		writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "limit must be between 1 and 500")
		return
	}
	store, ok := h.lifecycleJobs.(repository.RepositoryLifecycleJobStore)
	if !ok {
		writeHostedProblem(w, http.StatusNotImplemented, "not_supported", "lifecycle job aggregation is unavailable")
		return
	}
	records, err := store.ListAllLifecycleJobs(r.Context(), limit)
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "list lifecycle jobs failed")
		return
	}
	items := make(adminopenapi.RepositoryLifecycleJobList, 0, len(records))
	for _, record := range records {
		items = append(items, adminopenapi.RepositoryLifecycleJob{
			RepositoryId:   uuid.MustParse(record.Job.RepositoryID),
			RepositoryName: record.RepositoryName,
			Job:            lifecycleJobResponse(record.Job),
		})
	}
	writeNativeMavenJSON(w, http.StatusOK, items)
}

func lifecycleJobResponse(job repository.LifecycleJob) adminopenapi.LifecycleJob {
	item := adminopenapi.LifecycleJob{
		Id:              job.ID,
		Kind:            adminopenapi.LifecycleJobKind(job.Kind),
		State:           adminopenapi.LifecycleJobState(job.State),
		CreatedAt:       job.CreatedAt,
		Attempts:        job.Attempts,
		MaxAttempts:     job.MaxAttempts,
		ProgressCurrent: job.ProgressCurrent,
		ProgressTotal:   job.ProgressTotal,
	}
	if !job.StartedAt.IsZero() {
		item.StartedAt = &job.StartedAt
	}
	if !job.CompletedAt.IsZero() {
		item.CompletedAt = &job.CompletedAt
	}
	if !job.NextAttemptAt.IsZero() {
		item.NextAttemptAt = &job.NextAttemptAt
	}
	if !job.LeaseExpiresAt.IsZero() {
		item.LeaseExpiresAt = &job.LeaseExpiresAt
	}
	if job.ProgressMessage != "" {
		item.ProgressMessage = &job.ProgressMessage
	}
	if job.LastError != "" {
		item.LastError = &job.LastError
	}
	if job.Kind == repository.LifecycleJobIntelligence {
		var payload repository.ArtifactIntelligenceCopyPayload
		if err := json.Unmarshal(job.Payload, &payload); err == nil && payload.Format != "" && payload.SourceRepositoryID != "" && payload.Coordinate != "" && payload.Digest != "" {
			if sourceRepositoryID, err := uuid.Parse(payload.SourceRepositoryID); err == nil {
				item.Details = &adminopenapi.LifecycleJobDetails{
					Format:             adminopenapi.Format(payload.Format),
					SourceRepositoryId: &sourceRepositoryID,
					Coordinate:         payload.Coordinate,
					Digest:             payload.Digest,
				}
			}
		}
	}
	if job.Kind == repository.LifecycleJobScan {
		var payload repository.ArtifactScanPayload
		if err := json.Unmarshal(job.Payload, &payload); err == nil && payload.Format != "" && payload.Coordinate != "" && payload.Digest != "" {
			item.Details = &adminopenapi.LifecycleJobDetails{Format: adminopenapi.Format(payload.Format), Coordinate: payload.Coordinate, Digest: payload.Digest}
		}
	}
	return item
}

func (h generatedRepositoryAPIAdapter) RunRepositoryLifecycleJobNow(w http.ResponseWriter, r *http.Request, repositoryID adminopenapi.RepositoryId, lifecycleJobID adminopenapi.LifecycleJobId) {
	h.controlRepositoryLifecycleJob(w, r, repositoryID.String(), lifecycleJobID.String(), "lifecycle.run_now", h.lifecycleJobs.RunLifecycleJobNow)
}

func (h generatedRepositoryAPIAdapter) RetryRepositoryLifecycleJob(w http.ResponseWriter, r *http.Request, repositoryID adminopenapi.RepositoryId, lifecycleJobID adminopenapi.LifecycleJobId) {
	h.controlRepositoryLifecycleJob(w, r, repositoryID.String(), lifecycleJobID.String(), "lifecycle.retry", h.lifecycleJobs.RetryLifecycleJob)
}

func (h generatedRepositoryAPIAdapter) CancelRepositoryLifecycleJob(w http.ResponseWriter, r *http.Request, repositoryID adminopenapi.RepositoryId, lifecycleJobID adminopenapi.LifecycleJobId) {
	h.controlRepositoryLifecycleJob(w, r, repositoryID.String(), lifecycleJobID.String(), "lifecycle.cancel", h.lifecycleJobs.CancelLifecycleJob)
}

func (h generatedRepositoryAPIAdapter) controlRepositoryLifecycleJob(w http.ResponseWriter, r *http.Request, repositoryID, lifecycleJobID, operation string, control func(context.Context, string, string) (repository.LifecycleJob, error)) {
	h.withRepositoryScope(w, r, repositoryID, RepositoryAdmin, func(principal Principal, repo repository.HostedRepository) {
		job, err := control(r.Context(), repositoryID, lifecycleJobID)
		if errors.Is(err, repository.ErrNotFound) {
			writeHostedProblem(w, http.StatusNotFound, "not_found", "lifecycle job not found")
			return
		}
		if errors.Is(err, repository.ErrVersionConflict) {
			writeHostedProblem(w, http.StatusConflict, "invalid_job_state", "lifecycle job cannot perform this action in its current state")
			return
		}
		if err != nil {
			writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "control lifecycle job failed")
			return
		}
		if h.audit != nil {
			_ = h.audit.RecordAudit(r.Context(), repository.AuditRecord{Repository: repo.Name, GroupName: repo.Name, Actor: principal.Actor, Outcome: repository.AuditResolved, OccurredAt: time.Now().UTC(), Format: "management", Resource: "lifecycle-jobs/" + job.ID, Operation: operation, Status: http.StatusOK, CacheDisposition: "bypass"})
		}
		writeNativeMavenJSON(w, http.StatusOK, lifecycleJobResponse(job))
	})
}

func (h generatedRepositoryAPIAdapter) ListRepositoryTombstones(w http.ResponseWriter, r *http.Request, repositoryID adminopenapi.RepositoryId, params adminopenapi.ListRepositoryTombstonesParams) {
	h.withRepositoryScope(w, r, repositoryID.String(), RepositoryAdmin, func(_ Principal, repo repository.HostedRepository) {
		prefix := ""
		if params.Q != nil {
			prefix = *params.Q
		}
		if len(prefix) > 255 || strings.ContainsAny(prefix, "\\\x00") {
			writeHostedProblem(w, 400, "invalid_request", "q must be a valid tombstone coordinate prefix")
			return
		}
		limit := 50
		if params.PageSize != nil {
			limit = int(*params.PageSize)
		}
		if limit < 1 || limit > 200 {
			writeHostedProblem(w, 400, "invalid_request", "pageSize must be between 1 and 200")
			return
		}
		token := ""
		if params.PageToken != nil {
			token = string(*params.PageToken)
		}
		after, err := h.decodeTombstoneCursor(token, repo.ID, repo.Format, prefix)
		if err != nil {
			writeHostedProblem(w, 400, "invalid_page_token", "page token is invalid or expired")
			return
		}
		items, err := h.tombstones.ListArtifactTombstones(r.Context(), repo.ID, repo.Format, prefix, limit+1, after)
		if err != nil {
			writeHostedProblem(w, 500, "internal_error", "list tombstones failed")
			return
		}
		var next *string
		if len(items) > limit {
			items = items[:limit]
			value := h.encodeTombstoneCursor(repo.ID, repo.Format, prefix, items[len(items)-1].Coordinate)
			next = &value
		}
		out := make([]adminopenapi.ArtifactTombstone, 0, len(items))
		for _, item := range items {
			out = append(out, adminopenapi.ArtifactTombstone{Coordinate: item.Coordinate, Digest: item.Digest, TombstonedAt: item.TombstonedAt})
		}
		writeNativeMavenJSON(w, 200, adminopenapi.ArtifactTombstonePage{Items: out, NextPageToken: next})
	})
}

func (h generatedRepositoryAPIAdapter) TombstoneRepositoryArtifact(w http.ResponseWriter, r *http.Request, repositoryID adminopenapi.RepositoryId) {
	h.withRepositoryScope(w, r, repositoryID.String(), RepositoryAdmin, func(principal Principal, repo repository.HostedRepository) {
		if repo.Type != repository.RepositoryTypeHosted || !repository.FormatSupportsOperation(repo.Format, repo.Type, repository.RepositoryOperationDelete) {
			writeHostedProblem(w, http.StatusConflict, "unsupported_operation", "artifact deletion is not supported for this repository")
			return
		}
		var request adminopenapi.RestoreArtifact
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&request); err != nil || !validTombstoneCoordinate(repo.Format, request.Coordinate) {
			writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "coordinate must identify a visible artifact")
			return
		}
		var err error
		switch repo.Format {
		case repository.FormatMaven:
			artifact, lookupErr := h.sessions.store.GetMavenArtifactByCoordinate(r.Context(), repo.ID, request.Coordinate)
			if lookupErr != nil {
				err = lookupErr
			} else {
				_, err = h.sessions.store.TombstoneMavenArtifact(r.Context(), repo.ID, artifact.ID)
			}
		case repository.FormatOCI:
			name, digest, _ := parseOCIRestoreCoordinate(request.Coordinate)
			err = h.oci.DeleteOCIManifest(r.Context(), repo.ID, name, digest)
		case repository.FormatConan:
			reference, revision, packageID, packageRevision, packageDelete, _ := parseConanRestoreCoordinate(request.Coordinate)
			if packageDelete {
				_, err = h.conan.TombstoneConanPackageRevision(r.Context(), repo.ID, reference, revision, packageID, packageRevision)
			} else {
				_, err = h.conan.TombstoneConanRecipeRevision(r.Context(), repo.ID, reference, revision)
			}
		case repository.FormatRaw:
			err = h.sessions.store.DeleteRawAsset(r.Context(), repo.ID, request.Coordinate)
		case repository.FormatNPM:
			name, version, _ := parseNPMVersionCoordinate(request.Coordinate)
			_, err = h.sessions.store.TombstoneNPMVersion(r.Context(), repo.ID, name, version)
		case repository.FormatPyPI:
			project, version, _ := parsePyPIVersionCoordinate(request.Coordinate)
			_, err = h.sessions.store.TombstonePyPIVersion(r.Context(), repo.ID, project, version)
		}
		if errors.Is(err, repository.ErrNotFound) {
			writeHostedProblem(w, http.StatusNotFound, "not_found", "artifact not found")
			return
		}
		if err != nil {
			writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "tombstone artifact failed")
			return
		}
		if h.audit != nil {
			_ = h.audit.RecordAudit(r.Context(), repository.AuditRecord{GroupName: repo.Name, Repository: repo.Name, Actor: principal.Actor, Outcome: repository.AuditResolved, OccurredAt: time.Now().UTC(), Format: "management", Resource: request.Coordinate, Operation: "artifact.tombstone", Status: http.StatusNoContent, CacheDisposition: "bypass"})
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

func validTombstoneCoordinate(format repository.Format, coordinate string) bool {
	switch format {
	case repository.FormatMaven:
		return validMavenCoordinate(coordinate)
	case repository.FormatOCI:
		return validOCIRestoreCoordinate(coordinate)
	case repository.FormatConan:
		return validConanRestoreCoordinate(coordinate)
	case repository.FormatRaw:
		return coordinate != "" && validRawAssetPrefix(coordinate)
	case repository.FormatNPM:
		return validNPMVersionCoordinate(coordinate)
	case repository.FormatPyPI:
		return validPyPIVersionCoordinate(coordinate)
	default:
		return false
	}
}
