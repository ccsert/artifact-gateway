package repository

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

func cloneMavenImport(v MavenSnapshotImport) MavenSnapshotImport {
	b, _ := json.Marshal(v)
	var copy MavenSnapshotImport
	_ = json.Unmarshal(b, &copy)
	return copy
}
func (s *MemoryStore) checkMavenImportLocked(p MavenSnapshotImportPlan) error {
	if !validMavenImportPlan(p) {
		return ErrDisabled
	}
	repo, ok := s.hostedRepositories[p.RepositoryID]
	if !ok || repo.Format != FormatMaven || repo.Type != RepositoryTypeHosted || repo.State != RepositoryActive {
		return ErrNotFound
	}
	if s.retentionPolicies[p.RepositoryID].Enabled {
		return ErrMavenSnapshotImportRetention
	}
	if v, ok := s.mavenImports[p.RepositoryID+"\x00"+p.Coordinate]; ok {
		if v.PlanDigest != mavenImportDigest(p) {
			return ErrIdempotencyConflict
		}
		return nil
	}
	for _, a := range s.mavenArtifacts {
		if a.RepositoryID == p.RepositoryID && a.Coordinate == p.Coordinate {
			return ErrNameExists
		}
	}
	for _, v := range s.mavenSessions {
		if v.RepositoryID == p.RepositoryID && v.Coordinate == p.Coordinate {
			return ErrNameExists
		}
	}
	for _, a := range s.mavenAssets {
		if a.RepositoryID == p.RepositoryID && strings.HasPrefix(a.Path, mavenArtifactPathPrefix(p.Coordinate)) {
			return ErrNameExists
		}
	}
	return nil
}
func (s *MemoryStore) CheckMavenSnapshotImport(_ context.Context, p MavenSnapshotImportPlan) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.checkMavenImportLocked(p)
}
func (s *MemoryStore) GetMavenSnapshotImport(_ context.Context, repo, coordinate string) (MavenSnapshotImport, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.mavenImports[repo+"\x00"+coordinate]
	if !ok {
		return v, ErrNotFound
	}
	return cloneMavenImport(v), nil
}
func (s *MemoryStore) BeginMavenSnapshotImport(_ context.Context, p MavenSnapshotImportPlan) (MavenSnapshotImport, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkMavenImportLocked(p); err != nil {
		return MavenSnapshotImport{}, err
	}
	key := p.RepositoryID + "\x00" + p.Coordinate
	if v, ok := s.mavenImports[key]; ok {
		if v.State == "staged" {
			session := s.mavenSessions[v.SessionID]
			session.State = "open"
			session.ExpiresAt = time.Now().UTC().Add(24 * time.Hour)
			s.mavenSessions[v.SessionID] = session
		}
		return cloneMavenImport(v), nil
	}
	v := MavenSnapshotImport{RepositoryID: p.RepositoryID, Coordinate: p.Coordinate, TargetID: p.TargetID, TargetBinding: p.TargetBinding, SourceID: p.SourceID, ManifestDigest: p.ManifestDigest, PlanDigest: mavenImportDigest(p), SessionID: uuid.NewString(), Actor: p.Actor, State: "staged", Metadata: p.Metadata, Aliases: p.Aliases, CreatedAt: time.Now().UTC()}
	objects := make([]MavenDeclaredObject, 0, len(p.Assets))
	for _, a := range p.Assets {
		objects = append(objects, MavenDeclaredObject{Name: strings.TrimPrefix(a.Path, mavenArtifactPathPrefix(p.Coordinate)), Digest: a.Digest, Size: a.Size})
	}
	s.mavenSessions[v.SessionID] = MavenPublishSession{ID: v.SessionID, RepositoryID: p.RepositoryID, Coordinate: p.Coordinate, Publisher: p.Actor, PomObject: "snapshot-import", Objects: objects, State: "open", ExpiresAt: time.Now().UTC().Add(24 * time.Hour)}
	s.mavenUploads[v.SessionID] = map[string]string{}
	s.mavenImports[key] = cloneMavenImport(v)
	return cloneMavenImport(v), nil
}
func (s *MemoryStore) CommitMavenSnapshotImport(_ context.Context, p MavenSnapshotImportPlan) (MavenSnapshotImport, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkMavenImportLocked(p); err != nil {
		return MavenSnapshotImport{}, err
	}
	key := p.RepositoryID + "\x00" + p.Coordinate
	v, ok := s.mavenImports[key]
	if !ok {
		return v, ErrNotFound
	}
	if v.State == "committed" {
		return cloneMavenImport(v), nil
	}
	if len(s.mavenUploads[v.SessionID]) != len(p.Assets) {
		return v, ErrDisabled
	}
	var additional int64
	for _, a := range p.Assets {
		if s.mavenUploads[v.SessionID][strings.TrimPrefix(a.Path, mavenArtifactPathPrefix(p.Coordinate))] != a.ObjectKey {
			return v, ErrDisabled
		}
		i, ok := s.mavenObjectIntents[a.ObjectKey]
		if !ok || !i.claimedAt.IsZero() || !i.deletedAt.IsZero() {
			return v, ErrDisabled
		}
		if _, exists := s.mavenAssets[p.RepositoryID+"\x00"+a.Path]; exists {
			return v, ErrNameExists
		}
		if a.Size > int64(^uint64(0)>>1)-additional {
			return v, ErrQuotaExceeded
		}
		additional += a.Size
	}
	capacity, err := s.repositoryCapacityLocked(p.RepositoryID)
	if err != nil {
		return v, err
	}
	if capacity.QuotaBytes > 0 && additional > capacity.QuotaBytes-capacity.UsedBytes {
		return v, ErrQuotaExceeded
	}
	for i, a := range p.Artifacts {
		a.ID = uuid.NewSHA1(uuid.MustParse(v.SessionID), []byte(a.SourceTimestamp+":"+strconv.Itoa(a.SourceBuildNumber)+":"+a.Digest)).String()
		a.BuildNumber = i + 1
		a.CreatedAt, _ = time.Parse("20060102.150405", a.SourceTimestamp)
		a.State = "visible"
		s.mavenArtifacts[a.ID] = a
	}
	for _, a := range p.Assets {
		s.mavenAssets[p.RepositoryID+"\x00"+a.Path] = a
		s.mavenObjectRefs[a.ObjectKey] = true
	}
	session := s.mavenSessions[v.SessionID]
	session.State = "committed"
	s.mavenSessions[v.SessionID] = session
	v.State = "committed"
	s.mavenImports[key] = cloneMavenImport(v)
	s.Audits = append(s.Audits, AuditRecord{Repository: s.hostedRepositories[p.RepositoryID].Name, GroupName: s.hostedRepositories[p.RepositoryID].Name, Actor: p.Actor, Operation: "maven.snapshot.import", Format: "maven", Resource: p.Coordinate, Outcome: AuditResolved, OccurredAt: time.Now().UTC(), Evidence: map[string]string{"manifestDigest": p.ManifestDigest, "sourceId": p.SourceID, "sessionId": v.SessionID, "repositoryId": p.RepositoryID, "targetBinding": p.TargetBinding}})
	return cloneMavenImport(v), nil
}
