package repository

import "testing"

func TestUpstreamAuthValidate(t *testing.T) {
	cases := []struct {
		name    string
		auth    *UpstreamAuth
		wantErr bool
	}{
		{"nil is anonymous", nil, false},
		{"basic with username and secret", &UpstreamAuth{Scheme: UpstreamAuthSchemeBasic, Username: "gateway", Secret: "ciphertext"}, false},
		{"basic without username", &UpstreamAuth{Scheme: UpstreamAuthSchemeBasic, Secret: "ciphertext"}, true},
		{"basic with blank username", &UpstreamAuth{Scheme: UpstreamAuthSchemeBasic, Username: "   ", Secret: "ciphertext"}, true},
		{"basic without secret", &UpstreamAuth{Scheme: UpstreamAuthSchemeBasic, Username: "gateway"}, true},
		{"bearer with secret", &UpstreamAuth{Scheme: UpstreamAuthSchemeBearer, Secret: "ciphertext"}, false},
		{"bearer without secret", &UpstreamAuth{Scheme: UpstreamAuthSchemeBearer}, true},
		{"bearer with username", &UpstreamAuth{Scheme: UpstreamAuthSchemeBearer, Username: "gateway", Secret: "ciphertext"}, true},
		{"none is credential-free", &UpstreamAuth{Scheme: UpstreamAuthSchemeNone}, false},
		{"none must not carry a secret", &UpstreamAuth{Scheme: UpstreamAuthSchemeNone, Secret: "ciphertext"}, true},
		{"none must not carry a username", &UpstreamAuth{Scheme: UpstreamAuthSchemeNone, Username: "gateway"}, true},
		{"empty scheme", &UpstreamAuth{}, true},
		{"unknown scheme", &UpstreamAuth{Scheme: "oauth", Secret: "ciphertext"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.auth.Validate()
			if tc.wantErr && err == nil {
				t.Fatal("expected an error")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

// TestMemoryStoreClonesUpstreamAuth pins that stored repositories never share
// mutable upstream credential state with caller-owned structs, and that the
// response-only marker is never persisted.
func TestMemoryStoreClonesUpstreamAuth(t *testing.T) {
	store := NewMemoryStore()
	auth := &UpstreamAuth{Scheme: UpstreamAuthSchemeBearer, Secret: "ciphertext", CredentialsConfigured: true}
	repo, err := store.CreateHostedRepository(t.Context(), HostedRepository{ID: "repo-1", Name: "go-proxy", Format: FormatGo, Type: RepositoryTypeProxy, UpstreamAuth: auth})
	if err != nil {
		t.Fatal(err)
	}
	if repo.UpstreamAuth == auth {
		t.Fatal("stored upstreamAuth aliases the caller struct")
	}
	if repo.UpstreamAuth.CredentialsConfigured {
		t.Fatal("response-only marker must not persist")
	}
	auth.Secret = "mutated"
	stored, err := store.GetHostedRepository(t.Context(), repo.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.UpstreamAuth.Secret != "ciphertext" {
		t.Fatalf("stored credential followed the caller mutation: %q", stored.UpstreamAuth.Secret)
	}
}
