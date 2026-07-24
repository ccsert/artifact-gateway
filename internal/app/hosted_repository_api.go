package app

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	adminopenapi "github.com/artifact-gateway/artifact-gateway/internal/admin/openapi"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

var hostedRepositoryName = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)

// hostedRepositoryAPIHandler is the versioned management surface described by
// native-hosted-v1.json. It intentionally does not reuse the V2 Group routes.
type hostedRepositoryAPIHandler struct {
	store         repository.HostedRepositoryStore
	authenticator Authenticator
}

type createHostedRepositoryRequest struct {
	Name   string            `json:"name"`
	Format repository.Format `json:"format"`
}

type repositoryPage struct {
	Items         []repository.HostedRepository `json:"items"`
	NextPageToken string                        `json:"nextPageToken,omitempty"`
}

type repositoryPageCursor struct {
	Endpoint, ID string
	ExpiresAt    int64
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
	principal, ok := h.authenticator.Authenticate(r.Header.Get("Authorization"))
	if !ok || !principal.Admin {
		writeHostedProblem(w, http.StatusUnauthorized, "access_denied", "administrator authentication is required")
		return Principal{}, false
	}
	return principal, true
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

func (h generatedRepositoryAPIAdapter) DeleteRepository(w http.ResponseWriter, r *http.Request, id adminopenapi.RepositoryId) {
	if _, ok := h.authorize(w, r); ok {
		h.disable(w, r, id.String())
	}
}

func (h generatedRepositoryAPIAdapter) GetRepository(w http.ResponseWriter, r *http.Request, id adminopenapi.RepositoryId) {
	if _, ok := h.authorize(w, r); ok {
		h.get(w, r, id.String())
	}
}

func (h generatedRepositoryAPIAdapter) ListGrants(w http.ResponseWriter, r *http.Request, repositoryID adminopenapi.RepositoryId) {
	if _, ok := h.authorize(w, r); !ok {
		return
	}
	set, err := h.grants.GetRepositoryGrants(r.Context(), repositoryID.String())
	if errors.Is(err, repository.ErrNotFound) {
		writeHostedProblem(w, http.StatusNotFound, "not_found", "repository not found")
		return
	}
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "list grants failed")
		return
	}
	w.Header().Set("ETag", set.Version)
	writeNativeMavenJSON(w, http.StatusOK, set.Grants)
}

func (h generatedRepositoryAPIAdapter) ReplaceGrants(w http.ResponseWriter, r *http.Request, repositoryID adminopenapi.RepositoryId, params adminopenapi.ReplaceGrantsParams) {
	if _, ok := h.authorize(w, r); !ok {
		return
	}
	var grants []repository.RepositoryGrant
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&grants); err != nil || !validRepositoryGrants(grants) {
		writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "grants must contain unique principals and valid scopes")
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
}

func validRepositoryGrants(grants []repository.RepositoryGrant) bool {
	validScopes := map[string]bool{"repositories:read": true, "repositories:write": true, "repositories:admin": true}
	principals := map[string]bool{}
	for _, grant := range grants {
		if strings.TrimSpace(grant.Principal) == "" || principals[grant.Principal] || len(grant.Scopes) == 0 {
			return false
		}
		principals[grant.Principal] = true
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

func (h generatedRepositoryAPIAdapter) GetRetentionPolicy(w http.ResponseWriter, r *http.Request, repositoryID adminopenapi.RepositoryId) {
	if _, ok := h.authorize(w, r); !ok {
		return
	}
	policy, err := h.retentionPolicies.GetRepositoryRetentionPolicy(r.Context(), repositoryID.String())
	if errors.Is(err, repository.ErrNotFound) {
		writeHostedProblem(w, http.StatusNotFound, "not_found", "repository not found")
		return
	}
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "get retention policy failed")
		return
	}
	writeNativeMavenJSON(w, http.StatusOK, policy)
}

func (h generatedRepositoryAPIAdapter) ReplaceRetentionPolicy(w http.ResponseWriter, r *http.Request, repositoryID adminopenapi.RepositoryId, params adminopenapi.ReplaceRetentionPolicyParams) {
	if _, ok := h.authorize(w, r); !ok {
		return
	}
	var policy repository.RepositoryRetentionPolicy
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&policy); err != nil || policy.Version == "" || policy.KeepDays < 1 || policy.MinimumVersions < 1 {
		writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "version, keepDays, and minimumVersions must be valid")
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
		Name    string                   `json:"name"`
		Format  repository.Format        `json:"format"`
		Members []repository.GroupMember `json:"members"`
	}{group.Name, group.Format, group.Members})
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
	if !hostedRepositoryName.MatchString(group.Name) || (group.Format != repository.FormatOCI && group.Format != repository.FormatMaven && group.Format != repository.FormatRaw) || len(group.Members) == 0 {
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
	h.withSessionAdmin(w, r, func(principal Principal) {
		h.sessions.createWithIdempotencyKey(w, r, principal, repositoryID.String(), string(params.IdempotencyKey))
	})
}

func (h generatedRepositoryAPIAdapter) ListArtifacts(w http.ResponseWriter, r *http.Request, repositoryID adminopenapi.RepositoryId, _ adminopenapi.ListArtifactsParams) {
	h.withSessionAdmin(w, r, func(Principal) {
		h.sessions.listArtifacts(w, r, repositoryID.String())
	})
}

func (h generatedRepositoryAPIAdapter) GetPublishSession(w http.ResponseWriter, r *http.Request, sessionID adminopenapi.SessionId) {
	h.withSessionAdmin(w, r, func(Principal) {
		h.sessions.getSession(w, r, sessionID.String())
	})
}

func (h generatedRepositoryAPIAdapter) UploadPublishObject(w http.ResponseWriter, r *http.Request, sessionID adminopenapi.SessionId, objectName string) {
	h.withSessionAdmin(w, r, func(Principal) {
		h.sessions.upload(w, r, sessionID.String(), objectName)
	})
}

func (h generatedRepositoryAPIAdapter) CommitPublishSession(w http.ResponseWriter, r *http.Request, sessionID adminopenapi.SessionId) {
	h.withSessionAdmin(w, r, func(Principal) {
		h.sessions.commit(w, r, sessionID.String())
	})
}

func (h generatedRepositoryAPIAdapter) withSessionAdmin(w http.ResponseWriter, r *http.Request, operation func(Principal)) {
	if principal, ok := h.sessions.admin(r); ok {
		operation(principal)
		return
	}
	writeHostedProblem(w, http.StatusUnauthorized, "access_denied", "administrator authentication is required")
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
		writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "name and format must be valid")
		return
	}
	payload, _ := json.Marshal(request)
	digest := sha256.Sum256(payload)
	repo, _, err := h.store.CreateHostedRepositoryIdempotently(r.Context(), repository.HostedRepository{ID: uuid.NewString(), Name: request.Name, Format: request.Format}, principal.Actor, key, base64.RawURLEncoding.EncodeToString(digest[:]))
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
	_ = json.NewEncoder(w).Encode(repo)
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
	_ = json.NewEncoder(w).Encode(repositoryPage{Items: items, NextPageToken: nextToken})
}

func (h hostedRepositoryAPIHandler) encodeCursor(id string) string {
	payload, _ := json.Marshal(repositoryPageCursor{Endpoint: "repositories", ID: id, ExpiresAt: time.Now().UTC().Add(15 * time.Minute).Unix()})
	mac := hmac.New(sha256.New, []byte(h.authenticator.AdminToken))
	_, _ = mac.Write(payload)
	return base64.RawURLEncoding.EncodeToString(append(payload, mac.Sum(nil)...))
}

func (h hostedRepositoryAPIHandler) decodeCursor(token string) (string, error) {
	if token == "" {
		return "", nil
	}
	encoded, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(encoded) <= sha256.Size {
		return "", errors.New("invalid cursor")
	}
	payload, signature := encoded[:len(encoded)-sha256.Size], encoded[len(encoded)-sha256.Size:]
	mac := hmac.New(sha256.New, []byte(h.authenticator.AdminToken))
	_, _ = mac.Write(payload)
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return "", errors.New("invalid cursor")
	}
	var cursor repositoryPageCursor
	if err := json.Unmarshal(payload, &cursor); err != nil || cursor.Endpoint != "repositories" || cursor.ID == "" || time.Now().UTC().Unix() >= cursor.ExpiresAt {
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
	_ = json.NewEncoder(w).Encode(repo)
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

func validHostedRepository(request createHostedRepositoryRequest) bool {
	return hostedRepositoryName.MatchString(request.Name) && (request.Format == repository.FormatRaw || request.Format == repository.FormatOCI || request.Format == repository.FormatMaven)
}

func writeHostedProblem(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"type": "about:blank", "title": http.StatusText(status), "status": status, "code": code, "message": message, "requestId": ""})
}
