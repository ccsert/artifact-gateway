package pgstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/ccsert/artifact-gateway/internal/repository"
	domain "github.com/ccsert/artifact-gateway/internal/rubygemspublication"
)

// All reads use this transaction and connection. Grant replacements lock the
// same repository and grant-set rows; account/key/session changes conflict with
// these FOR SHARE locks until publication and its audit commit together.
func governanceAuthorityTx(ctx context.Context, tx *sql.Tx, c domain.GovernanceChange) (repository.RubyGemsGovernanceAuthority, error) {
	in := c.Identity
	a := repository.RubyGemsGovernanceAuthority{Identity: in, ValidUntil: in.ValidUntil}
	if in.Actor != c.Actor {
		return a, domain.ErrAccessDenied
	}
	if err := tx.QueryRowContext(ctx, `SELECT id::text,name,format,repo_type,state FROM hosted_repositories WHERE id::text=$1`, c.RepositoryID).Scan(&a.Repository.ID, &a.Repository.Name, &a.Repository.Format, &a.Repository.Type, &a.Repository.State); err != nil {
		return a, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO repository_grant_sets(repository_id,version) VALUES($1,1) ON CONFLICT DO NOTHING`, c.RepositoryID); err != nil {
		return a, err
	}
	if err := tx.QueryRowContext(ctx, `SELECT version::text FROM repository_grant_sets WHERE repository_id::text=$1 FOR SHARE`, c.RepositoryID).Scan(&a.Grants.Version); err != nil {
		return a, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT principal,array_to_json(scopes),resource_prefix FROM repository_grants WHERE repository_id::text=$1 ORDER BY principal,resource_prefix`, c.RepositoryID)
	if err != nil {
		return a, err
	}
	for rows.Next() {
		var g repository.RepositoryGrant
		var scopes []byte
		if err = rows.Scan(&g.Principal, &scopes, &g.ResourcePrefix); err != nil {
			break
		}
		if err = json.Unmarshal(scopes, &g.Scopes); err != nil {
			break
		}
		a.Grants.Grants = append(a.Grants.Grants, g)
	}
	readErr := rows.Err()
	closeErr := rows.Close()
	if err != nil {
		return a, err
	}
	if readErr != nil {
		return a, readErr
	}
	if closeErr != nil {
		return a, closeErr
	}
	switch in.Kind {
	case "static_admin", "static_resolver":
		a.Active = true
	case "local_session", "oidc":
		if !strings.HasPrefix(in.Actor, "user:") {
			return a, domain.ErrAccessDenied
		}
		var id, state string
		var version int64
		err = tx.QueryRowContext(ctx, `SELECT id::text,role,state,must_change_password,session_version FROM users WHERE lower(name)=lower($1) FOR SHARE`, strings.TrimPrefix(in.Actor, "user:")).Scan(&id, &a.Role, &state, &a.MustChangePassword, &version)
		if errors.Is(err, sql.ErrNoRows) {
			return a, nil
		}
		if err != nil {
			return a, err
		}
		a.Active = state == repository.UserActive && (in.UserID == "" || in.UserID == id) && (in.SessionVersion == 0 || in.SessionVersion == version)
		if in.SessionID != "" {
			var revoked sql.NullTime
			var expiry sql.NullTime
			err = tx.QueryRowContext(ctx, `SELECT revoked_at,expires_at FROM user_sessions WHERE user_id::text=$1 AND id::text=$2 FOR SHARE`, id, in.SessionID).Scan(&revoked, &expiry)
			if errors.Is(err, sql.ErrNoRows) {
				a.Active = false
				return a, nil
			}
			if err != nil {
				return a, err
			}
			a.Active = a.Active && !revoked.Valid && expiry.Valid
			if expiry.Valid {
				a.ValidUntil = repository.RubyGemsEarlierAuthorityExpiry(a.ValidUntil, expiry.Time)
			}
		}
	case "api_key", "service_account_credential":
		credential := strings.TrimPrefix(in.Actor, "api-key:")
		account := ""
		if in.Kind == "service_account_credential" {
			if !strings.HasPrefix(in.Actor, "service-account:") || in.CredentialID == "" {
				return a, domain.ErrAccessDenied
			}
			credential = in.CredentialID
			account = strings.TrimPrefix(in.Actor, "service-account:")
			var state string
			err = tx.QueryRowContext(ctx, `SELECT state FROM service_accounts WHERE id::text=$1 FOR SHARE`, account).Scan(&state)
			if errors.Is(err, sql.ErrNoRows) {
				return a, nil
			}
			if err != nil {
				return a, err
			}
			if state != string(repository.ServiceAccountActive) {
				return a, nil
			}
		} else if !strings.HasPrefix(in.Actor, "api-key:") {
			return a, domain.ErrAccessDenied
		}
		var roles []byte
		var revoked, expires sql.NullTime
		err = tx.QueryRowContext(ctx, `SELECT array_to_json(roles),revoked_at,expires_at FROM api_keys WHERE id::text=$1 AND COALESCE(service_account_id::text,'')=$2 FOR SHARE`, credential, account).Scan(&roles, &revoked, &expires)
		if errors.Is(err, sql.ErrNoRows) {
			return a, nil
		}
		if err != nil {
			return a, err
		}
		var values []string
		if err = json.Unmarshal(roles, &values); err != nil {
			return a, err
		}
		a.Role = repository.RubyGemsGovernanceKeyRole(values)
		a.Active = !revoked.Valid
		if expires.Valid {
			a.ValidUntil = repository.RubyGemsEarlierAuthorityExpiry(a.ValidUntil, expires.Time)
		}
	default:
		return a, domain.ErrAccessDenied
	}
	return a, nil
}
