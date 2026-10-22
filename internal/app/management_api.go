package app

import (
	helmapi "github.com/ccsert/artifact-gateway/internal/admin/helm"
	nugetapi "github.com/ccsert/artifact-gateway/internal/admin/nuget"
	adminopenapi "github.com/ccsert/artifact-gateway/internal/admin/openapi"
)

// managementServer composes domain handlers. Generated route and parameter
// binding is shared; authorization and persistence belong to each domain.
type managementServer struct {
	*nugetapi.Adapter
	*helmapi.Boundary
	repositoryManagementHandler
	artifactManagementHandler
	accessManagementHandler
	identityManagementHandler
	pypiYankManagementHandler
	generatedRepositoryAPIAdapter
}

var _ adminopenapi.ServerInterface = managementServer{}
