package helm

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ccsert/artifact-gateway/internal/authorization"
	helmprotocol "github.com/ccsert/artifact-gateway/internal/protocol/helm"
	"github.com/ccsert/artifact-gateway/internal/protocol/identity"
	"github.com/ccsert/artifact-gateway/internal/repository"
)

type previewGovernance struct {
	RepositoryID      string     `json:"repositoryId"`
	Name              string     `json:"name"`
	Version           string     `json:"version"`
	Digest            string     `json:"digest"`
	GovernanceVersion string     `json:"governanceVersion"`
	Tombstoned        bool       `json:"tombstoned"`
	Quarantined       bool       `json:"quarantined"`
	UpdatedAt         *time.Time `json:"updatedAt,omitempty"`
	UpdatedBy         string     `json:"updatedBy,omitempty"`
	Reason            string     `json:"reason,omitempty"`
}

func governanceDTO(p repository.HelmPublication) previewGovernance {
	g := p.Governance
	out := previewGovernance{RepositoryID: p.RepositoryID, Name: p.Name, Version: p.Version, Digest: p.Digest, GovernanceVersion: g.Version, Tombstoned: g.Tombstoned, Quarantined: g.Quarantined, UpdatedBy: g.UpdatedBy, Reason: g.Reason}
	if !g.UpdatedAt.IsZero() {
		out.UpdatedAt = &g.UpdatedAt
	}
	return out
}

func (m *Module) publicationBlocked(ctx context.Context, p repository.HelmPublication) (bool, error) {
	if p.Governance.Version == "" {
		return false, errIntegrity
	}
	if p.Governance.Tombstoned {
		return true, nil
	}
	if !p.Governance.Quarantined {
		return false, nil
	}
	policy, err := m.governance.GetRepositoryQuarantineReadPolicy(ctx, p.RepositoryID)
	return policy.Enabled, err
}

// Metadata from an upstream index has no governance until a complete closure is
// cached. Always reread retained state; frozen snapshots cannot grant visibility.
func (m *Module) entryBlocked(ctx context.Context, entry repository.HelmSourceEntry) (bool, error) {
	p, err := m.store.GetHelmPublication(ctx, entry.RepositoryID, entry.Name, entry.Version)
	if errors.Is(err, repository.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	blocked, err := m.publicationBlocked(ctx, p)
	if err != nil || blocked {
		return blocked, err
	}
	if p.Digest != entry.Digest {
		return false, repository.ErrHelmPublicationConflict
	}
	return false, nil
}

func publicationSession(p repository.HelmPublication) repository.HelmSession {
	return repository.HelmSession{RepositoryID: p.RepositoryID, Name: p.Name, Version: p.Version, Digest: p.Digest, Size: p.Size, ObjectKey: p.ObjectKey, ProvenanceDigest: p.ProvenanceDigest, ProvenanceSize: p.ProvenanceSize, ProvenanceKey: p.ProvenanceKey, MetadataJSON: p.MetadataJSON, ChartYAML: p.ChartYAML}
}

func (m *Module) verifyRetainedPublication(ctx context.Context, p repository.HelmPublication) error {
	chart, prov, err := m.verify(ctx, publicationSession(p))
	if err != nil {
		return err
	}
	_ = chart.Close()
	if prov != nil {
		_ = prov.Close()
	}
	return nil
}

func (m *Module) previewGovernance(w http.ResponseWriter, r *http.Request, repo repository.HostedRepository, name, version string) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		w.Header().Set("Allow", "GET, POST")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if !helmprotocol.ValidIdentity(name, version) {
		m.problem(w, 404, "not_found")
		return
	}
	p, ok := m.authorize(w, r, repo, authorization.RepositoryAdmin, name, false)
	if !ok {
		return
	}
	if r.Method == http.MethodGet {
		publication, err := m.store.GetHelmPublication(r.Context(), repo.ID, name, version)
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
	case "delete", "restore", "quarantine", "release":
	default:
		m.problem(w, 400, "invalid_governance_request")
		return
	}
	// Repository-scoped governance serialization also protects a policy relaxation
	// from a concurrently introduced quarantine. Publication locking spans byte
	// verification and the CAS transaction, shared with upload and cached reads.
	ctx, releaseRepository, err := m.store.LockHelmPublication(r.Context(), repo.ID, "governance", "repository")
	if err != nil {
		m.failure(w, err)
		return
	}
	defer releaseRepository()
	ctx, releasePublication, err := m.store.LockHelmPublication(ctx, repo.ID, name, version)
	if err != nil {
		m.failure(w, err)
		return
	}
	defer releasePublication()
	before, err := m.store.GetHelmPublication(ctx, repo.ID, name, version)
	if err == nil && (before.Digest != input.ExpectedDigest || before.Governance.Version != input.ExpectedGovernanceVersion) {
		err = repository.ErrVersionConflict
	}
	if err == nil && (input.Action == "restore" && before.Governance.Tombstoned || input.Action == "release" && before.Governance.Quarantined) {
		err = m.verifyRetainedPublication(ctx, before)
	}
	if err != nil {
		m.failure(w, err)
		return
	}
	publication, err := m.governance.TransitionHelmGovernance(ctx, repository.HelmGovernanceChange{RepositoryID: repo.ID, Name: name, Version: version, ExpectedDigest: input.ExpectedDigest, ExpectedVersion: input.ExpectedGovernanceVersion, Action: input.Action, Actor: p.Actor, Reason: input.Reason})
	if err != nil {
		m.failure(w, err)
		return
	}
	writeJSON(w, r, 200, governanceDTO(publication))
}

func (m *Module) previewQuarantineReadPolicy(w http.ResponseWriter, r *http.Request, repo repository.HostedRepository) {
	if r.Method != http.MethodGet && r.Method != http.MethodPut {
		w.Header().Set("Allow", "GET, PUT")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	p, ok := m.authorize(w, r, repo, authorization.RepositoryAdmin, "", false)
	if !ok {
		return
	}
	if r.Method == http.MethodGet {
		policy, err := m.governance.GetRepositoryQuarantineReadPolicy(r.Context(), repo.ID)
		if err != nil {
			m.failure(w, err)
			return
		}
		writeJSON(w, r, 200, map[string]any{"version": policy.Version, "enabled": policy.Enabled})
		return
	}
	var input struct {
		ExpectedVersion string `json:"expectedVersion"`
		Enabled         *bool  `json:"enabled"`
	}
	if !decodeBody(w, r, &input) || input.ExpectedVersion == "" || input.Enabled == nil {
		m.problem(w, 400, "invalid_governance_request")
		return
	}
	ctx, release, err := m.store.LockHelmPublication(r.Context(), repo.ID, "governance", "repository")
	if err != nil {
		m.failure(w, err)
		return
	}
	defer release()
	policy, err := m.governance.GetRepositoryQuarantineReadPolicy(ctx, repo.ID)
	if err == nil && policy.Version != input.ExpectedVersion {
		err = repository.ErrVersionConflict
	}
	if err == nil && policy.Enabled && !*input.Enabled {
		// Disabling the block is itself a relaxation. Check every retained closure
		// that would become readable before changing the independent policy.
		var all []repository.HelmPublication
		all, err = m.store.ListHelmPublications(ctx, repo.ID, helmprotocol.MaxCharts+1)
		if err == nil && len(all) > helmprotocol.MaxCharts {
			err = errInventoryLimit
		}
		for _, publication := range all {
			if err == nil && publication.Governance.Quarantined && !publication.Governance.Tombstoned {
				err = m.verifyRetainedPublication(ctx, publication)
			}
		}
	}
	if err == nil {
		policy, err = m.governance.ReplaceHelmQuarantineReadPolicy(ctx, repo.ID, repository.RepositoryQuarantineReadPolicy{Enabled: *input.Enabled}, input.ExpectedVersion, p.Actor)
	}
	if err != nil {
		m.failure(w, err)
		return
	}
	writeJSON(w, r, 200, map[string]any{"version": policy.Version, "enabled": policy.Enabled})
}
