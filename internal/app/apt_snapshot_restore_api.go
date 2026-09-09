package app

import (
	"errors"
	"mime"
	"net/http"

	adminopenapi "github.com/artifact-gateway/artifact-gateway/internal/admin/openapi"
	"github.com/artifact-gateway/artifact-gateway/internal/aptpublication"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

func (h generatedRepositoryAPIAdapter) RestoreAPTRepositorySnapshot(w http.ResponseWriter, r *http.Request, repositoryID adminopenapi.RepositoryId, params adminopenapi.RestoreAPTRepositorySnapshotParams) {
	h.withRepositoryScope(w, r, repositoryID.String(), RepositoryAdmin, func(principal Principal, repo repository.HostedRepository) {
		if repo.Format != repository.FormatAPT || repo.Type != repository.RepositoryTypeHosted {
			writeHostedProblem(w, 404, "not_found", "APT Hosted repository not found")
			return
		}
		if h.aptSnapshotImporter == nil {
			writeHostedProblem(w, 409, "invalid_state", "APT archive restore public-key trust is not configured")
			return
		}
		mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || mediaType != aptSnapshotArchiveContentType {
			writeHostedProblem(w, 415, "unsupported_media_type", "Content-Type must be "+aptSnapshotArchiveContentType)
			return
		}
		if !repository.ValidAPTSHA256Digest(params.XArtifactArchiveDigest) || len(r.Header.Values("X-Artifact-Archive-Digest")) != 1 {
			writeHostedProblem(w, 400, "invalid_request", "one independently saved SHA-256 archive receipt is required")
			return
		}
		if r.ContentLength > aptpublication.MaxSnapshotArchiveImportBytes {
			writeHostedProblem(w, 413, "invalid_request", "APT archive exceeds the 64 GiB restore limit")
			return
		}
		snapshot, err := h.aptSnapshotImporter.Import(r.Context(), repo.ID, params.XArtifactArchiveDigest, http.MaxBytesReader(w, r.Body, aptpublication.MaxSnapshotArchiveImportBytes), principal.Actor)
		if err != nil {
			writeAPTSnapshotRestoreProblem(w, err)
			return
		}
		response, err := aptRepositorySnapshotResponse(snapshot)
		if err != nil {
			writeHostedProblem(w, 500, "internal_error", "APT repository snapshot identity is invalid")
			return
		}
		w.Header().Set("Cache-Control", "private, no-store")
		writeNativeMavenJSON(w, http.StatusOK, response)
	})
}

func writeAPTSnapshotRestoreProblem(w http.ResponseWriter, err error) {
	var tooLarge *http.MaxBytesError
	switch {
	case errors.As(err, &tooLarge), errors.Is(err, aptpublication.ErrSnapshotArchiveTooLarge):
		writeHostedProblem(w, 413, "invalid_request", "APT archive exceeds the restore limit")
	case errors.Is(err, aptpublication.ErrArchiveReceiptMismatch):
		writeHostedProblem(w, 412, "digest_mismatch", "APT archive does not match the independently saved backup receipt")
	case errors.Is(err, aptpublication.ErrSnapshotArchiveUntrusted):
		writeHostedProblem(w, 422, "untrusted_archive", "APT archive signatures do not match the configured public-key trust policy")
	case errors.Is(err, aptpublication.ErrInvalidSnapshotArchiveInput):
		writeHostedProblem(w, 400, "invalid_request", "APT archive restore request is invalid")
	case errors.Is(err, aptpublication.ErrSnapshotArchiveCorrupt):
		writeHostedProblem(w, 400, "snapshot_corrupt", "APT archive failed package or signed index integrity verification")
	case errors.Is(err, aptpublication.ErrArchiveRestoreBusy):
		w.Header().Set("Retry-After", "5")
		writeHostedProblem(w, 429, "invalid_state", "APT archive restore concurrency limit reached")
	case errors.Is(err, repository.ErrNotFound):
		writeHostedProblem(w, 404, "not_found", "APT archive does not belong to this repository")
	case errors.Is(err, repository.ErrArtifactQuarantined):
		writeHostedProblem(w, http.StatusConflict, "artifact_quarantined", "APT snapshot contains a quarantined package")
	case errors.Is(err, repository.ErrQuotaExceeded):
		writeHostedProblem(w, 507, "quota_exceeded", "repository capacity quota would be exceeded")
	case errors.Is(err, repository.ErrIdempotencyConflict):
		writeHostedProblem(w, 409, "idempotency_conflict", "existing APT snapshot or package metadata differs from the archive")
	case errors.Is(err, repository.ErrNameExists), errors.Is(err, repository.ErrAPTPackageConflict):
		writeHostedProblem(w, 409, "coordinate_exists", "APT snapshot sequence or package coordinate conflicts with existing content")
	case errors.Is(err, repository.ErrDisabled), errors.Is(err, repository.ErrVersionConflict):
		writeHostedProblem(w, 409, "invalid_state", "APT archive cannot be restored from the current repository state")
	default:
		writeHostedProblem(w, 500, "internal_error", "APT archive restore failed")
	}
}
