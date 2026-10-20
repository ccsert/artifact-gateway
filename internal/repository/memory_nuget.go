package repository

import (
	"bytes"
	"context"
	"sort"
	"time"
)

func (s *MemoryStore) activeNuGetRepositoryLocked(id string) (HostedRepository, error) {
	repo, ok := s.hostedRepositories[id]
	if !ok || repo.State != RepositoryActive || repo.Type != RepositoryTypeHosted || repo.Format != FormatNuGet {
		return HostedRepository{}, ErrNotFound
	}
	return repo, nil
}

func (s *MemoryStore) ReserveNuGetPublication(ctx context.Context, in NuGetSession) (NuGetSession, bool, error) {
	if !validNuGetSession(in) || !in.ExpiresAt.After(time.Now()) || !in.CreatedAt.IsZero() || in.State != "" || in.ObjectKey != "" {
		return NuGetSession{}, false, ErrInvalidNuGetPublication
	}
	if err := ctx.Err(); err != nil {
		return NuGetSession{}, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.activeNuGetRepositoryLocked(in.RepositoryID); err != nil {
		return NuGetSession{}, false, err
	}
	if prior, found := s.nugetSessions[in.ID]; found {
		if !nuGetSessionSame(prior, in) || prior.Actor != in.Actor {
			return NuGetSession{}, false, ErrIdempotencyConflict
		}
		if prior.State == "expired" || prior.State != "committed" && !prior.ExpiresAt.After(time.Now()) {
			return NuGetSession{}, false, ErrNuGetSessionExpired
		}
		return cloneNuGetSession(prior), true, nil
	}
	for _, prior := range s.nugetSessions {
		if prior.RepositoryID != in.RepositoryID || prior.PackageID != in.PackageID || prior.Version != in.Version {
			continue
		}
		if (prior.State == "reserved" || prior.State == "staged") && !prior.ExpiresAt.After(time.Now()) {
			prior.State = "expired"
			s.nugetSessions[prior.ID] = prior
		}
		if prior.State == "expired" {
			continue
		}
		if !nuGetSessionSame(prior, in) {
			return NuGetSession{}, false, ErrNuGetPublicationConflict
		}
		if prior.State != "committed" && prior.Actor != in.Actor {
			return NuGetSession{}, false, ErrNuGetPublicationConflict
		}
		return cloneNuGetSession(prior), true, nil
	}
	capacity, _ := s.repositoryCapacityLocked(in.RepositoryID)
	reserved := int64(0)
	for _, current := range s.nugetSessions {
		if current.RepositoryID == in.RepositoryID && (current.State == "reserved" || current.State == "staged") && current.ExpiresAt.After(time.Now()) {
			reserved += current.Size
		}
	}
	if capacity.QuotaBytes > 0 && (capacity.UsedBytes+reserved > capacity.QuotaBytes || in.Size > capacity.QuotaBytes-capacity.UsedBytes-reserved) {
		return NuGetSession{}, false, ErrQuotaExceeded
	}
	in.CreatedAt = time.Now().UTC()
	in.State = "reserved"
	in.ObjectKey = nuGetSessionObjectKey(in)
	if s.nugetSessions == nil {
		s.nugetSessions = map[string]NuGetSession{}
	}
	s.nugetSessions[in.ID] = cloneNuGetSession(in)
	return cloneNuGetSession(in), false, nil
}

func (s *MemoryStore) GetNuGetSession(ctx context.Context, id string) (NuGetSession, error) {
	if err := ctx.Err(); err != nil {
		return NuGetSession{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	in, ok := s.nugetSessions[id]
	if !ok {
		return NuGetSession{}, ErrNotFound
	}
	if _, err := s.activeNuGetRepositoryLocked(in.RepositoryID); err != nil {
		return NuGetSession{}, err
	}
	return cloneNuGetSession(in), nil
}

func (s *MemoryStore) StageNuGetPublication(ctx context.Context, in NuGetSession) (NuGetSession, error) {
	if !validNuGetStaging(in) {
		return NuGetSession{}, ErrInvalidNuGetPublication
	}
	if err := ctx.Err(); err != nil {
		return NuGetSession{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	prior, ok := s.nugetSessions[in.ID]
	if !ok {
		return NuGetSession{}, ErrNotFound
	}
	if _, err := s.activeNuGetRepositoryLocked(in.RepositoryID); err != nil {
		return NuGetSession{}, err
	}
	if !nuGetSessionSame(prior, in) || prior.Actor != in.Actor {
		return NuGetSession{}, ErrNuGetPublicationConflict
	}
	if prior.State == "expired" || !prior.ExpiresAt.After(time.Now()) {
		return NuGetSession{}, ErrNuGetSessionExpired
	}
	if prior.State != "reserved" && prior.State != "staged" {
		return NuGetSession{}, ErrNuGetSessionState
	}
	if prior.State == "staged" && (!bytes.Equal(prior.Nuspec, in.Nuspec) || !bytes.Equal(prior.MetadataJSON, in.MetadataJSON)) {
		return NuGetSession{}, ErrNuGetPublicationConflict
	}
	prior.Nuspec = append([]byte(nil), in.Nuspec...)
	prior.MetadataJSON = append([]byte(nil), in.MetadataJSON...)
	prior.State = "staged"
	s.nugetSessions[in.ID] = prior
	return cloneNuGetSession(prior), nil
}

func (s *MemoryStore) CommitNuGetPublication(ctx context.Context, id, actor string) (NuGetPublication, bool, error) {
	if err := ctx.Err(); err != nil {
		return NuGetPublication{}, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.nugetSessions[id]
	if !ok {
		return NuGetPublication{}, false, ErrNotFound
	}
	repo, err := s.activeNuGetRepositoryLocked(session.RepositoryID)
	if err != nil {
		return NuGetPublication{}, false, err
	}
	if session.Actor != actor {
		return NuGetPublication{}, false, ErrNuGetPublicationConflict
	}
	key := nuGetPublicationKey(session.RepositoryID, session.PackageID, session.Version)
	if session.State == "committed" {
		return cloneNuGetPublication(s.nugetPublications[key]), true, nil
	}
	if session.State == "expired" || !session.ExpiresAt.After(time.Now()) {
		return NuGetPublication{}, false, ErrNuGetSessionExpired
	}
	if session.State != "staged" {
		return NuGetPublication{}, false, ErrNuGetSessionState
	}
	capacity, _ := s.repositoryCapacityLocked(session.RepositoryID)
	reserved := int64(0)
	for _, active := range s.nugetSessions {
		if active.RepositoryID == session.RepositoryID && (active.State == "reserved" || active.State == "staged") && active.ExpiresAt.After(time.Now()) {
			reserved += active.Size
		}
	}
	if capacity.QuotaBytes > 0 && capacity.UsedBytes+reserved > capacity.QuotaBytes {
		return NuGetPublication{}, false, ErrQuotaExceeded
	}
	if s.nugetPublications == nil {
		s.nugetPublications = map[string]NuGetPublication{}
	}
	session.State = "committed"
	session.CommittedAt = time.Now().UTC()
	s.nugetSessions[id] = session
	publication := nuGetPublication(session, session.CommittedAt)
	s.nugetPublications[key] = publication
	s.appendAuditLocked(withRequestContextAudit(ctx, nuGetPublicationAudit(publication, repo.Name)))
	return cloneNuGetPublication(publication), false, nil
}

func (s *MemoryStore) GetNuGetPublication(ctx context.Context, repo, id, version string) (NuGetPublication, error) {
	if err := ctx.Err(); err != nil {
		return NuGetPublication{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, err := s.activeNuGetRepositoryLocked(repo); err != nil {
		return NuGetPublication{}, err
	}
	in, ok := s.nugetPublications[nuGetPublicationKey(repo, id, version)]
	if !ok {
		return NuGetPublication{}, ErrNotFound
	}
	return cloneNuGetPublication(in), nil
}

func (s *MemoryStore) ListNuGetPublications(ctx context.Context, repo, id string, limit int, after string) ([]NuGetPublication, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, err := s.activeNuGetRepositoryLocked(repo); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 1000 {
		limit = 64
	}
	out := []NuGetPublication{}
	for _, p := range s.nugetPublications {
		if p.RepositoryID == repo && (id == "" || id == p.PackageID) && p.PackageID+"@"+p.Version > after {
			p = cloneNuGetPublication(p)
			p.Nuspec = nil
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PackageID+"@"+out[i].Version < out[j].PackageID+"@"+out[j].Version })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (s *MemoryStore) ExpireNuGetSessions(ctx context.Context, now time.Time, limit int) ([]NuGetSession, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	out := []NuGetSession{}
	for id, in := range s.nugetSessions {
		if (in.State == "reserved" || in.State == "staged") && !in.ExpiresAt.After(now) {
			in.State = "expired"
			s.nugetSessions[id] = in
		}
		if in.State == "expired" && in.CollectedAt.IsZero() {
			out = append(out, cloneNuGetSession(in))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (s *MemoryStore) MarkNuGetSessionCollected(ctx context.Context, id, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	in, ok := s.nugetSessions[id]
	if !ok {
		return ErrNotFound
	}
	if in.State != "expired" || in.ObjectKey != key {
		return ErrNuGetSessionState
	}
	if in.CollectedAt.IsZero() {
		in.CollectedAt = time.Now().UTC()
		s.nugetSessions[id] = in
	}
	return nil
}

func (s *MemoryStore) LockNuGetPublication(ctx context.Context, repo, id, version string) (context.Context, func(), error) {
	s.mu.Lock()
	if s.nugetPublicationLocks == nil {
		s.nugetPublicationLocks = map[string]chan struct{}{}
	}
	locks := s.nugetPublicationLocks
	s.mu.Unlock()
	unlock, err := s.lockRaw(ctx, locks, nuGetPublicationKey(repo, id, version))
	return ctx, unlock, err
}
