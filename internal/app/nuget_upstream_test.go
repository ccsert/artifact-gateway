package app

import (
	"context"
	"encoding/base64"
	"net/http"
	"strings"
	"testing"

	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/ccsert/artifact-gateway/internal/secrets"
	"github.com/ccsert/artifact-gateway/internal/upstreamauth"
)

func TestNuGetUpstreamPerHopCredentialAndEgressIsolation(t *testing.T) {
	t.Setenv(secrets.KeyEnv, upstreamAuthTestKey)
	for _, scheme := range []repository.UpstreamAuthScheme{repository.UpstreamAuthSchemeBasic, repository.UpstreamAuthSchemeBearer} {
		t.Run(string(scheme), func(t *testing.T) {
			fixture := newProxyRedirectFixture(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/start":
					w.Header().Set("Location", "https://"+redirectCDN+"/cdn")
					w.WriteHeader(302)
				case "/cdn":
					w.Header().Set("Location", "https://"+redirectOrigin+"/returned")
					w.WriteHeader(307)
				case "/forbidden":
					w.Header().Set("Location", "https://"+redirectDenied+"/never")
					w.WriteHeader(302)
				default:
					w.WriteHeader(200)
				}
			})
			ciphertext, err := secrets.Seal(upstreamauth.SecretPurpose, "nuget-source-private")
			if err != nil {
				t.Fatal(err)
			}
			auth := &repository.UpstreamAuth{Scheme: scheme, Secret: ciphertext}
			expected := "Bearer nuget-source-private"
			if scheme == repository.UpstreamAuthSchemeBasic {
				auth.Username = "upstream-only"
				expected = "Basic " + base64.StdEncoding.EncodeToString([]byte("upstream-only:nuget-source-private"))
			}
			repo := repository.HostedRepository{ID: "nuget-proxy", Version: "1", Endpoint: "https://" + redirectOrigin + "/index.json", AllowedHosts: []string{redirectCDN}, UpstreamAuth: auth, EgressProxy: &repository.EgressProxy{Mode: repository.EgressProxyModeDirect}}
			response, err := fixture.client.FetchNuGet(context.Background(), repo, "https://"+redirectOrigin+"/start", http.Header{"Authorization": []string{"Bearer local-must-not-cross"}, "Cookie": []string{"local-secret"}, "Proxy-Authorization": []string{"Basic proxy-must-not-cross"}, "If-None-Match": []string{`"etag"`}})
			if err != nil {
				t.Fatal(err)
			}
			_ = response.Body.Close()
			fixture.mu.Lock()
			requests := append([]proxyRedirectRequest(nil), fixture.requests...)
			fixture.mu.Unlock()
			if len(requests) != 3 {
				t.Fatalf("hops=%d", len(requests))
			}
			for i, request := range requests {
				want := ""
				if i == 0 {
					want = expected
				}
				if request.headers.Get("Authorization") != want || request.headers.Get("Cookie") != "" || request.headers.Get("Proxy-Authorization") != "" {
					t.Fatalf("credential crossed hop %d", i)
				}
				if request.headers.Get("If-None-Match") != `"etag"` {
					t.Fatal("conditional lost")
				}
			}
			if response, err = fixture.client.FetchNuGet(context.Background(), repo, "https://"+redirectOrigin+"/forbidden", nil); err == nil {
				if response != nil {
					_ = response.Body.Close()
				}
				t.Fatal("unapproved redirect accepted")
			}
			fixture.mu.Lock()
			defer fixture.mu.Unlock()
			for _, request := range fixture.requests {
				if strings.Contains(request.host, redirectDenied) {
					t.Fatal("unapproved origin received a request")
				}
			}
		})
	}
}
