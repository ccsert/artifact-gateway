package app

import (
	"net/http"
	"strings"
	"unicode/utf8"

	adminopenapi "github.com/artifact-gateway/artifact-gateway/internal/admin/openapi"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

func (h generatedRepositoryAPIAdapter) ListRepositoryArtifactIdentities(w http.ResponseWriter, r *http.Request, repositoryID adminopenapi.RepositoryId, params adminopenapi.ListRepositoryArtifactIdentitiesParams) {
	h.withRepositoryScope(w, r, repositoryID.String(), RepositoryRead, func(_ Principal, repo repository.HostedRepository) {
		purpose := repository.ArtifactIdentityPurpose(params.Purpose)
		if purpose != repository.ArtifactIdentityScan && purpose != repository.ArtifactIdentityDistribution {
			writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "purpose must be scan or distribution")
			return
		}
		if purpose == repository.ArtifactIdentityDistribution && !repository.FormatSupportsOperation(repo.Format, repo.Type, repository.RepositoryOperationPromote) {
			writeHostedProblem(w, http.StatusConflict, "unsupported_operation", "distribution identities are unavailable for this repository format and type")
			return
		}
		if purpose == repository.ArtifactIdentityScan && repo.Format == repository.FormatAPT {
			writeHostedProblem(w, http.StatusConflict, "unsupported_operation", "scan identities are unavailable for this repository format")
			return
		}
		store, ok := h.store.(repository.ArtifactIdentityStore)
		if !ok {
			writeHostedProblem(w, http.StatusNotImplemented, "not_supported", "artifact identity listing is unavailable")
			return
		}
		query := ""
		if params.Q != nil {
			query = strings.TrimSpace(*params.Q)
			if utf8.RuneCountInString(query) > 255 || strings.ContainsRune(query, '\x00') {
				writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "q must be at most 255 characters")
				return
			}
		}
		pageSize := 50
		if params.PageSize != nil {
			pageSize = int(*params.PageSize)
			if pageSize < 1 || pageSize > 200 {
				writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "pageSize must be between 1 and 200")
				return
			}
		}
		identities, err := store.ListArtifactIdentities(r.Context(), repo.ID, repo.Format, purpose, query, pageSize)
		if err != nil {
			writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "list artifact identities failed")
			return
		}
		items := make([]adminopenapi.ArtifactIdentity, 0, len(identities))
		for _, identity := range identities {
			items = append(items, adminopenapi.ArtifactIdentity{
				Coordinate: identity.Coordinate, Digest: identity.Digest, Size: identity.Size,
				PublishedAt: identity.PublishedAt, Intelligence: artifactIntelligenceSummaryResponse(identity.Intelligence),
			})
		}
		writeNativeMavenJSON(w, http.StatusOK, adminopenapi.ArtifactIdentityPage{Items: items})
	})
}
