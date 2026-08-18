package repository

import (
	"context"
	"time"
)

// APIKey is a revocable bearer credential. Standalone keys retain their own
// roles; service-account credentials inherit the stable account principal and
// never use the credential ID as an authorization subject. SecretHash is never
// exposed through management responses.
type APIKey struct {
	ID               string
	ServiceAccountID string
	Name             string
	SecretHash       string
	Roles            []string
	CreatedAt        time.Time
	RevokedAt        *time.Time
	ExpiresAt        *time.Time
	LastUsedAt       *time.Time
}

type APIKeyStore interface {
	CreateAPIKey(context.Context, APIKey) (APIKey, error)
	ListAPIKeys(context.Context) ([]APIKey, error)
	FindActiveAPIKeyByHash(context.Context, string) (APIKey, error)
	RevokeAPIKey(context.Context, string) (APIKey, error)
}
