package repository

import (
	"context"
	"sync"
	"time"
)

func rawAssetKey(repositoryID, path string) string { return repositoryID + "\x00" + path }

func (s *MemoryStore) LockRawObject(_ context.Context, digest string) (func(), error) {
	s.mu.Lock()
	lock := s.rawObjectLocks[digest]
	if lock == nil {
		lock = &sync.Mutex{}
		s.rawObjectLocks[digest] = lock
	}
	s.mu.Unlock()
	lock.Lock()
	return lock.Unlock, nil
}

func (s *MemoryStore) StageRawObject(_ context.Context, object RawObject) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.rawObjects[object.Digest]; ok {
		existing.CollectedAt = time.Time{}
		s.rawObjects[object.Digest] = existing
		return nil
	}
	object.CreatedAt = time.Now().UTC()
	s.rawObjects[object.Digest] = object
	return nil
}

func (s *MemoryStore) PutRawAsset(_ context.Context, asset RawAsset) (RawAsset, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if object, ok := s.rawObjects[asset.Digest]; ok {
		asset.ObjectKey = object.ObjectKey
		asset.Size = object.Size
	} else {
		s.rawObjects[asset.Digest] = RawObject{Digest: asset.Digest, ObjectKey: asset.ObjectKey, Size: asset.Size, CreatedAt: time.Now().UTC()}
	}
	s.rawAssets[rawAssetKey(asset.RepositoryID, asset.Path)] = asset
	return asset, nil
}
func (s *MemoryStore) GetRawAsset(_ context.Context, repositoryID, path string) (RawAsset, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	asset, ok := s.rawAssets[rawAssetKey(repositoryID, path)]
	if !ok {
		return RawAsset{}, ErrNotFound
	}
	return asset, nil
}
func (s *MemoryStore) DeleteRawAsset(_ context.Context, repositoryID, path string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := rawAssetKey(repositoryID, path)
	if _, ok := s.rawAssets[key]; !ok {
		return ErrNotFound
	}
	delete(s.rawAssets, key)
	return nil
}
func (s *MemoryStore) ListUnreferencedRawObjects(_ context.Context, before time.Time, limit int) ([]RawObject, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var objects []RawObject
	for digest, object := range s.rawObjects {
		if len(objects) >= limit || !object.CollectedAt.IsZero() || !object.CreatedAt.Before(before) {
			continue
		}
		referenced := false
		for _, asset := range s.rawAssets {
			if asset.Digest == digest {
				referenced = true
				break
			}
		}
		if !referenced {
			objects = append(objects, object)
		}
	}
	return objects, nil
}
func (s *MemoryStore) RawObjectIsUnreferenced(_ context.Context, digest string) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	object, ok := s.rawObjects[digest]
	if !ok || !object.CollectedAt.IsZero() {
		return false, nil
	}
	for _, asset := range s.rawAssets {
		if asset.Digest == digest {
			return false, nil
		}
	}
	return true, nil
}
func (s *MemoryStore) MarkRawObjectCollected(_ context.Context, digest string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	object, ok := s.rawObjects[digest]
	if !ok || !object.CollectedAt.IsZero() {
		return ErrNotFound
	}
	for _, asset := range s.rawAssets {
		if asset.Digest == digest {
			return ErrNotFound
		}
	}
	object.CollectedAt = time.Now().UTC()
	s.rawObjects[digest] = object
	return nil
}
