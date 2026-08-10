package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// EnqueueArtifactScanJob creates an idempotent scan request for one immutable
// artifact. The payload intentionally contains no object-store location.
func EnqueueArtifactScanJob(ctx context.Context, store LifecycleJobStore, repositoryID, idempotencyKey string, payload ArtifactScanPayload) (LifecycleJob, bool, error) {
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if idempotencyKey == "" || len(idempotencyKey) > 128 || payload.Format == "" || payload.Coordinate == "" || payload.Digest == "" {
		return LifecycleJob{}, false, ErrInvalidArtifactIntelligence
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return LifecycleJob{}, false, fmt.Errorf("marshal artifact scan payload: %w", err)
	}
	return store.EnqueueLifecycleJob(ctx, LifecycleJob{
		ID: uuid.NewString(), RepositoryID: repositoryID, Kind: LifecycleJobScan,
		IdempotencyKey: idempotencyKey, Payload: body,
	})
}
