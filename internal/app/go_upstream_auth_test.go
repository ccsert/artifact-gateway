package app

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/artifact-gateway/artifact-gateway/internal/secrets"
)

func sealedUpstreamSecret(t *testing.T, value string) string {
	t.Helper()
	t.Setenv("GATEWAY_SETTINGS_ENCRYPTION_KEY", upstreamAuthTestKey)
	sealed, err := secrets.Seal(upstreamAuthSecretPurpose, value)
	if err != nil {
		t.Fatal(err)
	}
	return sealed
}

func goProxyRepo(endpoint string, auth *repository.UpstreamAuth) repository.HostedRepository {
	return repository.HostedRepository{
		ID: "go-proxy", Name: "go-proxy", Format: repository.FormatGo,
		Type: repository.RepositoryTypeProxy, Endpoint: endpoint, UpstreamAuth: auth,
	}
}

func TestFetchGoInjectsUpstreamCredentials(t *testing.T) {
	t.Setenv("GATEWAY_SETTINGS_ENCRYPTION_KEY", upstreamAuthTestKey)
	cases := []struct {
		name string
		auth *repository.UpstreamAuth
		want string
	}{
		{
			name: "anonymous sends no Authorization header",
			auth: nil,
			want: "",
		},
		{
			name: "bearer sends the sealed token",
			auth: &repository.UpstreamAuth{Scheme: repository.UpstreamAuthSchemeBearer, Secret: sealedUpstreamSecret(t, "ghp_token")},
			want: "Bearer ghp_token",
		},
		{
			name: "basic sends a base64 credential",
			auth: &repository.UpstreamAuth{Scheme: repository.UpstreamAuthSchemeBasic, Username: "gateway", Secret: sealedUpstreamSecret(t, "s3cret")},
			want: "Basic " + base64.StdEncoding.EncodeToString([]byte("gateway:s3cret")),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var seen string
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				seen = r.Header.Get("Authorization")
				_, _ = w.Write([]byte("ok"))
			}))
			defer upstream.Close()

			client := UpstreamClient{HTTPClient: upstream.Client()}
			response, err := client.FetchGo(context.Background(), http.MethodGet, goProxyRepo(upstream.URL, tc.auth), upstream.URL+"/example.com/mod/@v/list", nil)
			if err != nil {
				t.Fatalf("fetch: %v", err)
			}
			defer func() { _ = response.Body.Close() }()
			if seen != tc.want {
				t.Fatalf("Authorization=%q want %q", seen, tc.want)
			}
		})
	}
}

// TestFetchGoStripsCredentialsOnCrossHostRedirect pins that a redirect to
// another host — even an allow-listed one — never receives the credential.
func TestFetchGoStripsCredentialsOnCrossHostRedirect(t *testing.T) {
	t.Setenv("GATEWAY_SETTINGS_ENCRYPTION_KEY", upstreamAuthTestKey)
	var leaked string
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		leaked = r.Header.Get("Authorization")
		_, _ = w.Write([]byte("ok"))
	}))
	defer other.Close()

	var origin string
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, origin, http.StatusFound)
	}))
	defer primary.Close()
	origin = other.URL + "/moved"

	repo := goProxyRepo(primary.URL, &repository.UpstreamAuth{Scheme: repository.UpstreamAuthSchemeBearer, Secret: sealedUpstreamSecret(t, "ghp_token")})
	// Both hosts must be allow-listed so the redirect is permitted by the
	// existing URL guard; the credential must still be dropped.
	repo.AllowedHosts = []string{strings.TrimPrefix(other.URL, "http://")}

	client := UpstreamClient{HTTPClient: primary.Client()}
	response, err := client.FetchGo(context.Background(), http.MethodGet, repo, primary.URL+"/example.com/mod/@v/list", nil)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	defer func() { _ = response.Body.Close() }()
	if leaked != "" {
		t.Fatalf("credential followed a cross-host redirect: %q", leaked)
	}
}

// TestFetchGoFailsWhenCredentialCannotBeDecrypted pins that a key mismatch is
// a hard failure. Silently falling back to an anonymous request would turn a
// key-rotation mistake into unexplained upstream 401s.
func TestFetchGoFailsWhenCredentialCannotBeDecrypted(t *testing.T) {
	t.Setenv("GATEWAY_SETTINGS_ENCRYPTION_KEY", upstreamAuthTestKey)
	reached := false
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		_, _ = w.Write([]byte("ok"))
	}))
	defer upstream.Close()

	repo := goProxyRepo(upstream.URL, &repository.UpstreamAuth{Scheme: repository.UpstreamAuthSchemeBearer, Secret: "not-a-valid-ciphertext"})
	client := UpstreamClient{HTTPClient: upstream.Client()}
	if _, err := client.FetchGo(context.Background(), http.MethodGet, repo, upstream.URL+"/example.com/mod/@v/list", nil); err == nil {
		t.Fatal("expected an error for an undecryptable credential")
	}
	if reached {
		t.Fatal("the upstream must not be contacted with an unusable credential")
	}
}
