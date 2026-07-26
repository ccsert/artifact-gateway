package repository

import (
	"context"
	"time"
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
	s.conanSessions[session.ID] = session
	s.conanUploads[session.ID] = map[string]string{}
	return session, nil
}

func (s *MemoryStore) GetConanPublishSession(_ context.Context, id string) (ConanPublishSession, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	session, ok := s.conanSessions[id]
	if !ok {
		return ConanPublishSession{}, ErrNotFound
	}
	return session, nil
}

func (s *MemoryStore) MarkConanPublishObject(_ context.Context, sessionID, name, objectKey string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.conanSessions[sessionID]
	if !ok {
		return ErrNotFound
	}
	if session.State != "open" || time.Now().After(session.ExpiresAt) {
		return ErrDisabled
	}
	for _, object := range session.Objects {
		if object.Name == name {
			s.conanUploads[sessionID][name] = objectKey
			return nil
		}
	}
	return ErrNotFound
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

func (s *MemoryStore) CommitConanPublishSession(_ context.Context, sessionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.conanSessions[sessionID]
	if !ok {
		return ErrNotFound
	}
	if session.State != "open" || time.Now().After(session.ExpiresAt) {
		return ErrDisabled
	}
	if len(s.conanUploads[sessionID]) != len(session.Objects) {
		return ErrDisabled
	}
	session.State = "committed"
	s.conanSessions[sessionID] = session
	return nil
}
