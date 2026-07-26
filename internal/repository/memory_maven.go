package repository

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"sort"
	"strconv"
	"strings"
	"time"
)

const mavenObjectClaimLease = 5 * time.Minute

func (s *MemoryStore) CreateMavenPublishSession(_ context.Context, session MavenPublishSession) (MavenPublishSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.mavenSessions[session.ID] = session
	s.mavenUploads[session.ID] = map[string]string{}
	return session, nil
}
func (s *MemoryStore) CreateMavenPublishSessionIdempotently(ctx context.Context, session MavenPublishSession, actor, target, key, payload string) (MavenPublishSession, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	recordKey := actor + "\x00" + target + "\x00" + key
	if record, ok := s.mavenSessionKeys[recordKey]; ok && time.Now().UTC().Before(record.expiresAt) {
		if record.payload != payload {
			return MavenPublishSession{}, false, ErrIdempotencyConflict
		}
		return s.mavenSessions[record.repositoryID], true, nil
	}
	s.mavenSessions[session.ID] = session
	s.mavenUploads[session.ID] = map[string]string{}
	s.mavenSessionKeys[recordKey] = idempotencyRecord{payload: payload, repositoryID: session.ID, expiresAt: time.Now().UTC().Add(24 * time.Hour)}
	return session, false, nil
}
func (s *MemoryStore) GetMavenPublishSession(_ context.Context, id string) (MavenPublishSession, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.mavenSessions[id]
	if !ok {
		return MavenPublishSession{}, ErrNotFound
	}
	return v, nil
}
func (s *MemoryStore) FindOpenMavenPublishSession(_ context.Context, repoID, coordinate, publisher string) (MavenPublishSession, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, v := range s.mavenSessions {
		if v.RepositoryID == repoID && v.Coordinate == coordinate && v.Publisher == publisher && v.State == "open" {
			return v, nil
		}
	}
	return MavenPublishSession{}, ErrNotFound
}
func (s *MemoryStore) FindMavenPublishSession(_ context.Context, repoID, coordinate, publisher string) (MavenPublishSession, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, v := range s.mavenSessions {
		if v.RepositoryID == repoID && v.Coordinate == coordinate && v.Publisher == publisher && v.State == "open" {
			return v, nil
		}
	}
	for _, v := range s.mavenSessions {
		if v.RepositoryID == repoID && v.Coordinate == coordinate && v.Publisher == publisher {
			return v, nil
		}
	}
	return MavenPublishSession{}, ErrNotFound
}
func (s *MemoryStore) FindAnyMavenPublishSession(_ context.Context, repoID, coordinate string) (MavenPublishSession, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, v := range s.mavenSessions {
		if v.RepositoryID == repoID && v.Coordinate == coordinate && v.State == "open" {
			return v, nil
		}
	}
	for _, v := range s.mavenSessions {
		if v.RepositoryID == repoID && v.Coordinate == coordinate {
			return v, nil
		}
	}
	return MavenPublishSession{}, ErrNotFound
}
func (s *MemoryStore) AppendMavenPublishObject(_ context.Context, id string, object MavenDeclaredObject) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.mavenSessions[id]
	if !ok || v.State != "open" {
		return ErrNotFound
	}
	for _, o := range v.Objects {
		if o.Name == object.Name {
			if o.Digest != object.Digest || o.Size != object.Size {
				return ErrNameExists
			}
			return nil
		}
	}
	v.Objects = append(v.Objects, object)
	s.mavenSessions[id] = v
	return nil
}
func (s *MemoryStore) SetMavenPublishPom(_ context.Context, id, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.mavenSessions[id]
	if !ok || v.State != "open" {
		return ErrNotFound
	}
	v.PomObject = name
	s.mavenSessions[id] = v
	return nil
}
func (s *MemoryStore) MarkMavenPublishObject(_ context.Context, id, name, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.mavenSessions[id]; !ok {
		return ErrNotFound
	}
	if intent, exists := s.mavenObjectIntents[key]; exists {
		if !intent.claimedAt.IsZero() && intent.deletedAt.IsZero() {
			return ErrDisabled
		}
		if !intent.deletedAt.IsZero() {
			intent.claimedAt, intent.deletedAt, intent.createdAt, intent.claimToken = time.Time{}, time.Time{}, time.Now().UTC(), ""
			s.mavenObjectIntents[key] = intent
		}
	} else {
		s.mavenObjectIntents[key] = mavenObjectIntent{createdAt: time.Now().UTC()}
	}
	s.mavenUploads[id][name] = key
	return nil
}
func (s *MemoryStore) CommitMavenPublishSession(_ context.Context, id string, assets []MavenAsset) (MavenArtifact, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.commitMavenPublishSessionLocked(id, assets)
}

func (s *MemoryStore) CommitMavenPublishSessionIdempotently(_ context.Context, id, key, payload string, assets []MavenAsset) (MavenArtifact, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if record, ok := s.mavenCommitKeys[id]; ok && time.Now().UTC().Before(record.expiresAt) {
		if record.key != key || record.payload != payload {
			return MavenArtifact{}, false, ErrIdempotencyConflict
		}
		artifact, ok := s.mavenArtifacts[id]
		if !ok {
			return MavenArtifact{}, false, ErrNotFound
		}
		return artifact, true, nil
	}
	delete(s.mavenCommitKeys, id)
	if session, ok := s.mavenSessions[id]; !ok {
		return MavenArtifact{}, false, ErrNotFound
	} else if session.State == "committed" {
		return MavenArtifact{}, false, ErrNameExists
	}
	artifact, err := s.commitMavenPublishSessionLocked(id, assets)
	if err != nil {
		return MavenArtifact{}, false, err
	}
	s.mavenCommitKeys[id] = mavenCommitRecord{key: key, payload: payload, expiresAt: time.Now().UTC().Add(24 * time.Hour)}
	return artifact, false, nil
}

func (s *MemoryStore) commitMavenPublishSessionLocked(id string, assets []MavenAsset) (MavenArtifact, error) {
	session, ok := s.mavenSessions[id]
	if !ok {
		return MavenArtifact{}, ErrNotFound
	}
	if session.State != "open" || time.Now().After(session.ExpiresAt) {
		return MavenArtifact{}, ErrDisabled
	}
	for _, o := range session.Objects {
		if s.mavenUploads[id][o.Name] == "" {
			return MavenArtifact{}, ErrDisabled
		}
		if intent := s.mavenObjectIntents[s.mavenUploads[id][o.Name]]; !intent.claimedAt.IsZero() {
			return MavenArtifact{}, ErrDisabled
		}
	}
	for _, existing := range s.mavenArtifacts {
		if existing.RepositoryID == session.RepositoryID && existing.Coordinate == session.Coordinate {
			return MavenArtifact{}, ErrNameExists
		}
	}
	for _, a := range assets {
		if intent := s.mavenObjectIntents[a.ObjectKey]; !intent.claimedAt.IsZero() || !intent.deletedAt.IsZero() {
			return MavenArtifact{}, ErrDisabled
		}
		k := a.RepositoryID + "\x00" + a.Path
		if _, exists := s.mavenAssets[k]; exists {
			return MavenArtifact{}, ErrNameExists
		}
		s.mavenAssets[k] = a
		s.mavenObjectRefs[a.ObjectKey] = true
	}
	artifact := MavenArtifact{ID: id, RepositoryID: session.RepositoryID, Coordinate: session.Coordinate, Digest: session.Objects[0].Digest, State: "visible", CreatedAt: time.Now().UTC()}
	s.mavenArtifacts[id] = artifact
	session.State = "committed"
	s.mavenSessions[id] = session
	return artifact, nil
}
func (s *MemoryStore) GetMavenAsset(_ context.Context, repositoryID, path string) (MavenAsset, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.mavenAssets[repositoryID+"\x00"+path]
	if !ok {
		return MavenAsset{}, ErrNotFound
	}
	return v, nil
}
func (s *MemoryStore) ListMavenArtifacts(_ context.Context, repositoryID string) ([]MavenArtifact, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []MavenArtifact{}
	for _, a := range s.mavenArtifacts {
		if a.RepositoryID == repositoryID && a.State == "visible" {
			out = append(out, a)
		}
	}
	return out, nil
}

func (s *MemoryStore) SearchMavenArtifacts(_ context.Context, repositoryID, prefix string, limit int, after string) ([]MavenArtifact, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []MavenArtifact{}
	for _, artifact := range s.mavenArtifacts {
		if artifact.RepositoryID == repositoryID && artifact.State == "visible" && strings.HasPrefix(artifact.Coordinate, prefix) && artifact.Coordinate > after {
			out = append(out, artifact)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Coordinate < out[j].Coordinate })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (s *MemoryStore) GetMavenArtifact(_ context.Context, repositoryID, artifactID string) (MavenArtifact, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	artifact, ok := s.mavenArtifacts[artifactID]
	if !ok || artifact.RepositoryID != repositoryID {
		return MavenArtifact{}, ErrNotFound
	}
	return artifact, nil
}

func (s *MemoryStore) TombstoneMavenArtifact(_ context.Context, repositoryID, artifactID string) (MavenArtifact, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	artifact, ok := s.mavenArtifacts[artifactID]
	if !ok || artifact.RepositoryID != repositoryID {
		return MavenArtifact{}, ErrNotFound
	}
	if artifact.State == "deleted" {
		return artifact, nil
	}
	prefix := mavenArtifactPathPrefix(artifact.Coordinate)
	for key, asset := range s.mavenAssets {
		if asset.RepositoryID == repositoryID && strings.HasPrefix(asset.Path, prefix) {
			delete(s.mavenAssets, key)
		}
	}
	for key := range s.mavenObjectRefs {
		stillReferenced := false
		for _, asset := range s.mavenAssets {
			if asset.ObjectKey == key {
				stillReferenced = true
				break
			}
		}
		if !stillReferenced {
			delete(s.mavenObjectRefs, key)
		}
	}
	artifact.State = "deleted"
	s.mavenArtifacts[artifactID] = artifact
	s.artifactTombstones[repositoryID+"\x00"+string(FormatMaven)+"\x00"+artifact.Coordinate] = ArtifactTombstone{RepositoryID: repositoryID, Format: FormatMaven, Coordinate: artifact.Coordinate, Digest: artifact.Digest, TombstonedAt: time.Now().UTC()}
	return artifact, nil
}

func mavenArtifactPathPrefix(coordinate string) string {
	parts := strings.Split(coordinate, ":")
	return strings.ReplaceAll(parts[0], ".", "/") + "/" + parts[1] + "/" + parts[2] + "/"
}
func (s *MemoryStore) ClaimExpiredMavenObjectIntents(_ context.Context, before time.Time, limit int) ([]MavenObjectIntent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	claimed := make([]MavenObjectIntent, 0, limit)
	for key, intent := range s.mavenObjectIntents {
		if len(claimed) == limit || (!intent.claimedAt.IsZero() && now.Sub(intent.claimedAt) < mavenObjectClaimLease) || !intent.deletedAt.IsZero() || intent.createdAt.After(before) || s.mavenObjectRefs[key] {
			continue
		}
		liveSession := false
		for sessionID, uploads := range s.mavenUploads {
			if uploadsKey := uploads; uploadsKey != nil {
				for _, uploadedKey := range uploadsKey {
					if uploadedKey == key {
						session := s.mavenSessions[sessionID]
						liveSession = session.State == "open" && session.ExpiresAt.After(time.Now())
						break
					}
				}
			}
			if liveSession {
				break
			}
		}
		if liveSession {
			continue
		}
		intent.claimedAt, intent.claimToken = now, newMavenObjectClaimToken()
		s.mavenObjectIntents[key] = intent
		claimed = append(claimed, MavenObjectIntent{RepositoryID: s.mavenObjectIntentRepositoryID(key), ObjectKey: key, ClaimToken: intent.claimToken})
	}
	return claimed, nil
}
func (s *MemoryStore) MavenObjectIntentClaimIsActive(_ context.Context, key, claimToken string) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	intent, ok := s.mavenObjectIntents[key]
	return ok && !intent.claimedAt.IsZero() && time.Since(intent.claimedAt) < mavenObjectClaimLease && intent.claimToken == claimToken && intent.deletedAt.IsZero() && !s.mavenObjectRefs[key], nil
}

func (s *MemoryStore) mavenObjectIntentRepositoryID(key string) string {
	for sessionID, uploads := range s.mavenUploads {
		for _, uploadedKey := range uploads {
			if uploadedKey == key {
				return s.mavenSessions[sessionID].RepositoryID
			}
		}
	}
	return ""
}
func (s *MemoryStore) MavenObjectIntentHasReference(_ context.Context, key string) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.mavenObjectRefs[key], nil
}
func (s *MemoryStore) DeleteClaimedMavenObjectIntent(_ context.Context, key, claimToken string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	intent, ok := s.mavenObjectIntents[key]
	if !ok || intent.claimedAt.IsZero() || intent.claimToken != claimToken || !intent.deletedAt.IsZero() || s.mavenObjectRefs[key] {
		return ErrNotFound
	}
	intent.deletedAt = time.Now().UTC()
	s.mavenObjectIntents[key] = intent
	return nil
}
func (s *MemoryStore) ReleaseClaimedMavenObjectIntent(_ context.Context, key, claimToken string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	intent, ok := s.mavenObjectIntents[key]
	if !ok || intent.claimedAt.IsZero() || intent.claimToken != claimToken || !intent.deletedAt.IsZero() || s.mavenObjectRefs[key] {
		return ErrNotFound
	}
	intent.claimedAt, intent.claimToken = time.Time{}, ""
	s.mavenObjectIntents[key] = intent
	return nil
}

func newMavenObjectClaimToken() string {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 10)
	}
	return hex.EncodeToString(value)
}
