package repository

import (
	"context"
	"time"
)

// ChangeRubyGemsDistribution keeps the fixture's publication callback, current
// subjects/grants and delegated audit writes under one repository lock. The
// caller holds its publication-domain mutex first and must not reenter this
// store except through the supplied audit context. Only commit mutates domain
// state, after the final cancellation/expiry/lease check and audit success.
// A nil identity is used only for recording worker progress/failure, which must
// remain possible after the submitting actor loses authority.
func (s *MemoryStore) ChangeRubyGemsDistribution(ctx context.Context, source, target string, identity *RubyGemsGovernanceIdentity, apply func(context.Context, RubyGemsGovernanceAuthority, RubyGemsGovernanceAuthority) error, validate func(RubyGemsGovernanceAuthority, RubyGemsGovernanceAuthority) error, commit func()) error {
	timer := time.NewTicker(time.Millisecond)
	defer timer.Stop()
	for !s.mu.TryLock() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		}
	}
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	var a, b RubyGemsGovernanceAuthority
	var err error
	if identity != nil {
		a, err = s.rubyGemsGovernanceAuthorityLocked(ctx, source, *identity)
		if err == nil {
			b, err = s.rubyGemsGovernanceAuthorityLocked(ctx, target, *identity)
		}
		if err != nil {
			return err
		}
	} else {
		a.Repository = s.hostedRepositories[source]
		b.Repository = s.hostedRepositories[target]
	}
	batch := &memoryAuditBatch{store: s}
	defer batch.finish()
	auditCtx := context.WithValue(ctx, memoryAuditBatchKey{}, batch)
	if err = apply(auditCtx, a, b); err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = validate(a, b); err != nil {
		return err
	}
	for _, record := range batch.finish() {
		s.appendAuditLocked(record)
	}
	commit()
	return nil
}
