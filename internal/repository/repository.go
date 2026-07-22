package repository

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"
)

var (
	ErrNotFound            = errors.New("group not found")
	ErrDisabled            = errors.New("group is disabled")
	ErrNameExists          = errors.New("group name already exists")
	ErrIdempotencyConflict = errors.New("idempotency key conflicts with request")
)

// Format is the immutable protocol family served by a Native Hosted
// Repository. It deliberately remains distinct from the legacy Group model.
type Format string

const (
	FormatRaw   Format = "raw"
	FormatOCI   Format = "oci"
	FormatMaven Format = "maven"
)

type RepositoryState string

const (
	RepositoryActive   RepositoryState = "active"
	RepositoryDeleting RepositoryState = "deleting"
)

type HostedRepository struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Format    Format          `json:"format"`
	State     RepositoryState `json:"state"`
	Version   string          `json:"version"`
	CreatedAt time.Time       `json:"-"`
}

// HostedRepositoryStore owns V3 Native Hosted configuration. Every protocol
// adapter must check State before it permits a read or write.
type HostedRepositoryStore interface {
	CreateHostedRepository(context.Context, HostedRepository) (HostedRepository, error)
	CreateHostedRepositoryIdempotently(context.Context, HostedRepository, string, string, string) (HostedRepository, bool, error)
	ListHostedRepositories(context.Context, int, string) ([]HostedRepository, string, error)
	GetHostedRepository(context.Context, string) (HostedRepository, error)
	GetHostedRepositoryByName(context.Context, string) (HostedRepository, error)
	DisableHostedRepository(context.Context, string) (HostedRepository, error)
}

// NativeMavenStore contains only committed Maven metadata. Object bytes live in
// the object store; staging rows never participate in protocol reads.
type NativeMavenStore interface {
	CreateMavenPublishSession(context.Context, MavenPublishSession) (MavenPublishSession, error)
	GetMavenPublishSession(context.Context, string) (MavenPublishSession, error)
	MarkMavenPublishObject(context.Context, string, string, string) error
	CommitMavenPublishSession(context.Context, string, []MavenAsset) (MavenArtifact, error)
	GetMavenAsset(context.Context, string, string) (MavenAsset, error)
	ListMavenArtifacts(context.Context, string) ([]MavenArtifact, error)
}

type MavenDeclaredObject struct {
	Name, Digest string
	Size         int64
}
type MavenPublishSession struct {
	ID, RepositoryID, Coordinate, PomObject, State string
	Objects                                        []MavenDeclaredObject
	ExpiresAt                                      time.Time
}
type MavenAsset struct {
	RepositoryID, Path, ObjectKey, Digest string
	Size                                  int64
}
type MavenArtifact struct {
	ID, RepositoryID, Coordinate, Digest, State string
	CreatedAt                                   time.Time
}

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
	Name            string    `json:"name"`
	Enabled         bool      `json:"enabled"`
	Anonymous       bool      `json:"anonymous"`
	CacheQuotaBytes int64     `json:"cacheQuotaBytes,omitempty"`
	Members         []Member  `json:"members"`
	CreatedAt       time.Time `json:"createdAt"`
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
	GroupName                                                                               string
	Repository                                                                              string
	MemberName                                                                              string
	Outcome                                                                                 AuditOutcome
	Actor                                                                                   string
	OccurredAt                                                                              time.Time
	Format, Resource, Representation, MemberType, UpstreamHost, Operation, CacheDisposition string
	RequestID, TraceID                                                                      string
	Status                                                                                  int
	Bytes                                                                                   int64
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

type RawStore interface {
	CreateRawGroup(context.Context, Group) (Group, error)
	GetRawGroup(context.Context, string) (Group, error)
	DisableRawGroup(context.Context, string) error
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
	mu                 sync.RWMutex
	groups             map[string]Group
	mavenGroups        map[string]Group
	rawGroups          map[string]Group
	conanGroups        map[string]Group
	Audits             []AuditRecord
	hostedRepositories map[string]HostedRepository
	idempotencyRecords map[string]idempotencyRecord
	mavenSessions      map[string]MavenPublishSession
	mavenUploads       map[string]map[string]string
	mavenAssets        map[string]MavenAsset
	mavenArtifacts     map[string]MavenArtifact
}

type idempotencyRecord struct {
	payload, repositoryID string
	expiresAt             time.Time
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{groups: make(map[string]Group), mavenGroups: make(map[string]Group), rawGroups: make(map[string]Group), conanGroups: make(map[string]Group), hostedRepositories: make(map[string]HostedRepository), idempotencyRecords: make(map[string]idempotencyRecord), mavenSessions: make(map[string]MavenPublishSession), mavenUploads: make(map[string]map[string]string), mavenAssets: make(map[string]MavenAsset), mavenArtifacts: make(map[string]MavenArtifact)}
}

func (s *MemoryStore) CreateMavenPublishSession(_ context.Context, session MavenPublishSession) (MavenPublishSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.mavenSessions[session.ID] = session
	s.mavenUploads[session.ID] = map[string]string{}
	return session, nil
}
func (s *MemoryStore) GetMavenPublishSession(_ context.Context, id string) (MavenPublishSession, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.mavenSessions[id]
	if !ok {
		return MavenPublishSession{}, ErrNotFound
	}
	return v, nil
}
func (s *MemoryStore) MarkMavenPublishObject(_ context.Context, id, name, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.mavenSessions[id]; !ok {
		return ErrNotFound
	}
	s.mavenUploads[id][name] = key
	return nil
}
func (s *MemoryStore) CommitMavenPublishSession(_ context.Context, id string, assets []MavenAsset) (MavenArtifact, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.mavenSessions[id]
	if !ok {
		return MavenArtifact{}, ErrNotFound
	}
	if session.State != "open" || time.Now().After(session.ExpiresAt) {
		return MavenArtifact{}, ErrDisabled
	}
	for _, o := range session.Objects {
		if s.mavenUploads[id][o.Name] == "" {
			return MavenArtifact{}, ErrDisabled
		}
	}
	for _, a := range assets {
		k := a.RepositoryID + "\x00" + a.Path
		if _, exists := s.mavenAssets[k]; exists {
			return MavenArtifact{}, ErrNameExists
		}
		s.mavenAssets[k] = a
	}
	artifact := MavenArtifact{ID: id, RepositoryID: session.RepositoryID, Coordinate: session.Coordinate, Digest: session.Objects[0].Digest, State: "visible", CreatedAt: time.Now().UTC()}
	s.mavenArtifacts[id] = artifact
	session.State = "committed"
	s.mavenSessions[id] = session
	return artifact, nil
}
func (s *MemoryStore) GetMavenAsset(_ context.Context, repositoryID, path string) (MavenAsset, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.mavenAssets[repositoryID+"\x00"+path]
	if !ok {
		return MavenAsset{}, ErrNotFound
	}
	return v, nil
}
func (s *MemoryStore) ListMavenArtifacts(_ context.Context, repositoryID string) ([]MavenArtifact, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []MavenArtifact{}
	for _, a := range s.mavenArtifacts {
		if a.RepositoryID == repositoryID {
			out = append(out, a)
		}
	}
	return out, nil
}

func (s *MemoryStore) CreateHostedRepository(_ context.Context, repo HostedRepository) (HostedRepository, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, existing := range s.hostedRepositories {
		if existing.Name == repo.Name {
			return HostedRepository{}, ErrNameExists
		}
	}
	repo.State = RepositoryActive
	repo.Version = "1"
	repo.CreatedAt = time.Now().UTC()
	s.hostedRepositories[repo.ID] = repo
	return repo, nil
}

func (s *MemoryStore) CreateHostedRepositoryIdempotently(_ context.Context, repo HostedRepository, actor, key, payload string) (HostedRepository, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	recordKey := actor + "\x00/repositories\x00" + key
	if record, ok := s.idempotencyRecords[recordKey]; ok && time.Now().UTC().Before(record.expiresAt) {
		if record.payload != payload {
			return HostedRepository{}, false, ErrIdempotencyConflict
		}
		return s.hostedRepositories[record.repositoryID], true, nil
	}
	for _, existing := range s.hostedRepositories {
		if existing.Name == repo.Name {
			return HostedRepository{}, false, ErrNameExists
		}
	}
	repo.State, repo.Version, repo.CreatedAt = RepositoryActive, "1", time.Now().UTC()
	s.hostedRepositories[repo.ID] = repo
	s.idempotencyRecords[recordKey] = idempotencyRecord{payload: payload, repositoryID: repo.ID, expiresAt: time.Now().UTC().Add(24 * time.Hour)}
	return repo, false, nil
}

func (s *MemoryStore) ListHostedRepositories(_ context.Context, limit int, after string) ([]HostedRepository, string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	items := make([]HostedRepository, 0, len(s.hostedRepositories))
	for _, repo := range s.hostedRepositories {
		items = append(items, repo)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	start := 0
	if after != "" {
		found := false
		for i, repo := range items {
			if repo.ID == after {
				start = i + 1
				found = true
				break
			}
		}
		if !found {
			return nil, "", ErrNotFound
		}
	}
	end := start + limit
	next := ""
	if end < len(items) {
		next = items[end-1].ID
	} else if start > len(items) {
		start = len(items)
		end = start
	} else {
		end = len(items)
	}
	return items[start:end], next, nil
}

func (s *MemoryStore) GetHostedRepository(_ context.Context, id string) (HostedRepository, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	repo, ok := s.hostedRepositories[id]
	if !ok {
		return HostedRepository{}, ErrNotFound
	}
	return repo, nil
}

func (s *MemoryStore) GetHostedRepositoryByName(_ context.Context, name string) (HostedRepository, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, repo := range s.hostedRepositories {
		if repo.Name == name {
			return repo, nil
		}
	}
	return HostedRepository{}, ErrNotFound
}

func (s *MemoryStore) DisableHostedRepository(_ context.Context, id string) (HostedRepository, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	repo, ok := s.hostedRepositories[id]
	if !ok || repo.State != RepositoryActive {
		return HostedRepository{}, ErrNotFound
	}
	repo.State = RepositoryDeleting
	repo.Version = "2"
	s.hostedRepositories[id] = repo
	return repo, nil
}

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
