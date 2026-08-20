package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/replication"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

type NativeGoPromotion struct {
	Store interface {
		repository.NativeGoStore
		repository.LifecycleJobStore
	}
	Intelligence           repository.ArtifactIntelligenceStore
	Metrics                repository.BackgroundOperationMetrics
	LeaseHeartbeatInterval time.Duration
}

type GoPromotionPayload struct {
	Format             repository.Format `json:"format"`
	SourceRepositoryID string            `json:"sourceRepositoryId"`
	Module             string            `json:"module"`
	Version            string            `json:"version"`
	Digest             string            `json:"digest"`
}

func (m NativeGoPromotion) Enqueue(ctx context.Context, targetID, key string, payload GoPromotionPayload) (repository.LifecycleJob, bool, error) {
	payload.Format = repository.FormatGo
	body, err := json.Marshal(payload)
	if err != nil {
		return repository.LifecycleJob{}, false, err
	}
	return m.Store.EnqueueLifecycleJob(ctx, repository.LifecycleJob{
		ID: uuid.NewString(), RepositoryID: targetID, Kind: repository.LifecycleJobPromotion, IdempotencyKey: key, Payload: body,
	})
}

func (m NativeGoPromotion) RunJobs(ctx context.Context, limit int) error {
	jobs, err := m.Store.ClaimLifecycleJobsByKindAndFormat(ctx, repository.LifecycleJobPromotion, repository.FormatGo, limit)
	if err != nil {
		return err
	}
	var firstErr error
	for _, job := range jobs {
		if err = m.run(ctx, job); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (m NativeGoPromotion) run(ctx context.Context, job repository.LifecycleJob) error {
	var payload GoPromotionPayload
	coordinate := ""
	if json.Unmarshal(job.Payload, &payload) == nil {
		coordinate = payload.Module + "@" + payload.Version
	}
	modulePath, version, valid := parseGoModuleVersionCoordinate(coordinate)
	if payload.Format != repository.FormatGo || payload.SourceRepositoryID == "" || !valid || modulePath != payload.Module || version != payload.Version || !validRepositoryDigest(payload.Digest) {
		return m.fail(ctx, job, "invalid Go promotion payload")
	}
	operationCtx := ctx
	workCtx, heartbeat, heartbeatErr := startLifecycleJobHeartbeat(ctx, m.Store, job.ID, job.LeaseToken, m.LeaseHeartbeatInterval)
	if heartbeatErr != nil {
		return heartbeatErr
	}
	defer func() { _ = heartbeat.stop() }()
	ctx = workCtx
	fail := func(message string) error {
		if leaseErr := heartbeat.stop(); leaseErr != nil {
			return leaseErr
		}
		return m.fail(operationCtx, job, message)
	}
	for attempt := 0; attempt < 3; attempt++ {
		snapshot, err := loadGoDistributionPublication(ctx, m.Store, payload.SourceRepositoryID, modulePath, version, payload.Digest)
		if err != nil {
			return fail("source Go module version is unavailable")
		}
		objectKeys := goPublicationObjectKeys(snapshot)
		objectCtx, releaseObjects, err := repository.LockObjectKeys(ctx, objectKeys, m.Store, repository.FormatGo, m.Store.LockGoObject)
		if err != nil {
			return fail("Go promotion object coordination failed")
		}
		admissionCtx, releaseAdmission, err := repository.LockArtifactDistributionCoordinates(objectCtx, m.Store, []repository.ArtifactDistributionCoordinate{
			{RepositoryID: payload.SourceRepositoryID, Format: repository.FormatGo, Coordinate: coordinate},
			{RepositoryID: job.RepositoryID, Format: repository.FormatGo, Coordinate: coordinate},
			{RepositoryID: job.RepositoryID, Format: repository.FormatGo, Coordinate: "__hosted_capacity__"},
		})
		if err != nil {
			releaseObjects()
			return fail("Go promotion admission coordination failed")
		}
		selected, loadErr := loadGoDistributionPublication(admissionCtx, m.Store, payload.SourceRepositoryID, modulePath, version, payload.Digest)
		if loadErr != nil {
			releaseAdmission()
			releaseObjects()
			return fail("source Go module version is unavailable")
		}
		if !sameGoDistributionPublication(snapshot, selected) {
			releaseAdmission()
			releaseObjects()
			continue
		}
		allowed, admissionErr := repository.ArtifactDistributionAllowedForDigests(admissionCtx, m.Store, payload.SourceRepositoryID, repository.FormatGo, coordinate, goPublicationDigests(selected))
		if admissionErr != nil {
			releaseAdmission()
			releaseObjects()
			return fail("evaluate Go artifact quarantine failed")
		}
		if !allowed {
			releaseAdmission()
			releaseObjects()
			return fail(repository.ArtifactQuarantinedReason)
		}
		if renewErr := m.Store.RenewLifecycleJobLease(admissionCtx, job.ID, job.LeaseToken); renewErr != nil {
			releaseAdmission()
			releaseObjects()
			return renewErr
		}
		releaseLease, leaseErr := m.Store.LockLifecycleJobLease(admissionCtx, job.ID, job.LeaseToken)
		if leaseErr != nil {
			releaseAdmission()
			releaseObjects()
			return leaseErr
		}
		target := goTargetPublication(selected, job.RepositoryID, nil)
		publishErr := publishGoDistributionStrict(admissionCtx, m.Store, target)
		if publishErr != nil {
			releaseLease()
			releaseAdmission()
			releaseObjects()
			return fail("publish target Go module version failed")
		}
		intelligenceErr := repository.CopyArtifactIntelligenceOrEnqueue(ctx, m.Intelligence, m.Store, job.RepositoryID, payload.SourceRepositoryID, repository.FormatGo, coordinate, payload.Digest)
		if intelligenceErr != nil && !errors.Is(intelligenceErr, repository.ErrArtifactIntelligenceDeferred) {
			releaseLease()
			releaseAdmission()
			releaseObjects()
			return fail(fmt.Sprintf("copy Go artifact intelligence failed: %v", intelligenceErr))
		}
		if errors.Is(intelligenceErr, repository.ErrArtifactIntelligenceDeferred) && m.Metrics != nil {
			m.Metrics.RecordBackgroundOperation("intelligence-copy", repository.FormatGo, "deferred")
		}
		heartbeatErr := heartbeat.stop()
		completeErr := m.Store.CompleteLifecycleJob(operationCtx, job.ID, job.LeaseToken)
		releaseLease()
		releaseAdmission()
		releaseObjects()
		if completeErr == nil {
			return nil
		}
		if heartbeatErr != nil {
			return heartbeatErr
		}
		return completeErr
	}
	return fail("source Go module version changed while coordinating promotion")
}

type lifecycleJobHeartbeat struct {
	cancel context.CancelFunc
	done   chan error
}

func startLifecycleJobHeartbeat(ctx context.Context, store repository.LifecycleJobStore, jobID, leaseToken string, interval time.Duration) (context.Context, *lifecycleJobHeartbeat, error) {
	if err := store.RenewLifecycleJobLease(ctx, jobID, leaseToken); err != nil {
		return ctx, nil, err
	}
	if interval <= 0 {
		interval = time.Minute
	}
	workCtx, cancel := context.WithCancel(ctx)
	heartbeat := &lifecycleJobHeartbeat{cancel: cancel, done: make(chan error, 1)}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-workCtx.Done():
				heartbeat.done <- nil
				return
			case <-ticker.C:
				if err := store.RenewLifecycleJobLease(workCtx, jobID, leaseToken); err != nil {
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

func (h *lifecycleJobHeartbeat) stop() error {
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

func (m NativeGoPromotion) fail(ctx context.Context, job repository.LifecycleJob, message string) error {
	_ = m.Store.FailLifecycleJob(ctx, job.ID, job.LeaseToken, message)
	return errors.New(message)
}

func (m NativeGoPromotion) Start(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		return
	}
	go func() {
		_ = m.RunJobs(ctx, 100)
		wake := notificationWake(ctx, m.Store, "artifact_gateway_lifecycle_jobs")
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_ = m.RunJobs(ctx, 100)
			case <-wake:
				_ = m.RunJobs(ctx, 100)
			}
		}
	}()
}

type GoReplication struct {
	Store interface {
		repository.NativeGoStore
		repository.ReplicationStore
	}
	Source      OCIObjectStore
	Destination OCIObjectStore
	ChunkBytes  int64
	Metrics     repository.BackgroundOperationMetrics
}

func (r GoReplication) RunJobs(ctx context.Context, limit int) error {
	return (replication.Worker{
		Store: r.Store, Source: r.Source, Destination: r.Destination, ChunkBytes: r.ChunkBytes,
		Format: repository.FormatGo, Publish: r.publish, LockObject: r.Store.LockGoObject,
		AdmissionSnapshot: r.admissionSnapshot, Metrics: r.Metrics,
	}).Run(ctx, limit)
}

func (r GoReplication) Start(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		return
	}
	go func() {
		_ = r.RunJobs(ctx, 100)
		wake := notificationWake(ctx, r.Store, "artifact_gateway_replication_plans")
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_ = r.RunJobs(ctx, 100)
			case <-wake:
				_ = r.RunJobs(ctx, 100)
			}
		}
	}()
}

func (r GoReplication) admissionSnapshot(ctx context.Context, plan repository.ReplicationPlan, checkpoints []repository.ReplicationCheckpoint) ([]string, bool, error) {
	modulePath, version, valid := parseGoModuleVersionCoordinate(plan.Coordinate)
	if !valid {
		return nil, false, errors.New("invalid Go replication coordinate")
	}
	publication, err := loadGoDistributionPublication(ctx, r.Store, plan.SourceRepositoryID, modulePath, version, plan.Digest)
	if err != nil {
		return nil, false, err
	}
	return goPublicationDigests(publication), goReplicationSnapshotMatches(publication, checkpoints, plan.TargetRepositoryID), nil
}

func (r GoReplication) publish(ctx context.Context, plan repository.ReplicationPlan, checkpoints []repository.ReplicationCheckpoint) error {
	if plan.Format != repository.FormatGo || len(checkpoints) != 3 {
		return errors.New("unsupported Go replication plan")
	}
	modulePath, version, valid := parseGoModuleVersionCoordinate(plan.Coordinate)
	if !valid {
		return errors.New("invalid Go replication coordinate")
	}
	source, err := loadGoDistributionPublication(ctx, r.Store, plan.SourceRepositoryID, modulePath, version, plan.Digest)
	if err != nil {
		return err
	}
	if !goReplicationSnapshotMatches(source, checkpoints, plan.TargetRepositoryID) {
		return errors.New("source Go replication snapshot changed")
	}
	targetKeys := make(map[string]string, 3)
	for _, sourceAsset := range source.Assets {
		for _, checkpoint := range checkpoints {
			if checkpoint.SourceObjectKey == sourceAsset.ObjectKey && checkpoint.Digest == sourceAsset.Digest && checkpoint.Size == sourceAsset.Size && strings.HasSuffix(checkpoint.ObjectKey, "/"+sourceAsset.Kind) {
				targetKeys[sourceAsset.Kind] = checkpoint.ObjectKey
				break
			}
		}
	}
	return publishGoDistributionStrict(ctx, r.Store, goTargetPublication(source, plan.TargetRepositoryID, targetKeys))
}

func loadGoDistributionPublication(ctx context.Context, store repository.NativeGoStore, repositoryID, modulePath, version, zipDigest string) (repository.GoModulePublication, error) {
	publication, err := loadGoPublication(ctx, store, repositoryID, modulePath, version)
	if err != nil {
		return repository.GoModulePublication{}, err
	}
	for _, asset := range publication.Assets {
		if asset.Kind == "zip" && asset.Digest == zipDigest {
			return publication, nil
		}
	}
	return repository.GoModulePublication{}, repository.ErrNotFound
}

func loadGoPublication(ctx context.Context, store repository.NativeGoStore, repositoryID, modulePath, version string) (repository.GoModulePublication, error) {
	item, err := store.GetGoModuleVersion(ctx, repositoryID, modulePath, version)
	if err != nil {
		return repository.GoModulePublication{}, err
	}
	assets := make([]repository.GoModuleAsset, 0, 3)
	for _, kind := range []string{"info", "mod", "zip"} {
		asset, assetErr := store.GetGoModuleAsset(ctx, repositoryID, modulePath, version, kind)
		if errors.Is(assetErr, repository.ErrNotFound) || asset.ObjectKey == "" {
			return repository.GoModulePublication{}, repository.ErrNotFound
		}
		if assetErr != nil {
			return repository.GoModulePublication{}, assetErr
		}
		assets = append(assets, asset)
	}
	return repository.GoModulePublication{Version: item, Assets: assets}, nil
}

// publishGoDistributionStrict applies distribution replay semantics to a
// complete immutable Go snapshot. Protocol publication deliberately tolerates
// a regenerated .info timestamp for client retries; promotion and replication
// must instead preserve all three selected representations exactly.
func publishGoDistributionStrict(ctx context.Context, store repository.NativeGoStore, incoming repository.GoModulePublication) error {
	existing, err := loadGoPublication(ctx, store, incoming.Version.RepositoryID, incoming.Version.Module, incoming.Version.Version)
	if err == nil {
		if sameGoDistributionPublication(existing, incoming) {
			return nil
		}
		return repository.ErrNameExists
	}
	if !errors.Is(err, repository.ErrNotFound) {
		return err
	}
	_, _, err = store.PublishGoModule(ctx, incoming)
	return err
}

func goTargetPublication(source repository.GoModulePublication, targetRepositoryID string, targetKeys map[string]string) repository.GoModulePublication {
	target := source
	target.Version.RepositoryID = targetRepositoryID
	target.Assets = append([]repository.GoModuleAsset(nil), source.Assets...)
	for index := range target.Assets {
		target.Assets[index].RepositoryID = targetRepositoryID
		target.Assets[index].SourceURL = ""
		if targetKeys != nil {
			target.Assets[index].ObjectKey = targetKeys[target.Assets[index].Kind]
		}
	}
	return target
}

func sameGoDistributionPublication(left, right repository.GoModulePublication) bool {
	if left.Version.RepositoryID != right.Version.RepositoryID || left.Version.Module != right.Version.Module || left.Version.Version != right.Version.Version || len(left.Assets) != len(right.Assets) {
		return false
	}
	for index := range left.Assets {
		if left.Assets[index].Kind != right.Assets[index].Kind || left.Assets[index].Digest != right.Assets[index].Digest || left.Assets[index].ObjectKey != right.Assets[index].ObjectKey || left.Assets[index].Size != right.Assets[index].Size {
			return false
		}
	}
	return true
}

func goPublicationObjectKeys(publication repository.GoModulePublication) []string {
	keys := make([]string, 0, len(publication.Assets))
	for _, asset := range publication.Assets {
		keys = append(keys, asset.ObjectKey)
	}
	return keys
}

func goPublicationDigests(publication repository.GoModulePublication) []string {
	digests := make([]string, 0, len(publication.Assets))
	for _, asset := range publication.Assets {
		digests = append(digests, asset.Digest)
	}
	return digests
}

func goReplicationSnapshotMatches(publication repository.GoModulePublication, checkpoints []repository.ReplicationCheckpoint, targetRepositoryID string) bool {
	if len(publication.Assets) != 3 || len(checkpoints) != 3 {
		return false
	}
	matched := make([]bool, len(checkpoints))
	for _, asset := range publication.Assets {
		found := false
		for index, checkpoint := range checkpoints {
			if !matched[index] && checkpoint.SourceObjectKey == asset.ObjectKey && checkpoint.ObjectKey == goReplicationTargetObjectKey(targetRepositoryID, asset.Digest, asset.Kind) && checkpoint.Digest == asset.Digest && checkpoint.Size == asset.Size {
				matched[index] = true
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func goReplicationTargetObjectKey(repositoryID, digest, kind string) string {
	return "native/go/replication/" + repositoryID + "/" + strings.TrimPrefix(digest, "sha256:") + "/" + kind
}
