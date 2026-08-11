package repository

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

func cloneAuthorizationRole(role AuthorizationRole) AuthorizationRole {
	role.Scopes = append([]string(nil), role.Scopes...)
	return role
}

func (s *MemoryStore) CreateAuthorizationRole(_ context.Context, role AuthorizationRole) (AuthorizationRole, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	name := strings.TrimSpace(role.Name)
	if name == "" {
		return AuthorizationRole{}, ErrNameExists
	}
	for _, existing := range s.authorizationRoles {
		if strings.EqualFold(existing.Name, name) {
			return AuthorizationRole{}, ErrAuthorizationRoleNameExists
		}
	}
	now := time.Now().UTC()
	if role.ID == "" {
		role.ID = uuid.NewString()
	}
	role.Name = name
	role.Version = "1"
	role.CreatedAt = now
	role.UpdatedAt = now
	role = cloneAuthorizationRole(role)
	s.authorizationRoles[role.ID] = role
	return cloneAuthorizationRole(role), nil
}

func (s *MemoryStore) ListAuthorizationRoles(_ context.Context) ([]AuthorizationRole, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]AuthorizationRole, 0, len(s.authorizationRoles))
	for _, role := range s.authorizationRoles {
		items = append(items, cloneAuthorizationRole(role))
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
	return items, nil
}

func (s *MemoryStore) GetAuthorizationRole(_ context.Context, id string) (AuthorizationRole, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	role, ok := s.authorizationRoles[id]
	if !ok {
		return AuthorizationRole{}, ErrNotFound
	}
	return cloneAuthorizationRole(role), nil
}

func (s *MemoryStore) UpdateAuthorizationRole(_ context.Context, role AuthorizationRole, expectedVersion string) (AuthorizationRole, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.authorizationRoles[role.ID]
	if !ok {
		return AuthorizationRole{}, ErrNotFound
	}
	if current.Version != expectedVersion {
		return AuthorizationRole{}, ErrVersionConflict
	}
	name := strings.TrimSpace(role.Name)
	if name == "" {
		return AuthorizationRole{}, ErrNameExists
	}
	for id, existing := range s.authorizationRoles {
		if id != role.ID && strings.EqualFold(existing.Name, name) {
			return AuthorizationRole{}, ErrAuthorizationRoleNameExists
		}
	}
	role.Name = name
	role.Version = nextHostedGroupVersion(current.Version)
	role.CreatedAt = current.CreatedAt
	role.UpdatedAt = time.Now().UTC()
	role = cloneAuthorizationRole(role)
	s.authorizationRoles[role.ID] = role
	return cloneAuthorizationRole(role), nil
}

func (s *MemoryStore) DeleteAuthorizationRole(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.authorizationRoles[id]; !ok {
		return ErrNotFound
	}
	delete(s.authorizationRoles, id)
	return nil
}
