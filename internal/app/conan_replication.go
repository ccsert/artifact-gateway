package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sort"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/replication"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

// ConanReplication copies all assets in one visible recipe revision, including
// its visible package revisions, before publishing target-owned metadata.
type ConanReplication struct {
	Store interface {
		repository.NativeConanStore
		repository.ReplicationStore
	}
	Source      OCIObjectStore
	Destination OCIObjectStore
	ChunkBytes  int64
	Metrics     repository.BackgroundOperationMetrics
}

func (r ConanReplication) RunJobs(ctx context.Context, limit int) error {
	return (replication.Worker{Store: r.Store, Source: r.Source, Destination: r.Destination, ChunkBytes: r.ChunkBytes, Format: repository.FormatConan, Publish: r.publish, Metrics: r.Metrics}).Run(ctx, limit)
}

func (r ConanReplication) Start(ctx context.Context, interval time.Duration) {
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

func conanReplicationCheckpoints(ctx context.Context, store repository.NativeConanStore, sourceID, targetID, reference, revision string) ([]repository.ReplicationCheckpoint, error) {
	assets, err := conanReplicationAssets(ctx, store, sourceID, reference, revision)
	if err != nil {
		return nil, err
	}
	checks := make([]repository.ReplicationCheckpoint, 0, len(assets))
	seen := map[string]bool{}
	for _, asset := range assets {
		if seen[asset.ObjectKey] {
			continue
		}
		seen[asset.ObjectKey] = true
		checks = append(checks, repository.ReplicationCheckpoint{SourceObjectKey: asset.ObjectKey, ObjectKey: conanReplicationTargetObjectKey(targetID, asset.ObjectKey), Digest: asset.Digest, Size: asset.Size})
	}
	return checks, nil
}

func (r ConanReplication) publish(ctx context.Context, plan repository.ReplicationPlan, checks []repository.ReplicationCheckpoint) error {
	if plan.Format != repository.FormatConan || len(checks) == 0 {
		return errors.New("unsupported Conan replication plan")
	}
	assets, err := conanReplicationAssetsForChecks(ctx, r.Store, plan.SourceRepositoryID, checks)
	if err != nil {
		return err
	}
	recipe, err := r.Store.GetConanRecipeRevision(ctx, plan.SourceRepositoryID, assets[0].Reference, assets[0].RecipeRevision)
	if err != nil || recipe.State != "visible" {
		return errors.New("source Conan recipe revision is unavailable")
	}
	if existing, err := r.Store.GetConanRecipeRevision(ctx, plan.TargetRepositoryID, recipe.Reference, recipe.Revision); err == nil {
		return conanReplicationAlreadyPublished(ctx, r.Store, plan.TargetRepositoryID, existing, recipe.Digest, assets, checks)
	} else if !errors.Is(err, repository.ErrNotFound) {
		return err
	}
	recipeAssets := conanTargetAssets(assets, checks, plan.TargetRepositoryID, "", "")
	packages, err := conanVisiblePackages(ctx, r.Store, plan.SourceRepositoryID, recipe.Reference, recipe.Revision)
	if err != nil {
		return err
	}
	// Recheck after loading package metadata. A tombstone between byte copy and
	// publication must fail the plan rather than omit a copied package revision.
	if _, err = conanReplicationAssetsForChecks(ctx, r.Store, plan.SourceRepositoryID, checks); err != nil {
		return err
	}
	targetAssets := append([]repository.ConanAsset(nil), recipeAssets...)
	for _, pkg := range packages {
		packageAssets := conanTargetAssets(assets, checks, plan.TargetRepositoryID, pkg.PackageID, pkg.Revision)
		if len(packageAssets) == 0 {
			return errors.New("source Conan package assets are unavailable")
		}
		targetAssets = append(targetAssets, packageAssets...)
	}
	_, err = r.Store.PublishReplicatedConanRevision(ctx, repository.ConanReplicationPublication{
		SourceRepositoryID: plan.SourceRepositoryID,
		Recipe:             repository.ConanRecipeRevision{RepositoryID: plan.TargetRepositoryID, Reference: recipe.Reference, Revision: recipe.Revision, Digest: recipe.Digest},
		Packages:           packages,
		SourceAssets:       assets,
		TargetAssets:       targetAssets,
	})
	return err
}

func conanReplicationAssetsForChecks(ctx context.Context, store repository.NativeConanStore, sourceID string, checks []repository.ReplicationCheckpoint) ([]repository.ConanAsset, error) {
	bySource := map[string]repository.ReplicationCheckpoint{}
	for _, check := range checks {
		if check.SourceObjectKey == "" || bySource[check.SourceObjectKey].ObjectKey != "" {
			return nil, errors.New("invalid Conan replication checkpoints")
		}
		bySource[check.SourceObjectKey] = check
	}
	return conanReplicationMatchingAssets(ctx, store, sourceID, bySource)
}

func conanReplicationMatchingAssets(ctx context.Context, store repository.NativeConanStore, sourceID string, expected map[string]repository.ReplicationCheckpoint) ([]repository.ConanAsset, error) {
	refs, err := store.SearchConanReferences(ctx, sourceID, "", 10000, "")
	if err != nil {
		return nil, err
	}
	for _, reference := range refs {
		revisions, err := store.ListConanRecipeRevisions(ctx, sourceID, reference.Reference)
		if err != nil {
			return nil, err
		}
		for _, revision := range revisions {
			assets, err := conanReplicationAssets(ctx, store, sourceID, reference.Reference, revision.Revision)
			if err != nil {
				return nil, err
			}
			if len(assets) != len(expected) {
				continue
			}
			matched := true
			for _, asset := range assets {
				check, ok := expected[asset.ObjectKey]
				if !ok || check.Digest != asset.Digest || check.Size != asset.Size {
					matched = false
					break
				}
			}
			if matched {
				return assets, nil
			}
		}
	}
	return nil, errors.New("source Conan revision is unavailable or changed")
}

func conanReplicationAssets(ctx context.Context, store repository.NativeConanStore, sourceID, reference, revision string) ([]repository.ConanAsset, error) {
	if reference == "" || revision == "" {
		return nil, errors.New("Conan source revision is required")
	}
	recipe, err := store.GetConanRecipeRevision(ctx, sourceID, reference, revision)
	if err != nil || recipe.State != "visible" {
		return nil, errors.New("source Conan recipe revision is unavailable")
	}
	assets, err := store.ListConanRecipeAssets(ctx, sourceID, reference, revision)
	if err != nil {
		return nil, err
	}
	packages, err := conanVisiblePackages(ctx, store, sourceID, reference, revision)
	if err != nil {
		return nil, err
	}
	for _, pkg := range packages {
		packageAssets, err := store.ListConanPackageAssets(ctx, sourceID, reference, revision, pkg.PackageID, pkg.Revision)
		if err != nil {
			return nil, err
		}
		assets = append(assets, packageAssets...)
	}
	sort.Slice(assets, func(i, j int) bool { return assets[i].ObjectKey < assets[j].ObjectKey })
	return assets, nil
}

func conanVisiblePackages(ctx context.Context, store repository.NativeConanStore, sourceID, reference, revision string) ([]repository.ConanPackageRevision, error) {
	ids, err := store.ListConanPackageIDs(ctx, sourceID, reference, revision)
	if err != nil {
		return nil, err
	}
	var packages []repository.ConanPackageRevision
	for _, id := range ids {
		revisions, err := store.ListConanPackageRevisions(ctx, sourceID, reference, revision, id)
		if err != nil {
			return nil, err
		}
		packages = append(packages, revisions...)
	}
	return packages, nil
}

func conanTargetAssets(assets []repository.ConanAsset, checks []repository.ReplicationCheckpoint, targetID, packageID, packageRevision string) []repository.ConanAsset {
	targets := map[string]string{}
	for _, check := range checks {
		targets[check.SourceObjectKey] = check.ObjectKey
	}
	var out []repository.ConanAsset
	for _, asset := range assets {
		if asset.PackageID == packageID && asset.PackageRevision == packageRevision {
			asset.RepositoryID, asset.ObjectKey = targetID, targets[asset.ObjectKey]
			out = append(out, asset)
		}
	}
	return out
}

func conanReplicationAlreadyPublished(ctx context.Context, store repository.NativeConanStore, targetID string, recipe repository.ConanRecipeRevision, sourceDigest string, sourceAssets []repository.ConanAsset, checks []repository.ReplicationCheckpoint) error {
	if recipe.Digest != sourceDigest {
		return errors.New("target Conan recipe already exists")
	}
	assets, err := conanReplicationAssets(ctx, store, targetID, recipe.Reference, recipe.Revision)
	if err != nil || len(assets) != len(sourceAssets) {
		return errors.New("target Conan recipe already exists")
	}
	targets := map[string]string{}
	for _, check := range checks {
		targets[check.SourceObjectKey] = check.ObjectKey
	}
	for _, source := range sourceAssets {
		found := false
		for _, target := range assets {
			if target.Path == source.Path && target.PackageID == source.PackageID && target.PackageRevision == source.PackageRevision && target.ObjectKey == targets[source.ObjectKey] && target.Digest == source.Digest && target.Size == source.Size {
				found = true
				break
			}
		}
		if !found {
			return errors.New("target Conan recipe already exists")
		}
	}
	return nil
}

func conanReplicationTargetObjectKey(repositoryID, sourceObjectKey string) string {
	sum := sha256.Sum256([]byte(sourceObjectKey))
	return "native/conan/replication/" + repositoryID + "/" + hex.EncodeToString(sum[:])
}
