package app

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"

	adminopenapi "github.com/artifact-gateway/artifact-gateway/internal/admin/openapi"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

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
	profile, supported := repository.FormatProfileFor(group.Format)
	if !hostedRepositoryName.MatchString(group.Name) || !supported || !profile.GroupSupported || len(group.Members) == 0 {
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
