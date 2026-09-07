package app

import (
	"errors"
	"net/http"

	adminopenapi "github.com/artifact-gateway/artifact-gateway/internal/admin/openapi"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

// GetGroupResolution inspects configuration using the protocol resolver itself.
// It does not query artifacts, contact upstreams, or decide access for a reader.
func (h generatedRepositoryAPIAdapter) GetGroupResolution(w http.ResponseWriter, r *http.Request, id adminopenapi.GroupId) {
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
	members, err := (v2GroupResolver{groups: h.groups, repos: h.store}).resolveMembers(r.Context(), group)
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "resolve group members failed")
		return
	}
	response := adminopenapi.GroupResolution{
		GroupId: id, GroupVersion: group.Version, Format: adminopenapi.Format(group.Format), Strategy: adminopenapi.HostedFirst,
		ExcludedMemberCount: len(group.Members) - len(members), Members: make([]adminopenapi.GroupResolutionMember, 0, len(members)),
	}
	for i, member := range members {
		repositoryID, err := uuid.Parse(member.RepositoryID)
		if err != nil {
			writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "invalid stored group member")
			return
		}
		response.Members = append(response.Members, adminopenapi.GroupResolutionMember{
			RepositoryId: repositoryID, RepositoryName: member.Name, Type: adminopenapi.GroupResolutionMemberType(member.Type),
			ConfiguredPosition: member.Position, ResolutionOrder: i + 1,
		})
	}
	writeNativeMavenJSON(w, http.StatusOK, response)
}
