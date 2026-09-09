package aptpublication

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/lifecycle"
	"github.com/artifact-gateway/artifact-gateway/internal/objectstore"
	"github.com/artifact-gateway/artifact-gateway/internal/replication"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

type distributionStore interface {
	publisherStore
	repository.APTArtifactStore
	repository.LifecycleJobStore
	repository.ReplicationStore
}

// Distribution stages immutable package bytes, then appends them to a freshly
// signed target suite. Source Release/Packages bytes are never copied.
type Distribution struct {
	Store                distributionStore
	Source, Destination  objectstore.Store
	Publisher            *Publisher
	Intelligence         repository.ArtifactIntelligenceStore
	Metrics              repository.BackgroundOperationMetrics
	LeaseRefreshInterval time.Duration
	ChunkBytes           int64
}

type PromotionPayload struct {
	Format             repository.Format `json:"format"`
	SourceRepositoryID string            `json:"sourceRepositoryId"`
	Coordinate         string            `json:"coordinate"`
	Digest             string            `json:"digest"`
	TargetSuite        string            `json:"aptTargetSuite"`
	Actor              string            `json:"actor"`
}

func (d Distribution) EnqueuePromotion(ctx context.Context, targetID, key string, p PromotionPayload) (repository.LifecycleJob, bool, error) {
	p.Format = repository.FormatAPT
	if !validPromotionPayload(p) || targetID == p.SourceRepositoryID || key == "" || len(key) > 128 {
		return repository.LifecycleJob{}, false, ErrInvalidSessionInput
	}
	body, err := json.Marshal(p)
	if err != nil {
		return repository.LifecycleJob{}, false, err
	}
	return d.Store.EnqueueLifecycleJob(ctx, repository.LifecycleJob{ID: uuid.NewString(), RepositoryID: targetID, Kind: repository.LifecycleJobPromotion, IdempotencyKey: key, Payload: body})
}
func validPromotionPayload(p PromotionPayload) bool {
	return p.Format == repository.FormatAPT && p.SourceRepositoryID != "" && repository.ValidAPTArtifactCoordinate(p.Coordinate) && repository.ValidAPTSHA256Digest(p.Digest) && repository.ValidAPTPublicationScope(p.TargetSuite) && p.Actor != "" && len(p.Actor) <= 512
}
func (d Distribution) runtime() lifecycle.Runtime {
	return lifecycle.Runtime{Store: d.Store, Kind: repository.LifecycleJobPromotion, Formats: []repository.Format{repository.FormatAPT}, Name: "APT promotion", Operation: "promotion", Metrics: d.Metrics, LeaseRefreshInterval: d.LeaseRefreshInterval, LeaseProgressMessage: "signing target APT snapshot"}
}
func (d Distribution) RunPromotionJobs(ctx context.Context, limit int) error {
	return d.runtime().RunJobs(ctx, limit, d.promote)
}
func (d Distribution) StartPromotion(ctx context.Context, interval time.Duration) {
	d.runtime().Start(ctx, interval, 100, d.promote)
}
func (d Distribution) promote(ctx context.Context, job repository.LifecycleJob) error {
	var p PromotionPayload
	if json.Unmarshal(job.Payload, &p) != nil || !validPromotionPayload(p) {
		return ErrInvalidSessionInput
	}
	return d.publish(ctx, "promote", job.ID, job.LeaseToken, job.RepositoryID, p, nil)
}
func (d Distribution) worker() replication.Worker {
	return replication.Worker{Store: d.Store, Source: d.Source, Destination: d.Destination, Format: repository.FormatAPT, ChunkBytes: d.ChunkBytes, Publish: d.replicate, LockObject: d.Store.LockAPTObject, AdmissionSnapshot: d.admissionSnapshot, Metrics: d.Metrics, LeaseHeartbeatInterval: d.LeaseRefreshInterval}
}
func (d Distribution) RunReplicationJobs(ctx context.Context, limit int) error {
	return d.worker().Run(ctx, limit)
}
func (d Distribution) StartReplication(ctx context.Context, interval time.Duration) {
	d.worker().Start(ctx, interval)
}
func (d Distribution) admissionSnapshot(ctx context.Context, plan repository.ReplicationPlan, checks []repository.ReplicationCheckpoint) ([]string, bool, error) {
	a, err := d.Store.GetAPTScanAsset(ctx, plan.SourceRepositoryID, plan.Coordinate, plan.Digest)
	if errors.Is(err, repository.ErrNotFound) {
		return []string{plan.Digest}, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return []string{a.Digest}, len(checks) == 1 && matchesCheckpoint(a, checks[0]), nil
}
func matchesCheckpoint(a repository.APTSnapshotAsset, c repository.ReplicationCheckpoint) bool {
	return c.SourceObjectKey == a.ObjectKey && c.ObjectKey == a.ObjectKey && c.Digest == a.Digest && c.Size == a.Size
}
func (d Distribution) replicate(ctx context.Context, plan repository.ReplicationPlan, checks []repository.ReplicationCheckpoint) error {
	p := PromotionPayload{Format: repository.FormatAPT, SourceRepositoryID: plan.SourceRepositoryID, Coordinate: plan.Coordinate, Digest: plan.Digest, TargetSuite: plan.APTTargetSuite, Actor: "replication-worker"}
	if !validPromotionPayload(p) || len(checks) != 1 || checks[0].State != "verified" {
		return ErrInvalidSessionInput
	}
	return d.publish(ctx, "replicate", plan.ID, plan.LeaseToken, plan.TargetRepositoryID, p, checks)
}

func (d Distribution) publish(ctx context.Context, operation, workID, leaseToken, targetID string, p PromotionPayload, checks []repository.ReplicationCheckpoint) error {
	if d.Publisher == nil || d.Source == nil || d.Destination == nil {
		return ErrSignerUnavailable
	}
	commandID := uuid.NewSHA1(uuid.NameSpaceURL, []byte("apt-distribution/"+operation+"/"+workID)).String()
	body, _ := json.Marshal(struct {
		Target  string
		Payload PromotionPayload
	}{targetID, p})
	requestDigest := digestBytes(body)
	// A committed result is an acknowledgement, even after subsequent deletion
	// or retirement. Replaying the worker never reactivates its old snapshot.
	if result, e := d.Store.GetAPTLifecycleResult(ctx, commandID); e == nil {
		if result.RequestDigest != requestDigest {
			return repository.ErrIdempotencyConflict
		}
		return d.copyIntelligence(ctx, targetID, p)
	} else if !errors.Is(e, repository.ErrNotFound) {
		return e
	}
	source, err := d.Store.GetAPTScanAsset(ctx, p.SourceRepositoryID, p.Coordinate, p.Digest)
	if err != nil {
		return err
	}
	if operation == "replicate" && !matchesCheckpoint(source, checks[0]) {
		return repository.ErrVersionConflict
	}
	allowed, err := repository.ArtifactDistributionAllowedForDigests(ctx, d.Store, p.SourceRepositoryID, repository.FormatAPT, p.Coordinate, []string{p.Digest})
	if err != nil {
		return err
	}
	if !allowed {
		return repository.ErrArtifactQuarantined
	}
	objectCtx, releaseObject, err := repository.LockObjectKeys(ctx, []string{source.ObjectKey}, d.Store, repository.FormatAPT, d.Store.LockAPTObject)
	if err != nil {
		return err
	}
	defer releaseObject()
	locked, unlock, err := repository.LockObjectKeys(objectCtx, []string{"apt-distribution-suite/" + targetID + "/" + p.TargetSuite, "apt-distribution/" + commandID}, d.Store, repository.FormatAPT, d.Store.LockAPTObject)
	if err != nil {
		return err
	}
	defer unlock()
	objectCtx = locked
	if result, e := d.Store.GetAPTLifecycleResult(objectCtx, commandID); e == nil {
		if result.RequestDigest != requestDigest {
			return repository.ErrIdempotencyConflict
		}
		return d.copyIntelligence(objectCtx, targetID, p)
	} else if !errors.Is(e, repository.ErrNotFound) {
		return e
	}
	// Parse and verify the destination bytes again, including resumed verified
	// checkpoints. Staging records target-owned metadata and durable references.
	objects := d.Source
	if operation == "replicate" {
		objects = d.Destination
	}
	verification, _, err := objects.Open(objectCtx, source.ObjectKey)
	if err != nil {
		return err
	}
	hash := sha256.New()
	size, readErr := io.Copy(hash, io.LimitReader(verification, source.Size+1))
	closeErr := verification.Close()
	if readErr != nil {
		return readErr
	}
	if closeErr != nil {
		return closeErr
	}
	if size != source.Size || "sha256:"+hex.EncodeToString(hash.Sum(nil)) != source.Digest {
		return ErrDigestMismatch
	}
	reader, _, err := objects.Open(objectCtx, source.ObjectKey)
	if err != nil {
		return err
	}
	defer func() { _ = reader.Close() }()
	manager := NewManager(d.Store, d.Destination)
	parts := strings.Split(p.Coordinate, "/")
	session, _, err := manager.CreateSession(objectCtx, CreateSessionInput{RepositoryID: targetID, Suite: p.TargetSuite, Component: parts[1], Publisher: p.Actor, ObjectName: parts[4], DeclaredDigest: p.Digest, DeclaredSize: source.Size, IdempotencyKey: commandID})
	if err != nil {
		return err
	}
	// Even when the session is already staged, verify bytes before publication.
	// Publisher.loadPackages also parses every package from its object store.
	if _, err = manager.UploadPackageAs(objectCtx, session.ID, session.ObjectName, reader, source.Size, p.Actor); err != nil {
		return err
	}
	baseID := ""
	var scopes []aptIndexScope
	sessions := []string{}
	base, err := d.Store.GetVisibleAPTRepositorySnapshot(objectCtx, targetID, p.TargetSuite)
	if err == nil {
		baseID = base.ID
		assets, e := d.Store.ListAPTSnapshotAssets(objectCtx, base.ID)
		if e != nil {
			return e
		}
		scopes = snapshotIndexScopes(p.TargetSuite, assets)
		_, members, e := d.Store.GetAPTRepositorySnapshot(objectCtx, base.ID)
		if e != nil {
			return e
		}
		for _, m := range members {
			revision, e := d.Store.GetAPTPackageRevisionForSession(objectCtx, m.PublicationSessionID)
			if e != nil {
				return e
			}
			if repository.APTPoolPath(m.Component, revision.Package, revision.ObjectName) == p.Coordinate {
				if revision.Digest != p.Digest {
					return repository.ErrAPTPackageConflict
				}
				session.ID = m.PublicationSessionID
			}
			sessions = append(sessions, m.PublicationSessionID)
		}
	} else if !errors.Is(err, repository.ErrNotFound) {
		return err
	}
	found := false
	for _, id := range sessions {
		found = found || id == session.ID
	}
	if !found {
		sessions = append(sessions, session.ID)
	}
	history, err := d.Store.ListAPTRepositorySnapshots(objectCtx, targetID, p.TargetSuite)
	if err != nil {
		return err
	}
	var sequence int64
	for _, h := range history {
		if h.Snapshot.Sequence > sequence {
			sequence = h.Snapshot.Sequence
		}
	}
	if sequence == math.MaxInt64 {
		return repository.ErrVersionConflict
	}
	commit := repository.APTDistributionCommit{ID: commandID, RequestDigest: requestDigest, Operation: operation, WorkID: workID, LeaseToken: leaseToken, BaseSnapshotID: baseID, SessionID: session.ID, Source: source}
	_, err = d.Publisher.Publish(objectCtx, PublishSnapshotInput{ID: uuid.NewString(), RepositoryID: targetID, Suite: p.TargetSuite, Sequence: sequence + 1, SessionIDs: sessions, Actor: p.Actor, CreatedAt: time.Now().UTC(), distribution: &commit, indexScopes: scopes})
	if err != nil {
		return fmt.Errorf("publish target APT snapshot: %w", err)
	}
	return d.copyIntelligence(objectCtx, targetID, p)
}
func (d Distribution) copyIntelligence(ctx context.Context, targetID string, p PromotionPayload) error {
	if d.Intelligence == nil {
		return nil
	}
	return repository.CopyArtifactIntelligenceOrEnqueue(ctx, d.Intelligence, d.Store, targetID, p.SourceRepositoryID, repository.FormatAPT, p.Coordinate, p.Digest)
}
