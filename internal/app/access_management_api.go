package app

import "github.com/ccsert/artifact-gateway/internal/repository"

type accessManagementOptions struct {
	Scope        managementScope
	Grants       repository.RepositoryGrantStore
	GrantRecords repository.RepositoryGrantRecordStore
	Templates    repository.AuthorizationTemplateStore
	Roles        repository.AuthorizationRoleStore
}

// accessManagementHandler owns grants, roles, templates and access explanation.
// Repository lookup, authentication and audit use only the shared scope ports.
type accessManagementHandler struct {
	managementScope
	grants             repository.RepositoryGrantStore
	grantRecords       repository.RepositoryGrantRecordStore
	templates          repository.AuthorizationTemplateStore
	authorizationRoles repository.AuthorizationRoleStore
}

func newAccessManagementHandler(options accessManagementOptions) accessManagementHandler {
	return accessManagementHandler{managementScope: options.Scope, grants: options.Grants, grantRecords: options.GrantRecords, templates: options.Templates, authorizationRoles: options.Roles}
}
