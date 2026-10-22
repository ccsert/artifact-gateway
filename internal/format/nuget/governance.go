package nuget

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ccsert/artifact-gateway/internal/authorization"
	"github.com/ccsert/artifact-gateway/internal/protocol/identity"
	nugetprotocol "github.com/ccsert/artifact-gateway/internal/protocol/nuget"
	"github.com/ccsert/artifact-gateway/internal/repository"
)

type previewGovernance struct {
	RepositoryID      string     `json:"repositoryId"`
	PackageID         string     `json:"packageId"`
	Version           string     `json:"version"`
	Digest            string     `json:"digest"`
	GovernanceVersion string     `json:"governanceVersion"`
	Listed            bool       `json:"listed"`
	Tombstoned        bool       `json:"tombstoned"`
	Quarantined       bool       `json:"quarantined"`
	UpdatedAt         *time.Time `json:"updatedAt,omitempty"`
	UpdatedBy         string     `json:"updatedBy,omitempty"`
	Reason            string     `json:"reason,omitempty"`
}

func governanceDTO(p repository.NuGetPublication) previewGovernance {
	g := p.Governance
	out := previewGovernance{RepositoryID: p.RepositoryID, PackageID: p.PackageID, Version: p.Version, Digest: p.Digest, GovernanceVersion: g.Version, Listed: g.Listed, Tombstoned: g.Tombstoned, Quarantined: g.Quarantined, UpdatedBy: g.UpdatedBy, Reason: g.Reason}
	if !g.UpdatedAt.IsZero() {
		out.UpdatedAt = &g.UpdatedAt
	}
	return out
}

// This predicate runs after Group ownership is selected. A hidden owner cannot
// expose another member's bytes at the same canonical package coordinate.
func (m *Module) governedPublication(ctx context.Context, p repository.NuGetPublication) (repository.NuGetPublication, bool, error) {
	if p.Governance.Version == "" {
		cached, err := m.store.GetNuGetPublication(ctx, p.RepositoryID, p.PackageID, p.Version)
		if err != nil && !errors.Is(err, repository.ErrNotFound) {
			return p, false, err
		}
		if err == nil {
			p.Governance = cached.Governance
			p.Digest, p.Size = cached.Digest, cached.Size
		}
	}
	if p.Governance.Tombstoned {
		return p, true, nil
	}
	if p.Governance.Quarantined {
		policy, err := m.store.GetRepositoryQuarantineReadPolicy(ctx, p.RepositoryID)
		if err != nil {
			return p, false, err
		}
		return p, policy.Enabled, nil
	}
	return p, false, nil
}

func (m *Module) visiblePublications(ctx context.Context, values []repository.NuGetPublication) ([]repository.NuGetPublication, error) {
	out := make([]repository.NuGetPublication, 0, len(values))
	for _, p := range values {
		p, blocked, err := m.governedPublication(ctx, p)
		if err != nil {
			return nil, err
		}
		if !blocked {
			out = append(out, p)
		}
	}
	return out, nil
}

func listedPublication(p repository.NuGetPublication, meta packageMetadata) bool {
	if p.Governance.Version != "" && p.SourceVersion == "" {
		return p.Governance.Listed
	}
	return meta.Listed == nil || *meta.Listed
}

func canonicalGovernanceCoordinate(id, version string) (string, string, bool) {
	id, e1 := nugetprotocol.NormalizeID(id)
	version, e2 := nugetprotocol.NormalizeVersion(version)
	return id, strings.ToLower(version), e1 == nil && e2 == nil
}

func (m *Module) nativeListing(w http.ResponseWriter, r *http.Request, repo repository.HostedRepository, rawID, rawVersion string) {
	if r.Method != http.MethodDelete && r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	id, version, valid := canonicalGovernanceCoordinate(rawID, rawVersion)
	if !valid {
		m.problem(w, 404, "not_found")
		return
	}
	p, ok := m.authorize(w, r, repo, authorization.RepositoryWrite, id)
	if !ok {
		return
	}
	if repo.Type != repository.RepositoryTypeHosted {
		m.problem(w, 405, "read_only_source")
		return
	}
	ctx, release, err := m.store.LockNuGetPublication(r.Context(), repo.ID, id, version)
	if err != nil {
		m.failure(w, err)
		return
	}
	defer release()
	publication, err := m.store.GetNuGetPublication(ctx, repo.ID, id, version)
	if err != nil || publication.Governance.Tombstoned {
		if err == nil {
			err = repository.ErrNotFound
		}
		m.failure(w, err)
		return
	}
	action := "unlist"
	if r.Method == http.MethodPost {
		action = "relist"
		if !publication.Governance.Listed {
			if _, err = m.verifyPublication(ctx, publication); err != nil {
				m.failure(w, err)
				return
			}
		}
	}
	_, err = m.store.TransitionNuGetGovernance(ctx, repository.NuGetGovernanceChange{RepositoryID: repo.ID, PackageID: id, Version: version, ExpectedDigest: publication.Digest, ExpectedVersion: publication.Governance.Version, Action: action, Actor: p.Actor, Reason: "Native NuGet " + action})
	if err != nil {
		m.failure(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (m *Module) previewGovernance(w http.ResponseWriter, r *http.Request, repo repository.HostedRepository, parts []string) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	id, version, valid := canonicalGovernanceCoordinate(parts[0], parts[1])
	if !valid {
		m.problem(w, 404, "not_found")
		return
	}
	p, ok := m.authorize(w, r, repo, authorization.RepositoryAdmin, id)
	if !ok {
		return
	}
	if r.Method == http.MethodGet {
		publication, err := m.store.GetNuGetPublication(r.Context(), repo.ID, id, version)
		if err != nil {
			m.failure(w, err)
			return
		}
		writeJSON(w, r, 200, governanceDTO(publication))
		return
	}
	var input struct {
		ExpectedDigest            string `json:"expectedDigest"`
		ExpectedGovernanceVersion string `json:"expectedGovernanceVersion"`
		Action                    string `json:"action"`
		Reason                    string `json:"reason"`
	}
	if !decodeBody(w, r, &input) || !identity.IsSHA256Digest(input.ExpectedDigest) || input.ExpectedGovernanceVersion == "" || strings.TrimSpace(input.Reason) == "" || utf8.RuneCountInString(input.Reason) > 2048 || strings.ContainsRune(input.Reason, '\x00') {
		m.problem(w, 400, "invalid_governance_request")
		return
	}
	switch input.Action {
	case "unlist", "relist", "delete", "restore", "quarantine", "release":
	default:
		m.problem(w, 400, "invalid_governance_request")
		return
	}
	if repo.Type != repository.RepositoryTypeHosted && (input.Action == "unlist" || input.Action == "relist") {
		m.problem(w, 405, "read_only_source")
		return
	}
	ctx, release, err := m.store.LockNuGetPublication(r.Context(), repo.ID, id, version)
	if err != nil {
		m.failure(w, err)
		return
	}
	defer release()
	if input.Action == "restore" || input.Action == "release" || input.Action == "relist" {
		before, readErr := m.store.GetNuGetPublication(ctx, repo.ID, id, version)
		if readErr != nil {
			m.failure(w, readErr)
			return
		}
		if before.Digest != input.ExpectedDigest || before.Governance.Version != input.ExpectedGovernanceVersion {
			m.failure(w, repository.ErrVersionConflict)
			return
		}
		if input.Action == "restore" && before.Governance.Tombstoned || input.Action == "release" && before.Governance.Quarantined || input.Action == "relist" && !before.Governance.Listed {
			if _, readErr = m.verifyPublication(ctx, before); readErr != nil {
				m.failure(w, readErr)
				return
			}
		}
	}
	publication, err := m.store.TransitionNuGetGovernance(ctx, repository.NuGetGovernanceChange{RepositoryID: repo.ID, PackageID: id, Version: version, ExpectedDigest: input.ExpectedDigest, ExpectedVersion: input.ExpectedGovernanceVersion, Action: input.Action, Actor: p.Actor, Reason: input.Reason})
	if errors.Is(err, repository.ErrInvalidNuGetPublication) {
		m.problem(w, 400, "invalid_governance_request")
		return
	}
	if err != nil {
		m.failure(w, err)
		return
	}
	writeJSON(w, r, 200, governanceDTO(publication))
}

func (m *Module) previewQuarantineReadPolicy(w http.ResponseWriter, r *http.Request, repo repository.HostedRepository) {
	if r.Method != http.MethodGet && r.Method != http.MethodPut {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	p, ok := m.authorize(w, r, repo, authorization.RepositoryAdmin, "")
	if !ok {
		return
	}
	policy, err := m.store.GetRepositoryQuarantineReadPolicy(r.Context(), repo.ID)
	if err == nil && r.Method == http.MethodPut {
		var input struct {
			ExpectedVersion string `json:"expectedVersion"`
			Enabled         *bool  `json:"enabled"`
		}
		if !decodeBody(w, r, &input) || input.ExpectedVersion == "" || input.Enabled == nil {
			m.problem(w, 400, "invalid_governance_request")
			return
		}
		policy, err = m.store.ReplaceNuGetQuarantineReadPolicy(r.Context(), repo.ID, repository.RepositoryQuarantineReadPolicy{Enabled: *input.Enabled}, input.ExpectedVersion, p.Actor)
	}
	if err != nil {
		m.failure(w, err)
		return
	}
	writeJSON(w, r, 200, map[string]any{"version": policy.Version, "enabled": policy.Enabled})
}
