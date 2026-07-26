package app

import (
	"context"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

type AuditRetentionMetrics interface{ RecordAuditRetentionCleanup(string, int) }
type AuditRetentionWorker struct {
	Store   repository.AuditRetentionStore
	Metrics AuditRetentionMetrics
}

func (w AuditRetentionWorker) Enqueue(ctx context.Context, key string, batchSize int) (repository.AuditCleanupJob, bool, error) {
	p, err := w.Store.GetAuditRetentionPolicy(ctx)
	if err != nil {
		return repository.AuditCleanupJob{}, false, err
	}
	if batchSize <= 0 || batchSize > 1000 {
		batchSize = 1000
	}
	return w.Store.EnqueueAuditCleanupJob(ctx, repository.AuditCleanupJob{ID: uuid.NewString(), IdempotencyKey: key, PolicyVersion: p.Version, CutoffAt: time.Now().UTC().AddDate(0, 0, -p.KeepDays), BatchSize: batchSize})
}
func (w AuditRetentionWorker) RunJobs(ctx context.Context, limit int) error {
	jobs, err := w.Store.ClaimAuditCleanupJobs(ctx, limit)
	if err != nil {
		return err
	}
	for _, j := range jobs {
		p, err := w.Store.GetAuditRetentionPolicy(ctx)
		if err != nil {
			_ = w.Store.FailAuditCleanupJob(ctx, j.ID, "get audit retention policy failed")
			continue
		}
		if !p.Enabled || p.Version != j.PolicyVersion {
			_ = w.Store.FailAuditCleanupJob(ctx, j.ID, "audit retention policy changed or is disabled")
			continue
		}
		batchSize := j.BatchSize
		if batchSize <= 0 || batchSize > 1000 {
			batchSize = 1000
		}
		deleted := 0
		for {
			n, err := w.Store.DeleteAuditsBefore(ctx, j.CutoffAt, batchSize)
			if err != nil {
				_ = w.Store.FailAuditCleanupJob(ctx, j.ID, "delete expired audits failed")
				if w.Metrics != nil {
					w.Metrics.RecordAuditRetentionCleanup("failed", deleted)
				}
				break
			}
			deleted += n
			if n < batchSize {
				if err = w.Store.CompleteAuditCleanupJob(ctx, j.ID, deleted); err != nil {
					return err
				}
				if w.Metrics != nil {
					w.Metrics.RecordAuditRetentionCleanup("completed", deleted)
				}
				break
			}
		}
	}
	return nil
}
func (w AuditRetentionWorker) Start(ctx context.Context, interval time.Duration) {
	go func() {
		_ = w.RunJobs(ctx, 100)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_ = w.RunJobs(ctx, 100)
			}
		}
	}()
}
