package repository

import (
	"context"
	"sort"
	"strings"
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

func (s *MemoryStore) ListReclaimableConanObjects(_ context.Context, before time.Time, limit int) ([]ConanObjectIntent, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []ConanObjectIntent{}
	for _, object := range s.conanObjects {
		if len(out) == limit || !object.CollectedAt.IsZero() || s.conanObjectVisibleLocked(object.ObjectKey) {
			continue
		}
		eligible := false
		for _, asset := range s.conanAssets {
			if asset.ObjectKey != object.ObjectKey {
				continue
			}
			if tombstone, ok := s.artifactTombstones[asset.RepositoryID+"\x00"+string(FormatConan)+"\x00"+asset.Reference+"#"+asset.RecipeRevision]; ok && !tombstone.TombstonedAt.After(before) {
				eligible = true
			}
		}
		if eligible {
			out = append(out, object)
		}
	}
	return out, nil
}

func (s *MemoryStore) ConanObjectHasVisibleReference(_ context.Context, key string) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.conanObjectVisibleLocked(key), nil
}
func (s *MemoryStore) conanObjectVisibleLocked(key string) bool {
	for _, asset := range s.conanAssets {
		if asset.ObjectKey != key {
			continue
		}
		recipe, ok := s.conanRecipes[conanRecipeKey(asset.RepositoryID, asset.Reference, asset.RecipeRevision)]
		if ok && recipe.State == "visible" {
			if asset.PackageID == "" {
				return true
			}
			if pkg, ok := s.conanPackages[conanPackageKey(asset.RepositoryID, asset.Reference, asset.RecipeRevision, asset.PackageID, asset.PackageRevision)]; ok && pkg.State == "visible" {
				return true
			}
		}
	}
	return false
}
func (s *MemoryStore) MarkConanObjectCollected(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	object, ok := s.conanObjects[key]
	if !ok {
		return ErrNotFound
	}
	object.CollectedAt = time.Now().UTC()
	s.conanObjects[key] = object
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

func (s *MemoryStore) PromoteConanRecipeRevision(_ context.Context, promotion ConanPromotion) (ConanRecipeRevision, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	source, ok := s.conanRecipes[conanRecipeKey(promotion.SourceRepositoryID, promotion.Reference, promotion.Revision)]
	if !ok || source.State != "visible" || source.Digest != promotion.Digest {
		return ConanRecipeRevision{}, ErrNotFound
	}
	targetKey := conanRecipeKey(promotion.TargetRepositoryID, promotion.Reference, promotion.Revision)
	if _, exists := s.conanRecipes[targetKey]; exists {
		return ConanRecipeRevision{}, ErrNameExists
	}
	for _, asset := range s.conanAssets {
		if asset.RepositoryID != promotion.SourceRepositoryID || asset.Reference != promotion.Reference || asset.RecipeRevision != promotion.Revision {
			continue
		}
		asset.RepositoryID = promotion.TargetRepositoryID
		s.conanAssets[conanAssetKey(asset)] = asset
	}
	for _, pkg := range s.conanPackages {
		if pkg.RepositoryID == promotion.SourceRepositoryID && pkg.Reference == promotion.Reference && pkg.RecipeRevision == promotion.Revision && pkg.State == "visible" {
			pkg.RepositoryID = promotion.TargetRepositoryID
			s.conanPackages[conanPackageKey(pkg.RepositoryID, pkg.Reference, pkg.RecipeRevision, pkg.PackageID, pkg.Revision)] = pkg
		}
	}
	source.RepositoryID, source.CreatedAt = promotion.TargetRepositoryID, time.Now().UTC()
	s.conanRecipes[targetKey] = source
	return source, nil
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

func (s *MemoryStore) SearchConanReferences(_ context.Context, repositoryID, prefix string, limit int, after string) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	seen := make(map[string]struct{})
	for _, item := range s.conanRecipes {
		if item.RepositoryID == repositoryID && item.State == "visible" && strings.HasPrefix(item.Reference, prefix) && item.Reference > after {
			seen[item.Reference] = struct{}{}
		}
	}
	refs := make([]string, 0, len(seen))
	for reference := range seen {
		refs = append(refs, reference)
	}
	sort.Strings(refs)
	if len(refs) > limit {
		refs = refs[:limit]
	}
	return refs, nil
}

func (s *MemoryStore) ListConanRecipeRevisions(_ context.Context, repositoryID, reference string) ([]ConanRecipeRevision, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []ConanRecipeRevision
	for _, item := range s.conanRecipes {
		if item.RepositoryID == repositoryID && item.Reference == reference && item.State == "visible" {
			out = append(out, item)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

func (s *MemoryStore) ListConanPackageRevisions(_ context.Context, repositoryID, reference, recipeRevision, packageID string) ([]ConanPackageRevision, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []ConanPackageRevision
	for _, item := range s.conanPackages {
		if item.RepositoryID == repositoryID && item.Reference == reference && item.RecipeRevision == recipeRevision && item.PackageID == packageID && item.State == "visible" {
			out = append(out, item)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}
func (s *MemoryStore) ListConanPackageIDs(_ context.Context, repositoryID, reference, recipeRevision string) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	seen := map[string]bool{}
	for _, item := range s.conanPackages {
		if item.RepositoryID == repositoryID && item.Reference == reference && item.RecipeRevision == recipeRevision && item.State == "visible" {
			seen[item.PackageID] = true
		}
	}
	out := make([]string, 0, len(seen))
	for id := range seen {
		out = append(out, id)
	}
	sort.Strings(out)
	return out, nil
}

func (s *MemoryStore) ListConanRecipeAssets(_ context.Context, repositoryID, reference, revision string) ([]ConanAsset, error) {
	return s.listConanAssets(repositoryID, reference, revision, "", "")
}

func (s *MemoryStore) ListConanPackageAssets(_ context.Context, repositoryID, reference, recipeRevision, packageID, packageRevision string) ([]ConanAsset, error) {
	return s.listConanAssets(repositoryID, reference, recipeRevision, packageID, packageRevision)
}

func (s *MemoryStore) listConanAssets(repositoryID, reference, recipeRevision, packageID, packageRevision string) ([]ConanAsset, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []ConanAsset
	for _, asset := range s.conanAssets {
		if asset.RepositoryID == repositoryID && asset.Reference == reference && asset.RecipeRevision == recipeRevision && asset.PackageID == packageID && asset.PackageRevision == packageRevision {
			out = append(out, asset)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

func (s *MemoryStore) GetConanRecipeAsset(_ context.Context, repositoryID, reference, revision, path string) (ConanAsset, error) {
	return s.getConanAsset(repositoryID, reference, revision, "", "", path)
}

func (s *MemoryStore) GetConanPackageAsset(_ context.Context, repositoryID, reference, recipeRevision, packageID, packageRevision, path string) (ConanAsset, error) {
	return s.getConanAsset(repositoryID, reference, recipeRevision, packageID, packageRevision, path)
}

func (s *MemoryStore) getConanAsset(repositoryID, reference, recipeRevision, packageID, packageRevision, path string) (ConanAsset, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	asset, ok := s.conanAssets[conanPackageKey(repositoryID, reference, recipeRevision, packageID, packageRevision)+"\x00"+path]
	if !ok {
		return ConanAsset{}, ErrNotFound
	}
	return asset, nil
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
	for packageKey, pkg := range s.conanPackages {
		if pkg.RepositoryID == repositoryID && pkg.Reference == reference && pkg.RecipeRevision == revision {
			pkg.State = "deleted"
			s.conanPackages[packageKey] = pkg
		}
	}
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

func (s *MemoryStore) RestoreConanRecipeRevision(_ context.Context, repositoryID, reference, revision string) (ConanRecipeRevision, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := conanRecipeKey(repositoryID, reference, revision)
	item, ok := s.conanRecipes[key]
	if !ok || item.State != "deleted" || s.conanAssetsCollectedLocked(repositoryID, reference, revision, "", "") {
		return ConanRecipeRevision{}, ErrNotFound
	}
	item.State = "visible"
	s.conanRecipes[key] = item
	delete(s.artifactTombstones, repositoryID+"\x00"+string(FormatConan)+"\x00"+reference+"#"+revision)
	return item, nil
}
func (s *MemoryStore) RestoreConanPackageRevision(_ context.Context, repositoryID, reference, recipeRevision, packageID, revision string) (ConanPackageRevision, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := conanPackageKey(repositoryID, reference, recipeRevision, packageID, revision)
	item, ok := s.conanPackages[key]
	if !ok || item.State != "deleted" || s.conanAssetsCollectedLocked(repositoryID, reference, recipeRevision, packageID, revision) {
		return ConanPackageRevision{}, ErrNotFound
	}
	if recipe := s.conanRecipes[conanRecipeKey(repositoryID, reference, recipeRevision)]; recipe.State != "visible" {
		return ConanPackageRevision{}, ErrDisabled
	}
	item.State = "visible"
	s.conanPackages[key] = item
	delete(s.artifactTombstones, repositoryID+"\x00"+string(FormatConan)+"\x00"+reference+"#"+recipeRevision+"/"+packageID+"#"+revision)
	return item, nil
}
func (s *MemoryStore) conanAssetsCollectedLocked(repositoryID, reference, recipeRevision, packageID, packageRevision string) bool {
	for _, asset := range s.conanAssets {
		if asset.RepositoryID == repositoryID && asset.Reference == reference && asset.RecipeRevision == recipeRevision && asset.PackageID == packageID && asset.PackageRevision == packageRevision && !s.conanObjects[asset.ObjectKey].CollectedAt.IsZero() {
			return true
		}
	}
	return false
}
