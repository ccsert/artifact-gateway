package repository

import (
	"context"
	"sort"
	"time"
)

func (s *MemoryStore) CreateRawGroup(_ context.Context, group Group) (Group, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.rawGroups[group.Name]; ok {
		return Group{}, ErrNameExists
	}
	group.CreatedAt = time.Now().UTC()
	normalizeGroup(&group)
	s.rawGroups[group.Name] = group
	return group, nil
}
func (s *MemoryStore) GetRawGroup(_ context.Context, name string) (Group, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	group, ok := s.rawGroups[name]
	if !ok {
		return Group{}, ErrNotFound
	}
	return group, nil
}
func (s *MemoryStore) DisableRawGroup(_ context.Context, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	group, ok := s.rawGroups[name]
	if !ok {
		return ErrNotFound
	}
	group.Enabled = false
	s.rawGroups[name] = group
	return nil
}

func (s *MemoryStore) CreateConanGroup(ctx context.Context, group Group) (Group, error) {
	return s.createConanGroup(ctx, group)
}
func (s *MemoryStore) createConanGroup(_ context.Context, group Group) (Group, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.conanGroups[group.Name]; exists {
		return Group{}, ErrNameExists
	}
	group.CreatedAt = time.Now().UTC()
	if group.CacheQuotaBytes == 0 {
		group.CacheQuotaBytes = 1 << 30
	}
	normalizeGroup(&group)
	s.conanGroups[group.Name] = group
	return group, nil
}
func (s *MemoryStore) GetConanGroup(_ context.Context, name string) (Group, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	group, exists := s.conanGroups[name]
	if !exists {
		return Group{}, ErrNotFound
	}
	return group, nil
}
func (s *MemoryStore) DisableConanGroup(_ context.Context, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	group, exists := s.conanGroups[name]
	if !exists {
		return ErrNotFound
	}
	group.Enabled = false
	s.conanGroups[name] = group
	return nil
}

func (s *MemoryStore) CreateGroup(_ context.Context, group Group) (Group, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.groups[group.Name]; exists {
		return Group{}, ErrNameExists
	}
	group.CreatedAt = time.Now().UTC()
	normalizeGroup(&group)
	s.groups[group.Name] = group
	return group, nil
}

func normalizeGroup(group *Group) {
	group.Enabled = true
	sort.Slice(group.Members, func(i, j int) bool { return group.Members[i].Position < group.Members[j].Position })
}

func (s *MemoryStore) GetGroup(_ context.Context, name string) (Group, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	group, exists := s.groups[name]
	if !exists {
		return Group{}, ErrNotFound
	}
	return group, nil
}

func (s *MemoryStore) DisableGroup(_ context.Context, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	group, exists := s.groups[name]
	if !exists {
		return ErrNotFound
	}
	group.Enabled = false
	s.groups[name] = group
	return nil
}

func (s *MemoryStore) CreateMavenGroup(_ context.Context, group Group) (Group, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.mavenGroups[group.Name]; exists {
		return Group{}, ErrNameExists
	}
	group.CreatedAt = time.Now().UTC()
	normalizeGroup(&group)
	s.mavenGroups[group.Name] = group
	return group, nil
}

func (s *MemoryStore) GetMavenGroup(_ context.Context, name string) (Group, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	group, exists := s.mavenGroups[name]
	if !exists {
		return Group{}, ErrNotFound
	}
	return group, nil
}

func (s *MemoryStore) DisableMavenGroup(_ context.Context, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	group, exists := s.mavenGroups[name]
	if !exists {
		return ErrNotFound
	}
	group.Enabled = false
	s.mavenGroups[name] = group
	return nil
}
