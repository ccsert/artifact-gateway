package rubygems

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ccsert/artifact-gateway/internal/authorization"
	"github.com/ccsert/artifact-gateway/internal/repository"
	domain "github.com/ccsert/artifact-gateway/internal/rubygemspublication"
)

type governanceGrants struct {
	id  string
	set repository.RepositoryGrantSet
}

func (g governanceGrants) GetRepositoryGrants(_ context.Context, id string) (repository.RepositoryGrantSet, error) {
	if id != g.id {
		return repository.RepositoryGrantSet{}, repository.ErrNotFound
	}
	return g.set, nil
}
func (governanceGrants) ReplaceRepositoryGrants(context.Context, string, []repository.RepositoryGrant, string) (repository.RepositoryGrantSet, error) {
	return repository.RepositoryGrantSet{}, domain.ErrAccessDenied
}
func (governanceGrants) UpsertRepositoryGrant(context.Context, string, repository.RepositoryGrant) (repository.RepositoryGrantSet, error) {
	return repository.RepositoryGrantSet{}, domain.ErrAccessDenied
}
func (governanceGrants) DeleteRepositoryGrant(context.Context, string, string, string) (repository.RepositoryGrantSet, error) {
	return repository.RepositoryGrantSet{}, domain.ErrAccessDenied
}

// Identity capture only narrows an already-verified credential. Session expiry
// and signed version are copied from that credential, never re-derived from a
// potentially newer database row. No token material crosses the persistence port.
func (m *Module) governanceIdentity(r *http.Request, p authorization.Principal) (repository.RubyGemsGovernanceIdentity, error) {
	in := repository.RubyGemsGovernanceIdentity{Actor: p.Actor, Kind: string(p.AuthenticationKind), CredentialID: p.CredentialID, UserID: p.UserID, SessionID: p.SessionID}
	if (p.AuthenticationKind == authorization.AuthenticationLocalSession || p.AuthenticationKind == authorization.AuthenticationOIDC) && strings.HasPrefix(p.Actor, "user:") && in.UserID == "" {
		if m.auth.Users == nil {
			return in, domain.ErrAccessDenied
		}
		u, err := m.auth.Users.GetUserByName(r.Context(), strings.TrimPrefix(p.Actor, "user:"))
		if err != nil {
			return in, domain.ErrAccessDenied
		}
		in.UserID = u.ID
	}
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if _, password, ok := r.BasicAuth(); ok {
		token = password
	}
	if p.AuthenticationKind == authorization.AuthenticationStaticAdmin || p.AuthenticationKind == authorization.AuthenticationStaticResolver && token == m.auth.ResolverToken {
		return in, nil
	}
	parts := strings.Split(token, ".")
	if len(parts) == 4 && (parts[0] == "us" || parts[0] == "ws" || parts[0] == "v1" || parts[0] == "v2") {
		exp, err := strconv.ParseInt(parts[2], 10, 64)
		if err != nil {
			return in, domain.ErrAccessDenied
		}
		in.ValidUntil = time.Unix(exp, 0).UTC()
		if parts[0] == "us" || parts[0] == "ws" {
			raw, err := base64.RawURLEncoding.DecodeString(parts[1])
			if err != nil {
				return in, domain.ErrAccessDenied
			}
			var claims struct {
				Version int64 `json:"v"`
			}
			if json.Unmarshal(raw, &claims) != nil {
				if parts[0] != "us" || string(raw) != p.UserID {
					return in, domain.ErrAccessDenied
				}
			}
			in.SessionVersion = claims.Version
			if in.SessionVersion == 0 {
				in.SessionVersion = 1
			}
		}
	} else if p.AuthenticationKind == authorization.AuthenticationOIDC {
		if len(parts) != 3 {
			return in, domain.ErrAccessDenied
		}
		raw, err := base64.RawURLEncoding.DecodeString(parts[1])
		if err != nil {
			return in, domain.ErrAccessDenied
		}
		var claims struct {
			Expiry int64 `json:"exp"`
		}
		if json.Unmarshal(raw, &claims) != nil || claims.Expiry <= 0 {
			return in, domain.ErrAccessDenied
		}
		in.ValidUntil = time.Unix(claims.Expiry, 0).UTC()
	}
	return in, nil
}

func (m *Module) governanceAuthorityCheck(initial repository.RubyGemsGovernanceIdentity, repoID, name string, op authorization.RepositoryOperation) domain.GovernanceCheck {
	return func(ctx context.Context, a repository.RubyGemsGovernanceAuthority) error {
		if a.Identity != initial || a.Repository.ID != repoID || !domain.Active(a.Repository) {
			return domain.ErrAccessDenied
		}
		p, ok := m.auth.DurablePrincipal(a.Identity.Actor, authorization.AuthenticationKind(a.Identity.Kind), a.Role, a.Active, a.MustChangePassword)
		if !ok {
			return domain.ErrAccessDenied
		}
		access := authorization.RepositoryAuthorizer{Grants: governanceGrants{repoID, a.Grants}, Legacy: m.auth}
		if !access.AuthorizeResource(ctx, p, a.Repository, op, name).Allowed {
			return domain.ErrAccessDenied
		}
		return nil
	}
}
