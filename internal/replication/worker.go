// Package replication provides the durable byte-copy phase shared by Hosted
// format adapters. Publication remains format-specific and happens only after
// every checkpoint has been verified.
package replication

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/objectstore"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

type Worker struct {
	Store       repository.ReplicationStore
	Source      objectstore.Store
	Destination objectstore.Store
	ChunkBytes  int64
	Format      repository.Format
	// Publish makes verified bytes visible through a format-specific metadata
	// transaction. It must be idempotent because a publish failure is retried.
	Publish func(context.Context, repository.ReplicationPlan, []repository.ReplicationCheckpoint) error
	// LockObject coordinates source reads and destination publication with
	// format-specific garbage collectors. It is optional for legacy formats.
	LockObject func(context.Context, string) (func(), error)
	// AdmissionSnapshot reloads an aggregate publication unit while its final
	// distribution lock is held. It returns every current digest and whether
	// the durable checkpoints still cover the complete immutable snapshot.
	AdmissionSnapshot func(context.Context, repository.ReplicationPlan, []repository.ReplicationCheckpoint) ([]string, bool, error)
	Metrics           repository.BackgroundOperationMetrics
}

func (w Worker) Run(ctx context.Context, limit int) error {
	var plans []repository.ReplicationPlan
	var err error
	if w.Format == "" {
		plans, err = w.Store.ClaimReplicationPlans(ctx, limit)
	} else {
		plans, err = w.Store.ClaimReplicationPlansByFormat(ctx, w.Format, limit)
	}
	if err != nil {
		return err
	}
	for _, plan := range plans {
		w.begin(plan.Format)
		if err := w.runPlan(ctx, plan); err != nil {
			w.end(plan.Format, "failed")
			return err
		}
		w.end(plan.Format, "completed")
	}
	return nil
}

func (w Worker) begin(format repository.Format) {
	if w.Metrics == nil {
		return
	}
	w.Metrics.RecordBackgroundOperation("replication", format, "started")
	w.Metrics.AddBackgroundOperationInFlight("replication", format, 1)
}
func (w Worker) end(format repository.Format, outcome string) {
	if w.Metrics == nil {
		return
	}
	w.Metrics.RecordBackgroundOperation("replication", format, outcome)
	w.Metrics.AddBackgroundOperationInFlight("replication", format, -1)
}

func (w Worker) Start(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		return
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_ = w.Run(ctx, 100)
			}
		}
	}()
}

func (w Worker) runPlan(ctx context.Context, plan repository.ReplicationPlan) error {
	if w.Store == nil || w.Source == nil || w.Destination == nil {
		return w.failPlan(ctx, plan.ID, "replication worker is not configured", plan.LeaseToken)
	}
	checks, err := w.Store.ListReplicationCheckpoints(ctx, plan.ID)
	if err != nil {
		return w.failPlan(ctx, plan.ID, "load replication checkpoints failed", plan.LeaseToken)
	}
	if plan.Coordinate == "" || plan.Digest == "" {
		return w.failPlan(ctx, plan.ID, "replication artifact identity is unavailable", plan.LeaseToken)
	}
	releaseEarlyAdmission, admissionErr := repository.LockArtifactDistributionUnit(ctx, w.Store, plan.SourceRepositoryID, plan.Format, plan.Coordinate, replicationAdmissionDigests(plan, checks))
	if admissionErr != nil {
		return w.failPlan(ctx, plan.ID, "coordinate artifact quarantine failed", plan.LeaseToken)
	}
	allowed, admissionErr := repository.ArtifactDistributionAllowedForDigests(ctx, w.Store, plan.SourceRepositoryID, plan.Format, plan.Coordinate, replicationAdmissionDigests(plan, checks))
	if admissionErr != nil {
		releaseEarlyAdmission()
		return w.failPlan(ctx, plan.ID, "evaluate artifact quarantine failed", plan.LeaseToken)
	}
	if !allowed {
		parkErr := w.parkPlan(ctx, plan.ID, repository.ArtifactQuarantinedReason, plan.LeaseToken)
		releaseEarlyAdmission()
		return parkErr
	}
	releaseEarlyAdmission()
	if w.Metrics != nil {
		for _, checkpoint := range checks {
			if checkpoint.Attempts > 0 {
				w.Metrics.RecordBackgroundOperation("replication", plan.Format, "retried")
				break
			}
		}
	}
	if w.LockObject != nil {
		objectKeys := make([]string, 0, len(checks)*2)
		for _, checkpoint := range checks {
			objectKeys = append(objectKeys, checkpoint.SourceObjectKey, checkpoint.ObjectKey)
		}
		lockedCtx, release, lockErr := repository.LockObjectKeys(ctx, objectKeys, w.Store, plan.Format, w.LockObject)
		if lockErr != nil {
			return w.failPlan(ctx, plan.ID, "replication object coordination failed", plan.LeaseToken)
		}
		ctx = lockedCtx
		defer release()
	}
	for _, checkpoint := range checks {
		if checkpoint.State == "verified" {
			continue
		}
		if err := w.copyCheckpoint(ctx, checkpoint, plan.LeaseToken); err != nil {
			// copyCheckpoint persists each successful chunk before attempting the
			// next one. Reload that durable offset so failure bookkeeping cannot
			// roll a completed chunk back to its stale in-memory value.
			if persisted, loadErr := w.Store.ListReplicationCheckpoints(ctx, plan.ID); loadErr == nil {
				for _, candidate := range persisted {
					if candidate.ObjectKey == checkpoint.ObjectKey {
						checkpoint = candidate
						break
					}
				}
			}
			checkpoint.State = "failed"
			checkpoint.Attempts++
			checkpoint.LastError = err.Error()
			if updateErr := w.Store.UpdateReplicationCheckpointWithLease(ctx, checkpoint, plan.LeaseToken); updateErr != nil {
				return updateErr
			}
			return w.failPlan(ctx, plan.ID, "replication checkpoint failed: "+checkpoint.ObjectKey, plan.LeaseToken)
		}
	}
	if w.Publish != nil {
		// Renew the lease from durable verified state immediately before
		// publication. Recovery changes the token, fencing stale workers here.
		checks, err = w.Store.ListReplicationCheckpoints(ctx, plan.ID)
		if err != nil {
			return w.failPlan(ctx, plan.ID, "reload verified replication checkpoints failed", plan.LeaseToken)
		}
		if len(checks) == 0 {
			return w.failPlan(ctx, plan.ID, "replication plan has no checkpoints", plan.LeaseToken)
		}
		for _, checkpoint := range checks {
			if checkpoint.State != "verified" {
				return w.failPlan(ctx, plan.ID, "replication checkpoint is not verified: "+checkpoint.ObjectKey, plan.LeaseToken)
			}
		}
		if err := w.Store.UpdateReplicationCheckpointWithLease(ctx, checks[len(checks)-1], plan.LeaseToken); err != nil {
			return err
		}
		admissionDigests := replicationAdmissionDigests(plan, checks)
		snapshotUnchanged := true
		admissionCtx := ctx
		var releaseAdmission func()
		if plan.Format == repository.FormatPyPI {
			admissionCtx, releaseAdmission, admissionErr = repository.LockArtifactDistributionCoordinates(ctx, w.Store, []repository.ArtifactDistributionCoordinate{
				{RepositoryID: plan.SourceRepositoryID, Format: plan.Format, Coordinate: plan.Coordinate},
				{RepositoryID: plan.TargetRepositoryID, Format: plan.Format, Coordinate: plan.Coordinate},
			})
		} else {
			releaseAdmission, admissionErr = repository.LockArtifactDistributionUnit(ctx, w.Store, plan.SourceRepositoryID, plan.Format, plan.Coordinate, admissionDigests)
		}
		if admissionErr != nil {
			return w.failPlan(ctx, plan.ID, "coordinate artifact quarantine failed", plan.LeaseToken)
		}
		if w.AdmissionSnapshot != nil {
			admissionDigests, snapshotUnchanged, admissionErr = w.AdmissionSnapshot(admissionCtx, plan, checks)
			if admissionErr != nil {
				releaseAdmission()
				return w.failPlan(ctx, plan.ID, "reload distribution admission identities failed", plan.LeaseToken)
			}
		}
		allowed, admissionErr = repository.ArtifactDistributionAllowedForDigests(admissionCtx, w.Store, plan.SourceRepositoryID, plan.Format, plan.Coordinate, admissionDigests)
		if admissionErr != nil {
			releaseAdmission()
			return w.failPlan(ctx, plan.ID, "evaluate artifact quarantine failed", plan.LeaseToken)
		}
		if !allowed {
			parkErr := w.parkPlan(admissionCtx, plan.ID, repository.ArtifactQuarantinedReason, plan.LeaseToken)
			releaseAdmission()
			return parkErr
		}
		if !snapshotUnchanged {
			parkErr := w.parkPlan(admissionCtx, plan.ID, repository.ReplicationSnapshotChangedReason, plan.LeaseToken)
			releaseAdmission()
			return parkErr
		}
		if err := w.Publish(admissionCtx, plan, checks); err != nil {
			releaseAdmission()
			return w.failPlan(ctx, plan.ID, "publish replicated artifacts failed", plan.LeaseToken)
		}
		releaseAdmission()
	}
	return w.Store.CompleteReplicationPlanWithLease(ctx, plan.ID, plan.LeaseToken)
}

func replicationAdmissionDigests(plan repository.ReplicationPlan, checkpoints []repository.ReplicationCheckpoint) []string {
	digests := []string{plan.Digest}
	if plan.Format != repository.FormatPyPI {
		return digests
	}
	for _, checkpoint := range checkpoints {
		digests = append(digests, checkpoint.Digest)
	}
	return digests
}

func (w Worker) copyCheckpoint(ctx context.Context, checkpoint repository.ReplicationCheckpoint, leaseToken string) error {
	if checkpoint.Size < 0 || checkpoint.ByteOffset < 0 || checkpoint.ByteOffset > checkpoint.Size || !validSHA256(checkpoint.Digest) {
		return fmt.Errorf("invalid checkpoint")
	}
	sourceObjectKey := checkpoint.SourceObjectKey
	if sourceObjectKey == "" {
		sourceObjectKey = checkpoint.ObjectKey
	}
	if info, err := w.Source.Stat(ctx, sourceObjectKey); err != nil {
		return fmt.Errorf("stat source: %w", err)
	} else if info.Size != checkpoint.Size {
		return fmt.Errorf("source size mismatch")
	}
	if checkpoint.ByteOffset == 0 {
		existing, err := w.Destination.Get(ctx, checkpoint.ObjectKey)
		if err == nil && int64(len(existing)) == checkpoint.Size && sha256Digest(existing) == checkpoint.Digest {
			if err := w.Destination.SetVerifiedDigest(ctx, checkpoint.ObjectKey, checkpoint.Digest); err != nil {
				return fmt.Errorf("record existing destination digest: %w", err)
			}
			checkpoint.State = "verified"
			checkpoint.VerifiedAt = time.Now().UTC()
			return w.Store.UpdateReplicationCheckpointWithLease(ctx, checkpoint, leaseToken)
		}
		if err != nil && err != objectstore.ErrNotFound {
			return fmt.Errorf("read destination object: %w", err)
		}
		if err == nil {
			return fmt.Errorf("destination object conflicts with checkpoint")
		}
	}
	if checkpoint.ByteOffset > 0 {
		partial, err := w.Destination.Get(ctx, checkpoint.ObjectKey)
		if err != nil || int64(len(partial)) != checkpoint.ByteOffset {
			return fmt.Errorf("checkpoint does not match destination partial object")
		}
	}
	chunkSize := w.ChunkBytes
	if chunkSize <= 0 {
		chunkSize = 8 << 20
	}
	for checkpoint.ByteOffset < checkpoint.Size {
		length := min(chunkSize, checkpoint.Size-checkpoint.ByteOffset)
		reader, _, err := w.Source.OpenRange(ctx, sourceObjectKey, checkpoint.ByteOffset, length)
		if err != nil {
			return fmt.Errorf("read source: %w", err)
		}
		chunk, readErr := io.ReadAll(reader)
		closeErr := reader.Close()
		if readErr != nil || closeErr != nil || int64(len(chunk)) != length {
			return fmt.Errorf("read source chunk failed")
		}
		prefix, err := w.Destination.Get(ctx, checkpoint.ObjectKey)
		if err == objectstore.ErrNotFound && checkpoint.ByteOffset == 0 {
			prefix = nil
		} else if err != nil || int64(len(prefix)) != checkpoint.ByteOffset {
			return fmt.Errorf("read destination partial object failed")
		}
		body := append(prefix, chunk...)
		if err := w.Destination.Put(ctx, checkpoint.ObjectKey, body); err != nil {
			return fmt.Errorf("write destination: %w", err)
		}
		checkpoint.ByteOffset += length
		checkpoint.State = "copying"
		checkpoint.LastError = ""
		if err := w.Store.UpdateReplicationCheckpointWithLease(ctx, checkpoint, leaseToken); err != nil {
			return err
		}
	}
	body, err := w.Destination.Get(ctx, checkpoint.ObjectKey)
	if err != nil || int64(len(body)) != checkpoint.Size || sha256Digest(body) != checkpoint.Digest {
		return fmt.Errorf("destination sha256 verification failed")
	}
	if err := w.Destination.SetVerifiedDigest(ctx, checkpoint.ObjectKey, checkpoint.Digest); err != nil {
		return fmt.Errorf("record destination digest: %w", err)
	}
	checkpoint.State = "verified"
	checkpoint.LastError = ""
	checkpoint.VerifiedAt = time.Now().UTC()
	return w.Store.UpdateReplicationCheckpointWithLease(ctx, checkpoint, leaseToken)
}

func (w Worker) failPlan(ctx context.Context, id, message, leaseToken string) error {
	if err := w.Store.FailReplicationPlanWithLease(ctx, id, message, leaseToken); err != nil {
		return err
	}
	return nil
}

func (w Worker) parkPlan(ctx context.Context, id, message, leaseToken string) error {
	return w.Store.ParkReplicationPlanWithLease(ctx, id, message, leaseToken)
}

func validSHA256(value string) bool {
	if !strings.HasPrefix(value, "sha256:") || len(value) != len("sha256:")+64 {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	return err == nil
}

func sha256Digest(body []byte) string {
	sum := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func min(left, right int64) int64 {
	if left < right {
		return left
	}
	return right
}
