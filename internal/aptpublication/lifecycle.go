package aptpublication

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"slices"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

// LifecycleRequest always names the reviewed base snapshot. Retention selects
// older uploads, never approximates Debian version ordering with string sorting.
type LifecycleRequest struct {
	Suite                 string   `json:"suite"`
	ExpectedSnapshotID    string   `json:"expectedSnapshotId"`
	Action                string   `json:"action"`
	PublicationSessionIDs []string `json:"publicationSessionIds,omitempty"`
	DeletionIDs           []string `json:"deletionIds,omitempty"`
	KeepLatest            int      `json:"keepLatest,omitempty"`
	OlderThanDays         int      `json:"olderThanDays,omitempty"`
}
type LifecyclePlan struct {
	ExpectedSnapshotID string   `json:"expectedSnapshotId"`
	RemoveSessionIDs   []string `json:"removeSessionIds"`
	RestoreIDs         []string `json:"restoreIds"`
	RemainingPackages  int      `json:"remainingPackages"`
	RecoveryDays       int      `json:"recoveryDays"`
}

type Lifecycle struct{ Publisher *Publisher }

func validLifecycleRequest(r LifecycleRequest) bool {
	if !repository.ValidAPTPublicationScope(r.Suite) {
		return false
	}
	if _, err := uuid.Parse(r.ExpectedSnapshotID); err != nil {
		return false
	}
	for _, ids := range [][]string{r.PublicationSessionIDs, r.DeletionIDs} {
		if len(ids) > 10000 {
			return false
		}
		seen := make(map[string]bool)
		for _, id := range ids {
			if _, err := uuid.Parse(id); err != nil || seen[id] {
				return false
			}
			seen[id] = true
		}
	}
	switch r.Action {
	case "delete":
		return len(r.PublicationSessionIDs) > 0 && len(r.DeletionIDs) == 0 && r.KeepLatest == 0 && r.OlderThanDays == 0
	case "restore":
		return len(r.DeletionIDs) > 0 && len(r.PublicationSessionIDs) == 0 && r.KeepLatest == 0 && r.OlderThanDays == 0
	case "retention":
		return len(r.DeletionIDs) == 0 && r.KeepLatest >= 1 && r.KeepLatest <= 10000 && r.OlderThanDays >= 1 && r.OlderThanDays <= 36500
	default:
		return false
	}
}

func PreviewLifecycle(ctx context.Context, store repository.NativeAPTPublicationStore, repoID string, r LifecycleRequest) (LifecyclePlan, error) {
	plan, _, err := planAPTLifecycle(ctx, store, repoID, r, time.Now().UTC())
	return plan, err
}

func planAPTLifecycle(ctx context.Context, store repository.NativeAPTPublicationStore, repoID string, r LifecycleRequest, now time.Time) (LifecyclePlan, []string, error) {
	plan := LifecyclePlan{ExpectedSnapshotID: r.ExpectedSnapshotID, RemoveSessionIDs: []string{}, RestoreIDs: []string{}, RecoveryDays: 7}
	if store == nil || !validLifecycleRequest(r) {
		return plan, nil, ErrInvalidSnapshotInput
	}
	base, before, err := store.GetAPTRepositorySnapshot(ctx, r.ExpectedSnapshotID)
	if err != nil {
		return plan, nil, err
	}
	if base.RepositoryID != repoID || base.Suite != r.Suite || base.State != repository.APTRepositorySnapshotVisible {
		return plan, nil, repository.ErrVersionConflict
	}
	sessions := make(map[string]repository.APTSnapshotPackage, len(before))
	for _, m := range before {
		sessions[m.PublicationSessionID] = m
	}
	switch r.Action {
	case "delete":
		plan.RemoveSessionIDs = slices.Clone(r.PublicationSessionIDs)
	case "retention":
		type candidate struct {
			id       string
			revision repository.APTPackageRevision
		}
		groups := make(map[string][]candidate)
		for _, m := range before {
			revision, e := store.GetAPTPackageRevisionForSession(ctx, m.PublicationSessionID)
			if e != nil {
				return plan, nil, e
			}
			key := m.Component + "\x00" + revision.Package + "\x00" + revision.Architecture
			groups[key] = append(groups[key], candidate{id: m.PublicationSessionID, revision: revision})
		}
		for _, group := range groups {
			slices.SortFunc(group, func(a, b candidate) int {
				if c := b.revision.CreatedAt.Compare(a.revision.CreatedAt); c != 0 {
					return c
				}
				if a.revision.CanonicalIdentity < b.revision.CanonicalIdentity {
					return -1
				}
				return 1
			})
			for i, c := range group {
				if i >= r.KeepLatest && c.revision.CreatedAt.Before(now.Add(-time.Duration(r.OlderThanDays)*24*time.Hour)) {
					plan.RemoveSessionIDs = append(plan.RemoveSessionIDs, c.id)
				}
			}
		}
	case "restore":
		deletions, e := store.ListAPTPackageDeletions(ctx, repoID, r.Suite)
		if e != nil {
			return plan, nil, e
		}
		for _, id := range r.DeletionIDs {
			found := false
			for _, d := range deletions {
				if d.ID == id {
					if !d.RestoredAt.IsZero() || !d.PurgedAt.IsZero() || !now.Before(d.RestoreUntil) {
						return plan, nil, repository.ErrVersionConflict
					}
					if _, ok := sessions[d.SessionID]; ok {
						return plan, nil, repository.ErrVersionConflict
					}
					sessions[d.SessionID] = repository.APTSnapshotPackage{PublicationSessionID: d.SessionID}
					found = true
				}
			}
			if !found {
				return plan, nil, repository.ErrNotFound
			}
		}
		plan.RestoreIDs = slices.Clone(r.DeletionIDs)
	}
	for _, id := range plan.RemoveSessionIDs {
		if _, ok := sessions[id]; !ok {
			return plan, nil, repository.ErrVersionConflict
		}
		delete(sessions, id)
	}
	ids := make([]string, 0, len(sessions))
	for id := range sessions {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	slices.Sort(plan.RemoveSessionIDs)
	slices.Sort(plan.RestoreIDs)
	plan.RemainingPackages = len(ids)
	return plan, ids, nil
}

func (l Lifecycle) Apply(ctx context.Context, repoID, actor, key string, r LifecycleRequest) (repository.APTRepositorySnapshot, error) {
	if l.Publisher == nil || actor == "" || len(actor) > 512 || len(key) == 0 || len(key) > 128 || !validLifecycleRequest(r) {
		return repository.APTRepositorySnapshot{}, ErrInvalidSnapshotInput
	}
	r.PublicationSessionIDs = slices.Clone(r.PublicationSessionIDs)
	r.DeletionIDs = slices.Clone(r.DeletionIDs)
	slices.Sort(r.PublicationSessionIDs)
	slices.Sort(r.DeletionIDs)
	body, _ := json.Marshal(r)
	digest := digestBytes(body)
	commandID := uuid.NewSHA1(uuid.NameSpaceURL, []byte(repoID+"\x00apt-lifecycle\x00"+actor+"\x00"+key)).String()
	p := l.Publisher
	locked, unlock, err := repository.LockObjectKeys(ctx, []string{"apt-lifecycle/" + repoID + "/" + r.Suite, "apt-lifecycle-command/" + commandID}, p.store, repository.FormatAPT, p.store.LockAPTObject)
	if err != nil {
		return repository.APTRepositorySnapshot{}, err
	}
	defer unlock()
	result, err := p.store.GetAPTLifecycleResult(locked, commandID)
	if err == nil {
		if result.RequestDigest != digest {
			return repository.APTRepositorySnapshot{}, repository.ErrIdempotencyConflict
		}
		snapshot, _, e := p.store.GetAPTRepositorySnapshot(locked, result.SnapshotID)
		return snapshot, e
	}
	if !errors.Is(err, repository.ErrNotFound) {
		return repository.APTRepositorySnapshot{}, err
	}
	now := time.Now().UTC()
	plan, sessions, err := planAPTLifecycle(locked, p.store, repoID, r, now)
	if err != nil {
		return repository.APTRepositorySnapshot{}, err
	}
	if r.Action == "retention" && !slices.Equal(r.PublicationSessionIDs, plan.RemoveSessionIDs) {
		return repository.APTRepositorySnapshot{}, repository.ErrVersionConflict
	}
	if len(plan.RemoveSessionIDs)+len(plan.RestoreIDs) == 0 {
		return repository.APTRepositorySnapshot{}, repository.ErrVersionConflict
	}
	history, err := p.store.ListAPTRepositorySnapshots(locked, repoID, r.Suite)
	if err != nil {
		return repository.APTRepositorySnapshot{}, err
	}
	var sequence int64
	for _, h := range history {
		if h.Snapshot.Sequence > sequence {
			sequence = h.Snapshot.Sequence
		}
	}
	if sequence == math.MaxInt64 {
		return repository.APTRepositorySnapshot{}, repository.ErrVersionConflict
	}
	assets, err := p.store.ListAPTSnapshotAssets(locked, r.ExpectedSnapshotID)
	if err != nil {
		return repository.APTRepositorySnapshot{}, err
	}
	attemptID := uuid.NewString()
	snapshot, err := p.Publish(locked, PublishSnapshotInput{ID: attemptID, RepositoryID: repoID, Suite: r.Suite, Sequence: sequence + 1, SessionIDs: sessions, Actor: actor, CreatedAt: now, indexScopes: snapshotIndexScopes(r.Suite, assets), lifecycle: &repository.APTLifecycleCommit{ID: commandID, RequestDigest: digest, BaseSnapshotID: r.ExpectedSnapshotID, Operation: r.Action, RemoveSessionIDs: plan.RemoveSessionIDs, RestoreIDs: plan.RestoreIDs, Now: now}})
	if err != nil {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(locked), 5*time.Second)
		defer cancel()
		_ = p.store.FailAPTRepositorySnapshot(cleanup, attemptID)
	}
	return snapshot, err
}
