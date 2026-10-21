package repository

import (
	"context"
	"time"
)

func (s *MemoryStore) nuGetSourceLocked(repo, version string) error {
	r, ok := s.hostedRepositories[repo]
	if !ok || r.Format != FormatNuGet || r.Type != RepositoryTypeProxy || r.State != RepositoryActive {
		return ErrNotFound
	}
	if r.Version != version {
		return ErrNuGetSourceChanged
	}
	return nil
}
func (s *MemoryStore) GetNuGetSourceDocument(ctx context.Context, repo, version, url string) (NuGetSourceDocument, error) {
	if err := ctx.Err(); err != nil {
		return NuGetSourceDocument{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.nuGetSourceLocked(repo, version); err != nil {
		return NuGetSourceDocument{}, err
	}
	doc, ok := s.nugetDocuments[nuGetDocumentKey(repo, version, url)]
	if !ok {
		return NuGetSourceDocument{}, ErrNotFound
	}
	return cloneNuGetDocument(doc), nil
}
func (s *MemoryStore) PutNuGetSourceDocument(ctx context.Context, in NuGetSourceDocument) error {
	if !validNuGetDocument(in) {
		return ErrInvalidNuGetPublication
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.nuGetSourceLocked(in.RepositoryID, in.SourceVersion); err != nil {
		return err
	}
	key := nuGetDocumentKey(in.RepositoryID, in.SourceVersion, in.URL)
	count, total := 0, len(in.Body)
	for k, d := range s.nugetDocuments {
		if d.RepositoryID != in.RepositoryID {
			continue
		}
		if d.SourceVersion != in.SourceVersion {
			delete(s.nugetDocuments, k)
			continue
		}
		if k != key {
			count++
			total += len(d.Body)
		}
	}
	if count >= 4096 || total > 64<<20 {
		return ErrNuGetCacheLimit
	}
	if s.nugetDocuments == nil {
		s.nugetDocuments = map[string]NuGetSourceDocument{}
	}
	s.nugetDocuments[key] = cloneNuGetDocument(in)
	return nil
}
func (s *MemoryStore) nuGetAuthorityOwnerLocked(group HostedGroup, in NuGetGroupAuthority) error {
	found := false
	for _, member := range group.Members {
		if member.RepositoryID == in.RepositoryID {
			found = true
			break
		}
	}
	r, ok := s.hostedRepositories[in.RepositoryID]
	if !found || !ok || r.State != RepositoryActive || r.Format != FormatNuGet || r.Version != in.RepositoryVersion {
		return ErrNuGetSourceChanged
	}
	return nil
}
func (s *MemoryStore) BindNuGetGroupAuthority(ctx context.Context, expected string, in NuGetGroupAuthority) (NuGetGroupAuthority, error) {
	if !validNuGetAuthority(in) {
		return NuGetGroupAuthority{}, ErrInvalidNuGetPublication
	}
	if err := ctx.Err(); err != nil {
		return NuGetGroupAuthority{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	group, ok := s.hostedGroups[in.GroupID]
	if !ok || group.Format != FormatNuGet {
		return NuGetGroupAuthority{}, ErrNotFound
	}
	if group.Version != expected {
		return NuGetGroupAuthority{}, ErrVersionConflict
	}
	key := nuGetAuthorityKey(in.GroupID, in.PackageID, in.Version)
	if prior, ok := s.nugetAuthorities[key]; ok {
		if err := s.nuGetAuthorityOwnerLocked(group, prior); err != nil {
			return NuGetGroupAuthority{}, err
		}
		if prior.RepositoryID == in.RepositoryID && prior.RepositoryVersion == in.RepositoryVersion && in.Digest != "" {
			if prior.Digest != "" && (prior.Digest != in.Digest || prior.Size != in.Size) {
				return NuGetGroupAuthority{}, ErrNuGetPublicationConflict
			}
			prior.Digest, prior.Size = in.Digest, in.Size
			s.nugetAuthorities[key] = prior
		}
		return prior, nil
	}
	if err := s.nuGetAuthorityOwnerLocked(group, in); err != nil {
		return NuGetGroupAuthority{}, err
	}
	in.CreatedAt = time.Now().UTC()
	if s.nugetAuthorities == nil {
		s.nugetAuthorities = map[string]NuGetGroupAuthority{}
	}
	s.nugetAuthorities[key] = in
	return in, nil
}
func (s *MemoryStore) GetNuGetGroupAuthority(ctx context.Context, groupID, id, version string) (NuGetGroupAuthority, error) {
	if err := ctx.Err(); err != nil {
		return NuGetGroupAuthority{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	in, ok := s.nugetAuthorities[nuGetAuthorityKey(groupID, id, version)]
	if !ok {
		return NuGetGroupAuthority{}, ErrNotFound
	}
	group, ok := s.hostedGroups[groupID]
	if !ok || group.Format != FormatNuGet {
		return NuGetGroupAuthority{}, ErrNotFound
	}
	return in, nil
}

func (s *MemoryStore) RevalidateNuGetGroupAuthority(ctx context.Context, groupVersion, priorVersion string, in NuGetGroupAuthority) (NuGetGroupAuthority, error) {
	if !validNuGetAuthority(in) || in.Digest == "" {
		return NuGetGroupAuthority{}, ErrInvalidNuGetPublication
	}
	if err := ctx.Err(); err != nil {
		return NuGetGroupAuthority{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	group, ok := s.hostedGroups[in.GroupID]
	if !ok || group.Format != FormatNuGet {
		return NuGetGroupAuthority{}, ErrNotFound
	}
	if group.Version != groupVersion {
		return NuGetGroupAuthority{}, ErrVersionConflict
	}
	key := nuGetAuthorityKey(in.GroupID, in.PackageID, in.Version)
	prior, ok := s.nugetAuthorities[key]
	if !ok {
		return NuGetGroupAuthority{}, ErrNotFound
	}
	if prior.RepositoryID != in.RepositoryID || prior.Digest == "" || prior.Digest != in.Digest || prior.Size != in.Size {
		return NuGetGroupAuthority{}, ErrNuGetPublicationConflict
	}
	if prior.RepositoryVersion != priorVersion && prior.RepositoryVersion != in.RepositoryVersion {
		return NuGetGroupAuthority{}, ErrVersionConflict
	}
	if err := s.nuGetAuthorityOwnerLocked(group, in); err != nil {
		return NuGetGroupAuthority{}, err
	}
	prior.RepositoryVersion = in.RepositoryVersion
	s.nugetAuthorities[key] = prior
	return prior, nil
}
