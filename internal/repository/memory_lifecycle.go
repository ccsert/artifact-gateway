package repository

import (
	"context"
	"encoding/json"
	"sort"
	"time"

	"github.com/google/uuid"
)

const lifecycleJobLeaseDuration = 10 * time.Minute

func lifecycleJobKey(repositoryID string, kind LifecycleJobKind, idempotencyKey string) string {
	return repositoryID + "\x00" + string(kind) + "\x00" + idempotencyKey
}

func cloneLifecycleJob(job LifecycleJob) LifecycleJob {
	job.Payload = append([]byte(nil), job.Payload...)
	return job
}

func (s *MemoryStore) EnqueueLifecycleJob(_ context.Context, job LifecycleJob) (LifecycleJob, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := lifecycleJobKey(job.RepositoryID, job.Kind, job.IdempotencyKey)
	if existing, ok := s.lifecycleJobs[key]; ok {
		if !equivalentLifecyclePayload(existing.Payload, job.Payload) {
			return LifecycleJob{}, false, ErrIdempotencyConflict
		}
		return cloneLifecycleJob(existing), true, nil
	}
	now := time.Now().UTC()
	createdAt := now
	if !createdAt.After(s.lifecycleCreatedAt) {
		createdAt = s.lifecycleCreatedAt.Add(time.Nanosecond)
	}
	s.lifecycleCreatedAt = createdAt
	if job.MaxAttempts <= 0 {
		job.MaxAttempts = DefaultLifecycleJobMaxAttempts
	}
	if job.ProgressTotal <= 0 && job.Kind != LifecycleJobRetention {
		job.ProgressTotal = 1
	}
	job.State, job.CreatedAt, job.NextAttemptAt, job.Payload = LifecycleJobPending, createdAt, now, append([]byte(nil), job.Payload...)
	s.lifecycleJobs[key] = job
	return cloneLifecycleJob(job), false, nil
}

func (s *MemoryStore) ListLifecycleJobs(_ context.Context, repositoryID string, limit int) ([]LifecycleJob, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	jobs := make([]LifecycleJob, 0, limit)
	for _, job := range s.lifecycleJobs {
		if job.RepositoryID == repositoryID {
			jobs = append(jobs, cloneLifecycleJob(job))
		}
	}
	sort.Slice(jobs, func(i, j int) bool { return jobs[i].CreatedAt.After(jobs[j].CreatedAt) })
	if len(jobs) > limit {
		jobs = jobs[:limit]
	}
	return jobs, nil
}

func (s *MemoryStore) ListAllLifecycleJobs(_ context.Context, limit int) ([]RepositoryLifecycleJob, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if limit <= 0 || limit > 500 {
		limit = 500
	}
	records := make([]RepositoryLifecycleJob, 0, len(s.lifecycleJobs))
	for _, job := range s.lifecycleJobs {
		repo, ok := s.hostedRepositories[job.RepositoryID]
		if !ok {
			continue
		}
		records = append(records, RepositoryLifecycleJob{RepositoryName: repo.Name, Job: cloneLifecycleJob(job)})
	}
	sort.Slice(records, func(i, j int) bool { return records[i].Job.CreatedAt.After(records[j].Job.CreatedAt) })
	if len(records) > limit {
		records = records[:limit]
	}
	return records, nil
}

func (s *MemoryStore) GetLifecycleJob(_ context.Context, repositoryID, id string) (LifecycleJob, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, job := range s.lifecycleJobs {
		if job.RepositoryID == repositoryID && job.ID == id {
			return cloneLifecycleJob(job), nil
		}
	}
	return LifecycleJob{}, ErrNotFound
}

func (s *MemoryStore) ClaimLifecycleJobs(_ context.Context, limit int) ([]LifecycleJob, error) {
	return s.claimLifecycleJobs("", "", limit)
}

func (s *MemoryStore) ClaimLifecycleJobsByKind(_ context.Context, kind LifecycleJobKind, limit int) ([]LifecycleJob, error) {
	return s.claimLifecycleJobs(kind, "", limit)
}

func (s *MemoryStore) ClaimLifecycleJobsByKindAndFormat(_ context.Context, kind LifecycleJobKind, format Format, limit int) ([]LifecycleJob, error) {
	return s.claimLifecycleJobs(kind, format, limit)
}

func (s *MemoryStore) claimLifecycleJobs(kind LifecycleJobKind, format Format, limit int) ([]LifecycleJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	s.recoverExpiredLifecycleJobsLocked(now)
	if limit <= 0 {
		limit = 100
	}
	keys := make([]string, 0, len(s.lifecycleJobs))
	claimedRepositories := make(map[string]bool)
	for key, job := range s.lifecycleJobs {
		if job.State == LifecycleJobRunning {
			claimedRepositories[job.RepositoryID] = true
		}
		if (job.State == LifecycleJobPending || job.State == LifecycleJobRetrying) && (job.NextAttemptAt.IsZero() || !job.NextAttemptAt.After(now)) && (kind == "" || job.Kind == kind) && lifecycleJobMatchesFormat(job.Payload, format) {
			keys = append(keys, key)
		}
	}
	sort.Slice(keys, func(i, j int) bool {
		return s.lifecycleJobs[keys[i]].CreatedAt.Before(s.lifecycleJobs[keys[j]].CreatedAt)
	})
	jobs := make([]LifecycleJob, 0, limit)
	for _, key := range keys {
		if len(jobs) == limit {
			break
		}
		job := s.lifecycleJobs[key]
		if claimedRepositories[job.RepositoryID] {
			continue
		}
		job.State, job.StartedAt, job.CompletedAt = LifecycleJobRunning, now, time.Time{}
		job.NextAttemptAt, job.LeaseExpiresAt, job.LeaseToken = time.Time{}, now.Add(lifecycleJobLeaseDuration), uuid.NewString()
		job.Attempts++
		s.lifecycleJobs[key] = job
		claimedRepositories[job.RepositoryID] = true
		jobs = append(jobs, cloneLifecycleJob(job))
	}
	return jobs, nil
}

func (s *MemoryStore) RecoverExpiredLifecycleJobs(_ context.Context, before time.Time) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.recoverExpiredLifecycleJobsLocked(before.UTC()), nil
}

func (s *MemoryStore) recoverExpiredLifecycleJobsLocked(before time.Time) int {
	recovered := 0
	for key, job := range s.lifecycleJobs {
		if job.State != LifecycleJobRunning || job.LeaseExpiresAt.IsZero() || job.LeaseExpiresAt.After(before) {
			continue
		}
		job.LeaseExpiresAt, job.LeaseToken = time.Time{}, ""
		job.LastError = "worker lease expired before completion"
		if job.Attempts >= job.MaxAttempts {
			job.State, job.CompletedAt = LifecycleJobFailed, before
		} else {
			job.State, job.NextAttemptAt, job.CompletedAt = LifecycleJobRetrying, before.Add(lifecycleRetryDelay(job.Attempts)), time.Time{}
		}
		s.lifecycleJobs[key] = job
		recovered++
	}
	return recovered
}

func (s *MemoryStore) RunLifecycleJobNow(_ context.Context, repositoryID, id string) (LifecycleJob, error) {
	return s.updateLifecycleJobControl(repositoryID, id, func(job *LifecycleJob, now time.Time) error {
		if job.State != LifecycleJobPending && job.State != LifecycleJobRetrying {
			return ErrVersionConflict
		}
		job.State, job.NextAttemptAt = LifecycleJobPending, now
		return nil
	})
}

func (s *MemoryStore) RetryLifecycleJob(_ context.Context, repositoryID, id string) (LifecycleJob, error) {
	return s.updateLifecycleJobControl(repositoryID, id, func(job *LifecycleJob, now time.Time) error {
		if job.State != LifecycleJobFailed && job.State != LifecycleJobCancelled {
			return ErrVersionConflict
		}
		job.State, job.NextAttemptAt = LifecycleJobPending, now
		job.Attempts, job.LastError = 0, ""
		job.StartedAt, job.CompletedAt, job.LeaseExpiresAt, job.LeaseToken = time.Time{}, time.Time{}, time.Time{}, ""
		job.ProgressCurrent, job.ProgressMessage = 0, ""
		return nil
	})
}

func (s *MemoryStore) CancelLifecycleJob(_ context.Context, repositoryID, id string) (LifecycleJob, error) {
	return s.updateLifecycleJobControl(repositoryID, id, func(job *LifecycleJob, now time.Time) error {
		if job.State != LifecycleJobPending && job.State != LifecycleJobRetrying {
			return ErrVersionConflict
		}
		job.State, job.CompletedAt, job.NextAttemptAt = LifecycleJobCancelled, now, time.Time{}
		return nil
	})
}

func (s *MemoryStore) updateLifecycleJobControl(repositoryID, id string, update func(*LifecycleJob, time.Time) error) (LifecycleJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, job := range s.lifecycleJobs {
		if job.RepositoryID != repositoryID || job.ID != id {
			continue
		}
		if err := update(&job, time.Now().UTC()); err != nil {
			return LifecycleJob{}, err
		}
		s.lifecycleJobs[key] = job
		return cloneLifecycleJob(job), nil
	}
	return LifecycleJob{}, ErrNotFound
}

func (s *MemoryStore) UpdateLifecycleJobProgress(_ context.Context, id, leaseToken string, current, total int, message string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if current < 0 || total < 0 || current > total {
		return ErrVersionConflict
	}
	for key, job := range s.lifecycleJobs {
		if job.ID != id || job.State != LifecycleJobRunning || job.LeaseToken != leaseToken {
			continue
		}
		job.ProgressCurrent, job.ProgressTotal, job.ProgressMessage = current, total, message
		job.LeaseExpiresAt = time.Now().UTC().Add(lifecycleJobLeaseDuration)
		s.lifecycleJobs[key] = job
		return nil
	}
	return ErrNotFound
}

func lifecycleJobMatchesFormat(payload []byte, format Format) bool {
	if format == "" {
		return true
	}
	var value struct {
		Format Format `json:"format"`
	}
	return json.Unmarshal(payload, &value) == nil && value.Format == format
}

func (s *MemoryStore) CompleteLifecycleJob(_ context.Context, id, leaseToken string) error {
	return s.finishLifecycleJob(id, leaseToken, LifecycleJobCompleted, "")
}

func (s *MemoryStore) FailLifecycleJob(_ context.Context, id, leaseToken, message string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	for key, job := range s.lifecycleJobs {
		if job.ID != id || job.State != LifecycleJobRunning || job.LeaseToken != leaseToken {
			continue
		}
		job.LastError, job.LeaseExpiresAt, job.LeaseToken = message, time.Time{}, ""
		if job.Attempts >= job.MaxAttempts {
			job.State, job.CompletedAt = LifecycleJobFailed, now
		} else {
			job.State, job.NextAttemptAt, job.CompletedAt = LifecycleJobRetrying, now.Add(lifecycleRetryDelay(job.Attempts)), time.Time{}
		}
		s.lifecycleJobs[key] = job
		return nil
	}
	return ErrNotFound
}

func (s *MemoryStore) finishLifecycleJob(id, leaseToken string, state LifecycleJobState, message string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, job := range s.lifecycleJobs {
		if job.ID != id {
			continue
		}
		if job.State != LifecycleJobRunning || job.LeaseToken != leaseToken {
			return ErrNotFound
		}
		job.State, job.CompletedAt, job.LastError, job.LeaseExpiresAt, job.LeaseToken = state, time.Now().UTC(), message, time.Time{}, ""
		if state == LifecycleJobCompleted && job.ProgressTotal > 0 {
			job.ProgressCurrent = job.ProgressTotal
		}
		s.lifecycleJobs[key] = job
		return nil
	}
	return ErrNotFound
}

func lifecycleRetryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	delay := 30 * time.Second * time.Duration(1<<min(attempt-1, 6))
	if delay > 30*time.Minute {
		return 30 * time.Minute
	}
	return delay
}
