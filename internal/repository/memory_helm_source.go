package repository

import (
	"context"
	"time"
)

func (s *MemoryStore) helmSourceLocked(id, version string) error {
	r, ok := s.hostedRepositories[id]
	if !ok || r.Format != FormatHelm || r.Type != RepositoryTypeProxy || r.State != RepositoryActive {
		return ErrNotFound
	}
	if r.Version != version {
		return ErrHelmSourceChanged
	}
	return nil
}
func (s *MemoryStore) GetHelmSourceDocument(ctx context.Context, id, version string) (HelmSourceDocument, error) {
	if e := ctx.Err(); e != nil {
		return HelmSourceDocument{}, e
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if e := s.helmSourceLocked(id, version); e != nil {
		return HelmSourceDocument{}, e
	}
	in, ok := s.helmDocuments[id]
	if !ok || in.RepositoryVersion != version {
		return HelmSourceDocument{}, ErrNotFound
	}
	return cloneHelmDocument(in), nil
}
func (s *MemoryStore) PutHelmSourceDocument(ctx context.Context, in HelmSourceDocument) error {
	if !validHelmDocument(in) {
		return ErrInvalidHelmPublication
	}
	if e := ctx.Err(); e != nil {
		return e
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := s.helmSourceLocked(in.RepositoryID, in.RepositoryVersion); e != nil {
		return e
	}
	if s.helmDocuments == nil {
		s.helmDocuments = map[string]HelmSourceDocument{}
	}
	s.helmDocuments[in.RepositoryID] = cloneHelmDocument(in)
	return nil
}
func (s *MemoryStore) helmViewLocked(id, version string) error {
	if r, ok := s.hostedRepositories[id]; ok && r.Format == FormatHelm && r.State == RepositoryActive {
		if r.Version != version {
			return ErrHelmSourceChanged
		}
		return nil
	}
	if g, ok := s.hostedGroups[id]; ok && g.Format == FormatHelm {
		if g.Version != version {
			return ErrVersionConflict
		}
		return nil
	}
	return ErrNotFound
}
func (s *MemoryStore) PutHelmSourceSnapshot(ctx context.Context, in HelmSourceSnapshot) (HelmSourceSnapshot, error) {
	if !validHelmSnapshot(in) {
		return HelmSourceSnapshot{}, ErrInvalidHelmPublication
	}
	if e := ctx.Err(); e != nil {
		return HelmSourceSnapshot{}, e
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := s.helmViewLocked(in.ViewID, in.ViewVersion); e != nil {
		return HelmSourceSnapshot{}, e
	}
	if prior, ok := s.helmSnapshots[in.ID]; ok {
		if prior.ViewID != in.ViewID || prior.ViewVersion != in.ViewVersion || prior.Digest != in.Digest {
			return HelmSourceSnapshot{}, ErrHelmPublicationConflict
		}
		return cloneHelmSnapshot(prior), nil
	}
	count := 0
	for _, snap := range s.helmSnapshots {
		if snap.ViewID == in.ViewID {
			count++
		}
	}
	if count >= 128 {
		return HelmSourceSnapshot{}, ErrHelmCacheLimit
	}
	for _, entry := range in.Entries {
		r, ok := s.hostedRepositories[entry.RepositoryID]
		if !ok || r.Format != FormatHelm || r.State != RepositoryActive || r.Version != entry.RepositoryVersion {
			return HelmSourceSnapshot{}, ErrHelmSourceChanged
		}
	}
	in.CreatedAt = time.Now().UTC()
	if s.helmSnapshots == nil {
		s.helmSnapshots = map[string]HelmSourceSnapshot{}
	}
	s.helmSnapshots[in.ID] = cloneHelmSnapshot(in)
	return cloneHelmSnapshot(in), nil
}
func (s *MemoryStore) GetHelmSourceSnapshot(ctx context.Context, view, id string) (HelmSourceSnapshot, error) {
	if e := ctx.Err(); e != nil {
		return HelmSourceSnapshot{}, e
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	in, ok := s.helmSnapshots[id]
	if !ok || in.ViewID != view {
		return HelmSourceSnapshot{}, ErrNotFound
	}
	return cloneHelmSnapshot(in), nil
}
func (s *MemoryStore) helmAuthorityOwnerLocked(group HostedGroup, in HelmSourceEntry) error {
	found := false
	for _, member := range group.Members {
		if member.RepositoryID == in.RepositoryID {
			found = true
		}
	}
	r, ok := s.hostedRepositories[in.RepositoryID]
	if !found || !ok || r.Format != FormatHelm || r.State != RepositoryActive || r.Version != in.RepositoryVersion {
		return ErrHelmSourceChanged
	}
	return nil
}
func (s *MemoryStore) BindHelmGroupAuthority(ctx context.Context, expected string, in HelmGroupAuthority) (HelmGroupAuthority, error) {
	if !validHelmSourceEntry(in.Entry) {
		return HelmGroupAuthority{}, ErrInvalidHelmPublication
	}
	if e := ctx.Err(); e != nil {
		return HelmGroupAuthority{}, e
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	group, ok := s.hostedGroups[in.GroupID]
	if !ok || group.Format != FormatHelm {
		return HelmGroupAuthority{}, ErrNotFound
	}
	if group.Version != expected {
		return HelmGroupAuthority{}, ErrVersionConflict
	}
	key := helmKey(in.GroupID, in.Entry.Name, in.Entry.Version)
	if prior, ok := s.helmAuthorities[key]; ok {
		if e := s.helmAuthorityOwnerLocked(group, prior.Entry); e != nil {
			return HelmGroupAuthority{}, e
		}
		prior.Entry = cloneHelmEntry(prior.Entry)
		return prior, nil
	}
	if e := s.helmAuthorityOwnerLocked(group, in.Entry); e != nil {
		return HelmGroupAuthority{}, e
	}
	in.CreatedAt = time.Now().UTC()
	if s.helmAuthorities == nil {
		s.helmAuthorities = map[string]HelmGroupAuthority{}
	}
	in.Entry = cloneHelmEntry(in.Entry)
	s.helmAuthorities[key] = in
	return in, nil
}
func (s *MemoryStore) GetHelmGroupAuthority(ctx context.Context, group, name, version string) (HelmGroupAuthority, error) {
	if e := ctx.Err(); e != nil {
		return HelmGroupAuthority{}, e
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	in, ok := s.helmAuthorities[helmKey(group, name, version)]
	if !ok {
		return HelmGroupAuthority{}, ErrNotFound
	}
	in.Entry = cloneHelmEntry(in.Entry)
	return in, nil
}
