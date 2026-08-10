package app

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/artifact-gateway/artifact-gateway/internal/scanning"
)

type artifactScanEnqueueHandler struct {
	jobs    repository.LifecycleJobStore
	audit   repository.Store
	scanner scanning.Scanner
	formats []repository.Format
}

type artifactScanRequest struct {
	Coordinate string `json:"coordinate"`
	Digest     string `json:"digest"`
}

func (h artifactScanEnqueueHandler) serve(w http.ResponseWriter, r *http.Request, principal Principal, repo repository.HostedRepository) {
	if repo.State != repository.RepositoryActive {
		writeHostedProblem(w, http.StatusNotFound, "not_found", "repository not found")
		return
	}
	if h.scanner == nil || !scanFormatEnabled(h.formats, repo.Format) || !scanRepositoryAssetsAvailable(repo) {
		writeHostedProblem(w, http.StatusServiceUnavailable, "scanner_unavailable", "artifact scanner is not configured for this repository format and type")
		return
	}
	idempotencyKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if idempotencyKey == "" || len(idempotencyKey) > 128 {
		writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "Idempotency-Key is required and must be at most 128 characters")
		return
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	decoder.DisallowUnknownFields()
	var request artifactScanRequest
	if err := decoder.Decode(&request); err != nil || decoder.Decode(&struct{}{}) != io.EOF || !validArtifactIntelligenceIdentity(repo.Format, request.Coordinate, request.Digest) {
		writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "coordinate and sha256 digest must identify an immutable artifact")
		return
	}
	job, replayed, err := repository.EnqueueArtifactScanJob(r.Context(), h.jobs, repo.ID, idempotencyKey, repository.ArtifactScanPayload{Format: repo.Format, Coordinate: request.Coordinate, Digest: request.Digest})
	if errors.Is(err, repository.ErrIdempotencyConflict) {
		writeHostedProblem(w, http.StatusConflict, "idempotency_conflict", "Idempotency-Key was already used with a different scan request")
		return
	}
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "enqueue artifact scan failed")
		return
	}
	if h.audit != nil {
		_ = h.audit.RecordAudit(r.Context(), repository.AuditRecord{
			Repository: repo.Name, GroupName: repo.Name, Actor: principal.Actor,
			Outcome: repository.AuditResolved, OccurredAt: time.Now().UTC(), Format: "management",
			Resource: request.Coordinate + "@" + request.Digest, Operation: "artifact.scan.enqueue",
			Status: http.StatusAccepted, CacheDisposition: "bypass",
		})
	}
	if replayed {
		w.Header().Set("Idempotent-Replayed", "true")
	}
	writeNativeMavenJSON(w, http.StatusAccepted, lifecycleJobResponse(job))
}

func scanFormatEnabled(formats []repository.Format, format repository.Format) bool {
	for _, candidate := range formats {
		if candidate == format {
			return true
		}
	}
	return false
}

func scanRepositoryAssetsAvailable(repo repository.HostedRepository) bool {
	if repo.Type != repository.RepositoryTypeProxy {
		return true
	}
	switch repo.Format {
	case repository.FormatNPM, repository.FormatPyPI, repository.FormatGo:
		return true
	default:
		return false
	}
}
