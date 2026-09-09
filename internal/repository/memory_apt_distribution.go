package repository

import (
	"context"
	"encoding/json"
	"time"
)

func (s *MemoryStore) CommitAPTDistributionSnapshot(_ context.Context, d APTDistributionCommit, snapshot APTRepositorySnapshot, assets []APTSnapshotAsset, release []byte, audit AuditRecord) (APTRepositorySnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.aptLifecycleResults[d.ID]; ok {
		return APTRepositorySnapshot{}, ErrIdempotencyConflict
	}
	for _, id := range []string{d.Source.RepositoryID, snapshot.RepositoryID} {
		repo, ok := s.hostedRepositories[id]
		if !ok || repo.Format != FormatAPT || repo.Type != RepositoryTypeHosted || repo.State != RepositoryActive {
			return APTRepositorySnapshot{}, ErrNotFound
		}
	}
	validLease := false
	switch d.Operation {
	case "promote":
		for _, job := range s.lifecycleJobs {
			if job.ID == d.WorkID && job.RepositoryID == snapshot.RepositoryID && job.Kind == LifecycleJobPromotion && job.State == LifecycleJobRunning && job.LeaseToken == d.LeaseToken && job.LeaseExpiresAt.After(time.Now()) {
				var payload struct {
					Format     Format `json:"format"`
					Source     string `json:"sourceRepositoryId"`
					Coordinate string `json:"coordinate"`
					Digest     string `json:"digest"`
					Suite      string `json:"aptTargetSuite"`
				}
				validLease = json.Unmarshal(job.Payload, &payload) == nil && payload.Format == FormatAPT && payload.Source == d.Source.RepositoryID && payload.Coordinate == d.Source.Path && payload.Digest == d.Source.Digest && payload.Suite == snapshot.Suite
			}
		}
	case "replicate":
		p := s.replicationPlans[d.WorkID]
		validLease = p.TargetRepositoryID == snapshot.RepositoryID && p.SourceRepositoryID == d.Source.RepositoryID && p.Format == FormatAPT && p.APTTargetSuite == snapshot.Suite && p.Coordinate == d.Source.Path && p.Digest == d.Source.Digest && p.State == "running" && p.LeaseToken == d.LeaseToken && p.LeaseExpiresAt.After(time.Now())
	}
	if !validLease {
		return APTRepositorySnapshot{}, ErrNotFound
	}
	sourceVisible := false
	baseID := ""
	for id, current := range s.aptSnapshots {
		if current.State != APTRepositorySnapshotVisible {
			continue
		}
		if current.RepositoryID == snapshot.RepositoryID && current.Suite == snapshot.Suite {
			baseID = id
		}
		if current.RepositoryID != d.Source.RepositoryID {
			continue
		}
		for _, a := range s.aptSnapshotAssets[id] {
			if a.Path == d.Source.Path && a.Digest == d.Source.Digest && a.ObjectKey == d.Source.ObjectKey && a.Size == d.Source.Size {
				sourceVisible = true
			}
		}
	}
	if !sourceVisible {
		return APTRepositorySnapshot{}, ErrNotFound
	}
	if s.aptAssetsQuarantinedLocked(d.Source.RepositoryID, []APTSnapshotAsset{d.Source}, "") {
		return APTRepositorySnapshot{}, ErrArtifactQuarantined
	}
	if err := validateAPTDistributionCommit(d, snapshot, baseID, s.aptSnapshotPackages[baseID], s.aptSnapshotPackages[snapshot.ID], assets); err != nil {
		return APTRepositorySnapshot{}, err
	}
	result, err := s.publishAPTRepositorySnapshotLocked(snapshot, assets, release, audit)
	if err != nil {
		return APTRepositorySnapshot{}, err
	}
	s.aptLifecycleResults[d.ID] = APTLifecycleResult{RequestDigest: d.RequestDigest, SnapshotID: result.ID}
	return result, nil
}
