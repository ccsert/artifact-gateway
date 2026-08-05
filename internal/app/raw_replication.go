package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/replication"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

// RawReplication turns verified generic checkpoints into immutable Raw asset
// references. Object bytes remain content-addressed and are never rewritten by
// the publication step.
type RawReplication struct {
	Store interface {
		repository.NativeRawStore
		repository.ReplicationStore
	}
	Source      OCIObjectStore
	Destination OCIObjectStore
	ChunkBytes  int64
	Metrics     repository.BackgroundOperationMetrics
}

func (r RawReplication) RunJobs(ctx context.Context, limit int) error {
	return (replication.Worker{Store: r.Store, Source: r.Source, Destination: r.Destination, ChunkBytes: r.ChunkBytes, Format: repository.FormatRaw, Publish: r.publish, Metrics: r.Metrics}).Run(ctx, limit)
}

func (r RawReplication) Start(ctx context.Context, interval time.Duration) {
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

func (r RawReplication) publish(ctx context.Context, plan repository.ReplicationPlan, checkpoints []repository.ReplicationCheckpoint) error {
	if plan.Format != repository.FormatRaw || len(checkpoints) != 1 {
		return fmt.Errorf("unsupported replication plan")
	}
	checkpoint := checkpoints[0]
	assets, err := r.sourceAssets(ctx, plan.SourceRepositoryID, checkpoint)
	if err != nil {
		return err
	}
	if len(assets) != 1 {
		return errors.New("source Raw asset is unavailable or ambiguous")
	}
	asset := assets[0]
	existing, err := r.Store.GetRawAsset(ctx, plan.TargetRepositoryID, asset.Path)
	if err == nil {
		if existing.Digest == asset.Digest && existing.ObjectKey == checkpoint.ObjectKey {
			return nil
		}
		return errors.New("target Raw path already exists")
	}
	if !errors.Is(err, repository.ErrNotFound) {
		return err
	}
	asset.RepositoryID = plan.TargetRepositoryID
	asset.ObjectKey = checkpoint.ObjectKey
	asset.Size = checkpoint.Size
	_, err = r.Store.PutRawAsset(ctx, asset)
	return err
}

func (r RawReplication) sourceAssets(ctx context.Context, repositoryID string, checkpoint repository.ReplicationCheckpoint) ([]repository.RawAsset, error) {
	assets := []repository.RawAsset{}
	after := ""
	for {
		page, err := r.Store.ListRawAssets(ctx, repositoryID, "", 100, after)
		if err != nil {
			return nil, err
		}
		for _, asset := range page {
			if asset.ObjectKey == checkpoint.ObjectKey && asset.Digest == checkpoint.Digest && asset.Size == checkpoint.Size {
				assets = append(assets, asset)
			}
		}
		if len(page) < 100 {
			return assets, nil
		}
		after = page[len(page)-1].Path
	}
}
