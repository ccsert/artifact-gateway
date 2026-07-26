package repository

import (
	"context"
	"time"
)

// APIKey is a revocable administrative credential. SecretHash is never exposed
// through management responses and is only used for bearer-token verification.
type APIKey struct {
	ID         string
	Name       string
	SecretHash string
	Roles      []string
	CreatedAt  time.Time
	RevokedAt  *time.Time
}

type APIKeyStore interface {
	CreateAPIKey(context.Context, APIKey) (APIKey, error)
	ListAPIKeys(context.Context) ([]APIKey, error)
	FindActiveAPIKeyByHash(context.Context, string) (APIKey, error)
	RevokeAPIKey(context.Context, string) (APIKey, error)
}
