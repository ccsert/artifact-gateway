package repository

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"
)

var (
	ErrNotFound   = errors.New("group not found")
	ErrDisabled   = errors.New("group is disabled")
	ErrNameExists = errors.New("group name already exists")
)

type MemberType string

const (
	MemberHosted MemberType = "hosted"
	MemberProxy  MemberType = "proxy"
)

type Member struct {
	Name     string     `json:"name"`
	Type     MemberType `json:"type"`
	Endpoint string     `json:"endpoint"`
	Position int        `json:"position"`
}

type Group struct {
	Name      string    `json:"name"`
	Enabled   bool      `json:"enabled"`
	Members   []Member  `json:"members"`
	CreatedAt time.Time `json:"createdAt"`
}

type AuditOutcome string

const (
	AuditResolved      AuditOutcome = "resolved"
	AuditNotFound      AuditOutcome = "not_found"
	AuditGroupDisabled AuditOutcome = "group_disabled"
	AuditStorageError  AuditOutcome = "storage_error"
)

type AuditRecord struct {
	GroupName  string
	Repository string
	MemberName string
	Outcome    AuditOutcome
	Actor      string
	OccurredAt time.Time
}

type Store interface {
	CreateGroup(context.Context, Group) (Group, error)
	GetGroup(context.Context, string) (Group, error)
	DisableGroup(context.Context, string) error
	RecordAudit(context.Context, AuditRecord) error
}

type MemoryStore struct {
	mu     sync.RWMutex
	groups map[string]Group
	Audits []AuditRecord
}

func NewMemoryStore() *MemoryStore { return &MemoryStore{groups: make(map[string]Group)} }

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

func (s *MemoryStore) RecordAudit(_ context.Context, record AuditRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Audits = append(s.Audits, record)
	return nil
}
