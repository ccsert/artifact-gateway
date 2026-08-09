package repository

import (
	"context"
	"sort"
	"strings"
	"time"
)

func pypiFileKey(repositoryID, filename string) string {
	return repositoryID + "\x00" + filename
}

func clonePyPIFile(file PyPIFile) PyPIFile { return file }

func (s *MemoryStore) LockPyPIObject(_ context.Context, objectKey string) (func(), error) {
	return s.lockMemoryObject(s.pypiObjectLocks, objectKey)
}

func (s *MemoryStore) PublishPyPIFile(_ context.Context, file PyPIFile) (PyPIFile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := pypiFileKey(file.RepositoryID, file.Filename)
	if existing, ok := s.pypiFiles[key]; ok {
		if existing.Digest == file.Digest {
			return clonePyPIFile(existing), nil
		}
		return PyPIFile{}, ErrNameExists
	}
	var used int64
	for _, candidate := range s.pypiFiles {
		if candidate.RepositoryID == file.RepositoryID && candidate.State == "visible" {
			used += candidate.Size
		}
	}
	if quota := s.capacityQuotas[file.RepositoryID]; quota > 0 && used+file.Size > quota {
		return PyPIFile{}, ErrQuotaExceeded
	}
	now := time.Now().UTC()
	if file.CreatedAt.IsZero() {
		file.CreatedAt = now
	}
	if file.CachedAt.IsZero() {
		file.CachedAt = now
	}
	file.State = "visible"
	s.pypiFiles[key] = file
	return clonePyPIFile(file), nil
}

func (s *MemoryStore) PublishPyPIVersion(_ context.Context, files []PyPIFile) ([]PyPIFile, error) {
	if len(files) == 0 {
		return nil, ErrNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var used, additional int64
	for _, candidate := range s.pypiFiles {
		if candidate.RepositoryID == files[0].RepositoryID && candidate.State == "visible" {
			used += candidate.Size
		}
	}
	for _, file := range files {
		if file.RepositoryID != files[0].RepositoryID || file.Project != files[0].Project || file.Version != files[0].Version || file.ObjectKey == "" {
			return nil, ErrDisabled
		}
		if existing, ok := s.pypiFiles[pypiFileKey(file.RepositoryID, file.Filename)]; ok {
			if existing.State != "visible" || existing.Digest != file.Digest || existing.Project != file.Project || existing.Version != file.Version {
				return nil, ErrNameExists
			}
			continue
		}
		additional += file.Size
	}
	if quota := s.capacityQuotas[files[0].RepositoryID]; quota > 0 && used+additional > quota {
		return nil, ErrQuotaExceeded
	}
	now := time.Now().UTC()
	result := make([]PyPIFile, 0, len(files))
	for _, file := range files {
		key := pypiFileKey(file.RepositoryID, file.Filename)
		if existing, ok := s.pypiFiles[key]; ok {
			result = append(result, existing)
			continue
		}
		if file.CreatedAt.IsZero() {
			file.CreatedAt = now
		}
		if file.CachedAt.IsZero() {
			file.CachedAt = now
		}
		file.State = "visible"
		s.pypiFiles[key] = file
		result = append(result, file)
	}
	return result, nil
}

func (s *MemoryStore) SyncPyPIProxyFiles(_ context.Context, repositoryID, project string, files []PyPIFile) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	for _, file := range files {
		key := pypiFileKey(repositoryID, file.Filename)
		if existing, ok := s.pypiFiles[key]; ok {
			if existing.Digest != "" && existing.Digest != file.Digest {
				return ErrUpstreamChanged
			}
			file.ObjectKey = existing.ObjectKey
			file.Size = existing.Size
			file.CachedAt = existing.CachedAt
			file.CreatedAt = existing.CreatedAt
			file.State = existing.State
			file.DeletedAt = existing.DeletedAt
			file.CollectedAt = existing.CollectedAt
		}
		file.RepositoryID = repositoryID
		file.Project = project
		if file.CreatedAt.IsZero() {
			file.CreatedAt = now
		}
		if file.State == "" {
			file.State = "visible"
		}
		s.pypiFiles[key] = file
	}
	return nil
}

func (s *MemoryStore) CachePyPIProxyFile(_ context.Context, incoming PyPIFile) (PyPIFile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := pypiFileKey(incoming.RepositoryID, incoming.Filename)
	file, ok := s.pypiFiles[key]
	if !ok || file.State != "visible" {
		return PyPIFile{}, ErrNotFound
	}
	if file.Digest != incoming.Digest || file.SourceURL != incoming.SourceURL {
		return PyPIFile{}, ErrUpstreamChanged
	}
	if file.ObjectKey != "" {
		return file, nil
	}
	var used int64
	for _, candidate := range s.pypiFiles {
		if candidate.RepositoryID == incoming.RepositoryID && candidate.State == "visible" {
			used += candidate.Size
		}
	}
	if quota := s.capacityQuotas[incoming.RepositoryID]; quota > 0 && used+incoming.Size > quota {
		return PyPIFile{}, ErrQuotaExceeded
	}
	file.ObjectKey = incoming.ObjectKey
	file.Size = incoming.Size
	file.CachedAt = incoming.CachedAt
	s.pypiFiles[key] = file
	return file, nil
}

func (s *MemoryStore) GetPyPIFile(_ context.Context, repositoryID, filename string) (PyPIFile, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	file, ok := s.pypiFiles[pypiFileKey(repositoryID, filename)]
	if !ok || file.State != "visible" {
		return PyPIFile{}, ErrNotFound
	}
	return clonePyPIFile(file), nil
}

func (s *MemoryStore) ListPyPIProjectFiles(_ context.Context, repositoryID, project string) ([]PyPIFile, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	files := make([]PyPIFile, 0)
	for _, file := range s.pypiFiles {
		if file.RepositoryID == repositoryID && file.Project == project && file.State == "visible" {
			files = append(files, clonePyPIFile(file))
		}
	}
	sort.Slice(files, func(i, j int) bool {
		if files[i].Version == files[j].Version {
			return files[i].Filename < files[j].Filename
		}
		return files[i].CreatedAt.After(files[j].CreatedAt)
	})
	if len(files) == 0 {
		return nil, ErrNotFound
	}
	return files, nil
}

func (s *MemoryStore) ListPyPIProjects(_ context.Context, repositoryID, prefix string, limit int, after string) ([]PyPIProjectSummary, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	projects := make(map[string]*PyPIProjectSummary)
	versions := make(map[string]map[string]struct{})
	for _, file := range s.pypiFiles {
		if file.RepositoryID != repositoryID || file.State != "visible" || !strings.HasPrefix(file.Project, prefix) || file.Project <= after {
			continue
		}
		summary := projects[file.Project]
		if summary == nil {
			summary = &PyPIProjectSummary{RepositoryID: repositoryID, Project: file.Project, CreatedAt: file.CreatedAt, UpdatedAt: file.CreatedAt}
			projects[file.Project] = summary
			versions[file.Project] = make(map[string]struct{})
		}
		summary.FileCount++
		versions[file.Project][file.Version] = struct{}{}
		if summary.Latest.Filename == "" || file.CreatedAt.After(summary.Latest.CreatedAt) {
			summary.Latest = clonePyPIFile(file)
		}
		if file.CreatedAt.Before(summary.CreatedAt) {
			summary.CreatedAt = file.CreatedAt
		}
		if file.CreatedAt.After(summary.UpdatedAt) {
			summary.UpdatedAt = file.CreatedAt
		}
	}
	names := make([]string, 0, len(projects))
	for name := range projects {
		names = append(names, name)
	}
	sort.Strings(names)
	if limit <= 0 || limit > len(names) {
		limit = len(names)
	}
	result := make([]PyPIProjectSummary, 0, limit)
	for _, name := range names[:limit] {
		projects[name].VersionCount = len(versions[name])
		result = append(result, *projects[name])
	}
	return result, nil
}

func (s *MemoryStore) TombstonePyPIVersion(_ context.Context, repositoryID, project, version string) ([]PyPIFile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	files := make([]PyPIFile, 0)
	for key, file := range s.pypiFiles {
		if file.RepositoryID == repositoryID && file.Project == project && file.Version == version && file.State == "visible" {
			file.State = "deleted"
			file.DeletedAt = now
			s.pypiFiles[key] = file
			files = append(files, file)
		}
	}
	if len(files) == 0 {
		return nil, ErrNotFound
	}
	coordinate := project + "@" + version
	s.artifactTombstones[repositoryID+"\x00"+string(FormatPyPI)+"\x00"+coordinate] = ArtifactTombstone{RepositoryID: repositoryID, Format: FormatPyPI, Coordinate: coordinate, TombstonedAt: now}
	return files, nil
}

func (s *MemoryStore) RestorePyPIVersion(ctx context.Context, repositoryID, project, version string) ([]PyPIFile, error) {
	s.mu.RLock()
	objectKeys := make([]string, 0)
	seen := make(map[string]bool)
	found, collected := false, false
	for _, file := range s.pypiFiles {
		if file.RepositoryID != repositoryID || file.Project != project || file.Version != version || file.State != "deleted" {
			continue
		}
		found = true
		collected = collected || !file.CollectedAt.IsZero()
		if file.ObjectKey != "" && !seen[file.ObjectKey] {
			seen[file.ObjectKey] = true
			objectKeys = append(objectKeys, file.ObjectKey)
		}
	}
	s.mu.RUnlock()
	if !found {
		return nil, ErrNotFound
	}
	if collected {
		return nil, ErrDisabled
	}
	release, err := LockObjectKeys(ctx, objectKeys, s.LockPyPIObject)
	if err != nil {
		return nil, err
	}
	defer release()

	s.mu.Lock()
	defer s.mu.Unlock()
	files := make([]PyPIFile, 0)
	found, collected = false, false
	for _, file := range s.pypiFiles {
		if file.RepositoryID == repositoryID && file.Project == project && file.Version == version && file.State == "deleted" {
			found = true
			collected = collected || !file.CollectedAt.IsZero()
		}
	}
	if !found {
		return nil, ErrNotFound
	}
	if collected {
		return nil, ErrDisabled
	}
	for key, file := range s.pypiFiles {
		if file.RepositoryID != repositoryID || file.Project != project || file.Version != version || file.State != "deleted" {
			continue
		}
		file.State = "visible"
		file.DeletedAt = time.Time{}
		s.pypiFiles[key] = file
		files = append(files, file)
	}
	if len(files) == 0 {
		return nil, ErrNotFound
	}
	delete(s.artifactTombstones, repositoryID+"\x00"+string(FormatPyPI)+"\x00"+project+"@"+version)
	return files, nil
}

func (s *MemoryStore) ListReclaimablePyPIObjects(_ context.Context, before time.Time, limit int) ([]PyPIObject, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	objects := make(map[string]PyPIObject)
	for _, file := range s.pypiFiles {
		if file.State != "deleted" || !file.CollectedAt.IsZero() || file.ObjectKey == "" {
			continue
		}
		reclaimable := true
		latestDeletion := file.DeletedAt
		for _, reference := range s.pypiFiles {
			if reference.ObjectKey != file.ObjectKey {
				continue
			}
			if reference.State == "visible" || reference.DeletedAt.IsZero() || reference.DeletedAt.After(before) {
				reclaimable = false
				break
			}
			if reference.DeletedAt.After(latestDeletion) {
				latestDeletion = reference.DeletedAt
			}
		}
		if reclaimable {
			objects[file.ObjectKey] = PyPIObject{RepositoryID: file.RepositoryID, ObjectKey: file.ObjectKey, Digest: file.Digest, Size: file.Size, DeletedAt: latestDeletion}
		}
	}
	result := make([]PyPIObject, 0, len(objects))
	for _, object := range objects {
		result = append(result, object)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].DeletedAt.Before(result[j].DeletedAt) })
	if limit > 0 && len(result) > limit {
		result = result[:limit]
	}
	return result, nil
}

func (s *MemoryStore) PyPIObjectHasVisibleReference(_ context.Context, objectKey string) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, file := range s.pypiFiles {
		if file.ObjectKey == objectKey && file.State == "visible" {
			return true, nil
		}
	}
	return false, nil
}

func (s *MemoryStore) MarkPyPIObjectCollected(_ context.Context, objectKey string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	for key, file := range s.pypiFiles {
		if file.ObjectKey == objectKey && file.State == "deleted" {
			file.CollectedAt = now
			s.pypiFiles[key] = file
		}
	}
	return nil
}
