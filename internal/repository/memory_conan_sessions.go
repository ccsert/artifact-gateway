package repository

import (
	"context"
)

func (s *MemoryStore) CreateConanPublishSession(_ context.Context, session ConanPublishSession) (ConanPublishSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.conanSessions[session.ID]; exists {
		return ConanPublishSession{}, ErrNameExists
	}
	if session.State == "" {
		session.State = "open"
	}
	session.Native, session.Version = false, 1
	s.conanSessions[session.ID] = cloneConanSession(session)
	s.conanUploads[session.ID] = map[string]string{}
	return cloneConanSession(session), nil
}

func (s *MemoryStore) GetConanPublishSession(_ context.Context, id string) (ConanPublishSession, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	session, ok := s.conanSessions[id]
	if !ok {
		return ConanPublishSession{}, ErrNotFound
	}
	return cloneConanSession(session), nil
}

func (s *MemoryStore) ListConanPublishUploads(_ context.Context, sessionID string) (map[string]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	uploads, ok := s.conanUploads[sessionID]
	if !ok {
		return nil, ErrNotFound
	}
	out := make(map[string]string, len(uploads))
	for name, key := range uploads {
		out[name] = key
	}
	return out, nil
}
