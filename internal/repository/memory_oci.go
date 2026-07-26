package repository

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"
)

func ociManifestKey(repositoryID, name, digest string) string {
	return repositoryID + "\x00" + name + "\x00" + digest
}
func ociTagKey(repositoryID, name, tag string) string {
	return repositoryID + "\x00" + name + "\x00" + tag
}

func (s *MemoryStore) CreateOCIUpload(_ context.Context, upload OCIUpload) (OCIUpload, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ociUploads[upload.ID] = upload
	return upload, nil
}
func (s *MemoryStore) StageOCIObjectIntent(_ context.Context, intent OCIObjectIntent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.ociObjectIntents[intent.ObjectKey]; !exists {
		intent.CreatedAt = time.Now().UTC()
		s.ociObjectIntents[intent.ObjectKey] = intent
	}
	return nil
}
func (s *MemoryStore) LockOCIUpload(_ context.Context, id string) (func(), error) {
	s.mu.Lock()
	lock := s.ociUploadLocks[id]
	if lock == nil {
		lock = &sync.Mutex{}
		s.ociUploadLocks[id] = lock
	}
	s.mu.Unlock()
	lock.Lock()
	return lock.Unlock, nil
}
func (s *MemoryStore) LockOCIObject(_ context.Context, objectKey string) (func(), error) {
	s.mu.Lock()
	lock := s.ociObjectLocks[objectKey]
	if lock == nil {
		lock = &sync.Mutex{}
		s.ociObjectLocks[objectKey] = lock
	}
	s.mu.Unlock()
	lock.Lock()
	return lock.Unlock, nil
}
func (s *MemoryStore) GetOCIUpload(_ context.Context, id string) (OCIUpload, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.ociUploads[id]
	if !ok {
		return OCIUpload{}, ErrNotFound
	}
	return v, nil
}
func (s *MemoryStore) UpdateOCIUpload(_ context.Context, id string, offset int64) (OCIUpload, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.ociUploads[id]
	if !ok || v.State != "open" || time.Now().After(v.ExpiresAt) {
		return OCIUpload{}, ErrNotFound
	}
	v.Offset = offset
	s.ociUploads[id] = v
	return v, nil
}
func (s *MemoryStore) CancelOCIUpload(_ context.Context, id string) (OCIUpload, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.ociUploads[id]
	if !ok || v.State != "open" {
		return OCIUpload{}, ErrNotFound
	}
	v.State = "expired"
	v.CollectedAt = time.Now().UTC()
	s.ociUploads[id] = v
	return v, nil
}
func (s *MemoryStore) CompleteOCIUpload(_ context.Context, id string, blob OCIBlob) (OCIBlob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.ociUploads[id]
	if !ok || v.State != "open" || time.Now().After(v.ExpiresAt) {
		return OCIBlob{}, ErrNotFound
	}
	if current, exists := s.ociBlobs[blob.Digest]; exists {
		blob = current
	} else {
		s.ociBlobs[blob.Digest] = blob
	}
	if s.ociRepositoryBlobs[v.RepositoryID] == nil {
		s.ociRepositoryBlobs[v.RepositoryID] = map[string]bool{}
	}
	s.ociRepositoryBlobs[v.RepositoryID][blob.Digest] = true
	v.State = "completed"
	s.ociUploads[id] = v
	delete(s.ociObjectIntents, blob.ObjectKey)
	return blob, nil
}
func (s *MemoryStore) ExpireOCIUploads(_ context.Context, before time.Time, limit int) ([]OCIUpload, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var uploads []OCIUpload
	for id, upload := range s.ociUploads {
		if len(uploads) >= limit || upload.State != "open" || !upload.ExpiresAt.Before(before) {
			continue
		}
		upload.State = "expired"
		s.ociUploads[id] = upload
		uploads = append(uploads, upload)
	}
	return uploads, nil
}
func (s *MemoryStore) ListUncollectedOCIUploads(_ context.Context, limit int) ([]OCIUpload, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var uploads []OCIUpload
	for _, upload := range s.ociUploads {
		if len(uploads) >= limit {
			break
		}
		if upload.State == "expired" && upload.CollectedAt.IsZero() {
			uploads = append(uploads, upload)
		}
	}
	return uploads, nil
}
func (s *MemoryStore) MarkOCIUploadCollected(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	upload, ok := s.ociUploads[id]
	if !ok || upload.State != "expired" {
		return ErrNotFound
	}
	upload.CollectedAt = time.Now().UTC()
	s.ociUploads[id] = upload
	return nil
}
func (s *MemoryStore) ListUnclaimedOCIObjectIntents(_ context.Context, before time.Time, limit int) ([]OCIObjectIntent, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var intents []OCIObjectIntent
	for _, intent := range s.ociObjectIntents {
		if len(intents) >= limit {
			break
		}
		if intent.RepositoryID != "" && intent.ClaimedAt.IsZero() && intent.CollectedAt.IsZero() && intent.CreatedAt.Before(before) {
			intents = append(intents, intent)
		}
	}
	return intents, nil
}
func (s *MemoryStore) OCIObjectIntentIsUnclaimed(_ context.Context, objectKey string) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	intent, ok := s.ociObjectIntents[objectKey]
	return ok && intent.ClaimedAt.IsZero() && intent.CollectedAt.IsZero(), nil
}
func (s *MemoryStore) MarkOCIObjectIntentCollected(_ context.Context, objectKey string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	intent, ok := s.ociObjectIntents[objectKey]
	if !ok || !intent.ClaimedAt.IsZero() || !intent.CollectedAt.IsZero() {
		return ErrNotFound
	}
	intent.CollectedAt = time.Now().UTC()
	s.ociObjectIntents[objectKey] = intent
	return nil
}
func (s *MemoryStore) MountOCIBlob(_ context.Context, repositoryID, digest string) (OCIBlob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.ociBlobs[digest]
	if !ok {
		return OCIBlob{}, ErrNotFound
	}
	if s.ociRepositoryBlobs[repositoryID] == nil {
		s.ociRepositoryBlobs[repositoryID] = map[string]bool{}
	}
	s.ociRepositoryBlobs[repositoryID][digest] = true
	return v, nil
}
func (s *MemoryStore) MountOCIBlobFrom(_ context.Context, repositoryID, sourceRepositoryID, digest string) (OCIBlob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.ociRepositoryBlobs[sourceRepositoryID][digest] {
		return OCIBlob{}, ErrNotFound
	}
	v, ok := s.ociBlobs[digest]
	if !ok {
		return OCIBlob{}, ErrNotFound
	}
	if s.ociRepositoryBlobs[repositoryID] == nil {
		s.ociRepositoryBlobs[repositoryID] = map[string]bool{}
	}
	s.ociRepositoryBlobs[repositoryID][digest] = true
	return v, nil
}
func (s *MemoryStore) GetOCIBlob(_ context.Context, repositoryID, digest string) (OCIBlob, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.ociRepositoryBlobs[repositoryID][digest] {
		return OCIBlob{}, ErrNotFound
	}
	v, ok := s.ociBlobs[digest]
	if !ok {
		return OCIBlob{}, ErrNotFound
	}
	return v, nil
}
func (s *MemoryStore) PutOCIManifest(_ context.Context, manifest OCIManifest, reference string) (OCIManifest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := ociManifestKey(manifest.RepositoryID, manifest.Name, manifest.Digest)
	if existing, ok := s.ociManifests[key]; ok {
		manifest = existing
	} else {
		s.ociManifests[key] = manifest
	}
	if !strings.HasPrefix(reference, "sha256:") {
		s.ociTags[ociTagKey(manifest.RepositoryID, manifest.Name, reference)] = manifest.Digest
	}
	delete(s.ociObjectIntents, manifest.ObjectKey)
	return manifest, nil
}

func (s *MemoryStore) PublishReplicatedOCIManifest(_ context.Context, publication OCIReplicationPublication) (OCIManifest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	source, ok := s.ociManifests[ociManifestKey(publication.SourceRepositoryID, publication.Manifest.Name, publication.Manifest.Digest)]
	if !ok || source.ObjectKey != publication.SourceObjectKey || source.Size != publication.Manifest.Size {
		return OCIManifest{}, ErrNotFound
	}
	key := ociManifestKey(publication.TargetRepositoryID, publication.Manifest.Name, publication.Manifest.Digest)
	if existing, ok := s.ociManifests[key]; ok {
		if existing.ObjectKey != publication.Manifest.ObjectKey {
			return OCIManifest{}, ErrNameExists
		}
		return existing, nil
	}
	for _, digest := range publication.BlobDigests {
		if !s.ociRepositoryBlobs[publication.SourceRepositoryID][digest] {
			return OCIManifest{}, ErrNotFound
		}
	}
	if s.ociRepositoryBlobs[publication.TargetRepositoryID] == nil {
		s.ociRepositoryBlobs[publication.TargetRepositoryID] = map[string]bool{}
	}
	for _, digest := range publication.BlobDigests {
		s.ociRepositoryBlobs[publication.TargetRepositoryID][digest] = true
	}
	s.ociManifests[key] = publication.Manifest
	delete(s.ociObjectIntents, publication.Manifest.ObjectKey)
	return publication.Manifest, nil
}
func (s *MemoryStore) GetOCIManifest(_ context.Context, repositoryID, name, reference string) (OCIManifest, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	digest := reference
	if !strings.HasPrefix(digest, "sha256:") {
		digest = s.ociTags[ociTagKey(repositoryID, name, reference)]
	}
	v, ok := s.ociManifests[ociManifestKey(repositoryID, name, digest)]
	if !ok {
		return OCIManifest{}, ErrNotFound
	}
	return v, nil
}
func (s *MemoryStore) ListOCIReferrers(_ context.Context, repositoryID, name, subject string, limit int, after string) ([]OCIManifest, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []OCIManifest
	for _, item := range s.ociManifests {
		if item.RepositoryID == repositoryID && item.Name == name && item.SubjectDigest == subject && item.Digest > after {
			out = append(out, item)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Digest < out[j].Digest })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
func (s *MemoryStore) ListOCIManifestNames(_ context.Context, repositoryID string, limit int, after string) ([]string, error) {
	return s.searchOCIManifestNames(repositoryID, "", limit, after)
}
func (s *MemoryStore) SearchOCIManifestNames(_ context.Context, repositoryID, prefix string, limit int, after string) ([]string, error) {
	return s.searchOCIManifestNames(repositoryID, prefix, limit, after)
}
func (s *MemoryStore) searchOCIManifestNames(repositoryID, prefix string, limit int, after string) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	names := make(map[string]struct{})
	for _, item := range s.ociManifests {
		if item.RepositoryID == repositoryID && strings.HasPrefix(item.Name, prefix) && item.Name > after {
			names[item.Name] = struct{}{}
		}
	}
	out := make([]string, 0, len(names))
	for name := range names {
		out = append(out, name)
	}
	sort.Strings(out)
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
func (s *MemoryStore) ListOCITags(_ context.Context, repositoryID, name string, limit int, after string) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if limit <= 0 {
		limit = 100
	}
	prefix := repositoryID + "\x00" + name + "\x00"
	var tags []string
	for key := range s.ociTags {
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		tag := strings.TrimPrefix(key, prefix)
		if after != "" && tag <= after {
			continue
		}
		tags = append(tags, tag)
	}
	sort.Strings(tags)
	if len(tags) > limit {
		tags = tags[:limit]
	}
	return tags, nil
}
func (s *MemoryStore) DeleteOCIManifest(_ context.Context, repositoryID, name, digest string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := ociManifestKey(repositoryID, name, digest)
	if _, ok := s.ociManifests[key]; !ok {
		return ErrNotFound
	}
	manifest := s.ociManifests[key]
	delete(s.ociManifests, key)
	s.ociObjectIntents[manifest.ObjectKey] = OCIObjectIntent{RepositoryID: repositoryID, ObjectKey: manifest.ObjectKey, Digest: manifest.Digest, Size: manifest.Size, CreatedAt: time.Now().UTC()}
	for tag, target := range s.ociTags {
		if target == digest && strings.HasPrefix(tag, repositoryID+"\x00"+name+"\x00") {
			delete(s.ociTags, tag)
		}
	}
	coordinate := name + "@" + digest
	s.artifactTombstones[repositoryID+"\x00"+string(FormatOCI)+"\x00"+coordinate] = ArtifactTombstone{RepositoryID: repositoryID, Format: FormatOCI, Coordinate: coordinate, Digest: digest, TombstonedAt: time.Now().UTC()}
	return nil
}

func (s *MemoryStore) GetArtifactTombstone(_ context.Context, repositoryID string, format Format, coordinate string) (ArtifactTombstone, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	tombstone, ok := s.artifactTombstones[repositoryID+"\x00"+string(format)+"\x00"+coordinate]
	if !ok {
		return ArtifactTombstone{}, ErrNotFound
	}
	return tombstone, nil
}

func (s *MemoryStore) ListArtifactTombstones(_ context.Context, repositoryID string, format Format, prefix string, limit int, after string) ([]ArtifactTombstone, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]ArtifactTombstone, 0)
	for _, item := range s.artifactTombstones {
		if item.RepositoryID == repositoryID && item.Format == format && strings.HasPrefix(item.Coordinate, prefix) && item.Coordinate > after {
			items = append(items, item)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Coordinate < items[j].Coordinate })
	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}
	return items, nil
}
