package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	adminopenapi "github.com/artifact-gateway/artifact-gateway/internal/admin/openapi"
	"github.com/artifact-gateway/artifact-gateway/internal/authorization"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

func (h generatedRepositoryAPIAdapter) GetRepositoryEffectiveAccess(w http.ResponseWriter, r *http.Request, repositoryID adminopenapi.RepositoryId, params adminopenapi.GetRepositoryEffectiveAccessParams) {
	principal, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	simulated := false
	if params.Actor == nil && params.Role != nil {
		writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "role requires an actor to simulate")
		return
	}
	if params.Actor != nil {
		actor := strings.TrimSpace(*params.Actor)
		if actor == "" {
			writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "actor must not be empty")
			return
		}
		if !principal.Admin {
			writeHostedProblem(w, http.StatusForbidden, "access_denied", "administrator permission is required to simulate another principal")
			return
		}
		role := Role("")
		if params.Role != nil {
			role = Role(*params.Role)
		}
		principal = Principal{
			Actor: actor, Role: role, Admin: role == RoleAdmin,
			AuthenticationKind: simulatedAuthenticationKind(actor),
		}
		simulated = true
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
	resource := ""
	if params.Resource != nil {
		resource = strings.TrimSpace(*params.Resource)
	}
	writeNativeMavenJSON(w, http.StatusOK, h.repositoryEffectiveAccess(r.Context(), principal, repo, resource, simulated))
}

func simulatedAuthenticationKind(actor string) authorization.AuthenticationKind {
	switch {
	case strings.HasPrefix(actor, "user:"):
		return authorization.AuthenticationLocalSession
	case strings.HasPrefix(actor, "api-key:"):
		return authorization.AuthenticationAPIKey
	default:
		return authorization.AuthenticationOIDC
	}
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

func (h generatedRepositoryAPIAdapter) repositoryEffectiveAccess(ctx context.Context, principal Principal, repo repository.HostedRepository, resource string, simulated bool) adminopenapi.RepositoryEffectiveAccess {
	decision := func(operation RepositoryOperation) adminopenapi.EffectiveAccessDecision {
		return effectiveAccessDecision(h.authorizer.AuthorizeResource(ctx, principal, repo, operation, resource))
	}
	anonymousReason := anonymousRepositoryReason(ctx, h.store, repo)
	response := adminopenapi.RepositoryEffectiveAccess{
		Actor: principal.Actor, Identity: currentIdentityResponse(principal),
		Resource: resource, Simulated: simulated,
		AnonymousRead: adminopenapi.EffectiveAccessDecision{
			Allowed: anonymousReason == "repository_anonymous_read_enabled",
			Source:  "anonymous_policy",
			Reason:  anonymousReason,
		},
		Permissions: adminopenapi.EffectiveAccessPermissions{
			Read:         decision(RepositoryRead),
			Write:        decision(RepositoryWrite),
			Admin:        decision(RepositoryAdmin),
			Intelligence: decision(RepositoryIntelligence),
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

func authorizationTemplateResponse(template repository.AuthorizationTemplate) adminopenapi.AuthorizationTemplate {
	grants := make([]adminopenapi.AuthorizationTemplateGrant, 0, len(template.Grants))
	for _, grant := range template.Grants {
		item := adminopenapi.AuthorizationTemplateGrant{Principal: grant.Principal}
		for _, scope := range grant.Scopes {
			item.Scopes = append(item.Scopes, adminopenapi.AuthorizationTemplateGrantScopes(scope))
		}
		if grant.ResourcePrefix != "" {
			prefix := grant.ResourcePrefix
			item.ResourcePrefix = &prefix
		}
		grants = append(grants, item)
	}
	description := template.Description
	return adminopenapi.AuthorizationTemplate{
		Id: templateID(template.ID), Name: template.Name, Description: &description,
		Grants: grants, Version: template.Version, CreatedAt: template.CreatedAt, UpdatedAt: template.UpdatedAt,
	}
}

func authorizationRoleResponse(role repository.AuthorizationRole) adminopenapi.AuthorizationRole {
	scopes := make([]adminopenapi.AuthorizationRoleScopes, 0, len(role.Scopes))
	for _, scope := range role.Scopes {
		scopes = append(scopes, adminopenapi.AuthorizationRoleScopes(scope))
	}
	description := role.Description
	return adminopenapi.AuthorizationRole{
		Id: templateID(role.ID), Name: role.Name, Description: &description,
		Scopes: scopes, Version: role.Version, CreatedAt: role.CreatedAt, UpdatedAt: role.UpdatedAt,
	}
}

func authorizationRoleScopes(body *adminopenapi.AuthorizationRoleWritable) []string {
	if body == nil {
		return nil
	}
	scopes := make([]string, 0, len(body.Scopes))
	for _, scope := range body.Scopes {
		scopes = append(scopes, string(scope))
	}
	return scopes
}

func validAuthorizationScopes(scopes []string) bool {
	if len(scopes) == 0 || len(scopes) > 4 {
		return false
	}
	validScopes := map[string]bool{"repositories:read": true, "repositories:write": true, "repositories:admin": true, "repositories:intelligence": true}
	seen := map[string]bool{}
	for _, scope := range scopes {
		if !validScopes[scope] || seen[scope] {
			return false
		}
		seen[scope] = true
	}
	return true
}

func templateID(id string) uuid.UUID {
	parsed, err := uuid.Parse(id)
	if err != nil {
		return uuid.Nil
	}
	return parsed
}

func authorizationTemplateGrants(body *adminopenapi.AuthorizationTemplateWritable) []repository.RepositoryGrant {
	if body == nil {
		return nil
	}
	grants := make([]repository.RepositoryGrant, 0, len(body.Grants))
	for _, grant := range body.Grants {
		prefix := ""
		if grant.ResourcePrefix != nil {
			prefix = *grant.ResourcePrefix
		}
		scopes := make([]string, 0, len(grant.Scopes))
		for _, scope := range grant.Scopes {
			scopes = append(scopes, string(scope))
		}
		grants = append(grants, repository.RepositoryGrant{Principal: grant.Principal, Scopes: scopes, ResourcePrefix: prefix})
	}
	return grants
}

func validAuthorizationTemplateGrants(grants []repository.RepositoryGrant) bool {
	if len(grants) > 500 {
		return false
	}
	keys := map[string]bool{}
	for _, grant := range grants {
		if strings.TrimSpace(grant.Principal) == "" || len(grant.Principal) > 512 || strings.ContainsAny(grant.Principal, "\x00\r\n") || len(grant.Scopes) == 0 || len(grant.ResourcePrefix) > 255 || strings.ContainsAny(grant.ResourcePrefix, "\x00\r\n") {
			return false
		}
		key := grant.Principal + "\x00" + grant.ResourcePrefix
		if keys[key] {
			return false
		}
		keys[key] = true
		if !validAuthorizationScopes(grant.Scopes) {
			return false
		}
	}
	return true
}

func (h generatedRepositoryAPIAdapter) ListAuthorizationRoles(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.authorize(w, r); !ok {
		return
	}
	items, err := h.authorizationRoles.ListAuthorizationRoles(r.Context())
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "list authorization roles failed")
		return
	}
	response := make(adminopenapi.AuthorizationRoleList, 0, len(items))
	for _, item := range items {
		response = append(response, authorizationRoleResponse(item))
	}
	writeNativeMavenJSON(w, http.StatusOK, response)
}

func (h generatedRepositoryAPIAdapter) CreateAuthorizationRole(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.authorize(w, r); !ok {
		return
	}
	var body adminopenapi.AuthorizationRoleWritable
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "invalid authorization role body")
		return
	}
	scopes := authorizationRoleScopes(&body)
	if strings.TrimSpace(body.Name) == "" || len(strings.TrimSpace(body.Name)) > 128 || (body.Description != nil && len(*body.Description) > 1000) || !validAuthorizationScopes(scopes) {
		writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "role name and scopes must be valid")
		return
	}
	description := ""
	if body.Description != nil {
		description = *body.Description
	}
	role, err := h.authorizationRoles.CreateAuthorizationRole(r.Context(), repository.AuthorizationRole{Name: body.Name, Description: description, Scopes: scopes})
	if errors.Is(err, repository.ErrAuthorizationRoleNameExists) {
		writeHostedProblem(w, http.StatusConflict, "name_exists", "authorization role name already exists")
		return
	}
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "create authorization role failed")
		return
	}
	writeNativeMavenJSON(w, http.StatusCreated, authorizationRoleResponse(role))
}

func (h generatedRepositoryAPIAdapter) GetAuthorizationRole(w http.ResponseWriter, r *http.Request, roleID adminopenapi.AuthorizationRoleId) {
	if _, ok := h.authorize(w, r); !ok {
		return
	}
	role, err := h.authorizationRoles.GetAuthorizationRole(r.Context(), roleID.String())
	if errors.Is(err, repository.ErrNotFound) {
		writeHostedProblem(w, http.StatusNotFound, "not_found", "authorization role not found")
		return
	}
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "get authorization role failed")
		return
	}
	w.Header().Set("ETag", role.Version)
	writeNativeMavenJSON(w, http.StatusOK, authorizationRoleResponse(role))
}

func (h generatedRepositoryAPIAdapter) UpdateAuthorizationRole(w http.ResponseWriter, r *http.Request, roleID adminopenapi.AuthorizationRoleId, params adminopenapi.UpdateAuthorizationRoleParams) {
	if _, ok := h.authorize(w, r); !ok {
		return
	}
	var body adminopenapi.AuthorizationRoleWritable
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "invalid authorization role body")
		return
	}
	scopes := authorizationRoleScopes(&body)
	if strings.TrimSpace(body.Name) == "" || len(strings.TrimSpace(body.Name)) > 128 || (body.Description != nil && len(*body.Description) > 1000) || !validAuthorizationScopes(scopes) {
		writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "role name and scopes must be valid")
		return
	}
	description := ""
	if body.Description != nil {
		description = *body.Description
	}
	role, err := h.authorizationRoles.UpdateAuthorizationRole(r.Context(), repository.AuthorizationRole{ID: roleID.String(), Name: body.Name, Description: description, Scopes: scopes}, string(params.IfMatch))
	if errors.Is(err, repository.ErrNotFound) {
		writeHostedProblem(w, http.StatusNotFound, "not_found", "authorization role not found")
		return
	}
	if errors.Is(err, repository.ErrVersionConflict) {
		writeHostedProblem(w, http.StatusPreconditionFailed, "version_conflict", "If-Match does not match current role version")
		return
	}
	if errors.Is(err, repository.ErrAuthorizationRoleNameExists) {
		writeHostedProblem(w, http.StatusConflict, "name_exists", "authorization role name already exists")
		return
	}
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "update authorization role failed")
		return
	}
	w.Header().Set("ETag", role.Version)
	writeNativeMavenJSON(w, http.StatusOK, authorizationRoleResponse(role))
}

func (h generatedRepositoryAPIAdapter) DeleteAuthorizationRole(w http.ResponseWriter, r *http.Request, roleID adminopenapi.AuthorizationRoleId) {
	if _, ok := h.authorize(w, r); !ok {
		return
	}
	err := h.authorizationRoles.DeleteAuthorizationRole(r.Context(), roleID.String())
	if errors.Is(err, repository.ErrNotFound) {
		writeHostedProblem(w, http.StatusNotFound, "not_found", "authorization role not found")
		return
	}
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "delete authorization role failed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h generatedRepositoryAPIAdapter) ListAuthorizationTemplates(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.authorize(w, r); !ok {
		return
	}
	items, err := h.templates.ListAuthorizationTemplates(r.Context())
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "list authorization templates failed")
		return
	}
	response := make(adminopenapi.AuthorizationTemplateList, 0, len(items))
	for _, item := range items {
		response = append(response, authorizationTemplateResponse(item))
	}
	writeNativeMavenJSON(w, http.StatusOK, response)
}

func (h generatedRepositoryAPIAdapter) CreateAuthorizationTemplate(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.authorize(w, r); !ok {
		return
	}
	var body adminopenapi.AuthorizationTemplateWritable
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 128<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "invalid authorization template body")
		return
	}
	grants := authorizationTemplateGrants(&body)
	if strings.TrimSpace(body.Name) == "" || len(strings.TrimSpace(body.Name)) > 128 || (body.Description != nil && len(*body.Description) > 1000) || !validAuthorizationTemplateGrants(grants) {
		writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "template name and grants must be valid")
		return
	}
	description := ""
	if body.Description != nil {
		description = *body.Description
	}
	template, err := h.templates.CreateAuthorizationTemplate(r.Context(), repository.AuthorizationTemplate{Name: body.Name, Description: description, Grants: grants})
	if errors.Is(err, repository.ErrTemplateNameExists) {
		writeHostedProblem(w, http.StatusConflict, "name_exists", "authorization template name already exists")
		return
	}
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "create authorization template failed")
		return
	}
	writeNativeMavenJSON(w, http.StatusCreated, authorizationTemplateResponse(template))
}

func (h generatedRepositoryAPIAdapter) GetAuthorizationTemplate(w http.ResponseWriter, r *http.Request, templateID adminopenapi.AuthorizationTemplateId) {
	if _, ok := h.authorize(w, r); !ok {
		return
	}
	template, err := h.templates.GetAuthorizationTemplate(r.Context(), templateID.String())
	if errors.Is(err, repository.ErrNotFound) {
		writeHostedProblem(w, http.StatusNotFound, "not_found", "authorization template not found")
		return
	}
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "get authorization template failed")
		return
	}
	w.Header().Set("ETag", template.Version)
	writeNativeMavenJSON(w, http.StatusOK, authorizationTemplateResponse(template))
}

func (h generatedRepositoryAPIAdapter) UpdateAuthorizationTemplate(w http.ResponseWriter, r *http.Request, templateID adminopenapi.AuthorizationTemplateId, params adminopenapi.UpdateAuthorizationTemplateParams) {
	if _, ok := h.authorize(w, r); !ok {
		return
	}
	var body adminopenapi.AuthorizationTemplateWritable
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 128<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "invalid authorization template body")
		return
	}
	grants := authorizationTemplateGrants(&body)
	if strings.TrimSpace(body.Name) == "" || len(strings.TrimSpace(body.Name)) > 128 || (body.Description != nil && len(*body.Description) > 1000) || !validAuthorizationTemplateGrants(grants) {
		writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "template name and grants must be valid")
		return
	}
	description := ""
	if body.Description != nil {
		description = *body.Description
	}
	template, err := h.templates.UpdateAuthorizationTemplate(r.Context(), repository.AuthorizationTemplate{ID: templateID.String(), Name: body.Name, Description: description, Grants: grants}, string(params.IfMatch))
	if errors.Is(err, repository.ErrNotFound) {
		writeHostedProblem(w, http.StatusNotFound, "not_found", "authorization template not found")
		return
	}
	if errors.Is(err, repository.ErrVersionConflict) {
		writeHostedProblem(w, http.StatusPreconditionFailed, "version_conflict", "If-Match does not match current template version")
		return
	}
	if errors.Is(err, repository.ErrTemplateNameExists) {
		writeHostedProblem(w, http.StatusConflict, "name_exists", "authorization template name already exists")
		return
	}
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "update authorization template failed")
		return
	}
	w.Header().Set("ETag", template.Version)
	writeNativeMavenJSON(w, http.StatusOK, authorizationTemplateResponse(template))
}

func (h generatedRepositoryAPIAdapter) DeleteAuthorizationTemplate(w http.ResponseWriter, r *http.Request, templateID adminopenapi.AuthorizationTemplateId) {
	if _, ok := h.authorize(w, r); !ok {
		return
	}
	err := h.templates.DeleteAuthorizationTemplate(r.Context(), templateID.String())
	if errors.Is(err, repository.ErrNotFound) {
		writeHostedProblem(w, http.StatusNotFound, "not_found", "authorization template not found")
		return
	}
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "delete authorization template failed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h generatedRepositoryAPIAdapter) ApplyAuthorizationTemplate(w http.ResponseWriter, r *http.Request, templateID adminopenapi.AuthorizationTemplateId, params adminopenapi.ApplyAuthorizationTemplateParams) {
	if _, ok := h.authorize(w, r); !ok {
		return
	}
	var body adminopenapi.ApplyAuthorizationTemplate
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil || body.RepositoryId == uuid.Nil {
		writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "repositoryId is required")
		return
	}
	repo, err := h.store.GetHostedRepository(r.Context(), body.RepositoryId.String())
	if errors.Is(err, repository.ErrNotFound) {
		writeHostedProblem(w, http.StatusNotFound, "not_found", "repository not found")
		return
	}
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "get repository failed")
		return
	}
	template, err := h.templates.GetAuthorizationTemplate(r.Context(), templateID.String())
	if errors.Is(err, repository.ErrNotFound) {
		writeHostedProblem(w, http.StatusNotFound, "not_found", "authorization template not found")
		return
	}
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "get authorization template failed")
		return
	}
	if !validRepositoryGrants(template.Grants, repo.Format) {
		writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "template grants are not valid for the target repository format")
		return
	}
	set, err := h.templates.ApplyAuthorizationTemplate(r.Context(), templateID.String(), body.RepositoryId.String(), string(params.IfMatch))
	if errors.Is(err, repository.ErrNotFound) {
		writeHostedProblem(w, http.StatusNotFound, "not_found", "repository or authorization template not found")
		return
	}
	if errors.Is(err, repository.ErrVersionConflict) {
		writeHostedProblem(w, http.StatusPreconditionFailed, "version_conflict", "If-Match does not match current repository grant version")
		return
	}
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "apply authorization template failed")
		return
	}
	w.Header().Set("ETag", set.Version)
	writeNativeMavenJSON(w, http.StatusOK, set.Grants)
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
	validScopes := map[string]bool{"repositories:read": true, "repositories:write": true, "repositories:admin": true, "repositories:intelligence": true}
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
