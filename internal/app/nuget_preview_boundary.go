package app

import (
	"net/http"

	adminopenapi "github.com/ccsert/artifact-gateway/internal/admin/openapi"
	"github.com/ccsert/artifact-gateway/internal/repository"
)

// Public repository DTOs only contain the admitted Format enum. The NuGet
// preview has independent DTOs; APT's existing provisioning workflow is kept.
func rejectNuGetFormalProjection(w http.ResponseWriter, repo repository.HostedRepository) bool {
	if repo.Format != repository.FormatNuGet {
		return false
	}
	writeHostedProblem(w, http.StatusNotFound, "not_found", "repository not found")
	return true
}

func runtimeWorkerFormatProjection(input []repository.Format) ([]adminopenapi.Format, *[]adminopenapi.NuGetPreviewWorkerFormat) {
	formal := make([]adminopenapi.Format, 0, len(input))
	var previews *[]adminopenapi.NuGetPreviewWorkerFormat
	for _, format := range input {
		if format == repository.FormatNuGet {
			if previews == nil {
				previews = &[]adminopenapi.NuGetPreviewWorkerFormat{adminopenapi.NuGetPreviewWorkerFormatNuget}
			}
			continue
		}
		formal = append(formal, adminopenapi.Format(format))
	}
	return formal, previews
}
