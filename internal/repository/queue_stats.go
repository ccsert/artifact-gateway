package repository

import "time"

type BackgroundOperationKind string

const (
	BackgroundOperationLifecycle   BackgroundOperationKind = "lifecycle"
	BackgroundOperationPromotion   BackgroundOperationKind = "promotion"
	BackgroundOperationReplication BackgroundOperationKind = "replication"
)

type BackgroundOperationQueueStat struct {
	Kind            BackgroundOperationKind
	Format          Format
	State           LifecycleJobState
	Count           int64
	OldestCreatedAt time.Time
}
