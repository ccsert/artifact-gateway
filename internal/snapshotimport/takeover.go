package snapshotimport

import (
	"context"
	"errors"

	"github.com/artifact-gateway/artifact-gateway/internal/objectstore"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

type TakeoverStore interface {
	Store
	repository.MavenSnapshotTakeoverStore
}

// RunTakeover checks the frozen source, every imported target byte and the
// committed checkpoint before a per-coordinate transaction grants publication.
// It writes neither objects nor metadata and creates no publishing session.
func RunTakeover(ctx context.Context, p *Prepared, store TakeoverStore, objects objectstore.Store, repo, target, actor, binding, key string, dryRun bool) (Report, error) {
	r := NewReport(p)
	r.Action = "takeover"
	r.TargetID = target
	r.RepositoryID = repo
	r.TargetBinding = binding
	plans, err := p.References(repo, target, actor, binding)
	if err != nil || key == "" || len(key) > 128 {
		r.Status = "rejected"
		r.Rejected = []Rejection{{Reason: "invalid_takeover_identity"}}
		return r.withCounts(), errors.New("invalid_takeover_identity")
	}
	if err = p.Reverify(ctx); err != nil {
		r.Status = "rejected"
		r.Rejected = []Rejection{{Reason: "source_drift"}}
		return r.withCounts(), err
	}
	// Check every listed GAV before changing the first one.
	for i, plan := range plans {
		v, err := store.CheckMavenSnapshotTakeover(ctx, plan, key)
		if err != nil {
			r.Status = "rejected"
			r.Entries[i].State = "conflict"
			r.Entries[i].Reason = takeoverReason(err)
			return r.withCounts(), err
		}
		r.Entries[i].SessionID = v.SessionID
		for _, a := range plan.Assets {
			if err = verifyTarget(ctx, objects, a); err != nil {
				r.Status = "rejected"
				r.Entries[i].State = "failed"
				r.Entries[i].Reason = "target_bytes_mismatch_or_unavailable"
				return r.withCounts(), err
			}
		}
		r.Entries[i].State = "takeover-ready"
	}
	if dryRun {
		r.Status = "ready"
		return r.withCounts(), nil
	}
	for i, plan := range plans {
		err := func() error {
			ctx, release, err := store.LockMavenSnapshotImport(ctx, repo, plan.Coordinate)
			if err != nil {
				return err
			}
			defer release()
			if _, err = store.CheckMavenSnapshotTakeover(ctx, plan, key); err != nil {
				return err
			}
			for _, a := range plan.Assets {
				if err = verifyTarget(ctx, objects, a); err != nil {
					return err
				}
			}
			_, err = store.TakeoverMavenSnapshotImport(ctx, plan, key)
			return err
		}()
		if err != nil {
			r.Status = "partial"
			r.Entries[i].State = "failed"
			r.Entries[i].Reason = takeoverReason(err)
			return r.withCounts(), err
		}
		r.Entries[i].State = "writable"
	}
	r.Status = "verified"
	return r.withCounts(), nil
}

func takeoverReason(err error) string {
	for _, v := range []error{repository.ErrIdempotencyConflict, repository.ErrMavenSnapshotTakeoverNotReady, repository.ErrMavenSnapshotBuildExhausted, repository.ErrMavenSnapshotTakenOver} {
		if errors.Is(err, v) {
			return v.Error()
		}
	}
	return "takeover_conflict_or_unavailable"
}
