package repository

import (
	"context"
	"maps"
	"slices"
	"time"

	"github.com/google/uuid"
)

func (s *MemoryStore) ListAPTRepositorySnapshots(_ context.Context, repoID, suite string) ([]APTSnapshotHistory, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]APTSnapshotHistory, 0)
	for _, snapshot := range s.aptSnapshots {
		if snapshot.RepositoryID == repoID && snapshot.Suite == suite {
			out = append(out, APTSnapshotHistory{Snapshot: snapshot, RetiredAt: s.aptRetiredAt[snapshot.ID]})
		}
	}
	sortAPTSnapshotHistory(out)
	return out, nil
}
func (s *MemoryStore) aptDeletionsLocked(repoID, suite string) []APTPackageDeletion {
	out := make([]APTPackageDeletion, 0)
	for _, d := range s.aptDeletions {
		if d.RepositoryID == repoID && d.Suite == suite {
			out = append(out, d)
		}
	}
	slices.SortFunc(out, func(a, b APTPackageDeletion) int {
		if c := a.DeletedAt.Compare(b.DeletedAt); c != 0 {
			return c
		}
		if a.ID < b.ID {
			return -1
		}
		if a.ID > b.ID {
			return 1
		}
		return 0
	})
	return out
}
func (s *MemoryStore) ListAPTPackageDeletions(_ context.Context, repoID, suite string) ([]APTPackageDeletion, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.aptDeletionsLocked(repoID, suite), nil
}
func (s *MemoryStore) GetAPTLifecycleResult(_ context.Context, id string) (APTLifecycleResult, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.aptLifecycleResults[id]
	if !ok {
		return r, ErrNotFound
	}
	return r, nil
}

func (s *MemoryStore) CommitAPTLifecycleSnapshot(_ context.Context, plan APTLifecycleCommit, snapshot APTRepositorySnapshot, assets []APTSnapshotAsset, release []byte, audit AuditRecord) (APTRepositorySnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.aptLifecycleResults[plan.ID]; ok {
		return APTRepositorySnapshot{}, ErrIdempotencyConflict
	}
	repo, ok := s.hostedRepositories[snapshot.RepositoryID]
	if !ok || repo.Format != FormatAPT || repo.Type != RepositoryTypeHosted || repo.State != RepositoryActive {
		return APTRepositorySnapshot{}, ErrNotFound
	}
	before := s.aptSnapshotPackages[plan.BaseSnapshotID]
	if err := validateAPTLifecycleCommit(plan, s.aptSnapshots[plan.BaseSnapshotID], snapshot, before, s.aptSnapshotPackages[snapshot.ID], s.aptDeletionsLocked(snapshot.RepositoryID, snapshot.Suite)); err != nil {
		return APTRepositorySnapshot{}, err
	}
	previous := s.aptDeletions
	s.aptDeletions = maps.Clone(previous)
	for _, id := range plan.RestoreIDs {
		d := s.aptDeletions[id]
		d.RestoredAt = plan.Now
		s.aptDeletions[id] = d
	}
	result, err := s.publishAPTRepositorySnapshotLocked(snapshot, assets, release, audit)
	if err != nil {
		s.aptDeletions = previous
		return APTRepositorySnapshot{}, err
	}
	for _, id := range plan.RemoveSessionIDs {
		for _, m := range before {
			if m.PublicationSessionID == id {
				d := APTPackageDeletion{ID: uuid.NewSHA1(uuid.MustParse(plan.ID), []byte(id)).String(), RepositoryID: snapshot.RepositoryID, Suite: snapshot.Suite, Component: m.Component, SessionID: id, Revision: s.aptPackageRevisions[m.PackageRevisionID], DeletedAt: plan.Now, RestoreUntil: plan.Now.Add(APTPackageRecoveryPeriod)}
				s.aptDeletions[d.ID] = d
			}
		}
	}
	s.aptLifecycleResults[plan.ID] = APTLifecycleResult{RequestDigest: plan.RequestDigest, SnapshotID: result.ID}
	return result, nil
}

func (s *MemoryStore) PruneAPTSnapshots(_ context.Context, repoID string, ids []string, now time.Time, audit AuditRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(ids) > 100 || now.IsZero() {
		return ErrDisabled
	}
	repo, ok := s.hostedRepositories[repoID]
	if !ok || repo.Format != FormatAPT || repo.Type != RepositoryTypeHosted || repo.State != RepositoryActive {
		return ErrNotFound
	}
	for _, id := range ids {
		snapshot, ok := s.aptSnapshots[id]
		if !ok || snapshot.RepositoryID != repoID {
			return ErrNotFound
		}
		if snapshot.State == APTRepositorySnapshotPruned {
			continue
		}
		retired := s.aptRetiredAt[id]
		if snapshot.State != APTRepositorySnapshotRetired || retired.IsZero() || now.Before(retired.Add(APTSnapshotGracePeriod)) {
			return ErrVersionConflict
		}
	}
	for _, id := range ids {
		snapshot := s.aptSnapshots[id]
		if snapshot.State == APTRepositorySnapshotPruned {
			continue
		}
		intents := s.aptSnapshotObjects[id]
		if intents == nil {
			intents = make(map[string]APTSnapshotObjectIntent)
		}
		for _, a := range s.aptSnapshotAssets[id] {
			if _, ok := intents[a.ObjectKey]; !ok {
				intents[a.ObjectKey] = APTSnapshotObjectIntent{SnapshotID: id, RepositoryID: repoID, ObjectKey: a.ObjectKey, Digest: a.Digest, Size: a.Size, CreatedAt: now}
			}
		}
		s.aptSnapshotObjects[id] = intents
		snapshot.State = APTRepositorySnapshotPruned
		s.aptSnapshots[id] = snapshot
		delete(s.aptSnapshotAssets, id)
		delete(s.aptSnapshotPackages, id)
	}
	// A deletion may outlive snapshot grace. Re-running prune (even with no IDs)
	// releases expired revisions only when every live build/view has let go.
	for id, d := range s.aptDeletions {
		if d.RepositoryID != repoID || !d.RestoredAt.IsZero() || !d.PurgedAt.IsZero() || now.Before(d.RestoreUntil) {
			continue
		}
		referenced := false
		for snapshotID, members := range s.aptSnapshotPackages {
			if aptSnapshotCollectible(s.aptSnapshots[snapshotID].State) {
				continue
			}
			for _, m := range members {
				if m.PackageRevisionID == d.Revision.ID {
					referenced = true
				}
			}
		}
		for _, other := range s.aptDeletions {
			if other.Revision.ID == d.Revision.ID && other.RestoredAt.IsZero() && other.PurgedAt.IsZero() && now.Before(other.RestoreUntil) {
				referenced = true
			}
		}
		for _, session := range s.aptPublicationSessions {
			if session.PackageRevisionID == d.Revision.ID && now.Before(session.CreatedAt.Add(APTSnapshotGracePeriod)) {
				referenced = true
			}
		}
		if referenced {
			continue
		}
		for snapshotID, members := range s.aptSnapshotPackages {
			if aptSnapshotCollectible(s.aptSnapshots[snapshotID].State) {
				s.aptSnapshotPackages[snapshotID] = slices.DeleteFunc(members, func(m APTSnapshotPackage) bool { return m.PackageRevisionID == d.Revision.ID })
			}
		}
		for sessionID, session := range s.aptPublicationSessions {
			if session.PackageRevisionID == d.Revision.ID {
				delete(s.aptPublicationSessions, sessionID)
				for key, record := range s.aptPublicationKeys {
					if record.sessionID == sessionID {
						delete(s.aptPublicationKeys, key)
					}
				}
			}
		}
		delete(s.aptPackageRevisions, d.Revision.ID)
		delete(s.aptPackageIdentities, aptPackageIdentityKey(repoID, d.Revision.CanonicalIdentity))
		d.PurgedAt = now
		s.aptDeletions[id] = d
	}
	s.appendAuditLocked(audit)
	return nil
}

func (s *MemoryStore) ListUnscheduledAPTDeletionObjects(_ context.Context, limit int) ([]APTPackageDeletion, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if limit <= 0 {
		limit = 100
	}
	out := make([]APTPackageDeletion, 0)
	for _, d := range s.aptDeletions {
		if !d.PurgedAt.IsZero() && d.ScheduledAt.IsZero() && d.CollectedAt.IsZero() {
			out = append(out, d)
		}
	}
	slices.SortFunc(out, func(a, b APTPackageDeletion) int {
		if a.ID < b.ID {
			return -1
		}
		return 1
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
func (s *MemoryStore) MarkAPTDeletionObjectScheduled(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.aptDeletions[id]
	if !ok {
		return ErrNotFound
	}
	if d.PurgedAt.IsZero() {
		return ErrVersionConflict
	}
	if d.ScheduledAt.IsZero() {
		d.ScheduledAt = time.Now().UTC()
		s.aptDeletions[id] = d
	}
	return nil
}
func (s *MemoryStore) MarkAPTDeletionObjectCollected(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.aptDeletions[id]
	if !ok {
		return ErrNotFound
	}
	if d.PurgedAt.IsZero() {
		return ErrVersionConflict
	}
	if d.CollectedAt.IsZero() {
		d.CollectedAt = time.Now().UTC()
		s.aptDeletions[id] = d
	}
	return nil
}
