package authorization

import "testing"

func TestDurablePrincipalUsesCurrentIdentityAndAuthority(t *testing.T) {
	auth := Authenticator{AdminToken: "current-admin-secret", AdminActor: "current-admin", ResolverToken: "current-reader-secret", ResolverActor: "current-reader"}
	for _, tt := range []struct {
		name, actor, role             string
		kind                          AuthenticationKind
		active, password, want, admin bool
	}{
		{"admin current", "current-admin", "", AuthenticationStaticAdmin, true, false, true, true},
		{"admin replaced", "old-admin", "admin", AuthenticationStaticAdmin, true, false, false, false},
		{"resolver current", "current-reader", "admin", AuthenticationStaticResolver, true, false, true, false},
		{"resolver replaced", "old-reader", "admin", AuthenticationStaticResolver, true, false, false, false},
		{"user demoted", "user:operator", "member", AuthenticationLocalSession, true, false, true, false},
		{"user disabled", "user:operator", "admin", AuthenticationLocalSession, false, false, false, false},
		{"user password reset", "user:operator", "admin", AuthenticationLocalSession, true, true, false, false},
		{"pending user", "user:operator", "none", AuthenticationLocalSession, true, false, false, false},
		{"user missing role", "user:operator", "", AuthenticationLocalSession, true, false, false, false},
		{"OIDC missing role", "user:operator", "", AuthenticationOIDC, true, false, false, false},
		{"OIDC pending", "user:operator", "none", AuthenticationOIDC, true, false, false, false},
		{"OIDC disabled", "user:operator", "member", AuthenticationOIDC, false, false, false, false},
		{"repository-only key", "api-key:key", "", AuthenticationAPIKey, true, false, true, false},
		{"key member", "api-key:key", "member", AuthenticationAPIKey, true, false, true, false},
		{"key admin", "api-key:key", "admin", AuthenticationAPIKey, true, false, true, true},
		{"key unknown role", "api-key:key", "unknown", AuthenticationAPIKey, true, false, false, false},
		{"key pending", "api-key:key", "none", AuthenticationAPIKey, true, false, false, false},
		{"repository-only key missing or revoked", "api-key:key", "", AuthenticationAPIKey, false, false, false, false},
		{"repository-only key password reset", "api-key:key", "", AuthenticationAPIKey, true, true, false, false},
		{"key revoked", "api-key:key", "admin", AuthenticationAPIKey, false, false, false, false},
		{"service disabled", "service-account:account", "", AuthenticationServiceAccountCredential, false, false, false, false},
		{"external unknown", "external-subject", "admin", AuthenticationKind("unknown"), true, false, false, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p, ok := auth.DurablePrincipal(tt.actor, tt.kind, tt.role, tt.active, tt.password)
			if ok != tt.want || ok && p.Admin != tt.admin {
				t.Fatalf("principal=%+v allowed=%t", p, ok)
			}
		})
	}
	for _, empty := range []Authenticator{{ResolverToken: "configured"}, {ResolverActor: "current-reader"}, {AdminToken: "configured"}, {AdminActor: "current-admin"}} {
		if _, ok := empty.DurablePrincipal("current-reader", AuthenticationStaticResolver, "", true, false); ok {
			t.Fatal("incomplete resolver identity accepted")
		}
		if _, ok := empty.DurablePrincipal("current-admin", AuthenticationStaticAdmin, "admin", true, false); ok {
			t.Fatal("incomplete administrator identity accepted")
		}
	}
}
