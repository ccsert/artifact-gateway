package repository

import (
	"context"
	"time"
)

func (s *MemoryStore) checkMavenTakeoverLocked(p MavenSnapshotImportPlan, key string) (MavenSnapshotImport, error) {
	v, ok := s.mavenImports[p.RepositoryID+"\x00"+p.Coordinate]
	if !ok {
		return v, ErrNotFound
	}
	if !validMavenImportPlan(p) || key == "" || len(key) > 128 || v.PlanDigest != mavenImportDigest(p) {
		return v, ErrIdempotencyConflict
	}
	repo := s.hostedRepositories[p.RepositoryID]
	if repo.State != RepositoryActive || repo.Type != RepositoryTypeHosted || repo.Format != FormatMaven || repo.MavenStrictPublication || s.retentionPolicies[p.RepositoryID].Enabled || v.State != "committed" {
		return v, ErrMavenSnapshotTakeoverNotReady
	}
	if v.Writable() && (v.TakeoverKey != key || v.TakeoverActor != p.Actor) {
		return v, ErrIdempotencyConflict
	}
	if s.mavenSnapshotReadBlockedLocked(p.RepositoryID, p.Coordinate) {
		return v, ErrArtifactQuarantined
	}
	for _, a := range s.mavenArtifacts {
		if a.RepositoryID == p.RepositoryID && a.Coordinate == p.Coordinate && (a.BuildNumber >= MaxMavenSnapshotBuildNumber || a.SourceBuildNumber >= MaxMavenSnapshotBuildNumber) {
			return v, ErrMavenSnapshotBuildExhausted
		}
	}
	for _, expected := range p.Assets {
		a, exists := s.mavenAssets[p.RepositoryID+"\x00"+expected.Path]
		intent := s.mavenObjectIntents[a.ObjectKey]
		if !exists || a != expected || !s.mavenObjectRefs[a.ObjectKey] || !intent.claimedAt.IsZero() || !intent.deletedAt.IsZero() {
			return v, ErrMavenSnapshotTakeoverNotReady
		}
	}
	for _, expected := range p.Artifacts {
		found := false
		for _, a := range s.mavenArtifacts {
			if a.RepositoryID == p.RepositoryID && a.Coordinate == p.Coordinate && a.SourceTimestamp == expected.SourceTimestamp && a.SourceBuildNumber == expected.SourceBuildNumber && a.Digest == expected.Digest && a.State == "visible" {
				found = true
			}
		}
		if !found {
			return v, ErrMavenSnapshotTakeoverNotReady
		}
	}
	return cloneMavenImport(v), nil
}

func (s *MemoryStore) CheckMavenSnapshotTakeover(_ context.Context, p MavenSnapshotImportPlan, key string) (MavenSnapshotImport, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.checkMavenTakeoverLocked(p, key)
}
func (s *MemoryStore) TakeoverMavenSnapshotImport(_ context.Context, p MavenSnapshotImportPlan, key string) (MavenSnapshotImport, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, err := s.checkMavenTakeoverLocked(p, key)
	if err != nil || v.Writable() {
		return v, err
	}
	v.TakeoverKey, v.TakeoverActor, v.TakenOverAt = key, p.Actor, time.Now().UTC()
	s.mavenImports[p.RepositoryID+"\x00"+p.Coordinate] = cloneMavenImport(v)
	repo := s.hostedRepositories[p.RepositoryID]
	s.Audits = append(s.Audits, AuditRecord{Repository: repo.Name, GroupName: repo.Name, Actor: p.Actor, Operation: "maven.snapshot.takeover", Format: "maven", Resource: p.Coordinate, Outcome: AuditResolved, OccurredAt: v.TakenOverAt, Evidence: map[string]string{"manifestDigest": p.ManifestDigest, "sourceId": p.SourceID, "repositoryId": p.RepositoryID, "targetBinding": p.TargetBinding, "idempotencyKey": key, "importSessionId": v.SessionID}})
	return cloneMavenImport(v), nil
}
func (s *MemoryStore) MavenSnapshotImportPathReserved(_ context.Context, repo, coordinate, path string) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, a := range s.mavenArtifacts {
		if a.RepositoryID == repo && a.Coordinate == coordinate && (a.SourceTimestamp != "" || s.mavenSessions[a.ID].State == "committed") && mavenArchivedNamespaceMatches(a, path) {
			return true, nil
		}
	}
	return false, nil
}
func (s *MemoryStore) FindMavenSnapshotDeployment(_ context.Context, repo, coordinate, publisher, stamp string, build int) (MavenPublishSession, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, v := range s.mavenSessions {
		if v.RepositoryID == repo && v.Coordinate == coordinate && v.Publisher == publisher && v.ClientTimestamp == stamp && v.ClientBuildNumber == build {
			return v, nil
		}
	}
	return MavenPublishSession{}, ErrNotFound
}
func (s *MemoryStore) CreateMavenSnapshotDeployment(_ context.Context, session MavenPublishSession) (MavenPublishSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v := s.mavenImports[session.RepositoryID+"\x00"+session.Coordinate]
	repo := s.hostedRepositories[session.RepositoryID]
	if repo.State != RepositoryActive || repo.MavenStrictPublication || s.retentionPolicies[repo.ID].Enabled {
		return session, ErrMavenSnapshotTakeoverNotReady
	}
	if !v.Writable() || session.ID == v.SessionID || !validMavenSnapshotReceipt(session) || session.State != "open" || !session.ExpiresAt.After(time.Now()) {
		return session, ErrDisabled
	}
	for _, existing := range s.mavenSessions {
		if existing.RepositoryID == session.RepositoryID && existing.Coordinate == session.Coordinate && existing.Publisher == session.Publisher && existing.ClientTimestamp == session.ClientTimestamp && existing.ClientBuildNumber == session.ClientBuildNumber {
			return session, ErrNameExists
		}
	}
	s.mavenSessions[session.ID] = session
	s.mavenUploads[session.ID] = map[string]string{}
	return session, nil
}
func (s *MemoryStore) CompleteMavenSnapshotDeployment(_ context.Context, id, fingerprint, mainExtension string) (MavenSnapshotImport, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.mavenSessions[id]
	v := s.mavenImports[session.RepositoryID+"\x00"+session.Coordinate]
	repo := s.hostedRepositories[session.RepositoryID]
	if repo.State != RepositoryActive || repo.MavenStrictPublication || s.retentionPolicies[repo.ID].Enabled || s.mavenSnapshotReadBlockedLocked(repo.ID, session.Coordinate) {
		return v, ErrMavenSnapshotTakeoverNotReady
	}
	if !ok || (session.State != "open" && session.State != "committed") || session.State == "open" && !session.ExpiresAt.After(time.Now()) || MavenSnapshotDeploymentFingerprint(session, s.mavenUploads[id]) != fingerprint {
		return v, ErrMavenSnapshotTakeoverNotReady
	}
	a := s.mavenArtifacts[id]
	aliases, err := mavenSnapshotDeploymentAliases(v, session, a, mainExtension)
	if err != nil {
		return v, err
	}
	for _, object := range session.Objects {
		key := s.mavenUploads[id][object.Name]
		asset := s.mavenAssets[session.RepositoryID+"\x00"+mavenSnapshotTimestampedPath(mavenArtifactPathPrefix(session.Coordinate)+object.Name, session.Coordinate, a.CreatedAt, a.BuildNumber)]
		intent := s.mavenObjectIntents[key]
		if key == "" || asset.ObjectKey != key || asset.Digest != object.Digest || asset.Size != object.Size || !s.mavenObjectRefs[key] || !intent.claimedAt.IsZero() || !intent.deletedAt.IsZero() {
			return v, ErrMavenSnapshotTakeoverNotReady
		}
	}
	for _, target := range aliases {
		asset, exists := s.mavenAssets[session.RepositoryID+"\x00"+target]
		visible := false
		for _, owner := range s.mavenArtifacts {
			if owner.RepositoryID == session.RepositoryID && owner.Coordinate == session.Coordinate && owner.State == "visible" && mavenAssetBelongsToArtifactBuild(asset, owner) {
				visible = true
			}
		}
		intent := s.mavenObjectIntents[asset.ObjectKey]
		if !exists || !visible || !s.mavenObjectRefs[asset.ObjectKey] || !intent.claimedAt.IsZero() || !intent.deletedAt.IsZero() {
			return v, ErrMavenSnapshotTakeoverNotReady
		}
	}
	if session.State == "committed" {
		return cloneMavenImport(v), nil
	}
	if !session.ExpiresAt.After(time.Now()) {
		return v, ErrMavenSnapshotTakeoverNotReady
	}
	if a.BuildNumber > v.CurrentBuildNumber {
		v.CurrentBuildNumber, v.CurrentAliases = a.BuildNumber, aliases
		s.mavenImports[v.RepositoryID+"\x00"+v.Coordinate] = cloneMavenImport(v)
	}
	session.State = "committed"
	s.mavenSessions[id] = session
	s.Audits = append(s.Audits, AuditRecord{Repository: s.hostedRepositories[v.RepositoryID].Name, Actor: session.Publisher, Operation: "maven.snapshot.deploy.complete", Format: "maven", Resource: v.Coordinate, Outcome: AuditResolved, OccurredAt: time.Now().UTC(), Evidence: map[string]string{"sessionId": id, "receipt": session.ClientTimestamp, "fingerprint": fingerprint}})
	return cloneMavenImport(v), nil
}

func (s *MemoryStore) mavenSnapshotReadBlockedLocked(repo, coordinate string) bool {
	if !s.quarantineReadPolicies[repo].Enabled {
		return false
	}
	for _, a := range s.mavenArtifacts {
		if a.RepositoryID == repo && a.Coordinate == coordinate && s.artifactQuarantines[artifactQuarantineKey(repo, FormatMaven, coordinate, a.Digest)].State == ArtifactQuarantineStateQuarantined {
			return true
		}
	}
	return false
}
