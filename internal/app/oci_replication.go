package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/replication"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

// OCIReplication copies a manifest into target-owned storage, then makes its
// metadata visible only while the source manifest and referenced blobs remain
// visible in the source repository.
type OCIReplication struct {
	Store interface {
		repository.NativeOCIStore
		repository.ReplicationStore
	}
	Source      OCIObjectStore
	Destination OCIObjectStore
	ChunkBytes  int64
	Metrics     repository.BackgroundOperationMetrics
}

func (r OCIReplication) RunJobs(ctx context.Context, limit int) error {
	return (replication.Worker{Store: r.Store, Source: r.Source, Destination: r.Destination, ChunkBytes: r.ChunkBytes, Format: repository.FormatOCI, Publish: r.publish, Metrics: r.Metrics}).Run(ctx, limit)
}

func (r OCIReplication) Start(ctx context.Context, interval time.Duration) {
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
				_ = r.RunJobs(ctx, 100)
			}
		}
	}()
}

func (r OCIReplication) publish(ctx context.Context, plan repository.ReplicationPlan, checkpoints []repository.ReplicationCheckpoint) error {
	if plan.Format != repository.FormatOCI || len(checkpoints) != 1 {
		return errors.New("unsupported OCI replication plan")
	}
	checkpoint := checkpoints[0]
	sourceKey := checkpoint.SourceObjectKey
	if sourceKey == "" {
		sourceKey = checkpoint.ObjectKey
	}

	// Recheck source metadata after copy: a tombstoned source must never become
	// newly visible in the target just because its bytes were copied earlier.
	source, err := r.sourceManifest(ctx, plan.SourceRepositoryID, sourceKey, checkpoint)
	if err != nil {
		return err
	}
	if target, err := r.Store.GetOCIManifest(ctx, plan.TargetRepositoryID, source.Name, source.Digest); err == nil {
		if target.ObjectKey == checkpoint.ObjectKey && target.Digest == checkpoint.Digest {
			return nil
		}
		return errors.New("target OCI manifest already exists")
	} else if !errors.Is(err, repository.ErrNotFound) {
		return err
	}

	body, err := r.Destination.Get(ctx, checkpoint.ObjectKey)
	if err != nil {
		return fmt.Errorf("read verified target OCI manifest: %w", err)
	}
	_, err = r.Store.PublishReplicatedOCIManifest(ctx, repository.OCIReplicationPublication{
		SourceRepositoryID: plan.SourceRepositoryID,
		TargetRepositoryID: plan.TargetRepositoryID,
		SourceObjectKey:    sourceKey,
		Manifest:           repository.OCIManifest{RepositoryID: plan.TargetRepositoryID, Name: source.Name, Digest: source.Digest, ObjectKey: checkpoint.ObjectKey, MediaType: source.MediaType, SubjectDigest: source.SubjectDigest, ArtifactType: source.ArtifactType, Size: checkpoint.Size},
		BlobDigests:        replicatedManifestBlobDigests(body),
	})
	return err
}

func (r OCIReplication) sourceManifest(ctx context.Context, repositoryID, sourceKey string, checkpoint repository.ReplicationCheckpoint) (repository.OCIManifest, error) {
	after := ""
	for {
		names, err := r.Store.ListOCIManifestNames(ctx, repositoryID, 100, after)
		if err != nil {
			return repository.OCIManifest{}, err
		}
		for _, name := range names {
			manifest, err := r.Store.GetOCIManifest(ctx, repositoryID, name, checkpoint.Digest)
			if err == nil && manifest.ObjectKey == sourceKey && manifest.Size == checkpoint.Size {
				return manifest, nil
			}
			if err != nil && !errors.Is(err, repository.ErrNotFound) {
				return repository.OCIManifest{}, err
			}
		}
		if len(names) < 100 {
			break
		}
		after = names[len(names)-1]
	}
	return repository.OCIManifest{}, errors.New("source OCI manifest is unavailable")
}

func ociReplicationTargetObjectKey(repositoryID, name, digest string) string {
	return "native/oci/manifests/" + repositoryID + "/" + url.PathEscape(name) + "/" + strings.TrimPrefix(digest, "sha256:")
}

func replicatedManifestBlobDigests(body []byte) []string {
	var manifest struct {
		Config struct {
			Digest string `json:"digest"`
		} `json:"config"`
		Layers []struct {
			Digest string `json:"digest"`
		} `json:"layers"`
		Manifests []struct {
			Digest string `json:"digest"`
		} `json:"manifests"`
	}
	if json.Unmarshal(body, &manifest) != nil {
		return nil
	}
	digests := make([]string, 0, 1+len(manifest.Layers)+len(manifest.Manifests))
	if manifest.Config.Digest != "" {
		digests = append(digests, manifest.Config.Digest)
	}
	for _, item := range manifest.Layers {
		if item.Digest != "" {
			digests = append(digests, item.Digest)
		}
	}
	for _, item := range manifest.Manifests {
		if item.Digest != "" {
			digests = append(digests, item.Digest)
		}
	}
	return digests
}
