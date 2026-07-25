package repository

import (
	"context"
	"sort"
	"time"
)

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
	job.State, job.CreatedAt, job.Payload = LifecycleJobPending, time.Now().UTC(), append([]byte(nil), job.Payload...)
	s.lifecycleJobs[key] = job
	return cloneLifecycleJob(job), false, nil
}

func (s *MemoryStore) ClaimLifecycleJobs(_ context.Context, limit int) ([]LifecycleJob, error) {
	return s.claimLifecycleJobs("", limit)
}

func (s *MemoryStore) ClaimLifecycleJobsByKind(_ context.Context, kind LifecycleJobKind, limit int) ([]LifecycleJob, error) {
	return s.claimLifecycleJobs(kind, limit)
}

func (s *MemoryStore) claimLifecycleJobs(kind LifecycleJobKind, limit int) ([]LifecycleJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if limit <= 0 {
		limit = 100
	}
	keys := make([]string, 0, len(s.lifecycleJobs))
	for key, job := range s.lifecycleJobs {
		if job.State == LifecycleJobPending && (kind == "" || job.Kind == kind) {
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
		job.State, job.StartedAt = LifecycleJobRunning, time.Now().UTC()
		s.lifecycleJobs[key] = job
		jobs = append(jobs, cloneLifecycleJob(job))
	}
	return jobs, nil
}

func (s *MemoryStore) CompleteLifecycleJob(_ context.Context, id string) error {
	return s.finishLifecycleJob(id, LifecycleJobCompleted, "")
}

func (s *MemoryStore) FailLifecycleJob(_ context.Context, id, message string) error {
	return s.finishLifecycleJob(id, LifecycleJobFailed, message)
}

func (s *MemoryStore) finishLifecycleJob(id string, state LifecycleJobState, message string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, job := range s.lifecycleJobs {
		if job.ID != id {
			continue
		}
		if job.State != LifecycleJobRunning {
			return ErrNotFound
		}
		job.State, job.CompletedAt, job.LastError = state, time.Now().UTC(), message
		s.lifecycleJobs[key] = job
		return nil
	}
	return ErrNotFound
}
