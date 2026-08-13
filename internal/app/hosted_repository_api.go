package app

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	adminopenapi "github.com/artifact-gateway/artifact-gateway/internal/admin/openapi"
	"github.com/artifact-gateway/artifact-gateway/internal/aptpublication"
	"github.com/artifact-gateway/artifact-gateway/internal/egress"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/artifact-gateway/artifact-gateway/internal/scanning"
	"github.com/google/uuid"
)

var hostedRepositoryName = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)

// hostedRepositoryAPIHandler is the versioned management surface described by
// native-hosted-v1.json. It intentionally does not reuse the V2 Group routes.
type hostedRepositoryAPIHandler struct {
	store         repository.HostedRepositoryStore
	groups        repository.HostedGroupStore
	authenticator Authenticator
}

type createHostedRepositoryRequest struct {
	Name          string              `json:"name"`
	Format        repository.Format   `json:"format"`
	Type          string              `json:"type,omitempty"`
	Endpoint      string              `json:"endpoint,omitempty"`
	AllowedHosts  []string            `json:"allowedHosts,omitempty"`
	EgressProxy   *egressProxyRequest `json:"egressProxy,omitempty"`
	AnonymousRead bool                `json:"anonymousRead,omitempty"`
}

type updateHostedRepositoryRequest struct {
	Endpoint      *string             `json:"endpoint,omitempty"`
	AllowedHosts  []string            `json:"allowedHosts,omitempty"`
	EgressProxy   *egressProxyRequest `json:"egressProxy,omitempty"`
	AnonymousRead *bool               `json:"anonymousRead,omitempty"`
}

type repositoryPage struct {
	Items         []repository.HostedRepository `json:"items"`
	NextPageToken string                        `json:"nextPageToken,omitempty"`
}

type repositoryPageCursor struct {
	Endpoint, ID string
	ExpiresAt    int64
}

type ociImagePageCursor struct {
	Endpoint, RepositoryID, Prefix, Name string
	ExpiresAt                            int64
}

type ociManifestPageCursor struct {
	Endpoint, RepositoryID, Name, Digest string
	ExpiresAt                            int64
}

type mavenCoordinatePageCursor struct {
	Endpoint, RepositoryID, Prefix, Coordinate string
	BuildNumber                                int
	ExpiresAt                                  int64
}

type conanReferencePageCursor struct {
	Endpoint, RepositoryID, Prefix, Reference string
	ExpiresAt                                 int64
}

type conanRevisionPageCursor struct {
	Endpoint, RepositoryID, Reference, Query, Revision string
	ExpiresAt                                          int64
}

type artifactSearchPageCursor struct {
	Endpoint, RepositoryID, Format, Query, Coordinate string
	BuildNumber                                       int
	ExpiresAt                                         int64
}

type artifactSearchPosition struct {
	Coordinate  string
	BuildNumber int
	Digest      string
}

type retentionDryRunPageCursor struct {
	Endpoint, RepositoryID, PolicyVersion, Coordinate, ArtifactID string
	ExpiresAt                                                     int64
}

type tombstonePageCursor struct {
	Endpoint, RepositoryID, Format, Prefix, Coordinate string
	ExpiresAt                                          int64
}

type auditPageCursor struct {
	Endpoint, GroupName, Repository, Outcome, Format, Operation, Actor string
	From, To                                                           time.Time
	OccurredAt                                                         time.Time
	ID                                                                 int64
	ExpiresAt                                                          int64
}

func (h hostedRepositoryAPIHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.authorize(w, r)
	if !ok {
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/v2/repositories")
	if path == "" || path == "/" {
		switch r.Method {
		case http.MethodGet:
			h.list(w, r)
		case http.MethodPost:
			h.create(w, r, principal)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
		return
	}
	if strings.Count(strings.Trim(path, "/"), "/") != 0 {
		writeHostedProblem(w, http.StatusNotFound, "not_found", "repository not found")
		return
	}
	id := strings.Trim(path, "/")
	switch r.Method {
	case http.MethodGet:
		h.get(w, r, id)
	case http.MethodDelete:
		h.disable(w, r, id)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (h hostedRepositoryAPIHandler) authorize(w http.ResponseWriter, r *http.Request) (Principal, bool) {
	principal, ok := h.authenticate(w, r)
	if !ok || !principal.Admin || principal.MustChangePassword {
		writeHostedProblem(w, http.StatusUnauthorized, "access_denied", "administrator authentication is required")
		return Principal{}, false
	}
	return principal, true
}

func (h hostedRepositoryAPIHandler) authenticate(w http.ResponseWriter, r *http.Request) (Principal, bool) {
	principal, ok := h.authenticator.Authenticate(r.Header.Get("Authorization"))
	if !ok {
		writeHostedProblem(w, http.StatusUnauthorized, "access_denied", "authentication is required")
		return Principal{}, false
	}
	return principal, true
}

func (h hostedRepositoryAPIHandler) authenticateManagementRequest(w http.ResponseWriter, r *http.Request) (Principal, bool) {
	if principal, ok := h.authenticator.Authenticate(r.Header.Get("Authorization")); ok {
		return principal, true
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return h.authenticate(w, r)
	}
	repositoryID, browse := managementBrowseRepositoryID(r.URL.Path)
	if !browse {
		return h.authenticate(w, r)
	}
	repo, err := h.store.GetHostedRepository(r.Context(), repositoryID)
	if err == nil && anonymousHostedRepositoryReadAllowed(r.Context(), h.store, repo, r.Method) {
		return anonymousPrincipal(), true
	}
	if errors.Is(err, repository.ErrNotFound) && h.groups != nil {
		group, groupErr := h.groups.GetHostedGroup(r.Context(), repositoryID)
		if groupErr == nil && anonymousHostedGroupReadAllowed(r.Context(), h.store, h.store, group, r.Method) {
			return anonymousPrincipal(), true
		}
	}
	return h.authenticate(w, r)
}

func managementBrowseRepositoryID(path string) (string, bool) {
	const prefix = "/api/v2/repositories/"
	rest := strings.TrimPrefix(path, prefix)
	if rest == path {
		return "", false
	}
	parts := strings.Split(rest, "/")
	if len(parts) == 2 && (parts[1] == "artifacts" || parts[1] == "artifact-search" || parts[1] == "artifact-intelligence") {
		return parts[0], true
	}
	if len(parts) == 3 && parts[1] == "artifacts" {
		return parts[0], true
	}
	if len(parts) == 3 && ((parts[1] == "oci" && (parts[2] == "images" || parts[2] == "manifests")) || (parts[1] == "maven" && parts[2] == "coordinates") || (parts[1] == "conan" && (parts[2] == "references" || parts[2] == "recipe-revisions" || parts[2] == "package-revisions" || parts[2] == "package-ids")) || (parts[1] == "cache" && parts[2] == "entries")) {
		return parts[0], true
	}
	return "", false
}

// generatedRepositoryAPIAdapter keeps authorization and domain behavior in the
// existing handler while the generated OpenAPI wrapper owns route and parameter
// binding for the active repository-management surface.
type generatedRepositoryAPIAdapter struct {
	hostedRepositoryAPIHandler
	sessions               nativeMavenHandler
	aptPublication         *aptpublication.Manager
	aptSnapshotPublisher   *aptpublication.Publisher
	aptPublications        repository.NativeAPTPublicationStore
	groups                 repository.HostedGroupStore
	grants                 repository.RepositoryGrantStore
	templates              repository.AuthorizationTemplateStore
	authorizationRoles     repository.AuthorizationRoleStore
	retentionPolicies      repository.RepositoryRetentionPolicyStore
	securityPolicies       repository.RepositorySecurityPolicyStore
	quarantineReadPolicies repository.RepositoryQuarantineReadPolicyStore
	capacities             repository.RepositoryCapacityStore
	tombstones             repository.ArtifactTombstoneStore
	lifecycleJobs          repository.LifecycleJobStore
	auditRetention         repository.AuditRetentionStore
	anonymousAccess        repository.AnonymousAccessPolicyStore
	oidcRuntime            *OIDCRuntime
	replication            repository.ReplicationStore
	oci                    repository.NativeOCIStore
	conan                  repository.NativeConanStore
	apiKeys                repository.APIKeyStore
	users                  userManagementStore
	authorizer             RepositoryAuthorizer
	audit                  repository.Store
	metrics                *Metrics
	maintenance            *CacheMaintenance
	proxyCache             proxyCacheBrowseHandler
	mavenProxy             mavenProxyOperationsHandler
	searchProjection       repository.ArtifactSearchStore
	intelligence           repository.ArtifactIntelligenceStore
	quarantine             repository.ArtifactQuarantineStore
	runtimeNodes           repository.RuntimeNodeStore
	scheduledTasks         repository.ScheduledTaskStore
	webhooks               repository.WebhookStore
	queueStats             repository.BackgroundOperationQueueStore
	diagnostics            Dependencies
	artifactScanner        scanning.Scanner
	artifactScanFormats    []repository.Format
}

type userManagementStore interface {
	repository.UserStore
	repository.UserIdentityStore
	repository.UserSessionStore
}

var _ adminopenapi.ServerInterface = generatedRepositoryAPIAdapter{}

func (h generatedRepositoryAPIAdapter) ListRepositories(w http.ResponseWriter, r *http.Request, params adminopenapi.ListRepositoriesParams) {
	if _, ok := h.authorize(w, r); ok {
		h.listBound(w, r, params)
	}
}

func (h generatedRepositoryAPIAdapter) CreateRepository(w http.ResponseWriter, r *http.Request, params adminopenapi.CreateRepositoryParams) {
	if principal, ok := h.authorize(w, r); ok {
		h.createWithIdempotencyKey(w, r, principal, string(params.IdempotencyKey))
	}
}

func (h generatedRepositoryAPIAdapter) GetCurrentIdentity(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	writeNativeMavenJSON(w, http.StatusOK, currentIdentityResponse(principal))
}

func (h generatedRepositoryAPIAdapter) DeleteRepository(w http.ResponseWriter, r *http.Request, id adminopenapi.RepositoryId) {
	h.withRepositoryScope(w, r, id.String(), RepositoryWrite, func(Principal, repository.HostedRepository) {
		h.disable(w, r, id.String())
	})
}

func (h generatedRepositoryAPIAdapter) GetRepository(w http.ResponseWriter, r *http.Request, id adminopenapi.RepositoryId) {
	h.withRepositoryScope(w, r, id.String(), RepositoryRead, func(Principal, repository.HostedRepository) {
		h.get(w, r, id.String())
	})
}

func (h generatedRepositoryAPIAdapter) UpdateRepository(w http.ResponseWriter, r *http.Request, id adminopenapi.RepositoryId, params adminopenapi.UpdateRepositoryParams) {
	h.withRepositoryScope(w, r, id.String(), RepositoryWrite, func(_ Principal, repo repository.HostedRepository) {
		h.update(w, r, repo, string(params.IfMatch))
	})
}

func (h generatedRepositoryAPIAdapter) GetRepositoryCapabilities(w http.ResponseWriter, r *http.Request, id adminopenapi.RepositoryId) {
	h.withRepositoryScope(w, r, id.String(), RepositoryRead, func(_ Principal, repo repository.HostedRepository) {
		artifactScanning := h.artifactScanner != nil && scanFormatEnabled(h.artifactScanFormats, repo.Format) && scanRepositoryAssetsAvailable(repo)
		publicationScanning := artifactScanning && publicationScanSupported(repo)
		writeNativeMavenJSON(w, http.StatusOK, repositoryCapabilities(repo.Format, repo.Type, artifactScanning, publicationScanning))
	})
}

func (h generatedRepositoryAPIAdapter) ListFormatProfiles(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.authorize(w, r); !ok {
		return
	}
	profiles := repository.SupportedFormatProfiles()
	items := make([]adminopenapi.FormatProfile, 0, len(profiles))
	for _, profile := range profiles {
		items = append(items, formatProfileResponse(profile))
	}
	writeNativeMavenJSON(w, http.StatusOK, adminopenapi.FormatProfileList{Items: items})
}

func (h generatedRepositoryAPIAdapter) withRepositoryScope(w http.ResponseWriter, r *http.Request, repositoryID string, operation RepositoryOperation, handler func(Principal, repository.HostedRepository)) {
	principal, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	repo, err := h.store.GetHostedRepository(r.Context(), repositoryID)
	if errors.Is(err, repository.ErrNotFound) {
		writeHostedProblem(w, http.StatusNotFound, "not_found", "repository not found")
		return
	}
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "get repository failed")
		return
	}
	if decision := h.authorizer.Authorize(r.Context(), principal, repo, operation); !decision.Allowed {
		h.recordAuthorizationDenial(r, principal, repo, operation, decision)
		writeHostedProblem(w, http.StatusForbidden, "access_denied", "repository scope is required")
		return
	}
	handler(principal, repo)
}

func (h generatedRepositoryAPIAdapter) withRepositoryBrowseScope(w http.ResponseWriter, r *http.Request, repositoryID string, handler func(Principal, repository.HostedRepository)) {
	principal, authenticated := h.authenticator.Authenticate(r.Header.Get("Authorization"))
	repo, err := h.store.GetHostedRepository(r.Context(), repositoryID)
	if errors.Is(err, repository.ErrNotFound) {
		writeHostedProblem(w, http.StatusNotFound, "not_found", "repository not found")
		return
	}
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "get repository failed")
		return
	}
	if !authenticated {
		if anonymousHostedRepositoryReadAllowed(r.Context(), h.store, repo, r.Method) {
			handler(anonymousPrincipal(), repo)
			return
		}
		writeHostedProblem(w, http.StatusUnauthorized, "access_denied", "authentication is required")
		return
	}
	if decision := h.authorizer.Authorize(r.Context(), principal, repo, RepositoryRead); !decision.Allowed {
		h.recordAuthorizationDenial(r, principal, repo, RepositoryRead, decision)
		writeHostedProblem(w, http.StatusForbidden, "access_denied", "repository scope is required")
		return
	}
	handler(principal, repo)
}

func (h generatedRepositoryAPIAdapter) withSessionScope(w http.ResponseWriter, r *http.Request, sessionID string, operation RepositoryOperation, handler func(Principal)) {
	principal, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	session, err := h.sessions.store.GetMavenPublishSession(r.Context(), sessionID)
	if errors.Is(err, repository.ErrNotFound) {
		writeHostedProblem(w, http.StatusNotFound, "not_found", "publish session not found")
		return
	}
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "get publish session failed")
		return
	}
	h.withRepositoryScopeForPrincipal(w, r, principal, session.RepositoryID, operation, handler)
}

func (h generatedRepositoryAPIAdapter) withRepositoryScopeForPrincipal(w http.ResponseWriter, r *http.Request, principal Principal, repositoryID string, operation RepositoryOperation, handler func(Principal)) {
	repo, err := h.store.GetHostedRepository(r.Context(), repositoryID)
	if errors.Is(err, repository.ErrNotFound) {
		writeHostedProblem(w, http.StatusNotFound, "not_found", "repository not found")
		return
	}
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "get repository failed")
		return
	}
	if decision := h.authorizer.Authorize(r.Context(), principal, repo, operation); !decision.Allowed {
		h.recordAuthorizationDenial(r, principal, repo, operation, decision)
		writeHostedProblem(w, http.StatusForbidden, "access_denied", "repository scope is required")
		return
	}
	handler(principal)
}

func (h generatedRepositoryAPIAdapter) recordAuthorizationDenial(r *http.Request, principal Principal, repo repository.HostedRepository, operation RepositoryOperation, decision AuthorizationDecision) {
	if h.metrics != nil {
		h.metrics.recordRepositoryAuthorizationDenied("management", decision.Source, decision.Reason)
	}
	if h.audit == nil {
		return
	}
	_ = h.audit.RecordAudit(r.Context(), repository.AuditRecord{
		GroupName: repo.Name, Repository: repo.Name, Actor: principal.Actor, Outcome: repository.AuditAccessDenied, OccurredAt: time.Now().UTC(),
		Format: "management", Resource: "repositories/" + repo.ID, Operation: string(operation), Status: http.StatusForbidden, CacheDisposition: "bypass",
		AuthorizationSource: decision.Source, AuthorizationReason: decision.Reason,
	})
}

func (h hostedRepositoryAPIHandler) create(w http.ResponseWriter, r *http.Request, principal Principal) {
	h.createWithIdempotencyKey(w, r, principal, r.Header.Get("Idempotency-Key"))
}

func (h hostedRepositoryAPIHandler) createWithIdempotencyKey(w http.ResponseWriter, r *http.Request, principal Principal, key string) {
	key = strings.TrimSpace(key)
	if key == "" || len(key) > 128 {
		writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "Idempotency-Key is required and must be at most 128 characters")
		return
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	decoder.DisallowUnknownFields()
	var request createHostedRepositoryRequest
	if err := decoder.Decode(&request); err != nil || !validHostedRepository(request) {
		writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "name, format, type, endpoint, and allowedHosts must be valid")
		return
	}
	repoType := repository.RepositoryTypeHosted
	if request.Type != "" {
		repoType = repository.RepositoryType(request.Type)
	}
	if repoType == repository.RepositoryTypeHosted && request.EgressProxy != nil {
		writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "hosted repositories do not accept egressProxy")
		return
	}
	egressProxy, err := resolveEgressProxy(request.EgressProxy, nil)
	if errors.Is(err, egress.ErrKeyNotConfigured) {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "egress proxy encryption key is not configured")
		return
	}
	if err != nil {
		writeHostedProblem(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	payload, _ := json.Marshal(request)
	digest := sha256.Sum256(payload)
	repo, _, err := h.store.CreateHostedRepositoryIdempotently(r.Context(), repository.HostedRepository{ID: uuid.NewString(), Name: request.Name, Format: request.Format, Type: repoType, Endpoint: request.Endpoint, AllowedHosts: request.AllowedHosts, EgressProxy: egressProxy, AnonymousRead: request.AnonymousRead}, principal.Actor, key, base64.RawURLEncoding.EncodeToString(digest[:]))
	if errors.Is(err, repository.ErrIdempotencyConflict) {
		writeHostedProblem(w, http.StatusConflict, "idempotency_conflict", "Idempotency-Key was already used with a different request")
		return
	}
	if errors.Is(err, repository.ErrNameExists) {
		writeHostedProblem(w, http.StatusConflict, "version_conflict", "repository name already exists")
		return
	}
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "create repository failed")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	// Returning the same documented response on a successful replay makes a
	// lost client response safe to retry without introducing another outcome.
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(redactEgressProxy(repo))
}

func (h hostedRepositoryAPIHandler) list(w http.ResponseWriter, r *http.Request) {
	params := adminopenapi.ListRepositoriesParams{}
	if raw := r.URL.Query().Get("pageSize"); raw != "" {
		pageSize, err := strconv.Atoi(raw)
		if err != nil {
			writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "pageSize must be between 1 and 200")
			return
		}
		params.PageSize = &pageSize
	}
	if token := r.URL.Query().Get("pageToken"); token != "" {
		params.PageToken = &token
	}
	h.listBound(w, r, params)
}

func (h hostedRepositoryAPIHandler) listBound(w http.ResponseWriter, r *http.Request, params adminopenapi.ListRepositoriesParams) {
	pageSize := 50
	if params.PageSize != nil {
		pageSize = int(*params.PageSize)
		if pageSize < 1 || pageSize > 200 {
			writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "pageSize must be between 1 and 200")
			return
		}
	}
	pageToken := ""
	if params.PageToken != nil {
		pageToken = string(*params.PageToken)
	}
	after, err := h.decodeCursor(pageToken)
	if err != nil {
		writeHostedProblem(w, http.StatusBadRequest, "invalid_page_token", "page token is invalid or expired")
		return
	}
	items, next, err := h.store.ListHostedRepositories(r.Context(), pageSize, after)
	if errors.Is(err, repository.ErrNotFound) {
		writeHostedProblem(w, http.StatusBadRequest, "invalid_page_token", "page token is invalid")
		return
	}
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "list repositories failed")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	nextToken := ""
	if next != "" {
		nextToken = h.encodeCursor(next)
	}
	for index := range items {
		items[index] = redactEgressProxy(items[index])
	}
	_ = json.NewEncoder(w).Encode(repositoryPage{Items: items, NextPageToken: nextToken})
}

func (h hostedRepositoryAPIHandler) encodeCursor(id string) string {
	return encodeSignedCursor(h.authenticator.AdminToken, repositoryPageCursor{Endpoint: "repositories", ID: id, ExpiresAt: time.Now().UTC().Add(15 * time.Minute).Unix()})
}

func (h hostedRepositoryAPIHandler) decodeCursor(token string) (string, error) {
	if token == "" {
		return "", nil
	}
	var cursor repositoryPageCursor
	if decodeSignedCursor(h.authenticator.AdminToken, token, &cursor) != nil || cursor.Endpoint != "repositories" || cursor.ID == "" || time.Now().UTC().Unix() >= cursor.ExpiresAt {
		return "", errors.New("invalid cursor")
	}
	return cursor.ID, nil
}

func (h hostedRepositoryAPIHandler) get(w http.ResponseWriter, r *http.Request, id string) {
	repo, err := h.store.GetHostedRepository(r.Context(), id)
	if errors.Is(err, repository.ErrNotFound) {
		writeHostedProblem(w, http.StatusNotFound, "not_found", "repository not found")
		return
	}
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "get repository failed")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(redactEgressProxy(repo))
}

func (h hostedRepositoryAPIHandler) disable(w http.ResponseWriter, r *http.Request, id string) {
	_, err := h.store.DisableHostedRepository(r.Context(), id)
	if errors.Is(err, repository.ErrNotFound) {
		writeHostedProblem(w, http.StatusNotFound, "not_found", "repository not found or already disabled")
		return
	}
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "disable repository failed")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]string{"id": id, "state": "pending"})
}

// update applies mutable management policy and proxy configuration. Protocol
// handlers do not consume anonymousRead yet, so this only changes management
// state until the protocol slice is implemented.
func (h hostedRepositoryAPIHandler) update(w http.ResponseWriter, r *http.Request, repo repository.HostedRepository, ifMatch string) {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	decoder.DisallowUnknownFields()
	var request updateHostedRepositoryRequest
	if err := decoder.Decode(&request); err != nil {
		writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "repository update body must be valid")
		return
	}
	updatedRepo := repository.HostedRepository{ID: repo.ID, Endpoint: repo.Endpoint, AllowedHosts: append([]string(nil), repo.AllowedHosts...), EgressProxy: repo.EgressProxy, AnonymousRead: repo.AnonymousRead}
	if request.AnonymousRead != nil {
		updatedRepo.AnonymousRead = *request.AnonymousRead
	}
	if repo.Type == repository.RepositoryTypeProxy {
		if request.Endpoint != nil {
			updatedRepo.Endpoint = *request.Endpoint
		}
		if request.AllowedHosts != nil {
			updatedRepo.AllowedHosts = request.AllowedHosts
		}
		if !validProxyConfiguration(repo.Format, updatedRepo.Endpoint, updatedRepo.AllowedHosts) {
			writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "endpoint must be a valid https URL and required allowedHosts must be valid hostnames")
			return
		}
		egressProxy, err := resolveEgressProxy(request.EgressProxy, repo.EgressProxy)
		if errors.Is(err, egress.ErrKeyNotConfigured) {
			writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "egress proxy encryption key is not configured")
			return
		}
		if err != nil {
			writeHostedProblem(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		updatedRepo.EgressProxy = egressProxy
	} else {
		if request.Endpoint != nil || request.AllowedHosts != nil || request.EgressProxy != nil {
			writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "hosted repositories only support anonymousRead updates")
			return
		}
		if request.AnonymousRead == nil {
			writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "anonymousRead is required for hosted repository updates")
			return
		}
	}
	updated, err := h.store.UpdateHostedRepository(r.Context(), updatedRepo, ifMatch)
	if errors.Is(err, repository.ErrNotFound) {
		writeHostedProblem(w, http.StatusNotFound, "not_found", "repository not found")
		return
	}
	if errors.Is(err, repository.ErrVersionConflict) {
		writeHostedProblem(w, http.StatusPreconditionFailed, "version_conflict", "If-Match does not match current version")
		return
	}
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "update repository failed")
		return
	}
	w.Header().Set("ETag", updated.Version)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(redactEgressProxy(updated))
}

func validHostedRepository(request createHostedRepositoryRequest) bool {
	if !hostedRepositoryName.MatchString(request.Name) {
		return false
	}
	repoType := request.Type
	if repoType == "" {
		repoType = string(repository.RepositoryTypeHosted)
	}
	repositoryType := repository.RepositoryType(repoType)
	if !repository.FormatSupportsRepositoryProvisioning(request.Format, repositoryType) {
		return false
	}
	switch repositoryType {
	case repository.RepositoryTypeHosted:
		// Hosted repositories serve local content only; an upstream endpoint or
		// egress allow-list would be meaningless and likely a client mistake.
		return request.Endpoint == "" && len(request.AllowedHosts) == 0
	case repository.RepositoryTypeProxy:
		return validProxyConfiguration(request.Format, request.Endpoint, request.AllowedHosts)
	default:
		return false
	}
}

func validProxyEndpoint(endpoint string) bool {
	parsed, err := url.Parse(endpoint)
	return err == nil && parsed.Scheme == "https" && parsed.Host != "" && parsed.User == nil && parsed.RawQuery == "" && parsed.Fragment == ""
}

func validProxyConfiguration(format repository.Format, endpoint string, allowedHosts []string) bool {
	if !validProxyEndpoint(endpoint) {
		return false
	}
	if proxyAllowedHostsRequired(format) && len(allowedHosts) == 0 {
		return false
	}
	for _, allowedHost := range allowedHosts {
		parsed, err := url.Parse("//" + strings.TrimSpace(allowedHost))
		if err != nil || allowedHost != strings.TrimSpace(allowedHost) || parsed.Hostname() == "" || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Port() != "" {
			return false
		}
	}
	return true
}

func proxyAllowedHostsRequired(format repository.Format) bool {
	switch format {
	case repository.FormatRaw, repository.FormatConan, repository.FormatNPM, repository.FormatPyPI, repository.FormatGo, repository.FormatAPT:
		return true
	default:
		return false
	}
}

func writeHostedProblem(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"type": "about:blank", "title": http.StatusText(status), "status": status, "code": code, "message": message, "requestId": ""})
}
