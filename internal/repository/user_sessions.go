package repository

import (
	"context"
	"sort"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

func (s *MemoryStore) CreateUserSession(_ context.Context, session UserSession) (UserSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.users[session.UserID]; !ok {
		return UserSession{}, ErrNotFound
	}
	if session.ID == "" {
		session.ID = uuid.NewString()
	}
	if session.CreatedAt.IsZero() {
		session.CreatedAt = time.Now().UTC()
	}
	if !validUserSession(session) {
		return UserSession{}, ErrInvalidUserSession
	}
	s.userSessions[session.ID] = session
	return session, nil
}

func validUserSession(session UserSession) bool {
	return session.UserID != "" &&
		(session.Kind == UserSessionLocal || session.Kind == UserSessionOIDC) &&
		session.ExpiresAt.After(session.CreatedAt) &&
		utf8.RuneCountInString(session.IPAddress) <= 64 &&
		utf8.RuneCountInString(session.UserAgent) <= 512
}

func (s *MemoryStore) GetUserSession(_ context.Context, userID, sessionID string) (UserSession, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	session, ok := s.userSessions[sessionID]
	if !ok || session.UserID != userID {
		return UserSession{}, ErrNotFound
	}
	return session, nil
}

func (s *MemoryStore) ListUserSessions(_ context.Context, userID string, includeInactive bool) ([]UserSession, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, ok := s.users[userID]; !ok {
		return nil, ErrNotFound
	}
	now := time.Now().UTC()
	items := make([]UserSession, 0)
	for _, session := range s.userSessions {
		if session.UserID != userID || !includeInactive && (session.RevokedAt != nil || !session.ExpiresAt.After(now)) {
			continue
		}
		items = append(items, session)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].CreatedAt.Equal(items[j].CreatedAt) {
			return items[i].ID < items[j].ID
		}
		return items[i].CreatedAt.After(items[j].CreatedAt)
	})
	return items, nil
}

func (s *MemoryStore) RevokeUserSession(_ context.Context, userID, sessionID string) (UserSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.userSessions[sessionID]
	if !ok || session.UserID != userID {
		return UserSession{}, ErrNotFound
	}
	if session.RevokedAt == nil {
		now := time.Now().UTC()
		session.RevokedAt = &now
		s.userSessions[session.ID] = session
	}
	return session, nil
}

func (s *MemoryStore) RevokeAllUserSessionRecords(_ context.Context, userID string, occurredAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.users[userID]; !ok {
		return ErrNotFound
	}
	s.revokeUserSessionRecordsLocked(userID, occurredAt.UTC())
	return nil
}

func (s *MemoryStore) revokeUserSessionRecordsLocked(userID string, when time.Time) {
	for id, session := range s.userSessions {
		if session.UserID == userID && session.RevokedAt == nil {
			session.RevokedAt = &when
			s.userSessions[id] = session
		}
	}
}

func (s *MemoryStore) PruneExpiredUserSessions(_ context.Context, before time.Time, limit int) (int, error) {
	if limit <= 0 {
		return 0, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	type candidate struct {
		id        string
		expiresAt time.Time
	}
	candidates := make([]candidate, 0)
	for id, session := range s.userSessions {
		if !session.ExpiresAt.After(before) {
			candidates = append(candidates, candidate{id: id, expiresAt: session.ExpiresAt})
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].expiresAt.Equal(candidates[j].expiresAt) {
			return candidates[i].id < candidates[j].id
		}
		return candidates[i].expiresAt.Before(candidates[j].expiresAt)
	})
	if len(candidates) > limit {
		candidates = candidates[:limit]
	}
	for _, item := range candidates {
		delete(s.userSessions, item.id)
	}
	return len(candidates), nil
}
