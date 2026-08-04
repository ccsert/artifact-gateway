package repository

import (
	"context"
	"sort"
	"time"

	"github.com/google/uuid"
)

const (
	replicationLeaseDuration = 10 * time.Minute
	replicationMaxAttempts   = 3
)

func (s *MemoryStore) CreateReplicationPlan(_ context.Context, p ReplicationPlan, checks []ReplicationCheckpoint) (ReplicationPlan, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := p.TargetRepositoryID + "\x00" + p.IdempotencyKey
	if id := s.replicationKeys[key]; id != "" {
		existing := s.replicationPlans[id]
		if existing.SourceRepositoryID != p.SourceRepositoryID || existing.TargetRepositoryID != p.TargetRepositoryID || existing.Format != p.Format || !equivalentReplicationCheckpoints(replicationCheckpointValues(s.replicationChecks[id]), checks) {
			return ReplicationPlan{}, false, ErrIdempotencyConflict
		}
		return existing, true, nil
	}
	p.State = "pending"
	p.CreatedAt = time.Now().UTC()
	if p.MaxAttempts <= 0 {
		p.MaxAttempts = replicationMaxAttempts
	} else if p.MaxAttempts > 10 {
		p.MaxAttempts = 10
	}
	s.replicationPlans[p.ID] = p
	s.replicationKeys[key] = p.ID
	s.replicationChecks[p.ID] = map[string]ReplicationCheckpoint{}
	for _, c := range checks {
		c.PlanID = p.ID
		c.State = "pending"
		c.UpdatedAt = p.CreatedAt
		s.replicationChecks[p.ID][c.ObjectKey] = c
	}
	return p, false, nil
}

func replicationCheckpointValues(checks map[string]ReplicationCheckpoint) []ReplicationCheckpoint {
	values := make([]ReplicationCheckpoint, 0, len(checks))
	for _, checkpoint := range checks {
		values = append(values, checkpoint)
	}
	sort.Slice(values, func(i, j int) bool { return values[i].ObjectKey < values[j].ObjectKey })
	return values
}
func (s *MemoryStore) ClaimReplicationPlans(_ context.Context, limit int) ([]ReplicationPlan, error) {
	return s.claimReplicationPlans(limit, "")
}
func (s *MemoryStore) ClaimReplicationPlansByFormat(_ context.Context, format Format, limit int) ([]ReplicationPlan, error) {
	return s.claimReplicationPlans(limit, format)
}
func (s *MemoryStore) claimReplicationPlans(limit int, format Format) ([]ReplicationPlan, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	s.recoverExpiredReplicationPlansLocked(now)
	out := []ReplicationPlan{}
	ids := make([]string, 0, len(s.replicationPlans))
	for id := range s.replicationPlans {
		ids = append(ids, id)
	}
	s.sortReplicationPlanIDs(ids)
	for _, id := range ids {
		p := s.replicationPlans[id]
		if len(out) == limit {
			break
		}
		if (format == "" || p.Format == format) && (p.State == "pending" || p.State == "failed") && p.Attempts < p.MaxAttempts && (p.NextAttemptAt.IsZero() || !now.Before(p.NextAttemptAt)) {
			p.State = "running"
			p.StartedAt = now
			p.CompletedAt = time.Time{}
			p.NextAttemptAt = time.Time{}
			p.Attempts++
			p.LeaseToken = uuid.NewString()
			p.LeaseExpiresAt = now.Add(replicationLeaseDuration)
			s.replicationPlans[id] = p
			out = append(out, p)
		}
	}
	return out, nil
}

func (s *MemoryStore) sortReplicationPlanIDs(ids []string) {
	sort.Slice(ids, func(i, j int) bool {
		return s.replicationPlans[ids[i]].CreatedAt.Before(s.replicationPlans[ids[j]].CreatedAt)
	})
}

func (s *MemoryStore) RecoverExpiredReplicationPlans(_ context.Context, before time.Time) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.recoverExpiredReplicationPlansLocked(before), nil
}

func (s *MemoryStore) recoverExpiredReplicationPlansLocked(before time.Time) int {
	count := 0
	for id, p := range s.replicationPlans {
		if p.State != "running" || p.LeaseExpiresAt.IsZero() || p.LeaseExpiresAt.After(before) {
			continue
		}
		p.State = "failed"
		p.LeaseToken = ""
		p.LeaseExpiresAt = time.Time{}
		p.LastError = "replication worker lease expired"
		if p.Attempts >= p.MaxAttempts {
			p.NextAttemptAt = time.Time{}
		} else {
			p.NextAttemptAt = before
		}
		s.replicationPlans[id] = p
		count++
	}
	return count
}
func (s *MemoryStore) ListReplicationPlans(_ context.Context, repositoryID string, limit int) ([]ReplicationPlan, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	plans := make([]ReplicationPlan, 0, limit)
	for _, plan := range s.replicationPlans {
		if plan.SourceRepositoryID == repositoryID || plan.TargetRepositoryID == repositoryID {
			plans = append(plans, plan)
		}
	}
	sort.Slice(plans, func(i, j int) bool { return plans[i].CreatedAt.After(plans[j].CreatedAt) })
	if len(plans) > limit {
		plans = plans[:limit]
	}
	return plans, nil
}
func (s *MemoryStore) GetReplicationPlan(_ context.Context, repositoryID, id string) (ReplicationPlan, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	plan, ok := s.replicationPlans[id]
	if !ok || (plan.SourceRepositoryID != repositoryID && plan.TargetRepositoryID != repositoryID) {
		return ReplicationPlan{}, ErrNotFound
	}
	return plan, nil
}
func (s *MemoryStore) ListReplicationCheckpoints(_ context.Context, id string) ([]ReplicationCheckpoint, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	m, ok := s.replicationChecks[id]
	if !ok {
		return nil, ErrNotFound
	}
	out := make([]ReplicationCheckpoint, 0, len(m))
	for _, c := range m {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ObjectKey < out[j].ObjectKey })
	return out, nil
}
func (s *MemoryStore) UpdateReplicationCheckpointWithLease(_ context.Context, c ReplicationCheckpoint, leaseToken string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.replicationPlans[c.PlanID]
	if !ok || p.State != "running" || p.LeaseToken == "" || p.LeaseToken != leaseToken || (!p.LeaseExpiresAt.IsZero() && !time.Now().UTC().Before(p.LeaseExpiresAt)) {
		return ErrNotFound
	}
	m := s.replicationChecks[c.PlanID]
	if m == nil {
		return ErrNotFound
	}
	if _, ok := m[c.ObjectKey]; !ok {
		return ErrNotFound
	}
	c.UpdatedAt = time.Now().UTC()
	m[c.ObjectKey] = c
	p.LeaseExpiresAt = c.UpdatedAt.Add(replicationLeaseDuration)
	s.replicationPlans[c.PlanID] = p
	return nil
}
func (s *MemoryStore) CompleteReplicationPlanWithLease(_ context.Context, id, leaseToken string) error {
	return s.finishReplication(id, "completed", "", leaseToken)
}
func (s *MemoryStore) FailReplicationPlanWithLease(_ context.Context, id, msg, leaseToken string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.replicationPlans[id]
	if !ok || p.State != "running" || p.LeaseToken == "" || p.LeaseToken != leaseToken || (!p.LeaseExpiresAt.IsZero() && !time.Now().UTC().Before(p.LeaseExpiresAt)) {
		return ErrNotFound
	}
	now := time.Now().UTC()
	p.State, p.LastError, p.CompletedAt = "failed", msg, now
	p.LeaseToken = ""
	p.LeaseExpiresAt = time.Time{}
	if p.Attempts >= p.MaxAttempts {
		p.NextAttemptAt = time.Time{}
	} else {
		p.NextAttemptAt = now
	}
	s.replicationPlans[id] = p
	return nil
}
func (s *MemoryStore) finishReplication(id, state, msg, leaseToken string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.replicationPlans[id]
	if !ok || p.State != "running" || p.LeaseToken == "" || p.LeaseToken != leaseToken || (!p.LeaseExpiresAt.IsZero() && !time.Now().UTC().Before(p.LeaseExpiresAt)) {
		return ErrNotFound
	}
	p.State, p.LastError, p.CompletedAt = state, msg, time.Now().UTC()
	p.LeaseToken = ""
	p.LeaseExpiresAt = time.Time{}
	s.replicationPlans[id] = p
	return nil
}

func (s *MemoryStore) CancelReplicationPlan(_ context.Context, repositoryID, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.replicationPlans[id]
	if !ok || (p.SourceRepositoryID != repositoryID && p.TargetRepositoryID != repositoryID) {
		return ErrNotFound
	}
	if p.State != "pending" && p.State != "failed" {
		return ErrNotFound
	}
	p.State = "cancelled"
	p.CompletedAt = time.Now().UTC()
	s.replicationPlans[id] = p
	return nil
}
