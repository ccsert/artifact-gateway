package rubygemspublication

import (
	"context"
	"math"
	"strconv"
	"time"

	"github.com/ccsert/artifact-gateway/internal/repository"
)

// PolicyChange uses the same durable subject and current administrator check as
// publication governance. Its CAS version is independent of publication state.
type PolicyChange struct {
	Identity                             repository.RubyGemsGovernanceIdentity
	RepositoryID, ExpectedVersion, Actor string
	Enabled                              bool
}

func ValidPolicyChange(c PolicyChange) bool {
	v, err := strconv.ParseInt(c.ExpectedVersion, 10, 64)
	return err == nil && v > 0 && v < math.MaxInt64 && strconv.FormatInt(v, 10) == c.ExpectedVersion && c.Actor != "" && len(c.Actor) <= 256 && c.Identity.Actor == c.Actor
}

func ApplyReadPolicy(ctx context.Context, current repository.RepositoryQuarantineReadPolicy, a repository.RubyGemsGovernanceAuthority, c PolicyChange, check GovernanceCheck) (repository.RepositoryQuarantineReadPolicy, *repository.AuditRecord, error) {
	if current.Version != c.ExpectedVersion {
		return current, nil, repository.ErrVersionConflict
	}
	if err := CheckGovernance(ctx, check, a); err != nil {
		return current, nil, err
	}
	if current.Enabled == c.Enabled {
		return current, nil, nil
	}
	v, _ := strconv.ParseInt(current.Version, 10, 64)
	next := repository.RepositoryQuarantineReadPolicy{Version: strconv.FormatInt(v+1, 10), Enabled: c.Enabled}
	audit := repository.AuditRecord{Actor: c.Actor, Repository: a.Repository.Name, GroupName: a.Repository.Name, Format: "rubygems", Resource: c.RepositoryID, Operation: "rubygems.quarantine_read_policy.configure", Outcome: repository.AuditResolved, Status: 200, OccurredAt: time.Now().UTC(), Evidence: map[string]string{"version": next.Version, "enabled": strconv.FormatBool(next.Enabled)}}
	return next, &audit, nil
}

func (m *Memory) GetReadPolicy(ctx context.Context, id string) (repository.RepositoryQuarantineReadPolicy, error) {
	reader, ok := m.repositories.(interface {
		GetRepositoryQuarantineReadPolicy(context.Context, string) (repository.RepositoryQuarantineReadPolicy, error)
	})
	if !ok {
		return repository.RepositoryQuarantineReadPolicy{}, ErrAccessDenied
	}
	return reader.GetRepositoryQuarantineReadPolicy(ctx, id)
}

func (m *Memory) ReplaceReadPolicy(ctx context.Context, c PolicyChange, check GovernanceCheck) (repository.RepositoryQuarantineReadPolicy, error) {
	if !ValidPolicyChange(c) {
		return repository.RepositoryQuarantineReadPolicy{}, ErrInvalid
	}
	writer, ok := m.repositories.(interface {
		ChangeRubyGemsReadPolicy(context.Context, string, repository.RubyGemsGovernanceIdentity, func(context.Context, repository.AuditRecord) error, func(repository.RepositoryQuarantineReadPolicy, repository.RubyGemsGovernanceAuthority) (repository.RepositoryQuarantineReadPolicy, *repository.AuditRecord, error)) (repository.RepositoryQuarantineReadPolicy, error)
	})
	if !ok {
		return repository.RepositoryQuarantineReadPolicy{}, ErrAccessDenied
	}
	return writer.ChangeRubyGemsReadPolicy(ctx, c.RepositoryID, c.Identity, m.audit.RecordAudit, func(current repository.RepositoryQuarantineReadPolicy, a repository.RubyGemsGovernanceAuthority) (repository.RepositoryQuarantineReadPolicy, *repository.AuditRecord, error) {
		return ApplyReadPolicy(ctx, current, a, c, check)
	})
}
