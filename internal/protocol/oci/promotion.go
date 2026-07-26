package oci

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/objectstore"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

// NativePromotion copies immutable manifest bytes to a target-owned object and
// mounts referenced blobs. Keeping manifest keys target-owned prevents a source
// tombstone/reclaim cycle from deleting bytes still visible in the target.
type NativePromotion struct {
	Store interface {
		repository.NativeOCIStore
		repository.LifecycleJobStore
	}
	Objects objectstore.Store
}

type PromotionPayload struct {
	Format             repository.Format `json:"format"`
	SourceRepositoryID string            `json:"sourceRepositoryId"`
	Name               string            `json:"name"`
	Digest             string            `json:"digest"`
}

func (m NativePromotion) Enqueue(ctx context.Context, targetID, key string, payload PromotionPayload) (repository.LifecycleJob, bool, error) {
	payload.Format = repository.FormatOCI
	encoded, err := json.Marshal(payload)
	if err != nil {
		return repository.LifecycleJob{}, false, err
	}
	return m.Store.EnqueueLifecycleJob(ctx, repository.LifecycleJob{ID: uuid.NewString(), RepositoryID: targetID, Kind: repository.LifecycleJobPromotion, IdempotencyKey: key, Payload: encoded})
}

func (m NativePromotion) RunJobs(ctx context.Context, limit int) error {
	jobs, err := m.Store.ClaimLifecycleJobsByKindAndFormat(ctx, repository.LifecycleJobPromotion, repository.FormatOCI, limit)
	if err != nil {
		return err
	}
	for _, job := range jobs {
		if err := m.run(ctx, job); err != nil {
			return err
		}
	}
	return nil
}

func (m NativePromotion) Start(ctx context.Context, interval time.Duration) {
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
				_ = m.RunJobs(ctx, 100)
			}
		}
	}()
}

func (m NativePromotion) run(ctx context.Context, job repository.LifecycleJob) error {
	var p PromotionPayload
	if err := json.Unmarshal(job.Payload, &p); err != nil || p.Format != repository.FormatOCI || p.SourceRepositoryID == "" || p.Name == "" || !strings.HasPrefix(p.Digest, "sha256:") {
		return m.fail(ctx, job.ID, "invalid OCI promotion payload")
	}
	source, err := m.Store.GetOCIManifest(ctx, p.SourceRepositoryID, p.Name, p.Digest)
	if err != nil {
		return m.fail(ctx, job.ID, "source OCI manifest is unavailable")
	}
	body, err := m.Objects.Get(ctx, source.ObjectKey)
	if err != nil {
		return m.fail(ctx, job.ID, "source OCI manifest object is unavailable")
	}
	for _, digest := range manifestBlobDigests(body) {
		if _, err = m.Store.MountOCIBlobFrom(ctx, job.RepositoryID, p.SourceRepositoryID, digest); err != nil {
			return m.fail(ctx, job.ID, "source OCI blob is unavailable")
		}
	}
	key := "native/oci/manifests/" + job.RepositoryID + "/" + p.Name + "/" + strings.TrimPrefix(p.Digest, "sha256:")
	if err = m.Store.StageOCIObjectIntent(ctx, repository.OCIObjectIntent{RepositoryID: job.RepositoryID, ObjectKey: key, Digest: p.Digest, Size: int64(len(body))}); err != nil {
		return m.fail(ctx, job.ID, "stage target OCI manifest failed")
	}
	if err = m.Objects.PutVerifiedReader(ctx, key, bytes.NewReader(body), int64(len(body)), p.Digest); err != nil {
		return m.fail(ctx, job.ID, "persist target OCI manifest failed")
	}
	if _, err = m.Store.PutOCIManifest(ctx, repository.OCIManifest{RepositoryID: job.RepositoryID, Name: p.Name, Digest: p.Digest, ObjectKey: key, MediaType: source.MediaType, SubjectDigest: source.SubjectDigest, ArtifactType: source.ArtifactType, Size: int64(len(body))}, p.Digest); err != nil {
		return m.fail(ctx, job.ID, "publish target OCI manifest failed")
	}
	return m.Store.CompleteLifecycleJob(ctx, job.ID)
}

func (m NativePromotion) fail(ctx context.Context, id, message string) error {
	if err := m.Store.FailLifecycleJob(ctx, id, message); err != nil {
		return err
	}
	return fmt.Errorf("%s", message)
}

func manifestBlobDigests(body []byte) []string {
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
