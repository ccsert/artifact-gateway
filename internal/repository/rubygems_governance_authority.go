package repository

import (
	"context"
	"strings"
	"time"
)

// RubyGemsGovernanceIdentity contains only the already-authenticated subject
// references needed to re-read current authority. It never contains a token.
type RubyGemsGovernanceIdentity struct {
	Actor, Kind, CredentialID, UserID, SessionID string
	SessionVersion                               int64
	ValidUntil                                   time.Time
}

type RubyGemsGovernanceAuthority struct {
	Identity                   RubyGemsGovernanceIdentity
	Role                       string
	Active, MustChangePassword bool
	ValidUntil                 time.Time
	Repository                 HostedRepository
	Grants                     RepositoryGrantSet
}

func RubyGemsGovernanceKeyRole(roles []string) string {
	role := ""
	for _, r := range roles {
		if r == "admin" {
			return r
		}
		if r == "member" {
			role = r
		}
	}
	return role
}

func RubyGemsEarlierAuthorityExpiry(a, b time.Time) time.Time {
	if a.IsZero() || !b.IsZero() && b.Before(a) {
		return b
	}
	return a
}

// ReadRubyGemsGovernanceAuthority reads one consistent in-memory subject and
// grant snapshot; publication state remains protected by its domain mutex.
func (s *MemoryStore) ReadRubyGemsGovernanceAuthority(ctx context.Context, id string, in RubyGemsGovernanceIdentity) (RubyGemsGovernanceAuthority, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.rubyGemsGovernanceAuthorityLocked(ctx, id, in)
}

func (s *MemoryStore) rubyGemsGovernanceAuthorityLocked(ctx context.Context, id string, in RubyGemsGovernanceIdentity) (RubyGemsGovernanceAuthority, error) {
	a := RubyGemsGovernanceAuthority{Identity: in, ValidUntil: in.ValidUntil}
	r, ok := s.hostedRepositories[id]
	if !ok || r.Format != FormatRubyGems || r.Type != RepositoryTypeHosted || r.State != RepositoryActive {
		return a, ErrNotFound
	}
	a.Repository = r
	a.Grants = s.repositoryGrants[id]
	if a.Grants.Version == "" {
		a.Grants.Version = "1"
	}
	a.Grants.Grants = append([]RepositoryGrant(nil), a.Grants.Grants...)
	for i := range a.Grants.Grants {
		a.Grants.Grants[i].Scopes = append([]string(nil), a.Grants.Grants[i].Scopes...)
	}
	switch in.Kind {
	case "static_admin", "static_resolver":
		a.Active = true
	case "local_session", "oidc":
		for _, u := range s.users {
			if !strings.EqualFold(u.Name, strings.TrimPrefix(in.Actor, "user:")) || in.UserID != "" && in.UserID != u.ID {
				continue
			}
			a.Active = u.State == UserActive && (in.SessionVersion == 0 || in.SessionVersion == u.SessionVersion)
			a.Role = u.Role
			a.MustChangePassword = u.MustChangePassword
			if in.SessionID != "" {
				session, ok := s.userSessions[in.SessionID]
				a.Active = a.Active && ok && session.UserID == u.ID && session.RevokedAt == nil
				a.ValidUntil = RubyGemsEarlierAuthorityExpiry(a.ValidUntil, session.ExpiresAt)
			}
			break
		}
	case "api_key", "service_account_credential":
		keyID := strings.TrimPrefix(in.Actor, "api-key:")
		accountID := ""
		if in.Kind == "service_account_credential" {
			keyID = in.CredentialID
			accountID = strings.TrimPrefix(in.Actor, "service-account:")
			account, ok := s.serviceAccounts[accountID]
			if !ok || account.State != ServiceAccountActive {
				return a, ctx.Err()
			}
		}
		key, ok := s.apiKeys[keyID]
		a.Active = ok && key.ServiceAccountID == accountID && key.RevokedAt == nil
		a.Role = RubyGemsGovernanceKeyRole(key.Roles)
		if key.ExpiresAt != nil {
			a.ValidUntil = RubyGemsEarlierAuthorityExpiry(a.ValidUntil, *key.ExpiresAt)
		}
	}
	return a, ctx.Err()
}

// ChangeRubyGemsReadPolicy shares the repository mutex with credential/grant and
// generic policy mutations. The callback validates CAS and current authority;
// policy and any audits delegated back to this store become visible together
// only after the injected recordAudit succeeds.
func (s *MemoryStore) ChangeRubyGemsReadPolicy(ctx context.Context, id string, identity RubyGemsGovernanceIdentity, recordAudit func(context.Context, AuditRecord) error, apply func(RepositoryQuarantineReadPolicy, RubyGemsGovernanceAuthority) (RepositoryQuarantineReadPolicy, *AuditRecord, error)) (RepositoryQuarantineReadPolicy, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, err := s.rubyGemsGovernanceAuthorityLocked(ctx, id, identity)
	if err != nil {
		return RepositoryQuarantineReadPolicy{}, err
	}
	current, ok := s.quarantineReadPolicies[id]
	if !ok {
		current = DefaultRepositoryQuarantineReadPolicy()
	}
	next, audit, err := apply(current, a)
	if err != nil {
		return RepositoryQuarantineReadPolicy{}, err
	}
	if err := ctx.Err(); err != nil {
		return RepositoryQuarantineReadPolicy{}, err
	}
	if audit != nil {
		// The injected sink may wrap this store. Stage delegated RecordAudit calls
		// instead of reacquiring s.mu or exposing an audit before sink success.
		batch := &memoryAuditBatch{store: s}
		defer batch.finish()
		auditCtx := context.WithValue(ctx, memoryAuditBatchKey{}, batch)
		if err := recordAudit(auditCtx, *audit); err != nil {
			return RepositoryQuarantineReadPolicy{}, err
		}
		for _, record := range batch.finish() {
			s.appendAuditLocked(record)
		}
		s.quarantineReadPolicies[id] = next
	}
	return next, nil
}
