package upstreamauth_test

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/ccsert/artifact-gateway/internal/secrets"
	"github.com/ccsert/artifact-gateway/internal/upstreamauth"
)

func TestApplyOriginDefaultPortCredentials(t *testing.T) {
	t.Setenv(secrets.KeyEnv, "0123456789abcdef0123456789abcdef")
	ciphertext, err := secrets.Seal(upstreamauth.SecretPurpose, "synthetic-origin-secret")
	if err != nil {
		t.Fatal(err)
	}
	for _, scheme := range []repository.UpstreamAuthScheme{repository.UpstreamAuthSchemeBasic, repository.UpstreamAuthSchemeBearer} {
		for _, test := range []struct {
			name, endpoint, target string
			allowed                bool
		}{
			{"https-default-explicit", "https://source.example", "https://source.example:443/asset", true},
			{"https-default-implicit", "https://source.example:443", "https://source.example/asset", true},
			{"http-default-explicit", "http://source.example", "http://source.example:80/asset", true},
			{"http-default-implicit", "http://source.example:80", "http://source.example/asset", true},
			{"host-scheme-case", "HTTPS://SOURCE.EXAMPLE", "https://source.example:443/asset", true},
			{"leading-zero-port", "https://source.example:00443", "https://source.example/asset", true},
			{"leading-zero-target", "https://source.example", "https://source.example:0443/asset", true},
			{"same-nondefault-port", "https://source.example:8443", "https://source.example:8443/asset", true},
			{"ipv6-default-port", "https://[2001:db8::1]", "https://[2001:db8::1]:443/asset", true},
			{"different-port", "https://source.example", "https://source.example:8443/asset", false},
			{"default-port-is-scheme-specific", "https://source.example", "https://source.example:80/asset", false},
			{"different-host", "https://source.example", "https://cdn.example:443/asset", false},
			{"subdomain", "https://source.example", "https://sub.source.example/asset", false},
			{"different-scheme", "https://source.example:443", "http://source.example:443/asset", false},
			{"trailing-dot-is-not-an-alias", "https://source.example", "https://source.example./asset", false},
		} {
			t.Run(string(scheme)+"/"+test.name, func(t *testing.T) {
				request, err := http.NewRequest(http.MethodGet, test.target, nil)
				if err != nil {
					t.Fatal(err)
				}
				auth := &repository.UpstreamAuth{Scheme: scheme, Secret: ciphertext}
				if scheme == repository.UpstreamAuthSchemeBasic {
					auth.Username = "source-reader"
				}
				if err := upstreamauth.ApplyOrigin(request, test.endpoint, auth); err != nil {
					t.Fatal(err)
				}
				if !test.allowed {
					if request.Header.Get("Authorization") != "" {
						t.Fatal("repository credential reached a different origin")
					}
				} else if scheme == repository.UpstreamAuthSchemeBasic {
					username, password, ok := request.BasicAuth()
					if !ok || username != "source-reader" || password != "synthetic-origin-secret" {
						t.Fatal("same-origin request did not receive its Basic credential")
					}
				} else if request.Header.Get("Authorization") != "Bearer synthetic-origin-secret" {
					t.Fatal("same-origin request did not receive its Bearer credential")
				}
			})
		}
	}
}

func TestStripRedirectDefaultPortBoundary(t *testing.T) {
	for _, test := range []struct {
		name, endpoint, previous, next string
		keep                           bool
	}{
		{"default-explicit", "https://source.example", "https://source.example", "https://source.example:443/asset", true},
		{"default-implicit", "https://source.example:443", "https://source.example:443", "https://source.example/asset", true},
		{"different-port", "https://source.example", "https://source.example", "https://source.example:8443/asset", false},
		{"different-host", "https://source.example", "https://source.example", "https://cdn.example/asset", false},
		{"different-scheme", "https://source.example", "https://source.example", "http://source.example/asset", false},
		{"cdn-return-cannot-restore", "https://source.example", "https://cdn.example/asset", "https://source.example:443/asset", false},
		{"port-return-cannot-restore", "https://source.example:443", "https://source.example:8443/asset", "https://source.example/asset", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			previous, err := url.Parse(test.previous)
			if err != nil {
				t.Fatal(err)
			}
			next, err := http.NewRequest(http.MethodGet, test.next, nil)
			if err != nil {
				t.Fatal(err)
			}
			next.Header.Set("Authorization", "Bearer synthetic-origin-secret")
			next.Header.Set("Proxy-Authorization", "Basic synthetic-proxy-secret")
			upstreamauth.StripRedirect(next, []*http.Request{{URL: previous}}, test.endpoint)
			if (next.Header.Get("Authorization") != "") != test.keep || next.Header.Get("Proxy-Authorization") != "" {
				t.Fatal("redirect exceeded its origin credential scope")
			}
		})
	}
}
