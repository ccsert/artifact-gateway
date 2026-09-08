package repository

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
)

func (s *MemoryStore) aptArchiveStateLocked(plan APTArchiveRestorePlan) aptArchiveRestoreState {
	state := aptArchiveRestoreState{repo: s.hostedRepositories[plan.Snapshot.RepositoryID], quota: s.capacityQuotas[plan.Snapshot.RepositoryID], packages: make(map[string]APTPackageRevision), snapshots: s.aptSnapshots, assets: s.aptSnapshotAssets, pool: make(map[string]APTSnapshotAsset)}
	for path, a := range s.aptPoolPaths[plan.Snapshot.RepositoryID] {
		state.pool[path] = a
	}
	state.deletions = s.aptDeletionsLocked(plan.Snapshot.RepositoryID, plan.Snapshot.Suite)
	state.baseBytes, _ = s.aptBaseCapacityLocked(plan.Snapshot.RepositoryID)
	for _, p := range s.aptPackageRevisions {
		if p.RepositoryID == plan.Snapshot.RepositoryID {
			state.packages[p.CanonicalIdentity] = p
		}
	}
	for _, session := range s.aptPublicationSessions {
		if session.RepositoryID == plan.Snapshot.RepositoryID && (session.State == APTPublicationSessionOpen || session.State == APTPublicationSessionUploading) {
			state.reservedBytes += session.DeclaredSize
		}
	}
	for _, r := range s.aptArchiveRestores {
		if r.RepositoryID == plan.Snapshot.RepositoryID && r.SnapshotID != plan.Snapshot.ID && r.State == "preparing" {
			state.reservedBytes += r.ReservedBytes
		}
	}
	for id, assets := range s.aptSnapshotAssets {
		if s.aptSnapshots[id].RepositoryID == plan.Snapshot.RepositoryID {
			for _, a := range assets {
				if strings.HasPrefix(a.Path, "pool/") {
					state.pool[a.Path] = a
				}
			}
		}
	}
	for _, p := range s.aptSnapshotPackages[plan.Snapshot.ID] {
		state.members = append(state.members, APTArchivePackage{Component: p.Component, Revision: s.aptPackageRevisions[p.PackageRevisionID]})
	}
	return state
}

func (s *MemoryStore) BeginAPTArchiveRestore(_ context.Context, plan APTArchiveRestorePlan) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.aptArchiveRestores[plan.ID]; exists {
		return ErrIdempotencyConflict
	}
	for _, old := range s.aptArchiveRestores {
		if old.SnapshotID == plan.Snapshot.ID && old.RepositoryID != plan.Snapshot.RepositoryID {
			return ErrIdempotencyConflict
		}
	}
	reserved, _, err := checkAPTArchiveRestore(plan, s.aptArchiveStateLocked(plan))
	if err != nil {
		return err
	}
	for id, r := range s.aptArchiveRestores {
		if r.SnapshotID == plan.Snapshot.ID && r.State == "preparing" {
			r.State = "failed"
			r.ReservedBytes = 0
			s.aptArchiveRestores[id] = r
		}
	}
	s.aptArchiveRestores[plan.ID] = APTArchiveRestore{ID: plan.ID, RepositoryID: plan.Snapshot.RepositoryID, SnapshotID: plan.Snapshot.ID, PlanDigest: aptArchivePlanDigest(plan), State: "preparing", ReservedBytes: reserved, CreatedAt: time.Now().UTC()}
	intents := make(map[string]APTArchiveObjectIntent)
	for _, item := range aptArchiveObjectIntents(plan) {
		intents[item.ObjectKey] = item
	}
	s.aptArchiveObjects[plan.ID] = intents
	return nil
}

func (s *MemoryStore) CommitAPTArchiveRestore(_ context.Context, plan APTArchiveRestorePlan, release []byte, audit AuditRecord) (APTRepositorySnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	attempt, ok := s.aptArchiveRestores[plan.ID]
	if !ok || attempt.State != "preparing" || attempt.PlanDigest != aptArchivePlanDigest(plan) {
		return APTRepositorySnapshot{}, ErrVersionConflict
	}
	snapshot := plan.Snapshot
	snapshot.State = APTRepositorySnapshotVisible
	if !validAPTSnapshotPublication(snapshot, plan.Assets, release) {
		return APTRepositorySnapshot{}, ErrDisabled
	}
	_, replay, err := checkAPTArchiveRestore(plan, s.aptArchiveStateLocked(plan))
	if err != nil {
		return APTRepositorySnapshot{}, err
	}
	if !replay {
		members := make([]APTSnapshotPackage, 0, len(plan.Packages))
		for _, p := range plan.Packages {
			revision := p.Revision
			identity := aptPackageIdentityKey(snapshot.RepositoryID, revision.CanonicalIdentity)
			if id := s.aptPackageIdentities[identity]; id != "" {
				revision = s.aptPackageRevisions[id]
			} else {
				s.aptPackageRevisions[revision.ID] = revision
				s.aptPackageIdentities[identity] = revision.ID
			}
			sessionID := uuid.NewString()
			s.aptPublicationSessions[sessionID] = APTPublicationSession{ID: sessionID, RepositoryID: snapshot.RepositoryID, Suite: snapshot.Suite, Component: p.Component, Publisher: revision.Publisher, ObjectName: revision.ObjectName, DeclaredDigest: revision.Digest, DeclaredSize: revision.Size, ExpectedIdentity: revision.CanonicalIdentity, ObjectKey: revision.ObjectKey, PackageRevisionID: revision.ID, State: APTPublicationSessionStaged, CreatedAt: revision.CreatedAt, ExpiresAt: time.Now().UTC().Add(24 * time.Hour)}
			members = append(members, APTSnapshotPackage{SnapshotID: snapshot.ID, PublicationSessionID: sessionID, PackageRevisionID: revision.ID, Component: p.Component, Architecture: revision.Architecture})
		}
		for id, old := range s.aptSnapshots {
			if old.RepositoryID == snapshot.RepositoryID && old.Suite == snapshot.Suite && old.State == APTRepositorySnapshotVisible {
				old.State = APTRepositorySnapshotRetired
				s.aptRetiredAt[id] = time.Now().UTC()
				s.aptSnapshots[id] = old
			}
		}
		s.aptSnapshotPackages[snapshot.ID] = members
		s.aptSnapshots[snapshot.ID] = snapshot
		s.aptSnapshotAssets[snapshot.ID] = append([]APTSnapshotAsset(nil), plan.Assets...)
		if s.aptPoolPaths[snapshot.RepositoryID] == nil {
			s.aptPoolPaths[snapshot.RepositoryID] = make(map[string]APTSnapshotAsset)
		}
		for _, a := range plan.Assets {
			if strings.HasPrefix(a.Path, "pool/") {
				s.aptPoolPaths[snapshot.RepositoryID][a.Path] = a
			}
		}
	} else {
		snapshot = s.aptSnapshots[snapshot.ID]
	}
	attempt.State = "completed"
	attempt.ReservedBytes = 0
	s.aptArchiveRestores[plan.ID] = attempt
	s.appendAuditLocked(audit)
	return snapshot, nil
}

func (s *MemoryStore) FailAPTArchiveRestore(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.aptArchiveRestores[id]
	if !ok {
		return ErrNotFound
	}
	if r.State == "completed" {
		return ErrVersionConflict
	}
	r.State = "failed"
	r.ReservedBytes = 0
	s.aptArchiveRestores[id] = r
	return nil
}

func (s *MemoryStore) ExpireAPTArchiveRestores(ctx context.Context, before time.Time, limit int) error {
	if limit <= 0 {
		limit = 100
	}
	s.mu.RLock()
	var items []APTArchiveRestore
	for _, r := range s.aptArchiveRestores {
		if r.State == "preparing" && r.CreatedAt.Before(before) {
			items = append(items, r)
		}
	}
	s.mu.RUnlock()
	slices.SortFunc(items, func(a, b APTArchiveRestore) int { return strings.Compare(a.ID, b.ID) })
	for _, r := range items[:min(len(items), limit)] {
		release, err := s.LockAPTObject(ctx, "snapshot-lock/"+r.SnapshotID)
		if err != nil {
			return err
		}
		s.mu.Lock()
		current := s.aptArchiveRestores[r.ID]
		if current.State == "preparing" && current.CreatedAt.Before(before) {
			current.State = "failed"
			current.ReservedBytes = 0
			s.aptArchiveRestores[r.ID] = current
		}
		s.mu.Unlock()
		release()
	}
	return nil
}

func (s *MemoryStore) ListUnscheduledAPTArchiveObjects(_ context.Context, limit int) ([]APTArchiveObjectIntent, error) {
	if limit <= 0 {
		limit = 100
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []APTArchiveObjectIntent
	for id, items := range s.aptArchiveObjects {
		if s.aptArchiveRestores[id].State == "failed" {
			for _, item := range items {
				if item.ScheduledAt.IsZero() && item.CollectedAt.IsZero() {
					result = append(result, item)
				}
			}
		}
	}
	slices.SortFunc(result, func(a, b APTArchiveObjectIntent) int {
		return strings.Compare(a.RestoreID+"\x00"+a.ObjectKey, b.RestoreID+"\x00"+b.ObjectKey)
	})
	return result[:min(len(result), limit)], nil
}

func (s *MemoryStore) MarkAPTArchiveObjectScheduled(_ context.Context, id, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.aptArchiveObjects[id][key]
	if !ok || s.aptArchiveRestores[id].State != "failed" || !item.CollectedAt.IsZero() {
		return ErrVersionConflict
	}
	if item.ScheduledAt.IsZero() {
		item.ScheduledAt = time.Now().UTC()
	}
	s.aptArchiveObjects[id][key] = item
	return nil
}
func (s *MemoryStore) MarkAPTArchiveObjectCollected(_ context.Context, id, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.aptArchiveObjects[id][key]
	if !ok || s.aptArchiveRestores[id].State != "failed" {
		return ErrVersionConflict
	}
	if item.CollectedAt.IsZero() {
		item.CollectedAt = time.Now().UTC()
	}
	s.aptArchiveObjects[id][key] = item
	return nil
}

func (s *MemoryStore) aptArchiveReservedBytesLocked(repositoryID string) int64 {
	var total int64
	for _, r := range s.aptArchiveRestores {
		if r.RepositoryID == repositoryID && r.State == "preparing" {
			total += r.ReservedBytes
		}
	}
	return total
}
