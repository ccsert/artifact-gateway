package repository

import (
	"context"
	"slices"

	"github.com/google/uuid"
)

// APTDistributionCommit binds a target-owned signed view to the immutable
// source package and a live worker lease. The store validates these together
// with the target base in the visibility transaction, after signing finishes.
type APTDistributionCommit struct {
	ID, RequestDigest, Operation, WorkID, LeaseToken string
	BaseSnapshotID, SessionID                        string
	Source                                           APTSnapshotAsset
}

type NativeAPTDistributionStore interface {
	CommitAPTDistributionSnapshot(context.Context, APTDistributionCommit, APTRepositorySnapshot, []APTSnapshotAsset, []byte, AuditRecord) (APTRepositorySnapshot, error)
}

func validateAPTDistributionCommit(d APTDistributionCommit, snapshot APTRepositorySnapshot, baseID string, before, after []APTSnapshotPackage, assets []APTSnapshotAsset) error {
	if _, err := uuid.Parse(d.ID); err != nil {
		return ErrDisabled
	}
	if _, err := uuid.Parse(d.WorkID); err != nil {
		return ErrDisabled
	}
	if !ValidAPTSHA256Digest(d.RequestDigest) || d.LeaseToken == "" || (d.Operation != "promote" && d.Operation != "replicate") ||
		d.Source.RepositoryID == snapshot.RepositoryID || !ValidAPTArtifactCoordinate(d.Source.Path) || !ValidAPTSHA256Digest(d.Source.Digest) || d.Source.Size <= 0 || d.SessionID == "" {
		return ErrDisabled
	}
	if d.BaseSnapshotID != baseID {
		return ErrVersionConflict
	}
	want := make([]string, 0, len(before)+1)
	for _, m := range before {
		want = append(want, m.PublicationSessionID)
	}
	if !slices.Contains(want, d.SessionID) {
		want = append(want, d.SessionID)
	}
	got := make([]string, 0, len(after))
	for _, m := range after {
		got = append(got, m.PublicationSessionID)
	}
	slices.Sort(want)
	slices.Sort(got)
	if !slices.Equal(want, got) {
		return ErrVersionConflict
	}
	for _, a := range assets {
		if a.Path == d.Source.Path && a.Digest == d.Source.Digest && a.ObjectKey == d.Source.ObjectKey && a.Size == d.Source.Size {
			return nil
		}
	}
	return ErrVersionConflict
}
