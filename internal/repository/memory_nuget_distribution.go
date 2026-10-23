package repository

import (
	"context"
	"sort"
	"strings"
	"time"

	dist "github.com/ccsert/artifact-gateway/internal/nugetdistribution"
	"github.com/google/uuid"
)

func (s *MemoryStore) nuGetReplicationAuthorityLocked(j dist.Job) (dist.Authority, error) {
	a := dist.Authority{Actor: j.Actor, Kind: j.ActorKind}
	src, se := s.activeNuGetRepositoryLocked(j.SourceRepositoryID)
	dst, de := s.activeNuGetRepositoryLocked(j.TargetRepositoryID)
	if se != nil || de != nil || src.Type != RepositoryTypeHosted || dst.Type != RepositoryTypeHosted {
		return a, dist.ErrNotFound
	}
	a.Source = nuGetReplicationGrantSet(src, s.repositoryGrants[src.ID])
	a.Target = nuGetReplicationGrantSet(dst, s.repositoryGrants[dst.ID])
	if !nuGetReplicationActorValid(j) {
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
		a.Role = nuGetReplicationKeyRole(k.Roles)
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
func (s *MemoryStore) nuGetReplicationSourceLocked(j dist.Job, i int) (NuGetPublication, error) {
	if i < 0 || i >= len(j.Items) {
		return NuGetPublication{}, dist.ErrInvalid
	}
	item := j.Items[i]
	p, ok := s.nugetPublications[nuGetPublicationKey(j.SourceRepositoryID, item.PackageID, item.Version)]
	if !ok {
		return p, dist.ErrSourceChanged
	}
	return p, nuGetReplicationSource(p, item)
}
func (s *MemoryStore) nuGetReplicationPreflightLocked(ctx context.Context, j dist.Job, check dist.Check) (dist.Authority, error) {
	a, err := s.nuGetReplicationAuthorityLocked(j)
	if err != nil {
		return a, err
	}
	if err = nuGetReplicationCheck(ctx, check, a); err != nil {
		return a, err
	}
	return a, nil
}
func (s *MemoryStore) CreateNuGetReplication(ctx context.Context, j dist.Job, check dist.Check) (dist.Job, bool, error) {
	if !dist.ValidNewJob(j) {
		return dist.Job{}, false, dist.ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	a, err := s.nuGetReplicationPreflightLocked(ctx, j, check)
	if err != nil {
		return dist.Job{}, false, err
	}
	if old, ok := s.nugetJobs[j.ID]; ok {
		if old.PlanDigest != j.PlanDigest || old.Actor != j.Actor || old.ActorKind != j.ActorKind || old.CredentialID != j.CredentialID {
			return dist.Job{}, false, dist.ErrConflict
		}
		return dist.Clone(old), true, nil
	}
	extra := int64(0)
	for i, item := range j.Items {
		if _, err = s.nuGetReplicationSourceLocked(j, i); err != nil {
			return dist.Job{}, false, err
		}
		if _, ok := s.nugetPublications[nuGetPublicationKey(j.TargetRepositoryID, item.PackageID, item.Version)]; !ok {
			extra += item.Size
		}
	}
	capacity, _ := s.repositoryCapacityLocked(j.TargetRepositoryID)
	for _, session := range s.nugetSessions {
		if session.RepositoryID == j.TargetRepositoryID && (session.State == "reserved" || session.State == "staged") && session.ExpiresAt.After(time.Now()) {
			extra += session.Size
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
	if s.nugetJobs == nil {
		s.nugetJobs = map[string]dist.Job{}
	}
	s.nugetJobs[j.ID] = dist.Clone(j)
	s.appendAuditLocked(withRequestContextAudit(ctx, nuGetReplicationAudit(j, a.Target.Name, "create", 202, nil)))
	return dist.Clone(j), false, nil
}
func (s *MemoryStore) GetNuGetReplication(ctx context.Context, id string) (dist.Job, error) {
	if err := ctx.Err(); err != nil {
		return dist.Job{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	j, ok := s.nugetJobs[id]
	if !ok {
		return dist.Job{}, dist.ErrNotFound
	}
	return dist.Clone(j), nil
}
func (s *MemoryStore) RetryNuGetReplication(ctx context.Context, id, expected string, indexes []int, key string, check dist.Check) (dist.Job, bool, error) {
	digest, err := dist.RetryDigest(expected, indexes)
	if err != nil || key == "" || len(key) > 200 || strings.ContainsAny(key, "\x00\r\n") {
		return dist.Job{}, false, dist.ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.nugetJobs[id]
	if !ok {
		return dist.Job{}, false, dist.ErrNotFound
	}
	a, err := s.nuGetReplicationPreflightLocked(ctx, j, check)
	if err != nil {
		return dist.Job{}, false, err
	}
	record := id + "\x00" + key
	if old, ok := s.nugetRetries[record]; ok {
		if old != digest {
			return dist.Job{}, false, dist.ErrConflict
		}
		return dist.Clone(j), true, nil
	}
	j, err = nuGetReplicationRetry(j, expected, indexes)
	if err != nil {
		return dist.Job{}, false, err
	}
	if s.nugetRetries == nil {
		s.nugetRetries = map[string]string{}
	}
	s.nugetRetries[record] = digest
	s.nugetJobs[id] = dist.Clone(j)
	s.appendAuditLocked(withRequestContextAudit(ctx, nuGetReplicationAudit(j, a.Target.Name, "retry", 202, nil)))
	return dist.Clone(j), false, nil
}
func (s *MemoryStore) ClaimNuGetReplication(ctx context.Context, owner string, d time.Duration) (dist.Job, error) {
	if err := ctx.Err(); err != nil {
		return dist.Job{}, err
	}
	if owner == "" || len(owner) > 512 || !dist.ValidDuration(d) {
		return dist.Job{}, dist.ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	jobs := []dist.Job{}
	for _, j := range s.nugetJobs {
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
	s.nugetJobs[j.ID] = dist.Clone(j)
	return j, nil
}
func (s *MemoryStore) RenewNuGetReplication(ctx context.Context, lease dist.Lease, d time.Duration) (dist.Lease, error) {
	if err := ctx.Err(); err != nil {
		return dist.Lease{}, err
	}
	if !dist.ValidDuration(d) {
		return dist.Lease{}, dist.ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	j := s.nugetJobs[lease.JobID]
	if !nuGetReplicationLease(j, lease) {
		return dist.Lease{}, dist.ErrLeaseLost
	}
	j.Lease.Until = time.Now().UTC().Add(d)
	s.nugetJobs[j.ID] = j
	return j.Lease, nil
}
func (s *MemoryStore) BindNuGetReplicationSession(ctx context.Context, lease dist.Lease, index int, sessionID string, check dist.Check) (dist.Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j := s.nugetJobs[lease.JobID]
	if !nuGetReplicationLease(j, lease) {
		return dist.Job{}, dist.ErrLeaseLost
	}
	a, err := s.nuGetReplicationPreflightLocked(ctx, j, check)
	if err != nil {
		return dist.Job{}, err
	}
	if !nuGetReplicationLease(j, lease) {
		return dist.Job{}, dist.ErrLeaseLost
	}
	if !a.ValidUntil.IsZero() && !a.ValidUntil.After(time.Now()) {
		return dist.Job{}, dist.ErrAccessDenied
	}
	source, err := s.nuGetReplicationSourceLocked(j, index)
	if err != nil {
		return dist.Job{}, err
	}
	session, ok := s.nugetSessions[sessionID]
	if !ok || session.RepositoryID != j.TargetRepositoryID || session.PackageID != source.PackageID || session.Version != source.Version || session.Digest != source.Digest || session.Size != source.Size || session.SourceVersion != "" {
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
	s.nugetJobs[j.ID] = dist.Clone(j)
	return dist.Clone(j), nil
}
func (s *MemoryStore) CompleteNuGetReplicationItem(ctx context.Context, lease dist.Lease, index int, proof dist.Commit, check dist.Check) (dist.Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j := s.nugetJobs[lease.JobID]
	if !nuGetReplicationLease(j, lease) {
		return dist.Job{}, dist.ErrLeaseLost
	}
	a, err := s.nuGetReplicationPreflightLocked(ctx, j, check)
	if err != nil {
		return dist.Job{}, err
	}
	source, err := s.nuGetReplicationSourceLocked(j, index)
	if err != nil {
		return dist.Job{}, err
	}
	if !nuGetReplicationLease(j, lease) {
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
	session, ok := s.nugetSessions[proof.SessionID]
	if !ok || session.RepositoryID != j.TargetRepositoryID {
		return dist.Job{}, dist.ErrTargetBlocked
	}
	target, exists := s.nugetPublications[nuGetPublicationKey(j.TargetRepositoryID, item.PackageID, item.Version)]
	if exists {
		if err = nuGetReplicationExisting(source, target); err != nil {
			return dist.Job{}, err
		}
		if target.SessionID != session.ID {
			return dist.Job{}, dist.ErrTargetBlocked
		}
		item.Result = "already_present"
	} else {
		if err = nuGetReplicationSession(source, session, j.TargetRepositoryID); err != nil {
			return dist.Job{}, err
		}
		if session.Actor != j.Actor || session.State != "staged" || !session.ExpiresAt.After(time.Now()) || !validNuGetStaging(session) {
			return dist.Job{}, dist.ErrTargetBlocked
		}
		capacity, _ := s.repositoryCapacityLocked(j.TargetRepositoryID)
		reserved := int64(0)
		for _, active := range s.nugetSessions {
			if active.RepositoryID == j.TargetRepositoryID && (active.State == "reserved" || active.State == "staged") && active.ExpiresAt.After(time.Now()) {
				reserved += active.Size
			}
		}
		if capacity.QuotaBytes > 0 && capacity.UsedBytes+reserved > capacity.QuotaBytes {
			return dist.Job{}, ErrQuotaExceeded
		}
		if !a.ValidUntil.IsZero() && !a.ValidUntil.After(time.Now()) {
			return dist.Job{}, dist.ErrAccessDenied
		}
		if !nuGetReplicationLease(j, lease) {
			return dist.Job{}, dist.ErrLeaseLost
		}
		session.State = "committed"
		session.CommittedAt = time.Now().UTC()
		s.nugetSessions[session.ID] = cloneNuGetSession(session)
		target = nuGetPublication(session, session.CommittedAt)
		if s.nugetPublications == nil {
			s.nugetPublications = map[string]NuGetPublication{}
		}
		s.nugetPublications[nuGetPublicationKey(j.TargetRepositoryID, item.PackageID, item.Version)] = cloneNuGetPublication(target)
		s.appendAuditLocked(withRequestContextAudit(ctx, nuGetPublicationAudit(target, a.Target.Name)))
		item.Result = "published"
	}
	now := time.Now().UTC()
	item.State = "succeeded"
	item.Code = ""
	listed := target.Governance.Listed
	item.Listed = &listed
	item.CompletedAt = &now
	j.Items[index] = item
	j.Version = dist.NextVersion(j.Version)
	j.UpdatedAt = now
	s.nugetJobs[j.ID] = dist.Clone(j)
	s.appendAuditLocked(withRequestContextAudit(ctx, nuGetReplicationAudit(j, a.Target.Name, "item", 200, &item)))
	return dist.Clone(j), nil
}
func (s *MemoryStore) FailNuGetReplicationItem(ctx context.Context, lease dist.Lease, index int, code string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	j := s.nugetJobs[lease.JobID]
	if !nuGetReplicationLease(j, lease) {
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
	s.nugetJobs[j.ID] = dist.Clone(j)
	name := s.hostedRepositories[j.TargetRepositoryID].Name
	s.appendAuditLocked(withRequestContextAudit(ctx, nuGetReplicationAudit(j, name, "item", dist.FailureStatus(code), &item)))
	return nil
}
func (s *MemoryStore) FinishNuGetReplication(ctx context.Context, lease dist.Lease) (dist.Job, error) {
	if err := ctx.Err(); err != nil {
		return dist.Job{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	j := s.nugetJobs[lease.JobID]
	if !nuGetReplicationLease(j, lease) {
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
	s.nugetJobs[j.ID] = dist.Clone(j)
	status := 200
	if j.State == "failed" {
		status = 409
	}
	s.appendAuditLocked(withRequestContextAudit(ctx, nuGetReplicationAudit(j, s.hostedRepositories[j.TargetRepositoryID].Name, "complete", status, nil)))
	return dist.Clone(j), nil
}

func (s *MemoryStore) AuthorizeNuGetReplicationItem(ctx context.Context, lease dist.Lease, index int, check dist.Check) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	j := s.nugetJobs[lease.JobID]
	if !nuGetReplicationLease(j, lease) {
		return dist.ErrLeaseLost
	}
	if _, err := s.nuGetReplicationPreflightLocked(ctx, j, check); err != nil {
		return err
	}
	_, err := s.nuGetReplicationSourceLocked(j, index)
	return err
}
