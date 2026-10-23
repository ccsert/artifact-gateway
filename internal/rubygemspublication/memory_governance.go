package rubygemspublication

import (
	"context"

	"github.com/ccsert/artifact-gateway/internal/repository"
)

func (m *Memory) retained(repo, name, version, platform string) (Session, error) {
	for _, s := range m.sessions {
		if s.RepositoryID == repo && s.State == "committed" && s.Identity() == Identity(name, version, platform) {
			return clone(s), nil
		}
	}
	return Session{}, repository.ErrNotFound
}

func (m *Memory) GetRetained(ctx context.Context, repo, name, version, platform string) (Publication, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, err := m.active(ctx, repo); err != nil {
		return Publication{}, err
	}
	s, err := m.retained(repo, name, version, platform)
	return Visible(s), err
}

func (m *Memory) TransitionGovernance(ctx context.Context, c GovernanceChange) (Publication, error) {
	if !ValidGovernanceChange(c) {
		return Publication{}, ErrInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	repo, err := m.active(ctx, c.RepositoryID)
	if err != nil {
		return Publication{}, err
	}
	s, err := m.retained(c.RepositoryID, c.Name, c.Version, c.Platform)
	if err != nil {
		return Publication{}, err
	}
	g, changed, err := ApplyGovernance(Visible(s), c)
	if err != nil {
		return Publication{}, err
	}
	if !changed {
		return Visible(s), nil
	}
	s.Governance = g
	p := Visible(s)
	if err := m.audit.RecordAudit(ctx, GovernanceAudit(p, repo.Name, c.Action)); err != nil {
		return Publication{}, err
	}
	m.sessions[s.ID] = s
	return p, nil
}
