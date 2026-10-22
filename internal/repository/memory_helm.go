package repository

import (
	"context"
	"sort"
	"time"
)

func (s *MemoryStore) activeHelmRepositoryLocked(id string) (HostedRepository, error) {
	repo, ok := s.hostedRepositories[id]
	if !ok || repo.State != RepositoryActive || repo.Type != RepositoryTypeHosted || repo.Format != FormatHelm {
		return HostedRepository{}, ErrNotFound
	}
	return repo, nil
}
func (s *MemoryStore) helmQuotaLocked(repo string, extra int64) error {
	capacity, err := s.repositoryCapacityLocked(repo)
	if err != nil {
		return err
	}
	reserved := int64(0)
	for _, in := range s.helmSessions {
		if in.RepositoryID == repo && in.State == "pending" && in.ExpiresAt.After(time.Now()) {
			reserved += in.Size + in.ProvenanceSize
		}
	}
	if capacity.QuotaBytes > 0 && (capacity.UsedBytes+reserved > capacity.QuotaBytes || extra > capacity.QuotaBytes-capacity.UsedBytes-reserved) {
		return ErrQuotaExceeded
	}
	return nil
}
func (s *MemoryStore) ReserveHelmPublication(ctx context.Context, in HelmSession) (HelmSession, bool, error) {
	if !validHelmSession(in) || !in.ExpiresAt.After(time.Now()) || !in.CreatedAt.IsZero() || in.State != "" || in.ObjectKey != "" || in.ProvenanceKey != "" || !in.CommittedAt.IsZero() || !in.CollectedAt.IsZero() {
		return HelmSession{}, false, ErrInvalidHelmPublication
	}
	if err := ctx.Err(); err != nil {
		return HelmSession{}, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.activeHelmRepositoryLocked(in.RepositoryID); err != nil {
		return HelmSession{}, false, err
	}
	if prior, ok := s.helmSessions[in.ID]; ok {
		if !helmSessionSame(prior, in) || prior.Actor != in.Actor {
			return HelmSession{}, false, ErrIdempotencyConflict
		}
		if prior.State == "expired" || prior.State != "committed" && !prior.ExpiresAt.After(time.Now()) {
			return HelmSession{}, false, ErrHelmSessionExpired
		}
		return cloneHelmSession(prior), true, nil
	}
	for id, prior := range s.helmSessions {
		if prior.RepositoryID != in.RepositoryID || prior.Name != in.Name || prior.Version != in.Version {
			continue
		}
		if prior.State == "pending" && !prior.ExpiresAt.After(time.Now()) {
			prior.State = "expired"
			s.helmSessions[id] = prior
		}
		if prior.State == "expired" {
			continue
		}
		if !helmSessionSame(prior, in) || (prior.State != "committed" && prior.Actor != in.Actor) {
			return HelmSession{}, false, ErrHelmPublicationConflict
		}
		return cloneHelmSession(prior), true, nil
	}
	if err := s.helmQuotaLocked(in.RepositoryID, in.Size+in.ProvenanceSize); err != nil {
		return HelmSession{}, false, err
	}
	in.CreatedAt = time.Now().UTC()
	in.State = "pending"
	in.ObjectKey, in.ProvenanceKey = helmObjectKeys(in)
	if s.helmSessions == nil {
		s.helmSessions = map[string]HelmSession{}
	}
	s.helmSessions[in.ID] = cloneHelmSession(in)
	return cloneHelmSession(in), false, nil
}
func (s *MemoryStore) GetHelmSession(ctx context.Context, id string) (HelmSession, error) {
	if err := ctx.Err(); err != nil {
		return HelmSession{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	in, ok := s.helmSessions[id]
	if !ok {
		return HelmSession{}, ErrNotFound
	}
	return cloneHelmSession(in), nil
}
func (s *MemoryStore) CommitHelmPublication(ctx context.Context, id, actor string) (HelmPublication, bool, error) {
	if err := ctx.Err(); err != nil {
		return HelmPublication{}, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	in, ok := s.helmSessions[id]
	if !ok {
		return HelmPublication{}, false, ErrNotFound
	}
	repo, err := s.activeHelmRepositoryLocked(in.RepositoryID)
	if err != nil {
		return HelmPublication{}, false, err
	}
	if in.Actor != actor {
		return HelmPublication{}, false, ErrHelmPublicationConflict
	}
	key := helmKey(in.RepositoryID, in.Name, in.Version)
	if in.State == "committed" {
		return cloneHelmPublication(s.helmPublications[key]), true, nil
	}
	if in.State == "expired" || !in.ExpiresAt.After(time.Now()) {
		return HelmPublication{}, false, ErrHelmSessionExpired
	}
	if in.State != "pending" {
		return HelmPublication{}, false, ErrHelmSessionState
	}
	if err = s.helmQuotaLocked(in.RepositoryID, 0); err != nil {
		return HelmPublication{}, false, err
	}
	in.State = "committed"
	in.CommittedAt = time.Now().UTC()
	s.helmSessions[id] = in
	publication := helmPublication(in)
	if s.helmPublications == nil {
		s.helmPublications = map[string]HelmPublication{}
	}
	s.helmPublications[key] = publication
	s.appendAuditLocked(withRequestContextAudit(ctx, helmPublicationAudit(publication, repo.Name)))
	return cloneHelmPublication(publication), false, nil
}
func (s *MemoryStore) GetHelmPublication(ctx context.Context, repo, name, version string) (HelmPublication, error) {
	if err := ctx.Err(); err != nil {
		return HelmPublication{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, err := s.activeHelmRepositoryLocked(repo); err != nil {
		return HelmPublication{}, err
	}
	p, ok := s.helmPublications[helmKey(repo, name, version)]
	if !ok {
		return HelmPublication{}, ErrNotFound
	}
	return cloneHelmPublication(p), nil
}
func (s *MemoryStore) ListHelmPublications(ctx context.Context, repo string, limit int) ([]HelmPublication, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, err := s.activeHelmRepositoryLocked(repo); err != nil {
		return nil, err
	}
	if limit < 1 || limit > 4097 {
		limit = 4097
	}
	out := []HelmPublication{}
	for _, p := range s.helmPublications {
		if p.RepositoryID == repo {
			out = append(out, cloneHelmPublication(p))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name+"@"+out[i].Version < out[j].Name+"@"+out[j].Version })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
func (s *MemoryStore) ExpireHelmSessions(ctx context.Context, now time.Time, limit int) ([]HelmSession, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if limit < 1 || limit > 1000 {
		limit = 100
	}
	out := []HelmSession{}
	for id, in := range s.helmSessions {
		if in.State == "pending" && !in.ExpiresAt.After(now) {
			in.State = "expired"
			s.helmSessions[id] = in
		}
		if in.State == "expired" && in.CollectedAt.IsZero() {
			out = append(out, cloneHelmSession(in))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
func (s *MemoryStore) MarkHelmSessionCollected(ctx context.Context, id, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	in, ok := s.helmSessions[id]
	if !ok {
		return ErrNotFound
	}
	if in.State != "expired" || in.ObjectKey != key {
		return ErrHelmSessionState
	}
	if in.CollectedAt.IsZero() {
		in.CollectedAt = time.Now().UTC()
		s.helmSessions[id] = in
	}
	return nil
}
func (s *MemoryStore) LockHelmPublication(ctx context.Context, repo, name, version string) (context.Context, func(), error) {
	s.mu.Lock()
	if s.helmPublicationLocks == nil {
		s.helmPublicationLocks = map[string]chan struct{}{}
	}
	locks := s.helmPublicationLocks
	s.mu.Unlock()
	unlock, err := s.lockRaw(ctx, locks, helmKey(repo, name, version))
	return ctx, unlock, err
}
