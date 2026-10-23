package repository

import (
	"context"
	"sort"
	"strings"
	"time"

	dist "github.com/ccsert/artifact-gateway/internal/helmdistribution"
	"github.com/google/uuid"
)

func (s *MemoryStore) helmReplicationAuthorityLocked(j dist.Job) (dist.Authority, error) {
	a := dist.Authority{Actor: j.Actor, Kind: j.ActorKind}
	src, se := s.activeHelmRepositoryLocked(j.SourceRepositoryID)
	dst, de := s.activeHelmRepositoryLocked(j.TargetRepositoryID)
	if se != nil || de != nil || src.Type != RepositoryTypeHosted || dst.Type != RepositoryTypeHosted {
		return a, dist.ErrNotFound
	}
	a.Source = helmReplicationGrantSet(src, s.repositoryGrants[src.ID])
	a.Target = helmReplicationGrantSet(dst, s.repositoryGrants[dst.ID])
	if !helmReplicationActorValid(j) {
		return a, dist.ErrAccessDenied
	}
	switch j.ActorKind {
	case "static_admin", "static_resolver":
		a.Active = true
	case "local_session", "oidc":
		for _, u := range s.users {
			if strings.EqualFold(u.Name, strings.TrimPrefix(j.Actor, "user:")) {
				a.Active = u.State == UserActive
				a.Role = u.Role
				a.MustChangePassword = u.MustChangePassword
				break
			}
		}
	case "api_key":
		k, ok := s.apiKeys[strings.TrimPrefix(j.Actor, "api-key:")]
		a.Active = ok && k.ServiceAccountID == "" && k.RevokedAt == nil && (k.ExpiresAt == nil || k.ExpiresAt.After(time.Now()))
		a.Role = helmReplicationKeyRole(k.Roles)
		if k.ExpiresAt != nil {
			a.ValidUntil = *k.ExpiresAt
		}
	case "service_account_credential":
		account, ok := s.serviceAccounts[strings.TrimPrefix(j.Actor, "service-account:")]
		k, found := s.apiKeys[j.CredentialID]
		a.Active = ok && account.State == ServiceAccountActive && found && k.ServiceAccountID == account.ID && k.RevokedAt == nil && (k.ExpiresAt == nil || k.ExpiresAt.After(time.Now()))
		if k.ExpiresAt != nil {
			a.ValidUntil = *k.ExpiresAt
		}
	}
	return a, nil
}
func (s *MemoryStore) helmReplicationSourceLocked(j dist.Job, i int) (HelmPublication, error) {
	if i < 0 || i >= len(j.Items) {
		return HelmPublication{}, dist.ErrInvalid
	}
	item := j.Items[i]
	p, ok := s.helmPublications[helmKey(j.SourceRepositoryID, item.Name, item.Version)]
	if !ok {
		return p, dist.ErrSourceChanged
	}
	return p, helmReplicationSource(p, item)
}
func (s *MemoryStore) helmReplicationPreflightLocked(ctx context.Context, j dist.Job, check dist.Check) (dist.Authority, error) {
	a, err := s.helmReplicationAuthorityLocked(j)
	if err != nil {
		return a, err
	}
	if err = helmReplicationCheck(ctx, check, a); err != nil {
		return a, err
	}
	return a, nil
}
func (s *MemoryStore) CreateHelmReplication(ctx context.Context, j dist.Job, check dist.Check) (dist.Job, bool, error) {
	if !dist.ValidNewJob(j) {
		return dist.Job{}, false, dist.ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	a, err := s.helmReplicationPreflightLocked(ctx, j, check)
	if err != nil {
		return dist.Job{}, false, err
	}
	if old, ok := s.helmJobs[j.ID]; ok {
		if old.PlanDigest != j.PlanDigest || old.Actor != j.Actor || old.ActorKind != j.ActorKind || old.CredentialID != j.CredentialID {
			return dist.Job{}, false, dist.ErrConflict
		}
		return dist.Clone(old), true, nil
	}
	extra := int64(0)
	for i, item := range j.Items {
		if _, err = s.helmReplicationSourceLocked(j, i); err != nil {
			return dist.Job{}, false, err
		}
		if _, ok := s.helmPublications[helmKey(j.TargetRepositoryID, item.Name, item.Version)]; !ok {
			extra += item.Size + item.ProvenanceSize
		}
	}
	capacity, _ := s.repositoryCapacityLocked(j.TargetRepositoryID)
	for _, session := range s.helmSessions {
		if session.RepositoryID == j.TargetRepositoryID && session.State == "pending" && session.ExpiresAt.After(time.Now()) {
			extra += session.Size + session.ProvenanceSize
		}
	}
	if capacity.QuotaBytes > 0 && (capacity.UsedBytes > capacity.QuotaBytes || extra > capacity.QuotaBytes-capacity.UsedBytes) {
		return dist.Job{}, false, ErrQuotaExceeded
	}
	j.CreatedAt = time.Now().UTC()
	j.UpdatedAt = j.CreatedAt
	j.Version = "1"
	j.State = "queued"
	j.Lease.JobID = j.ID
	if s.helmJobs == nil {
		s.helmJobs = map[string]dist.Job{}
	}
	s.helmJobs[j.ID] = dist.Clone(j)
	s.appendAuditLocked(withRequestContextAudit(ctx, helmReplicationAudit(j, a.Target.Name, "create", 202, nil)))
	return dist.Clone(j), false, nil
}
func (s *MemoryStore) GetHelmReplication(ctx context.Context, id string) (dist.Job, error) {
	if err := ctx.Err(); err != nil {
		return dist.Job{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	j, ok := s.helmJobs[id]
	if !ok {
		return dist.Job{}, dist.ErrNotFound
	}
	return dist.Clone(j), nil
}
func (s *MemoryStore) RetryHelmReplication(ctx context.Context, id, expected string, indexes []int, key string, check dist.Check) (dist.Job, bool, error) {
	digest, err := dist.RetryDigest(expected, indexes)
	if err != nil || key == "" || len(key) > 200 || strings.ContainsAny(key, "\x00\r\n") {
		return dist.Job{}, false, dist.ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.helmJobs[id]
	if !ok {
		return dist.Job{}, false, dist.ErrNotFound
	}
	a, err := s.helmReplicationPreflightLocked(ctx, j, check)
	if err != nil {
		return dist.Job{}, false, err
	}
	record := id + "\x00" + key
	if old, ok := s.helmRetries[record]; ok {
		if old != digest {
			return dist.Job{}, false, dist.ErrConflict
		}
		return dist.Clone(j), true, nil
	}
	j, err = helmReplicationRetry(j, expected, indexes)
	if err != nil {
		return dist.Job{}, false, err
	}
	if s.helmRetries == nil {
		s.helmRetries = map[string]string{}
	}
	s.helmRetries[record] = digest
	s.helmJobs[id] = dist.Clone(j)
	s.appendAuditLocked(withRequestContextAudit(ctx, helmReplicationAudit(j, a.Target.Name, "retry", 202, nil)))
	return dist.Clone(j), false, nil
}
func (s *MemoryStore) ClaimHelmReplication(ctx context.Context, owner string, d time.Duration) (dist.Job, error) {
	if err := ctx.Err(); err != nil {
		return dist.Job{}, err
	}
	if owner == "" || len(owner) > 512 || !dist.ValidDuration(d) {
		return dist.Job{}, dist.ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	jobs := []dist.Job{}
	for _, j := range s.helmJobs {
		if j.State == "queued" || j.State == "running" && !j.Lease.Until.After(time.Now()) {
			jobs = append(jobs, j)
		}
	}
	sort.Slice(jobs, func(i, j int) bool {
		if jobs[i].CreatedAt.Equal(jobs[j].CreatedAt) {
			return jobs[i].ID < jobs[j].ID
		}
		return jobs[i].CreatedAt.Before(jobs[j].CreatedAt)
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
	s.helmJobs[j.ID] = dist.Clone(j)
	return j, nil
}
func (s *MemoryStore) RenewHelmReplication(ctx context.Context, lease dist.Lease, d time.Duration) (dist.Lease, error) {
	if err := ctx.Err(); err != nil {
		return dist.Lease{}, err
	}
	if !dist.ValidDuration(d) {
		return dist.Lease{}, dist.ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	j := s.helmJobs[lease.JobID]
	if !helmReplicationLease(j, lease) {
		return dist.Lease{}, dist.ErrLeaseLost
	}
	j.Lease.Until = time.Now().UTC().Add(d)
	s.helmJobs[j.ID] = j
	return j.Lease, nil
}
func (s *MemoryStore) BindHelmReplicationSession(ctx context.Context, lease dist.Lease, index int, sessionID string, check dist.Check) (dist.Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j := s.helmJobs[lease.JobID]
	if !helmReplicationLease(j, lease) {
		return dist.Job{}, dist.ErrLeaseLost
	}
	a, err := s.helmReplicationPreflightLocked(ctx, j, check)
	if err != nil {
		return dist.Job{}, err
	}
	if !helmReplicationLease(j, lease) {
		return dist.Job{}, dist.ErrLeaseLost
	}
	if !a.ValidUntil.IsZero() && !a.ValidUntil.After(time.Now()) {
		return dist.Job{}, dist.ErrAccessDenied
	}
	source, err := s.helmReplicationSourceLocked(j, index)
	if err != nil {
		return dist.Job{}, err
	}
	session, ok := s.helmSessions[sessionID]
	if !ok || session.RepositoryID != j.TargetRepositoryID || session.Name != source.Name || session.Version != source.Version || session.Digest != source.Digest || session.Size != source.Size || session.ProvenanceDigest != source.ProvenanceDigest || session.ProvenanceSize != source.ProvenanceSize || session.SourceVersion != "" {
		return dist.Job{}, dist.ErrTargetBlocked
	}
	item := j.Items[index]
	if item.State != "pending" && item.State != "running" {
		return dist.Job{}, dist.ErrConflict
	}
	if item.State != "running" {
		item.Attempts++
	}
	item.State = "running"
	item.TargetSessionID = sessionID
	j.Items[index] = item
	j.Version = dist.NextVersion(j.Version)
	j.UpdatedAt = time.Now().UTC()
	s.helmJobs[j.ID] = dist.Clone(j)
	return dist.Clone(j), nil
}
func (s *MemoryStore) CompleteHelmReplicationItem(ctx context.Context, lease dist.Lease, index int, proof dist.Commit, check dist.Check) (dist.Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j := s.helmJobs[lease.JobID]
	if !helmReplicationLease(j, lease) {
		return dist.Job{}, dist.ErrLeaseLost
	}
	a, err := s.helmReplicationPreflightLocked(ctx, j, check)
	if err != nil {
		return dist.Job{}, err
	}
	source, err := s.helmReplicationSourceLocked(j, index)
	if err != nil {
		return dist.Job{}, err
	}
	if !helmReplicationLease(j, lease) {
		return dist.Job{}, dist.ErrLeaseLost
	}
	if !a.ValidUntil.IsZero() && !a.ValidUntil.After(time.Now()) {
		return dist.Job{}, dist.ErrAccessDenied
	}
	item := j.Items[index]
	if item.State == "succeeded" {
		return dist.Clone(j), nil
	}
	if item.State != "running" || item.TargetSessionID != proof.SessionID {
		return dist.Job{}, dist.ErrConflict
	}
	session, ok := s.helmSessions[proof.SessionID]
	if !ok || session.RepositoryID != j.TargetRepositoryID {
		return dist.Job{}, dist.ErrTargetBlocked
	}
	target, exists := s.helmPublications[helmKey(j.TargetRepositoryID, item.Name, item.Version)]
	if exists {
		if err = helmReplicationExisting(source, target); err != nil {
			return dist.Job{}, err
		}
		if target.SessionID != session.ID {
			return dist.Job{}, dist.ErrTargetBlocked
		}
		item.Result = "already_present"
	} else {
		if err = helmReplicationSession(source, session, j.TargetRepositoryID); err != nil {
			return dist.Job{}, err
		}
		if session.Actor != j.Actor || session.State != "pending" || !session.ExpiresAt.After(time.Now()) || !validHelmSession(session) {
			return dist.Job{}, dist.ErrTargetBlocked
		}
		capacity, _ := s.repositoryCapacityLocked(j.TargetRepositoryID)
		reserved := int64(0)
		for _, active := range s.helmSessions {
			if active.RepositoryID == j.TargetRepositoryID && active.State == "pending" && active.ExpiresAt.After(time.Now()) {
				reserved += active.Size + active.ProvenanceSize
			}
		}
		if capacity.QuotaBytes > 0 && capacity.UsedBytes+reserved > capacity.QuotaBytes {
			return dist.Job{}, ErrQuotaExceeded
		}
		if !a.ValidUntil.IsZero() && !a.ValidUntil.After(time.Now()) {
			return dist.Job{}, dist.ErrAccessDenied
		}
		if !helmReplicationLease(j, lease) {
			return dist.Job{}, dist.ErrLeaseLost
		}
		session.State = "committed"
		session.CommittedAt = time.Now().UTC()
		s.helmSessions[session.ID] = cloneHelmSession(session)
		target = helmPublication(session)
		if s.helmPublications == nil {
			s.helmPublications = map[string]HelmPublication{}
		}
		s.helmPublications[helmKey(j.TargetRepositoryID, item.Name, item.Version)] = cloneHelmPublication(target)
		s.appendAuditLocked(withRequestContextAudit(ctx, helmPublicationAudit(target, a.Target.Name)))
		item.Result = "published"
	}
	now := time.Now().UTC()
	item.State = "succeeded"
	item.Code = ""
	item.CompletedAt = &now
	j.Items[index] = item
	j.Version = dist.NextVersion(j.Version)
	j.UpdatedAt = now
	s.helmJobs[j.ID] = dist.Clone(j)
	s.appendAuditLocked(withRequestContextAudit(ctx, helmReplicationAudit(j, a.Target.Name, "item", 200, &item)))
	return dist.Clone(j), nil
}
func (s *MemoryStore) FailHelmReplicationItem(ctx context.Context, lease dist.Lease, index int, code string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	j := s.helmJobs[lease.JobID]
	if !helmReplicationLease(j, lease) {
		return dist.ErrLeaseLost
	}
	if index < 0 || index >= len(j.Items) || j.Items[index].State == "succeeded" {
		return dist.ErrConflict
	}
	if !dist.ValidFailureCode(code) {
		return dist.ErrInvalid
	}
	item := j.Items[index]
	if item.State == "pending" {
		item.Attempts++
	}
	item.State = "failed"
	item.Code = code
	now := time.Now().UTC()
	item.CompletedAt = &now
	j.Items[index] = item
	j.Version = dist.NextVersion(j.Version)
	j.UpdatedAt = now
	s.helmJobs[j.ID] = dist.Clone(j)
	name := s.hostedRepositories[j.TargetRepositoryID].Name
	s.appendAuditLocked(withRequestContextAudit(ctx, helmReplicationAudit(j, name, "item", dist.FailureStatus(code), &item)))
	return nil
}
func (s *MemoryStore) FinishHelmReplication(ctx context.Context, lease dist.Lease) (dist.Job, error) {
	if err := ctx.Err(); err != nil {
		return dist.Job{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	j := s.helmJobs[lease.JobID]
	if !helmReplicationLease(j, lease) {
		return dist.Job{}, dist.ErrLeaseLost
	}
	j.State = "succeeded"
	for _, item := range j.Items {
		if item.State == "pending" || item.State == "running" {
			return dist.Job{}, dist.ErrConflict
		}
		if item.State == "failed" {
			j.State = "failed"
		}
	}
	j.Lease.Owner = ""
	j.Lease.Token = ""
	j.Lease.Until = time.Time{}
	j.Version = dist.NextVersion(j.Version)
	j.UpdatedAt = time.Now().UTC()
	s.helmJobs[j.ID] = dist.Clone(j)
	status := 200
	if j.State == "failed" {
		status = 409
	}
	s.appendAuditLocked(withRequestContextAudit(ctx, helmReplicationAudit(j, s.hostedRepositories[j.TargetRepositoryID].Name, "complete", status, nil)))
	return dist.Clone(j), nil
}

func (s *MemoryStore) AuthorizeHelmReplicationItem(ctx context.Context, lease dist.Lease, index int, check dist.Check) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	j := s.helmJobs[lease.JobID]
	if !helmReplicationLease(j, lease) {
		return dist.ErrLeaseLost
	}
	if _, err := s.helmReplicationPreflightLocked(ctx, j, check); err != nil {
		return err
	}
	_, err := s.helmReplicationSourceLocked(j, index)
	return err
}
