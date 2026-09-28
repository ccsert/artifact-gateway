package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/objectstore"
	"github.com/artifact-gateway/artifact-gateway/internal/protocol/cargo"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/felixge/httpsnoop"
	"github.com/google/uuid"
)

const cargoPublishBodyLimit = (129 << 20) + 8

type nativeCargoHandler struct {
	store      cargoPublicationStore
	repos      repository.HostedRepositoryStore
	objects    OCIObjectStore
	auth       Authenticator
	authorizer RepositoryAuthorizer
	audit      repository.Store
	anonymous  repository.AnonymousAccessPolicyStore
}

type cargoPublicationStore interface {
	repository.NativeCargoStore
	repository.LifecycleJobStore
}

type cargoRoute struct {
	repository string
	kind       string
	name       string
	version    string
}

func newNativeCargoHandler(store GatewayStore, objects OCIObjectStore, auth Authenticator) nativeCargoHandler {
	if objects == nil {
		objects = NewMemoryOCIObjectStore()
	}
	return nativeCargoHandler{store: store, repos: store, objects: objects, auth: auth,
		authorizer: RepositoryAuthorizer{Grants: store, Legacy: auth}, audit: store, anonymous: store}
}

func (h nativeCargoHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	route, ok := parseCargoRoute(r.URL.EscapedPath())
	if !ok {
		http.NotFound(w, r)
		return
	}
	repo, err := h.repos.GetHostedRepositoryByName(r.Context(), route.repository)
	if errors.Is(err, repository.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		h.writeError(w, http.StatusServiceUnavailable, "repository unavailable")
		return
	}
	if repo.Format != repository.FormatCargo || repo.Type != repository.RepositoryTypeHosted || repo.State != repository.RepositoryActive {
		http.NotFound(w, r)
		return
	}
	allowedMethod := false
	switch route.kind {
	case "publish", "unyank":
		allowedMethod = r.Method == http.MethodPut
	case "yank":
		allowedMethod = r.Method == http.MethodDelete
	default:
		allowedMethod = r.Method == http.MethodGet || r.Method == http.MethodHead
	}
	if !allowedMethod {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	principal, authenticated := h.principal(r)
	if !authenticated {
		if route.kind == "publish" || route.kind == "yank" || route.kind == "unyank" ||
			!anonymousHostedRepositoryReadAllowed(r.Context(), h.anonymous, repo, r.Method) {
			h.challenge(w, http.StatusUnauthorized)
			h.recordAudit(r, repo, route.name, route.kind, anonymousActor, repository.AuditAccessDenied, http.StatusUnauthorized, 0)
			return
		}
		principal = anonymousPrincipal()
	}
	switch route.kind {
	case "publish":
		h.publish(w, r, repo, principal)
		return
	case "yank", "unyank":
		h.setYanked(w, r, repo, route.name, route.version, route.kind == "yank", principal)
		return
	}
	if !isAnonymous(principal) {
		decision := h.authorizer.AuthorizeResource(r.Context(), principal, repo, RepositoryRead, route.name)
		if !decision.Allowed {
			h.challenge(w, http.StatusForbidden)
			h.recordAudit(r, repo, route.name, route.kind, principal.Actor, repository.AuditAccessDenied, http.StatusForbidden, 0)
			return
		}
	}
	switch route.kind {
	case "config":
		h.config(w, r, repo)
	case "search":
		h.search(w, r, repo, principal.Actor)
	case "index":
		h.index(w, r, repo, route.name, principal.Actor)
	case "download":
		h.download(w, r, repo, route.name, route.version, principal.Actor)
	default:
		http.NotFound(w, r)
	}
}

func parseCargoRoute(escapedPath string) (cargoRoute, bool) {
	if !strings.HasPrefix(escapedPath, "/cargo/") {
		return cargoRoute{}, false
	}
	repositoryName, path, found := strings.Cut(strings.TrimPrefix(escapedPath, "/cargo/"), "/")
	if !found || repositoryName == "" || strings.ContainsAny(repositoryName, "%\\") || path == "" {
		return cargoRoute{}, false
	}
	route := cargoRoute{repository: repositoryName}
	switch path {
	case "config.json":
		route.kind = "config"
		return route, true
	case "api/v1/crates/new":
		route.kind = "publish"
		return route, true
	case "api/v1/crates":
		route.kind = "search"
		return route, true
	}
	if strings.HasPrefix(path, "api/v1/crates/") {
		parts := strings.Split(strings.TrimPrefix(path, "api/v1/crates/"), "/")
		if len(parts) != 3 || (parts[2] != "download" && parts[2] != "yank" && parts[2] != "unyank") {
			return cargoRoute{}, false
		}
		name, nameErr := url.PathUnescape(parts[0])
		version, versionErr := url.PathUnescape(parts[1])
		if nameErr != nil || versionErr != nil || strings.ContainsAny(name+version, "/\\") {
			return cargoRoute{}, false
		}
		if _, err := cargo.NormalizeIdentity(name, version); err != nil {
			return cargoRoute{}, false
		}
		route.kind, route.name, route.version = parts[2], name, version
		return route, true
	}
	parts := strings.Split(path, "/")
	name := parts[len(parts)-1]
	expected, err := cargo.SparseIndexPath(name)
	if err != nil || expected != path {
		return cargoRoute{}, false
	}
	route.kind, route.name = "index", name
	return route, true
}

func (h nativeCargoHandler) principal(r *http.Request) (Principal, bool) {
	header := r.Header.Get("Authorization")
	if header != "" && !strings.HasPrefix(header, "Bearer ") && !strings.HasPrefix(header, "Basic ") {
		header = "Bearer " + header // Cargo's registry token is sent without a scheme.
	}
	if principal, ok := h.auth.Authenticate(header); ok {
		return principal, true
	}
	username, password, ok := r.BasicAuth()
	if !ok {
		return Principal{}, false
	}
	return h.auth.AuthenticateBasic(username, password)
}

func (h nativeCargoHandler) challenge(w http.ResponseWriter, status int) {
	h.writeError(w, status, "Cargo registry authentication or repository permission required")
}

func (h nativeCargoHandler) writeError(w http.ResponseWriter, status int, detail string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"errors": []map[string]string{{"detail": detail}}})
}

func cargoRepositoryURL(r *http.Request, name string) string {
	scheme := "http"
	if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	return (&url.URL{Scheme: scheme, Host: r.Host, Path: "/cargo/" + name}).String()
}

func (h nativeCargoHandler) config(w http.ResponseWriter, r *http.Request, repo repository.HostedRepository) {
	base := cargoRepositoryURL(r, repo.Name)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if r.Method == http.MethodHead {
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"dl": base + "/api/v1/crates", "api": base,
		"auth-required": !anonymousHostedRepositoryReadAllowed(r.Context(), h.anonymous, repo, http.MethodGet),
	})
}

func (h nativeCargoHandler) publish(w http.ResponseWriter, r *http.Request, repo repository.HostedRepository, principal Principal) {
	spool, err := spoolUpload(r.Body, cargoPublishBodyLimit)
	if errors.Is(err, errUploadTooLarge) {
		h.writeError(w, http.StatusRequestEntityTooLarge, "Cargo publish body is too large")
		return
	}
	if err != nil {
		h.writeError(w, http.StatusBadRequest, "read Cargo publish body failed")
		return
	}
	defer func() { _ = spool.Close() }()
	inspection, err := inspectCargoPublicationIdentity(r.Context(), repo.ID, spool.file, spool.Size())
	if err != nil {
		h.writeError(w, http.StatusUnprocessableEntity, "Cargo publication is invalid")
		return
	}
	decision := h.authorizer.AuthorizeResource(r.Context(), principal, repo, RepositoryWrite, inspection.Claim.Name)
	if !decision.Allowed {
		h.challenge(w, http.StatusForbidden)
		h.recordAudit(r, repo, inspection.Claim.Name, "crate", principal.Actor, repository.AuditAccessDenied, http.StatusForbidden, 0)
		return
	}
	registryURL := "sparse+" + cargoRepositoryURL(r, repo.Name) + "/"
	if _, err := cargo.TranslateIndexEntry(inspection.Envelope.Metadata,
		strings.TrimPrefix(inspection.Claim.Digest, "sha256:"), time.Now().UTC(), registryURL); err != nil {
		h.writeError(w, http.StatusUnprocessableEntity, "Cargo index metadata is invalid")
		return
	}
	reservation, _, err := h.store.ReserveCargoIdentity(r.Context(), inspection.Claim)
	if errors.Is(err, repository.ErrCargoIdentityConflict) {
		h.writeError(w, http.StatusConflict, "crate name or version is already reserved with different content")
		return
	}
	if err != nil {
		h.writeError(w, http.StatusServiceUnavailable, "reserve Cargo identity failed")
		return
	}
	entry, err := cargo.TranslateIndexEntry(inspection.Envelope.Metadata,
		strings.TrimPrefix(inspection.Claim.Digest, "sha256:"), reservation.CreatedAt, registryURL)
	if err != nil {
		h.writeError(w, http.StatusUnprocessableEntity, "Cargo index metadata is invalid")
		return
	}
	row, err := json.Marshal(entry)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "build Cargo index row failed")
		return
	}
	publication := repository.CargoPublication{
		CargoIdentityClaim: inspection.Claim,
		ObjectKey:          "native/cargo/sha256/" + strings.TrimPrefix(inspection.Claim.Digest, "sha256:"),
		Size:               inspection.Envelope.CrateSize, IndexRow: row, Publisher: principal.Actor,
		PublishedAt: reservation.CreatedAt,
	}
	if inspection.Envelope.Metadata.Description != nil {
		publication.Description = *inspection.Envelope.Metadata.Description
	}
	objectCtx, releaseObject, err := repository.LockObjectKeys(r.Context(), []string{publication.ObjectKey}, h.store,
		repository.FormatCargo, h.store.LockCargoObject)
	if err != nil {
		h.writeError(w, http.StatusServiceUnavailable, "Cargo object coordination is unavailable")
		return
	}
	defer releaseObject()
	if existing, lookupErr := h.store.GetCargoPublication(objectCtx, repo.ID, publication.Name, publication.Version); lookupErr == nil {
		if existing.Publisher != publication.Publisher {
			h.writeError(w, http.StatusConflict, "crate version belongs to a different publisher")
			return
		}
		publication.IndexRow, publication.PublishedAt = existing.IndexRow, existing.PublishedAt
	} else if !errors.Is(lookupErr, repository.ErrNotFound) {
		h.writeError(w, http.StatusServiceUnavailable, "check Cargo publication failed")
		return
	}
	created, err := h.ensureObject(objectCtx, spool.file, inspection.Envelope, publication)
	if err != nil {
		h.writeError(w, http.StatusServiceUnavailable, "store Cargo crate failed")
		return
	}
	committed, replay, err := h.store.CommitCargoPublication(objectCtx, publication)
	if err != nil {
		if created {
			h.cleanupUnreferencedObject(objectCtx, publication.ObjectKey)
		}
		switch {
		case repository.IsQuotaExceeded(err):
			h.writeError(w, http.StatusInsufficientStorage, "repository capacity quota exceeded")
		case errors.Is(err, repository.ErrCargoPublicationConflict), errors.Is(err, repository.ErrCargoIdentityConflict):
			h.writeError(w, http.StatusConflict, "crate version already exists with different content")
		default:
			h.writeError(w, http.StatusServiceUnavailable, "commit Cargo publication failed")
		}
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{"warnings": map[string]any{
		"invalid_categories": []string{}, "invalid_badges": []string{}, "other": []string{},
	}})
	if !replay {
		h.recordAudit(r, repo, committed.Name+"@"+committed.Version, "crate", principal.Actor, repository.AuditResolved, http.StatusOK, committed.Size)
	}
}

func (h nativeCargoHandler) ensureObject(ctx context.Context, source io.ReaderAt, envelope cargo.PublishEnvelope, publication repository.CargoPublication) (bool, error) {
	stored, err := h.objects.Stat(ctx, publication.ObjectKey)
	if err == nil {
		if stored.Digest == publication.Digest && stored.Size == publication.Size {
			return false, nil
		}
		return false, errors.New("cargo object checksum changed")
	}
	if !errors.Is(err, objectstore.ErrNotFound) {
		return false, err
	}
	payload, err := json.Marshal(cargoReclaimPayload{Format: repository.FormatCargo, ObjectKey: publication.ObjectKey})
	if err != nil {
		return false, err
	}
	jobID := uuid.NewString()
	if _, _, err = h.store.EnqueueLifecycleJob(ctx, repository.LifecycleJob{
		ID: jobID, RepositoryID: publication.RepositoryID, Kind: repository.LifecycleJobReclaim,
		IdempotencyKey: "cargo-publication-object:" + jobID, Payload: payload,
	}); err != nil {
		return false, err
	}
	section := io.NewSectionReader(source, envelope.CrateOffset, envelope.CrateSize)
	if err = h.objects.PutVerifiedReader(ctx, publication.ObjectKey, section, publication.Size, publication.Digest); err != nil {
		return false, err
	}
	return true, nil
}

func (h nativeCargoHandler) cleanupUnreferencedObject(ctx context.Context, objectKey string) {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	referenced, err := h.store.CargoObjectHasReference(cleanupCtx, objectKey)
	if err == nil && !referenced {
		_ = h.objects.Delete(cleanupCtx, objectKey)
	}
}

func (h nativeCargoHandler) setYanked(w http.ResponseWriter, r *http.Request, repo repository.HostedRepository, name, version string, yanked bool, principal Principal) {
	decision := h.authorizer.AuthorizeResource(r.Context(), principal, repo, RepositoryWrite, name)
	if !decision.Allowed {
		h.challenge(w, http.StatusForbidden)
		h.recordAudit(r, repo, name+"@"+version, "crate", principal.Actor, repository.AuditAccessDenied, http.StatusForbidden, 0)
		return
	}
	publication, _, err := h.store.SetCargoYanked(r.Context(), repo.ID, name, version, yanked)
	if errors.Is(err, repository.ErrNotFound) {
		h.writeError(w, http.StatusNotFound, "crate version does not exist")
		return
	}
	if err != nil {
		h.writeError(w, http.StatusServiceUnavailable, "update Cargo index failed")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
	h.recordAudit(r, repo, publication.Name+"@"+publication.Version, "index", principal.Actor, repository.AuditResolved, http.StatusOK, 0)
}

func (h nativeCargoHandler) search(w http.ResponseWriter, r *http.Request, repo repository.HostedRepository, actor string) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if query == "" || len(query) > 255 || strings.ContainsRune(query, '\x00') {
		h.writeError(w, http.StatusBadRequest, "q must contain between 1 and 255 characters")
		return
	}
	limit := 10
	if value := r.URL.Query().Get("per_page"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 || parsed > 100 {
			h.writeError(w, http.StatusBadRequest, "per_page must be between 1 and 100")
			return
		}
		limit = parsed
	}
	items, total, err := h.store.SearchCargoCrates(r.Context(), repo.ID, query, limit, "", false)
	if err != nil {
		h.writeError(w, http.StatusServiceUnavailable, "Cargo search unavailable")
		return
	}
	results := make([]map[string]string, 0, len(items))
	for _, item := range items {
		results = append(results, map[string]string{
			"name": item.Name, "max_version": item.MaxVersion, "description": item.Description,
		})
	}
	body, err := json.Marshal(map[string]any{"crates": results, "meta": map[string]int{"total": total}})
	if err != nil {
		h.writeError(w, http.StatusServiceUnavailable, "Cargo search unavailable")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if r.Method == http.MethodGet {
		_, _ = w.Write(body)
	}
	h.recordAudit(r, repo, query, "search", actor, repository.AuditResolved, http.StatusOK, int64(len(body)))
}

func (h nativeCargoHandler) index(w http.ResponseWriter, r *http.Request, repo repository.HostedRepository, name, actor string) {
	items, err := h.store.ListCargoPublications(r.Context(), repo.ID, name)
	if errors.Is(err, repository.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		h.writeError(w, http.StatusServiceUnavailable, "Cargo index unavailable")
		return
	}
	var body bytes.Buffer
	for _, item := range items {
		var entry cargo.IndexEntry
		if err := json.Unmarshal(item.IndexRow, &entry); err != nil {
			h.writeError(w, http.StatusServiceUnavailable, "Cargo index metadata is invalid")
			return
		}
		entry.Yanked = item.Yanked
		row, err := json.Marshal(entry)
		if err != nil {
			h.writeError(w, http.StatusServiceUnavailable, "Cargo index metadata is invalid")
			return
		}
		body.Write(row)
		body.WriteByte('\n')
	}
	sum := sha256.Sum256(body.Bytes())
	w.Header().Set("ETag", `"`+hex.EncodeToString(sum[:])+`"`)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	captured := httpsnoop.CaptureMetricsFn(w, func(output http.ResponseWriter) {
		// A yank can change the index more than once in one second. Strong ETags
		// remain precise where Last-Modified's second resolution would not.
		http.ServeContent(output, r, name, time.Time{}, bytes.NewReader(body.Bytes()))
	})
	h.recordAudit(r, repo, name, "index", actor, repository.AuditResolved, captured.Code, captured.Written)
}

func (h nativeCargoHandler) download(w http.ResponseWriter, r *http.Request, repo repository.HostedRepository, name, version, actor string) {
	publication, err := h.store.GetCargoPublication(r.Context(), repo.ID, name, version)
	if errors.Is(err, repository.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		h.writeError(w, http.StatusServiceUnavailable, "Cargo crate unavailable")
		return
	}
	reader, size, err := h.objects.Open(r.Context(), publication.ObjectKey)
	if err != nil {
		h.writeError(w, http.StatusServiceUnavailable, "Cargo crate object unavailable")
		return
	}
	defer func() { _ = reader.Close() }()
	if size != publication.Size {
		h.writeError(w, http.StatusServiceUnavailable, "Cargo crate object size changed")
		return
	}
	spool, err := spoolUpload(reader, publication.Size)
	if err != nil || spool.Size() != publication.Size || spool.Digest() != publication.Digest {
		if spool != nil {
			_ = spool.Close()
		}
		h.writeError(w, http.StatusServiceUnavailable, "Cargo crate object checksum changed")
		return
	}
	defer func() { _ = spool.Close() }()
	w.Header().Set("ETag", `"`+strings.TrimPrefix(publication.Digest, "sha256:")+`"`)
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("X-Checksum-Sha256", strings.TrimPrefix(publication.Digest, "sha256:"))
	captured := httpsnoop.CaptureMetricsFn(w, func(output http.ResponseWriter) {
		http.ServeContent(output, r, name+"-"+version+".crate", publication.PublishedAt, spool.file)
	})
	h.recordAudit(r, repo, name+"@"+version, "crate", actor, repository.AuditResolved, captured.Code, captured.Written)
}

func (h nativeCargoHandler) recordAudit(r *http.Request, repo repository.HostedRepository, resource, representation, actor string, outcome repository.AuditOutcome, status int, size int64) {
	if actor == "" {
		actor = anonymousActor
	}
	_ = h.audit.RecordAudit(r.Context(), repository.AuditRecord{
		GroupName: repo.Name, Repository: repo.Name, Actor: actor, Outcome: outcome,
		OccurredAt: time.Now().UTC(), Format: string(repository.FormatCargo), Resource: resource,
		Representation: representation, MemberType: string(repo.Type), Operation: strings.ToLower(r.Method),
		Status: status, Bytes: size, CacheDisposition: "bypass",
	})
}
