package repository

import (
	"context"
	"sort"
	"strings"
	"time"
)

func aptPublicationKey(actor, target, key string) string {
	return actor + "\x00" + target + "\x00" + key
}

func aptPackageIdentityKey(repositoryID, identity string) string {
	return repositoryID + "\x00" + identity
}

func (s *MemoryStore) CreateAPTPublicationSessionIdempotently(_ context.Context, session APTPublicationSession, actor, target, key, payload string) (APTPublicationSession, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	idempotencyKey := aptPublicationKey(actor, target, key)
	if record, ok := s.aptPublicationKeys[idempotencyKey]; ok && now.Before(record.expiresAt) {
		if record.payload != payload {
			return APTPublicationSession{}, false, ErrIdempotencyConflict
		}
		existing, ok := s.aptPublicationSessions[record.sessionID]
		if !ok {
			return APTPublicationSession{}, false, ErrNotFound
		}
		return existing, true, nil
	}
	delete(s.aptPublicationKeys, idempotencyKey)
	if !validAPTPublicationSession(session) || key == "" || payload == "" {
		return APTPublicationSession{}, false, ErrDisabled
	}
	if _, ok := s.hostedRepositories[session.RepositoryID]; !ok {
		return APTPublicationSession{}, false, ErrNotFound
	}
	if _, exists := s.aptPublicationSessions[session.ID]; exists {
		return APTPublicationSession{}, false, ErrNameExists
	}
	if quota := s.capacityQuotas[session.RepositoryID]; quota > 0 && s.aptReservedAndStoredBytesLocked(session.RepositoryID)+session.DeclaredSize > quota {
		return APTPublicationSession{}, false, ErrQuotaExceeded
	}
	if session.CreatedAt.IsZero() {
		session.CreatedAt = now
	}
	s.aptPublicationSessions[session.ID] = session
	s.aptPublicationKeys[idempotencyKey] = aptPublicationIdempotencyRecord{payload: payload, sessionID: session.ID, expiresAt: now.Add(24 * time.Hour)}
	return session, false, nil
}

func validAPTPublicationSession(session APTPublicationSession) bool {
	return session.ID != "" && session.RepositoryID != "" && validAPTScopeSegment(session.Suite) &&
		validAPTScopeSegment(session.Component) && session.Publisher != "" && session.ObjectName != "" &&
		len(session.Publisher) <= 512 && validAPTObjectName(session.ObjectName) && validAPTSHA256(session.DeclaredDigest) &&
		session.DeclaredSize > 0 && session.DeclaredSize <= 1<<30 && len(session.ExpectedIdentity) <= 1024 &&
		!strings.ContainsRune(session.ExpectedIdentity, '\x00') && session.State == APTPublicationSessionOpen && !session.ExpiresAt.IsZero()
}

func validAPTSHA256(value string) bool {
	if len(value) != 71 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	for _, r := range value[7:] {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

func validAPTObjectName(value string) bool {
	return value != "" && len(value) <= 255 && !strings.Contains(value, "/") && strings.HasSuffix(value, ".deb")
}

func validAPTPackageRevision(revision APTPackageRevision) bool {
	return revision.ID != "" && revision.RepositoryID != "" && validAPTPackageName(revision.Package) && validAPTVersion(revision.Version) &&
		validAPTArchitecture(revision.Architecture) && revision.CanonicalIdentity == revision.Package+"@"+revision.Version+"#"+revision.Architecture &&
		validAPTSHA256(revision.Digest) && revision.ObjectKey == "native/apt/sha256/"+strings.TrimPrefix(revision.Digest, "sha256:") &&
		revision.Size > 0 && revision.Size <= 1<<30 && validAPTObjectName(revision.ObjectName) &&
		revision.Publisher != "" && len(revision.Publisher) <= 512
}

func validAPTPackageName(value string) bool {
	if len(value) < 2 || len(value) > 255 || ((value[0] < 'a' || value[0] > 'z') && (value[0] < '0' || value[0] > '9')) {
		return false
	}
	for _, r := range value[1:] {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '+' && r != '.' && r != '-' {
			return false
		}
	}
	return true
}

func validAPTVersion(value string) bool {
	if value == "" || len(value) > 255 {
		return false
	}
	for _, r := range value {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') &&
			r != '.' && r != '+' && r != ':' && r != '~' && r != '-' {
			return false
		}
	}
	remainder := value
	if epoch, version, found := strings.Cut(value, ":"); found {
		if epoch == "" || strings.Trim(epoch, "0123456789") != "" {
			return false
		}
		remainder = version
	}
	return remainder != "" && remainder[0] >= '0' && remainder[0] <= '9'
}

func validAPTArchitecture(value string) bool {
	if value == "" || len(value) > 255 || ((value[0] < 'a' || value[0] > 'z') && (value[0] < '0' || value[0] > '9')) {
		return false
	}
	for _, r := range value[1:] {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
			return false
		}
	}
	return true
}

func validAPTScopeSegment(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for _, r := range value {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' && r != '+' && r != '.' {
			return false
		}
	}
	return true
}

func (s *MemoryStore) aptReservedAndStoredBytesLocked(repositoryID string) int64 {
	var used int64
	for _, asset := range s.aptAssets {
		if asset.RepositoryID == repositoryID {
			used += asset.Size
		}
	}
	for _, revision := range s.aptPackageRevisions {
		if revision.RepositoryID == repositoryID {
			used += revision.Size
		}
	}
	for _, session := range s.aptPublicationSessions {
		if session.RepositoryID == repositoryID && (session.State == APTPublicationSessionOpen || session.State == APTPublicationSessionUploading) {
			used += session.DeclaredSize
		}
	}
	return used
}

func (s *MemoryStore) GetAPTPublicationSession(_ context.Context, id string) (APTPublicationSession, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	session, ok := s.aptPublicationSessions[id]
	if !ok {
		return APTPublicationSession{}, ErrNotFound
	}
	return session, nil
}

func (s *MemoryStore) BeginAPTPackageUpload(_ context.Context, id, objectKey string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.aptPublicationSessions[id]
	if !ok {
		return ErrNotFound
	}
	if session.State == APTPublicationSessionUploading && session.ObjectKey == objectKey {
		return nil
	}
	if session.State != APTPublicationSessionOpen || time.Now().After(session.ExpiresAt) || objectKey == "" {
		return ErrDisabled
	}
	session.State = APTPublicationSessionUploading
	session.ObjectKey = objectKey
	s.aptPublicationSessions[id] = session
	return nil
}

func (s *MemoryStore) CompleteAPTPackageUpload(_ context.Context, id string, revision APTPackageRevision) (APTPackageRevision, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.aptPublicationSessions[id]
	if !ok {
		return APTPackageRevision{}, ErrNotFound
	}
	if session.State == APTPublicationSessionStaged && session.PackageRevisionID != "" {
		stored, ok := s.aptPackageRevisions[session.PackageRevisionID]
		if !ok {
			return APTPackageRevision{}, ErrNotFound
		}
		return stored, nil
	}
	if session.State != APTPublicationSessionUploading || time.Now().After(session.ExpiresAt) ||
		!validAPTPackageRevision(revision) || revision.RepositoryID != session.RepositoryID || revision.Digest != session.DeclaredDigest ||
		revision.Size != session.DeclaredSize || revision.ObjectKey != session.ObjectKey || revision.ObjectName != session.ObjectName ||
		revision.CanonicalIdentity == "" || (session.ExpectedIdentity != "" && session.ExpectedIdentity != revision.CanonicalIdentity) {
		return APTPackageRevision{}, ErrDisabled
	}
	identityKey := aptPackageIdentityKey(revision.RepositoryID, revision.CanonicalIdentity)
	if existingID := s.aptPackageIdentities[identityKey]; existingID != "" {
		existing := s.aptPackageRevisions[existingID]
		if existing.Digest != revision.Digest || existing.ObjectKey != revision.ObjectKey || existing.Size != revision.Size {
			return APTPackageRevision{}, ErrAPTPackageConflict
		}
		session.State = APTPublicationSessionStaged
		session.PackageRevisionID = existing.ID
		s.aptPublicationSessions[id] = session
		return existing, nil
	}
	if revision.CreatedAt.IsZero() {
		revision.CreatedAt = time.Now().UTC()
	}
	s.aptPackageRevisions[revision.ID] = revision
	s.aptPackageIdentities[identityKey] = revision.ID
	session.State = APTPublicationSessionStaged
	session.PackageRevisionID = revision.ID
	s.aptPublicationSessions[id] = session
	return revision, nil
}

func (s *MemoryStore) GetAPTPackageRevisionForSession(_ context.Context, sessionID string) (APTPackageRevision, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	session, ok := s.aptPublicationSessions[sessionID]
	if !ok || session.PackageRevisionID == "" {
		return APTPackageRevision{}, ErrNotFound
	}
	revision, ok := s.aptPackageRevisions[session.PackageRevisionID]
	if !ok {
		return APTPackageRevision{}, ErrNotFound
	}
	return revision, nil
}

func (s *MemoryStore) ExpireAPTPublicationSessions(_ context.Context, before time.Time, limit int) ([]APTAbandonedUpload, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if limit <= 0 {
		limit = 100
	}
	ids := make([]string, 0)
	for id, session := range s.aptPublicationSessions {
		if (session.State == APTPublicationSessionOpen || session.State == APTPublicationSessionUploading) && !session.ExpiresAt.After(before) {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	if len(ids) > limit {
		ids = ids[:limit]
	}
	abandoned := make([]APTAbandonedUpload, 0, len(ids))
	for _, id := range ids {
		session := s.aptPublicationSessions[id]
		objectKey := session.ObjectKey
		session.State = APTPublicationSessionAborted
		s.aptPublicationSessions[id] = session
		if objectKey != "" && !s.aptObjectHasPackageReferenceLocked(objectKey) {
			abandoned = append(abandoned, APTAbandonedUpload{SessionID: id, ObjectKey: objectKey})
		}
	}
	return abandoned, nil
}

func (s *MemoryStore) aptObjectHasPackageReferenceLocked(objectKey string) bool {
	for _, revision := range s.aptPackageRevisions {
		if revision.ObjectKey == objectKey {
			return true
		}
	}
	return false
}

func (s *MemoryStore) APTObjectHasPackageReference(_ context.Context, objectKey string) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.aptObjectHasPackageReferenceLocked(objectKey), nil
}

func (s *MemoryStore) CreateAPTRepositorySnapshot(_ context.Context, snapshot APTRepositorySnapshot, items []APTSnapshotPackage) (APTRepositorySnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if snapshot.ID == "" || snapshot.RepositoryID == "" || !validAPTScopeSegment(snapshot.Suite) || snapshot.Sequence <= 0 || snapshot.State != APTRepositorySnapshotBuilding || len(items) == 0 {
		return APTRepositorySnapshot{}, ErrDisabled
	}
	if err := validateAPTSnapshotMembership(items); err != nil {
		return APTRepositorySnapshot{}, err
	}
	if _, exists := s.aptSnapshots[snapshot.ID]; exists {
		return APTRepositorySnapshot{}, ErrNameExists
	}
	for _, existing := range s.aptSnapshots {
		if existing.RepositoryID == snapshot.RepositoryID && existing.Suite == snapshot.Suite && existing.Sequence == snapshot.Sequence {
			return APTRepositorySnapshot{}, ErrNameExists
		}
	}
	storedItems := make([]APTSnapshotPackage, len(items))
	for i, item := range items {
		revision, ok := s.aptPackageRevisions[item.PackageRevisionID]
		session, sessionOK := s.aptPublicationSessions[item.PublicationSessionID]
		if !ok || !sessionOK || session.State != APTPublicationSessionStaged || session.RepositoryID != snapshot.RepositoryID ||
			session.Suite != snapshot.Suite || session.Component != item.Component || session.PackageRevisionID != item.PackageRevisionID ||
			revision.RepositoryID != snapshot.RepositoryID || !validAPTScopeSegment(item.Component) || item.Architecture != revision.Architecture {
			return APTRepositorySnapshot{}, ErrDisabled
		}
		item.SnapshotID = snapshot.ID
		storedItems[i] = item
	}
	if snapshot.CreatedAt.IsZero() {
		snapshot.CreatedAt = time.Now().UTC()
	}
	s.aptSnapshots[snapshot.ID] = snapshot
	s.aptSnapshotPackages[snapshot.ID] = storedItems
	return snapshot, nil
}

func validateAPTSnapshotMembership(items []APTSnapshotPackage) error {
	sessions := make(map[string]struct{}, len(items))
	memberships := make(map[string]struct{}, len(items))
	for _, item := range items {
		if _, duplicate := sessions[item.PublicationSessionID]; duplicate {
			return ErrNameExists
		}
		sessions[item.PublicationSessionID] = struct{}{}
		membership := item.PackageRevisionID + "\x00" + item.Component + "\x00" + item.Architecture
		if _, duplicate := memberships[membership]; duplicate {
			return ErrNameExists
		}
		memberships[membership] = struct{}{}
	}
	return nil
}

func (s *MemoryStore) GetVisibleAPTRepositorySnapshot(_ context.Context, repositoryID, suite string) (APTRepositorySnapshot, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var found APTRepositorySnapshot
	for _, snapshot := range s.aptSnapshots {
		if snapshot.RepositoryID == repositoryID && snapshot.Suite == suite && snapshot.State == APTRepositorySnapshotVisible && snapshot.Sequence > found.Sequence {
			found = snapshot
		}
	}
	if found.ID == "" {
		return APTRepositorySnapshot{}, ErrNotFound
	}
	return found, nil
}

var _ NativeAPTPublicationStore = (*MemoryStore)(nil)
