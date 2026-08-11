package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

type nativeConanPublishHandler struct {
	store              GatewayStore
	objects            OCIObjectStore
	auth               Authenticator
	authorizer         RepositoryAuthorizer
	publicationScanner *publicationScanScheduler
}

type nativeConanPublishRequest struct {
	Kind, Reference, RecipeRevision, PackageID, PackageRevision string
	Objects                                                     []repository.MavenDeclaredObject
}

func newNativeConanPublishHandler(store GatewayStore, objects OCIObjectStore, auth Authenticator) nativeConanPublishHandler {
	if objects == nil {
		objects = NewMemoryOCIObjectStore()
	}
	return nativeConanPublishHandler{store: store, objects: objects, auth: auth, authorizer: RepositoryAuthorizer{Grants: store, Legacy: auth}}
}

func (h nativeConanPublishHandler) withPublicationScanner(scanner publicationScanScheduler) nativeConanPublishHandler {
	h.publicationScanner = &scanner
	return h
}

func (h nativeConanPublishHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/v2/repositories/") {
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/v2/repositories/"), "/")
		if len(parts) == 2 && parts[0] != "" && parts[1] == "conan-publish-sessions" && r.Method == http.MethodPost {
			h.create(w, r, parts[0])
			return
		}
	}
	if strings.HasPrefix(r.URL.Path, "/api/v2/conan-publish-sessions/") {
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/v2/conan-publish-sessions/"), "/")
		id := strings.TrimSuffix(parts[0], ":commit")
		if len(parts) == 1 && strings.HasSuffix(parts[0], ":commit") && r.Method == http.MethodPost {
			h.withSession(w, r, id, RepositoryWrite, func(session repository.ConanPublishSession) { h.commit(w, r, session) })
			return
		}
		if len(parts) == 3 && parts[1] == "objects" && r.Method == http.MethodPut {
			h.withSession(w, r, id, RepositoryWrite, func(session repository.ConanPublishSession) { h.upload(w, r, session, parts[2]) })
			return
		}
		if len(parts) == 1 && r.Method == http.MethodGet {
			h.withSession(w, r, id, RepositoryRead, func(session repository.ConanPublishSession) { writeNativeMavenJSON(w, http.StatusOK, session) })
			return
		}
	}
	http.NotFound(w, r)
}

func (h nativeConanPublishHandler) create(w http.ResponseWriter, r *http.Request, repositoryID string) {
	principal, ok := h.auth.Authenticate(r.Header.Get("Authorization"))
	if !ok {
		writeHostedProblem(w, http.StatusUnauthorized, "access_denied", "authentication is required")
		return
	}
	repo, err := h.store.GetHostedRepository(r.Context(), repositoryID)
	if errors.Is(err, repository.ErrNotFound) || repo.Format != repository.FormatConan || repo.State != repository.RepositoryActive {
		writeHostedProblem(w, http.StatusNotFound, "not_found", "Conan repository not found")
		return
	}
	if err != nil {
		writeHostedProblem(w, 500, "internal_error", "get repository failed")
		return
	}
	var body nativeConanPublishRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<20))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&body) != nil || !validConanPublishRequest(body) {
		writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "valid Conan revision and declared objects are required")
		return
	}
	if decision := h.authorizer.AuthorizeResource(r.Context(), principal, repo, RepositoryWrite, conanPublishResource(body)); !decision.Allowed {
		writeHostedProblem(w, http.StatusForbidden, "access_denied", "repository scope is required")
		return
	}
	session, err := h.store.CreateConanPublishSession(r.Context(), repository.ConanPublishSession{ID: uuid.NewString(), RepositoryID: repo.ID, Publisher: principal.Actor, Kind: body.Kind, Reference: body.Reference, RecipeRevision: body.RecipeRevision, PackageID: body.PackageID, PackageRevision: body.PackageRevision, State: "open", Objects: body.Objects, ExpiresAt: time.Now().UTC().Add(time.Hour)})
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "create Conan publish session failed")
		return
	}
	writeNativeMavenJSON(w, http.StatusCreated, session)
}

func (h nativeConanPublishHandler) upload(w http.ResponseWriter, r *http.Request, session repository.ConanPublishSession, name string) {
	var declared *repository.MavenDeclaredObject
	for i := range session.Objects {
		if session.Objects[i].Name == name {
			declared = &session.Objects[i]
			break
		}
	}
	if declared == nil {
		writeHostedProblem(w, http.StatusNotFound, "not_found", "declared object not found")
		return
	}
	spool, err := spoolUpload(r.Body, declared.Size)
	if err != nil || spool.Size() != declared.Size {
		if spool != nil {
			_ = spool.Close()
		}
		writeHostedProblem(w, http.StatusUnprocessableEntity, "digest_mismatch", "uploaded object size does not match declaration")
		return
	}
	defer func() { _ = spool.Close() }()
	digest := spool.Digest()
	if digest != declared.Digest {
		writeHostedProblem(w, http.StatusUnprocessableEntity, "digest_mismatch", "uploaded object digest does not match declaration")
		return
	}
	key := "native/conan/sessions/" + session.ID + "/" + name
	if err = h.store.StageConanObject(r.Context(), repository.ConanObjectIntent{RepositoryID: session.RepositoryID, ObjectKey: key, Digest: digest, Size: spool.Size()}); err != nil {
		writeHostedProblem(w, 500, "internal_error", "stage Conan object failed")
		return
	}
	if err = h.store.MarkConanPublishObject(r.Context(), session.ID, name, key); err != nil {
		writeHostedProblem(w, http.StatusConflict, "session_closed", "publish session is closed")
		return
	}
	if err = h.objects.PutVerifiedReader(r.Context(), key, spool.Reader(), spool.Size(), digest); err != nil {
		writeHostedProblem(w, 500, "internal_error", "stage Conan object failed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h nativeConanPublishHandler) commit(w http.ResponseWriter, r *http.Request, session repository.ConanPublishSession) {
	if session.State != "open" || time.Now().After(session.ExpiresAt) {
		writeHostedProblem(w, http.StatusConflict, "session_closed", "publish session is closed")
		return
	}
	if h.conanRevisionExists(r, session) {
		writeHostedProblem(w, http.StatusConflict, "revision_exists", "Conan revision already exists")
		return
	}
	uploads, err := h.store.ListConanPublishUploads(r.Context(), session.ID)
	if err != nil || len(uploads) != len(session.Objects) {
		writeHostedProblem(w, http.StatusUnprocessableEntity, "invalid_publish_session", "every declared object must be uploaded")
		return
	}
	assets := make([]repository.ConanAsset, 0, len(session.Objects))
	for _, object := range session.Objects {
		key := uploads[object.Name]
		info, statErr := h.objects.Stat(r.Context(), key)
		if statErr != nil || info.Size != object.Size || info.Digest != object.Digest {
			writeHostedProblem(w, http.StatusUnprocessableEntity, "invalid_publish_session", "staged object is unavailable or has changed")
			return
		}
		assets = append(assets, repository.ConanAsset{RepositoryID: session.RepositoryID, Reference: session.Reference, RecipeRevision: session.RecipeRevision, PackageID: session.PackageID, PackageRevision: session.PackageRevision, Path: object.Name, ObjectKey: key, Digest: object.Digest, Size: object.Size})
	}
	digest := conanPublishDigest(session.Objects)
	if session.Kind == "recipe" {
		_, err = h.store.PutConanRecipeRevision(r.Context(), repository.ConanRecipeRevision{RepositoryID: session.RepositoryID, Reference: session.Reference, Revision: session.RecipeRevision, Digest: digest}, assets)
	} else {
		_, err = h.store.PutConanPackageRevision(r.Context(), repository.ConanPackageRevision{RepositoryID: session.RepositoryID, Reference: session.Reference, RecipeRevision: session.RecipeRevision, PackageID: session.PackageID, Revision: session.PackageRevision, Digest: digest}, assets)
	}
	if errors.Is(err, repository.ErrDisabled) {
		writeHostedProblem(w, http.StatusConflict, "revision_exists", "Conan revision already exists")
		return
	}
	if repository.IsQuotaExceeded(err) {
		writeHostedProblem(w, http.StatusInsufficientStorage, "quota_exceeded", "repository capacity quota exceeded")
		return
	}
	if err != nil {
		writeHostedProblem(w, http.StatusUnprocessableEntity, "invalid_publish_session", "Conan revision cannot be published")
		return
	}
	if err = h.store.CommitConanPublishSession(r.Context(), session.ID); err != nil {
		writeHostedProblem(w, 500, "internal_error", "mark Conan publish session committed failed")
		return
	}
	if h.publicationScanner != nil {
		coordinate := session.Reference + "#" + session.RecipeRevision
		if session.Kind == "package" {
			coordinate += "/" + session.PackageID + "#" + session.PackageRevision
		}
		_ = h.publicationScanner.ScheduleRepository(r.Context(), session.RepositoryID, repository.FormatConan, coordinate, digest, session.Publisher)
	}
	writeNativeMavenJSON(w, http.StatusOK, map[string]string{"state": "visible", "revision": session.RecipeRevision})
}

func (h nativeConanPublishHandler) conanRevisionExists(r *http.Request, session repository.ConanPublishSession) bool {
	if session.Kind == "recipe" {
		_, err := h.store.GetConanRecipeRevision(r.Context(), session.RepositoryID, session.Reference, session.RecipeRevision)
		return err == nil
	}
	_, err := h.store.GetConanPackageRevision(r.Context(), session.RepositoryID, session.Reference, session.RecipeRevision, session.PackageID, session.PackageRevision)
	return err == nil
}

func (h nativeConanPublishHandler) withSession(w http.ResponseWriter, r *http.Request, id string, operation RepositoryOperation, next func(repository.ConanPublishSession)) {
	session, err := h.store.GetConanPublishSession(r.Context(), id)
	if errors.Is(err, repository.ErrNotFound) {
		writeHostedProblem(w, http.StatusNotFound, "not_found", "Conan publish session not found")
		return
	}
	if err != nil {
		writeHostedProblem(w, 500, "internal_error", "load Conan publish session failed")
		return
	}
	h.withRepositoryResource(w, r, session.RepositoryID, operation, conanSessionResource(session), func(Principal, repository.HostedRepository) { next(session) })
}

func (h nativeConanPublishHandler) withRepositoryResource(w http.ResponseWriter, r *http.Request, id string, operation RepositoryOperation, resource string, next func(Principal, repository.HostedRepository)) {
	principal, ok := h.auth.Authenticate(r.Header.Get("Authorization"))
	if !ok {
		writeHostedProblem(w, http.StatusUnauthorized, "access_denied", "authentication is required")
		return
	}
	repo, err := h.store.GetHostedRepository(r.Context(), id)
	if errors.Is(err, repository.ErrNotFound) {
		writeHostedProblem(w, http.StatusNotFound, "not_found", "repository not found")
		return
	}
	if err != nil {
		writeHostedProblem(w, 500, "internal_error", "get repository failed")
		return
	}
	if decision := h.authorizer.AuthorizeResource(r.Context(), principal, repo, operation, resource); !decision.Allowed {
		writeHostedProblem(w, http.StatusForbidden, "access_denied", "repository scope is required")
		return
	}
	next(principal, repo)
}

func conanPublishResource(request nativeConanPublishRequest) string {
	resource := request.Reference + "/revisions/" + request.RecipeRevision
	if request.Kind == "package" {
		resource += "/packages/" + request.PackageID + "/revisions/" + request.PackageRevision
	}
	return resource
}

func conanSessionResource(session repository.ConanPublishSession) string {
	return conanPublishResource(nativeConanPublishRequest{Kind: session.Kind, Reference: session.Reference, RecipeRevision: session.RecipeRevision, PackageID: session.PackageID, PackageRevision: session.PackageRevision})
}

func validConanPublishRequest(request nativeConanPublishRequest) bool {
	parts := strings.Split(request.Reference, "/")
	if len(parts) != 4 || request.Kind != "recipe" && request.Kind != "package" || !validDeclaredObjects(request.Objects) {
		return false
	}
	for _, part := range append(parts, request.RecipeRevision, request.PackageID, request.PackageRevision) {
		if part != "" && !validConanSegment(part) {
			return false
		}
	}
	if request.RecipeRevision == "" {
		return false
	}
	return request.Kind == "recipe" && request.PackageID == "" && request.PackageRevision == "" || request.Kind == "package" && request.PackageID != "" && request.PackageRevision != ""
}

func conanPublishDigest(objects []repository.MavenDeclaredObject) string {
	items := append([]repository.MavenDeclaredObject(nil), objects...)
	sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
	hash := sha256.New()
	for _, object := range items {
		_, _ = hash.Write([]byte(object.Name + "\x00" + object.Digest + "\x00"))
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil))
}
