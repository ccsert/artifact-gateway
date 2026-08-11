package repository

import (
	"context"
	"sort"
	"strings"
	"time"
)

type UserListQuery struct {
	Search string
	Role   string
	State  string
	Limit  int
	Offset int
}

type UserPage struct {
	Items  []User
	Total  int
	Offset int
	Limit  int
}

type UserUpdate struct {
	ID          string
	DisplayName *string
	Email       *string
	Description *string
	Role        *string
	State       *string
}

// UserStore persists local user accounts. SecretHash is a bcrypt hash; it is
// never returned by the management surface (handlers project to a safe shape).
type UserStore interface {
	CreateUser(context.Context, User) (User, error)
	ListUsers(context.Context, UserListQuery) (UserPage, error)
	GetUser(context.Context, string) (User, error)
	GetUserByName(context.Context, string) (User, error)
	UpdateUser(context.Context, UserUpdate, string) (User, error)
	DeleteUser(context.Context, string) error
	RecordUserLoginSuccess(context.Context, string, time.Time) (User, error)
	RecordUserLoginFailure(context.Context, string, time.Time, int, time.Duration) (User, error)
	UpdateUserPassword(context.Context, string, string, string, bool) (User, error)
	RevokeUserSessions(context.Context, string, string) (User, error)
}

func (s *MemoryStore) CreateUser(_ context.Context, user User) (User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, existing := range s.users {
		if strings.EqualFold(existing.Name, user.Name) {
			return User{}, ErrNameExists
		}
	}
	user.State = UserActive
	user.CreatedAt = time.Now().UTC()
	user.UpdatedAt = user.CreatedAt
	if user.SecretHash != "" {
		user.PasswordChangedAt = timePointer(user.CreatedAt)
	}
	user.SessionVersion = 1
	user.Version = "1"
	s.users[user.ID] = user
	return user, nil
}

func (s *MemoryStore) ListUsers(_ context.Context, query UserListQuery) (UserPage, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	search := strings.ToLower(strings.TrimSpace(query.Search))
	filtered := make([]User, 0, len(s.users))
	for _, u := range s.users {
		if search != "" && !strings.Contains(strings.ToLower(u.Name+" "+u.DisplayName+" "+u.Email), search) {
			continue
		}
		if query.Role != "" && u.Role != query.Role || query.State != "" && u.State != query.State {
			continue
		}
		filtered = append(filtered, u)
	}
	sort.Slice(filtered, func(i, j int) bool {
		if filtered[i].CreatedAt.Equal(filtered[j].CreatedAt) {
			return filtered[i].ID < filtered[j].ID
		}
		return filtered[i].CreatedAt.Before(filtered[j].CreatedAt)
	})
	page := UserPage{Total: len(filtered), Offset: maxInt(query.Offset, 0), Limit: query.Limit}
	if page.Limit <= 0 || page.Limit > 500 {
		page.Limit = 100
	}
	if page.Offset > len(filtered) {
		page.Offset = len(filtered)
	}
	end := minInt(page.Offset+page.Limit, len(filtered))
	page.Items = append([]User(nil), filtered[page.Offset:end]...)
	return page, nil
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
		if strings.EqualFold(user.Name, name) {
			return user, nil
		}
	}
	return User{}, ErrNotFound
}

func (s *MemoryStore) UpdateUser(_ context.Context, update UserUpdate, expectedVersion string) (User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.users[update.ID]
	if !ok {
		return User{}, ErrNotFound
	}
	if current.Version != expectedVersion {
		return User{}, ErrVersionConflict
	}
	if removesLastActiveAdmin(current, update.Role, update.State) && !hasOtherActiveAdmin(s.users, current.ID) {
		return User{}, ErrLastActiveAdmin
	}
	if update.Role != nil {
		current.Role = *update.Role
	}
	if update.State != nil {
		current.State = *update.State
	}
	if update.DisplayName != nil {
		current.DisplayName = *update.DisplayName
	}
	if update.Email != nil {
		current.Email = *update.Email
	}
	if update.Description != nil {
		current.Description = *update.Description
	}
	current.Version = nextHostedGroupVersion(current.Version)
	current.UpdatedAt = time.Now().UTC()
	s.users[update.ID] = current
	return current, nil
}

func (s *MemoryStore) DeleteUser(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	user, ok := s.users[id]
	if !ok {
		return ErrNotFound
	}
	if user.Role == "admin" && user.State == UserActive && !hasOtherActiveAdmin(s.users, user.ID) {
		return ErrLastActiveAdmin
	}
	delete(s.users, id)
	for identityID, identity := range s.userIdentities {
		if identity.UserID == id {
			delete(s.userIdentities, identityID)
		}
	}
	for sessionID, session := range s.userSessions {
		if session.UserID == id {
			delete(s.userSessions, sessionID)
		}
	}
	return nil
}

func removesLastActiveAdmin(current User, role, state *string) bool {
	if current.Role != "admin" || current.State != UserActive {
		return false
	}
	nextRole, nextState := current.Role, current.State
	if role != nil {
		nextRole = *role
	}
	if state != nil {
		nextState = *state
	}
	return nextRole != "admin" || nextState != UserActive
}

func hasOtherActiveAdmin(users map[string]User, excludedID string) bool {
	for _, user := range users {
		if user.ID != excludedID && user.Role == "admin" && user.State == UserActive {
			return true
		}
	}
	return false
}

func (s *MemoryStore) RecordUserLoginSuccess(_ context.Context, id string, occurredAt time.Time) (User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	user, ok := s.users[id]
	if !ok {
		return User{}, ErrNotFound
	}
	user.LastLoginAt = timePointer(occurredAt.UTC())
	user.FailedLoginAttempts = 0
	user.LockedUntil = nil
	user.Version = nextHostedGroupVersion(user.Version)
	user.UpdatedAt = occurredAt.UTC()
	s.users[id] = user
	return user, nil
}

func (s *MemoryStore) RecordUserLoginFailure(_ context.Context, id string, occurredAt time.Time, maxAttempts int, lockout time.Duration) (User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	user, ok := s.users[id]
	if !ok {
		return User{}, ErrNotFound
	}
	user.FailedLoginAttempts++
	if maxAttempts > 0 && user.FailedLoginAttempts >= maxAttempts && lockout > 0 {
		until := occurredAt.UTC().Add(lockout)
		user.LockedUntil = &until
	}
	user.Version = nextHostedGroupVersion(user.Version)
	user.UpdatedAt = occurredAt.UTC()
	s.users[id] = user
	return user, nil
}

func (s *MemoryStore) UpdateUserPassword(_ context.Context, id, secretHash, expectedVersion string, mustChange bool) (User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	user, ok := s.users[id]
	if !ok {
		return User{}, ErrNotFound
	}
	if user.Version != expectedVersion {
		return User{}, ErrVersionConflict
	}
	user.SecretHash = secretHash
	user.PasswordChangedAt = timePointer(time.Now().UTC())
	user.MustChangePassword = mustChange
	user.FailedLoginAttempts = 0
	user.LockedUntil = nil
	user.SessionVersion++
	user.Version = nextHostedGroupVersion(user.Version)
	now := time.Now().UTC()
	user.UpdatedAt = now
	s.users[id] = user
	s.revokeUserSessionRecordsLocked(id, now)
	return user, nil
}

func (s *MemoryStore) RevokeUserSessions(_ context.Context, id, expectedVersion string) (User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	user, ok := s.users[id]
	if !ok {
		return User{}, ErrNotFound
	}
	if user.Version != expectedVersion {
		return User{}, ErrVersionConflict
	}
	user.SessionVersion++
	user.Version = nextHostedGroupVersion(user.Version)
	now := time.Now().UTC()
	user.UpdatedAt = now
	s.users[id] = user
	s.revokeUserSessionRecordsLocked(id, now)
	return user, nil
}

func timePointer(value time.Time) *time.Time {
	return &value
}

func maxInt(left, right int) int {
	if left > right {
		return left
	}
	return right
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}
