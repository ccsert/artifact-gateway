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
	Name         string     `json:"name"`
	Type         MemberType `json:"type"`
	Endpoint     string     `json:"endpoint"`
	Position     int        `json:"position"`
	Anonymous    bool       `json:"anonymous"`
	AllowedHosts []string   `json:"allowedHosts,omitempty"`
}

type Group struct {
	Name      string    `json:"name"`
	Enabled   bool      `json:"enabled"`
	Anonymous bool      `json:"anonymous"`
	Members   []Member  `json:"members"`
	CreatedAt time.Time `json:"createdAt"`
}

type AuditOutcome string

const (
	AuditResolved          AuditOutcome = "resolved"
	AuditInternalPreferred AuditOutcome = "internal_preferred"
	AuditNotFound          AuditOutcome = "not_found"
	AuditGroupDisabled     AuditOutcome = "group_disabled"
	AuditStorageError      AuditOutcome = "storage_error"
	AuditUpstreamError     AuditOutcome = "upstream_error"
	AuditAccessDenied      AuditOutcome = "access_denied"
	AuditProxyDenied       AuditOutcome = "proxy_denied"
)

type AuditRecord struct {
	GroupName  string
	Repository string
	MemberName string
	Outcome    AuditOutcome
	Actor      string
	OccurredAt time.Time
}

type AuditQuery struct {
	GroupName  string
	Repository string
	Limit      int
}

type Store interface {
	CreateGroup(context.Context, Group) (Group, error)
	GetGroup(context.Context, string) (Group, error)
	DisableGroup(context.Context, string) error
	RecordAudit(context.Context, AuditRecord) error
	ListAudits(context.Context, AuditQuery) ([]AuditRecord, error)
}

type AuditStore interface {
	ListAudits(context.Context, AuditQuery) ([]AuditRecord, error)
}

// MavenStore keeps Maven Group configuration separate from OCI Groups.
type MavenStore interface {
	CreateMavenGroup(context.Context, Group) (Group, error)
	GetMavenGroup(context.Context, string) (Group, error)
	DisableMavenGroup(context.Context, string) error
	RecordAudit(context.Context, AuditRecord) error
}

// ConanStore deliberately uses a separate Group namespace from OCI because a
// Conan Group is a distinct format and authorization boundary.
type ConanStore interface {
	CreateConanGroup(context.Context, Group) (Group, error)
	GetConanGroup(context.Context, string) (Group, error)
	DisableConanGroup(context.Context, string) error
	RecordAudit(context.Context, AuditRecord) error
}

type MemoryStore struct {
	mu          sync.RWMutex
	groups      map[string]Group
	mavenGroups map[string]Group
	conanGroups map[string]Group
	Audits      []AuditRecord
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{groups: make(map[string]Group), mavenGroups: make(map[string]Group), conanGroups: make(map[string]Group)}
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

func (s *MemoryStore) RecordAudit(_ context.Context, record AuditRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Audits = append(s.Audits, record)
	return nil
}

func (s *MemoryStore) ListAudits(_ context.Context, query AuditQuery) ([]AuditRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	limit := query.Limit
	if limit <= 0 {
		limit = 100
	}
	records := make([]AuditRecord, 0, limit)
	for i := len(s.Audits) - 1; i >= 0 && len(records) < limit; i-- {
		record := s.Audits[i]
		if query.GroupName != "" && record.GroupName != query.GroupName {
			continue
		}
		if query.Repository != "" && record.Repository != query.Repository {
			continue
		}
		records = append(records, record)
	}
	return records, nil
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
