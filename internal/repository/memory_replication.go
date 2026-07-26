package repository

import (
	"context"
	"sort"
	"time"
)

func (s *MemoryStore) CreateReplicationPlan(_ context.Context, p ReplicationPlan, checks []ReplicationCheckpoint) (ReplicationPlan, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := p.TargetRepositoryID + "\x00" + p.IdempotencyKey
	if id := s.replicationKeys[key]; id != "" {
		return s.replicationPlans[id], true, nil
	}
	p.State = "pending"
	p.CreatedAt = time.Now().UTC()
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
func (s *MemoryStore) ClaimReplicationPlans(_ context.Context, limit int) ([]ReplicationPlan, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []ReplicationPlan{}
	for id, p := range s.replicationPlans {
		if len(out) == limit {
			break
		}
		if p.State == "pending" || p.State == "failed" {
			p.State = "running"
			p.StartedAt = time.Now().UTC()
			p.CompletedAt = time.Time{}
			s.replicationPlans[id] = p
			out = append(out, p)
		}
	}
	return out, nil
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
func (s *MemoryStore) UpdateReplicationCheckpoint(_ context.Context, c ReplicationCheckpoint) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.replicationChecks[c.PlanID]
	if m == nil {
		return ErrNotFound
	}
	if _, ok := m[c.ObjectKey]; !ok {
		return ErrNotFound
	}
	c.UpdatedAt = time.Now().UTC()
	m[c.ObjectKey] = c
	return nil
}
func (s *MemoryStore) CompleteReplicationPlan(_ context.Context, id string) error {
	return s.finishReplication(id, "completed", "")
}
func (s *MemoryStore) FailReplicationPlan(_ context.Context, id, msg string) error {
	return s.finishReplication(id, "failed", msg)
}
func (s *MemoryStore) finishReplication(id, state, msg string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.replicationPlans[id]
	if !ok || p.State != "running" {
		return ErrNotFound
	}
	p.State, p.LastError, p.CompletedAt = state, msg, time.Now().UTC()
	s.replicationPlans[id] = p
	return nil
}
