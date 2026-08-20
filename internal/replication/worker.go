// Package replication provides the durable byte-copy phase shared by Hosted
// format adapters. Publication remains format-specific and happens only after
// every checkpoint has been verified.
package replication

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
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
	AdmissionSnapshot      func(context.Context, repository.ReplicationPlan, []repository.ReplicationCheckpoint) ([]string, bool, error)
	Metrics                repository.BackgroundOperationMetrics
	LeaseHeartbeatInterval time.Duration
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
	operationCtx := ctx
	workCtx, heartbeat, heartbeatErr := w.startLeaseHeartbeat(ctx, plan.ID, plan.LeaseToken)
	if heartbeatErr != nil {
		return heartbeatErr
	}
	defer func() { _ = heartbeat.stop() }()
	ctx = workCtx
	fail := func(message string) error {
		if leaseErr := heartbeat.stop(); leaseErr != nil {
			return leaseErr
		}
		return w.failPlan(operationCtx, plan.ID, message, plan.LeaseToken)
	}
	park := func(message string) error {
		if leaseErr := heartbeat.stop(); leaseErr != nil {
			return leaseErr
		}
		return w.parkPlan(operationCtx, plan.ID, message, plan.LeaseToken)
	}
	checks, err := w.Store.ListReplicationCheckpoints(ctx, plan.ID)
	if err != nil {
		return fail("load replication checkpoints failed")
	}
	if plan.Coordinate == "" || plan.Digest == "" {
		return fail("replication artifact identity is unavailable")
	}
	earlyAdmissionCtx, releaseEarlyAdmission, admissionErr := repository.LockArtifactDistributionUnitContext(ctx, w.Store, plan.SourceRepositoryID, plan.Format, plan.Coordinate, replicationAdmissionDigests(plan, checks))
	if admissionErr != nil {
		return fail("coordinate artifact quarantine failed")
	}
	allowed, admissionErr := repository.ArtifactDistributionAllowedForDigests(earlyAdmissionCtx, w.Store, plan.SourceRepositoryID, plan.Format, plan.Coordinate, replicationAdmissionDigests(plan, checks))
	if admissionErr != nil {
		releaseEarlyAdmission()
		return fail("evaluate artifact quarantine failed")
	}
	if !allowed {
		parkErr := park(repository.ArtifactQuarantinedReason)
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
			return fail("replication object coordination failed")
		}
		ctx = lockedCtx
		defer release()
	}
	for _, checkpoint := range checks {
		if checkpoint.State == "verified" {
			continue
		}
		if err := w.copyCheckpoint(ctx, checkpoint, plan.LeaseToken); err != nil {
			if leaseErr := heartbeat.stop(); leaseErr != nil {
				return leaseErr
			}
			// Reload durable state so failure bookkeeping cannot roll a completed
			// object back to its stale in-memory checkpoint.
			if persisted, loadErr := w.Store.ListReplicationCheckpoints(operationCtx, plan.ID); loadErr == nil {
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
			if updateErr := w.Store.UpdateReplicationCheckpointWithLease(operationCtx, checkpoint, plan.LeaseToken); updateErr != nil {
				return updateErr
			}
			return w.failPlan(operationCtx, plan.ID, "replication checkpoint failed: "+checkpoint.ObjectKey, plan.LeaseToken)
		}
	}
	if w.Publish != nil {
		// Renew the lease from durable verified state immediately before
		// publication. Recovery changes the token, fencing stale workers here.
		checks, err = w.Store.ListReplicationCheckpoints(ctx, plan.ID)
		if err != nil {
			return fail("reload verified replication checkpoints failed")
		}
		if len(checks) == 0 {
			return fail("replication plan has no checkpoints")
		}
		for _, checkpoint := range checks {
			if checkpoint.State != "verified" {
				return fail("replication checkpoint is not verified: " + checkpoint.ObjectKey)
			}
		}
		if err := w.Store.UpdateReplicationCheckpointWithLease(ctx, checks[len(checks)-1], plan.LeaseToken); err != nil {
			return err
		}
		admissionDigests := replicationAdmissionDigests(plan, checks)
		snapshotUnchanged := true
		var admissionCtx context.Context
		var releaseAdmission func()
		if plan.Format == repository.FormatPyPI || plan.Format == repository.FormatGo {
			admissionCtx, releaseAdmission, admissionErr = repository.LockArtifactDistributionCoordinates(ctx, w.Store, []repository.ArtifactDistributionCoordinate{
				{RepositoryID: plan.SourceRepositoryID, Format: plan.Format, Coordinate: plan.Coordinate},
				{RepositoryID: plan.TargetRepositoryID, Format: plan.Format, Coordinate: plan.Coordinate},
			})
		} else {
			admissionCtx, releaseAdmission, admissionErr = repository.LockArtifactDistributionUnitContext(ctx, w.Store, plan.SourceRepositoryID, plan.Format, plan.Coordinate, admissionDigests)
		}
		if admissionErr != nil {
			return fail("coordinate artifact quarantine failed")
		}
		if w.AdmissionSnapshot != nil {
			admissionDigests, snapshotUnchanged, admissionErr = w.AdmissionSnapshot(admissionCtx, plan, checks)
			if admissionErr != nil {
				releaseAdmission()
				return fail("reload distribution admission identities failed")
			}
		}
		allowed, admissionErr = repository.ArtifactDistributionAllowedForDigests(admissionCtx, w.Store, plan.SourceRepositoryID, plan.Format, plan.Coordinate, admissionDigests)
		if admissionErr != nil {
			releaseAdmission()
			return fail("evaluate artifact quarantine failed")
		}
		if !allowed {
			releaseAdmission()
			return park(repository.ArtifactQuarantinedReason)
		}
		if !snapshotUnchanged {
			releaseAdmission()
			return park(repository.ReplicationSnapshotChangedReason)
		}
		// The lease fence is held from final validation through metadata publish
		// and plan completion. Recovery takes the same fence, so it cannot hand
		// ownership to another worker in the publication commit window.
		releaseLease, leaseErr := w.Store.LockReplicationPlanLease(admissionCtx, plan.ID, plan.LeaseToken)
		if leaseErr != nil {
			releaseAdmission()
			if heartbeatErr := heartbeat.stop(); heartbeatErr != nil {
				return heartbeatErr
			}
			return leaseErr
		}
		if err := w.Publish(admissionCtx, plan, checks); err != nil {
			releaseLease()
			releaseAdmission()
			return fail("publish replicated artifacts failed")
		}
		heartbeatErr := heartbeat.stop()
		completeErr := w.Store.CompleteReplicationPlanWithLease(operationCtx, plan.ID, plan.LeaseToken)
		releaseLease()
		releaseAdmission()
		if completeErr == nil {
			return nil
		}
		if heartbeatErr != nil {
			return heartbeatErr
		}
		return completeErr
	}
	releaseLease, leaseErr := w.Store.LockReplicationPlanLease(ctx, plan.ID, plan.LeaseToken)
	if leaseErr != nil {
		if heartbeatErr := heartbeat.stop(); heartbeatErr != nil {
			return heartbeatErr
		}
		return leaseErr
	}
	heartbeatErr = heartbeat.stop()
	completeErr := w.Store.CompleteReplicationPlanWithLease(operationCtx, plan.ID, plan.LeaseToken)
	releaseLease()
	if completeErr == nil {
		return nil
	}
	if heartbeatErr != nil {
		return heartbeatErr
	}
	return completeErr
}

type replicationLeaseHeartbeat struct {
	cancel context.CancelFunc
	done   chan error
}

func (w Worker) startLeaseHeartbeat(ctx context.Context, planID, leaseToken string) (context.Context, *replicationLeaseHeartbeat, error) {
	if err := w.Store.RenewReplicationPlanLease(ctx, planID, leaseToken); err != nil {
		return ctx, nil, err
	}
	interval := w.LeaseHeartbeatInterval
	if interval <= 0 {
		interval = time.Minute
	}
	workCtx, cancel := context.WithCancel(ctx)
	heartbeat := &replicationLeaseHeartbeat{cancel: cancel, done: make(chan error, 1)}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-workCtx.Done():
				heartbeat.done <- nil
				return
			case <-ticker.C:
				if err := w.Store.RenewReplicationPlanLease(workCtx, planID, leaseToken); err != nil {
					if workCtx.Err() != nil {
						heartbeat.done <- nil
						return
					}
					heartbeat.done <- err
					cancel()
					return
				}
			}
		}
	}()
	return workCtx, heartbeat, nil
}

func (h *replicationLeaseHeartbeat) stop() error {
	if h == nil {
		return nil
	}
	h.cancel()
	err, ok := <-h.done
	if !ok {
		return nil
	}
	close(h.done)
	return err
}

func replicationAdmissionDigests(plan repository.ReplicationPlan, checkpoints []repository.ReplicationCheckpoint) []string {
	digests := []string{plan.Digest}
	if plan.Format != repository.FormatPyPI && plan.Format != repository.FormatGo {
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
	if info, err := w.Destination.Stat(ctx, checkpoint.ObjectKey); err == nil {
		if info.Size == checkpoint.Size {
			matches := info.Digest == checkpoint.Digest
			if !matches && info.Digest == "" {
				matches, err = objectDigestMatches(ctx, w.Destination, checkpoint.ObjectKey, checkpoint.Size, checkpoint.Digest)
				if err != nil {
					return fmt.Errorf("verify existing destination object: %w", err)
				}
			}
			if !matches {
				return fmt.Errorf("destination object conflicts with checkpoint")
			}
			if err := w.Destination.SetVerifiedDigest(ctx, checkpoint.ObjectKey, checkpoint.Digest); err != nil {
				return fmt.Errorf("record existing destination digest: %w", err)
			}
			return w.verifyCheckpoint(ctx, checkpoint, leaseToken)
		}
		// A shorter object can be a legacy partial upload, including the
		// crash window where the object advanced before byte_offset did. The
		// target key is held exclusively, so a full streaming overwrite is a
		// safe reset. Larger objects remain a conflict.
		if info.Size > checkpoint.Size {
			return fmt.Errorf("destination object conflicts with checkpoint")
		}
	} else if err != objectstore.ErrNotFound {
		return fmt.Errorf("stat destination object: %w", err)
	}
	sourceReader, openedSize, err := w.Source.Open(ctx, sourceObjectKey)
	if err != nil {
		return fmt.Errorf("open source: %w", err)
	}
	if openedSize != checkpoint.Size {
		_ = sourceReader.Close()
		return fmt.Errorf("source size mismatch")
	}
	hasher := sha256.New()
	counted := &countingHashReader{reader: sourceReader, hash: hasher}
	putErr := w.Destination.PutReader(ctx, checkpoint.ObjectKey, counted, checkpoint.Size)
	closeErr := sourceReader.Close()
	if putErr != nil {
		return fmt.Errorf("write destination: %w", putErr)
	}
	if closeErr != nil || counted.bytes != checkpoint.Size || "sha256:"+hex.EncodeToString(hasher.Sum(nil)) != checkpoint.Digest {
		_ = w.Destination.Delete(ctx, checkpoint.ObjectKey)
		return fmt.Errorf("source sha256 verification failed")
	}
	if err := w.Destination.SetVerifiedDigest(ctx, checkpoint.ObjectKey, checkpoint.Digest); err != nil {
		return fmt.Errorf("record destination digest: %w", err)
	}
	return w.verifyCheckpoint(ctx, checkpoint, leaseToken)
}

func (w Worker) verifyCheckpoint(ctx context.Context, checkpoint repository.ReplicationCheckpoint, leaseToken string) error {
	checkpoint.ByteOffset = checkpoint.Size
	checkpoint.State = "verified"
	checkpoint.LastError = ""
	checkpoint.VerifiedAt = time.Now().UTC()
	return w.Store.UpdateReplicationCheckpointWithLease(ctx, checkpoint, leaseToken)
}

func objectDigestMatches(ctx context.Context, store objectstore.Store, key string, size int64, digest string) (bool, error) {
	reader, openedSize, err := store.Open(ctx, key)
	if err != nil {
		return false, err
	}
	hasher := sha256.New()
	written, copyErr := io.Copy(hasher, reader)
	closeErr := reader.Close()
	if copyErr != nil {
		return false, copyErr
	}
	if closeErr != nil {
		return false, closeErr
	}
	return openedSize == size && written == size && "sha256:"+hex.EncodeToString(hasher.Sum(nil)) == digest, nil
}

type countingHashReader struct {
	reader io.Reader
	hash   hash.Hash
	bytes  int64
}

func (r *countingHashReader) Read(buffer []byte) (int, error) {
	count, err := r.reader.Read(buffer)
	if count > 0 {
		_, _ = r.hash.Write(buffer[:count])
		r.bytes += int64(count)
	}
	return count, err
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
