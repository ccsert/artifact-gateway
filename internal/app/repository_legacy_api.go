package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/artifact-gateway/artifact-gateway/internal/v2contract"
)

type mavenAPIHandler struct {
	store         repository.MavenStore
	repositories  repository.HostedRepositoryStore
	authenticator Authenticator
}

type conanAPIHandler struct {
	store         GatewayStore
	authenticator Authenticator
}
type conanCacheInvalidationHandler struct {
	store         repository.ConanStore
	authenticator Authenticator
	cache         *ConanCache
}

func (h conanCacheInvalidationHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p, ok := h.authenticator.Authenticate(r.Header.Get("Authorization"))
	if !ok || !p.Admin {
		writeError(w, http.StatusForbidden, "forbidden", "administrator permission required")
		return
	}
	if h.cache == nil {
		writeError(w, http.StatusServiceUnavailable, "cache_unavailable", "Conan cache is not configured")
		return
	}
	defer func() { _ = r.Body.Close() }()
	var request struct{ Group, Path, Member, Endpoint string }
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
		return
	}
	if request.Group == "" || request.Path == "" || request.Member == "" || request.Endpoint == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "group, path, member and endpoint are required")
		return
	}
	if _, _, _, _, valid := parseConanPath(http.MethodGet, "/conan/v2/"+request.Group+"/conans/"+request.Path); !valid {
		writeError(w, http.StatusBadRequest, "invalid_path", "path must be a supported Conan read endpoint")
		return
	}
	group, err := h.store.GetConanGroup(r.Context(), request.Group)
	if errors.Is(err, repository.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "group not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "storage_error", "unable to read group")
		return
	}
	for _, member := range group.Members {
		if member.Name == request.Member && member.Endpoint == request.Endpoint {
			h.cache.Invalidate(r.Context(), request.Group, request.Path, member)
			w.WriteHeader(http.StatusNoContent)
			return
		}
	}
	writeError(w, http.StatusNotFound, "not_found", "member not found")
}

type rawAPIHandler struct {
	store         repository.RawStore
	repositories  repository.HostedRepositoryStore
	authenticator Authenticator
	cache         *RawCache
}

func (a rawAPIHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p, ok := a.authenticator.Authenticate(r.Header.Get("Authorization"))
	if !ok || !p.Admin {
		writeError(w, http.StatusForbidden, "forbidden", "administrator permission required")
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/raw/groups")
	if path == "" || path == "/" {
		if r.Method == http.MethodGet {
			groups, err := a.store.ListRawGroups(r.Context())
			if err != nil {
				writeError(w, http.StatusInternalServerError, "storage_error", "unable to list groups")
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"items": groups})
			return
		}
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		defer func() { _ = r.Body.Close() }()
		var g repository.Group
		if err := json.NewDecoder(r.Body).Decode(&g); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
			return
		}
		if err := validateRawGroup(g); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_group", err.Error())
			return
		}
		if err := validateGroupRepositoryBindings(r.Context(), a.repositories, g, repository.FormatRaw); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_group", err.Error())
			return
		}
		created, err := a.store.CreateRawGroup(r.Context(), g)
		if errors.Is(err, repository.ErrNameExists) {
			writeError(w, http.StatusConflict, "group_exists", "group name already exists")
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "storage_error", "unable to create group")
			return
		}
		writeJSON(w, http.StatusCreated, created)
		return
	}
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 1 && r.Method == http.MethodGet {
		g, err := a.store.GetRawGroup(r.Context(), parts[0])
		if errors.Is(err, repository.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "group not found")
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "storage_error", "unable to read group")
			return
		}
		writeJSON(w, http.StatusOK, g)
		return
	}
	if len(parts) == 2 && parts[1] == "disable" && r.Method == http.MethodPost {
		err := a.store.DisableRawGroup(r.Context(), parts[0])
		if errors.Is(err, repository.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "group not found")
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "storage_error", "unable to disable group")
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	writeError(w, http.StatusNotFound, "not_found", "resource not found")
}

type rawCacheInvalidationHandler struct {
	store         repository.RawStore
	authenticator Authenticator
	cache         *RawCache
}

type rawInvalidationRequest struct {
	Group    string `json:"group"`
	Path     string `json:"path"`
	Member   string `json:"member,omitempty"`
	Endpoint string `json:"endpoint,omitempty"`
}

func (h rawCacheInvalidationHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p, ok := h.authenticator.Authenticate(r.Header.Get("Authorization"))
	if !ok || !p.Admin {
		writeError(w, http.StatusForbidden, "forbidden", "administrator permission required")
		return
	}
	if h.cache == nil {
		writeError(w, http.StatusServiceUnavailable, "cache_unavailable", "Raw cache is not configured")
		return
	}
	defer func() { _ = r.Body.Close() }()
	var request rawInvalidationRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
		return
	}
	if !validRawGroupName(request.Group) {
		writeError(w, http.StatusBadRequest, "invalid_group", "group must be a non-reserved DNS label")
		return
	}
	resource, valid := v2contract.NewCanonicalResource(request.Path)
	if !valid || resource.String() != request.Path {
		writeError(w, http.StatusBadRequest, "invalid_path", "path must be canonical")
		return
	}
	if request.Member == "" || request.Endpoint == "" {
		writeError(w, http.StatusBadRequest, "invalid_member", "member and endpoint are required")
		return
	}
	group, err := h.store.GetRawGroup(r.Context(), request.Group)
	if errors.Is(err, repository.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "group not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "storage_error", "unable to read group")
		return
	}
	matched := false
	for _, member := range group.Members {
		if member.Name != request.Member || member.Endpoint != request.Endpoint {
			continue
		}
		h.cache.Invalidate(r.Context(), h.cache.Key(request.Group, resource.String(), member.Name, member.Endpoint))
		matched = true
	}
	if !matched {
		writeError(w, http.StatusNotFound, "not_found", "member not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a conanAPIHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p, ok := a.authenticator.Authenticate(r.Header.Get("Authorization"))
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "valid bearer token required")
		return
	}
	if !p.Admin {
		writeError(w, http.StatusForbidden, "forbidden", "administrator permission required")
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/conan/groups")
	if path == "" || path == "/" {
		if r.Method == http.MethodGet {
			groups, err := a.store.ListConanGroups(r.Context())
			if err != nil {
				writeError(w, http.StatusInternalServerError, "storage_error", "unable to list groups")
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"items": groups})
			return
		}
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		defer func() { _ = r.Body.Close() }()
		var group repository.Group
		if err := json.NewDecoder(r.Body).Decode(&group); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
			return
		}
		if err := validateConanGroup(group); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_group", err.Error())
			return
		}
		if err := validateGroupRepositoryBindings(r.Context(), a.store, group, repository.FormatConan); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_group", err.Error())
			return
		}
		created, err := a.store.CreateConanGroup(r.Context(), group)
		if errors.Is(err, repository.ErrNameExists) {
			writeError(w, http.StatusConflict, "group_exists", "group name already exists")
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "storage_error", "unable to create group")
			return
		}
		writeJSON(w, http.StatusCreated, created)
		return
	}
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 1 && r.Method == http.MethodGet {
		group, err := a.store.GetConanGroup(r.Context(), parts[0])
		if errors.Is(err, repository.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "group not found")
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "storage_error", "unable to read group")
			return
		}
		writeJSON(w, http.StatusOK, group)
		return
	}
	if len(parts) == 2 && parts[1] == "disable" && r.Method == http.MethodPost {
		err := a.store.DisableConanGroup(r.Context(), parts[0])
		if errors.Is(err, repository.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "group not found")
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "storage_error", "unable to disable group")
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	writeError(w, http.StatusNotFound, "not_found", "resource not found")
}

func (a mavenAPIHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	principal, ok := a.authenticator.Authenticate(r.Header.Get("Authorization"))
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "valid bearer token required")
		return
	}
	if !principal.Admin {
		writeError(w, http.StatusForbidden, "forbidden", "administrator permission required")
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/maven/groups")
	if path == "" || path == "/" {
		if r.Method == http.MethodGet {
			groups, err := a.store.ListMavenGroups(r.Context())
			if err != nil {
				writeError(w, http.StatusInternalServerError, "storage_error", "unable to list groups")
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"items": groups})
			return
		}
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		defer func() { _ = r.Body.Close() }()
		var group repository.Group
		if err := json.NewDecoder(r.Body).Decode(&group); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
			return
		}
		if err := validateGroup(group); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_group", err.Error())
			return
		}
		if err := validateGroupRepositoryBindings(r.Context(), a.repositories, group, repository.FormatMaven); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_group", err.Error())
			return
		}
		created, err := a.store.CreateMavenGroup(r.Context(), group)
		if errors.Is(err, repository.ErrNameExists) {
			writeError(w, http.StatusConflict, "group_exists", "group name already exists")
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "storage_error", "unable to create group")
			return
		}
		writeJSON(w, http.StatusCreated, created)
		return
	}
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 1 && r.Method == http.MethodGet {
		group, err := a.store.GetMavenGroup(r.Context(), parts[0])
		if errors.Is(err, repository.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "group not found")
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "storage_error", "unable to read group")
			return
		}
		writeJSON(w, http.StatusOK, group)
		return
	}
	if len(parts) == 2 && parts[1] == "disable" && r.Method == http.MethodPost {
		err := a.store.DisableMavenGroup(r.Context(), parts[0])
		if errors.Is(err, repository.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "group not found")
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "storage_error", "unable to disable group")
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	writeError(w, http.StatusNotFound, "not_found", "resource not found")
}

type apiHandler struct {
	store         repository.Store
	repositories  repository.HostedRepositoryStore
	resolver      Resolver
	authenticator Authenticator
}

func (a apiHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	principal, ok := a.authenticator.Authenticate(r.Header.Get("Authorization"))
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "valid bearer token required")
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/oci/groups")
	if path == "" || path == "/" {
		if r.Method != http.MethodPost && r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		if !a.requireAdmin(w, principal) {
			return
		}
		if r.Method == http.MethodGet {
			a.list(w, r)
			return
		}
		a.create(w, r)
		return
	}
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 1 && r.Method == http.MethodGet {
		if !a.requireAdmin(w, principal) {
			return
		}
		a.get(w, r, parts[0])
		return
	}
	if len(parts) == 2 && parts[1] == "disable" && r.Method == http.MethodPost {
		if !a.requireAdmin(w, principal) {
			return
		}
		a.disable(w, r, parts[0])
		return
	}
	if len(parts) == 2 && parts[1] == "resolve" && r.Method == http.MethodGet {
		a.resolve(w, r, parts[0], principal)
		return
	}
	writeError(w, http.StatusNotFound, "not_found", "resource not found")
}

func (a apiHandler) requireAdmin(w http.ResponseWriter, principal Principal) bool {
	if principal.Admin {
		return true
	}
	writeError(w, http.StatusForbidden, "forbidden", "administrator permission required")
	return false
}

func (a apiHandler) create(w http.ResponseWriter, r *http.Request) {
	defer func() { _ = r.Body.Close() }()
	var group repository.Group
	if err := json.NewDecoder(r.Body).Decode(&group); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
		return
	}
	if err := validateGroup(group); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_group", err.Error())
		return
	}
	if err := validateGroupRepositoryBindings(r.Context(), a.repositories, group, repository.FormatOCI); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_group", err.Error())
		return
	}
	created, err := a.store.CreateGroup(r.Context(), group)
	if errors.Is(err, repository.ErrNameExists) {
		writeError(w, http.StatusConflict, "group_exists", "group name already exists")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "storage_error", "unable to create group")
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (a apiHandler) get(w http.ResponseWriter, r *http.Request, name string) {
	group, err := a.store.GetGroup(r.Context(), name)
	if errors.Is(err, repository.ErrNotFound) {
		writeError(w, 404, "not_found", "group not found")
		return
	}
	if err != nil {
		writeError(w, 500, "storage_error", "unable to read group")
		return
	}
	writeJSON(w, 200, group)
}

func (a apiHandler) list(w http.ResponseWriter, r *http.Request) {
	groups, err := a.store.ListGroups(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "storage_error", "unable to list groups")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": groups})
}
func (a apiHandler) disable(w http.ResponseWriter, r *http.Request, name string) {
	err := a.store.DisableGroup(r.Context(), name)
	if errors.Is(err, repository.ErrNotFound) {
		writeError(w, 404, "not_found", "group not found")
		return
	}
	if err != nil {
		writeError(w, 500, "storage_error", "unable to disable group")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (a apiHandler) resolve(w http.ResponseWriter, r *http.Request, name string, principal Principal) {
	repositoryName := r.URL.Query().Get("repository")
	if repositoryName == "" {
		a.resolver.Metrics.failed.Add(1)
		writeError(w, 400, "invalid_repository", "repository query parameter is required")
		return
	}
	if !a.authenticator.CanReadRepository(principal, repositoryName) {
		if err := a.store.RecordAudit(r.Context(), repository.AuditRecord{GroupName: name, Repository: repositoryName, Outcome: repository.AuditAccessDenied, Actor: principal.Actor, OccurredAt: time.Now().UTC()}); err != nil {
			writeError(w, http.StatusInternalServerError, "storage_error", "unable to record repository audit")
			return
		}
		a.resolver.Metrics.failed.Add(1)
		writeError(w, http.StatusForbidden, "forbidden", "repository read permission required")
		return
	}
	member, err := a.resolver.Resolve(r.Context(), name, repositoryName, principal.Actor)
	if errors.Is(err, repository.ErrDisabled) {
		writeError(w, 409, "group_disabled", "group is disabled")
		return
	}
	if errors.Is(err, repository.ErrNotFound) {
		writeError(w, 404, "not_found", "no member can serve repository")
		return
	}
	if err != nil {
		writeError(w, 500, "resolver_error", "unable to resolve repository")
		return
	}
	writeJSON(w, 200, member)
}

func validateGroup(group repository.Group) error {
	if group.Name == "" || strings.Contains(group.Name, "/") {
		return errors.New("name must be a non-empty OCI namespace")
	}
	if len(group.Members) == 0 {
		return errors.New("at least one member is required")
	}
	positions := make(map[int]bool, len(group.Members))
	for _, member := range group.Members {
		if member.Name == "" || member.Endpoint == "" {
			return errors.New("member name and endpoint are required")
		}
		if member.Type != repository.MemberHosted && member.Type != repository.MemberProxy {
			return errors.New("member type must be hosted or proxy")
		}
		if member.Position < 0 || positions[member.Position] {
			return errors.New("member positions must be unique non-negative integers")
		}
		positions[member.Position] = true
	}
	for position := range group.Members {
		if !positions[position] {
			return errors.New("member positions must start at zero and be contiguous")
		}
	}
	return nil
}

func validateRawGroup(group repository.Group) error {
	if err := validateGroup(group); err != nil {
		return err
	}
	if !validRawGroupName(group.Name) {
		return errors.New("name must be a non-reserved DNS label")
	}
	if group.CacheQuotaBytes <= 0 {
		return errors.New("cacheQuotaBytes must be a positive integer")
	}
	for _, member := range group.Members {
		if member.Type == repository.MemberProxy && len(member.AllowedHosts) == 0 {
			return errors.New("proxy members require a non-empty allowlist")
		}
	}
	return nil
}

func validateConanGroup(group repository.Group) error {
	if err := validateGroup(group); err != nil {
		return err
	}
	if !validRawGroupName(group.Name) {
		return errors.New("name must be a non-reserved DNS label")
	}
	if group.CacheQuotaBytes < 0 {
		return errors.New("cacheQuotaBytes must be a positive integer")
	}
	for _, member := range group.Members {
		if member.Type == repository.MemberProxy && len(member.AllowedHosts) == 0 {
			return errors.New("proxy members require a non-empty allowlist")
		}
	}
	return nil
}

func validateGroupRepositoryBindings(ctx context.Context, store repository.HostedRepositoryStore, group repository.Group, format repository.Format) error {
	if store == nil {
		return errors.New("repository binding validation is unavailable")
	}
	for _, member := range group.Members {
		if member.RepositoryID == "" {
			continue
		}
		repo, err := store.GetHostedRepository(ctx, member.RepositoryID)
		if err != nil || repo.Format != format || repo.State != repository.RepositoryActive {
			return fmt.Errorf("member repositoryId must reference an active %s repository", format)
		}
	}
	return nil
}

func validRawGroupName(name string) bool {
	if len(name) == 0 || len(name) > 63 || rawReservedGroupNames[name] {
		return false
	}
	if name[0] == '-' || name[len(name)-1] == '-' {
		return false
	}
	for _, r := range name {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
			return false
		}
	}
	return true
}

var rawReservedGroupNames = map[string]bool{
	"api": true, "metrics": true, "livez": true, "readyz": true, "operations": true,
	"v2": true, "maven": true, "raw": true, "conan": true,
}
