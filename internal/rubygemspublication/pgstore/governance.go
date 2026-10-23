package pgstore

import (
	"context"

	protocol "github.com/ccsert/artifact-gateway/internal/protocol/rubygems"
	domain "github.com/ccsert/artifact-gateway/internal/rubygemspublication"
)

func (s *Store) GetRetained(ctx context.Context, repo, name, version, platform string) (domain.Publication, error) {
	in, err := scan(s.db.QueryRowContext(ctx, `SELECT `+columns+` FROM native_rubygems_sessions WHERE repository_id::text=$1 AND name=$2 AND canonical_version=$3 AND platform=$4 AND state='committed' AND EXISTS(SELECT 1 FROM hosted_repositories r WHERE r.id=repository_id AND r.format='rubygems' AND r.repo_type='hosted' AND r.state='active')`, repo, name, protocol.CanonicalVersion(version), platform))
	return domain.Visible(in), err
}

func (s *Store) TransitionGovernance(ctx context.Context, c domain.GovernanceChange) (domain.Publication, error) {
	if !domain.ValidGovernanceChange(c) {
		return domain.Publication{}, domain.ErrInvalid
	}
	tx, name, err := s.transaction(ctx, c.RepositoryID)
	if err != nil {
		return domain.Publication{}, err
	}
	defer func() { _ = tx.Rollback() }()
	in, err := scan(tx.QueryRowContext(ctx, `SELECT `+columns+` FROM native_rubygems_sessions WHERE repository_id::text=$1 AND name=$2 AND canonical_version=$3 AND platform=$4 AND state='committed' FOR UPDATE`, c.RepositoryID, c.Name, protocol.CanonicalVersion(c.Version), c.Platform))
	if err != nil {
		return domain.Publication{}, err
	}
	g, changed, err := domain.ApplyGovernance(domain.Visible(in), c)
	if err != nil {
		return domain.Publication{}, err
	}
	if !changed {
		return domain.Visible(in), tx.Commit()
	}
	in, err = scan(tx.QueryRowContext(ctx, `UPDATE native_rubygems_sessions SET governance_version=$2,yanked=$3,governance_updated_at=$4,governance_updated_by=$5,governance_reason=$6 WHERE id::text=$1 RETURNING `+columns, in.ID, g.Version, g.Yanked, g.UpdatedAt, g.UpdatedBy, g.Reason))
	if err != nil {
		return domain.Publication{}, err
	}
	p := domain.Visible(in)
	if err = insertAudit(ctx, tx, domain.GovernanceAudit(p, name, c.Action)); err != nil {
		return domain.Publication{}, err
	}
	return p, tx.Commit()
}
