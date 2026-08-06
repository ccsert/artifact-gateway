package repository

import (
	"context"
	"encoding/json"
	"sort"
	"time"
)

type backgroundOperationQueueKey struct {
	kind   BackgroundOperationKind
	format Format
	state  LifecycleJobState
}

func (s *MemoryStore) BackgroundOperationQueueStats(_ context.Context) ([]BackgroundOperationQueueStat, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	aggregated := make(map[backgroundOperationQueueKey]BackgroundOperationQueueStat)
	for _, job := range s.lifecycleJobs {
		format := lifecycleQueueFormat(job.Payload)
		if format == "" || !queueVisibleState(job.State) {
			continue
		}
		kind := BackgroundOperationLifecycle
		if job.Kind == LifecycleJobPromotion {
			kind = BackgroundOperationPromotion
		} else if job.Kind == LifecycleJobReplication {
			kind = BackgroundOperationReplication
		}
		addBackgroundOperationQueueStat(aggregated, backgroundOperationQueueKey{kind: kind, format: format, state: job.State}, job.CreatedAt)
	}
	for _, plan := range s.replicationPlans {
		state := replicationQueueState(plan)
		if !queueVisibleState(state) {
			continue
		}
		addBackgroundOperationQueueStat(aggregated, backgroundOperationQueueKey{kind: BackgroundOperationReplication, format: plan.Format, state: state}, plan.CreatedAt)
	}
	return sortedBackgroundOperationQueueStats(aggregated), nil
}

func replicationQueueState(plan ReplicationPlan) LifecycleJobState {
	if plan.State == string(LifecycleJobFailed) && plan.Attempts < plan.MaxAttempts {
		return LifecycleJobRetrying
	}
	return LifecycleJobState(plan.State)
}

func lifecycleQueueFormat(payload []byte) Format {
	var value struct {
		Format Format `json:"format"`
	}
	if json.Unmarshal(payload, &value) != nil {
		return ""
	}
	return value.Format
}

func queueVisibleState(state LifecycleJobState) bool {
	return state == LifecycleJobPending || state == LifecycleJobRetrying || state == LifecycleJobRunning || state == LifecycleJobFailed
}

func addBackgroundOperationQueueStat(aggregated map[backgroundOperationQueueKey]BackgroundOperationQueueStat, key backgroundOperationQueueKey, createdAt time.Time) {
	stat := aggregated[key]
	stat.Kind, stat.Format, stat.State = key.kind, key.format, key.state
	stat.Count++
	if stat.OldestCreatedAt.IsZero() || createdAt.Before(stat.OldestCreatedAt) {
		stat.OldestCreatedAt = createdAt
	}
	aggregated[key] = stat
}

func sortedBackgroundOperationQueueStats(aggregated map[backgroundOperationQueueKey]BackgroundOperationQueueStat) []BackgroundOperationQueueStat {
	stats := make([]BackgroundOperationQueueStat, 0, len(aggregated))
	for _, stat := range aggregated {
		stats = append(stats, stat)
	}
	sort.Slice(stats, func(i, j int) bool {
		if stats[i].Kind != stats[j].Kind {
			return stats[i].Kind < stats[j].Kind
		}
		if stats[i].Format != stats[j].Format {
			return stats[i].Format < stats[j].Format
		}
		return stats[i].State < stats[j].State
	})
	return stats
}
