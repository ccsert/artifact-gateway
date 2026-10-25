package rubygems

import (
	"net/http"

	"github.com/ccsert/artifact-gateway/internal/authorization"
	"github.com/ccsert/artifact-gateway/internal/repository"
	domain "github.com/ccsert/artifact-gateway/internal/rubygemspublication"
)

func (m *Module) previewReadPolicy(w http.ResponseWriter, r *http.Request, repo repository.HostedRepository) {
	if r.Method != http.MethodGet && r.Method != http.MethodPut {
		w.Header().Set("Allow", "GET, PUT")
		w.WriteHeader(405)
		return
	}
	p, ok := m.authorize(w, r, repo, authorization.RepositoryAdmin, "", false)
	if !ok {
		return
	}
	if r.Method == http.MethodGet {
		policy, err := m.store.GetReadPolicy(r.Context(), repo.ID)
		if err != nil {
			m.failure(w, err)
			return
		}
		jsonResponse(w, 200, map[string]any{"version": policy.Version, "enabled": policy.Enabled})
		return
	}
	var input struct {
		ExpectedVersion string `json:"expectedVersion"`
		Enabled         *bool  `json:"enabled"`
	}
	if !decode(w, r, &input) || input.Enabled == nil {
		m.problem(w, 400, "invalid_read_policy_request")
		return
	}
	identity, err := m.governanceIdentity(r, p)
	if err != nil {
		m.failure(w, err)
		return
	}
	c := domain.PolicyChange{Identity: identity, RepositoryID: repo.ID, ExpectedVersion: input.ExpectedVersion, Actor: p.Actor, Enabled: *input.Enabled}
	if !domain.ValidPolicyChange(c) {
		m.problem(w, 400, "invalid_read_policy_request")
		return
	}
	policy, err := m.store.ReplaceReadPolicy(r.Context(), c, m.governanceAuthorityCheck(identity, repo.ID, "", authorization.RepositoryAdmin))
	if err != nil {
		m.failure(w, err)
		return
	}
	jsonResponse(w, 200, map[string]any{"version": policy.Version, "enabled": policy.Enabled})
}
