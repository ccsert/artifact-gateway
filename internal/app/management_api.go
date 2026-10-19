package app

import adminopenapi "github.com/ccsert/artifact-gateway/internal/admin/openapi"

// managementServer composes domain handlers. Generated route and parameter
// binding is shared; authorization and persistence belong to each domain.
type managementServer struct {
	repositoryManagementHandler
	artifactManagementHandler
	accessManagementHandler
	pypiYankManagementHandler
	generatedRepositoryAPIAdapter
}

var _ adminopenapi.ServerInterface = managementServer{}
