package rubygemspublication

import (
	"context"
	"hash/fnv"
	"sort"
	"sync"
	"time"

	"github.com/ccsert/artifact-gateway/internal/repository"
	dist "github.com/ccsert/artifact-gateway/internal/rubygemsdistribution"
)

type Memory struct {
	mu                 sync.Mutex
	repositories       RepositoryReader
	audit              Auditor
	sessions           map[string]Session
	quotas             map[string]int64
	locks              [256]chan struct{}
	replications       map[string]dist.Job
	replicationRetries map[string]string
}

func NewMemory(repos RepositoryReader, audit Auditor) *Memory {
	m := &Memory{repositories: repos, audit: audit, sessions: map[string]Session{}, quotas: map[string]int64{}}
	for i := range m.locks {
		m.locks[i] = make(chan struct{}, 1)
		m.locks[i] <- struct{}{}
	}
	return m
}
func (m *Memory) Lock(ctx context.Context, repo, identity string) (context.Context, func(), error) {
	h := fnv.New32a()
	_, _ = h.Write([]byte(repo + "\x00" + identity))
	ch := m.locks[h.Sum32()%uint32(len(m.locks))]
	select {
	case <-ctx.Done():
		return ctx, func() {}, ctx.Err()
	case <-ch:
	}
	var once sync.Once
	return ctx, func() { once.Do(func() { ch <- struct{}{} }) }, nil
}
func (m *Memory) active(ctx context.Context, id string) (repository.HostedRepository, error) {
	r, e := m.repositories.GetHostedRepository(ctx, id)
	if e != nil {
		return r, e
	}
	if !Active(r) {
		return r, repository.ErrNotFound
	}
	return r, nil
}
func clone(s Session) Session {
	s.MetadataJSON = append([]byte(nil), s.MetadataJSON...)
	s.MetadataYAML = append([]byte(nil), s.MetadataYAML...)
	return s
}
func (m *Memory) capacity(repo string) Capacity {
	c := Capacity{QuotaBytes: m.quotas[repo]}
	for _, s := range m.sessions {
		if s.RepositoryID != repo {
			continue
		}
		if s.State == "committed" {
			c.UsedBytes += s.Size
			c.ObjectCount++
		} else if s.State == "pending" && s.ExpiresAt.After(time.Now()) {
			c.PendingBytes += s.Size
		}
	}
	return c
}
func (m *Memory) Reserve(ctx context.Context, in Session) (Session, bool, error) {
	if !ValidInput(in) || !in.ExpiresAt.After(time.Now()) || in.State != "" || in.ObjectKey != "" || !in.CreatedAt.IsZero() || !in.CommittedAt.IsZero() || !in.CollectedAt.IsZero() || in.Governance != (Governance{}) {
		return Session{}, false, ErrInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, e := m.active(ctx, in.RepositoryID); e != nil {
		return Session{}, false, e
	}
	for id, s := range m.sessions {
		if s.State == "pending" && !s.ExpiresAt.After(time.Now()) {
			s.State = "expired"
			m.sessions[id] = s
		}
		if s.ID == in.ID && (!Same(s, in) || s.Actor != in.Actor) {
			return Session{}, false, ErrConflict
		}
		if s.RepositoryID == in.RepositoryID && (s.Identity() == in.Identity() || Visible(s).FullName() == Visible(in).FullName()) && (s.State == "pending" || s.State == "committed") {
			if s.Governance.Yanked || s.Governance.Quarantined || !Same(s, in) || s.State == "pending" && s.Actor != in.Actor {
				return Session{}, false, ErrConflict
			}
			return clone(s), true, nil
		}
	}
	if old, ok := m.sessions[in.ID]; ok {
		return clone(old), false, ErrExpired
	}
	c := m.capacity(in.RepositoryID)
	if c.QuotaBytes > 0 && (c.UsedBytes+c.PendingBytes > c.QuotaBytes || in.Size > c.QuotaBytes-c.UsedBytes-c.PendingBytes) {
		return Session{}, false, repository.ErrQuotaExceeded
	}
	activeCount := 0
	for _, s := range m.sessions {
		if s.RepositoryID == in.RepositoryID && (s.State == "pending" || s.State == "committed") {
			activeCount++
		}
	}
	if activeCount >= protocolIndexLimit {
		return Session{}, false, repository.ErrQuotaExceeded
	}
	in.State = "pending"
	in.Governance.Version = "1"
	in.CreatedAt = time.Now().UTC()
	in.ObjectKey = ObjectKey(in)
	m.sessions[in.ID] = clone(in)
	return clone(in), false, nil
}

const protocolIndexLimit = 4096

func (m *Memory) Commit(ctx context.Context, id, actor string) (Publication, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[id]
	if !ok {
		return Publication{}, false, repository.ErrNotFound
	}
	repo, e := m.active(ctx, s.RepositoryID)
	if e != nil {
		return Publication{}, false, e
	}
	if s.State == "committed" {
		if s.Governance.Yanked || s.Governance.Quarantined {
			return Publication{}, false, ErrConflict
		}
		return Visible(s), true, nil
	}
	if s.State != "pending" || !s.ExpiresAt.After(time.Now()) {
		return Publication{}, false, ErrExpired
	}
	if s.Actor != actor {
		return Publication{}, false, ErrConflict
	}
	s.State = "committed"
	s.CommittedAt = time.Now().UTC()
	p := Visible(s)
	if e = m.audit.RecordAudit(ctx, Audit(p, repo.Name)); e != nil {
		return Publication{}, false, e
	}
	m.sessions[id] = s
	return p, false, nil
}
func (m *Memory) Get(ctx context.Context, repo, fullName string) (Publication, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, e := m.active(ctx, repo); e != nil {
		return Publication{}, e
	}
	for _, s := range m.sessions {
		p := Visible(s)
		if s.RepositoryID == repo && s.State == "committed" && !s.Governance.Yanked && p.FullName() == fullName {
			return p, nil
		}
	}
	return Publication{}, repository.ErrNotFound
}
func (m *Memory) List(ctx context.Context, repo string, limit int) ([]Publication, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, e := m.active(ctx, repo); e != nil {
		return nil, e
	}
	if limit < 1 || limit > 4097 {
		limit = 4097
	}
	out := []Publication{}
	for _, s := range m.sessions {
		if s.RepositoryID == repo && s.State == "committed" && !s.Governance.Yanked {
			out = append(out, Visible(s))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].FullName() < out[j].FullName() })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
func (m *Memory) GetSession(ctx context.Context, id string) (Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[id]
	if !ok {
		return s, repository.ErrNotFound
	}
	return clone(s), ctx.Err()
}
func (m *Memory) Expire(ctx context.Context, now time.Time, limit int) ([]Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if limit < 1 || limit > 1000 {
		limit = 100
	}
	out := []Session{}
	keys := make([]string, 0, len(m.sessions))
	for k := range m.sessions {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		s := m.sessions[k]
		if s.CollectedAt.IsZero() && (s.State == "expired" || s.State == "pending" && !s.ExpiresAt.After(now)) {
			s.State = "expired"
			m.sessions[k] = s
			out = append(out, clone(s))
			if len(out) == limit {
				break
			}
		}
	}
	return out, ctx.Err()
}
func (m *Memory) MarkCollected(ctx context.Context, id, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[id]
	if !ok || s.State != "expired" || s.ObjectKey != key {
		return ErrState
	}
	s.CollectedAt = time.Now().UTC()
	m.sessions[id] = s
	return ctx.Err()
}
func (m *Memory) Capacity(ctx context.Context, repo string) (Capacity, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, e := m.active(ctx, repo); e != nil {
		return Capacity{}, e
	}
	return m.capacity(repo), nil
}
func (m *Memory) SetQuota(ctx context.Context, repo, actor string, quota int64) (Capacity, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, e := m.active(ctx, repo)
	if e != nil {
		return Capacity{}, e
	}
	if quota < 0 {
		return Capacity{}, ErrInvalid
	}
	c := m.capacity(repo)
	if quota > 0 && quota < c.UsedBytes+c.PendingBytes {
		return Capacity{}, repository.ErrQuotaExceeded
	}
	if e = m.audit.RecordAudit(ctx, repository.AuditRecord{Actor: actor, Repository: r.Name, GroupName: r.Name, Format: "rubygems", Resource: repo, Operation: "rubygems.quota.configure", Outcome: repository.AuditResolved, Status: 200, OccurredAt: time.Now().UTC()}); e != nil {
		return Capacity{}, e
	}
	m.quotas[repo] = quota
	c.QuotaBytes = quota
	return c, nil
}

var _ Store = (*Memory)(nil)
