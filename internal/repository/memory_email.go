package repository

import (
	"context"
	"github.com/google/uuid"
	"sort"
	"time"
)

func (s *MemoryStore) CreateEmailTarget(_ context.Context, v EmailTarget) (EmailTarget, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v.Version = "1"
	v.CreatedAt = time.Now().UTC()
	v.UpdatedAt = v.CreatedAt
	s.emailTargets[v.ID] = v
	return v, nil
}
func (s *MemoryStore) ListEmailTargets(_ context.Context) ([]EmailTarget, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]EmailTarget, 0, len(s.emailTargets))
	for _, v := range s.emailTargets {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}
func (s *MemoryStore) GetEmailTarget(_ context.Context, id string) (EmailTarget, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.emailTargets[id]
	if !ok {
		return EmailTarget{}, ErrNotFound
	}
	return v, nil
}
func (s *MemoryStore) UpdateEmailTarget(_ context.Context, v EmailTarget, version string) (EmailTarget, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	old, ok := s.emailTargets[v.ID]
	if !ok {
		return EmailTarget{}, ErrNotFound
	}
	if old.Version != version {
		return EmailTarget{}, ErrVersionConflict
	}
	v.Version = nextHostedGroupVersion(old.Version)
	v.CreatedAt = old.CreatedAt
	v.UpdatedAt = time.Now().UTC()
	s.emailTargets[v.ID] = v
	return v, nil
}

func (s *MemoryStore) emailRate(now time.Time) error {
	times := s.emailRequestTimes[:0]
	for _, at := range s.emailRequestTimes {
		if at.After(now.Add(-time.Minute)) {
			times = append(times, at)
		}
	}
	s.emailRequestTimes = times
	if len(times) >= 5 {
		return ErrEmailRateLimited
	}
	active := 0
	for _, v := range s.emailDeliveries {
		if v.State != "accepted" && v.State != "dead" {
			active++
		}
	}
	if active >= 1000 {
		return ErrEmailQueueFull
	}
	return nil
}
func (s *MemoryStore) EnqueueEmailTest(_ context.Context, r EmailTestRequest) (EmailDelivery, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, v := range s.emailDeliveries {
		if v.RequestKey == r.RequestKey {
			if !emailSameRequest(v, r) {
				return EmailDelivery{}, ErrEmailIdempotencyConflict
			}
			return v, nil
		}
	}
	target, ok := s.emailTargets[r.TargetID]
	if !ok {
		return EmailDelivery{}, ErrNotFound
	}
	if target.Version != r.TargetVersion {
		return EmailDelivery{}, ErrVersionConflict
	}
	if !target.Enabled {
		return EmailDelivery{}, ErrEmailTargetDisabled
	}
	now := time.Now().UTC()
	if err := s.emailRate(now); err != nil {
		return EmailDelivery{}, err
	}
	v := EmailDelivery{ID: r.ID, EventID: r.EventID, RequestKey: r.RequestKey, TargetID: target.ID, TargetVersion: target.Version, Scenario: r.Scenario, Locale: target.Locale, TemplateVersion: r.TemplateVersion, RecipientCiphertext: target.RecipientCiphertext, From: r.From, ConsoleOrigin: r.ConsoleOrigin, State: "pending", Version: "1", CreatedAt: now, UpdatedAt: now, NextAttemptAt: now}
	s.emailDeliveries[v.ID] = v
	s.emailRequestTimes = append(s.emailRequestTimes, now)
	return v, nil
}
func (s *MemoryStore) ListEmailDeliveries(_ context.Context, limit int) ([]EmailDelivery, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]EmailDelivery, 0, len(s.emailDeliveries))
	for _, v := range s.emailDeliveries {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
func (s *MemoryStore) GetEmailDelivery(_ context.Context, id string) (EmailDelivery, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.emailDeliveries[id]
	if !ok {
		return EmailDelivery{}, ErrNotFound
	}
	return v, nil
}
func (s *MemoryStore) ClaimEmailDelivery(_ context.Context, owner string) (EmailDelivery, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	for id, v := range s.emailDeliveries {
		if v.Kind == "repository_quota" {
			blocked := false
			for _, prior := range s.emailDeliveries {
				if prior.Kind == "repository_quota" && prior.QuotaRuleID == v.QuotaRuleID && prior.EventSequence < v.EventSequence && prior.State != "accepted" && prior.State != "dead" {
					blocked = true
					break
				}
			}
			if blocked {
				continue
			}
		}
		if v.State == "accepted" || v.State == "dead" || v.NextAttemptAt.After(now) || (v.State == "delivering" && v.LeaseExpiresAt.After(now)) {
			continue
		}
		if v.State == "delivering" {
			v.PossibleDuplicate = true
		}
		if v.AutomaticCancellationCode != "" {
			v.State = "dead"
			v.ErrorCode = v.AutomaticCancellationCode
			v.Version = nextHostedGroupVersion(v.Version)
			v.UpdatedAt = now
			v.LeaseToken = ""
			v.LeaseOwner = ""
			v.LeaseExpiresAt = time.Time{}
			s.emailDeliveries[id] = v
			continue
		}
		if v.Attempts >= EmailMaxAttempts {
			v.State = "dead"
			v.ErrorCode = "attempts_exhausted"
			v.Version = nextHostedGroupVersion(v.Version)
			v.UpdatedAt = now
			s.emailDeliveries[id] = v
			continue
		}
		v.State = "delivering"
		v.Attempts++
		v.LeaseOwner = owner
		v.LeaseToken = uuid.NewString()
		v.LeaseExpiresAt = now.Add(EmailLease)
		v.Version = nextHostedGroupVersion(v.Version)
		v.UpdatedAt = now
		s.emailDeliveries[id] = v
		return v, nil
	}
	return EmailDelivery{}, ErrNotFound
}
func (s *MemoryStore) FinishEmailDelivery(_ context.Context, id, token string, result EmailAttemptResult) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.emailDeliveries[id]
	if !ok {
		return ErrNotFound
	}
	now := time.Now().UTC()
	if v.State != "delivering" || v.LeaseToken != token || !v.LeaseExpiresAt.After(now) {
		return ErrVersionConflict
	}
	result.Code = emailSafeErrorCode(result.Code)
	v.ErrorCode = result.Code
	v.PossibleDuplicate = v.PossibleDuplicate || result.OutcomeUnknown
	switch {
	case result.Code == "":
		v.State = "accepted"
		v.AcceptedAt = now
	case v.AutomaticCancellationCode != "":
		v.State = "dead"
		v.ErrorCode = v.AutomaticCancellationCode
	case result.Permanent || v.Attempts >= EmailMaxAttempts:
		v.State = "dead"
	default:
		v.State = "retrying"
		v.NextAttemptAt = now.Add(emailRetryDelay(v.Attempts))
	}
	v.LeaseToken = ""
	v.LeaseOwner = ""
	v.LeaseExpiresAt = time.Time{}
	v.UpdatedAt = now
	v.Version = nextHostedGroupVersion(v.Version)
	s.emailDeliveries[id] = v
	return nil
}
func (s *MemoryStore) ReplayEmailDelivery(_ context.Context, id, version string) (EmailDelivery, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.emailDeliveries[id]
	if !ok {
		return EmailDelivery{}, ErrNotFound
	}
	if v.Version != version {
		return EmailDelivery{}, ErrVersionConflict
	}
	if v.State != "dead" {
		return EmailDelivery{}, ErrEmailInvalidState
	}
	target := s.emailTargets[v.TargetID]
	if target.Version != v.TargetVersion {
		return EmailDelivery{}, ErrVersionConflict
	}
	if !target.Enabled {
		return EmailDelivery{}, ErrEmailTargetDisabled
	}
	now := time.Now().UTC()
	if err := s.emailRate(now); err != nil {
		return EmailDelivery{}, err
	}
	v.State = "pending"
	v.AutomaticCancellationCode = ""
	v.Attempts = 0
	v.ErrorCode = ""
	v.LeaseToken = ""
	v.LeaseOwner = ""
	v.LeaseExpiresAt = time.Time{}
	v.NextAttemptAt = now
	v.UpdatedAt = now
	v.Version = nextHostedGroupVersion(v.Version)
	s.emailDeliveries[id] = v
	s.emailRequestTimes = append(s.emailRequestTimes, now)
	return v, nil
}
