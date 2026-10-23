package authorization

// DurablePrincipal rebuilds authority from a transaction-locked current
// subject snapshot. It never restores the submitter's admin/OIDC claims or
// persists a bearer. Callers must supply a fresh snapshot at every execution.
func (a Authenticator) DurablePrincipal(actor string, kind AuthenticationKind, role string, active, mustChangePassword bool) (Principal, bool) {
	if actor == "" || !active {
		return Principal{}, false
	}
	p := Principal{Actor: actor, AuthenticationKind: kind, RepositoryPatterns: a.RepositoryReaders[actor], MustChangePassword: mustChangePassword}
	switch kind {
	case AuthenticationStaticAdmin:
		if a.AdminToken == "" || a.AdminActor != actor {
			return Principal{}, false
		}
		p.Admin = true
		p.Role = RoleAdmin
	case AuthenticationStaticResolver:
		if a.ResolverToken == "" || a.ResolverActor == "" || a.ResolverActor != actor {
			return Principal{}, false
		}
	case AuthenticationLocalSession, AuthenticationOIDC, AuthenticationAPIKey:
		p.Role = Role(role)
		if !validGatewayRole(p.Role) {
			return Principal{}, false
		}
		p.Admin = p.Role == RoleAdmin
	case AuthenticationServiceAccountCredential:
	default:
		return Principal{}, false
	}
	return p, p.AccountStateReason() == ""
}
