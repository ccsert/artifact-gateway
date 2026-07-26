package repository

import (
	"context"
	"sort"
	"time"
)

func defaultAuditRetentionPolicy() AuditRetentionPolicy { return AuditRetentionPolicy{Version: "1"} }

func (s *MemoryStore) GetAuditRetentionPolicy(_ context.Context) (AuditRetentionPolicy, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.auditRetentionPolicy.Version == "" {
		return defaultAuditRetentionPolicy(), nil
	}
	return s.auditRetentionPolicy, nil
}
func (s *MemoryStore) ReplaceAuditRetentionPolicy(_ context.Context, p AuditRetentionPolicy, expected string) (AuditRetentionPolicy, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	current := s.auditRetentionPolicy
	if current.Version == "" {
		current = defaultAuditRetentionPolicy()
	}
	if current.Version != expected {
		return AuditRetentionPolicy{}, ErrVersionConflict
	}
	p.Version = nextHostedGroupVersion(current.Version)
	s.auditRetentionPolicy = p
	return p, nil
}
func (s *MemoryStore) EnqueueAuditCleanupJob(_ context.Context, job AuditCleanupJob) (AuditCleanupJob, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.auditCleanupJobs[job.IdempotencyKey]; ok {
		if existing.PolicyVersion != job.PolicyVersion || existing.BatchSize != job.BatchSize {
			return AuditCleanupJob{}, false, ErrIdempotencyConflict
		}
		return existing, true, nil
	}
	job.State, job.CreatedAt = LifecycleJobPending, time.Now().UTC()
	s.auditCleanupJobs[job.IdempotencyKey] = job
	return job, false, nil
}
func (s *MemoryStore) ListAuditCleanupJobs(_ context.Context, limit int) ([]AuditCleanupJob, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	jobs := make([]AuditCleanupJob, 0, len(s.auditCleanupJobs))
	for _, j := range s.auditCleanupJobs {
		jobs = append(jobs, j)
	}
	sort.Slice(jobs, func(i, j int) bool { return jobs[i].CreatedAt.After(jobs[j].CreatedAt) })
	if len(jobs) > limit {
		jobs = jobs[:limit]
	}
	return jobs, nil
}
func (s *MemoryStore) ClaimAuditCleanupJobs(_ context.Context, limit int) ([]AuditCleanupJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if limit <= 0 {
		limit = 100
	}
	jobs := make([]AuditCleanupJob, 0, limit)
	for key, j := range s.auditCleanupJobs {
		if len(jobs) == limit {
			break
		}
		if j.State == LifecycleJobPending || j.State == LifecycleJobFailed {
			j.State = LifecycleJobRunning
			j.StartedAt = time.Now().UTC()
			j.CompletedAt = time.Time{}
			s.auditCleanupJobs[key] = j
			jobs = append(jobs, j)
		}
	}
	return jobs, nil
}
func (s *MemoryStore) CompleteAuditCleanupJob(_ context.Context, id string, deleted int) error {
	return s.finishAuditCleanupJob(id, LifecycleJobCompleted, "", deleted)
}
func (s *MemoryStore) FailAuditCleanupJob(_ context.Context, id, message string) error {
	return s.finishAuditCleanupJob(id, LifecycleJobFailed, message, 0)
}
func (s *MemoryStore) finishAuditCleanupJob(id string, state LifecycleJobState, message string, deleted int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, j := range s.auditCleanupJobs {
		if j.ID == id && j.State == LifecycleJobRunning {
			j.State, j.CompletedAt, j.LastError, j.Deleted = state, time.Now().UTC(), message, deleted
			s.auditCleanupJobs[key] = j
			return nil
		}
	}
	return ErrNotFound
}
func (s *MemoryStore) DeleteAuditsBefore(_ context.Context, cutoff time.Time, limit int) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	kept := s.Audits[:0]
	deleted := 0
	for _, a := range s.Audits {
		if deleted < limit && a.OccurredAt.Before(cutoff) {
			deleted++
			continue
		}
		kept = append(kept, a)
	}
	s.Audits = kept
	return deleted, nil
}
