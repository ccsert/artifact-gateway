package app

import (
	"crypto/sha1" // The Conan 2 upload wire checksum is SHA-1, not the storage identity.
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"strings"

	conanprotocol "github.com/ccsert/artifact-gateway/internal/protocol/conan"
	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

func (h ConanHandler) uploadNativeConan(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.EscapedPath(), "/"), "/")
	if len(parts) < 4 || parts[0] != "conan" || parts[2] != "v2" || parts[3] != "conans" {
		http.NotFound(w, r)
		return
	}
	p, err := conanprotocol.ParseUploadPath(r.URL.EscapedPath())
	if err != nil {
		writeHostedProblem(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if nilDependency(h.Repositories) {
		writeHostedProblem(w, http.StatusNotImplemented, "not_supported", "Conan publication is unavailable")
		return
	}
	repo, err := h.Repositories.GetHostedRepositoryByName(r.Context(), p.Repository)
	if errors.Is(err, repository.ErrNotFound) || err == nil && (repo.Format != repository.FormatConan || repo.State != repository.RepositoryActive) {
		writeHostedProblem(w, http.StatusNotFound, "not_found", "Conan Hosted repository not found")
		return
	}
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "repository lookup failed")
		return
	}
	principal, ok := h.authenticate(r)
	if !ok {
		w.Header().Set("WWW-Authenticate", `Basic realm="Artifact Gateway Conan"`)
		h.audit(withConanAuditStatus(r.Context(), http.StatusUnauthorized), repo.Name, p.Reference, "native", "anonymous", repository.AuditAccessDenied)
		writeHostedProblem(w, http.StatusUnauthorized, "access_denied", "authentication is required")
		return
	}
	incoming := repository.ConanPublishSession{RepositoryID: repo.ID, Publisher: principal.Actor, Kind: p.Kind(), Reference: p.Reference, RecipeRevision: p.RecipeRevision, PackageID: p.PackageID, PackageRevision: p.PackageRevision}
	resource := conanSessionResource(incoming)
	decision := h.Authorizer.AuthorizeResource(r.Context(), principal, repo, RepositoryWrite, resource)
	if !decision.Allowed {
		h.audit(withConanAuditAuthorization(withConanAuditStatus(r.Context(), http.StatusForbidden), decision), repo.Name, resource, "native", principal.Actor, repository.AuditAccessDenied)
		writeHostedProblem(w, http.StatusForbidden, "access_denied", "repository write permission is required")
		return
	}
	if repo.Type != repository.RepositoryTypeHosted {
		w.Header().Set("Allow", "GET, HEAD")
		writeHostedProblem(w, http.StatusMethodNotAllowed, "not_supported", "Conan uploads require a Hosted repository")
		return
	}
	if nilDependency(h.NativeStore) || nilDependency(h.NativeObjects) || nilDependency(h.Publications) {
		writeHostedProblem(w, http.StatusNotImplemented, "not_supported", "Conan publication capability is unavailable")
		return
	}
	checksum := strings.ToLower(r.Header.Get("X-Checksum-Sha1"))
	if decoded, err := hex.DecodeString(checksum); err != nil || len(decoded) != sha1.Size {
		writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "X-Checksum-Sha1 is required")
		return
	}
	limit := int64(1 << 30)
	if p.File == conanprotocol.ManifestName || p.File == "conaninfo.txt" {
		limit = conanprotocol.ManifestMaxBytes
	}
	spool, err := spoolUpload(r.Body, limit)
	if err != nil {
		writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "Conan upload is incomplete or exceeds its byte limit")
		return
	}
	defer func() { _ = spool.Close() }()
	hash := sha1.New()
	if _, err = io.Copy(hash, io.NewSectionReader(spool.File(), 0, spool.Size())); err != nil || hex.EncodeToString(hash.Sum(nil)) != checksum || r.ContentLength >= 0 && spool.Size() != r.ContentLength {
		writeHostedProblem(w, http.StatusUnprocessableEntity, "digest_mismatch", "Conan upload checksum or length does not match")
		return
	}
	session, err := h.Publications.BeginConanNativeSession(r.Context(), incoming)
	if err != nil {
		writeConanPublicationError(w, err)
		return
	}
	// Never reuse a mutable staging key: retries and concurrent writers may
	// replace session pointers, but cannot alter a verified snapshot's bytes.
	key := "native/conan/sessions/" + session.ID + "/" + uuid.NewString() + "/" + p.File
	object := repository.MavenDeclaredObject{Name: p.File, Digest: spool.Digest(), Size: spool.Size()}
	if err = h.NativeStore.StageConanObject(r.Context(), repository.ConanObjectIntent{RepositoryID: repo.ID, ObjectKey: key, Digest: object.Digest, Size: object.Size}); err != nil {
		writeConanPublicationError(w, err)
		return
	}
	if err = h.NativeObjects.PutVerifiedReader(r.Context(), key, spool.Reader(), object.Size, object.Digest); err != nil {
		writeConanPublicationError(w, err)
		return
	}
	session, err = h.Publications.StageConanPublicationObject(r.Context(), session.ID, object, key)
	if err != nil {
		writeConanPublicationError(w, err)
		return
	}
	w.Header().Set("X-Artifact-Gateway-Publish-Session", session.ID)
	w.Header().Set("X-Artifact-Gateway-Publish-State", session.State)
	if p.File != conanprotocol.ManifestName {
		w.WriteHeader(http.StatusCreated)
		return
	}
	uploads, err := h.NativeStore.ListConanPublishUploads(r.Context(), session.ID)
	if err != nil {
		writeConanPublicationError(w, err)
		return
	}
	if err = validateConanPublication(r.Context(), h.NativeObjects, session, uploads); err != nil {
		writeHostedProblem(w, http.StatusUnprocessableEntity, "invalid_publication", err.Error())
		return
	}
	digest := conanPublishDigest(session.Objects)
	committed, _, err := h.Publications.CommitConanPublicationSession(r.Context(), session.ID, session.Version, digest, principal.Actor)
	if err != nil {
		writeConanPublicationError(w, err)
		return
	}
	if h.PublicationScanner != nil {
		_ = h.PublicationScanner.Schedule(r.Context(), repo, conanSessionCoordinate(committed), digest, principal.Actor)
	}
	w.Header().Set("X-Artifact-Gateway-Publish-State", committed.State)
	w.WriteHeader(http.StatusCreated)
}

func writeConanPublicationError(w http.ResponseWriter, err error) {
	switch {
	case repository.IsQuotaExceeded(err):
		writeHostedProblem(w, http.StatusInsufficientStorage, "quota_exceeded", "repository capacity quota exceeded")
	case errors.Is(err, repository.ErrNameExists):
		writeHostedProblem(w, http.StatusConflict, "revision_conflict", "immutable Conan revision already exists with different bytes")
	case errors.Is(err, repository.ErrArtifactTombstoned):
		writeHostedProblem(w, http.StatusConflict, "artifact_tombstoned", "Conan recipe revision is tombstoned")
	case errors.Is(err, repository.ErrDisabled), errors.Is(err, repository.ErrVersionConflict), errors.Is(err, repository.ErrNotFound):
		writeHostedProblem(w, http.StatusConflict, "publication_conflict", "Conan session, recipe state or staged snapshot does not permit publication")
	default:
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "Conan publication storage failed")
	}
}
