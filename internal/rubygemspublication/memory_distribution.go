package rubygemspublication

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/ccsert/artifact-gateway/internal/repository"
	dist "github.com/ccsert/artifact-gateway/internal/rubygemsdistribution"
	"github.com/google/uuid"
)

type distributionAuthorityWriter interface {
	ChangeRubyGemsDistribution(context.Context, string, string, *repository.RubyGemsGovernanceIdentity, func(context.Context, repository.RubyGemsGovernanceAuthority, repository.RubyGemsGovernanceAuthority) error, func(repository.RubyGemsGovernanceAuthority, repository.RubyGemsGovernanceAuthority) error, func()) error
}

// The domain mutex is always acquired before the fixture repository mutex.
// apply stages local copies and audits; commit alone publishes their changes.
func (m *Memory) replicationChange(ctx context.Context, j dist.Job, check dist.Check, lease *dist.Lease, apply func(context.Context, dist.Authority) error, commit func()) error {
	if lease != nil && !dist.LeaseMatches(m.replications[j.ID], *lease) {
		return dist.ErrLeaseLost
	}
	writer, ok := m.repositories.(distributionAuthorityWriter)
	if !ok {
		return dist.ErrAccessDenied
	}
	var subject *repository.RubyGemsGovernanceIdentity
	if check != nil {
		v := ReplicationIdentity(j)
		subject = &v
	}
	validate := func(a, b repository.RubyGemsGovernanceAuthority) error {
		if lease != nil && !dist.LeaseMatches(m.replications[j.ID], *lease) {
			return dist.ErrLeaseLost
		}
		if check != nil {
			return dist.CheckAuthority(ctx, check, ReplicationAuthority(j, a, b))
		}
		return ctx.Err()
	}
	return writer.ChangeRubyGemsDistribution(ctx, j.SourceRepositoryID, j.TargetRepositoryID, subject, func(auditCtx context.Context, a, b repository.RubyGemsGovernanceAuthority) error {
		if err := validate(a, b); err != nil {
			return err
		}
		return apply(auditCtx, ReplicationAuthority(j, a, b))
	}, validate, commit)
}
func (m *Memory) replicationSource(j dist.Job, index int) (Publication, error) {
	if index < 0 || index >= len(j.Items) {
		return Publication{}, dist.ErrInvalid
	}
	i := j.Items[index]
	s, err := m.retained(j.SourceRepositoryID, i.Name, i.Version, i.Platform)
	if err != nil {
		return Publication{}, dist.ErrSourceChanged
	}
	p := Visible(s)
	return p, ReplicationSource(p, i)
}
func (m *Memory) CreateRubyGemsReplication(ctx context.Context, j dist.Job, check dist.Check) (dist.Job, bool, error) {
	if !dist.ValidNewJob(j) {
		return dist.Job{}, false, dist.ErrInvalid
	}
	if check == nil {
		return dist.Job{}, false, dist.ErrAccessDenied
	}
	if err := replicationMemoryLock(ctx, m); err != nil {
		return dist.Job{}, false, err
	}
	defer m.mu.Unlock()
	prior, replay := m.replications[j.ID]
	if replay && (prior.PlanDigest != j.PlanDigest || !dist.SameSubject(prior.Subject(), j.Subject())) {
		return dist.Job{}, false, dist.ErrConflict
	}
	out := dist.Clone(j)
	err := m.replicationChange(ctx, j, check, nil, func(auditCtx context.Context, a dist.Authority) error {
		if replay {
			out = dist.Clone(prior)
			return nil
		}
		extra := int64(0)
		for index, item := range j.Items {
			source, err := m.replicationSource(j, index)
			if err != nil {
				return err
			}
			exists := false
			for _, s := range m.sessions {
				if s.RepositoryID == j.TargetRepositoryID && (s.State == "committed" || s.State == "pending" && s.ExpiresAt.After(time.Now())) && (s.Identity() == Identity(item.Name, item.Version, item.Platform) || Visible(s).FullName() == source.FullName()) {
					if err := ReplicationSession(source, s, j.TargetRepositoryID); err != nil {
						return err
					}
					if s.State == "pending" && s.Actor != j.Actor {
						return dist.ErrTargetBlocked
					}
					exists = true
				}
			}
			if !exists {
				extra += item.Size
			}
		}
		c := m.capacity(j.TargetRepositoryID)
		if c.QuotaBytes > 0 && (c.UsedBytes+c.PendingBytes > c.QuotaBytes || extra > c.QuotaBytes-c.UsedBytes-c.PendingBytes) {
			return dist.ErrCapacity
		}
		out.State = "queued"
		out.Version = "1"
		out.CreatedAt = time.Now().UTC()
		out.UpdatedAt = out.CreatedAt
		out.Lease.JobID = out.ID
		return m.audit.RecordAudit(auditCtx, ReplicationAudit(out, a.Target.Name, "create", 202, nil))
	}, func() {
		if !replay {
			if m.replications == nil {
				m.replications = map[string]dist.Job{}
			}
			m.replications[j.ID] = dist.Clone(out)
		}
	})
	return dist.Clone(out), replay, err
}
func (m *Memory) GetRubyGemsReplication(ctx context.Context, id string) (dist.Job, error) {
	if err := replicationMemoryLock(ctx, m); err != nil {
		return dist.Job{}, err
	}
	defer m.mu.Unlock()
	j, ok := m.replications[id]
	if !ok {
		return j, dist.ErrNotFound
	}
	return dist.Clone(j), nil
}
func (m *Memory) RetryRubyGemsReplication(ctx context.Context, id, expected string, indexes []int, key string, check dist.Check) (dist.Job, bool, error) {
	digest, err := dist.RetryDigest(expected, indexes)
	if err != nil || key == "" || len(key) > 200 || strings.ContainsAny(key, "\x00\r\n") {
		return dist.Job{}, false, dist.ErrInvalid
	}
	if check == nil {
		return dist.Job{}, false, dist.ErrAccessDenied
	}
	if err = replicationMemoryLock(ctx, m); err != nil {
		return dist.Job{}, false, err
	}
	defer m.mu.Unlock()
	j, ok := m.replications[id]
	if !ok {
		return j, false, dist.ErrNotFound
	}
	record := id + "\x00" + key
	old, replay := m.replicationRetries[record]
	if replay && old != digest {
		return dist.Job{}, false, dist.ErrConflict
	}
	out := dist.Clone(j)
	err = m.replicationChange(ctx, j, check, nil, func(auditCtx context.Context, a dist.Authority) error {
		if replay {
			return nil
		}
		out, err = dist.RetryJob(j, expected, indexes)
		if err != nil {
			return err
		}
		return m.audit.RecordAudit(auditCtx, ReplicationAudit(out, a.Target.Name, "retry", 202, nil))
	}, func() {
		if !replay {
			if m.replicationRetries == nil {
				m.replicationRetries = map[string]string{}
			}
			m.replicationRetries[record] = digest
			m.replications[id] = dist.Clone(out)
		}
	})
	return out, replay, err
}
func (m *Memory) ClaimRubyGemsReplication(ctx context.Context, owner string, d time.Duration) (dist.Job, error) {
	if owner == "" || len(owner) > 512 || !dist.ValidDuration(d) {
		return dist.Job{}, dist.ErrInvalid
	}
	if err := replicationMemoryLock(ctx, m); err != nil {
		return dist.Job{}, err
	}
	defer m.mu.Unlock()
	jobs := []dist.Job{}
	for _, j := range m.replications {
		if j.State == "queued" || j.State == "running" && !j.Lease.Until.After(time.Now()) {
			jobs = append(jobs, j)
		}
	}
	sort.Slice(jobs, func(i, k int) bool {
		if jobs[i].CreatedAt.Equal(jobs[k].CreatedAt) {
			return jobs[i].ID < jobs[k].ID
		}
		return jobs[i].CreatedAt.Before(jobs[k].CreatedAt)
	})
	if len(jobs) == 0 {
		return dist.Job{}, dist.ErrNotFound
	}
	j := dist.Clone(jobs[0])
	j.State = "running"
	j.Version = dist.NextVersion(j.Version)
	j.UpdatedAt = time.Now().UTC()
	j.Lease = dist.Lease{JobID: j.ID, Owner: owner, Token: uuid.NewString(), Fence: j.Lease.Fence + 1, Until: time.Now().UTC().Add(d)}
	for i := range j.Items {
		if j.Items[i].State == "running" {
			j.Items[i].State = "pending"
		}
	}
	m.replications[j.ID] = j
	return dist.Clone(j), nil
}
func (m *Memory) RenewRubyGemsReplication(ctx context.Context, l dist.Lease, d time.Duration) (dist.Lease, error) {
	if !dist.ValidDuration(d) {
		return dist.Lease{}, dist.ErrInvalid
	}
	if err := replicationMemoryLock(ctx, m); err != nil {
		return dist.Lease{}, err
	}
	defer m.mu.Unlock()
	j := m.replications[l.JobID]
	if !dist.LeaseMatches(j, l) {
		return dist.Lease{}, dist.ErrLeaseLost
	}
	j.Lease.Until = time.Now().UTC().Add(d)
	m.replications[j.ID] = j
	return j.Lease, nil
}
func (m *Memory) AuthorizeRubyGemsReplicationItem(ctx context.Context, l dist.Lease, index int, check dist.Check) error {
	if check == nil {
		return dist.ErrAccessDenied
	}
	if err := replicationMemoryLock(ctx, m); err != nil {
		return err
	}
	defer m.mu.Unlock()
	j := m.replications[l.JobID]
	return m.replicationChange(ctx, j, check, &l, func(context.Context, dist.Authority) error { _, err := m.replicationSource(j, index); return err }, func() {})
}
func (m *Memory) BindRubyGemsReplicationSession(ctx context.Context, l dist.Lease, index int, id string, check dist.Check) (dist.Job, error) {
	if check == nil {
		return dist.Job{}, dist.ErrAccessDenied
	}
	if err := replicationMemoryLock(ctx, m); err != nil {
		return dist.Job{}, err
	}
	defer m.mu.Unlock()
	j := dist.Clone(m.replications[l.JobID])
	err := m.replicationChange(ctx, j, check, &l, func(context.Context, dist.Authority) error {
		source, err := m.replicationSource(j, index)
		if err != nil {
			return err
		}
		s, ok := m.sessions[id]
		if !ok {
			return dist.ErrTargetBlocked
		}
		if err = ReplicationSession(source, s, j.TargetRepositoryID); err != nil {
			return err
		}
		if s.State != "committed" && (s.State != "pending" || !s.ExpiresAt.After(time.Now()) || s.Actor != j.Actor) {
			return dist.ErrTargetBlocked
		}
		i := j.Items[index]
		if i.State != "pending" && i.State != "running" {
			return dist.ErrConflict
		}
		if i.State == "pending" {
			i.Attempts++
		}
		i.State = "running"
		i.TargetSessionID = id
		j.Items[index] = i
		j.Version = dist.NextVersion(j.Version)
		j.UpdatedAt = time.Now().UTC()
		return nil
	}, func() { m.replications[j.ID] = dist.Clone(j) })
	return dist.Clone(j), err
}
func (m *Memory) CompleteRubyGemsReplicationItem(ctx context.Context, l dist.Lease, index int, proof dist.Commit, check dist.Check) (dist.Job, error) {
	if check == nil {
		return dist.Job{}, dist.ErrAccessDenied
	}
	if err := replicationMemoryLock(ctx, m); err != nil {
		return dist.Job{}, err
	}
	defer m.mu.Unlock()
	j := dist.Clone(m.replications[l.JobID])
	var published *Session
	err := m.replicationChange(ctx, j, check, &l, func(auditCtx context.Context, a dist.Authority) error {
		source, err := m.replicationSource(j, index)
		if err != nil {
			return err
		}
		i := j.Items[index]
		s, ok := m.sessions[proof.SessionID]
		if !ok || i.TargetSessionID != proof.SessionID {
			return dist.ErrTargetBlocked
		}
		if err = ReplicationSession(source, s, j.TargetRepositoryID); err != nil {
			return err
		}
		if i.State == "succeeded" {
			return nil
		}
		if i.State != "running" {
			return dist.ErrConflict
		}
		if s.State == "committed" {
			i.Result = "already_present"
		} else {
			if s.State != "pending" || s.Actor != j.Actor || !s.ExpiresAt.After(time.Now()) || !ValidInput(s) {
				return dist.ErrTargetBlocked
			}
			c := m.capacity(j.TargetRepositoryID)
			if c.QuotaBytes > 0 && c.UsedBytes+c.PendingBytes > c.QuotaBytes {
				return dist.ErrCapacity
			}
			s.State = "committed"
			s.CommittedAt = time.Now().UTC()
			published = &s
			i.Result = "published"
			if err = m.audit.RecordAudit(auditCtx, Audit(Visible(s), a.Target.Name)); err != nil {
				return err
			}
		}
		now := time.Now().UTC()
		i.State = "succeeded"
		i.Code = ""
		i.CompletedAt = &now
		j.Items[index] = i
		j.Version = dist.NextVersion(j.Version)
		j.UpdatedAt = now
		return m.audit.RecordAudit(auditCtx, ReplicationAudit(j, a.Target.Name, "item", 200, &i))
	}, func() {
		if published != nil {
			m.sessions[published.ID] = clone(*published)
		}
		m.replications[j.ID] = dist.Clone(j)
	})
	return dist.Clone(j), err
}
func (m *Memory) FailRubyGemsReplicationItem(ctx context.Context, l dist.Lease, index int, code string) error {
	if !dist.ValidFailureCode(code) {
		return dist.ErrInvalid
	}
	if err := replicationMemoryLock(ctx, m); err != nil {
		return err
	}
	defer m.mu.Unlock()
	j := dist.Clone(m.replications[l.JobID])
	return m.replicationChange(ctx, j, nil, &l, func(auditCtx context.Context, a dist.Authority) error {
		if index < 0 || index >= len(j.Items) || j.Items[index].State == "succeeded" {
			return dist.ErrConflict
		}
		i := j.Items[index]
		if i.State == "pending" {
			i.Attempts++
		}
		i.State = "failed"
		i.Code = code
		now := time.Now().UTC()
		i.CompletedAt = &now
		j.Items[index] = i
		j.Version = dist.NextVersion(j.Version)
		j.UpdatedAt = now
		return m.audit.RecordAudit(auditCtx, ReplicationAudit(j, a.Target.Name, "item", dist.FailureStatus(code), &i))
	}, func() { m.replications[j.ID] = dist.Clone(j) })
}
func (m *Memory) FinishRubyGemsReplication(ctx context.Context, l dist.Lease) (dist.Job, error) {
	if err := replicationMemoryLock(ctx, m); err != nil {
		return dist.Job{}, err
	}
	defer m.mu.Unlock()
	j := dist.Clone(m.replications[l.JobID])
	err := m.replicationChange(ctx, j, nil, &l, func(auditCtx context.Context, a dist.Authority) error {
		j.State = "succeeded"
		for _, i := range j.Items {
			if i.State == "pending" || i.State == "running" {
				return dist.ErrConflict
			}
			if i.State == "failed" {
				j.State = "failed"
			}
		}
		j.Lease.Owner = ""
		j.Lease.Token = ""
		j.Lease.Until = time.Time{}
		j.Version = dist.NextVersion(j.Version)
		j.UpdatedAt = time.Now().UTC()
		status := 200
		if j.State == "failed" {
			status = 409
		}
		return m.audit.RecordAudit(auditCtx, ReplicationAudit(j, a.Target.Name, "complete", status, nil))
	}, func() { m.replications[j.ID] = dist.Clone(j) })
	return j, err
}

var _ dist.Store = (*Memory)(nil)
