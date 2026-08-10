package repository

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
)

func (s *MemoryStore) ListUserIdentities(_ context.Context, userID string) ([]UserIdentity, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, ok := s.users[userID]; !ok {
		return nil, ErrNotFound
	}
	items := make([]UserIdentity, 0)
	for _, identity := range s.userIdentities {
		if identity.UserID == userID {
			items = append(items, identity)
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].CreatedAt.Equal(items[j].CreatedAt) {
			return items[i].ID < items[j].ID
		}
		return items[i].CreatedAt.Before(items[j].CreatedAt)
	})
	return items, nil
}

func (s *MemoryStore) CreateUserIdentity(_ context.Context, identity UserIdentity) (UserIdentity, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.users[identity.UserID]; !ok {
		return UserIdentity{}, ErrNotFound
	}
	identity = normalizeUserIdentity(identity)
	if identity.Kind != UserIdentityOIDC || identity.Issuer == "" || identity.Subject == "" {
		return UserIdentity{}, ErrIdentityExists
	}
	for _, existing := range s.userIdentities {
		if existing.Kind == identity.Kind && existing.Issuer == identity.Issuer && (existing.Subject == identity.Subject || existing.UserID == identity.UserID) {
			return UserIdentity{}, ErrIdentityExists
		}
	}
	if identity.ID == "" {
		identity.ID = uuid.NewString()
	}
	now := time.Now().UTC()
	identity.CreatedAt = now
	identity.UpdatedAt = now
	s.userIdentities[identity.ID] = identity
	return identity, nil
}

func (s *MemoryStore) DeleteUserIdentity(_ context.Context, userID, identityID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	identity, ok := s.userIdentities[identityID]
	if !ok || identity.UserID != userID {
		return ErrNotFound
	}
	delete(s.userIdentities, identityID)
	return nil
}

func (s *MemoryStore) GetUserByOIDCIdentity(_ context.Context, issuer, subject string) (User, UserIdentity, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	issuer = normalizeOIDCIssuer(issuer)
	for _, identity := range s.userIdentities {
		if identity.Kind != UserIdentityOIDC || identity.Issuer != issuer || identity.Subject != strings.TrimSpace(subject) {
			continue
		}
		user, ok := s.users[identity.UserID]
		if !ok {
			return User{}, UserIdentity{}, ErrNotFound
		}
		return user, identity, nil
	}
	return User{}, UserIdentity{}, ErrNotFound
}

func (s *MemoryStore) ResolveOIDCIdentity(_ context.Context, provision OIDCIdentityProvision) (User, UserIdentity, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	provision.Issuer = normalizeOIDCIssuer(provision.Issuer)
	provision.Subject = strings.TrimSpace(provision.Subject)
	if provision.Issuer == "" || provision.Subject == "" {
		return User{}, UserIdentity{}, false, ErrNotFound
	}
	now := provision.OccurredAt.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	for id, identity := range s.userIdentities {
		if identity.Kind != UserIdentityOIDC || identity.Issuer != provision.Issuer || identity.Subject != provision.Subject {
			continue
		}
		user, ok := s.users[identity.UserID]
		if !ok {
			return User{}, UserIdentity{}, false, ErrNotFound
		}
		identity = refreshUserIdentity(identity, provision, now)
		user.LastLoginAt = timePointer(now)
		user.UpdatedAt = now
		user.Version = nextHostedGroupVersion(user.Version)
		s.userIdentities[id] = identity
		s.users[user.ID] = user
		return user, identity, false, nil
	}
	if !provision.Provision {
		return User{}, UserIdentity{}, false, ErrNotFound
	}

	var user User
	if provision.MatchEmail && provision.EmailVerified && strings.TrimSpace(provision.Email) != "" {
		for _, candidate := range s.users {
			if !strings.EqualFold(candidate.Email, strings.TrimSpace(provision.Email)) {
				continue
			}
			if user.ID != "" {
				return User{}, UserIdentity{}, false, ErrIdentityAmbiguous
			}
			user = candidate
		}
	}
	created := false
	if user.ID == "" {
		user = newOIDCProvisionedUser(provision, s.users, now)
		s.users[user.ID] = user
		created = true
	} else {
		for _, existing := range s.userIdentities {
			if existing.UserID == user.ID && existing.Kind == UserIdentityOIDC && existing.Issuer == provision.Issuer {
				return User{}, UserIdentity{}, false, ErrIdentityExists
			}
		}
		user.LastLoginAt = timePointer(now)
		user.UpdatedAt = now
		user.Version = nextHostedGroupVersion(user.Version)
		s.users[user.ID] = user
	}
	identity := refreshUserIdentity(UserIdentity{
		ID: uuid.NewString(), UserID: user.ID, Kind: UserIdentityOIDC,
		Issuer: provision.Issuer, Subject: provision.Subject, CreatedAt: now,
	}, provision, now)
	s.userIdentities[identity.ID] = identity
	return user, identity, created, nil
}

func normalizeUserIdentity(identity UserIdentity) UserIdentity {
	identity.Kind = strings.TrimSpace(identity.Kind)
	identity.Issuer = normalizeOIDCIssuer(identity.Issuer)
	identity.Subject = strings.TrimSpace(identity.Subject)
	identity.Email = strings.TrimSpace(identity.Email)
	identity.DisplayName = strings.TrimSpace(identity.DisplayName)
	return identity
}

func normalizeOIDCIssuer(value string) string {
	return strings.TrimRight(strings.TrimSpace(value), "/")
}

func refreshUserIdentity(identity UserIdentity, provision OIDCIdentityProvision, now time.Time) UserIdentity {
	identity.Email = strings.TrimSpace(provision.Email)
	identity.DisplayName = strings.TrimSpace(provision.DisplayName)
	identity.EmailVerified = provision.EmailVerified
	identity.LastLoginAt = timePointer(now)
	identity.UpdatedAt = now
	return identity
}

func newOIDCProvisionedUser(provision OIDCIdentityProvision, existing map[string]User, now time.Time) User {
	role := provision.Role
	if role != "admin" && role != "writer" && role != "reader" {
		role = provision.DefaultRole
	}
	if role != "admin" && role != "writer" && role != "reader" {
		role = "reader"
	}
	name := provisionedUsername(provision.PreferredUsername, provision.Email, provision.Issuer, provision.Subject, existing)
	return User{
		ID: uuid.NewString(), Name: name, DisplayName: strings.TrimSpace(provision.DisplayName),
		Email: strings.TrimSpace(provision.Email), Role: role, State: UserActive,
		LastLoginAt: timePointer(now), SessionVersion: 1,
		CreatedAt: now, UpdatedAt: now, Version: "1",
	}
}

func provisionedUsername(preferred, email, issuer, subject string, existing map[string]User) string {
	base := provisionedUsernameBase(preferred, email)
	available := true
	for _, user := range existing {
		if strings.EqualFold(user.Name, base) {
			available = false
			break
		}
	}
	if available {
		return base
	}
	return provisionedUsernameSuffix(base, issuer, subject)
}

func provisionedUsernameBase(preferred, email string) string {
	base := sanitizeUsername(preferred)
	if base == "" {
		if local, _, ok := strings.Cut(strings.TrimSpace(email), "@"); ok {
			base = sanitizeUsername(local)
		}
	}
	if base == "" {
		base = "oidc-user"
	}
	runes := []rune(base)
	if len(runes) > 100 {
		base = string(runes[:100])
	}
	return base
}

func provisionedUsernameSuffix(base, issuer, subject string) string {
	digest := sha256.Sum256([]byte(issuer + "\x00" + subject))
	return base + "-" + hex.EncodeToString(digest[:5])
}

func sanitizeUsername(value string) string {
	value = strings.TrimSpace(value)
	var builder strings.Builder
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '.' || r == '_' || r == '-' {
			builder.WriteRune(r)
		}
	}
	return builder.String()
}
