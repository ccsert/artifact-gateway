package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/google/uuid"
)

// ArtifactIntelligenceCopyPayload is persisted in a lifecycle job after a
// promotion publishes the target artifact. Keeping this as a separate job
// means a scanner outage cannot roll back or strand an already-published
// artifact.
type ArtifactIntelligenceCopyPayload struct {
	Format             Format `json:"format"`
	SourceRepositoryID string `json:"sourceRepositoryId"`
	Coordinate         string `json:"coordinate"`
	Digest             string `json:"digest"`
}

// EnqueueArtifactIntelligenceCopyJob creates an idempotent follow-up job. The
// key is derived from the immutable source identity, so promotion retries do
// not create duplicate metadata work.
func EnqueueArtifactIntelligenceCopyJob(ctx context.Context, store LifecycleJobStore, targetRepositoryID, sourceRepositoryID string, format Format, coordinate, digest string) (LifecycleJob, bool, error) {
	payload := ArtifactIntelligenceCopyPayload{Format: format, SourceRepositoryID: sourceRepositoryID, Coordinate: coordinate, Digest: digest}
	body, err := json.Marshal(payload)
	if err != nil {
		return LifecycleJob{}, false, err
	}
	key := "artifact-intelligence:" + sourceRepositoryID + ":" + string(format) + ":" + coordinate + ":" + digest
	return store.EnqueueLifecycleJob(ctx, LifecycleJob{ID: uuid.NewString(), RepositoryID: targetRepositoryID, Kind: LifecycleJobIntelligence, IdempotencyKey: key, Payload: body})
}

// CopyArtifactIntelligence copies immutable evidence without overwriting
// target-owned metadata. A missing source record is intentionally a no-op:
// security metadata is optional unless the target policy requires it.
func CopyArtifactIntelligence(ctx context.Context, store ArtifactIntelligenceStore, targetRepositoryID, sourceRepositoryID string, format Format, coordinate, digest string) error {
	source, err := store.GetArtifactIntelligence(ctx, sourceRepositoryID, format, coordinate, digest)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	target, err := store.GetArtifactIntelligence(ctx, targetRepositoryID, format, coordinate, digest)
	if err == nil {
		if equivalentArtifactIntelligence(source, target) {
			return nil
		}
		return ErrArtifactIntelligenceConflict
	}
	if !errors.Is(err, ErrNotFound) {
		return err
	}
	source.RepositoryID = targetRepositoryID
	if _, err = store.ReplaceArtifactIntelligence(ctx, source, ""); err == nil {
		return nil
	} else if !errors.Is(err, ErrVersionConflict) {
		return err
	}
	// Another worker may have won the first-write race. Re-read and only
	// accept it when the immutable evidence is exactly the same.
	target, readErr := store.GetArtifactIntelligence(ctx, targetRepositoryID, format, coordinate, digest)
	if readErr == nil && equivalentArtifactIntelligence(source, target) {
		return nil
	}
	if readErr != nil {
		return readErr
	}
	return ErrArtifactIntelligenceConflict
}

// CopyArtifactIntelligenceOrEnqueue performs the fast path and falls back to
// a durable task for transient storage failures. Conflicts are recorded in a
// failed follow-up task instead of failing an already-published promotion or
// silently overwriting target-owned evidence.
func CopyArtifactIntelligenceOrEnqueue(ctx context.Context, intelligence ArtifactIntelligenceStore, jobs LifecycleJobStore, targetRepositoryID, sourceRepositoryID string, format Format, coordinate, digest string) error {
	if intelligence == nil {
		return nil
	}
	err := CopyArtifactIntelligence(ctx, intelligence, targetRepositoryID, sourceRepositoryID, format, coordinate, digest)
	if err == nil {
		return err
	}
	if jobs == nil {
		return err
	}
	if _, _, enqueueErr := EnqueueArtifactIntelligenceCopyJob(ctx, jobs, targetRepositoryID, sourceRepositoryID, format, coordinate, digest); enqueueErr != nil {
		return fmt.Errorf("%w: %v", ErrArtifactIntelligenceDeferred, enqueueErr)
	}
	if errors.Is(err, ErrArtifactIntelligenceConflict) {
		return nil
	}
	return nil
}

func equivalentArtifactIntelligence(left, right ArtifactIntelligence) bool {
	left = normalizeArtifactIntelligence(left)
	right = normalizeArtifactIntelligence(right)
	return reflect.DeepEqual(left, right)
}

func normalizeArtifactIntelligence(value ArtifactIntelligence) ArtifactIntelligence {
	value.RepositoryID = ""
	value.Version = ""
	value.CreatedAt = time.Time{}
	value.UpdatedAt = time.Time{}
	value.UpdatedBy = ""
	if value.Signatures == nil {
		value.Signatures = []ArtifactSignature{}
	}
	if value.SBOMs == nil {
		value.SBOMs = []ArtifactSBOM{}
	}
	if value.Licenses == nil {
		value.Licenses = []ArtifactLicense{}
	}
	return value
}
