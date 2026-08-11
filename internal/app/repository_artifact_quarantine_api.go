package app

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	adminopenapi "github.com/artifact-gateway/artifact-gateway/internal/admin/openapi"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

func (h generatedRepositoryAPIAdapter) GetArtifactQuarantine(w http.ResponseWriter, r *http.Request, repositoryID adminopenapi.RepositoryId, params adminopenapi.GetArtifactQuarantineParams) {
	h.withRepositoryScope(w, r, repositoryID.String(), RepositoryRead, func(_ Principal, repo repository.HostedRepository) {
		coordinate := strings.TrimSpace(params.Coordinate)
		digest := strings.TrimSpace(params.Digest)
		if !validArtifactQuarantineIdentity(repo.Format, coordinate, digest) {
			writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "coordinate and digest must identify a quarantine distribution anchor")
			return
		}
		value, err := h.quarantine.GetArtifactQuarantine(r.Context(), repo.ID, repo.Format, coordinate, digest)
		if errors.Is(err, repository.ErrNotFound) {
			writeHostedProblem(w, http.StatusNotFound, "not_found", "artifact quarantine not found")
			return
		}
		if err != nil {
			writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "get artifact quarantine failed")
			return
		}
		writeArtifactQuarantine(w, value)
	})
}

func (h generatedRepositoryAPIAdapter) ReplaceArtifactQuarantine(w http.ResponseWriter, r *http.Request, repositoryID adminopenapi.RepositoryId, params adminopenapi.ReplaceArtifactQuarantineParams) {
	h.withRepositoryScope(w, r, repositoryID.String(), RepositoryAdmin, func(principal Principal, repo repository.HostedRepository) {
		coordinate := strings.TrimSpace(params.Coordinate)
		digest := strings.TrimSpace(params.Digest)
		if !validArtifactQuarantineIdentity(repo.Format, coordinate, digest) {
			writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "coordinate and digest must identify a quarantine distribution anchor")
			return
		}
		var input adminopenapi.ArtifactQuarantineUpdate
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil || decoder.Decode(&struct{}{}) != io.EOF {
			writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "artifact quarantine payload is invalid")
			return
		}
		reason := strings.TrimSpace(input.Reason)
		state := repository.ArtifactQuarantineState(input.State)
		if (state != repository.ArtifactQuarantineStateQuarantined && state != repository.ArtifactQuarantineStateReleased) || reason == "" || utf8.RuneCountInString(reason) > 1024 || strings.ContainsRune(reason, '\x00') {
			writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "state and a bounded audit reason are required")
			return
		}

		release, err := repository.LockArtifactQuarantineTransition(r.Context(), h.quarantine, repo.ID, repo.Format, coordinate, digest)
		if err != nil {
			writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "coordinate artifact quarantine failed")
			return
		}
		defer release()

		expected := string(params.IfMatch)
		current, currentErr := h.quarantine.GetArtifactQuarantine(r.Context(), repo.ID, repo.Format, coordinate, digest)
		switch {
		case currentErr == nil:
			if current.Version != expected {
				writeHostedProblem(w, http.StatusPreconditionFailed, "version_conflict", "If-Match does not match current quarantine version")
				return
			}
			if current.State == state {
				writeHostedProblem(w, http.StatusConflict, "invalid_state", "artifact quarantine is already in the requested state")
				return
			}
		case errors.Is(currentErr, repository.ErrNotFound):
			if expected != "0" {
				writeHostedProblem(w, http.StatusPreconditionFailed, "version_conflict", "If-Match does not match current quarantine version")
				return
			}
			if state != repository.ArtifactQuarantineStateQuarantined {
				writeHostedProblem(w, http.StatusConflict, "invalid_state", "an artifact must be quarantined before it can be released")
				return
			}
			if h.searchProjection == nil {
				writeHostedProblem(w, http.StatusNotImplemented, "not_supported", "artifact search projection is unavailable")
				return
			}
			visible, err := securityPolicyArtifactVisible(r.Context(), h.searchProjection, repo.ID, repo.Format, coordinate, digest)
			if err != nil {
				writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "verify artifact quarantine identity failed")
				return
			}
			if !visible {
				writeHostedProblem(w, http.StatusNotFound, "not_found", "artifact to quarantine not found")
				return
			}
		default:
			writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "get artifact quarantine failed")
			return
		}

		updated, err := h.quarantine.ReplaceArtifactQuarantine(r.Context(), repository.ArtifactQuarantine{
			RepositoryID: repo.ID,
			Format:       repo.Format,
			Coordinate:   coordinate,
			Digest:       digest,
			State:        state,
			Reason:       reason,
			UpdatedBy:    principal.Actor,
		}, expected)
		if errors.Is(err, repository.ErrVersionConflict) {
			writeHostedProblem(w, http.StatusPreconditionFailed, "version_conflict", "If-Match does not match current quarantine version")
			return
		}
		if errors.Is(err, repository.ErrNotFound) {
			writeHostedProblem(w, http.StatusNotFound, "not_found", "repository or artifact quarantine not found")
			return
		}
		if errors.Is(err, repository.ErrInvalidArtifactQuarantine) {
			writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "artifact quarantine payload is invalid")
			return
		}
		if err != nil {
			writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "replace artifact quarantine failed")
			return
		}
		if h.audit != nil {
			operation := "artifact.quarantine"
			if state == repository.ArtifactQuarantineStateReleased {
				operation = "artifact.release"
			}
			_ = h.audit.RecordAudit(r.Context(), repository.AuditRecord{
				GroupName: repo.Name, Repository: repo.Name, Actor: principal.Actor,
				Outcome: repository.AuditResolved, OccurredAt: time.Now().UTC(),
				Format: string(repo.Format), Resource: coordinate, Representation: digest,
				Operation: operation, Status: http.StatusOK, AuthorizationReason: reason,
				CacheDisposition: "bypass",
			})
		}
		writeArtifactQuarantine(w, updated)
	})
}

func validArtifactQuarantineIdentity(format repository.Format, coordinate, digest string) bool {
	if !validArtifactIntelligenceIdentity(format, coordinate, digest) {
		return false
	}
	if format != repository.FormatConan {
		return true
	}

	// A Conan recipe revision and all of its visible package revisions are one
	// atomic promotion/replication unit. A package-only quarantine would be
	// bypassed when its parent recipe is distributed, so the recipe revision is
	// the only safe quarantine anchor for this slice.
	_, _, _, _, packageRevision, ok := parseConanRestoreCoordinate(coordinate)
	return ok && !packageRevision
}

func writeArtifactQuarantine(w http.ResponseWriter, value repository.ArtifactQuarantine) {
	response := adminopenapi.ArtifactQuarantine{
		RepositoryId: uuid.MustParse(value.RepositoryID),
		Format:       adminopenapi.Format(value.Format),
		Coordinate:   value.Coordinate,
		Digest:       value.Digest,
		State:        adminopenapi.ArtifactQuarantineState(value.State),
		Reason:       value.Reason,
		Version:      value.Version,
		UpdatedBy:    value.UpdatedBy,
		UpdatedAt:    value.UpdatedAt,
	}
	if !value.QuarantinedAt.IsZero() {
		response.QuarantinedAt = &value.QuarantinedAt
	}
	if !value.ReleasedAt.IsZero() {
		response.ReleasedAt = &value.ReleasedAt
	}
	w.Header().Set("ETag", value.Version)
	writeNativeMavenJSON(w, http.StatusOK, response)
}
