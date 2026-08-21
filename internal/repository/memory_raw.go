package repository

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"
)

func rawAssetKey(repositoryID, path string) string { return repositoryID + "\x00" + path }

func (s *MemoryStore) CreateRawUpload(_ context.Context, upload RawUpload) (RawUpload, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rawUploads[upload.ID] = upload
	return upload, nil
}
func (s *MemoryStore) LockRawUpload(_ context.Context, id string) (func(), error) {
	s.mu.Lock()
	lock := s.rawUploadLocks[id]
	if lock == nil {
		lock = &sync.Mutex{}
		s.rawUploadLocks[id] = lock
	}
	s.mu.Unlock()
	lock.Lock()
	return lock.Unlock, nil
}
func (s *MemoryStore) GetRawUpload(_ context.Context, id string) (RawUpload, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.rawUploads[id]
	if !ok {
		return RawUpload{}, ErrNotFound
	}
	return v, nil
}
func (s *MemoryStore) UpdateRawUpload(_ context.Context, id string, offset int64) (RawUpload, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.rawUploads[id]
	if !ok || v.State != "open" || time.Now().After(v.ExpiresAt) {
		return RawUpload{}, ErrNotFound
	}
	v.Offset = offset
	s.rawUploads[id] = v
	return v, nil
}
func (s *MemoryStore) CancelRawUpload(_ context.Context, id string) (RawUpload, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.rawUploads[id]
	if !ok || v.State != "open" {
		return RawUpload{}, ErrNotFound
	}
	v.State = "cancelled"
	s.rawUploads[id] = v
	return v, nil
}
func (s *MemoryStore) CompleteRawUpload(_ context.Context, id string, asset RawAsset) (RawAsset, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.rawUploads[id]
	if !ok || v.State != "open" || time.Now().After(v.ExpiresAt) || v.RepositoryID != asset.RepositoryID || v.Path != asset.Path {
		return RawAsset{}, ErrNotFound
	}
	now := time.Now().UTC()
	asset.UpdatedAt = now
	s.rawObjects[asset.Digest] = RawObject{RepositoryID: asset.RepositoryID, Digest: asset.Digest, ObjectKey: asset.ObjectKey, Size: asset.Size, CreatedAt: now}
	s.rawAssets[rawAssetKey(asset.RepositoryID, asset.Path)] = asset
	v.State = "completed"
	s.rawUploads[id] = v
	return asset, nil
}

func (s *MemoryStore) ExpireRawUploads(_ context.Context, before time.Time, limit int) ([]RawUpload, error) {
	if limit <= 0 {
		limit = 100
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	uploads := make([]RawUpload, 0)
	for id, upload := range s.rawUploads {
		if len(uploads) >= limit || upload.State != "open" || !upload.ExpiresAt.Before(before) {
			continue
		}
		upload.State = "cancelled"
		s.rawUploads[id] = upload
		uploads = append(uploads, upload)
	}
	return uploads, nil
}

func (s *MemoryStore) ListUncollectedRawUploads(_ context.Context, limit int) ([]RawUpload, error) {
	if limit <= 0 {
		limit = 100
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	uploads := make([]RawUpload, 0)
	for _, upload := range s.rawUploads {
		if len(uploads) >= limit {
			break
		}
		if (upload.State == "completed" || upload.State == "cancelled") && upload.CollectedAt.IsZero() {
			uploads = append(uploads, upload)
		}
	}
	return uploads, nil
}

func (s *MemoryStore) MarkRawUploadCollected(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	upload, ok := s.rawUploads[id]
	if !ok || (upload.State != "completed" && upload.State != "cancelled") || !upload.CollectedAt.IsZero() {
		return ErrNotFound
	}
	upload.CollectedAt = time.Now().UTC()
	s.rawUploads[id] = upload
	return nil
}

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
	asset.UpdatedAt = time.Now().UTC()
	if object, ok := s.rawObjects[asset.Digest]; ok {
		object.RepositoryID = asset.RepositoryID
		object.CollectedAt = time.Time{}
		s.rawObjects[asset.Digest] = object
		asset.ObjectKey = object.ObjectKey
		asset.Size = object.Size
	} else {
		s.rawObjects[asset.Digest] = RawObject{RepositoryID: asset.RepositoryID, Digest: asset.Digest, ObjectKey: asset.ObjectKey, Size: asset.Size, CreatedAt: time.Now().UTC()}
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
func (s *MemoryStore) ListRawAssets(_ context.Context, repositoryID, prefix string, limit int, after string) ([]RawAsset, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	assets := make([]RawAsset, 0)
	for _, asset := range s.rawAssets {
		if asset.RepositoryID == repositoryID && strings.HasPrefix(asset.Path, prefix) && asset.Path > after {
			assets = append(assets, asset)
		}
	}
	sort.Slice(assets, func(i, j int) bool { return assets[i].Path < assets[j].Path })
	if limit > 0 && len(assets) > limit {
		assets = assets[:limit]
	}
	return assets, nil
}
func (s *MemoryStore) DeleteRawAsset(_ context.Context, repositoryID, path string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := rawAssetKey(repositoryID, path)
	asset, ok := s.rawAssets[key]
	if !ok {
		return ErrNotFound
	}
	now := time.Now().UTC()
	s.rawAssetTombstones[key] = asset
	s.artifactTombstones[repositoryID+"\x00"+string(FormatRaw)+"\x00"+path] = ArtifactTombstone{RepositoryID: repositoryID, Format: FormatRaw, Coordinate: path, Digest: asset.Digest, TombstonedAt: now}
	if object, exists := s.rawObjects[asset.Digest]; exists {
		object.CreatedAt, object.CollectedAt = now, time.Time{}
		s.rawObjects[asset.Digest] = object
	}
	delete(s.rawAssets, key)
	return nil
}

func (s *MemoryStore) RestoreRawAsset(_ context.Context, repositoryID, path string) (RawAsset, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := rawAssetKey(repositoryID, path)
	asset, ok := s.rawAssetTombstones[key]
	if !ok {
		return RawAsset{}, ErrNotFound
	}
	if _, exists := s.rawAssets[key]; exists {
		return RawAsset{}, ErrNameExists
	}
	object, ok := s.rawObjects[asset.Digest]
	if !ok || !object.CollectedAt.IsZero() {
		return RawAsset{}, ErrDisabled
	}
	asset.ObjectKey, asset.Size, asset.UpdatedAt = object.ObjectKey, object.Size, time.Now().UTC()
	s.rawAssets[key] = asset
	delete(s.rawAssetTombstones, key)
	delete(s.artifactTombstones, repositoryID+"\x00"+string(FormatRaw)+"\x00"+path)
	return asset, nil
}
func (s *MemoryStore) ListUnreferencedRawObjects(_ context.Context, before time.Time, limit int) ([]RawObject, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var objects []RawObject
	for digest, object := range s.rawObjects {
		if len(objects) >= limit || object.RepositoryID == "" || !object.CollectedAt.IsZero() || !object.CreatedAt.Before(before) {
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
