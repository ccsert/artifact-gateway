package snapshotimport

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/capacityplan"
	"github.com/artifact-gateway/artifact-gateway/internal/objectstore"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

type Store interface {
	repository.MavenSnapshotImportStore
	MarkMavenPublishObject(context.Context, string, string, string) error
	LockMavenObject(context.Context, string) (context.Context, func(), error)
	ListMavenAssets(context.Context, string, string) ([]repository.MavenAsset, error)
	GetMavenAsset(context.Context, string, string) (repository.MavenAsset, error)
}
type EntryResult struct {
	Coordinate string `json:"coordinate"`
	State      string `json:"state"`
	Reason     string `json:"reason,omitempty"`
	SessionID  string `json:"sessionId,omitempty"`
	Files      int    `json:"files"`
}
type Report struct {
	TargetBinding      string                   `json:"targetBinding,omitempty"`
	CheckedAt          time.Time                `json:"checkedAt"`
	Counts             Counts                   `json:"counts"`
	CapacityReferences []capacityplan.Reference `json:"capacityReferences,omitempty"`
	SchemaVersion      int                      `json:"schemaVersion"`
	ManifestDigest     string                   `json:"manifestDigest"`
	SourceID           string                   `json:"sourceId"`
	TargetID           string                   `json:"targetId,omitempty"`
	RepositoryID       string                   `json:"repositoryId,omitempty"`
	Status             string                   `json:"status"`
	Entries            []EntryResult            `json:"entries"`
	Rejected           []Rejection              `json:"rejected"`
	Excluded           []Exclusion              `json:"excluded"`
}

func NewReport(p *Prepared) Report {
	r := Report{SchemaVersion: 1, CheckedAt: time.Now().UTC(), Status: "pending", Entries: []EntryResult{}, Rejected: []Rejection{}, Excluded: []Exclusion{}}
	if p != nil {
		r.Counts.Planned = len(p.Manifest.Coordinates)
		r.ManifestDigest = p.Digest
		r.SourceID = p.Manifest.SourceID
		r.Excluded = p.Manifest.Excluded
		for _, v := range p.Plans {
			r.Entries = append(r.Entries, EntryResult{Coordinate: v.Coordinate, State: "pending", Files: len(v.Assets)})
		}
	}
	return r
}
func CapacityReferences(plans []repository.MavenSnapshotImportPlan) []capacityplan.Reference {
	out := []capacityplan.Reference{}
	for _, p := range plans {
		for _, a := range p.Assets {
			size := a.Size
			out = append(out, capacityplan.Reference{RepositoryID: p.RepositoryID, LogicalKey: a.Path, ObjectKey: a.ObjectKey, Digest: a.Digest, Size: &size})
		}
	}
	return out
}
func ValidateCapacity(plan capacityplan.Plan, plans []repository.MavenSnapshotImportPlan, inventory, target string) error {
	if plan.InventoryID != inventory || plan.TargetID != target || capacityplan.Evaluate(plan, time.Now().UTC()).Status != capacityplan.Sufficient {
		return errors.New("capacity_preflight_not_sufficient")
	}
	expected := CapacityReferences(plans)
	if len(expected) != len(plan.References) {
		return errors.New("capacity_references_mismatch")
	}
	byPath := map[string]capacityplan.Reference{}
	for _, r := range plan.References {
		k := r.RepositoryID + "\x00" + r.LogicalKey
		if _, ok := byPath[k]; ok {
			return errors.New("capacity_references_mismatch")
		}
		byPath[k] = r
	}
	for _, r := range expected {
		v, ok := byPath[r.RepositoryID+"\x00"+r.LogicalKey]
		if !ok || v.ObjectKey != r.ObjectKey || v.Digest != r.Digest || v.Size == nil || *v.Size != *r.Size {
			return errors.New("capacity_references_mismatch")
		}
	}
	return nil
}
func verifyTarget(ctx context.Context, objects objectstore.Store, a repository.MavenAsset) error {
	r, size, err := objects.Open(ctx, a.ObjectKey)
	if err != nil {
		return err
	}
	defer func() { _ = r.Close() }()
	if size != a.Size {
		return errors.New("target_bytes_mismatch")
	}
	h := sha256.New()
	n, err := io.Copy(h, &contextReader{ctx: ctx, r: io.LimitReader(r, a.Size+1)})
	if err != nil || n != a.Size || "sha256:"+hex.EncodeToString(h.Sum(nil)) != a.Digest {
		return errors.New("target_bytes_mismatch")
	}
	return nil
}

// Run performs target-only dry-run or resumable per-GAV atomic apply. Caller
// already verified all input and, for apply, the bound fresh capacity plan.
func Run(ctx context.Context, p *Prepared, store Store, objects objectstore.Store, repo, target, actor string, binding string, apply bool, capacity ...capacityplan.Plan) (Report, error) {
	report := NewReport(p)
	report.TargetID = target
	report.TargetBinding = binding
	report.RepositoryID = repo
	plans, err := p.References(repo, target, actor, binding)
	if err != nil {
		report.Status = "rejected"
		report.Rejected = []Rejection{{Reason: "invalid_import_plan"}}
		return report.withCounts(), err
	}
	if err := p.Reverify(ctx); err != nil {
		report.Status = "rejected"
		report.Rejected = []Rejection{{Reason: "source_drift"}}
		return report.withCounts(), err
	}
	if apply && (len(capacity) != 1 || ValidateCapacity(capacity[0], plans, p.Digest, binding) != nil) {
		report.Status = "rejected"
		report.Rejected = []Rejection{{Reason: "capacity_preflight_not_sufficient_or_bound"}}
		return report.withCounts(), errors.New("capacity_preflight_not_sufficient_or_bound")
	}
	for i, plan := range plans {
		if err := store.CheckMavenSnapshotImport(ctx, plan); err != nil {
			report.Entries[i].State = "conflict"
			report.Entries[i].Reason = "target_or_input_conflict"
			if errors.Is(err, repository.ErrMavenSnapshotImportRetention) {
				report.Entries[i].Reason = "snapshot_import_retention_enabled"
			}
			report.Status = "rejected"
			return report.withCounts(), errors.New("target_or_input_conflict")
		}
	}
	// Preflight all existing physical objects before any coordinate reservation.
	seen := map[string]bool{}
	for _, plan := range plans {
		for _, a := range plan.Assets {
			if seen[a.ObjectKey] {
				continue
			}
			seen[a.ObjectKey] = true
			if err := verifyTarget(ctx, objects, a); err != nil && !errors.Is(err, objectstore.ErrNotFound) {
				report.Status = "rejected"
				report.Rejected = append(report.Rejected, Rejection{Coordinate: plan.Coordinate, Reason: "target_object_conflict_or_unavailable"})
				return report.withCounts(), errors.New("target_object_conflict_or_unavailable")
			}
		}
	}
	if !apply {
		report.Status = "ready"
		return report.withCounts(), nil
	}
	for i, plan := range plans {
		if err := ctx.Err(); err != nil {
			report.Status = "partial"
			return report.withCounts(), err
		}
		err := func() error {
			ctx, release, err := store.LockMavenSnapshotImport(ctx, repo, plan.Coordinate)
			if err != nil {
				return err
			}
			defer release()
			// Full preflight and lock waiting may outlive the evidence window.
			// Recheck at each GAV's first durable write, not only at invocation.
			if ValidateCapacity(capacity[0], plans, p.Digest, binding) != nil {
				report.Entries[i].Reason = "capacity_preflight_expired_before_write"
				return errors.New("capacity_preflight_expired_before_write")
			}
			checkpoint, err := store.BeginMavenSnapshotImport(ctx, plan)
			if err != nil {
				return err
			}
			report.Entries[i].SessionID = checkpoint.SessionID
			report.Entries[i].State = checkpoint.State
			if checkpoint.State != "committed" {
				for _, a := range plan.Assets {
					err := func() error {
						ctx, release, err := store.LockMavenObject(ctx, a.ObjectKey)
						if err != nil {
							return err
						}
						defer release()
						// Mark intent first; a resumed session protects it from GC. An active
						// collector claim rejects recovery instead of racing object deletion.
						name := a.Path[len(base(plan.Coordinate)):]
						if err := store.MarkMavenPublishObject(ctx, checkpoint.SessionID, name, a.ObjectKey); err != nil {
							return err
						}
						err = verifyTarget(ctx, objects, a)
						if errors.Is(err, objectstore.ErrNotFound) {
							reader, err := p.Reader(ctx, a)
							if err != nil {
								return err
							}
							err = objects.PutVerifiedReader(ctx, a.ObjectKey, reader, a.Size, a.Digest)
							closeErr := reader.Close()
							if err != nil {
								return err
							}
							if closeErr != nil {
								return closeErr
							}
							if err = verifyTarget(ctx, objects, a); err != nil {
								return err
							}
						} else if err != nil {
							return err
						}
						return nil
					}()
					if err != nil {
						return err
					}
				}
				// Verify all staged objects again before the metadata visibility switch.
				for _, a := range plan.Assets {
					if err := verifyTarget(ctx, objects, a); err != nil {
						return err
					}
				}
				checkpoint, err = store.CommitMavenSnapshotImport(ctx, plan)
				if err != nil {
					return err
				}
				report.Entries[i].State = checkpoint.State
			}
			for _, a := range plan.Assets {
				if err := verifyTarget(ctx, objects, a); err != nil {
					return err
				}
			}
			for _, a := range plan.Assets {
				if plan.Metadata != nil && (a.Path == plan.Metadata.Path || strings.HasPrefix(a.Path, plan.Metadata.Path+".")) {
					continue
				}
				visible, err := store.GetMavenAsset(ctx, repo, a.Path)
				if err != nil || visible != a {
					return errors.New("target_visibility_mismatch")
				}
			}
			report.Entries[i].State = "verified"
			return nil
		}()
		if err != nil {
			// A lost commit response can still have committed durably. Report
			// that boundary conservatively; the same input retry verifies it.
			if checkpoint, lookupErr := store.GetMavenSnapshotImport(ctx, repo, plan.Coordinate); lookupErr == nil && checkpoint.SessionID == report.Entries[i].SessionID && checkpoint.State == "committed" {
				report.Entries[i].State = "committed"
			}
			if report.Entries[i].Reason == "" {
				report.Entries[i].Reason = "import_or_verification_failed"
			}
			if report.Entries[i].State != "committed" {
				report.Entries[i].State = "failed"
			}
			report.Status = "partial"
			if i == 0 && report.Entries[i].SessionID == "" {
				report.Status = "rejected"
			}
			return report.withCounts(), errors.New("import_or_verification_failed")
		}
	}
	report.Status = "verified"
	return report.withCounts(), nil
}

type Counts struct {
	Planned   int `json:"planned"`
	Pending   int `json:"pending"`
	Staged    int `json:"staged"`
	Committed int `json:"committed"`
	Verified  int `json:"verified"`
	Failed    int `json:"failed"`
	Conflict  int `json:"conflict"`
	Rejected  int `json:"rejected"`
	Excluded  int `json:"excluded"`
}

func (r Report) withCounts() Report {
	r.Counts = Counts{Planned: r.Counts.Planned, Rejected: len(r.Rejected), Excluded: len(r.Excluded)}
	for _, e := range r.Entries {
		switch e.State {
		case "pending":
			r.Counts.Pending++
		case "staged":
			r.Counts.Staged++
		case "committed":
			r.Counts.Committed++
		case "verified":
			r.Counts.Verified++
		case "failed":
			r.Counts.Failed++
		case "conflict":
			r.Counts.Conflict++
		}
	}
	return r
}
