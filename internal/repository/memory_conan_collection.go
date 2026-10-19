package repository

import (
	"context"
	"time"
)

func (s *MemoryStore) BeginConanObjectCollection(ctx context.Context, key string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	object, ok := s.conanObjects[key]
	if !ok {
		return false, ErrNotFound
	}
	if !object.CollectedAt.IsZero() || s.conanObjectVisibleLocked(key) || s.conanObjectInOpenSessionLocked(key) {
		return false, nil
	}
	if object.CollectingAt.IsZero() {
		object.CollectingAt = time.Now().UTC()
		s.conanObjects[key] = object
	}
	return true, nil
}

func (s *MemoryStore) CompleteConanObjectCollection(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	object, ok := s.conanObjects[key]
	if !ok {
		return ErrNotFound
	}
	if !object.CollectedAt.IsZero() {
		return nil
	}
	if object.CollectingAt.IsZero() || s.conanObjectVisibleLocked(key) || s.conanObjectInOpenSessionLocked(key) {
		return ErrDisabled
	}
	object.CollectedAt = time.Now().UTC()
	s.conanObjects[key] = object
	return nil
}
