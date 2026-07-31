package repository

import (
	"context"
	"sort"
	"time"
)

// UserStore persists local user accounts. SecretHash is a bcrypt hash; it is
// never returned by the management surface (handlers project to a safe shape).
type UserStore interface {
	CreateUser(context.Context, User) (User, error)
	ListUsers(context.Context) ([]User, error)
	GetUser(context.Context, string) (User, error)
	GetUserByName(context.Context, string) (User, error)
	UpdateUser(context.Context, User, string) (User, error)
	DeleteUser(context.Context, string) error
}

func (s *MemoryStore) CreateUser(_ context.Context, user User) (User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, existing := range s.users {
		if existing.Name == user.Name {
			return User{}, ErrNameExists
		}
	}
	user.State = UserActive
	user.CreatedAt = time.Now().UTC()
	user.UpdatedAt = user.CreatedAt
	user.Version = "1"
	s.users[user.ID] = user
	return user, nil
}

func (s *MemoryStore) ListUsers(_ context.Context) ([]User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]User, 0, len(s.users))
	for _, u := range s.users {
		out = append(out, u)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

func (s *MemoryStore) GetUser(_ context.Context, id string) (User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	user, ok := s.users[id]
	if !ok {
		return User{}, ErrNotFound
	}
	return user, nil
}

func (s *MemoryStore) GetUserByName(_ context.Context, name string) (User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, user := range s.users {
		if user.Name == name {
			return user, nil
		}
	}
	return User{}, ErrNotFound
}

func (s *MemoryStore) UpdateUser(_ context.Context, user User, expectedVersion string) (User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.users[user.ID]
	if !ok {
		return User{}, ErrNotFound
	}
	if current.Version != expectedVersion {
		return User{}, ErrVersionConflict
	}
	current.Role = user.Role
	if user.State != "" {
		current.State = user.State
	}
	current.Version = nextHostedGroupVersion(current.Version)
	current.UpdatedAt = time.Now().UTC()
	s.users[user.ID] = current
	return current, nil
}

func (s *MemoryStore) DeleteUser(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.users[id]; !ok {
		return ErrNotFound
	}
	delete(s.users, id)
	return nil
}
