package repository

import (
	"context"
	"slices"
	"time"

	"github.com/google/uuid"
)

const aptEmptyObjectDigest = "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

const APTSnapshotGracePeriod = 24 * time.Hour
const APTPackageRecoveryPeriod = 7 * 24 * time.Hour

// Deletions are independent of immutable snapshot membership. A retired view
// continues to describe exactly what its signed Release published.
type APTPackageDeletion struct {
	ID           string             `json:"id"`
	RepositoryID string             `json:"repositoryId"`
	Suite        string             `json:"suite"`
	Component    string             `json:"component"`
	SessionID    string             `json:"publicationSessionId"`
	Revision     APTPackageRevision `json:"revision"`
	DeletedAt    time.Time          `json:"deletedAt"`
	RestoreUntil time.Time          `json:"restoreUntil"`
	RestoredAt   time.Time          `json:"restoredAt,omitempty"`
	PurgedAt     time.Time          `json:"purgedAt,omitempty"`
	ScheduledAt  time.Time          `json:"-"`
	CollectedAt  time.Time          `json:"-"`
}

type APTSnapshotHistory struct {
	Snapshot  APTRepositorySnapshot
	RetiredAt time.Time
}

type APTLifecycleCommit struct {
	ID, RequestDigest, BaseSnapshotID, Operation string
	RemoveSessionIDs, RestoreIDs                 []string
	Now                                          time.Time
}

type APTLifecycleResult struct{ RequestDigest, SnapshotID string }

type NativeAPTLifecycleStore interface {
	ListAPTRepositorySnapshots(context.Context, string, string) ([]APTSnapshotHistory, error)
	ListAPTPackageDeletions(context.Context, string, string) ([]APTPackageDeletion, error)
	GetAPTLifecycleResult(context.Context, string) (APTLifecycleResult, error)
	CommitAPTLifecycleSnapshot(context.Context, APTLifecycleCommit, APTRepositorySnapshot, []APTSnapshotAsset, []byte, AuditRecord) (APTRepositorySnapshot, error)
	PruneAPTSnapshots(context.Context, string, []string, time.Time, AuditRecord) error
	ListUnscheduledAPTDeletionObjects(context.Context, int) ([]APTPackageDeletion, error)
	MarkAPTDeletionObjectScheduled(context.Context, string) error
	MarkAPTDeletionObjectCollected(context.Context, string) error
}

func aptDeletionBlocks(d APTPackageDeletion, suite, component, identity string) bool {
	return d.Suite == suite && d.Component == component && d.Revision.CanonicalIdentity == identity && d.RestoredAt.IsZero()
}

// validateAPTLifecycleCommit binds the plan to the current complete membership.
// The repository lock must cover this validation and the visibility switch.
func validateAPTLifecycleCommit(plan APTLifecycleCommit, base, next APTRepositorySnapshot, before, after []APTSnapshotPackage, deletions []APTPackageDeletion) error {
	if _, err := uuid.Parse(plan.ID); err != nil {
		return ErrDisabled
	}
	if plan.ID == "" || !ValidAPTSHA256Digest(plan.RequestDigest) || plan.Now.IsZero() || base.ID != plan.BaseSnapshotID || base.State != APTRepositorySnapshotVisible || base.RepositoryID != next.RepositoryID || base.Suite != next.Suite || next.Sequence <= base.Sequence {
		return ErrVersionConflict
	}
	if plan.Operation != "delete" && plan.Operation != "restore" && plan.Operation != "retention" {
		return ErrDisabled
	}
	if (plan.Operation == "restore" && (len(plan.RemoveSessionIDs) != 0 || len(plan.RestoreIDs) == 0)) || (plan.Operation != "restore" && (len(plan.RestoreIDs) != 0 || len(plan.RemoveSessionIDs) == 0)) {
		return ErrDisabled
	}
	expected := make(map[string]APTSnapshotPackage, len(before))
	for _, m := range before {
		expected[m.PublicationSessionID] = m
	}
	for _, id := range plan.RemoveSessionIDs {
		if _, ok := expected[id]; !ok {
			return ErrVersionConflict
		}
		delete(expected, id)
	}
	for _, id := range plan.RestoreIDs {
		found := false
		for _, d := range deletions {
			if d.ID != id {
				continue
			}
			if !d.RestoredAt.IsZero() || !d.PurgedAt.IsZero() || !plan.Now.Before(d.RestoreUntil) || d.RepositoryID != next.RepositoryID || d.Suite != next.Suite {
				return ErrVersionConflict
			}
			if _, exists := expected[d.SessionID]; exists {
				return ErrVersionConflict
			}
			expected[d.SessionID] = APTSnapshotPackage{PublicationSessionID: d.SessionID, PackageRevisionID: d.Revision.ID, Component: d.Component, Architecture: d.Revision.Architecture}
			found = true
		}
		if !found {
			return ErrNotFound
		}
	}
	if len(after) != len(expected) {
		return ErrVersionConflict
	}
	for _, m := range after {
		want, ok := expected[m.PublicationSessionID]
		want.SnapshotID = m.SnapshotID
		if !ok || want != m {
			return ErrVersionConflict
		}
		delete(expected, m.PublicationSessionID)
	}
	return nil
}

func aptSnapshotCollectible(state APTRepositorySnapshotState) bool {
	return state == APTRepositorySnapshotFailed || state == APTRepositorySnapshotPruned
}
func sortAPTSnapshotHistory(items []APTSnapshotHistory) {
	slices.SortFunc(items, func(a, b APTSnapshotHistory) int {
		if a.Snapshot.Sequence < b.Snapshot.Sequence {
			return -1
		}
		if a.Snapshot.Sequence > b.Snapshot.Sequence {
			return 1
		}
		return 0
	})
}
