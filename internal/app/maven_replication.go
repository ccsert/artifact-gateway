package app

import (
	"context"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/replication"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

// MavenReplication publishes target-owned assets only after every source asset
// checkpoint is verified. The repository transaction rechecks source visibility
// before it creates the target artifact.
type MavenReplication struct {
	Store interface {
		repository.NativeMavenStore
		repository.ReplicationStore
	}
	Source      OCIObjectStore
	Destination OCIObjectStore
	ChunkBytes  int64
	Metrics     repository.BackgroundOperationMetrics
}

func (r MavenReplication) RunJobs(ctx context.Context, limit int) error {
	return (replication.Worker{Store: r.Store, Source: r.Source, Destination: r.Destination, ChunkBytes: r.ChunkBytes, Format: repository.FormatMaven, Publish: r.publish, Metrics: r.Metrics}).Run(ctx, limit)
}

func (r MavenReplication) Start(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		return
	}
	go func() {
		_ = r.RunJobs(ctx, 100)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_ = r.RunJobs(ctx, 100)
			}
		}
	}()
}

func (r MavenReplication) publish(ctx context.Context, plan repository.ReplicationPlan, checkpoints []repository.ReplicationCheckpoint) error {
	if plan.Format != repository.FormatMaven || len(checkpoints) == 0 {
		return errors.New("unsupported Maven replication plan")
	}
	artifacts, err := r.Store.ListMavenArtifacts(ctx, plan.SourceRepositoryID)
	if err != nil {
		return err
	}
	for _, artifact := range artifacts {
		assets, err := r.Store.ListMavenAssets(ctx, plan.SourceRepositoryID, artifact.Coordinate)
		if err != nil {
			return err
		}
		copied, ok := mavenReplicationAssets(assets, checkpoints)
		if !ok {
			continue
		}
		_, err = r.Store.PublishReplicatedMavenArtifact(ctx, repository.MavenReplication{ID: plan.ID, SourceRepositoryID: plan.SourceRepositoryID, TargetRepositoryID: plan.TargetRepositoryID, Coordinate: artifact.Coordinate, Digest: artifact.Digest, Assets: copied})
		return err
	}
	return errors.New("source Maven artifact is unavailable or does not match checkpoints")
}

func mavenReplicationAssets(assets []repository.MavenAsset, checkpoints []repository.ReplicationCheckpoint) ([]repository.MavenReplicationAsset, bool) {
	byDigest := make(map[string]repository.ReplicationCheckpoint, len(checkpoints))
	for _, checkpoint := range checkpoints {
		key := checkpoint.Digest + "\x00" + strconv.FormatInt(checkpoint.Size, 10)
		if byDigest[key].ObjectKey != "" {
			return nil, false
		}
		byDigest[key] = checkpoint
	}
	copied := make([]repository.MavenReplicationAsset, 0, len(assets))
	usedDigests := make(map[string]bool, len(checkpoints))
	for _, asset := range assets {
		key := asset.Digest + "\x00" + strconv.FormatInt(asset.Size, 10)
		checkpoint, ok := byDigest[key]
		if !ok {
			return nil, false
		}
		usedDigests[key] = true
		copied = append(copied, repository.MavenReplicationAsset{Path: asset.Path, SourceObjectKey: asset.ObjectKey, ObjectKey: checkpoint.ObjectKey, Digest: asset.Digest, Size: asset.Size})
	}
	if len(usedDigests) != len(checkpoints) {
		return nil, false
	}
	sort.Slice(copied, func(i, j int) bool { return copied[i].Path < copied[j].Path })
	return copied, true
}

func mavenReplicationTargetObjectKey(repositoryID, digest string) string {
	return "native/maven/replication/" + repositoryID + "/" + strings.TrimPrefix(digest, "sha256:")
}
