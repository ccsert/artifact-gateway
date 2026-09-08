package repository

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
)

// APTArchiveRestorePlan is validated signed content, not a partially staged
// publication. Only CommitAPTArchiveRestore may expose its package metadata.
type APTArchiveRestorePlan struct {
	ID            string
	ArchiveDigest string
	Snapshot      APTRepositorySnapshot
	Packages      []APTArchivePackage
	Assets        []APTSnapshotAsset
}

type APTArchivePackage struct {
	Component string
	Revision  APTPackageRevision
}

type APTArchiveRestore struct {
	ID, RepositoryID, SnapshotID, PlanDigest, State string
	ReservedBytes                                   int64
	CreatedAt                                       time.Time
}

type APTArchiveObjectIntent struct {
	RestoreID, RepositoryID, ObjectKey, Digest string
	Size                                       int64
	ScheduledAt, CollectedAt                   time.Time
}

type NativeAPTArchiveRestoreStore interface {
	// The caller holds snapshot-lock/<snapshot ID> through begin, object writes,
	// and commit. Writers and reclaimers coordinate each object through its lock.
	BeginAPTArchiveRestore(context.Context, APTArchiveRestorePlan) error
	CommitAPTArchiveRestore(context.Context, APTArchiveRestorePlan, []byte, AuditRecord) (APTRepositorySnapshot, error)
	FailAPTArchiveRestore(context.Context, string) error
	ExpireAPTArchiveRestores(context.Context, time.Time, int) error
	ListUnscheduledAPTArchiveObjects(context.Context, int) ([]APTArchiveObjectIntent, error)
	MarkAPTArchiveObjectScheduled(context.Context, string, string) error
	MarkAPTArchiveObjectCollected(context.Context, string, string) error
}

type aptArchiveRestoreState struct {
	deletions                       []APTPackageDeletion
	repo                            HostedRepository
	quota, baseBytes, reservedBytes int64
	packages                        map[string]APTPackageRevision
	snapshots                       map[string]APTRepositorySnapshot
	assets                          map[string][]APTSnapshotAsset
	members                         []APTArchivePackage
	pool                            map[string]APTSnapshotAsset
}

func aptArchivePlanDigest(plan APTArchiveRestorePlan) string {
	body, _ := json.Marshal(plan)
	sum := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func validAPTArchivePlan(plan APTArchiveRestorePlan) bool {
	if _, err := uuid.Parse(plan.ID); err != nil {
		return false
	}
	if _, err := uuid.Parse(plan.Snapshot.ID); err != nil {
		return false
	}
	if !ValidAPTSHA256Digest(plan.ArchiveDigest) || len(plan.Packages) > 10000 || len(plan.Assets) > 50016 || plan.Snapshot.CreatedAt.IsZero() || plan.Snapshot.PublishedAt.IsZero() {
		return false
	}
	if plan.Snapshot.State != APTRepositorySnapshotVisible && plan.Snapshot.State != APTRepositorySnapshotRetired {
		return false
	}
	seen := make(map[string]bool)
	expectedPool := make(map[string]APTPackageRevision)
	for _, item := range plan.Packages {
		r := item.Revision
		if !ValidAPTPublicationScope(item.Component) || !validAPTPackageRevision(r) || r.RepositoryID != plan.Snapshot.RepositoryID || r.CreatedAt.IsZero() {
			return false
		}
		key := item.Component + "\x00" + r.CanonicalIdentity
		if seen[key] {
			return false
		}
		seen[key] = true
		path := APTPoolPath(item.Component, r.Package, r.ObjectName)
		if _, ok := expectedPool[path]; ok {
			return false
		}
		expectedPool[path] = r
	}
	count := 0
	for _, a := range plan.Assets {
		if strings.HasPrefix(a.Path, "pool/") {
			r, ok := expectedPool[a.Path]
			if !ok || a.Digest != r.Digest || a.Size != r.Size || a.ObjectKey != r.ObjectKey {
				return false
			}
			count++
		}
	}
	return count == len(expectedPool) && validAPTSnapshotAssets(plan.Snapshot, plan.Assets)
}

func sameAPTArchiveRevision(a, b APTPackageRevision) bool {
	a.ID, b.ID = "", ""
	if !a.CreatedAt.Equal(b.CreatedAt) {
		return false
	}
	a.CreatedAt, b.CreatedAt = time.Time{}, time.Time{}
	return a == b
}

func sameAPTArchiveSnapshot(a, b APTRepositorySnapshot) bool {
	a.State, b.State = "", ""
	if !a.CreatedAt.Equal(b.CreatedAt) || !a.PublishedAt.Equal(b.PublishedAt) {
		return false
	}
	a.CreatedAt, b.CreatedAt = time.Time{}, time.Time{}
	a.PublishedAt, b.PublishedAt = time.Time{}, time.Time{}
	return a == b
}

func checkAPTArchiveRestore(plan APTArchiveRestorePlan, state aptArchiveRestoreState) (reserved int64, replay bool, err error) {
	if !validAPTArchivePlan(plan) {
		return 0, false, ErrDisabled
	}
	if state.repo.ID != plan.Snapshot.RepositoryID || state.repo.State != RepositoryActive || state.repo.Format != FormatAPT || state.repo.Type != RepositoryTypeHosted {
		return 0, false, ErrNotFound
	}
	if existing, ok := state.snapshots[plan.Snapshot.ID]; ok {
		if (existing.State != APTRepositorySnapshotVisible && existing.State != APTRepositorySnapshotRetired) || !sameAPTArchiveSnapshot(existing, plan.Snapshot) {
			return 0, false, ErrIdempotencyConflict
		}
		if len(state.members) != len(plan.Packages) || len(state.assets[existing.ID]) != len(plan.Assets) {
			return 0, false, ErrIdempotencyConflict
		}
		members := make(map[string]APTPackageRevision)
		for _, p := range state.members {
			members[p.Component+"\x00"+p.Revision.CanonicalIdentity] = p.Revision
		}
		for _, p := range plan.Packages {
			old, ok := members[p.Component+"\x00"+p.Revision.CanonicalIdentity]
			if !ok || !sameAPTArchiveRevision(old, p.Revision) {
				return 0, false, ErrIdempotencyConflict
			}
		}
		assets := make(map[string]APTSnapshotAsset)
		for _, a := range state.assets[existing.ID] {
			assets[a.Path] = a
		}
		for _, a := range plan.Assets {
			if assets[a.Path] != a {
				return 0, false, ErrIdempotencyConflict
			}
		}
		return 0, true, nil
	}
	for _, p := range plan.Packages {
		for _, d := range state.deletions {
			if aptDeletionBlocks(d, plan.Snapshot.Suite, p.Component, p.Revision.CanonicalIdentity) {
				return 0, false, ErrVersionConflict
			}
		}
	}
	for _, existing := range state.snapshots {
		if existing.RepositoryID == plan.Snapshot.RepositoryID && existing.Suite == plan.Snapshot.Suite && (existing.Sequence == plan.Snapshot.Sequence || (existing.State == APTRepositorySnapshotVisible && existing.Sequence >= plan.Snapshot.Sequence)) {
			return 0, false, ErrVersionConflict
		}
	}
	var newPackages int64
	seen := make(map[string]APTPackageRevision)
	for _, p := range plan.Packages {
		r := p.Revision
		if previous, ok := seen[r.CanonicalIdentity]; ok {
			if !sameAPTArchiveRevision(previous, r) {
				return 0, false, ErrAPTPackageConflict
			}
			continue
		}
		seen[r.CanonicalIdentity] = r
		if existing, ok := state.packages[r.CanonicalIdentity]; ok {
			if !sameAPTArchiveRevision(existing, r) {
				return 0, false, ErrAPTPackageConflict
			}
		} else {
			newPackages += r.Size
		}
	}
	for _, a := range plan.Assets {
		if old, ok := state.pool[a.Path]; ok && (old.Digest != a.Digest || old.Size != a.Size || old.ContentType != a.ContentType) {
			return 0, false, ErrAPTPackageConflict
		}
	}
	currentGenerated := make(map[string]int64)
	futureGenerated := aptSnapshotGeneratedObjects(plan.Assets)
	for id, assets := range state.assets {
		snapshot := state.snapshots[id]
		if snapshot.RepositoryID != plan.Snapshot.RepositoryID || snapshot.State != APTRepositorySnapshotVisible {
			continue
		}
		for key, size := range aptSnapshotGeneratedObjects(assets) {
			currentGenerated[key] = size
			if snapshot.Suite != plan.Snapshot.Suite {
				futureGenerated[key] = size
			}
		}
	}
	var currentBytes, futureBytes int64
	for _, size := range currentGenerated {
		currentBytes += size
	}
	for _, size := range futureGenerated {
		futureBytes += size
	}
	future := state.baseBytes + state.reservedBytes + newPackages + futureBytes
	if state.quota > 0 && future > state.quota {
		return 0, false, ErrQuotaExceeded
	}
	return max(int64(0), newPackages+futureBytes-currentBytes), false, nil
}

func aptArchiveObjectIntents(plan APTArchiveRestorePlan) []APTArchiveObjectIntent {
	objects := make(map[string]APTArchiveObjectIntent)
	for _, a := range plan.Assets {
		objects[a.ObjectKey] = APTArchiveObjectIntent{RestoreID: plan.ID, RepositoryID: a.RepositoryID, ObjectKey: a.ObjectKey, Digest: a.Digest, Size: a.Size}
	}
	result := make([]APTArchiveObjectIntent, 0, len(objects))
	for _, o := range objects {
		result = append(result, o)
	}
	slices.SortFunc(result, func(a, b APTArchiveObjectIntent) int { return strings.Compare(a.ObjectKey, b.ObjectKey) })
	return result
}
