package nexusmigration

import (
	"context"
	"strings"
)

// OperatorIdentity is trusted server-side authentication evidence. HTTP handlers
// construct it from the authenticated principal, never from request JSON or an
// actor's display name. No credential secret or authentication claims are stored.
type OperatorIdentity struct {
	Actor              string
	AuthenticationKind string
	UserID             string
}
type operatorIdentityKey struct{}

func WithOperatorIdentity(ctx context.Context, identity OperatorIdentity) context.Context {
	return context.WithValue(ctx, operatorIdentityKey{}, identity)
}
func operatorBinding(ctx context.Context, actor string) (kind, ownerID string) {
	identity, ok := ctx.Value(operatorIdentityKey{}).(OperatorIdentity)
	if !ok || identity.Actor != actor {
		return "", ""
	}
	switch identity.AuthenticationKind {
	case "api_key":
		if strings.HasPrefix(actor, "api-key:") {
			ownerID = strings.TrimPrefix(actor, "api-key:")
		}
	case "local_session", "oidc":
		ownerID = identity.UserID
	}
	return identity.AuthenticationKind, ownerID
}

type migrationActorValidator interface {
	ValidateMigrationActor(context.Context, string, string, string, []Mapping) (bool, error)
}

func (s *Service) validateMigrationActor(ctx context.Context, actor, kind, ownerID string, mappings []Mapping) error {
	if check, ok := s.publication.(migrationActorValidator); ok {
		_, err := check.ValidateMigrationActor(ctx, actor, kind, ownerID, mappings)
		return err
	}
	return nil
}

// Old format paths do not acquire a new identity policy. Only a format explicitly
// requiring bound ownership persists the trusted kind and stable subject ID.
func (s *Service) migrationActor(ctx context.Context, actor string, mappings []Mapping) (kind, ownerID string, err error) {
	kind, ownerID = operatorBinding(ctx, actor)
	if check, ok := s.publication.(migrationActorValidator); ok {
		required, err := check.ValidateMigrationActor(ctx, actor, kind, ownerID, mappings)
		if err != nil {
			return "", "", err
		}
		if required {
			return kind, ownerID, nil
		}
	}
	return "", "", nil
}
