package repository

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

const scheduledTaskDispatchPendingError = "dispatch interrupted before submission"

func cloneScheduledTask(task ScheduledTask) ScheduledTask         { return task }
func cloneScheduledTaskRun(run ScheduledTaskRun) ScheduledTaskRun { return run }

func (s *MemoryStore) CreateScheduledTask(_ context.Context, task ScheduledTask) (ScheduledTask, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if task.ID == "" {
		task.ID = uuid.NewString()
	}
	if task.Version == "" {
		task.Version = "1"
	}
	now := time.Now().UTC()
	if task.CreatedAt.IsZero() {
		task.CreatedAt = now
	}
	if task.UpdatedAt.IsZero() {
		task.UpdatedAt = task.CreatedAt
	}
	if task.NextRunAt.IsZero() {
		task.NextRunAt = now.Add(time.Duration(task.IntervalSeconds) * time.Second)
	}
	if _, exists := s.scheduledTasks[task.ID]; exists {
		return ScheduledTask{}, ErrNameExists
	}
	for _, existing := range s.scheduledTasks {
		if strings.EqualFold(existing.Name, task.Name) {
			return ScheduledTask{}, ErrNameExists
		}
	}
	s.scheduledTasks[task.ID] = task
	return cloneScheduledTask(task), nil
}

func (s *MemoryStore) ListScheduledTasks(_ context.Context) ([]ScheduledTask, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]ScheduledTask, 0, len(s.scheduledTasks))
	for _, task := range s.scheduledTasks {
		items = append(items, cloneScheduledTask(task))
	}
	sort.Slice(items, func(i, j int) bool { return items[i].CreatedAt.After(items[j].CreatedAt) })
	return items, nil
}

func (s *MemoryStore) GetScheduledTask(_ context.Context, id string) (ScheduledTask, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	task, ok := s.scheduledTasks[id]
	if !ok {
		return ScheduledTask{}, ErrNotFound
	}
	return cloneScheduledTask(task), nil
}

func (s *MemoryStore) UpdateScheduledTask(_ context.Context, task ScheduledTask, expectedVersion string) (ScheduledTask, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.scheduledTasks[task.ID]
	if !ok {
		return ScheduledTask{}, ErrNotFound
	}
	if expectedVersion != "" && expectedVersion != current.Version {
		return ScheduledTask{}, ErrVersionConflict
	}
	for id, existing := range s.scheduledTasks {
		if id != task.ID && strings.EqualFold(existing.Name, task.Name) {
			return ScheduledTask{}, ErrNameExists
		}
	}
	version, _ := strconv.ParseInt(current.Version, 10, 64)
	if version < 1 {
		version = 1
	}
	task.Version = strconv.FormatInt(version+1, 10)
	task.CreatedAt = current.CreatedAt
	task.UpdatedAt = time.Now().UTC()
	if task.NextRunAt.IsZero() {
		task.NextRunAt = current.NextRunAt
	}
	s.scheduledTasks[task.ID] = task
	return cloneScheduledTask(task), nil
}

func (s *MemoryStore) DeleteScheduledTask(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.scheduledTasks[id]; !ok {
		return ErrNotFound
	}
	delete(s.scheduledTasks, id)
	for runID, run := range s.scheduledTaskRuns {
		if run.TaskID == id {
			delete(s.scheduledTaskRuns, runID)
		}
	}
	return nil
}

func (s *MemoryStore) ClaimDueScheduledTasks(_ context.Context, now time.Time, limit int) ([]ScheduledTaskClaim, error) {
	if limit <= 0 {
		limit = 100
	}
	now = now.UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	items := make([]ScheduledTask, 0, len(s.scheduledTasks))
	for _, task := range s.scheduledTasks {
		if task.Enabled && !task.NextRunAt.After(now) {
			items = append(items, task)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].NextRunAt.Before(items[j].NextRunAt) })
	if len(items) > limit {
		items = items[:limit]
	}
	claims := make([]ScheduledTaskClaim, 0, len(items))
	for _, task := range items {
		task.LastRunAt = now
		task.NextRunAt = now.Add(time.Duration(task.IntervalSeconds) * time.Second)
		task.UpdatedAt = now
		run := ScheduledTaskRun{ID: uuid.NewString(), TaskID: task.ID, Trigger: "scheduled", State: ScheduledTaskFailed, ScheduledAt: now, CreatedAt: now, LastError: scheduledTaskDispatchPendingError}
		task.LastRunID, task.LastRunState, task.LastError = run.ID, ScheduledTaskFailed, run.LastError
		s.scheduledTasks[task.ID] = task
		s.scheduledTaskRuns[run.ID] = run
		claims = append(claims, ScheduledTaskClaim{Task: cloneScheduledTask(task), Run: cloneScheduledTaskRun(run)})
	}
	return claims, nil
}

func (s *MemoryStore) CreateScheduledTaskRun(_ context.Context, taskID, trigger string, now time.Time) (ScheduledTaskRun, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	task, ok := s.scheduledTasks[taskID]
	if !ok {
		return ScheduledTaskRun{}, ErrNotFound
	}
	now = now.UTC()
	run := ScheduledTaskRun{ID: uuid.NewString(), TaskID: taskID, Trigger: trigger, State: ScheduledTaskFailed, ScheduledAt: now, CreatedAt: now, LastError: scheduledTaskDispatchPendingError}
	task.LastRunAt, task.LastRunID, task.LastRunState, task.LastError, task.UpdatedAt = now, run.ID, ScheduledTaskFailed, run.LastError, now
	s.scheduledTasks[taskID], s.scheduledTaskRuns[run.ID] = task, run
	return run, nil
}

func (s *MemoryStore) ListScheduledTaskRuns(_ context.Context, taskID string, limit int) ([]ScheduledTaskRun, error) {
	if limit <= 0 {
		limit = 100
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]ScheduledTaskRun, 0)
	for _, run := range s.scheduledTaskRuns {
		if run.TaskID == taskID {
			items = append(items, cloneScheduledTaskRun(run))
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].CreatedAt.After(items[j].CreatedAt) })
	if len(items) > limit {
		items = items[:limit]
	}
	return items, nil
}

func (s *MemoryStore) UpdateScheduledTaskRun(_ context.Context, run ScheduledTaskRun) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.scheduledTaskRuns[run.ID]
	if !ok {
		return ErrNotFound
	}
	if run.State != ScheduledTaskSubmitted && run.State != ScheduledTaskFailed {
		return ErrVersionConflict
	}
	run.TaskID, run.CreatedAt, run.ScheduledAt = current.TaskID, current.CreatedAt, current.ScheduledAt
	if run.CompletedAt.IsZero() {
		run.CompletedAt = time.Now().UTC()
	}
	s.scheduledTaskRuns[run.ID] = run
	if task, ok := s.scheduledTasks[run.TaskID]; ok {
		task.LastRunState, task.LastError, task.UpdatedAt = run.State, run.LastError, time.Now().UTC()
		s.scheduledTasks[run.TaskID] = task
	}
	return nil
}
