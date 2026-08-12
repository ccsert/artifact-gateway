package app

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"

	adminopenapi "github.com/artifact-gateway/artifact-gateway/internal/admin/openapi"
	"github.com/artifact-gateway/artifact-gateway/internal/aptpublication"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

func (h generatedRepositoryAPIAdapter) CreateAPTPublicationSession(w http.ResponseWriter, r *http.Request, repositoryID adminopenapi.RepositoryId, params adminopenapi.CreateAPTPublicationSessionParams) {
	h.withRepositoryScope(w, r, repositoryID.String(), RepositoryWrite, func(principal Principal, repo repository.HostedRepository) {
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
		decoder.DisallowUnknownFields()
		var request adminopenapi.CreateAPTPublicationSession
		if err := decoder.Decode(&request); err != nil || decoder.Decode(&struct{}{}) != io.EOF {
			writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "APT publication session body is invalid")
			return
		}
		expectedIdentity := ""
		if request.ExpectedIdentity != nil {
			expectedIdentity = *request.ExpectedIdentity
		}
		session, _, err := h.aptPublication.CreateSession(r.Context(), aptpublication.CreateSessionInput{
			RepositoryID: repo.ID, Suite: request.Suite, Component: request.Component, Publisher: principal.Actor,
			ObjectName: request.ObjectName, DeclaredDigest: request.DeclaredDigest, DeclaredSize: request.DeclaredSize,
			ExpectedIdentity: expectedIdentity, IdempotencyKey: string(params.IdempotencyKey),
		})
		if err != nil {
			writeAPTPublicationProblem(w, err)
			return
		}
		response, err := aptPublicationSessionResponse(session)
		if err != nil {
			writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "APT publication session identity is invalid")
			return
		}
		writeNativeMavenJSON(w, http.StatusCreated, response)
	})
}

func (h generatedRepositoryAPIAdapter) GetAPTPublicationSession(w http.ResponseWriter, r *http.Request, repositoryID adminopenapi.RepositoryId, sessionID adminopenapi.SessionId) {
	h.withRepositoryScope(w, r, repositoryID.String(), RepositoryWrite, func(_ Principal, repo repository.HostedRepository) {
		session, err := h.aptPublications.GetAPTPublicationSession(r.Context(), sessionID.String())
		if errors.Is(err, repository.ErrNotFound) || (err == nil && session.RepositoryID != repo.ID) {
			writeHostedProblem(w, http.StatusNotFound, "not_found", "APT publication session not found")
			return
		}
		if err != nil {
			writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "get APT publication session failed")
			return
		}
		response, err := aptPublicationSessionResponse(session)
		if err != nil {
			writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "APT publication session identity is invalid")
			return
		}
		writeNativeMavenJSON(w, http.StatusOK, response)
	})
}

func (h generatedRepositoryAPIAdapter) UploadAPTPublicationPackage(w http.ResponseWriter, r *http.Request, repositoryID adminopenapi.RepositoryId, sessionID adminopenapi.SessionId) {
	h.withRepositoryScope(w, r, repositoryID.String(), RepositoryWrite, func(principal Principal, repo repository.HostedRepository) {
		mediaType, _, mediaTypeErr := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if mediaTypeErr != nil || mediaType != "application/vnd.debian.binary-package" {
			writeHostedProblem(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "Content-Type must be application/vnd.debian.binary-package")
			return
		}
		session, err := h.aptPublications.GetAPTPublicationSession(r.Context(), sessionID.String())
		if errors.Is(err, repository.ErrNotFound) || (err == nil && session.RepositoryID != repo.ID) {
			writeHostedProblem(w, http.StatusNotFound, "not_found", "APT publication session not found")
			return
		}
		if err != nil {
			writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "get APT publication session failed")
			return
		}
		revision, err := h.aptPublication.UploadPackageAs(r.Context(), session.ID, session.ObjectName, r.Body, r.ContentLength, principal.Actor)
		if err != nil {
			writeAPTPublicationProblem(w, err)
			return
		}
		response, err := aptPackageRevisionResponse(revision)
		if err != nil {
			writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "APT package revision identity is invalid")
			return
		}
		writeNativeMavenJSON(w, http.StatusOK, response)
	})
}

func aptPublicationSessionResponse(session repository.APTPublicationSession) (adminopenapi.APTPublicationSession, error) {
	id, err := uuid.Parse(session.ID)
	if err != nil {
		return adminopenapi.APTPublicationSession{}, err
	}
	repositoryID, err := uuid.Parse(session.RepositoryID)
	if err != nil {
		return adminopenapi.APTPublicationSession{}, err
	}
	response := adminopenapi.APTPublicationSession{
		Id: id, RepositoryId: repositoryID, Suite: session.Suite, Component: session.Component, Publisher: session.Publisher,
		ObjectName: session.ObjectName, DeclaredDigest: session.DeclaredDigest, DeclaredSize: session.DeclaredSize,
		State: adminopenapi.APTPublicationSessionState(session.State), ExpiresAt: session.ExpiresAt, CreatedAt: session.CreatedAt,
	}
	if session.ExpectedIdentity != "" {
		response.ExpectedIdentity = &session.ExpectedIdentity
	}
	if session.PackageRevisionID != "" {
		value, parseErr := uuid.Parse(session.PackageRevisionID)
		if parseErr != nil {
			return adminopenapi.APTPublicationSession{}, parseErr
		}
		response.PackageRevisionId = &value
	}
	return response, nil
}

func aptPackageRevisionResponse(revision repository.APTPackageRevision) (adminopenapi.APTPackageRevision, error) {
	id, err := uuid.Parse(revision.ID)
	if err != nil {
		return adminopenapi.APTPackageRevision{}, err
	}
	repositoryID, err := uuid.Parse(revision.RepositoryID)
	if err != nil {
		return adminopenapi.APTPackageRevision{}, err
	}
	return adminopenapi.APTPackageRevision{
		Id: id, RepositoryId: repositoryID, Package: revision.Package, Version: revision.Version, Architecture: revision.Architecture,
		CanonicalIdentity: revision.CanonicalIdentity, Digest: revision.Digest, Size: revision.Size, ObjectName: revision.ObjectName,
		Publisher: revision.Publisher, CreatedAt: revision.CreatedAt,
	}, nil
}

func writeAPTPublicationProblem(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, aptpublication.ErrInvalidSessionInput):
		writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "APT publication request is invalid")
	case errors.Is(err, aptpublication.ErrDigestMismatch):
		writeHostedProblem(w, http.StatusUnprocessableEntity, "digest_mismatch", "Debian package does not match the declared size or digest")
	case errors.Is(err, aptpublication.ErrIdentityMismatch):
		writeHostedProblem(w, http.StatusUnprocessableEntity, "identity_mismatch", "Debian package identity does not match expectedIdentity")
	case errors.Is(err, aptpublication.ErrInvalidPackage):
		writeHostedProblem(w, http.StatusUnprocessableEntity, "invalid_request", "Debian binary package is invalid")
	case errors.Is(err, repository.ErrNotFound):
		writeHostedProblem(w, http.StatusNotFound, "not_found", "APT publication resource not found")
	case errors.Is(err, repository.ErrIdempotencyConflict):
		writeHostedProblem(w, http.StatusConflict, "idempotency_conflict", "Idempotency-Key was already used with a different request")
	case errors.Is(err, repository.ErrNameExists), errors.Is(err, repository.ErrAPTPackageConflict):
		writeHostedProblem(w, http.StatusConflict, "coordinate_exists", "APT package identity already exists with different content")
	case errors.Is(err, repository.ErrDisabled):
		writeHostedProblem(w, http.StatusConflict, "invalid_state", "APT publication session is not writable")
	case errors.Is(err, repository.ErrQuotaExceeded):
		writeHostedProblem(w, http.StatusInsufficientStorage, "quota_exceeded", "repository capacity quota would be exceeded")
	default:
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "APT publication operation failed")
	}
}
