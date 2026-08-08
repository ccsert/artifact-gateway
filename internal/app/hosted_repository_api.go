package app

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	adminopenapi "github.com/artifact-gateway/artifact-gateway/internal/admin/openapi"
	"github.com/artifact-gateway/artifact-gateway/internal/authorization"
	"github.com/artifact-gateway/artifact-gateway/internal/egress"
	conanprotocol "github.com/artifact-gateway/artifact-gateway/internal/protocol/conan"
	mavenprotocol "github.com/artifact-gateway/artifact-gateway/internal/protocol/maven"
	ociprotocol "github.com/artifact-gateway/artifact-gateway/internal/protocol/oci"
	rawprotocol "github.com/artifact-gateway/artifact-gateway/internal/protocol/raw"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
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
	if !ok || !principal.Admin {
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
	if len(parts) == 2 && (parts[1] == "artifacts" || parts[1] == "artifact-search") {
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
	sessions          nativeMavenHandler
	groups            repository.HostedGroupStore
	grants            repository.RepositoryGrantStore
	retentionPolicies repository.RepositoryRetentionPolicyStore
	capacities        repository.RepositoryCapacityStore
	tombstones        repository.ArtifactTombstoneStore
	lifecycleJobs     repository.LifecycleJobStore
	auditRetention    repository.AuditRetentionStore
	anonymousAccess   repository.AnonymousAccessPolicyStore
	replication       repository.ReplicationStore
	oci               repository.NativeOCIStore
	conan             repository.NativeConanStore
	apiKeys           repository.APIKeyStore
	users             repository.UserStore
	authorizer        RepositoryAuthorizer
	audit             repository.Store
	metrics           *Metrics
	maintenance       *CacheMaintenance
	proxyCache        proxyCacheBrowseHandler
	mavenProxy        mavenProxyOperationsHandler
	searchProjection  repository.ArtifactSearchStore
	runtimeNodes      repository.RuntimeNodeStore
}

var _ adminopenapi.ServerInterface = generatedRepositoryAPIAdapter{}

func (h generatedRepositoryAPIAdapter) ListApiKeys(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.authorize(w, r); !ok {
		return
	}
	keys, err := h.apiKeys.ListAPIKeys(r.Context())
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "list API keys failed")
		return
	}
	items := make([]adminopenapi.APIKey, 0, len(keys))
	for _, key := range keys {
		items = append(items, apiKeyResponse(key))
	}
	writeNativeMavenJSON(w, http.StatusOK, adminopenapi.APIKeyList{Items: items})
}

func (h generatedRepositoryAPIAdapter) CreateApiKey(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.authorize(w, r); !ok {
		return
	}
	var request adminopenapi.CreateAPIKey
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil || strings.TrimSpace(request.Name) == "" || len(request.Roles) == 0 {
		writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "name and at least one role are required")
		return
	}
	now := time.Now().UTC()
	expiresAt := now.Add(90 * 24 * time.Hour)
	if request.ExpiresAt != nil {
		expiresAt = request.ExpiresAt.UTC()
	}
	if !expiresAt.After(now) || expiresAt.After(now.Add(365*24*time.Hour)) {
		writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "API key expiry must be in the future and no more than 365 days away")
		return
	}
	roles := make([]string, 0, len(request.Roles))
	for _, role := range request.Roles {
		switch role {
		case adminopenapi.CreateAPIKeyRolesAdmin, adminopenapi.CreateAPIKeyRolesWriter, adminopenapi.CreateAPIKeyRolesReader:
		default:
			writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "unsupported API key role")
			return
		}
		roles = append(roles, string(role))
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "generate API key failed")
		return
	}
	token := "agk_" + base64.RawURLEncoding.EncodeToString(raw)
	key, err := h.apiKeys.CreateAPIKey(r.Context(), repository.APIKey{ID: uuid.NewString(), Name: strings.TrimSpace(request.Name), SecretHash: authorization.HashAPIKey(token), Roles: roles, ExpiresAt: &expiresAt})
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "create API key failed")
		return
	}
	response := adminopenapi.CreatedAPIKey{Id: uuid.MustParse(key.ID), Name: key.Name, CreatedAt: key.CreatedAt, ExpiresAt: key.ExpiresAt, LastUsedAt: key.LastUsedAt, Token: token, Roles: make([]adminopenapi.CreatedAPIKeyRoles, 0, len(key.Roles))}
	for _, role := range key.Roles {
		response.Roles = append(response.Roles, adminopenapi.CreatedAPIKeyRoles(role))
	}
	writeNativeMavenJSON(w, http.StatusCreated, response)
}

func (h generatedRepositoryAPIAdapter) RevokeApiKey(w http.ResponseWriter, r *http.Request, apiKeyID uuid.UUID) {
	if _, ok := h.authorize(w, r); !ok {
		return
	}
	key, err := h.apiKeys.RevokeAPIKey(r.Context(), apiKeyID.String())
	if errors.Is(err, repository.ErrNotFound) {
		writeHostedProblem(w, http.StatusNotFound, "not_found", "API key not found")
		return
	}
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "revoke API key failed")
		return
	}
	writeNativeMavenJSON(w, http.StatusOK, apiKeyResponse(key))
}

func apiKeyResponse(key repository.APIKey) adminopenapi.APIKey {
	response := adminopenapi.APIKey{Id: uuid.MustParse(key.ID), Name: key.Name, CreatedAt: key.CreatedAt, RevokedAt: key.RevokedAt, ExpiresAt: key.ExpiresAt, LastUsedAt: key.LastUsedAt, Roles: make([]adminopenapi.APIKeyRoles, 0, len(key.Roles))}
	for _, role := range key.Roles {
		response.Roles = append(response.Roles, adminopenapi.APIKeyRoles(role))
	}
	return response
}

func userResponse(user repository.User) adminopenapi.User {
	response := adminopenapi.User{CreatedAt: user.CreatedAt, Id: uuid.MustParse(user.ID), Name: user.Name, Role: adminopenapi.UserRole(user.Role), State: adminopenapi.UserState(user.State), Version: user.Version}
	if !user.UpdatedAt.IsZero() {
		updated := user.UpdatedAt
		response.UpdatedAt = &updated
	}
	return response
}

func validUserRole(role string) bool {
	return role == string(authorization.RoleAdmin) || role == string(authorization.RoleWriter) || role == string(authorization.RoleReader)
}

func (h generatedRepositoryAPIAdapter) ListUsers(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.authorize(w, r); !ok {
		return
	}
	users, err := h.users.ListUsers(r.Context())
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "list users failed")
		return
	}
	items := make([]adminopenapi.User, 0, len(users))
	for _, user := range users {
		items = append(items, userResponse(user))
	}
	writeNativeMavenJSON(w, http.StatusOK, adminopenapi.UserList{Items: items})
}

func (h generatedRepositoryAPIAdapter) CreateUser(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.authorize(w, r); !ok {
		return
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	decoder.DisallowUnknownFields()
	var request adminopenapi.CreateUser
	if err := decoder.Decode(&request); err != nil || strings.TrimSpace(request.Name) == "" || len(request.Password) < 8 || !validUserRole(string(request.Role)) {
		writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "name, a password of at least 8 characters, and a valid role are required")
		return
	}
	hash, err := authorization.HashPassword(request.Password)
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "hash password failed")
		return
	}
	user, err := h.users.CreateUser(r.Context(), repository.User{ID: uuid.NewString(), Name: strings.TrimSpace(request.Name), SecretHash: hash, Role: string(request.Role)})
	if errors.Is(err, repository.ErrNameExists) {
		writeHostedProblem(w, http.StatusConflict, "version_conflict", "user name already exists")
		return
	}
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "create user failed")
		return
	}
	writeNativeMavenJSON(w, http.StatusCreated, userResponse(user))
}

func (h generatedRepositoryAPIAdapter) GetUser(w http.ResponseWriter, r *http.Request, userID string) {
	if _, ok := h.authorize(w, r); !ok {
		return
	}
	user, err := h.users.GetUser(r.Context(), userID)
	if errors.Is(err, repository.ErrNotFound) {
		writeHostedProblem(w, http.StatusNotFound, "not_found", "user not found")
		return
	}
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "get user failed")
		return
	}
	writeNativeMavenJSON(w, http.StatusOK, userResponse(user))
}

func (h generatedRepositoryAPIAdapter) UpdateUser(w http.ResponseWriter, r *http.Request, userID string, params adminopenapi.UpdateUserParams) {
	if _, ok := h.authorize(w, r); !ok {
		return
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10))
	decoder.DisallowUnknownFields()
	var request adminopenapi.UpdateUser
	if err := decoder.Decode(&request); err != nil {
		writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "invalid user update body")
		return
	}
	role := ""
	if request.Role != nil {
		role = string(*request.Role)
	}
	if role != "" && !validUserRole(role) {
		writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "unsupported user role")
		return
	}
	state := ""
	if request.State != nil {
		state = string(*request.State)
	}
	if state != "" && state != repository.UserActive && state != repository.UserDisabled {
		writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "unsupported user state")
		return
	}
	if role == "" && state == "" {
		writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "provide a role or state to update")
		return
	}
	updated, err := h.users.UpdateUser(r.Context(), repository.User{ID: userID, Role: role, State: state}, string(params.IfMatch))
	if errors.Is(err, repository.ErrNotFound) {
		writeHostedProblem(w, http.StatusNotFound, "not_found", "user not found")
		return
	}
	if errors.Is(err, repository.ErrVersionConflict) {
		writeHostedProblem(w, http.StatusPreconditionFailed, "version_conflict", "If-Match does not match current version")
		return
	}
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "update user failed")
		return
	}
	writeNativeMavenJSON(w, http.StatusOK, userResponse(updated))
}

func (h generatedRepositoryAPIAdapter) DeleteUser(w http.ResponseWriter, r *http.Request, userID string) {
	if _, ok := h.authorize(w, r); !ok {
		return
	}
	if err := h.users.DeleteUser(r.Context(), userID); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			writeHostedProblem(w, http.StatusNotFound, "not_found", "user not found")
			return
		}
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "delete user failed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h generatedRepositoryAPIAdapter) ListRepositories(w http.ResponseWriter, r *http.Request, params adminopenapi.ListRepositoriesParams) {
	if _, ok := h.authorize(w, r); ok {
		h.listBound(w, r, params)
	}
}

func (h generatedRepositoryAPIAdapter) ListAudits(w http.ResponseWriter, r *http.Request, params adminopenapi.ListAuditsParams) {
	if _, ok := h.authorize(w, r); !ok {
		return
	}
	limit := 100
	if params.Limit != nil {
		limit = *params.Limit
	}
	if limit < 1 || limit > 500 {
		writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "limit must be between 1 and 500")
		return
	}
	query := repository.AuditQuery{Limit: limit}
	if params.Group != nil {
		query.GroupName = *params.Group
	}
	if params.Repository != nil {
		query.Repository = *params.Repository
	}
	if params.Outcome != nil {
		query.Outcome = *params.Outcome
	}
	if params.Format != nil {
		query.Format = *params.Format
	}
	if params.Operation != nil {
		query.Operation = *params.Operation
	}
	if params.Actor != nil {
		query.Actor = *params.Actor
	}
	audits, err := h.audit.ListAudits(r.Context(), query)
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "list audits failed")
		return
	}
	response := make([]auditResponse, 0, len(audits))
	for _, audit := range audits {
		response = append(response, auditResponseFromRecord(audit))
	}
	writeNativeMavenJSON(w, http.StatusOK, response)
}

func (h generatedRepositoryAPIAdapter) ListAuditPage(w http.ResponseWriter, r *http.Request, params adminopenapi.ListAuditPageParams) {
	if _, ok := h.authorize(w, r); !ok {
		return
	}
	pageSize := 100
	if params.PageSize != nil {
		pageSize = int(*params.PageSize)
	}
	if pageSize < 1 || pageSize > 200 {
		writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "pageSize must be between 1 and 200")
		return
	}
	from := time.Time{}
	if params.From != nil {
		from = params.From.UTC()
	}
	to := time.Time{}
	if params.To != nil {
		to = params.To.UTC()
	}
	if !from.IsZero() && !to.IsZero() && from.After(to) {
		writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "from must be before or equal to to")
		return
	}
	query := repository.AuditQuery{
		GroupName: auditString(params.Group), Repository: auditString(params.Repository), Outcome: auditString(params.Outcome),
		Format: auditString(params.Format), Operation: auditString(params.Operation), Actor: auditString(params.Actor), Limit: pageSize,
		From: from, To: to,
	}
	if token := auditToken(params.PageToken); token != "" {
		var cursor auditPageCursor
		if decodeSignedCursor(h.authenticator.AdminToken, token, &cursor) != nil || cursor.Endpoint != "audits" || time.Now().UTC().Unix() >= cursor.ExpiresAt ||
			cursor.GroupName != query.GroupName || cursor.Repository != query.Repository || cursor.Outcome != query.Outcome || cursor.Format != query.Format ||
			cursor.Operation != query.Operation || cursor.Actor != query.Actor || !cursor.From.Equal(query.From) || !cursor.To.Equal(query.To) || cursor.OccurredAt.IsZero() || cursor.ID <= 0 {
			writeHostedProblem(w, http.StatusBadRequest, "invalid_page_token", "page token is invalid or expired")
			return
		}
		query.Before = repository.AuditCursor{OccurredAt: cursor.OccurredAt, ID: cursor.ID}
	}
	pageStore, ok := h.audit.(repository.AuditPageStore)
	if !ok {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "audit cursor paging is unavailable")
		return
	}
	page, err := pageStore.ListAuditPage(r.Context(), query)
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "list audit page failed")
		return
	}
	response := auditPageResponse{Items: make([]auditResponse, 0, len(page.Items))}
	for _, audit := range page.Items {
		response.Items = append(response.Items, auditResponseFromRecord(audit))
	}
	if page.Next != nil {
		response.NextPageToken = h.encodeAuditCursor(query, *page.Next)
	}
	writeNativeMavenJSON(w, http.StatusOK, response)
}

func (h generatedRepositoryAPIAdapter) GetAnonymousAccessPolicy(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.authorize(w, r); !ok {
		return
	}
	policy, err := h.anonymousAccess.GetAnonymousAccessPolicy(r.Context())
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "get anonymous access policy failed")
		return
	}
	writeNativeMavenJSON(w, http.StatusOK, anonymousAccessPolicyResponse(policy))
}

func (h generatedRepositoryAPIAdapter) ReplaceAnonymousAccessPolicy(w http.ResponseWriter, r *http.Request, params adminopenapi.ReplaceAnonymousAccessPolicyParams) {
	if _, ok := h.authorize(w, r); !ok {
		return
	}
	var request adminopenapi.AnonymousAccessPolicy
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil || request.Version == "" {
		writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "version and enabled are required")
		return
	}
	policy, err := h.anonymousAccess.ReplaceAnonymousAccessPolicy(r.Context(), repository.AnonymousAccessPolicy{Enabled: request.Enabled}, string(params.IfMatch))
	if errors.Is(err, repository.ErrVersionConflict) {
		writeHostedProblem(w, http.StatusPreconditionFailed, "version_conflict", "If-Match does not match current version")
		return
	}
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "replace anonymous access policy failed")
		return
	}
	writeNativeMavenJSON(w, http.StatusOK, anonymousAccessPolicyResponse(policy))
}

func anonymousAccessPolicyResponse(policy repository.AnonymousAccessPolicy) adminopenapi.AnonymousAccessPolicy {
	return adminopenapi.AnonymousAccessPolicy{Enabled: policy.Enabled, Version: policy.Version}
}

func (h generatedRepositoryAPIAdapter) GetAuditRetentionPolicy(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.authorize(w, r); !ok {
		return
	}
	p, err := h.auditRetention.GetAuditRetentionPolicy(r.Context())
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "get audit retention policy failed")
		return
	}
	writeNativeMavenJSON(w, http.StatusOK, adminopenapi.AuditRetentionPolicy{Version: p.Version, Enabled: p.Enabled, KeepDays: p.KeepDays})
}

func (h generatedRepositoryAPIAdapter) ReplaceAuditRetentionPolicy(w http.ResponseWriter, r *http.Request, params adminopenapi.ReplaceAuditRetentionPolicyParams) {
	if _, ok := h.authorize(w, r); !ok {
		return
	}
	var request adminopenapi.AuditRetentionPolicy
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil || request.Version == "" || request.KeepDays < 0 || request.KeepDays > 36500 || (request.Enabled && request.KeepDays < 1) {
		writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "version, enabled, and keepDays must be valid")
		return
	}
	p, err := h.auditRetention.ReplaceAuditRetentionPolicy(r.Context(), repository.AuditRetentionPolicy{Version: request.Version, Enabled: request.Enabled, KeepDays: request.KeepDays}, string(params.IfMatch))
	if errors.Is(err, repository.ErrVersionConflict) {
		writeHostedProblem(w, http.StatusPreconditionFailed, "version_conflict", "If-Match does not match current version")
		return
	}
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "replace audit retention policy failed")
		return
	}
	writeNativeMavenJSON(w, http.StatusOK, adminopenapi.AuditRetentionPolicy{Version: p.Version, Enabled: p.Enabled, KeepDays: p.KeepDays})
}

func (h generatedRepositoryAPIAdapter) ExecuteAuditRetention(w http.ResponseWriter, r *http.Request, params adminopenapi.ExecuteAuditRetentionParams) {
	if _, ok := h.authorize(w, r); !ok {
		return
	}
	p, err := h.auditRetention.GetAuditRetentionPolicy(r.Context())
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "get audit retention policy failed")
		return
	}
	if !p.Enabled {
		writeHostedProblem(w, http.StatusConflict, "retention_disabled", "audit retention is disabled")
		return
	}
	job, _, err := (AuditRetentionWorker{Store: h.auditRetention}).Enqueue(r.Context(), string(params.IdempotencyKey), 1000)
	if errors.Is(err, repository.ErrIdempotencyConflict) {
		writeHostedProblem(w, http.StatusConflict, "idempotency_conflict", "Idempotency-Key conflicts with an existing audit cleanup job")
		return
	}
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "enqueue audit cleanup job failed")
		return
	}
	writeNativeMavenJSON(w, http.StatusAccepted, auditCleanupJobResponse(job))
}

func (h generatedRepositoryAPIAdapter) ListAuditRetentionJobs(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.authorize(w, r); !ok {
		return
	}
	jobs, err := h.auditRetention.ListAuditCleanupJobs(r.Context(), 100)
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "list audit cleanup jobs failed")
		return
	}
	response := make([]adminopenapi.AuditCleanupJob, 0, len(jobs))
	for _, job := range jobs {
		response = append(response, auditCleanupJobResponse(job))
	}
	writeNativeMavenJSON(w, http.StatusOK, response)
}

func auditCleanupJobResponse(job repository.AuditCleanupJob) adminopenapi.AuditCleanupJob {
	response := adminopenapi.AuditCleanupJob{Id: uuid.MustParse(job.ID), PolicyVersion: job.PolicyVersion, CutoffAt: job.CutoffAt, BatchSize: job.BatchSize, Deleted: job.Deleted, State: adminopenapi.AuditCleanupJobState(job.State), CreatedAt: job.CreatedAt}
	if !job.StartedAt.IsZero() {
		response.StartedAt = &job.StartedAt
	}
	if !job.CompletedAt.IsZero() {
		response.CompletedAt = &job.CompletedAt
	}
	if job.LastError != "" {
		response.LastError = &job.LastError
	}
	return response
}

// auditResponse is the V2 audit representation. V1 keeps returning the
// historical repository.AuditRecord JSON field names for compatibility.
type auditResponse struct {
	GroupName           string                  `json:"groupName,omitempty"`
	Repository          string                  `json:"repository,omitempty"`
	MemberName          string                  `json:"memberName,omitempty"`
	Outcome             repository.AuditOutcome `json:"outcome"`
	Actor               string                  `json:"actor,omitempty"`
	OccurredAt          time.Time               `json:"occurredAt"`
	Format              string                  `json:"format,omitempty"`
	Resource            string                  `json:"resource,omitempty"`
	Representation      string                  `json:"representation,omitempty"`
	MemberType          string                  `json:"memberType,omitempty"`
	UpstreamHost        string                  `json:"upstreamHost,omitempty"`
	Operation           string                  `json:"operation,omitempty"`
	Status              int                     `json:"status,omitempty"`
	CacheDisposition    string                  `json:"cacheDisposition,omitempty"`
	Bytes               int64                   `json:"bytes,omitempty"`
	AuthorizationSource string                  `json:"authorizationSource,omitempty"`
	AuthorizationReason string                  `json:"authorizationReason,omitempty"`
	RequestID           string                  `json:"requestId,omitempty"`
	TraceID             string                  `json:"traceId,omitempty"`
}

type auditPageResponse struct {
	Items         []auditResponse `json:"items"`
	NextPageToken string          `json:"nextPageToken,omitempty"`
}

func auditString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func auditToken(value *adminopenapi.PageToken) string {
	if value == nil {
		return ""
	}
	return string(*value)
}

func (h generatedRepositoryAPIAdapter) encodeAuditCursor(query repository.AuditQuery, cursor repository.AuditCursor) string {
	return encodeSignedCursor(h.authenticator.AdminToken, auditPageCursor{
		Endpoint: "audits", GroupName: query.GroupName, Repository: query.Repository, Outcome: query.Outcome,
		Format: query.Format, Operation: query.Operation, Actor: query.Actor, From: query.From, To: query.To,
		OccurredAt: cursor.OccurredAt, ID: cursor.ID, ExpiresAt: time.Now().UTC().Add(15 * time.Minute).Unix(),
	})
}

func auditResponseFromRecord(audit repository.AuditRecord) auditResponse {
	return auditResponse{
		GroupName: audit.GroupName, Repository: audit.Repository, MemberName: audit.MemberName, Outcome: audit.Outcome, Actor: audit.Actor, OccurredAt: audit.OccurredAt,
		Format: audit.Format, Resource: audit.Resource, Representation: audit.Representation, MemberType: audit.MemberType, UpstreamHost: audit.UpstreamHost,
		Operation: audit.Operation, CacheDisposition: audit.CacheDisposition, AuthorizationSource: audit.AuthorizationSource, AuthorizationReason: audit.AuthorizationReason,
		RequestID: audit.RequestID, TraceID: audit.TraceID, Status: audit.Status, Bytes: audit.Bytes,
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
		writeNativeMavenJSON(w, http.StatusOK, repositoryCapabilities(repo.Format, repo.Type))
	})
}

func (h generatedRepositoryAPIAdapter) GetRepositoryEffectiveAccess(w http.ResponseWriter, r *http.Request, repositoryID adminopenapi.RepositoryId) {
	principal, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	repo, err := h.store.GetHostedRepository(r.Context(), repositoryID.String())
	if errors.Is(err, repository.ErrNotFound) {
		writeHostedProblem(w, http.StatusNotFound, "not_found", "repository not found")
		return
	}
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "get repository failed")
		return
	}
	writeNativeMavenJSON(w, http.StatusOK, h.repositoryEffectiveAccess(r.Context(), principal, repo))
}

func currentIdentityResponse(principal Principal) adminopenapi.CurrentIdentity {
	response := adminopenapi.CurrentIdentity{
		Actor:         principal.Actor,
		Administrator: principal.Admin,
		Kind:          adminopenapi.AuthenticationKind(principal.AuthenticationKind),
	}
	if principal.Role != "" {
		role := adminopenapi.CurrentIdentityRole(principal.Role)
		response.Role = &role
	}
	if principal.AuthenticationKind == authorization.AuthenticationOIDC {
		details := adminopenapi.OIDCIdentityDetails{
			AdminSubject: principal.OIDCAdminSubject,
			RoleMappings: make([]adminopenapi.OIDCRoleMappingMatch, 0, len(principal.OIDCRoleMappings)),
		}
		for _, mapping := range principal.OIDCRoleMappings {
			details.RoleMappings = append(details.RoleMappings, adminopenapi.OIDCRoleMappingMatch{
				ExternalRole: mapping.ExternalRole,
				GatewayRole:  adminopenapi.OIDCRoleMappingMatchGatewayRole(mapping.GatewayRole),
			})
		}
		response.Oidc = &details
	}
	return response
}

func (h generatedRepositoryAPIAdapter) repositoryEffectiveAccess(ctx context.Context, principal Principal, repo repository.HostedRepository) adminopenapi.RepositoryEffectiveAccess {
	decision := func(operation RepositoryOperation) adminopenapi.EffectiveAccessDecision {
		return effectiveAccessDecision(h.authorizer.Authorize(ctx, principal, repo, operation))
	}
	anonymousReason := anonymousRepositoryReason(ctx, h.store, repo)
	response := adminopenapi.RepositoryEffectiveAccess{
		Actor:    principal.Actor,
		Identity: currentIdentityResponse(principal),
		AnonymousRead: adminopenapi.EffectiveAccessDecision{
			Allowed: anonymousReason == "repository_anonymous_read_enabled",
			Source:  "anonymous_policy",
			Reason:  anonymousReason,
		},
		Permissions: adminopenapi.EffectiveAccessPermissions{
			Read:  decision(RepositoryRead),
			Write: decision(RepositoryWrite),
			Admin: decision(RepositoryAdmin),
		},
	}
	response.Repository.Id = uuid.MustParse(repo.ID)
	response.Repository.Name = repo.Name
	response.Repository.Format = adminopenapi.Format(repo.Format)
	response.Repository.Type = adminopenapi.RepositoryEffectiveAccessRepositoryType(repo.Type)
	response.Repository.State = adminopenapi.RepositoryEffectiveAccessRepositoryState(repo.State)
	return response
}

func effectiveAccessDecision(decision AuthorizationDecision) adminopenapi.EffectiveAccessDecision {
	return adminopenapi.EffectiveAccessDecision{Allowed: decision.Allowed, Source: decision.Source, Reason: decision.Reason}
}

func anonymousRepositoryReason(ctx context.Context, source any, repo repository.HostedRepository) string {
	if repo.State != repository.RepositoryActive {
		return "repository_not_active"
	}
	if !anonymousAccessAllowed(ctx, source) {
		return "global_anonymous_access_disabled"
	}
	if !repo.AnonymousRead {
		return "repository_anonymous_read_disabled"
	}
	return "repository_anonymous_read_enabled"
}

func (h generatedRepositoryAPIAdapter) SearchRepositoryArtifacts(w http.ResponseWriter, r *http.Request, repositoryID adminopenapi.RepositoryId, params adminopenapi.SearchRepositoryArtifactsParams) {
	if _, err := h.store.GetHostedRepository(r.Context(), repositoryID.String()); errors.Is(err, repository.ErrNotFound) {
		group, groupErr := h.groups.GetHostedGroup(r.Context(), repositoryID.String())
		if groupErr == nil {
			if !anonymousHostedGroupReadAllowed(r.Context(), h.store, h.store, group, r.Method) {
				writeHostedProblem(w, http.StatusForbidden, "access_denied", "group anonymous read is not enabled")
				return
			}
			h.searchHostedGroupArtifacts(w, r, group, params)
			return
		}
	}
	h.withRepositoryBrowseScope(w, r, repositoryID.String(), func(_ Principal, repo repository.HostedRepository) {
		query := ""
		if params.Q != nil {
			query = *params.Q
		}
		if !validArtifactSearchQuery(repo.Format, query) {
			writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "q is not a valid artifact prefix for this repository format")
			return
		}
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
		after, err := h.decodeArtifactSearchCursor(pageToken, repo.ID, repo.Format, query)
		if err != nil {
			writeHostedProblem(w, http.StatusBadRequest, "invalid_page_token", "page token is invalid or expired")
			return
		}
		items := make([]adminopenapi.ArtifactSummary, 0, pageSize)
		var lastCoordinate string
		hasMore := false
		switch repo.Format {
		case repository.FormatOCI:
			names, err := h.oci.SearchOCIManifestNames(r.Context(), repo.ID, query, pageSize+1, after.Coordinate)
			if err != nil {
				writeHostedProblem(w, 500, "internal_error", "search OCI artifacts failed")
				return
			}
			hasMore = len(names) > pageSize
			if hasMore {
				names = names[:pageSize]
			}
			for _, name := range names {
				items = append(items, adminopenapi.ArtifactSummary{Coordinate: name})
				lastCoordinate = name
			}
		case repository.FormatMaven:
			artifacts, err := h.sessions.store.SearchMavenArtifacts(r.Context(), repo.ID, query, pageSize+1, repository.MavenArtifactCursor{Coordinate: after.Coordinate, BuildNumber: after.BuildNumber})
			if err != nil {
				writeHostedProblem(w, 500, "internal_error", "search Maven artifacts failed")
				return
			}
			hasMore = len(artifacts) > pageSize
			if hasMore {
				artifacts = artifacts[:pageSize]
			}
			for _, a := range artifacts {
				d := a.Digest
				created := a.CreatedAt
				buildNumber := int32(a.BuildNumber)
				items = append(items, adminopenapi.ArtifactSummary{Coordinate: a.Coordinate, Digest: &d, CreatedAt: &created, BuildNumber: &buildNumber, Publisher: optionalPublisher(a.Publisher)})
				lastCoordinate = a.Coordinate
			}
		case repository.FormatConan:
			references, err := h.conan.SearchConanReferences(r.Context(), repo.ID, query, pageSize+1, after.Coordinate)
			if err != nil {
				writeHostedProblem(w, 500, "internal_error", "search Conan artifacts failed")
				return
			}
			hasMore = len(references) > pageSize
			if hasMore {
				references = references[:pageSize]
			}
			for _, reference := range references {
				items = append(items, adminopenapi.ArtifactSummary{Coordinate: reference.Reference, Publisher: optionalPublisher(reference.Publisher)})
				lastCoordinate = reference.Reference
			}
		case repository.FormatRaw:
			assets, err := h.sessions.store.ListRawAssets(r.Context(), repo.ID, query, pageSize+1, after.Coordinate)
			if err != nil {
				writeHostedProblem(w, 500, "internal_error", "search Raw artifacts failed")
				return
			}
			hasMore = len(assets) > pageSize
			if hasMore {
				assets = assets[:pageSize]
			}
			for _, a := range assets {
				d, ct := a.Digest, a.ContentType
				size := a.Size
				updatedAt := a.UpdatedAt
				items = append(items, adminopenapi.ArtifactSummary{Coordinate: a.Path, Digest: &d, ContentType: &ct, Size: &size, CreatedAt: &updatedAt})
				lastCoordinate = a.Path
			}
		}
		var next *string
		if hasMore {
			buildNumber := 0
			if last := items[len(items)-1].BuildNumber; last != nil {
				buildNumber = int(*last)
			}
			token := h.encodeArtifactSearchCursor(repo.ID, repo.Format, query, lastCoordinate, buildNumber)
			next = &token
		}
		writeNativeMavenJSON(w, http.StatusOK, adminopenapi.ArtifactSummaryPage{Items: items, NextPageToken: next})
	})
}

func repositoryCapabilities(format repository.Format, repoType repository.RepositoryType) adminopenapi.RepositoryCapabilities {
	if repoType == repository.RepositoryTypeProxy {
		operations := []adminopenapi.RepositoryCapabilitiesOperations{adminopenapi.RepositoryCapabilitiesOperationsRead, adminopenapi.RepositoryCapabilitiesOperationsBrowse, adminopenapi.RepositoryCapabilitiesOperationsReclaim}
		return adminopenapi.RepositoryCapabilities{Format: adminopenapi.Format(format), Type: adminopenapi.RepositoryCapabilitiesTypeProxy, Operations: operations}
	}
	operations := []adminopenapi.RepositoryCapabilitiesOperations{adminopenapi.RepositoryCapabilitiesOperationsRead, adminopenapi.RepositoryCapabilitiesOperationsPublish, adminopenapi.RepositoryCapabilitiesOperationsBrowse, adminopenapi.RepositoryCapabilitiesOperationsDelete, adminopenapi.RepositoryCapabilitiesOperationsReclaim}
	switch format {
	case repository.FormatMaven:
		operations = append(operations, adminopenapi.RepositoryCapabilitiesOperationsRetain, adminopenapi.RepositoryCapabilitiesOperationsRestore)
	case repository.FormatConan, repository.FormatOCI, repository.FormatRaw:
		operations = append(operations, adminopenapi.RepositoryCapabilitiesOperationsRestore)
	}
	return adminopenapi.RepositoryCapabilities{Format: adminopenapi.Format(format), Type: adminopenapi.RepositoryCapabilitiesTypeHosted, Operations: operations}
}

func (h generatedRepositoryAPIAdapter) ListGrants(w http.ResponseWriter, r *http.Request, repositoryID adminopenapi.RepositoryId) {
	h.withRepositoryScope(w, r, repositoryID.String(), RepositoryAdmin, func(Principal, repository.HostedRepository) {
		set, err := h.grants.GetRepositoryGrants(r.Context(), repositoryID.String())
		if err != nil {
			writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "list grants failed")
			return
		}
		w.Header().Set("ETag", set.Version)
		writeNativeMavenJSON(w, http.StatusOK, set.Grants)
	})
}

func (h generatedRepositoryAPIAdapter) ListRepositoryGrants(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.authorize(w, r); !ok {
		return
	}
	store, ok := h.grants.(repository.RepositoryGrantRecordStore)
	if !ok {
		writeHostedProblem(w, http.StatusNotImplemented, "not_supported", "repository grant aggregation is unavailable")
		return
	}
	records, err := store.ListRepositoryGrantRecords(r.Context())
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "list repository grants failed")
		return
	}
	items := make(adminopenapi.RepositoryGrantRecordList, 0, len(records))
	for _, record := range records {
		item := adminopenapi.RepositoryGrantRecord{
			RepositoryId:   uuid.MustParse(record.RepositoryID),
			RepositoryName: record.RepositoryName,
			Format:         adminopenapi.Format(record.Format),
			Principal:      record.Grant.Principal,
			Scopes:         make([]adminopenapi.RepositoryGrantRecordScopes, 0, len(record.Grant.Scopes)),
		}
		for _, scope := range record.Grant.Scopes {
			item.Scopes = append(item.Scopes, adminopenapi.RepositoryGrantRecordScopes(scope))
		}
		if record.Grant.ResourcePrefix != "" {
			item.ResourcePrefix = &record.Grant.ResourcePrefix
		}
		items = append(items, item)
	}
	writeNativeMavenJSON(w, http.StatusOK, items)
}

func (h generatedRepositoryAPIAdapter) ReplaceGrants(w http.ResponseWriter, r *http.Request, repositoryID adminopenapi.RepositoryId, params adminopenapi.ReplaceGrantsParams) {
	h.withRepositoryScope(w, r, repositoryID.String(), RepositoryAdmin, func(_ Principal, repo repository.HostedRepository) {
		var grants []repository.RepositoryGrant
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&grants); err != nil || !validRepositoryGrants(grants, repo.Format) {
			writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "grants must contain unique principal/resource prefixes, valid scopes, and canonical resource prefixes")
			return
		}
		set, err := h.grants.ReplaceRepositoryGrants(r.Context(), repositoryID.String(), grants, string(params.IfMatch))
		if errors.Is(err, repository.ErrNotFound) {
			writeHostedProblem(w, http.StatusNotFound, "not_found", "repository not found")
			return
		}
		if errors.Is(err, repository.ErrVersionConflict) {
			writeHostedProblem(w, http.StatusPreconditionFailed, "version_conflict", "If-Match does not match current version")
			return
		}
		if err != nil {
			writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "replace grants failed")
			return
		}
		w.Header().Set("ETag", set.Version)
		writeNativeMavenJSON(w, http.StatusOK, set.Grants)
	})
}

func validRepositoryGrants(grants []repository.RepositoryGrant, format repository.Format) bool {
	validScopes := map[string]bool{"repositories:read": true, "repositories:write": true, "repositories:admin": true}
	keys := map[string]bool{}
	for _, grant := range grants {
		if strings.TrimSpace(grant.Principal) == "" || len(grant.Scopes) == 0 || !validArtifactSearchQuery(format, grant.ResourcePrefix) {
			return false
		}
		key := grant.Principal + "\x00" + grant.ResourcePrefix
		if keys[key] {
			return false
		}
		keys[key] = true
		scopes := map[string]bool{}
		for _, scope := range grant.Scopes {
			if !validScopes[scope] || scopes[scope] {
				return false
			}
			scopes[scope] = true
		}
	}
	return true
}

func normalizeAndValidateRetentionPolicy(policy *repository.RepositoryRetentionPolicy) error {
	if policy.KeepDays < 1 || policy.KeepDays > 36500 {
		return errors.New("keepDays must be between 1 and 36500")
	}
	if policy.SnapshotKeepDays == 0 {
		policy.SnapshotKeepDays = policy.KeepDays
	}
	if policy.SnapshotKeepDays < 1 || policy.SnapshotKeepDays > 36500 {
		return errors.New("snapshotKeepDays must be between 1 and 36500")
	}
	if policy.MinimumVersions < 1 || policy.MinimumVersions > 100000 {
		return errors.New("minimumVersions must be between 1 and 100000")
	}
	if policy.MaximumVersions < 0 || policy.MaximumVersions > 100000 {
		return errors.New("maximumVersions must be between 0 and 100000")
	}
	if policy.MaximumVersions > 0 && policy.MaximumVersions < policy.MinimumVersions {
		return errors.New("maximumVersions must be zero or greater than or equal to minimumVersions")
	}
	var err error
	policy.CoordinatePatterns, err = normalizeRetentionPatterns(policy.CoordinatePatterns)
	if err != nil {
		return fmt.Errorf("coordinatePatterns %w", err)
	}
	policy.ProtectedPatterns, err = normalizeRetentionPatterns(policy.ProtectedPatterns)
	if err != nil {
		return fmt.Errorf("protectedPatterns %w", err)
	}
	return nil
}

func normalizeRetentionPatterns(patterns []string) ([]string, error) {
	if len(patterns) > 20 {
		return nil, errors.New("must contain at most 20 regular expressions")
	}
	result := make([]string, 0, len(patterns))
	for _, pattern := range patterns {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" || len(pattern) > 256 {
			return nil, errors.New("must contain non-empty expressions of at most 256 characters")
		}
		if _, err := regexp.Compile(pattern); err != nil {
			return nil, fmt.Errorf("contains invalid regular expression %q", pattern)
		}
		result = append(result, pattern)
	}
	return result, nil
}

func (h generatedRepositoryAPIAdapter) GetRetentionPolicy(w http.ResponseWriter, r *http.Request, repositoryID adminopenapi.RepositoryId) {
	h.withRepositoryScope(w, r, repositoryID.String(), RepositoryRead, func(_ Principal, repo repository.HostedRepository) {
		if repo.Type != repository.RepositoryTypeHosted || !supportsRepositoryRetention(repo.Format) {
			writeHostedProblem(w, http.StatusConflict, "unsupported_operation", "retention policies are supported for Maven, OCI, Conan, and Raw hosted repositories")
			return
		}
		policy, err := h.retentionPolicies.GetRepositoryRetentionPolicy(r.Context(), repositoryID.String())
		if err != nil {
			writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "get retention policy failed")
			return
		}
		writeNativeMavenJSON(w, http.StatusOK, policy)
	})
}

func (h generatedRepositoryAPIAdapter) ReplaceRetentionPolicy(w http.ResponseWriter, r *http.Request, repositoryID adminopenapi.RepositoryId, params adminopenapi.ReplaceRetentionPolicyParams) {
	h.withRepositoryScope(w, r, repositoryID.String(), RepositoryAdmin, func(_ Principal, repo repository.HostedRepository) {
		if repo.Type != repository.RepositoryTypeHosted || !supportsRepositoryRetention(repo.Format) {
			writeHostedProblem(w, http.StatusConflict, "unsupported_operation", "retention policies are supported for Maven, OCI, Conan, and Raw hosted repositories")
			return
		}
		var policy repository.RepositoryRetentionPolicy
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&policy); err != nil || policy.Version == "" {
			writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "version must be valid")
			return
		}
		if err := normalizeAndValidateRetentionPolicy(&policy); err != nil {
			writeHostedProblem(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		updated, err := h.retentionPolicies.ReplaceRepositoryRetentionPolicy(r.Context(), repositoryID.String(), policy, string(params.IfMatch))
		if errors.Is(err, repository.ErrNotFound) {
			writeHostedProblem(w, http.StatusNotFound, "not_found", "repository not found")
			return
		}
		if errors.Is(err, repository.ErrVersionConflict) {
			writeHostedProblem(w, http.StatusPreconditionFailed, "version_conflict", "If-Match does not match current version")
			return
		}
		if err != nil {
			writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "replace retention policy failed")
			return
		}
		writeNativeMavenJSON(w, http.StatusOK, updated)
	})
}

func (h generatedRepositoryAPIAdapter) GetRepositoryCapacity(w http.ResponseWriter, r *http.Request, repositoryID adminopenapi.RepositoryId) {
	h.withRepositoryScope(w, r, repositoryID.String(), RepositoryRead, func(_ Principal, repo repository.HostedRepository) {
		capacity, err := h.capacities.GetRepositoryCapacity(r.Context(), repositoryID.String())
		if errors.Is(err, repository.ErrNotFound) {
			writeHostedProblem(w, http.StatusNotFound, "not_found", "repository not found")
			return
		}
		if err != nil {
			writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "get repository capacity failed")
			return
		}
		if repo.Type == repository.RepositoryTypeProxy && h.maintenance != nil {
			// Proxy repositories do not own Hosted artifacts, but they do own
			// read-through cache bytes. Keep quota from the capacity store and
			// replace usage with live cache usage so the Console does not show 0.
			if proxyCapacity, err := (proxyCacheBrowseHandler{store: h.store, maintenance: h.maintenance, authenticator: h.authenticator, authorizer: h.authorizer}).proxyCacheCapacity(r.Context(), repo, capacity); err == nil {
				capacity = proxyCapacity
			}
		}
		writeNativeMavenJSON(w, http.StatusOK, capacity)
	})
}

func (h generatedRepositoryAPIAdapter) ListRepositoryCapacities(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.authorize(w, r); !ok {
		return
	}
	store, ok := h.capacities.(repository.RepositoryCapacityRecordStore)
	if !ok {
		writeHostedProblem(w, http.StatusNotImplemented, "not_supported", "repository capacity aggregation is unavailable")
		return
	}
	records, err := store.ListRepositoryCapacityRecords(r.Context())
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "list repository capacities failed")
		return
	}
	proxyCapacities, proxyErr := (proxyCacheBrowseHandler{store: h.store, maintenance: h.maintenance, authenticator: h.authenticator, authorizer: h.authorizer}).proxyCacheCapacities(r.Context(), records)
	items := make(adminopenapi.RepositoryCapacityList, 0, len(records))
	for _, record := range records {
		capacity := record.Capacity
		if proxyErr == nil {
			capacity = proxyCapacities[capacity.RepositoryID]
		}
		item := adminopenapi.RepositoryCapacity{
			RepositoryId: uuid.MustParse(capacity.RepositoryID),
			Format:       adminopenapi.Format(capacity.Format),
			UsedBytes:    capacity.UsedBytes,
			ObjectCount:  capacity.ObjectCount,
			QuotaBytes:   capacity.QuotaBytes,
		}
		if capacity.PrimaryBytes != 0 {
			item.PrimaryBytes = &capacity.PrimaryBytes
		}
		if capacity.SidecarBytes != 0 {
			item.SidecarBytes = &capacity.SidecarBytes
		}
		if capacity.NegativeCount != 0 {
			item.NegativeCount = &capacity.NegativeCount
		}
		if capacity.ExpiredObjectCount != 0 {
			item.ExpiredObjectCount = &capacity.ExpiredObjectCount
		}
		if capacity.ReclaimableBytes != 0 {
			item.ReclaimableBytes = &capacity.ReclaimableBytes
		}
		items = append(items, item)
	}
	writeNativeMavenJSON(w, http.StatusOK, items)
}

func (h generatedRepositoryAPIAdapter) ReplaceRepositoryCapacity(w http.ResponseWriter, r *http.Request, repositoryID adminopenapi.RepositoryId) {
	h.withRepositoryScope(w, r, repositoryID.String(), RepositoryAdmin, func(principal Principal, repo repository.HostedRepository) {
		var request adminopenapi.RepositoryCapacityQuota
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&request); err != nil || request.QuotaBytes < 0 {
			writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "quotaBytes must be a non-negative integer")
			return
		}
		capacity, err := h.capacities.ReplaceRepositoryCapacityQuota(r.Context(), repositoryID.String(), request.QuotaBytes)
		if repository.IsQuotaExceeded(err) {
			writeHostedProblem(w, http.StatusConflict, "quota_exceeded", "quotaBytes is lower than current repository usage")
			return
		}
		if errors.Is(err, repository.ErrNotFound) {
			writeHostedProblem(w, http.StatusNotFound, "not_found", "repository not found")
			return
		}
		if err != nil {
			writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "replace repository capacity failed")
			return
		}
		if h.audit != nil {
			_ = h.audit.RecordAudit(r.Context(), repository.AuditRecord{GroupName: repo.Name, Repository: repo.Name, Actor: principal.Actor, Outcome: repository.AuditResolved, OccurredAt: time.Now().UTC(), Format: "management", Resource: "repositories/" + repo.ID + "/capacity", Operation: "capacity.configure", Status: http.StatusOK, CacheDisposition: "bypass"})
		}
		writeNativeMavenJSON(w, http.StatusOK, capacity)
	})
}

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

func (h generatedRepositoryAPIAdapter) CreateRepositoryPromotion(w http.ResponseWriter, r *http.Request, repositoryID adminopenapi.RepositoryId, params adminopenapi.CreateRepositoryPromotionParams) {
	h.withRepositoryScope(w, r, repositoryID.String(), RepositoryAdmin, func(principal Principal, source repository.HostedRepository) {
		if source.Format != repository.FormatMaven && source.Format != repository.FormatOCI && source.Format != repository.FormatRaw && source.Format != repository.FormatConan {
			writeHostedProblem(w, http.StatusConflict, "unsupported_operation", "promotion is currently supported only for Maven, OCI, Raw, and Conan repositories")
			return
		}
		var request adminopenapi.PromotionRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&request); err != nil || request.Digest == "" || (source.Format == repository.FormatMaven && !validMavenCoordinate(request.Coordinate)) || (source.Format == repository.FormatOCI && (request.Coordinate == "" || strings.Contains(request.Coordinate, "@"))) || (source.Format == repository.FormatRaw && strings.Trim(request.Coordinate, "/") == "") || (source.Format == repository.FormatConan && !strings.Contains(request.Coordinate, "#")) {
			writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "targetRepositoryId, immutable artifact coordinate, and digest are required")
			return
		}
		h.withRepositoryScopeForPrincipal(w, r, principal, request.TargetRepositoryId.String(), RepositoryAdmin, func(Principal) {
			target, err := h.sessions.store.GetHostedRepository(r.Context(), request.TargetRepositoryId.String())
			if err != nil || target.Format != source.Format || target.State != repository.RepositoryActive {
				writeHostedProblem(w, http.StatusConflict, "invalid_target", "target must be an active repository with the same format")
				return
			}
			var job repository.LifecycleJob
			switch source.Format {
			case repository.FormatMaven:
				promotionID := uuid.NewSHA1(uuid.NameSpaceURL, []byte("maven-promotion:"+target.ID+":"+string(params.IdempotencyKey))).String()
				job, _, err = (mavenprotocol.NativePromotion{Store: h.sessions.store}).Enqueue(r.Context(), target.ID, string(params.IdempotencyKey), mavenprotocol.PromotionPayload{SourceRepositoryID: source.ID, Coordinate: request.Coordinate, Digest: request.Digest, PromotionID: promotionID})
			case repository.FormatOCI:
				job, _, err = (ociprotocol.NativePromotion{Store: h.sessions.store}).Enqueue(r.Context(), target.ID, string(params.IdempotencyKey), ociprotocol.PromotionPayload{SourceRepositoryID: source.ID, Name: request.Coordinate, Digest: request.Digest})
			case repository.FormatRaw:
				job, _, err = (rawprotocol.NativePromotion{Store: h.sessions.store}).Enqueue(r.Context(), target.ID, string(params.IdempotencyKey), rawprotocol.PromotionPayload{SourceRepositoryID: source.ID, Path: request.Coordinate, Digest: request.Digest})
			default:
				reference, revision, _ := strings.Cut(request.Coordinate, "#")
				job, _, err = (conanprotocol.NativePromotion{Store: h.sessions.store}).Enqueue(r.Context(), target.ID, string(params.IdempotencyKey), conanprotocol.PromotionPayload{SourceRepositoryID: source.ID, Reference: reference, Revision: revision, Digest: request.Digest})
			}
			if errors.Is(err, repository.ErrIdempotencyConflict) {
				writeHostedProblem(w, http.StatusConflict, "idempotency_conflict", "Idempotency-Key conflicts with an existing promotion job")
				return
			}
			if err != nil {
				writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "enqueue promotion job failed")
				return
			}
			_ = h.audit.RecordAudit(r.Context(), repository.AuditRecord{Repository: source.Name, GroupName: source.Name, Actor: principal.Actor, Outcome: repository.AuditResolved, OccurredAt: time.Now().UTC(), Format: "management", Resource: request.Coordinate, Operation: "promote", Status: http.StatusAccepted})
			writeNativeMavenJSON(w, http.StatusAccepted, lifecycleJobResponse(job))
		})
	})
}

func (h generatedRepositoryAPIAdapter) CreateRepositoryReplication(w http.ResponseWriter, r *http.Request, repositoryID adminopenapi.RepositoryId, params adminopenapi.CreateRepositoryReplicationParams) {
	h.withRepositoryScope(w, r, repositoryID.String(), RepositoryAdmin, func(principal Principal, source repository.HostedRepository) {
		var request adminopenapi.ReplicationRequest
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&request); err != nil || (source.Format != repository.FormatRaw && source.Format != repository.FormatOCI && source.Format != repository.FormatMaven && source.Format != repository.FormatConan) || strings.TrimSpace(request.Coordinate) == "" || !validRepositoryDigest(request.Digest) || (source.Format == repository.FormatMaven && !validMavenCoordinate(request.Coordinate)) || (source.Format == repository.FormatConan && !validConanReplicationCoordinate(request.Coordinate)) {
			writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "Maven, OCI, Raw, or Conan replication requires a visible coordinate and sha256 digest")
			return
		}
		h.withRepositoryScopeForPrincipal(w, r, principal, request.TargetRepositoryId.String(), RepositoryAdmin, func(Principal) {
			target, err := h.store.GetHostedRepository(r.Context(), request.TargetRepositoryId.String())
			if err != nil || target.Format != source.Format || target.State != repository.RepositoryActive {
				writeHostedProblem(w, http.StatusConflict, "invalid_target", "target must be an active repository with the same format")
				return
			}
			format := source.Format
			var checkpoints []repository.ReplicationCheckpoint
			if format == repository.FormatRaw {
				asset, lookupErr := h.sessions.store.GetRawAsset(r.Context(), source.ID, request.Coordinate)
				if errors.Is(lookupErr, repository.ErrNotFound) || asset.Digest != request.Digest {
					writeHostedProblem(w, http.StatusNotFound, "not_found", "source Raw artifact is unavailable")
					return
				}
				if lookupErr != nil {
					writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "lookup source Raw artifact failed")
					return
				}
				checkpoints = []repository.ReplicationCheckpoint{{ObjectKey: asset.ObjectKey, Digest: asset.Digest, Size: asset.Size}}
			} else if format == repository.FormatMaven {
				artifact, lookupErr := h.sessions.store.GetMavenArtifactByCoordinate(r.Context(), source.ID, request.Coordinate)
				if errors.Is(lookupErr, repository.ErrNotFound) || artifact.Digest != request.Digest {
					writeHostedProblem(w, http.StatusNotFound, "not_found", "source Maven artifact is unavailable")
					return
				}
				if lookupErr != nil {
					writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "lookup source Maven artifact failed")
					return
				}
				assets, lookupErr := h.sessions.store.ListMavenAssets(r.Context(), source.ID, request.Coordinate)
				if lookupErr != nil || len(assets) == 0 {
					writeHostedProblem(w, http.StatusNotFound, "not_found", "source Maven assets are unavailable")
					return
				}
				checkpoints = make([]repository.ReplicationCheckpoint, 0, len(assets))
				seenDigests := make(map[string]bool, len(assets))
				for _, asset := range assets {
					key := asset.Digest + "\x00" + strconv.FormatInt(asset.Size, 10)
					if seenDigests[key] {
						continue
					}
					seenDigests[key] = true
					checkpoints = append(checkpoints, repository.ReplicationCheckpoint{SourceObjectKey: asset.ObjectKey, ObjectKey: mavenReplicationTargetObjectKey(target.ID, asset.Digest), Digest: asset.Digest, Size: asset.Size})
				}
			} else if format == repository.FormatOCI {
				manifest, lookupErr := h.sessions.store.GetOCIManifest(r.Context(), source.ID, request.Coordinate, request.Digest)
				if errors.Is(lookupErr, repository.ErrNotFound) || manifest.Digest != request.Digest {
					writeHostedProblem(w, http.StatusNotFound, "not_found", "source OCI manifest is unavailable")
					return
				}
				if lookupErr != nil {
					writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "lookup source OCI manifest failed")
					return
				}
				checkpoints = []repository.ReplicationCheckpoint{{SourceObjectKey: manifest.ObjectKey, ObjectKey: ociReplicationTargetObjectKey(target.ID, manifest.Name, manifest.Digest), Digest: manifest.Digest, Size: manifest.Size}}
			} else {
				reference, revision, _ := strings.Cut(request.Coordinate, "#")
				recipe, lookupErr := h.conan.GetConanRecipeRevision(r.Context(), source.ID, reference, revision)
				if errors.Is(lookupErr, repository.ErrNotFound) || recipe.Digest != request.Digest || recipe.State != "visible" {
					writeHostedProblem(w, http.StatusNotFound, "not_found", "source Conan recipe revision is unavailable")
					return
				}
				if lookupErr != nil {
					writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "lookup source Conan recipe revision failed")
					return
				}
				checkpoints, lookupErr = conanReplicationCheckpoints(r.Context(), h.conan, source.ID, target.ID, reference, revision)
				if lookupErr != nil || len(checkpoints) == 0 {
					writeHostedProblem(w, http.StatusNotFound, "not_found", "source Conan assets are unavailable")
					return
				}
			}
			plan, _, err := h.replication.CreateReplicationPlan(r.Context(), repository.ReplicationPlan{ID: uuid.NewString(), SourceRepositoryID: source.ID, TargetRepositoryID: target.ID, Format: format, IdempotencyKey: string(params.IdempotencyKey)}, checkpoints)
			if errors.Is(err, repository.ErrIdempotencyConflict) {
				writeHostedProblem(w, http.StatusConflict, "idempotency_conflict", "Idempotency-Key conflicts with an existing replication plan")
				return
			}
			if err != nil {
				writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "create replication plan failed")
				return
			}
			_ = h.audit.RecordAudit(r.Context(), repository.AuditRecord{Repository: source.Name, GroupName: source.Name, Actor: principal.Actor, Outcome: repository.AuditResolved, OccurredAt: time.Now().UTC(), Format: "management", Resource: request.Coordinate, Operation: "replicate", Status: http.StatusAccepted})
			writeNativeMavenJSON(w, http.StatusAccepted, toOpenAPIReplicationPlan(plan))
		})
	})
}

func validConanReplicationCoordinate(value string) bool {
	reference, revision, found := strings.Cut(value, "#")
	return found && reference != "" && revision != "" && !strings.Contains(revision, "/") && validConanPublishRequest(nativeConanPublishRequest{Kind: "recipe", Reference: reference, RecipeRevision: revision, Objects: []repository.MavenDeclaredObject{{Name: "object", Digest: "sha256:" + strings.Repeat("0", 64), Size: 1}}})
}

func (h generatedRepositoryAPIAdapter) ListRepositoryReplications(w http.ResponseWriter, r *http.Request, repositoryID adminopenapi.RepositoryId) {
	h.withRepositoryScope(w, r, repositoryID.String(), RepositoryAdmin, func(Principal, repository.HostedRepository) {
		plans, err := h.replication.ListReplicationPlans(r.Context(), repositoryID.String(), 100)
		if err != nil {
			writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "list replication plans failed")
			return
		}
		items := make([]adminopenapi.ReplicationPlan, 0, len(plans))
		for _, plan := range plans {
			items = append(items, toOpenAPIReplicationPlan(plan))
		}
		writeNativeMavenJSON(w, http.StatusOK, items)
	})
}

func (h generatedRepositoryAPIAdapter) GetRepositoryReplication(w http.ResponseWriter, r *http.Request, repositoryID adminopenapi.RepositoryId, replicationPlanID adminopenapi.ReplicationPlanId) {
	h.withRepositoryScope(w, r, repositoryID.String(), RepositoryAdmin, func(_ Principal, _ repository.HostedRepository) {
		plan, err := h.replication.GetReplicationPlan(r.Context(), repositoryID.String(), replicationPlanID.String())
		if errors.Is(err, repository.ErrNotFound) {
			writeHostedProblem(w, http.StatusNotFound, "not_found", "replication plan not found")
			return
		}
		if err != nil {
			writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "get replication plan failed")
			return
		}
		checkpoints, err := h.replication.ListReplicationCheckpoints(r.Context(), plan.ID)
		if err != nil {
			writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "list replication checkpoints failed")
			return
		}
		writeNativeMavenJSON(w, http.StatusOK, toOpenAPIReplicationPlanDetail(plan, checkpoints))
	})
}

func (h generatedRepositoryAPIAdapter) DeleteRepositoryReplication(w http.ResponseWriter, r *http.Request, repositoryID adminopenapi.RepositoryId, replicationPlanID adminopenapi.ReplicationPlanId) {
	h.withRepositoryScope(w, r, repositoryID.String(), RepositoryAdmin, func(_ Principal, _ repository.HostedRepository) {
		plan, err := h.replication.GetReplicationPlan(r.Context(), repositoryID.String(), replicationPlanID.String())
		if errors.Is(err, repository.ErrNotFound) {
			writeHostedProblem(w, http.StatusNotFound, "not_found", "replication plan not found")
			return
		}
		if err != nil {
			writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "get replication plan failed")
			return
		}
		// Only pending or failed plans can be cancelled: the worker owns a
		// running plan mid-flight, and completed/cancelled plans are terminal.
		if plan.State != "pending" && plan.State != "failed" {
			writeHostedProblem(w, http.StatusConflict, "invalid_state", "only pending or failed replication plans can be cancelled")
			return
		}
		if err := h.replication.CancelReplicationPlan(r.Context(), repositoryID.String(), replicationPlanID.String()); err != nil {
			if errors.Is(err, repository.ErrNotFound) {
				writeHostedProblem(w, http.StatusConflict, "invalid_state", "replication plan was claimed or completed before cancellation")
				return
			}
			writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "cancel replication plan failed")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

func validRepositoryDigest(value string) bool {
	if !strings.HasPrefix(value, "sha256:") || len(value) != len("sha256:")+64 {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	return err == nil
}

func validOCIRestoreCoordinate(value string) bool {
	_, _, ok := parseOCIRestoreCoordinate(value)
	return ok
}

func parseOCIRestoreCoordinate(value string) (name, digest string, ok bool) {
	if len(value) > 1024 || strings.ContainsAny(value, "\\\x00") {
		return "", "", false
	}
	name, digest, split := strings.Cut(value, "@")
	if !split || name == "" || digest == "" || strings.Contains(digest, "@") || !validOCIImagePrefix(name) || !validRepositoryDigest(digest) {
		return "", "", false
	}
	// OCI image names cannot contain empty path components. The shared prefix
	// validator intentionally accepts a trailing slash for search prefixes.
	for _, component := range strings.Split(name, "/") {
		if component == "" {
			return "", "", false
		}
	}
	return name, digest, true
}

func toOpenAPIReplicationPlan(plan repository.ReplicationPlan) adminopenapi.ReplicationPlan {
	item := adminopenapi.ReplicationPlan{Id: uuid.MustParse(plan.ID), SourceRepositoryId: uuid.MustParse(plan.SourceRepositoryID), TargetRepositoryId: uuid.MustParse(plan.TargetRepositoryID), Format: adminopenapi.Format(plan.Format), State: adminopenapi.ReplicationPlanState(plan.State), CreatedAt: plan.CreatedAt}
	if !plan.StartedAt.IsZero() {
		item.StartedAt = &plan.StartedAt
	}
	if !plan.CompletedAt.IsZero() {
		item.CompletedAt = &plan.CompletedAt
	}
	if plan.LastError != "" {
		item.LastError = &plan.LastError
	}
	return item
}

func toOpenAPIReplicationPlanDetail(plan repository.ReplicationPlan, checkpoints []repository.ReplicationCheckpoint) adminopenapi.ReplicationPlanDetail {
	item := adminopenapi.ReplicationPlanDetail{Id: uuid.MustParse(plan.ID), SourceRepositoryId: uuid.MustParse(plan.SourceRepositoryID), TargetRepositoryId: uuid.MustParse(plan.TargetRepositoryID), Format: adminopenapi.Format(plan.Format), State: adminopenapi.ReplicationPlanDetailState(plan.State), CreatedAt: plan.CreatedAt, Checkpoints: make([]adminopenapi.ReplicationCheckpointProgress, 0, len(checkpoints))}
	if !plan.StartedAt.IsZero() {
		item.StartedAt = &plan.StartedAt
	}
	if !plan.CompletedAt.IsZero() {
		item.CompletedAt = &plan.CompletedAt
	}
	if plan.LastError != "" {
		item.LastError = &plan.LastError
	}
	for _, checkpoint := range checkpoints {
		progress := adminopenapi.ReplicationCheckpointProgress{ObjectKey: checkpoint.ObjectKey, Digest: checkpoint.Digest, Size: checkpoint.Size, ByteOffset: checkpoint.ByteOffset, State: adminopenapi.ReplicationCheckpointProgressState(checkpoint.State), Attempts: checkpoint.Attempts}
		if checkpoint.LastError != "" {
			progress.LastError = &checkpoint.LastError
		}
		if !checkpoint.VerifiedAt.IsZero() {
			progress.VerifiedAt = &checkpoint.VerifiedAt
		}
		item.Checkpoints = append(item.Checkpoints, progress)
	}
	return item
}

func (h generatedRepositoryAPIAdapter) RestoreRepositoryArtifact(w http.ResponseWriter, r *http.Request, repositoryID adminopenapi.RepositoryId) {
	h.withRepositoryScope(w, r, repositoryID.String(), RepositoryAdmin, func(_ Principal, repo repository.HostedRepository) {
		var request adminopenapi.RestoreArtifact
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&request); err != nil || (repo.Format == repository.FormatConan && !validConanRestoreCoordinate(request.Coordinate)) || (repo.Format == repository.FormatMaven && !validMavenCoordinate(request.Coordinate)) || (repo.Format == repository.FormatOCI && !validOCIRestoreCoordinate(request.Coordinate)) || (repo.Format == repository.FormatRaw && (strings.Trim(request.Coordinate, "/") == "" || !validRawAssetPrefix(request.Coordinate))) {
			writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "coordinate must identify a supported artifact tombstone")
			return
		}
		if repo.Format != repository.FormatConan && repo.Format != repository.FormatMaven && repo.Format != repository.FormatOCI && repo.Format != repository.FormatRaw {
			writeHostedProblem(w, http.StatusConflict, "unsupported_operation", "restore is not supported for this repository format")
			return
		}
		if _, err := h.tombstones.GetArtifactTombstone(r.Context(), repo.ID, repo.Format, request.Coordinate); errors.Is(err, repository.ErrNotFound) {
			writeHostedProblem(w, http.StatusNotFound, "not_found", "tombstone not found")
			return
		} else if err != nil {
			writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "get tombstone failed")
			return
		}
		var err error
		switch repo.Format {
		case repository.FormatMaven:
			artifact, getErr := h.sessions.store.GetMavenArtifactByCoordinate(r.Context(), repo.ID, request.Coordinate)
			if getErr != nil {
				err = getErr
			} else {
				_, err = h.sessions.store.RestoreMavenArtifact(r.Context(), repo.ID, artifact.ID)
			}
		case repository.FormatConan:
			err = h.restoreConanCoordinate(r, repo.ID, request.Coordinate)
		case repository.FormatOCI:
			name, digest, _ := parseOCIRestoreCoordinate(request.Coordinate)
			_, err = h.oci.RestoreOCIManifest(r.Context(), repo.ID, name, digest)
		default:
			_, err = h.sessions.store.RestoreRawAsset(r.Context(), repo.ID, request.Coordinate)
		}
		if errors.Is(err, repository.ErrNotFound) || errors.Is(err, repository.ErrDisabled) || errors.Is(err, repository.ErrNameExists) {
			writeHostedProblem(w, http.StatusConflict, "restore_unavailable", "artifact cannot be restored")
			return
		}
		if err != nil {
			writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "restore artifact failed")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

func (h generatedRepositoryAPIAdapter) restoreConanCoordinate(r *http.Request, repositoryID, coordinate string) error {
	reference, recipeRevision, packageID, packageRevision, packageRestore, ok := parseConanRestoreCoordinate(coordinate)
	if !ok {
		return repository.ErrNotFound
	}
	if packageRestore {
		_, err := h.conan.RestoreConanPackageRevision(r.Context(), repositoryID, reference, recipeRevision, packageID, packageRevision)
		return err
	}
	_, err := h.conan.RestoreConanRecipeRevision(r.Context(), repositoryID, reference, recipeRevision)
	return err
}

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

func (h generatedRepositoryAPIAdapter) ListGroups(w http.ResponseWriter, r *http.Request, params adminopenapi.ListGroupsParams) {
	if _, ok := h.authorize(w, r); !ok {
		return
	}
	limit, after := 50, ""
	if params.PageSize != nil {
		limit = int(*params.PageSize)
	}
	if params.PageToken != nil {
		after = string(*params.PageToken)
	}
	items, next, err := h.groups.ListHostedGroups(r.Context(), limit, after)
	if errors.Is(err, repository.ErrNotFound) {
		writeHostedProblem(w, http.StatusBadRequest, "invalid_page_token", "page token is invalid")
		return
	}
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "list groups failed")
		return
	}
	writeNativeMavenJSON(w, http.StatusOK, map[string]any{"items": items, "nextPageToken": next})
}

func (h generatedRepositoryAPIAdapter) CreateGroup(w http.ResponseWriter, r *http.Request, params adminopenapi.CreateGroupParams) {
	principal, ok := h.authorize(w, r)
	if !ok {
		return
	}
	var group repository.HostedGroup
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&group); err != nil || !h.validHostedGroup(r, group) {
		writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "name, format, and members must be valid")
		return
	}
	group.ID = uuid.NewString()
	payload, _ := json.Marshal(struct {
		Name          string                   `json:"name"`
		Format        repository.Format        `json:"format"`
		AnonymousRead bool                     `json:"anonymousRead"`
		Members       []repository.GroupMember `json:"members"`
	}{group.Name, group.Format, group.AnonymousRead, group.Members})
	digest := sha256.Sum256(payload)
	created, _, err := h.groups.CreateHostedGroupIdempotently(r.Context(), group, principal.Actor, string(params.IdempotencyKey), base64.RawURLEncoding.EncodeToString(digest[:]))
	if errors.Is(err, repository.ErrIdempotencyConflict) {
		writeHostedProblem(w, http.StatusConflict, "idempotency_conflict", "Idempotency-Key was already used with a different request")
		return
	}
	if errors.Is(err, repository.ErrNameExists) {
		writeHostedProblem(w, http.StatusConflict, "version_conflict", "group name already exists")
		return
	}
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "create group failed")
		return
	}
	writeNativeMavenJSON(w, http.StatusCreated, created)
}

func (h generatedRepositoryAPIAdapter) GetGroup(w http.ResponseWriter, r *http.Request, id adminopenapi.GroupId) {
	h.writeGroup(w, r, id.String())
}
func (h generatedRepositoryAPIAdapter) ListGroupMembers(w http.ResponseWriter, r *http.Request, id adminopenapi.GroupId) {
	if _, ok := h.authorize(w, r); !ok {
		return
	}
	group, err := h.groups.GetHostedGroup(r.Context(), id.String())
	if errors.Is(err, repository.ErrNotFound) {
		writeHostedProblem(w, 404, "not_found", "group not found")
		return
	}
	if err != nil {
		writeHostedProblem(w, 500, "internal_error", "get group failed")
		return
	}
	writeNativeMavenJSON(w, 200, group.Members)
}

func (h generatedRepositoryAPIAdapter) GetGroupCapacity(w http.ResponseWriter, r *http.Request, id adminopenapi.GroupId) {
	if _, ok := h.authorize(w, r); !ok {
		return
	}
	group, err := h.groups.GetHostedGroup(r.Context(), id.String())
	if errors.Is(err, repository.ErrNotFound) {
		writeHostedProblem(w, http.StatusNotFound, "not_found", "group not found")
		return
	}
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "get group failed")
		return
	}
	items := make([]map[string]any, 0, len(group.Members))
	for _, member := range group.Members {
		repo, repoErr := h.store.GetHostedRepository(r.Context(), member.RepositoryID)
		if repoErr != nil {
			continue
		}
		capacity, capacityErr := h.capacities.GetRepositoryCapacity(r.Context(), member.RepositoryID)
		if capacityErr != nil {
			continue
		}
		if repo.Type == repository.RepositoryTypeProxy && h.maintenance != nil {
			if proxyCapacity, proxyErr := (proxyCacheBrowseHandler{store: h.store, maintenance: h.maintenance, authenticator: h.authenticator, authorizer: h.authorizer}).proxyCacheCapacity(r.Context(), repo, capacity); proxyErr == nil {
				capacity = proxyCapacity
			}
		}
		items = append(items, map[string]any{"position": member.Position, "repositoryId": repo.ID, "format": repo.Format, "type": repo.Type, "usedBytes": capacity.UsedBytes, "objectCount": capacity.ObjectCount, "quotaBytes": capacity.QuotaBytes})
	}
	writeNativeMavenJSON(w, http.StatusOK, map[string]any{"groupId": group.ID, "format": group.Format, "members": items})
}
func (h generatedRepositoryAPIAdapter) DeleteGroup(w http.ResponseWriter, r *http.Request, id adminopenapi.GroupId) {
	if _, ok := h.authorize(w, r); !ok {
		return
	}
	err := h.groups.DeleteHostedGroup(r.Context(), id.String())
	if errors.Is(err, repository.ErrNotFound) {
		writeHostedProblem(w, 404, "not_found", "group not found")
		return
	}
	if err != nil {
		writeHostedProblem(w, 500, "internal_error", "delete group failed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h generatedRepositoryAPIAdapter) ReplaceGroup(w http.ResponseWriter, r *http.Request, id adminopenapi.GroupId, params adminopenapi.ReplaceGroupParams) {
	if _, ok := h.authorize(w, r); !ok {
		return
	}
	var group repository.HostedGroup
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&group); err != nil || !h.validHostedGroup(r, group) {
		writeHostedProblem(w, 400, "invalid_request", "name, format, and members must be valid")
		return
	}
	group.ID = id.String()
	updated, err := h.groups.ReplaceHostedGroup(r.Context(), group, string(params.IfMatch))
	h.writeGroupMutation(w, updated, err)
}
func (h generatedRepositoryAPIAdapter) ReplaceGroupMembers(w http.ResponseWriter, r *http.Request, id adminopenapi.GroupId, params adminopenapi.ReplaceGroupMembersParams) {
	if _, ok := h.authorize(w, r); !ok {
		return
	}
	group, err := h.groups.GetHostedGroup(r.Context(), id.String())
	if errors.Is(err, repository.ErrNotFound) {
		writeHostedProblem(w, 404, "not_found", "group not found")
		return
	}
	var members []repository.GroupMember
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&members); err != nil || !h.validHostedGroup(r, repository.HostedGroup{Name: group.Name, Format: group.Format, Members: members}) {
		writeHostedProblem(w, 400, "invalid_request", "members must be valid")
		return
	}
	updated, err := h.groups.ReplaceHostedGroupMembers(r.Context(), id.String(), members, string(params.IfMatch))
	h.writeGroupMutation(w, updated, err)
}

func (h generatedRepositoryAPIAdapter) writeGroup(w http.ResponseWriter, r *http.Request, id string) {
	if _, ok := h.authorize(w, r); !ok {
		return
	}
	group, err := h.groups.GetHostedGroup(r.Context(), id)
	if errors.Is(err, repository.ErrNotFound) {
		writeHostedProblem(w, 404, "not_found", "group not found")
		return
	}
	if err != nil {
		writeHostedProblem(w, 500, "internal_error", "get group failed")
		return
	}
	writeNativeMavenJSON(w, 200, group)
}
func (h generatedRepositoryAPIAdapter) writeGroupMutation(w http.ResponseWriter, group repository.HostedGroup, err error) {
	if errors.Is(err, repository.ErrNotFound) {
		writeHostedProblem(w, 404, "not_found", "group not found")
		return
	}
	if errors.Is(err, repository.ErrVersionConflict) {
		writeHostedProblem(w, 412, "version_conflict", "If-Match does not match current version")
		return
	}
	if err != nil {
		writeHostedProblem(w, 500, "internal_error", "update group failed")
		return
	}
	writeNativeMavenJSON(w, 200, group)
}
func (h generatedRepositoryAPIAdapter) validHostedGroup(r *http.Request, group repository.HostedGroup) bool {
	if !hostedRepositoryName.MatchString(group.Name) || (group.Format != repository.FormatOCI && group.Format != repository.FormatMaven && group.Format != repository.FormatRaw && group.Format != repository.FormatConan) || len(group.Members) == 0 {
		return false
	}
	seen := map[string]bool{}
	for _, member := range group.Members {
		if _, err := uuid.Parse(member.RepositoryID); err != nil || member.Position < 0 || seen[member.RepositoryID] {
			return false
		}
		seen[member.RepositoryID] = true
		repo, err := h.store.GetHostedRepository(r.Context(), member.RepositoryID)
		if err != nil || repo.Format != group.Format || repo.State != repository.RepositoryActive {
			return false
		}
	}
	for i := range group.Members {
		found := false
		for _, member := range group.Members {
			if member.Position == i {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func (h generatedRepositoryAPIAdapter) CreatePublishSession(w http.ResponseWriter, r *http.Request, repositoryID adminopenapi.RepositoryId, params adminopenapi.CreatePublishSessionParams) {
	h.withRepositoryScope(w, r, repositoryID.String(), RepositoryWrite, func(principal Principal, _ repository.HostedRepository) {
		h.sessions.createWithIdempotencyKey(w, r, principal, repositoryID.String(), string(params.IdempotencyKey))
	})
}

func (h generatedRepositoryAPIAdapter) ListArtifacts(w http.ResponseWriter, r *http.Request, repositoryID adminopenapi.RepositoryId, _ adminopenapi.ListArtifactsParams) {
	h.withRepositoryBrowseScope(w, r, repositoryID.String(), func(Principal, repository.HostedRepository) {
		h.sessions.listArtifacts(w, r, repositoryID.String())
	})
}

func (h generatedRepositoryAPIAdapter) ListOCIImages(w http.ResponseWriter, r *http.Request, repositoryID adminopenapi.RepositoryId, params adminopenapi.ListOCIImagesParams) {
	h.withRepositoryBrowseScope(w, r, repositoryID.String(), func(_ Principal, repo repository.HostedRepository) {
		if repo.Format != repository.FormatOCI {
			writeHostedProblem(w, http.StatusNotFound, "not_found", "OCI repository not found")
			return
		}
		pageSize := 50
		if params.PageSize != nil {
			pageSize = int(*params.PageSize)
			if pageSize < 1 || pageSize > 200 {
				writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "pageSize must be between 1 and 200")
				return
			}
		}
		prefix := ""
		if params.Q != nil {
			prefix = string(*params.Q)
		}
		if !validOCIImagePrefix(prefix) {
			writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "q must be a valid OCI image-name prefix")
			return
		}
		pageToken := ""
		if params.PageToken != nil {
			pageToken = string(*params.PageToken)
		}
		after, err := h.decodeOCIImageCursor(pageToken, repo.ID, prefix)
		if err != nil {
			writeHostedProblem(w, http.StatusBadRequest, "invalid_page_token", "page token is invalid or expired")
			return
		}
		names, err := h.oci.SearchOCIManifestNames(r.Context(), repo.ID, prefix, pageSize+1, after)
		if err != nil {
			writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "list OCI images failed")
			return
		}
		var next *string
		if len(names) > pageSize {
			names = names[:pageSize]
			token := h.encodeOCIImageCursor(repo.ID, prefix, names[len(names)-1])
			next = &token
		}
		items := make([]adminopenapi.OCIImage, 0, len(names))
		for _, name := range names {
			items = append(items, adminopenapi.OCIImage{Name: name})
		}
		writeNativeMavenJSON(w, http.StatusOK, adminopenapi.OCIImagePage{Items: items, NextPageToken: next})
	})
}

func (h generatedRepositoryAPIAdapter) ListOCIManifests(w http.ResponseWriter, r *http.Request, repositoryID adminopenapi.RepositoryId, params adminopenapi.ListOCIManifestsParams) {
	h.withRepositoryBrowseScope(w, r, repositoryID.String(), func(_ Principal, repo repository.HostedRepository) {
		name := strings.TrimSpace(params.Name)
		if repo.Format != repository.FormatOCI || name == "" || !validOCIImagePrefix(name) {
			writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "name must identify an OCI image")
			return
		}
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
		after, err := h.decodeOCIManifestCursor(pageToken, repo.ID, name)
		if err != nil {
			writeHostedProblem(w, http.StatusBadRequest, "invalid_page_token", "page token is invalid or expired")
			return
		}
		manifests, err := h.oci.ListOCIManifests(r.Context(), repo.ID, name, pageSize+1, after)
		if err != nil {
			writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "list OCI manifests failed")
			return
		}
		var next *string
		if len(manifests) > pageSize {
			manifests = manifests[:pageSize]
			token := h.encodeOCIManifestCursor(repo.ID, name, manifests[len(manifests)-1].Digest)
			next = &token
		}
		items := make([]adminopenapi.OCIManifestSummary, 0, len(manifests))
		for _, manifest := range manifests {
			tags := append([]string{}, manifest.Tags...)
			item := adminopenapi.OCIManifestSummary{Digest: manifest.Digest, MediaType: manifest.MediaType, Size: manifest.Size, Tags: tags}
			if manifest.SubjectDigest != "" {
				item.SubjectDigest = &manifest.SubjectDigest
			}
			if manifest.ArtifactType != "" {
				item.ArtifactType = &manifest.ArtifactType
			}
			items = append(items, item)
		}
		writeNativeMavenJSON(w, http.StatusOK, adminopenapi.OCIManifestSummaryPage{Items: items, NextPageToken: next})
	})
}

func (h generatedRepositoryAPIAdapter) ListMavenCoordinates(w http.ResponseWriter, r *http.Request, repositoryID adminopenapi.RepositoryId, params adminopenapi.ListMavenCoordinatesParams) {
	h.withRepositoryBrowseScope(w, r, repositoryID.String(), func(_ Principal, repo repository.HostedRepository) {
		if repo.Format != repository.FormatMaven {
			writeHostedProblem(w, http.StatusNotFound, "not_found", "Maven repository not found")
			return
		}
		pageSize := 50
		if params.PageSize != nil {
			pageSize = int(*params.PageSize)
			if pageSize < 1 || pageSize > 200 {
				writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "pageSize must be between 1 and 200")
				return
			}
		}
		prefix := ""
		if params.Q != nil {
			prefix = string(*params.Q)
		}
		if !validMavenCoordinatePrefix(prefix) {
			writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "q must be a valid Maven coordinate prefix")
			return
		}
		pageToken := ""
		if params.PageToken != nil {
			pageToken = string(*params.PageToken)
		}
		after, err := h.decodeMavenCoordinateCursor(pageToken, repo.ID, prefix)
		if err != nil {
			writeHostedProblem(w, http.StatusBadRequest, "invalid_page_token", "page token is invalid or expired")
			return
		}
		artifacts, err := h.sessions.store.SearchMavenArtifacts(r.Context(), repo.ID, prefix, pageSize+1, after)
		if err != nil {
			writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "list Maven coordinates failed")
			return
		}
		var next *string
		if len(artifacts) > pageSize {
			artifacts = artifacts[:pageSize]
			last := artifacts[len(artifacts)-1]
			token := h.encodeMavenCoordinateCursor(repo.ID, prefix, last.Coordinate, last.BuildNumber)
			next = &token
		}
		items := make([]adminopenapi.MavenCoordinate, 0, len(artifacts))
		for _, artifact := range artifacts {
			items = append(items, adminopenapi.MavenCoordinate{Coordinate: artifact.Coordinate, Digest: artifact.Digest, CreatedAt: artifact.CreatedAt, Publisher: optionalPublisher(artifact.Publisher), BuildNumber: optionalBuildNumber(artifact.BuildNumber)})
		}
		writeNativeMavenJSON(w, http.StatusOK, adminopenapi.MavenCoordinatePage{Items: items, NextPageToken: next})
	})
}

func (h generatedRepositoryAPIAdapter) ListConanReferences(w http.ResponseWriter, r *http.Request, repositoryID adminopenapi.RepositoryId, params adminopenapi.ListConanReferencesParams) {
	h.withRepositoryBrowseScope(w, r, repositoryID.String(), func(_ Principal, repo repository.HostedRepository) {
		if repo.Format != repository.FormatConan {
			writeHostedProblem(w, http.StatusNotFound, "not_found", "Conan repository not found")
			return
		}
		pageSize := 50
		if params.PageSize != nil {
			pageSize = int(*params.PageSize)
			if pageSize < 1 || pageSize > 200 {
				writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "pageSize must be between 1 and 200")
				return
			}
		}
		prefix := ""
		if params.Q != nil {
			prefix = string(*params.Q)
		}
		if !validConanReferencePrefix(prefix) {
			writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "q must be a valid Conan reference prefix")
			return
		}
		pageToken := ""
		if params.PageToken != nil {
			pageToken = string(*params.PageToken)
		}
		after, err := h.decodeConanReferenceCursor(pageToken, repo.ID, prefix)
		if err != nil {
			writeHostedProblem(w, http.StatusBadRequest, "invalid_page_token", "page token is invalid or expired")
			return
		}
		references, err := h.conan.SearchConanReferences(r.Context(), repo.ID, prefix, pageSize+1, after)
		if err != nil {
			writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "list Conan references failed")
			return
		}
		var next *string
		if len(references) > pageSize {
			references = references[:pageSize]
			token := h.encodeConanReferenceCursor(repo.ID, prefix, references[len(references)-1].Reference)
			next = &token
		}
		items := make([]adminopenapi.ConanReference, 0, len(references))
		for _, reference := range references {
			items = append(items, adminopenapi.ConanReference{Reference: reference.Reference, Publisher: optionalPublisher(reference.Publisher)})
		}
		writeNativeMavenJSON(w, http.StatusOK, adminopenapi.ConanReferencePage{Items: items, NextPageToken: next})
	})
}

func (h generatedRepositoryAPIAdapter) ListConanRecipeRevisions(w http.ResponseWriter, r *http.Request, repositoryID adminopenapi.RepositoryId, params adminopenapi.ListConanRecipeRevisionsParams) {
	if _, err := h.store.GetHostedRepository(r.Context(), repositoryID.String()); errors.Is(err, repository.ErrNotFound) {
		group, groupErr := h.groups.GetHostedGroup(r.Context(), repositoryID.String())
		if groupErr == nil {
			if group.Format != repository.FormatConan {
				writeHostedProblem(w, http.StatusNotFound, "not_found", "Conan repository not found")
				return
			}
			if !anonymousHostedGroupReadAllowed(r.Context(), h.store, h.store, group, r.Method) {
				writeHostedProblem(w, http.StatusForbidden, "access_denied", "group anonymous read is not enabled")
				return
			}
			h.listHostedGroupConanRecipeRevisions(w, r, group, params)
			return
		}
	}
	h.withRepositoryBrowseScope(w, r, repositoryID.String(), func(_ Principal, repo repository.HostedRepository) {
		reference := strings.TrimSuffix(strings.TrimSpace(params.Reference), "/")
		if repo.Format != repository.FormatConan || repo.Type == repository.RepositoryTypeProxy || !validConanReferencePrefix(reference) || strings.Count(reference, "/") != 3 {
			writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "reference must be a valid Conan recipe reference")
			return
		}
		query := ""
		if params.Q != nil {
			query = strings.TrimSpace(*params.Q)
		}
		if len(query) > 255 || strings.ContainsRune(query, '\x00') {
			writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "q must be at most 255 characters")
			return
		}
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
		after, err := h.decodeConanRevisionCursor(pageToken, repo.ID, reference, query)
		if err != nil {
			writeHostedProblem(w, http.StatusBadRequest, "invalid_page_token", "page token is invalid or expired")
			return
		}
		revisions, err := h.conan.SearchConanRecipeRevisions(r.Context(), repo.ID, reference, query, pageSize+1, after)
		if err != nil {
			writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "list Conan recipe revisions failed")
			return
		}
		var next *string
		if len(revisions) > pageSize {
			revisions = revisions[:pageSize]
			token := h.encodeConanRevisionCursor(repo.ID, reference, query, revisions[len(revisions)-1].Revision)
			next = &token
		}
		items := make([]adminopenapi.ConanRecipeRevision, 0, len(revisions))
		for _, revision := range revisions {
			items = append(items, adminopenapi.ConanRecipeRevision{Reference: revision.Reference, Revision: revision.Revision, Digest: revision.Digest, State: adminopenapi.ConanRecipeRevisionState(revision.State), CreatedAt: revision.CreatedAt})
		}
		writeNativeMavenJSON(w, http.StatusOK, adminopenapi.ConanRecipeRevisionList{Items: items, NextPageToken: next})
	})
}

func (h generatedRepositoryAPIAdapter) ListConanPackageRevisions(w http.ResponseWriter, r *http.Request, repositoryID adminopenapi.RepositoryId, params adminopenapi.ListConanPackageRevisionsParams) {
	h.withRepositoryBrowseScope(w, r, repositoryID.String(), func(_ Principal, repo repository.HostedRepository) {
		reference, recipeRevision, packageID := strings.TrimSuffix(strings.TrimSpace(params.Reference), "/"), strings.TrimSpace(params.RecipeRevision), strings.TrimSpace(params.PackageId)
		if repo.Format != repository.FormatConan || repo.Type == repository.RepositoryTypeProxy || !validConanReferencePrefix(reference) || strings.Count(reference, "/") != 3 || !validConanSegment(recipeRevision) || !validConanSegment(packageID) {
			writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "reference, recipeRevision, and packageId must identify a Conan package")
			return
		}
		revisions, err := h.conan.ListConanPackageRevisions(r.Context(), repo.ID, reference, recipeRevision, packageID)
		if err != nil {
			writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "list Conan package revisions failed")
			return
		}
		items := make([]adminopenapi.ConanPackageRevision, 0, len(revisions))
		for _, revision := range revisions {
			items = append(items, adminopenapi.ConanPackageRevision{Reference: revision.Reference, RecipeRevision: revision.RecipeRevision, PackageId: revision.PackageID, Revision: revision.Revision, Digest: revision.Digest, State: adminopenapi.ConanPackageRevisionState(revision.State), CreatedAt: revision.CreatedAt})
		}
		writeNativeMavenJSON(w, http.StatusOK, adminopenapi.ConanPackageRevisionList{Items: items})
	})
}

func (h generatedRepositoryAPIAdapter) ListConanPackageIds(w http.ResponseWriter, r *http.Request, repositoryID adminopenapi.RepositoryId, params adminopenapi.ListConanPackageIdsParams) {
	h.withRepositoryBrowseScope(w, r, repositoryID.String(), func(_ Principal, repo repository.HostedRepository) {
		reference, recipeRevision := strings.TrimSuffix(strings.TrimSpace(params.Reference), "/"), strings.TrimSpace(params.RecipeRevision)
		if repo.Format != repository.FormatConan || repo.Type == repository.RepositoryTypeProxy || !validConanReferencePrefix(reference) || strings.Count(reference, "/") != 3 || !validConanSegment(recipeRevision) {
			writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "reference and recipeRevision must identify a Conan recipe revision")
			return
		}
		items, err := h.conan.ListConanPackageIDs(r.Context(), repo.ID, reference, recipeRevision)
		if err != nil {
			writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "list Conan package IDs failed")
			return
		}
		if items == nil {
			items = []string{}
		}
		writeNativeMavenJSON(w, http.StatusOK, adminopenapi.ConanPackageIdList{Items: items})
	})
}

func (h generatedRepositoryAPIAdapter) DeleteConanPackageRevision(w http.ResponseWriter, r *http.Request, repositoryID adminopenapi.RepositoryId, revision string, params adminopenapi.DeleteConanPackageRevisionParams) {
	h.withRepositoryScope(w, r, repositoryID.String(), RepositoryWrite, func(principal Principal, repo repository.HostedRepository) {
		reference, recipeRevision, packageID := strings.TrimSuffix(strings.TrimSpace(params.Reference), "/"), strings.TrimSpace(params.RecipeRevision), strings.TrimSpace(params.PackageId)
		if repo.Format != repository.FormatConan || repo.Type == repository.RepositoryTypeProxy || !validConanReferencePrefix(reference) || strings.Count(reference, "/") != 3 || !validConanSegment(recipeRevision) || !validConanSegment(packageID) || !validConanSegment(revision) {
			writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "reference, recipeRevision, packageId, and revision must identify a Conan package revision")
			return
		}
		if _, err := h.conan.TombstoneConanPackageRevision(r.Context(), repo.ID, reference, recipeRevision, packageID, revision); errors.Is(err, repository.ErrNotFound) {
			writeHostedProblem(w, http.StatusNotFound, "not_found", "Conan package revision not found")
			return
		} else if err != nil {
			writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "tombstone Conan package revision failed")
			return
		}
		_ = h.audit.RecordAudit(r.Context(), repository.AuditRecord{Repository: repo.Name, GroupName: repo.Name, Actor: principal.Actor, Outcome: repository.AuditResolved, OccurredAt: time.Now().UTC(), Format: "management", Resource: reference + "#" + recipeRevision + "/" + packageID + "#" + revision, Operation: "conan.package_revision.tombstone", Status: http.StatusNoContent})
		w.WriteHeader(http.StatusNoContent)
	})
}

func (h generatedRepositoryAPIAdapter) ListProxyCacheEntries(w http.ResponseWriter, r *http.Request, _ adminopenapi.RepositoryId, _ adminopenapi.ListProxyCacheEntriesParams) {
	h.proxyCache.ServeHTTP(w, r)
}

func (h generatedRepositoryAPIAdapter) InvalidateProxyCache(w http.ResponseWriter, r *http.Request, _ adminopenapi.RepositoryId) {
	h.proxyCache.Invalidate(w, r)
}

func (h generatedRepositoryAPIAdapter) ClearProxyNegativeCache(w http.ResponseWriter, r *http.Request, _ adminopenapi.RepositoryId) {
	h.proxyCache.ClearNegative(w, r)
}

func (h generatedRepositoryAPIAdapter) RefreshProxyCache(w http.ResponseWriter, r *http.Request, _ adminopenapi.RepositoryId) {
	h.mavenProxy.Refresh(w, r)
}

func (h generatedRepositoryAPIAdapter) GetProxyHealth(w http.ResponseWriter, r *http.Request, _ adminopenapi.RepositoryId) {
	h.mavenProxy.Health(w, r)
}

func (h generatedRepositoryAPIAdapter) GetArtifact(w http.ResponseWriter, r *http.Request, repositoryID adminopenapi.RepositoryId, artifactID uuid.UUID) {
	h.withRepositoryBrowseScope(w, r, repositoryID.String(), func(Principal, repository.HostedRepository) {
		h.sessions.getArtifact(w, r, repositoryID.String(), artifactID.String())
	})
}

func (h generatedRepositoryAPIAdapter) DeleteArtifact(w http.ResponseWriter, r *http.Request, repositoryID adminopenapi.RepositoryId, artifactID uuid.UUID) {
	h.withRepositoryScope(w, r, repositoryID.String(), RepositoryWrite, func(Principal, repository.HostedRepository) {
		h.sessions.deleteArtifact(w, r, repositoryID.String(), artifactID.String())
	})
}

func (h generatedRepositoryAPIAdapter) GetPublishSession(w http.ResponseWriter, r *http.Request, sessionID adminopenapi.SessionId) {
	h.withSessionScope(w, r, sessionID.String(), RepositoryRead, func(Principal) {
		h.sessions.getSession(w, r, sessionID.String())
	})
}

func (h generatedRepositoryAPIAdapter) UploadPublishObject(w http.ResponseWriter, r *http.Request, sessionID adminopenapi.SessionId, objectName string) {
	h.withSessionScope(w, r, sessionID.String(), RepositoryWrite, func(Principal) {
		h.sessions.upload(w, r, sessionID.String(), objectName)
	})
}

func (h generatedRepositoryAPIAdapter) CommitPublishSession(w http.ResponseWriter, r *http.Request, sessionID adminopenapi.SessionId) {
	h.withSessionScope(w, r, sessionID.String(), RepositoryWrite, func(Principal) {
		h.sessions.commit(w, r, sessionID.String())
	})
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

func validOCIImagePrefix(value string) bool {
	if len(value) > 255 || strings.HasPrefix(value, "/") || strings.Contains(value, "//") {
		return false
	}
	for _, component := range strings.Split(value, "/") {
		if component == "." || component == ".." {
			return false
		}
	}
	for _, c := range value {
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '.' || c == '_' || c == '-' || c == '/' {
			continue
		}
		return false
	}
	return true
}

func validMavenCoordinatePrefix(value string) bool {
	if len(value) > 255 {
		return false
	}
	for _, r := range value {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '.' && r != '_' && r != '-' && r != ':' {
			return false
		}
	}
	return true
}

// optionalPublisher maps an empty publisher (no committed publish session was
// recorded, for example replicated or pre-session artifacts) to nil so the
// field is omitted from the JSON response.
func optionalPublisher(publisher string) *string {
	if publisher == "" {
		return nil
	}
	return &publisher
}

// optionalBuildNumber omits the build number for release coordinates (build 0)
// so only SNAPSHOT builds carry it in responses.
func optionalBuildNumber(buildNumber int) *int {
	if buildNumber <= 0 {
		return nil
	}
	return &buildNumber
}

func validConanReferencePrefix(value string) bool {
	if len(value) > 255 || strings.ContainsAny(value, "\\\x00#") || strings.Contains(strings.ToLower(value), "%2f") || strings.Contains(strings.ToLower(value), "%23") {
		return false
	}
	parts := strings.Split(value, "/")
	for i, part := range parts {
		if part == "" && i == len(parts)-1 {
			continue
		}
		if !validConanSegment(part) {
			return false
		}
	}
	return true
}

func validConanRestoreCoordinate(value string) bool {
	_, _, _, _, _, ok := parseConanRestoreCoordinate(value)
	return ok
}

func parseConanRestoreCoordinate(value string) (reference, recipeRevision, packageID, packageRevision string, packageRestore, ok bool) {
	if len(value) > 1024 || strings.ContainsAny(value, "\\\x00") {
		return "", "", "", "", false, false
	}
	reference, remainder, split := strings.Cut(value, "#")
	if !split || strings.Count(reference, "/") != 3 || !validConanReferencePrefix(reference) {
		return "", "", "", "", false, false
	}
	if recipeRevision, remainder, split = strings.Cut(remainder, "/"); !split {
		return reference, recipeRevision, "", "", false, validConanSegment(recipeRevision)
	}
	packageID, packageRevision, split = strings.Cut(remainder, "#")
	if !split || strings.Contains(packageRevision, "/") {
		return "", "", "", "", false, false
	}
	return reference, recipeRevision, packageID, packageRevision, true, validConanSegment(recipeRevision) && validConanSegment(packageID) && validConanSegment(packageRevision)
}

func validRawAssetPrefix(value string) bool {
	if len(value) > 255 || strings.HasPrefix(value, "/") || strings.ContainsAny(value, "\\\x00") {
		return false
	}
	for _, segment := range strings.Split(value, "/") {
		if segment == "." || segment == ".." {
			return false
		}
	}
	return true
}

func validArtifactSearchQuery(format repository.Format, query string) bool {
	switch format {
	case repository.FormatOCI:
		return validOCIImagePrefix(query)
	case repository.FormatMaven:
		return validMavenCoordinatePrefix(query)
	case repository.FormatConan:
		return validConanReferencePrefix(query)
	case repository.FormatRaw:
		return validRawAssetPrefix(query)
	default:
		return false
	}
}

func (h hostedRepositoryAPIHandler) encodeOCIImageCursor(repositoryID, prefix, name string) string {
	return encodeSignedCursor(h.authenticator.AdminToken, ociImagePageCursor{Endpoint: "oci-images", RepositoryID: repositoryID, Prefix: prefix, Name: name, ExpiresAt: time.Now().UTC().Add(15 * time.Minute).Unix()})
}

func (h hostedRepositoryAPIHandler) decodeOCIImageCursor(token, repositoryID, prefix string) (string, error) {
	if token == "" {
		return "", nil
	}
	var cursor ociImagePageCursor
	if decodeSignedCursor(h.authenticator.AdminToken, token, &cursor) != nil || cursor.Endpoint != "oci-images" || cursor.RepositoryID != repositoryID || cursor.Prefix != prefix || cursor.Name == "" || time.Now().UTC().Unix() >= cursor.ExpiresAt {
		return "", errors.New("invalid cursor")
	}
	return cursor.Name, nil
}

func (h hostedRepositoryAPIHandler) encodeOCIManifestCursor(repositoryID, name, digest string) string {
	return encodeSignedCursor(h.authenticator.AdminToken, ociManifestPageCursor{Endpoint: "oci-manifests", RepositoryID: repositoryID, Name: name, Digest: digest, ExpiresAt: time.Now().UTC().Add(15 * time.Minute).Unix()})
}

func (h hostedRepositoryAPIHandler) decodeOCIManifestCursor(token, repositoryID, name string) (string, error) {
	if token == "" {
		return "", nil
	}
	var cursor ociManifestPageCursor
	if decodeSignedCursor(h.authenticator.AdminToken, token, &cursor) != nil || cursor.Endpoint != "oci-manifests" || cursor.RepositoryID != repositoryID || cursor.Name != name || cursor.Digest == "" || time.Now().UTC().Unix() >= cursor.ExpiresAt {
		return "", errors.New("invalid cursor")
	}
	return cursor.Digest, nil
}

func (h hostedRepositoryAPIHandler) encodeMavenCoordinateCursor(repositoryID, prefix, coordinate string, buildNumber int) string {
	return encodeSignedCursor(h.authenticator.AdminToken, mavenCoordinatePageCursor{Endpoint: "maven-coordinates", RepositoryID: repositoryID, Prefix: prefix, Coordinate: coordinate, BuildNumber: buildNumber, ExpiresAt: time.Now().UTC().Add(15 * time.Minute).Unix()})
}

func (h hostedRepositoryAPIHandler) decodeMavenCoordinateCursor(token, repositoryID, prefix string) (repository.MavenArtifactCursor, error) {
	if token == "" {
		return repository.MavenArtifactCursor{}, nil
	}
	var cursor mavenCoordinatePageCursor
	if decodeSignedCursor(h.authenticator.AdminToken, token, &cursor) != nil || cursor.Endpoint != "maven-coordinates" || cursor.RepositoryID != repositoryID || cursor.Prefix != prefix || cursor.Coordinate == "" || time.Now().UTC().Unix() >= cursor.ExpiresAt {
		return repository.MavenArtifactCursor{}, errors.New("invalid cursor")
	}
	return repository.MavenArtifactCursor{Coordinate: cursor.Coordinate, BuildNumber: cursor.BuildNumber}, nil
}

func (h hostedRepositoryAPIHandler) encodeConanReferenceCursor(repositoryID, prefix, reference string) string {
	return encodeSignedCursor(h.authenticator.AdminToken, conanReferencePageCursor{Endpoint: "conan-references", RepositoryID: repositoryID, Prefix: prefix, Reference: reference, ExpiresAt: time.Now().UTC().Add(15 * time.Minute).Unix()})
}

func (h hostedRepositoryAPIHandler) decodeConanReferenceCursor(token, repositoryID, prefix string) (string, error) {
	if token == "" {
		return "", nil
	}
	var cursor conanReferencePageCursor
	if decodeSignedCursor(h.authenticator.AdminToken, token, &cursor) != nil || cursor.Endpoint != "conan-references" || cursor.RepositoryID != repositoryID || cursor.Prefix != prefix || cursor.Reference == "" || time.Now().UTC().Unix() >= cursor.ExpiresAt {
		return "", errors.New("invalid cursor")
	}
	return cursor.Reference, nil
}

func (h hostedRepositoryAPIHandler) encodeConanRevisionCursor(repositoryID, reference, query, revision string) string {
	return encodeSignedCursor(h.authenticator.AdminToken, conanRevisionPageCursor{Endpoint: "conan-revisions", RepositoryID: repositoryID, Reference: reference, Query: query, Revision: revision, ExpiresAt: time.Now().UTC().Add(15 * time.Minute).Unix()})
}

func (h hostedRepositoryAPIHandler) decodeConanRevisionCursor(token, repositoryID, reference, query string) (string, error) {
	if token == "" {
		return "", nil
	}
	var cursor conanRevisionPageCursor
	if decodeSignedCursor(h.authenticator.AdminToken, token, &cursor) != nil || cursor.Endpoint != "conan-revisions" || cursor.RepositoryID != repositoryID || cursor.Reference != reference || cursor.Query != query || cursor.Revision == "" || time.Now().UTC().Unix() >= cursor.ExpiresAt {
		return "", errors.New("invalid cursor")
	}
	return cursor.Revision, nil
}

func (h hostedRepositoryAPIHandler) encodeArtifactSearchCursor(repositoryID string, format repository.Format, query, coordinate string, buildNumber int) string {
	return encodeSignedCursor(h.authenticator.AdminToken, artifactSearchPageCursor{Endpoint: "artifact-search", RepositoryID: repositoryID, Format: string(format), Query: query, Coordinate: coordinate, BuildNumber: buildNumber, ExpiresAt: time.Now().UTC().Add(15 * time.Minute).Unix()})
}

func (h hostedRepositoryAPIHandler) decodeArtifactSearchCursor(token, repositoryID string, format repository.Format, query string) (artifactSearchPosition, error) {
	if token == "" {
		return artifactSearchPosition{}, nil
	}
	var cursor artifactSearchPageCursor
	if decodeSignedCursor(h.authenticator.AdminToken, token, &cursor) != nil || cursor.Endpoint != "artifact-search" || cursor.RepositoryID != repositoryID || cursor.Format != string(format) || cursor.Query != query || cursor.Coordinate == "" || time.Now().UTC().Unix() >= cursor.ExpiresAt {
		return artifactSearchPosition{}, errors.New("invalid cursor")
	}
	return artifactSearchPosition{Coordinate: cursor.Coordinate, BuildNumber: cursor.BuildNumber}, nil
}

func (h hostedRepositoryAPIHandler) encodeRetentionDryRunCursor(repositoryID, policyVersion, coordinate, artifactID string) string {
	return encodeSignedCursor(h.authenticator.AdminToken, retentionDryRunPageCursor{Endpoint: "retention-dry-run", RepositoryID: repositoryID, PolicyVersion: policyVersion, Coordinate: coordinate, ArtifactID: artifactID, ExpiresAt: time.Now().UTC().Add(15 * time.Minute).Unix()})
}

func (h hostedRepositoryAPIHandler) decodeRetentionDryRunCursor(token, repositoryID, policyVersion string) (string, string, error) {
	if token == "" {
		return "", "", nil
	}
	var cursor retentionDryRunPageCursor
	if decodeSignedCursor(h.authenticator.AdminToken, token, &cursor) != nil || cursor.Endpoint != "retention-dry-run" || cursor.RepositoryID != repositoryID || cursor.PolicyVersion != policyVersion || cursor.Coordinate == "" || cursor.ArtifactID == "" || time.Now().UTC().Unix() >= cursor.ExpiresAt {
		return "", "", errors.New("invalid cursor")
	}
	return cursor.Coordinate, cursor.ArtifactID, nil
}

func (h hostedRepositoryAPIHandler) encodeTombstoneCursor(repositoryID string, format repository.Format, prefix, coordinate string) string {
	return encodeSignedCursor(h.authenticator.AdminToken, tombstonePageCursor{Endpoint: "tombstones", RepositoryID: repositoryID, Format: string(format), Prefix: prefix, Coordinate: coordinate, ExpiresAt: time.Now().UTC().Add(15 * time.Minute).Unix()})
}
func (h hostedRepositoryAPIHandler) decodeTombstoneCursor(token, repositoryID string, format repository.Format, prefix string) (string, error) {
	if token == "" {
		return "", nil
	}
	var cursor tombstonePageCursor
	if decodeSignedCursor(h.authenticator.AdminToken, token, &cursor) != nil || cursor.Endpoint != "tombstones" || cursor.RepositoryID != repositoryID || cursor.Format != string(format) || cursor.Prefix != prefix || cursor.Coordinate == "" || time.Now().UTC().Unix() >= cursor.ExpiresAt {
		return "", errors.New("invalid cursor")
	}
	return cursor.Coordinate, nil
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
		if !validProxyUpdate(repo.Format, updatedRepo.Endpoint, updatedRepo.AllowedHosts) {
			writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "endpoint must be a valid https URL and allowedHosts must be present for raw and conan proxies")
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
	if !hostedRepositoryName.MatchString(request.Name) || (request.Format != repository.FormatRaw && request.Format != repository.FormatOCI && request.Format != repository.FormatMaven && request.Format != repository.FormatConan) {
		return false
	}
	repoType := request.Type
	if repoType == "" {
		repoType = string(repository.RepositoryTypeHosted)
	}
	switch repository.RepositoryType(repoType) {
	case repository.RepositoryTypeHosted:
		// Hosted repositories serve local content only; an upstream endpoint or
		// egress allow-list would be meaningless and likely a client mistake.
		return request.Endpoint == "" && len(request.AllowedHosts) == 0
	case repository.RepositoryTypeProxy:
		if !validProxyEndpoint(request.Endpoint) {
			return false
		}
		// Raw and Conan proxies resolve upstream assets by host, so they must
		// declare which hosts they may egress to.
		if (request.Format == repository.FormatRaw || request.Format == repository.FormatConan) && len(request.AllowedHosts) == 0 {
			return false
		}
		return true
	default:
		return false
	}
}

func validProxyEndpoint(endpoint string) bool {
	parsed, err := url.Parse(endpoint)
	return err == nil && parsed.Scheme == "https" && parsed.Host != "" && parsed.User == nil
}

func validProxyUpdate(format repository.Format, endpoint string, allowedHosts []string) bool {
	if !validProxyEndpoint(endpoint) {
		return false
	}
	if (format == repository.FormatRaw || format == repository.FormatConan) && len(allowedHosts) == 0 {
		return false
	}
	return true
}

func writeHostedProblem(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"type": "about:blank", "title": http.StatusText(status), "status": status, "code": code, "message": message, "requestId": ""})
}
