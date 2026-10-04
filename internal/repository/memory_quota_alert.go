package repository

import (
	"context"
	"github.com/artifact-gateway/artifact-gateway/internal/quotaalert"
	"sort"
	"time"
)

func (s *MemoryStore) ListRepositoryQuotaAlertEvents(_ context.Context, id string) ([]RepositoryQuotaAlertEvent, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, ok := s.quotaAlertRules[id]; !ok {
		return nil, ErrNotFound
	}
	out := make([]RepositoryQuotaAlertEvent, 0)
	for _, v := range s.quotaAlertEvents {
		if v.Snapshot.RuleID != id {
			continue
		}
		if d, ok := s.emailDeliveries[v.DeliveryID]; ok {
			v.DeliveryState, v.DeliveryErrorCode = d.State, d.ErrorCode
		}
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Snapshot.Sequence > out[j].Snapshot.Sequence })
	if len(out) > 50 {
		out = out[:50]
	}
	return out, nil
}

func (s *MemoryStore) ListRepositoryQuotaAlertRules(context.Context) ([]RepositoryQuotaAlertRule, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]RepositoryQuotaAlertRule, 0, len(s.quotaAlertRules))
	for _, v := range s.quotaAlertRules {
		out = append(out, quotaRuleAt(v, time.Now()))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (s *MemoryStore) GetRepositoryQuotaAlertRule(_ context.Context, id string) (RepositoryQuotaAlertRule, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.quotaAlertRules[id]
	if !ok {
		return v, ErrNotFound
	}
	return quotaRuleAt(v, time.Now()), nil
}
func (s *MemoryStore) UpdateRepositoryQuotaAlertRule(_ context.Context, v RepositoryQuotaAlertRule, version string) (RepositoryQuotaAlertRule, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	old, ok := s.quotaAlertRules[v.ID]
	if !ok {
		return v, ErrNotFound
	}
	if old.Version != version {
		return old, ErrVersionConflict
	}
	if old.Deleted {
		return old, ErrQuotaAlertDeleted
	}
	if old.RepositoryID != v.RepositoryID {
		return old, ErrQuotaAlertScopeImmutable
	}
	if err := v.Policy.Validate(); err != nil {
		return old, err
	}
	target, ok := s.emailTargets[v.TargetID]
	if !ok {
		return old, ErrNotFound
	}
	if target.Version != v.TargetVersion {
		return old, ErrVersionConflict
	}
	if v.Enabled && !target.Enabled {
		return old, ErrEmailTargetDisabled
	}
	if quotaSameConfig(old, v) {
		return quotaRuleAt(old, time.Now()), nil
	}
	old.TargetID, old.TargetVersion, old.Policy, old.Enabled = v.TargetID, v.TargetVersion, v.Policy, v.Enabled
	quality := "disabled"
	if old.Enabled {
		quality = "configuration_changed"
	}
	old.State = quotaalert.Reset(old.State, quality)
	old.Version, old.StateVersion = nextHostedGroupVersion(old.Version), nextHostedGroupVersion(old.StateVersion)
	old.UpdatedAt = time.Now().UTC()
	code := "rule_changed"
	if !old.Enabled {
		code = "rule_disabled"
	}
	s.cancelQuotaDeliveriesLocked(old.ID, code, old.UpdatedAt)
	s.quotaAlertRules[old.ID] = old
	return old, nil
}
func (s *MemoryStore) DeleteRepositoryQuotaAlertRule(_ context.Context, id, version string) (RepositoryQuotaAlertRule, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.quotaAlertRules[id]
	if !ok {
		return v, ErrNotFound
	}
	if v.Version != version {
		return v, ErrVersionConflict
	}
	if v.Deleted {
		return v, nil
	}
	v.Deleted, v.Enabled = true, false
	v.State = quotaalert.Reset(v.State, "deleted")
	v.Version, v.StateVersion = nextHostedGroupVersion(v.Version), nextHostedGroupVersion(v.StateVersion)
	v.UpdatedAt = time.Now().UTC()
	s.cancelQuotaDeliveriesLocked(v.ID, "rule_deleted", v.UpdatedAt)
	s.quotaAlertRules[id] = v
	return v, nil
}

func (s *MemoryStore) CreateRepositoryQuotaAlertRule(_ context.Context, v RepositoryQuotaAlertRule) (RepositoryQuotaAlertRule, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	repo, ok := s.hostedRepositories[v.RepositoryID]
	if !ok {
		return v, ErrNotFound
	}
	if repo.State != RepositoryActive {
		return v, ErrDisabled
	}
	target, ok := s.emailTargets[v.TargetID]
	if !ok {
		return v, ErrNotFound
	}
	if target.Version != v.TargetVersion {
		return v, ErrVersionConflict
	}
	if v.Enabled && !target.Enabled {
		return v, ErrEmailTargetDisabled
	}
	if len(s.quotaAlertRules) >= 100 {
		return v, ErrQuotaAlertLimit
	}
	for _, old := range s.quotaAlertRules {
		if !old.Deleted && old.RepositoryID == v.RepositoryID {
			return v, ErrQuotaAlertConflict
		}
	}
	if err := v.Policy.Validate(); err != nil {
		return v, err
	}
	v.Version, v.StateVersion = "1", "1"
	v.CreatedAt = time.Now().UTC()
	v.UpdatedAt = v.CreatedAt
	quality := "disabled"
	if v.Enabled {
		quality = "unknown"
	}
	v.State = quotaalert.Reset(quotaalert.State{}, quality)
	if s.quotaAlertRules == nil {
		s.quotaAlertRules = make(map[string]RepositoryQuotaAlertRule)
	}
	s.quotaAlertRules[v.ID] = v
	return v, nil
}
