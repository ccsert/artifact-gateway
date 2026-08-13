package repository

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"sort"
	"strconv"
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
	return s.createAPTPublicationSessionIdempotently(session, actor, target, key, payload, nil)
}

func (s *MemoryStore) CreateAPTPublicationSessionWithAuditIdempotently(_ context.Context, session APTPublicationSession, actor, target, key, payload string, audit AuditRecord) (APTPublicationSession, bool, error) {
	return s.createAPTPublicationSessionIdempotently(session, actor, target, key, payload, &audit)
}

func (s *MemoryStore) createAPTPublicationSessionIdempotently(session APTPublicationSession, actor, target, key, payload string, audit *AuditRecord) (APTPublicationSession, bool, error) {
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
	if audit != nil {
		s.appendAuditLocked(*audit)
	}
	return session, false, nil
}

func validAPTPublicationSession(session APTPublicationSession) bool {
	return session.ID != "" && session.RepositoryID != "" && ValidAPTPublicationScope(session.Suite) &&
		ValidAPTPublicationScope(session.Component) && session.Publisher != "" && session.ObjectName != "" &&
		len(session.Publisher) <= 512 && ValidAPTObjectName(session.ObjectName) && ValidAPTSHA256Digest(session.DeclaredDigest) &&
		session.DeclaredSize > 0 && session.DeclaredSize <= 1<<30 && len(session.ExpectedIdentity) <= 1024 &&
		!strings.ContainsRune(session.ExpectedIdentity, '\x00') && session.State == APTPublicationSessionOpen && !session.ExpiresAt.IsZero()
}

// ValidAPTSHA256Digest validates the immutable APT publication digest grammar.
func ValidAPTSHA256Digest(value string) bool {
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

// ValidAPTObjectName validates a single Debian binary upload filename.
func ValidAPTObjectName(value string) bool {
	return value != "" && len(value) <= 255 && !strings.Contains(value, "/") && strings.HasSuffix(value, ".deb")
}

func validAPTPackageRevision(revision APTPackageRevision) bool {
	return revision.ID != "" && revision.RepositoryID != "" && validAPTPackageName(revision.Package) && validAPTVersion(revision.Version) &&
		validAPTArchitecture(revision.Architecture) && revision.CanonicalIdentity == revision.Package+"@"+revision.Version+"#"+revision.Architecture &&
		ValidAPTSHA256Digest(revision.Digest) && revision.ObjectKey == "native/apt/sha256/"+strings.TrimPrefix(revision.Digest, "sha256:") &&
		revision.Size > 0 && revision.Size <= 1<<30 && ValidAPTObjectName(revision.ObjectName) &&
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

// ValidAPTPublicationScope validates a suite or component path segment.
func ValidAPTPublicationScope(value string) bool {
	if value == "" || len(value) > 128 || (value[0] < 'a' || value[0] > 'z') && (value[0] < '0' || value[0] > '9') {
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
	return s.completeAPTPackageUpload(id, revision, nil)
}

func (s *MemoryStore) CompleteAPTPackageUploadWithAudit(_ context.Context, id string, revision APTPackageRevision, audit AuditRecord) (APTPackageRevision, error) {
	return s.completeAPTPackageUpload(id, revision, &audit)
}

func (s *MemoryStore) completeAPTPackageUpload(id string, revision APTPackageRevision, audit *AuditRecord) (APTPackageRevision, error) {
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
		if audit != nil {
			s.appendAuditLocked(*audit)
		}
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
	if audit != nil {
		s.appendAuditLocked(*audit)
	}
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
			abandoned = append(abandoned, APTAbandonedUpload{SessionID: id, RepositoryID: session.RepositoryID, ObjectKey: objectKey})
		}
	}
	return abandoned, nil
}

func (s *MemoryStore) ListUncollectedAPTPublicationObjects(_ context.Context, limit int) ([]APTAbandonedUpload, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if limit <= 0 {
		limit = 100
	}
	ids := make([]string, 0)
	for id, session := range s.aptPublicationSessions {
		if session.State == APTPublicationSessionAborted && session.ObjectKey != "" && session.CollectedAt.IsZero() {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	if len(ids) > limit {
		ids = ids[:limit]
	}
	items := make([]APTAbandonedUpload, 0, len(ids))
	for _, id := range ids {
		session := s.aptPublicationSessions[id]
		items = append(items, APTAbandonedUpload{SessionID: id, RepositoryID: session.RepositoryID, ObjectKey: session.ObjectKey})
	}
	return items, nil
}

func (s *MemoryStore) ListUnscheduledAPTPublicationObjects(_ context.Context, limit int) ([]APTAbandonedUpload, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if limit <= 0 {
		limit = 100
	}
	ids := make([]string, 0)
	for id, session := range s.aptPublicationSessions {
		if session.State == APTPublicationSessionAborted && session.ObjectKey != "" && session.ReclaimScheduledAt.IsZero() && session.CollectedAt.IsZero() {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	if len(ids) > limit {
		ids = ids[:limit]
	}
	items := make([]APTAbandonedUpload, 0, len(ids))
	for _, id := range ids {
		session := s.aptPublicationSessions[id]
		items = append(items, APTAbandonedUpload{SessionID: id, RepositoryID: session.RepositoryID, ObjectKey: session.ObjectKey})
	}
	return items, nil
}

func (s *MemoryStore) MarkAPTPublicationObjectScheduled(_ context.Context, sessionID, objectKey string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.aptPublicationSessions[sessionID]
	if !ok {
		return ErrNotFound
	}
	if session.State != APTPublicationSessionAborted || session.ObjectKey != objectKey || !session.CollectedAt.IsZero() {
		return ErrVersionConflict
	}
	if session.ReclaimScheduledAt.IsZero() {
		session.ReclaimScheduledAt = time.Now().UTC()
		s.aptPublicationSessions[sessionID] = session
	}
	return nil
}

func (s *MemoryStore) MarkAPTPublicationObjectCollected(_ context.Context, sessionID, objectKey string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.aptPublicationSessions[sessionID]
	if !ok {
		return ErrNotFound
	}
	if session.State != APTPublicationSessionAborted || session.ObjectKey != objectKey {
		return ErrVersionConflict
	}
	if session.CollectedAt.IsZero() {
		session.CollectedAt = time.Now().UTC()
		s.aptPublicationSessions[sessionID] = session
	}
	return nil
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
	if snapshot.ID == "" || snapshot.RepositoryID == "" || !ValidAPTPublicationScope(snapshot.Suite) || snapshot.Sequence <= 0 || snapshot.State != APTRepositorySnapshotBuilding || len(items) == 0 {
		return APTRepositorySnapshot{}, ErrDisabled
	}
	if err := validateAPTSnapshotMembership(items); err != nil {
		return APTRepositorySnapshot{}, err
	}
	if existing, exists := s.aptSnapshots[snapshot.ID]; exists {
		if existing.RepositoryID != snapshot.RepositoryID || existing.Suite != snapshot.Suite || existing.Sequence != snapshot.Sequence ||
			!sameAPTSnapshotMembership(s.aptSnapshotPackages[snapshot.ID], items) {
			return APTRepositorySnapshot{}, ErrIdempotencyConflict
		}
		return existing, nil
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
			revision.RepositoryID != snapshot.RepositoryID || !ValidAPTPublicationScope(item.Component) || item.Architecture != revision.Architecture {
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

func (s *MemoryStore) GetAPTRepositorySnapshot(_ context.Context, snapshotID string) (APTRepositorySnapshot, []APTSnapshotPackage, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	snapshot, ok := s.aptSnapshots[snapshotID]
	if !ok {
		return APTRepositorySnapshot{}, nil, ErrNotFound
	}
	return snapshot, append([]APTSnapshotPackage(nil), s.aptSnapshotPackages[snapshotID]...), nil
}

func sameAPTSnapshotMembership(left, right []APTSnapshotPackage) bool {
	if len(left) != len(right) {
		return false
	}
	keys := func(items []APTSnapshotPackage) []string {
		result := make([]string, 0, len(items))
		for _, item := range items {
			result = append(result, strings.Join([]string{item.PublicationSessionID, item.PackageRevisionID, item.Component, item.Architecture}, "\x00"))
		}
		sort.Strings(result)
		return result
	}
	return slices.Equal(keys(left), keys(right))
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

// ValidAPTRepositoryPath validates an immutable Hosted APT protocol path.
func ValidAPTRepositoryPath(path string) bool {
	if path == "" || len(path) > 2048 || strings.HasPrefix(path, "/") || strings.ContainsRune(path, 0) || strings.ContainsAny(path, "\\\r\n\t?#") {
		return false
	}
	segments := strings.Split(path, "/")
	if len(segments) < 2 || (segments[0] != "dists" && segments[0] != "pool") {
		return false
	}
	for _, segment := range segments {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
	}
	return true
}

// APTPoolPath derives the repository-global immutable path for one package.
func APTPoolPath(component, packageName, objectName string) string {
	prefix := packageName[:1]
	if strings.HasPrefix(packageName, "lib") && len(packageName) >= 4 {
		prefix = packageName[:4]
	}
	return "pool/" + component + "/" + prefix + "/" + packageName + "/" + objectName
}

func validAPTSnapshotAssets(snapshot APTRepositorySnapshot, assets []APTSnapshotAsset) bool {
	if len(assets) == 0 {
		return false
	}
	paths := make(map[string]struct{}, len(assets))
	byPath := make(map[string]APTSnapshotAsset, len(assets))
	poolCount, indexCount := 0, 0
	required := map[string]bool{
		"dists/" + snapshot.Suite + "/Release":     false,
		"dists/" + snapshot.Suite + "/InRelease":   false,
		"dists/" + snapshot.Suite + "/Release.gpg": false,
	}
	for _, asset := range assets {
		if asset.SnapshotID != snapshot.ID || asset.RepositoryID != snapshot.RepositoryID || !ValidAPTRepositoryPath(asset.Path) ||
			!ValidAPTSHA256Digest(asset.Digest) || asset.ObjectKey != "native/apt/sha256/"+strings.TrimPrefix(asset.Digest, "sha256:") ||
			asset.Size <= 0 || asset.Size > 1<<30 || asset.ContentType == "" || len(asset.ContentType) > 255 ||
			strings.ContainsAny(asset.ContentType, "\x00\r\n") ||
			(strings.HasPrefix(asset.Path, "dists/") && !strings.HasPrefix(asset.Path, "dists/"+snapshot.Suite+"/")) {
			return false
		}
		if _, duplicate := paths[asset.Path]; duplicate {
			return false
		}
		paths[asset.Path] = struct{}{}
		byPath[asset.Path] = asset
		if strings.HasPrefix(asset.Path, "pool/") {
			poolCount++
		}
		if aptDirectIndexPath(snapshot.Suite, asset.Path) {
			indexCount++
		}
		if _, ok := required[asset.Path]; ok {
			required[asset.Path] = true
		}
	}
	for _, present := range required {
		if !present {
			return false
		}
	}
	if poolCount == 0 || indexCount == 0 || assetDigestAtPath(assets, "dists/"+snapshot.Suite+"/Release") != snapshot.ReleaseDigest ||
		assetDigestAtPath(assets, "dists/"+snapshot.Suite+"/InRelease") != snapshot.InReleaseDigest {
		return false
	}
	for _, asset := range assets {
		if aptDirectIndexPath(snapshot.Suite, asset.Path) {
			base := asset.Path[:strings.LastIndex(asset.Path, "/")]
			byHash, ok := byPath[base+"/by-hash/SHA256/"+strings.TrimPrefix(asset.Digest, "sha256:")]
			if !ok || byHash.Digest != asset.Digest || byHash.Size != asset.Size {
				return false
			}
		}
		if strings.Contains(asset.Path, "/by-hash/SHA256/") {
			base, _, _ := strings.Cut(asset.Path, "/by-hash/SHA256/")
			if !strings.HasSuffix(asset.Path, "/"+strings.TrimPrefix(asset.Digest, "sha256:")) {
				return false
			}
			matched := false
			for _, direct := range assets {
				if aptDirectIndexPath(snapshot.Suite, direct.Path) && strings.HasPrefix(direct.Path, base+"/") &&
					direct.Digest == asset.Digest && direct.Size == asset.Size {
					matched = true
					break
				}
			}
			if !matched {
				return false
			}
		}
	}
	return true
}

func aptDirectIndexPath(suite, path string) bool {
	return strings.HasPrefix(path, "dists/"+suite+"/") && !strings.Contains(path, "/by-hash/") &&
		path != "dists/"+suite+"/Release" && path != "dists/"+suite+"/InRelease" && path != "dists/"+suite+"/Release.gpg"
}

func validAPTSnapshotPublication(snapshot APTRepositorySnapshot, assets []APTSnapshotAsset, release []byte) bool {
	return snapshot.State == APTRepositorySnapshotVisible && ValidAPTSHA256Digest(snapshot.ReleaseDigest) &&
		ValidAPTSHA256Digest(snapshot.InReleaseDigest) && snapshot.SignerIdentity != "" && len(snapshot.SignerIdentity) <= 512 &&
		snapshot.KeyFingerprint != "" && len(snapshot.KeyFingerprint) <= 512 && snapshot.SignatureAlgorithm != "" &&
		len(snapshot.SignatureAlgorithm) <= 128 && !strings.ContainsAny(snapshot.SignerIdentity+snapshot.KeyFingerprint+snapshot.SignatureAlgorithm, "\x00\r\n") &&
		validAPTSnapshotAssets(snapshot, assets) && validAPTReleaseClosure(snapshot, assets, release)
}

func validAPTReleaseClosure(snapshot APTRepositorySnapshot, assets []APTSnapshotAsset, release []byte) bool {
	if len(release) == 0 || len(release) > 16<<20 {
		return false
	}
	sum := sha256.Sum256(release)
	if "sha256:"+hex.EncodeToString(sum[:]) != snapshot.ReleaseDigest {
		return false
	}
	expected := make(map[string]APTSnapshotAsset)
	for _, asset := range assets {
		if aptDirectIndexPath(snapshot.Suite, asset.Path) {
			expected[strings.TrimPrefix(asset.Path, "dists/"+snapshot.Suite+"/")] = asset
		}
	}
	actual := make(map[string]struct{}, len(expected))
	scanner := bufio.NewScanner(bytes.NewReader(release))
	inSHA256, seenSection, suiteMatches, acquireByHash := false, false, false, false
	for scanner.Scan() {
		line := scanner.Text()
		suiteMatches = suiteMatches || line == "Suite: "+snapshot.Suite
		acquireByHash = acquireByHash || line == "Acquire-By-Hash: yes"
		if line == "SHA256:" {
			if seenSection {
				return false
			}
			seenSection, inSHA256 = true, true
			continue
		}
		if !inSHA256 {
			continue
		}
		if !strings.HasPrefix(line, " ") {
			inSHA256 = false
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 3 {
			return false
		}
		asset, ok := expected[fields[2]]
		size, err := strconv.ParseInt(fields[1], 10, 64)
		if !ok || err != nil || fields[0] != strings.TrimPrefix(asset.Digest, "sha256:") || size != asset.Size {
			return false
		}
		if _, duplicate := actual[fields[2]]; duplicate {
			return false
		}
		actual[fields[2]] = struct{}{}
	}
	return scanner.Err() == nil && seenSection && suiteMatches && acquireByHash && len(actual) == len(expected)
}

func validAPTSnapshotObjectIntent(snapshot APTRepositorySnapshot, intent APTSnapshotObjectIntent) bool {
	return intent.SnapshotID == snapshot.ID && intent.RepositoryID == snapshot.RepositoryID && ValidAPTSHA256Digest(intent.Digest) &&
		intent.ObjectKey == "native/apt/sha256/"+strings.TrimPrefix(intent.Digest, "sha256:") && intent.Size > 0 && intent.Size <= 1<<30
}

func (s *MemoryStore) CreateAPTSnapshotObjectIntents(_ context.Context, snapshotID string, intents []APTSnapshotObjectIntent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	snapshot, ok := s.aptSnapshots[snapshotID]
	if !ok {
		return ErrNotFound
	}
	if snapshot.State != APTRepositorySnapshotBuilding || len(intents) == 0 {
		return ErrDisabled
	}
	stored := s.aptSnapshotObjects[snapshotID]
	if stored == nil {
		stored = make(map[string]APTSnapshotObjectIntent, len(intents))
	}
	for _, intent := range intents {
		if !validAPTSnapshotObjectIntent(snapshot, intent) {
			return ErrDisabled
		}
		if current, exists := stored[intent.ObjectKey]; exists && (current.Digest != intent.Digest || current.Size != intent.Size) {
			return ErrVersionConflict
		}
	}
	for _, intent := range intents {
		if intent.CreatedAt.IsZero() {
			intent.CreatedAt = time.Now().UTC()
		}
		stored[intent.ObjectKey] = intent
	}
	s.aptSnapshotObjects[snapshotID] = stored
	return nil
}

func assetDigestAtPath(assets []APTSnapshotAsset, path string) string {
	for _, asset := range assets {
		if asset.Path == path {
			return asset.Digest
		}
	}
	return ""
}

func aptSnapshotGeneratedObjects(assets []APTSnapshotAsset) map[string]int64 {
	objects := make(map[string]int64)
	for _, asset := range assets {
		if strings.HasPrefix(asset.Path, "dists/") {
			objects[asset.ObjectKey] = asset.Size
		}
	}
	return objects
}

func (s *MemoryStore) PublishAPTRepositorySnapshotWithAudit(_ context.Context, snapshot APTRepositorySnapshot, assets []APTSnapshotAsset, release []byte, audit AuditRecord) (APTRepositorySnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	existing, ok := s.aptSnapshots[snapshot.ID]
	if !ok {
		return APTRepositorySnapshot{}, ErrNotFound
	}
	if existing.State != APTRepositorySnapshotBuilding || existing.RepositoryID != snapshot.RepositoryID || existing.Suite != snapshot.Suite ||
		existing.Sequence != snapshot.Sequence || !validAPTSnapshotPublication(snapshot, assets, release) ||
		!s.validAPTSnapshotPoolAssetsLocked(snapshot, assets) {
		return APTRepositorySnapshot{}, ErrDisabled
	}
	baseBytes, _ := s.aptBaseCapacityLocked(snapshot.RepositoryID)
	if quota := s.capacityQuotas[snapshot.RepositoryID]; quota > 0 {
		generated := aptSnapshotGeneratedObjects(assets)
		for snapshotID, visibleAssets := range s.aptSnapshotAssets {
			visible := s.aptSnapshots[snapshotID]
			if visible.RepositoryID != snapshot.RepositoryID || visible.State != APTRepositorySnapshotVisible || visible.Suite == snapshot.Suite {
				continue
			}
			for objectKey, size := range aptSnapshotGeneratedObjects(visibleAssets) {
				generated[objectKey] = size
			}
		}
		var generatedBytes int64
		for _, size := range generated {
			generatedBytes += size
		}
		if baseBytes+generatedBytes > quota {
			return APTRepositorySnapshot{}, ErrQuotaExceeded
		}
	}
	for existingSnapshotID, existingAssets := range s.aptSnapshotAssets {
		if s.aptSnapshots[existingSnapshotID].RepositoryID != snapshot.RepositoryID {
			continue
		}
		for _, existingAsset := range existingAssets {
			if !strings.HasPrefix(existingAsset.Path, "pool/") {
				continue
			}
			for _, asset := range assets {
				if asset.Path == existingAsset.Path && asset.Digest != existingAsset.Digest {
					return APTRepositorySnapshot{}, ErrAPTPackageConflict
				}
			}
		}
	}
	for id, current := range s.aptSnapshots {
		if current.RepositoryID == snapshot.RepositoryID && current.Suite == snapshot.Suite && current.State == APTRepositorySnapshotVisible {
			if current.Sequence >= snapshot.Sequence {
				return APTRepositorySnapshot{}, ErrVersionConflict
			}
			current.State = APTRepositorySnapshotRetired
			s.aptSnapshots[id] = current
		}
	}
	if snapshot.PublishedAt.IsZero() {
		snapshot.PublishedAt = time.Now().UTC()
	}
	snapshot.CreatedAt = existing.CreatedAt
	s.aptSnapshots[snapshot.ID] = snapshot
	s.aptSnapshotAssets[snapshot.ID] = append([]APTSnapshotAsset(nil), assets...)
	s.appendAuditLocked(audit)
	return snapshot, nil
}

func (s *MemoryStore) validAPTSnapshotPoolAssetsLocked(snapshot APTRepositorySnapshot, assets []APTSnapshotAsset) bool {
	expected := make(map[string]APTSnapshotAsset)
	for _, item := range s.aptSnapshotPackages[snapshot.ID] {
		revision, ok := s.aptPackageRevisions[item.PackageRevisionID]
		if !ok {
			return false
		}
		path := APTPoolPath(item.Component, revision.Package, revision.ObjectName)
		if _, duplicate := expected[path]; duplicate {
			return false
		}
		expected[path] = APTSnapshotAsset{Digest: revision.Digest, ObjectKey: revision.ObjectKey, Size: revision.Size}
	}
	actual := 0
	for _, asset := range assets {
		if !strings.HasPrefix(asset.Path, "pool/") {
			continue
		}
		actual++
		want, ok := expected[asset.Path]
		if !ok || want.Digest != asset.Digest || want.ObjectKey != asset.ObjectKey || want.Size != asset.Size {
			return false
		}
	}
	return actual == len(expected) && actual > 0
}

func (s *MemoryStore) FailAPTRepositorySnapshot(_ context.Context, snapshotID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	snapshot, ok := s.aptSnapshots[snapshotID]
	if !ok {
		return ErrNotFound
	}
	if snapshot.State == APTRepositorySnapshotFailed {
		return nil
	}
	if snapshot.State != APTRepositorySnapshotBuilding {
		return ErrVersionConflict
	}
	snapshot.State = APTRepositorySnapshotFailed
	s.aptSnapshots[snapshotID] = snapshot
	return nil
}

func (s *MemoryStore) ExpireAPTRepositorySnapshots(ctx context.Context, before time.Time, limit int) error {
	if limit <= 0 {
		limit = 100
	}
	s.mu.RLock()
	ids := make([]string, 0)
	for id, snapshot := range s.aptSnapshots {
		if snapshot.State == APTRepositorySnapshotBuilding && snapshot.CreatedAt.Before(before) {
			ids = append(ids, id)
		}
	}
	s.mu.RUnlock()
	sort.Strings(ids)
	if len(ids) > limit {
		ids = ids[:limit]
	}
	for _, id := range ids {
		release, err := s.LockAPTObject(ctx, "snapshot-lock/"+id)
		if err != nil {
			return err
		}
		s.mu.Lock()
		snapshot := s.aptSnapshots[id]
		if snapshot.State == APTRepositorySnapshotBuilding && snapshot.CreatedAt.Before(before) {
			snapshot.State = APTRepositorySnapshotFailed
			s.aptSnapshots[id] = snapshot
		}
		s.mu.Unlock()
		release()
	}
	return nil
}

func (s *MemoryStore) ListUnscheduledAPTSnapshotObjects(_ context.Context, limit int) ([]APTSnapshotObjectIntent, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if limit <= 0 {
		limit = 100
	}
	result := make([]APTSnapshotObjectIntent, 0)
	for snapshotID, intents := range s.aptSnapshotObjects {
		if s.aptSnapshots[snapshotID].State != APTRepositorySnapshotFailed {
			continue
		}
		for _, intent := range intents {
			if intent.ScheduledAt.IsZero() && intent.CollectedAt.IsZero() {
				result = append(result, intent)
			}
		}
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].SnapshotID+"\x00"+result[i].ObjectKey < result[j].SnapshotID+"\x00"+result[j].ObjectKey
	})
	if len(result) > limit {
		result = result[:limit]
	}
	return result, nil
}

func (s *MemoryStore) MarkAPTSnapshotObjectScheduled(_ context.Context, snapshotID, objectKey string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	intent, ok := s.aptSnapshotObjects[snapshotID][objectKey]
	if !ok {
		return ErrNotFound
	}
	if s.aptSnapshots[snapshotID].State != APTRepositorySnapshotFailed || !intent.CollectedAt.IsZero() {
		return ErrVersionConflict
	}
	if intent.ScheduledAt.IsZero() {
		intent.ScheduledAt = time.Now().UTC()
		s.aptSnapshotObjects[snapshotID][objectKey] = intent
	}
	return nil
}

func (s *MemoryStore) MarkAPTSnapshotObjectCollected(_ context.Context, snapshotID, objectKey string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	intent, ok := s.aptSnapshotObjects[snapshotID][objectKey]
	if !ok {
		return ErrNotFound
	}
	if s.aptSnapshots[snapshotID].State != APTRepositorySnapshotFailed {
		return ErrVersionConflict
	}
	if intent.CollectedAt.IsZero() {
		intent.CollectedAt = time.Now().UTC()
		s.aptSnapshotObjects[snapshotID][objectKey] = intent
	}
	return nil
}

func (s *MemoryStore) APTObjectHasDurableReference(_ context.Context, objectKey string) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.aptObjectHasPackageReferenceLocked(objectKey) {
		return true, nil
	}
	for snapshotID, assets := range s.aptSnapshotAssets {
		if s.aptSnapshots[snapshotID].State == APTRepositorySnapshotFailed {
			continue
		}
		for _, asset := range assets {
			if asset.ObjectKey == objectKey {
				return true, nil
			}
		}
	}
	for snapshotID, intents := range s.aptSnapshotObjects {
		if s.aptSnapshots[snapshotID].State == APTRepositorySnapshotFailed {
			continue
		}
		if _, ok := intents[objectKey]; ok {
			return true, nil
		}
	}
	return false, nil
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

func (s *MemoryStore) GetVisibleAPTSnapshotAsset(_ context.Context, repositoryID, path string) (APTSnapshotAsset, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var found APTSnapshotAsset
	var foundSequence int64
	for snapshotID, assets := range s.aptSnapshotAssets {
		snapshot := s.aptSnapshots[snapshotID]
		if snapshot.RepositoryID != repositoryID || snapshot.State != APTRepositorySnapshotVisible || snapshot.Sequence < foundSequence {
			continue
		}
		if strings.HasPrefix(path, "dists/") && !strings.HasPrefix(path, "dists/"+snapshot.Suite+"/") {
			continue
		}
		for _, asset := range assets {
			if asset.Path == path {
				found, foundSequence = asset, snapshot.Sequence
				break
			}
		}
	}
	if found.Path == "" {
		return APTSnapshotAsset{}, ErrNotFound
	}
	return found, nil
}

func (s *MemoryStore) ListVisibleAPTSnapshotAssets(_ context.Context, repositoryID, suite string) ([]APTSnapshotAsset, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var snapshotID string
	var sequence int64
	for id, snapshot := range s.aptSnapshots {
		if snapshot.RepositoryID == repositoryID && snapshot.Suite == suite && snapshot.State == APTRepositorySnapshotVisible && snapshot.Sequence > sequence {
			snapshotID, sequence = id, snapshot.Sequence
		}
	}
	if snapshotID == "" {
		return nil, ErrNotFound
	}
	assets := append([]APTSnapshotAsset(nil), s.aptSnapshotAssets[snapshotID]...)
	sort.Slice(assets, func(i, j int) bool { return assets[i].Path < assets[j].Path })
	return assets, nil
}

var _ NativeAPTPublicationStore = (*MemoryStore)(nil)
