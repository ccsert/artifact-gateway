package app

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	adminopenapi "github.com/ccsert/artifact-gateway/internal/admin/openapi"
	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

// authorizeGroupMembership reports whether the principal may manage the group
// made of these members. The platform tier always may; otherwise every member
// has to be a repository the principal administers. A member without a
// repository binding cannot be attributed to any repository, and a member whose
// repository cannot be read fails closed, so both need the platform tier.
// Nothing is cached: a member that is removed, a repository that is deleted, and
// a grant that is withdrawn all change the answer on the next request.
func (h repositoryManagementHandler) authorizeGroupMembership(w http.ResponseWriter, r *http.Request, principal Principal, groupName string, members []repository.GroupMember) bool {
	if code, message, blocked := accountStateProblem(principal.AccountStateReason()); blocked {
		writeHostedProblem(w, http.StatusForbidden, code, message)
		return false
	}
	if principal.Admin {
		return true
	}
	for _, member := range members {
		repositoryName := ""
		allowed := false
		if member.RepositoryID != "" {
			if repo, err := h.store.GetHostedRepository(r.Context(), member.RepositoryID); err == nil {
				repositoryName = repo.Name
				allowed = h.authorizer.Authorize(r.Context(), principal, repo, RepositoryAdmin).Allowed
			}
		}
		if allowed {
			continue
		}
		h.recordGroupMembershipDenial(r, groupName, repositoryName, principal.Actor)
		writeHostedProblem(w, http.StatusForbidden, "access_denied", "administrator permission is required for member \""+groupMemberLabel(member, repositoryName)+"\"")
		return false
	}
	return true
}

// groupMemberLabel names a member for an operator: its repository name when the
// repository is readable, its ID otherwise, and its position for a member that
// still carries no repository binding.
func groupMemberLabel(member repository.GroupMember, repositoryName string) string {
	if repositoryName != "" {
		return repositoryName
	}
	if member.RepositoryID != "" {
		return member.RepositoryID
	}
	return "position " + strconv.Itoa(member.Position)
}

func (h repositoryManagementHandler) recordGroupMembershipDenial(r *http.Request, groupName, repositoryName, actor string) {
	if h.audit == nil {
		return
	}
	_ = h.audit.RecordAudit(r.Context(), repository.AuditRecord{
		GroupName: groupName, Repository: repositoryName, Actor: actor,
		Outcome: repository.AuditAccessDenied, OccurredAt: time.Now().UTC(), Format: "management",
		Resource: "groups/" + groupName, Operation: "group.membership_denied", Status: http.StatusForbidden,
		CacheDisposition: "bypass",
	})
}

// administersGroupMembers reports, without answering or auditing, whether a
// principal outside the platform tier administers every member: the rule that
// lets it write the Group also lets it read the Group. A member without a
// readable repository fails closed.
func (h repositoryManagementHandler) administersGroupMembers(r *http.Request, principal Principal, members []repository.GroupMember) bool {
	for _, member := range members {
		if member.RepositoryID == "" {
			return false
		}
		repo, err := h.store.GetHostedRepository(r.Context(), member.RepositoryID)
		if err != nil || !h.authorizer.Authorize(r.Context(), principal, repo, RepositoryAdmin).Allowed {
			return false
		}
	}
	return true
}

// readGroup loads a Group for a read endpoint. The platform tier reads every
// Group; a caller who administers every member reads that Group, the same rule
// writes use. Any other caller gets 404, as if the Group did not exist, so a
// read does not reveal a Group the caller only partly administers.
func (h repositoryManagementHandler) readGroup(w http.ResponseWriter, r *http.Request, id string) (repository.HostedGroup, bool) {
	principal, ok := h.authenticate(w, r)
	if !ok {
		return repository.HostedGroup{}, false
	}
	if code, message, blocked := accountStateProblem(principal.AccountStateReason()); blocked {
		writeHostedProblem(w, http.StatusForbidden, code, message)
		return repository.HostedGroup{}, false
	}
	group, err := h.groups.GetHostedGroup(r.Context(), id)
	if errors.Is(err, repository.ErrNotFound) || (err == nil && !principal.Admin && !h.administersGroupMembers(r, principal, group.Members)) {
		writeHostedProblem(w, http.StatusNotFound, "not_found", "group not found")
		return repository.HostedGroup{}, false
	}
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "get group failed")
		return repository.HostedGroup{}, false
	}
	return group, true
}

// groupListScanBudget bounds how many Groups one filtered list request checks,
// so a caller who can read few Groups cannot make a single request scan the
// whole catalog.
var groupListScanBudget = 1000

// ListGroups lists every Group to the platform tier and, to anyone else, the
// Groups whose members it all administers. A filtered page may span several
// store batches and stops after groupListScanBudget Groups, so it can be short
// or empty while more remain; its token is the last Group scanned, so the next
// page resumes after it rather than after the last Group returned.
func (h repositoryManagementHandler) ListGroups(w http.ResponseWriter, r *http.Request, params adminopenapi.ListGroupsParams) {
	principal, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	if code, message, blocked := accountStateProblem(principal.AccountStateReason()); blocked {
		writeHostedProblem(w, http.StatusForbidden, code, message)
		return
	}
	limit, after := 50, ""
	if params.PageSize != nil {
		limit = int(*params.PageSize)
	}
	if params.PageToken != nil {
		after = string(*params.PageToken)
	}
	if principal.Admin {
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
		return
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	items := make([]repository.HostedGroup, 0, limit)
	scanned, hasMore, budget := after, false, groupListScanBudget
	for len(items) < limit && budget > 0 {
		batch, next, err := h.groups.ListHostedGroups(r.Context(), min(200, budget), scanned)
		if errors.Is(err, repository.ErrNotFound) {
			writeHostedProblem(w, http.StatusBadRequest, "invalid_page_token", "page token is invalid")
			return
		}
		if err != nil {
			writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "list groups failed")
			return
		}
		budget -= len(batch)
		hasMore = next != ""
		for index, candidate := range batch {
			scanned = candidate.ID
			if h.administersGroupMembers(r, principal, candidate.Members) {
				items = append(items, candidate)
			}
			if len(items) == limit {
				hasMore = index < len(batch)-1 || next != ""
				break
			}
		}
		if len(items) == limit || next == "" {
			break
		}
	}
	next := ""
	if hasMore {
		next = scanned
	}
	writeNativeMavenJSON(w, http.StatusOK, map[string]any{"items": items, "nextPageToken": next})
}

func (h repositoryManagementHandler) CreateGroup(w http.ResponseWriter, r *http.Request, params adminopenapi.CreateGroupParams) {
	principal, ok := h.authenticate(w, r)
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
	if !h.authorizeGroupMembership(w, r, principal, group.Name, group.Members) {
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
	if errors.Is(err, repository.ErrCargoGroupConflict) {
		writeHostedProblem(w, http.StatusConflict, "cargo_group_conflict", "Cargo group members contain conflicting crate versions")
		return
	}
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "create group failed")
		return
	}
	writeNativeMavenJSON(w, http.StatusCreated, created)
}

func (h repositoryManagementHandler) GetGroup(w http.ResponseWriter, r *http.Request, id adminopenapi.GroupId) {
	h.writeGroup(w, r, id.String())
}

func (h repositoryManagementHandler) ListGroupMembers(w http.ResponseWriter, r *http.Request, id adminopenapi.GroupId) {
	group, ok := h.readGroup(w, r, id.String())
	if !ok {
		return
	}
	writeNativeMavenJSON(w, 200, group.Members)
}

func (h repositoryManagementHandler) GetGroupCapacity(w http.ResponseWriter, r *http.Request, id adminopenapi.GroupId) {
	group, ok := h.readGroup(w, r, id.String())
	if !ok {
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

func (h repositoryManagementHandler) DeleteGroup(w http.ResponseWriter, r *http.Request, id adminopenapi.GroupId) {
	principal, ok := h.authenticate(w, r)
	if !ok {
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
	if !h.authorizeGroupMembership(w, r, principal, group.Name, group.Members) {
		return
	}
	if err = h.groups.DeleteHostedGroup(r.Context(), id.String()); err != nil {
		writeHostedProblem(w, 500, "internal_error", "delete group failed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h repositoryManagementHandler) ReplaceGroup(w http.ResponseWriter, r *http.Request, id adminopenapi.GroupId, params adminopenapi.ReplaceGroupParams) {
	principal, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	current, err := h.groups.GetHostedGroup(r.Context(), id.String())
	if errors.Is(err, repository.ErrNotFound) {
		writeHostedProblem(w, 404, "not_found", "group not found")
		return
	}
	if err != nil {
		writeHostedProblem(w, 500, "internal_error", "get group failed")
		return
	}
	var group repository.HostedGroup
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&group); err != nil || !h.validHostedGroup(r, group) {
		writeHostedProblem(w, 400, "invalid_request", "name, format, and members must be valid")
		return
	}
	// A membership change needs authority over the members it keeps or drops as
	// well as the ones it adds.
	if !h.authorizeGroupMembership(w, r, principal, group.Name, append(append([]repository.GroupMember{}, current.Members...), group.Members...)) {
		return
	}
	group.ID = id.String()
	updated, err := h.groups.ReplaceHostedGroup(r.Context(), group, string(params.IfMatch))
	h.writeGroupMutation(w, updated, err)
}

func (h repositoryManagementHandler) ReplaceGroupMembers(w http.ResponseWriter, r *http.Request, id adminopenapi.GroupId, params adminopenapi.ReplaceGroupMembersParams) {
	principal, ok := h.authenticate(w, r)
	if !ok {
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
	var members []repository.GroupMember
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&members); err != nil || !h.validHostedGroup(r, repository.HostedGroup{Name: group.Name, Format: group.Format, Members: members}) {
		writeHostedProblem(w, 400, "invalid_request", "members must be valid")
		return
	}
	// A membership change needs authority over the members it keeps or drops as
	// well as the ones it adds.
	if !h.authorizeGroupMembership(w, r, principal, group.Name, append(append([]repository.GroupMember{}, group.Members...), members...)) {
		return
	}
	updated, err := h.groups.ReplaceHostedGroupMembers(r.Context(), id.String(), members, string(params.IfMatch))
	h.writeGroupMutation(w, updated, err)
}

func (h repositoryManagementHandler) writeGroup(w http.ResponseWriter, r *http.Request, id string) {
	if group, ok := h.readGroup(w, r, id); ok {
		writeNativeMavenJSON(w, 200, group)
	}
}

func (h repositoryManagementHandler) writeGroupMutation(w http.ResponseWriter, group repository.HostedGroup, err error) {
	if errors.Is(err, repository.ErrNotFound) {
		writeHostedProblem(w, 404, "not_found", "group not found")
		return
	}
	if errors.Is(err, repository.ErrVersionConflict) {
		writeHostedProblem(w, 412, "version_conflict", "If-Match does not match current version")
		return
	}
	if errors.Is(err, repository.ErrCargoGroupConflict) {
		writeHostedProblem(w, http.StatusConflict, "cargo_group_conflict", "Cargo group members contain conflicting crate versions")
		return
	}
	if err != nil {
		writeHostedProblem(w, 500, "internal_error", "update group failed")
		return
	}
	writeNativeMavenJSON(w, 200, group)
}

func (h repositoryManagementHandler) validHostedGroup(r *http.Request, group repository.HostedGroup) bool {
	profile, supported := repository.FormatProfileFor(group.Format)
	if !hostedRepositoryName.MatchString(group.Name) || group.Format == repository.FormatConan && group.Name == "v2" || !supported || !profile.GroupSupported || len(group.Members) == 0 {
		return false
	}
	seen := map[string]bool{}
	for _, member := range group.Members {
		if _, err := uuid.Parse(member.RepositoryID); err != nil || member.Position < 0 || seen[member.RepositoryID] {
			return false
		}
		seen[member.RepositoryID] = true
		repo, err := h.store.GetHostedRepository(r.Context(), member.RepositoryID)
		if err != nil || repo.Format != group.Format || repo.State != repository.RepositoryActive ||
			(group.Format == repository.FormatAPT && repo.Type != repository.RepositoryTypeProxy) {
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
