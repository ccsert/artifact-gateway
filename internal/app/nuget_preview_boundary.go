package app

import (
	"net/http"

	adminopenapi "github.com/ccsert/artifact-gateway/internal/admin/openapi"
	"github.com/ccsert/artifact-gateway/internal/repository"
)

// Public repository DTOs only contain the admitted Format enum. The format
// previews have independent DTOs; APT's existing provisioning workflow is kept.
func rejectPreviewFormalProjection(w http.ResponseWriter, repo repository.HostedRepository) bool {
	if repository.IsSupportedFormat(repo.Format) {
		return false
	}
	writeHostedProblem(w, http.StatusNotFound, "not_found", "repository not found")
	return true
}

func runtimeWorkerFormatProjection(input []repository.Format) ([]adminopenapi.Format, *[]adminopenapi.PreviewWorkerFormat) {
	formal := make([]adminopenapi.Format, 0, len(input))
	var previews *[]adminopenapi.PreviewWorkerFormat
	for _, format := range input {
		if format == repository.FormatNuGet || format == repository.FormatHelm {
			if previews == nil {
				previews = &[]adminopenapi.PreviewWorkerFormat{}
			}
			*previews = append(*previews, adminopenapi.PreviewWorkerFormat(format))
			continue
		}
		if repository.IsSupportedFormat(format) {
			formal = append(formal, adminopenapi.Format(format))
		}
	}
	return formal, previews
}
