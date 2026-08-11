package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// EnqueueArtifactScanJob creates an idempotent scan request for one immutable
// artifact. The payload intentionally contains no object-store location.
func EnqueueArtifactScanJob(ctx context.Context, store LifecycleJobStore, repositoryID, idempotencyKey string, payload ArtifactScanPayload) (LifecycleJob, bool, error) {
	if locker, ok := store.(ArtifactScanIdentityLockStore); ok {
		unlock, err := locker.LockArtifactScanIdentity(ctx, repositoryID, payload.Format, payload.Coordinate, payload.Digest)
		if err != nil {
			return LifecycleJob{}, false, err
		}
		defer unlock()
	}
	return EnqueueArtifactScanJobLocked(ctx, store, repositoryID, idempotencyKey, payload)
}

// EnqueueArtifactScanJobLocked enqueues a scan while the caller holds the
// artifact identity lock, avoiding a nested lock during reconciliation.
func EnqueueArtifactScanJobLocked(ctx context.Context, store LifecycleJobStore, repositoryID, idempotencyKey string, payload ArtifactScanPayload) (LifecycleJob, bool, error) {
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if idempotencyKey == "" || len(idempotencyKey) > 128 || payload.Format == "" || payload.Coordinate == "" || payload.Digest == "" {
		return LifecycleJob{}, false, ErrInvalidArtifactIntelligence
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return LifecycleJob{}, false, fmt.Errorf("marshal artifact scan payload: %w", err)
	}
	if existing, lookupErr := store.GetLatestArtifactScanJob(ctx, repositoryID, payload.Format, payload.Coordinate, payload.Digest); lookupErr == nil {
		switch existing.State {
		case LifecycleJobPending, LifecycleJobRunning, LifecycleJobRetrying:
			return existing, true, nil
		}
	} else if !errors.Is(lookupErr, ErrNotFound) {
		return LifecycleJob{}, false, lookupErr
	}
	return store.EnqueueLifecycleJob(ctx, LifecycleJob{
		ID: uuid.NewString(), RepositoryID: repositoryID, Kind: LifecycleJobScan,
		IdempotencyKey: idempotencyKey, Payload: body,
	})
}
