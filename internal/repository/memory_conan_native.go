package repository

import (
	"context"
	"time"
)

func conanRecipeKey(repositoryID, reference, revision string) string {
	return repositoryID + "\x00" + reference + "\x00" + revision
}

func conanPackageKey(repositoryID, reference, recipeRevision, packageID, revision string) string {
	return repositoryID + "\x00" + reference + "\x00" + recipeRevision + "\x00" + packageID + "\x00" + revision
}

func conanAssetKey(asset ConanAsset) string {
	return conanPackageKey(asset.RepositoryID, asset.Reference, asset.RecipeRevision, asset.PackageID, asset.PackageRevision) + "\x00" + asset.Path
}

func (s *MemoryStore) StageConanObject(_ context.Context, object ConanObjectIntent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.conanObjects[object.ObjectKey]; ok {
		if existing.ClaimedAt.IsZero() {
			return nil
		}
	}
	object.CreatedAt = time.Now().UTC()
	s.conanObjects[object.ObjectKey] = object
	return nil
}

func (s *MemoryStore) PutConanRecipeRevision(_ context.Context, revision ConanRecipeRevision, assets []ConanAsset) (ConanRecipeRevision, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := conanRecipeKey(revision.RepositoryID, revision.Reference, revision.Revision)
	if existing, ok := s.conanRecipes[key]; ok {
		return existing, nil
	}
	for _, asset := range assets {
		object := s.conanObjects[asset.ObjectKey]
		if object.ObjectKey == "" || !object.ClaimedAt.IsZero() {
			return ConanRecipeRevision{}, ErrDisabled
		}
		object.ClaimedAt = time.Now().UTC()
		s.conanObjects[asset.ObjectKey] = object
		s.conanAssets[conanAssetKey(asset)] = asset
	}
	revision.State, revision.CreatedAt = "visible", time.Now().UTC()
	s.conanRecipes[key] = revision
	return revision, nil
}

func (s *MemoryStore) PutConanPackageRevision(_ context.Context, revision ConanPackageRevision, assets []ConanAsset) (ConanPackageRevision, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if recipe, ok := s.conanRecipes[conanRecipeKey(revision.RepositoryID, revision.Reference, revision.RecipeRevision)]; !ok || recipe.State != "visible" {
		return ConanPackageRevision{}, ErrNotFound
	}
	key := conanPackageKey(revision.RepositoryID, revision.Reference, revision.RecipeRevision, revision.PackageID, revision.Revision)
	if existing, ok := s.conanPackages[key]; ok {
		return existing, nil
	}
	for _, asset := range assets {
		object := s.conanObjects[asset.ObjectKey]
		if object.ObjectKey == "" || !object.ClaimedAt.IsZero() {
			return ConanPackageRevision{}, ErrDisabled
		}
		object.ClaimedAt = time.Now().UTC()
		s.conanObjects[asset.ObjectKey] = object
		s.conanAssets[conanAssetKey(asset)] = asset
	}
	revision.State, revision.CreatedAt = "visible", time.Now().UTC()
	s.conanPackages[key] = revision
	return revision, nil
}

func (s *MemoryStore) GetConanRecipeRevision(_ context.Context, repositoryID, reference, revision string) (ConanRecipeRevision, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	item, ok := s.conanRecipes[conanRecipeKey(repositoryID, reference, revision)]
	if !ok {
		return ConanRecipeRevision{}, ErrNotFound
	}
	return item, nil
}

func (s *MemoryStore) GetConanPackageRevision(_ context.Context, repositoryID, reference, recipeRevision, packageID, revision string) (ConanPackageRevision, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	item, ok := s.conanPackages[conanPackageKey(repositoryID, reference, recipeRevision, packageID, revision)]
	if !ok {
		return ConanPackageRevision{}, ErrNotFound
	}
	return item, nil
}

func (s *MemoryStore) TombstoneConanRecipeRevision(_ context.Context, repositoryID, reference, revision string) (ConanRecipeRevision, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := conanRecipeKey(repositoryID, reference, revision)
	item, ok := s.conanRecipes[key]
	if !ok {
		return ConanRecipeRevision{}, ErrNotFound
	}
	item.State = "deleted"
	s.conanRecipes[key] = item
	s.artifactTombstones[repositoryID+"\x00"+string(FormatConan)+"\x00"+reference+"#"+revision] = ArtifactTombstone{RepositoryID: repositoryID, Format: FormatConan, Coordinate: reference + "#" + revision, Digest: item.Digest, TombstonedAt: time.Now().UTC()}
	return item, nil
}

func (s *MemoryStore) TombstoneConanPackageRevision(_ context.Context, repositoryID, reference, recipeRevision, packageID, revision string) (ConanPackageRevision, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := conanPackageKey(repositoryID, reference, recipeRevision, packageID, revision)
	item, ok := s.conanPackages[key]
	if !ok {
		return ConanPackageRevision{}, ErrNotFound
	}
	item.State = "deleted"
	s.conanPackages[key] = item
	coordinate := reference + "#" + recipeRevision + "/" + packageID + "#" + revision
	s.artifactTombstones[repositoryID+"\x00"+string(FormatConan)+"\x00"+coordinate] = ArtifactTombstone{RepositoryID: repositoryID, Format: FormatConan, Coordinate: coordinate, Digest: item.Digest, TombstonedAt: time.Now().UTC()}
	return item, nil
}
