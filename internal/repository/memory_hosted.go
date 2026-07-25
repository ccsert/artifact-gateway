package repository

import (
	"context"
	"sort"
	"strconv"
	"time"
)

func (s *MemoryStore) CreateHostedRepository(_ context.Context, repo HostedRepository) (HostedRepository, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, existing := range s.hostedRepositories {
		if existing.Name == repo.Name {
			return HostedRepository{}, ErrNameExists
		}
	}
	repo.State = RepositoryActive
	repo.Version = "1"
	repo.CreatedAt = time.Now().UTC()
	s.hostedRepositories[repo.ID] = repo
	return repo, nil
}

func (s *MemoryStore) CreateHostedRepositoryIdempotently(_ context.Context, repo HostedRepository, actor, key, payload string) (HostedRepository, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	recordKey := actor + "\x00/repositories\x00" + key
	if record, ok := s.idempotencyRecords[recordKey]; ok && time.Now().UTC().Before(record.expiresAt) {
		if record.payload != payload {
			return HostedRepository{}, false, ErrIdempotencyConflict
		}
		return s.hostedRepositories[record.repositoryID], true, nil
	}
	for _, existing := range s.hostedRepositories {
		if existing.Name == repo.Name {
			return HostedRepository{}, false, ErrNameExists
		}
	}
	repo.State, repo.Version, repo.CreatedAt = RepositoryActive, "1", time.Now().UTC()
	s.hostedRepositories[repo.ID] = repo
	s.idempotencyRecords[recordKey] = idempotencyRecord{payload: payload, repositoryID: repo.ID, expiresAt: time.Now().UTC().Add(24 * time.Hour)}
	return repo, false, nil
}

func (s *MemoryStore) ListHostedRepositories(_ context.Context, limit int, after string) ([]HostedRepository, string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	items := make([]HostedRepository, 0, len(s.hostedRepositories))
	for _, repo := range s.hostedRepositories {
		items = append(items, repo)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	start := 0
	if after != "" {
		found := false
		for i, repo := range items {
			if repo.ID == after {
				start = i + 1
				found = true
				break
			}
		}
		if !found {
			return nil, "", ErrNotFound
		}
	}
	end := start + limit
	next := ""
	if end < len(items) {
		next = items[end-1].ID
	} else if start > len(items) {
		start = len(items)
		end = start
	} else {
		end = len(items)
	}
	return items[start:end], next, nil
}

func (s *MemoryStore) GetHostedRepository(_ context.Context, id string) (HostedRepository, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	repo, ok := s.hostedRepositories[id]
	if !ok {
		return HostedRepository{}, ErrNotFound
	}
	return repo, nil
}

func (s *MemoryStore) GetHostedRepositoryByName(_ context.Context, name string) (HostedRepository, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, repo := range s.hostedRepositories {
		if repo.Name == name {
			return repo, nil
		}
	}
	return HostedRepository{}, ErrNotFound
}

func (s *MemoryStore) DisableHostedRepository(_ context.Context, id string) (HostedRepository, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	repo, ok := s.hostedRepositories[id]
	if !ok || repo.State != RepositoryActive {
		return HostedRepository{}, ErrNotFound
	}
	repo.State = RepositoryDeleting
	repo.Version = "2"
	s.hostedRepositories[id] = repo
	return repo, nil
}

func cloneHostedGroup(group HostedGroup) HostedGroup {
	group.Members = append([]GroupMember(nil), group.Members...)
	return group
}

func nextHostedGroupVersion(version string) string {
	current, err := strconv.ParseInt(version, 10, 64)
	if err != nil || current < 1 {
		return "1"
	}
	return strconv.FormatInt(current+1, 10)
}

func (s *MemoryStore) CreateHostedGroupIdempotently(_ context.Context, group HostedGroup, actor, key, payload string) (HostedGroup, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	recordKey := actor + "\x00/groups\x00" + key
	if record, ok := s.idempotencyRecords[recordKey]; ok && time.Now().UTC().Before(record.expiresAt) {
		if record.payload != payload {
			return HostedGroup{}, false, ErrIdempotencyConflict
		}
		return cloneHostedGroup(s.hostedGroups[record.repositoryID]), true, nil
	}
	for _, existing := range s.hostedGroups {
		if existing.Name == group.Name {
			return HostedGroup{}, false, ErrNameExists
		}
	}
	group.Version = "1"
	group.Members = append([]GroupMember(nil), group.Members...)
	sort.Slice(group.Members, func(i, j int) bool { return group.Members[i].Position < group.Members[j].Position })
	s.hostedGroups[group.ID] = group
	s.idempotencyRecords[recordKey] = idempotencyRecord{payload: payload, repositoryID: group.ID, expiresAt: time.Now().UTC().Add(24 * time.Hour)}
	return cloneHostedGroup(group), false, nil
}

func (s *MemoryStore) ListHostedGroups(_ context.Context, limit int, after string) ([]HostedGroup, string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	items := make([]HostedGroup, 0, len(s.hostedGroups))
	for _, group := range s.hostedGroups {
		items = append(items, cloneHostedGroup(group))
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	start := 0
	if after != "" {
		found := false
		for i, group := range items {
			if group.ID == after {
				start, found = i+1, true
				break
			}
		}
		if !found {
			return nil, "", ErrNotFound
		}
	}
	end := start + limit
	next := ""
	if end < len(items) {
		next = items[end-1].ID
	} else {
		end = len(items)
	}
	return items[start:end], next, nil
}

func (s *MemoryStore) GetHostedGroup(_ context.Context, id string) (HostedGroup, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	group, ok := s.hostedGroups[id]
	if !ok {
		return HostedGroup{}, ErrNotFound
	}
	return cloneHostedGroup(group), nil
}

func (s *MemoryStore) ReplaceHostedGroup(_ context.Context, group HostedGroup, expectedVersion string) (HostedGroup, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	stored, ok := s.hostedGroups[group.ID]
	if !ok {
		return HostedGroup{}, ErrNotFound
	}
	if stored.Version != expectedVersion {
		return HostedGroup{}, ErrVersionConflict
	}
	group.Version = nextHostedGroupVersion(stored.Version)
	group.Members = append([]GroupMember(nil), group.Members...)
	sort.Slice(group.Members, func(i, j int) bool { return group.Members[i].Position < group.Members[j].Position })
	s.hostedGroups[group.ID] = group
	return cloneHostedGroup(group), nil
}

func (s *MemoryStore) ReplaceHostedGroupMembers(_ context.Context, id string, members []GroupMember, expectedVersion string) (HostedGroup, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	group, ok := s.hostedGroups[id]
	if !ok {
		return HostedGroup{}, ErrNotFound
	}
	if group.Version != expectedVersion {
		return HostedGroup{}, ErrVersionConflict
	}
	group.Members = append([]GroupMember(nil), members...)
	sort.Slice(group.Members, func(i, j int) bool { return group.Members[i].Position < group.Members[j].Position })
	group.Version = nextHostedGroupVersion(group.Version)
	s.hostedGroups[id] = group
	return cloneHostedGroup(group), nil
}

func (s *MemoryStore) DeleteHostedGroup(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.hostedGroups[id]; !ok {
		return ErrNotFound
	}
	delete(s.hostedGroups, id)
	return nil
}

func cloneRepositoryGrantSet(set RepositoryGrantSet) RepositoryGrantSet {
	set.Grants = append([]RepositoryGrant(nil), set.Grants...)
	for i := range set.Grants {
		set.Grants[i].Scopes = append([]string(nil), set.Grants[i].Scopes...)
	}
	return set
}

func (s *MemoryStore) GetRepositoryGrants(_ context.Context, repositoryID string) (RepositoryGrantSet, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, ok := s.hostedRepositories[repositoryID]; !ok {
		return RepositoryGrantSet{}, ErrNotFound
	}
	set, ok := s.repositoryGrants[repositoryID]
	if !ok {
		return RepositoryGrantSet{Version: "1", Grants: []RepositoryGrant{}}, nil
	}
	return cloneRepositoryGrantSet(set), nil
}

func (s *MemoryStore) ReplaceRepositoryGrants(_ context.Context, repositoryID string, grants []RepositoryGrant, expectedVersion string) (RepositoryGrantSet, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.hostedRepositories[repositoryID]; !ok {
		return RepositoryGrantSet{}, ErrNotFound
	}
	set, ok := s.repositoryGrants[repositoryID]
	if !ok {
		set.Version = "1"
	}
	if set.Version != expectedVersion {
		return RepositoryGrantSet{}, ErrVersionConflict
	}
	set.Version = nextHostedGroupVersion(set.Version)
	set.Grants = append([]RepositoryGrant{}, grants...)
	for i := range set.Grants {
		set.Grants[i].Scopes = append([]string(nil), set.Grants[i].Scopes...)
	}
	s.repositoryGrants[repositoryID] = set
	return cloneRepositoryGrantSet(set), nil
}

func defaultRepositoryRetentionPolicy() RepositoryRetentionPolicy {
	return RepositoryRetentionPolicy{Version: "1", KeepDays: 30, MinimumVersions: 1}
}

func (s *MemoryStore) GetRepositoryRetentionPolicy(_ context.Context, repositoryID string) (RepositoryRetentionPolicy, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, ok := s.hostedRepositories[repositoryID]; !ok {
		return RepositoryRetentionPolicy{}, ErrNotFound
	}
	policy, ok := s.retentionPolicies[repositoryID]
	if !ok {
		return defaultRepositoryRetentionPolicy(), nil
	}
	return policy, nil
}

func (s *MemoryStore) ReplaceRepositoryRetentionPolicy(_ context.Context, repositoryID string, policy RepositoryRetentionPolicy, expectedVersion string) (RepositoryRetentionPolicy, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.hostedRepositories[repositoryID]; !ok {
		return RepositoryRetentionPolicy{}, ErrNotFound
	}
	current, ok := s.retentionPolicies[repositoryID]
	if !ok {
		current = defaultRepositoryRetentionPolicy()
	}
	if current.Version != expectedVersion {
		return RepositoryRetentionPolicy{}, ErrVersionConflict
	}
	policy.Version = nextHostedGroupVersion(current.Version)
	s.retentionPolicies[repositoryID] = policy
	return policy, nil
}
