package app

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/replication"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

type NPMReplication struct {
	Store interface {
		repository.NativeNPMStore
		repository.ReplicationStore
	}
	Source      OCIObjectStore
	Destination OCIObjectStore
	ChunkBytes  int64
	Metrics     repository.BackgroundOperationMetrics
}

func (r NPMReplication) RunJobs(ctx context.Context, limit int) error {
	return (replication.Worker{Store: r.Store, Source: r.Source, Destination: r.Destination, ChunkBytes: r.ChunkBytes, Format: repository.FormatNPM, Publish: r.publish, LockObject: r.Store.LockNPMObject, Metrics: r.Metrics}).Run(ctx, limit)
}

func (r NPMReplication) Start(ctx context.Context, interval time.Duration) {
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

func (r NPMReplication) publish(ctx context.Context, plan repository.ReplicationPlan, checkpoints []repository.ReplicationCheckpoint) error {
	if plan.Format != repository.FormatNPM || len(checkpoints) != 1 {
		return errors.New("unsupported npm replication plan")
	}
	checkpoint := checkpoints[0]
	source, err := r.sourceVersion(ctx, plan.SourceRepositoryID, checkpoint)
	if err != nil {
		return err
	}
	if existing, lookupErr := r.Store.GetNPMVersion(ctx, plan.TargetRepositoryID, source.PackageName, source.Version); lookupErr == nil {
		if existing.Digest == source.Digest && existing.ObjectKey == checkpoint.ObjectKey {
			return nil
		}
		return errors.New("target npm version already exists")
	} else if !errors.Is(lookupErr, repository.ErrNotFound) {
		return lookupErr
	}
	pkg, err := r.Store.GetNPMPackage(ctx, plan.SourceRepositoryID, source.PackageName)
	if err != nil {
		return err
	}
	tags := make(map[string]string)
	for tag, version := range pkg.DistTags {
		if version == source.Version {
			tags[tag] = version
		}
	}
	source.RepositoryID = plan.TargetRepositoryID
	source.ObjectKey = checkpoint.ObjectKey
	source.Size = checkpoint.Size
	_, err = r.Store.PublishNPMVersion(ctx, source, tags)
	return err
}

func (r NPMReplication) sourceVersion(ctx context.Context, repositoryID string, checkpoint repository.ReplicationCheckpoint) (repository.NPMVersion, error) {
	after := ""
	for {
		packages, err := r.Store.SearchNPMPackages(ctx, repositoryID, "", 200, after)
		if err != nil {
			return repository.NPMVersion{}, err
		}
		for _, pkg := range packages {
			versions, err := r.Store.ListNPMVersions(ctx, repositoryID, pkg.Name)
			if err != nil {
				return repository.NPMVersion{}, err
			}
			for _, version := range versions {
				if version.ObjectKey == checkpoint.SourceObjectKey && version.Digest == checkpoint.Digest && version.Size == checkpoint.Size {
					return version, nil
				}
			}
		}
		if len(packages) < 200 {
			return repository.NPMVersion{}, errors.New("source npm version is unavailable or changed")
		}
		after = packages[len(packages)-1].Name
	}
}

func npmReplicationTargetObjectKey(repositoryID, digest string) string {
	return "native/npm/replication/" + repositoryID + "/" + strings.TrimPrefix(digest, "sha256:")
}
