package repository

import "context"

func (s *MemoryStore) SetPyPIYank(ctx context.Context, mutation PyPIYankMutation) ([]PyPIFile, error) {
	release, err := lockArtifactDistributionCoordinates(ctx, s, mutation.RepositoryID, FormatPyPI, []string{mutation.Project + "@" + mutation.Version})
	if err != nil {
		return nil, err
	}
	defer release()
	s.mu.Lock()
	defer s.mu.Unlock()
	repo, exists := s.hostedRepositories[mutation.RepositoryID]
	if !exists || repo.Format != FormatPyPI || repo.Type != RepositoryTypeHosted || repo.State != RepositoryActive {
		return nil, ErrNotFound
	}
	var files []PyPIFile
	for _, file := range s.pypiFiles {
		if file.RepositoryID == mutation.RepositoryID && file.Project == mutation.Project && file.Version == mutation.Version && file.State == "visible" {
			files = append(files, file)
		}
	}
	result, changed, err := applyPyPIYank(files, mutation)
	if err != nil {
		return nil, err
	}
	for _, file := range result {
		s.pypiFiles[pypiFileKey(file.RepositoryID, file.Filename)] = file
	}
	s.appendAuditLocked(withRequestContextAudit(ctx, mutation.auditRecord(result, changed)))
	return result, nil
}
