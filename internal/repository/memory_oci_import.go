package repository

import (
	"context"
	"math"
	"sort"
	"sync"
	"time"
)

func (s *MemoryStore) StageOCIImportObject(ctx context.Context, intent OCIObjectIntent) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if old, ok := s.ociObjectIntents[intent.ObjectKey]; ok && (old.Digest != intent.Digest || old.Size != intent.Size) {
		return ErrNameExists
	}
	intent.CreatedAt = time.Now().UTC()
	for _, m := range s.ociManifests {
		if m.ObjectKey == intent.ObjectKey {
			intent.ClaimedAt = intent.CreatedAt
			break
		}
	}
	if b, ok := s.ociBlobs[intent.Digest]; ok && b.ObjectKey == intent.ObjectKey {
		intent.ClaimedAt = intent.CreatedAt
	}
	s.ociObjectIntents[intent.ObjectKey] = intent
	return nil
}

func (s *MemoryStore) LockOCIImport(ctx context.Context, c OCIImportClosure) (context.Context, func(), error) {
	type lock struct {
		key     string
		acquire func() (func(), error)
	}
	locks := make([]lock, 0, len(c.Manifests)+len(c.objects()))
	for _, m := range c.Manifests {
		manifest := m
		locks = append(locks, lock{artifactScanLockKey(c.RepositoryID, FormatOCI, c.Name, m.Digest), func() (func(), error) {
			return s.LockArtifactScanIdentity(ctx, c.RepositoryID, FormatOCI, c.Name, manifest.Digest)
		}})
	}
	for _, object := range c.objects() {
		key := object.ObjectKey
		locks = append(locks, lock{"native-oci-object:" + key, func() (func(), error) { return s.LockOCIObject(ctx, key) }})
	}
	sort.Slice(locks, func(i, j int) bool { return locks[i].key < locks[j].key })
	releases := []func(){}
	var once sync.Once
	releaseAll := func() {
		once.Do(func() {
			for i := len(releases) - 1; i >= 0; i-- {
				releases[i]()
			}
		})
	}
	last := ""
	for _, item := range locks {
		if item.key == last {
			continue
		}
		last = item.key
		if err := ctx.Err(); err != nil {
			releaseAll()
			return ctx, nil, err
		}
		release, err := item.acquire()
		if err != nil {
			releaseAll()
			return ctx, nil, err
		}
		releases = append(releases, release)
	}
	return ctx, releaseAll, nil
}

func (s *MemoryStore) PublishOCIImportClosure(ctx context.Context, c OCIImportClosure) error {
	if err := validateOCIImport(c); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	repo, ok := s.hostedRepositories[c.RepositoryID]
	if !ok || repo.Format != FormatOCI || repo.Type != RepositoryTypeHosted || repo.State != RepositoryActive {
		return ErrDisabled
	}
	tagKey := ociTagKey(c.RepositoryID, c.Name, c.Reference)
	if current, ok := s.ociTags[tagKey]; ok && current != c.RootDigest {
		return ErrNameExists
	}
	capacity, err := s.repositoryCapacityLocked(c.RepositoryID)
	if err != nil {
		return err
	}
	additional := int64(0)
	changed := false
	for _, m := range c.Manifests {
		coordinate := c.Name + "@" + m.Digest
		if _, ok := s.artifactTombstones[c.RepositoryID+"\x00oci\x00"+coordinate]; ok {
			return ErrNameExists
		}
		if q, ok := s.artifactQuarantines[artifactQuarantineKey(c.RepositoryID, FormatOCI, c.Name, m.Digest)]; ok && q.State == ArtifactQuarantineStateQuarantined {
			return ErrArtifactQuarantined
		}
		if old, ok := s.ociManifests[ociManifestKey(c.RepositoryID, c.Name, m.Digest)]; ok {
			if old.ObjectKey != m.ObjectKey || old.Size != m.Size || old.MediaType != m.MediaType {
				return ErrNameExists
			}
		} else {
			if m.Size > math.MaxInt64-additional {
				return ErrQuotaExceeded
			}
			additional += m.Size
			changed = true
			intent, ok := s.ociObjectIntents[m.ObjectKey]
			if !ok || intent.Digest != m.Digest || intent.Size != m.Size || !intent.CollectedAt.IsZero() {
				return ErrNotFound
			}
		}
	}
	for _, b := range c.Blobs {
		if old, ok := s.ociBlobs[b.Digest]; ok && (old.ObjectKey != b.ObjectKey || old.Size != b.Size) {
			return ErrNameExists
		}
		if !s.ociRepositoryBlobs[c.RepositoryID][b.Digest] {
			if b.Size > math.MaxInt64-additional {
				return ErrQuotaExceeded
			}
			additional += b.Size
			changed = true
			if _, exists := s.ociBlobs[b.Digest]; !exists {
				intent, ok := s.ociObjectIntents[b.ObjectKey]
				if !ok || intent.Digest != b.Digest || intent.Size != b.Size || !intent.CollectedAt.IsZero() {
					return ErrNotFound
				}
			}
		}
	}
	if capacity.QuotaBytes > 0 && (additional > math.MaxInt64-capacity.UsedBytes || capacity.UsedBytes+additional > capacity.QuotaBytes) {
		return ErrQuotaExceeded
	}
	now := time.Now().UTC()
	if s.ociRepositoryBlobs[c.RepositoryID] == nil {
		s.ociRepositoryBlobs[c.RepositoryID] = map[string]bool{}
	}
	for _, b := range c.Blobs {
		s.ociBlobs[b.Digest] = b
		s.ociRepositoryBlobs[c.RepositoryID][b.Digest] = true
		delete(s.ociObjectIntents, b.ObjectKey)
	}
	for _, m := range c.Manifests {
		key := ociManifestKey(c.RepositoryID, c.Name, m.Digest)
		if _, ok := s.ociManifests[key]; !ok {
			m.CreatedAt = now
			s.ociManifests[key] = m.OCIManifest
		}
		delete(s.ociObjectIntents, m.ObjectKey)
	}
	if c.Reference != c.RootDigest {
		if _, ok := s.ociTags[tagKey]; !ok {
			changed = true
		}
		s.ociTags[tagKey] = c.RootDigest
	}
	if changed {
		s.appendAuditLocked(withRequestContextAudit(ctx, ociImportAudit(c, repo.Name)))
	}
	return nil
}
