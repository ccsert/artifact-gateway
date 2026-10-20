package app

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/ccsert/artifact-gateway/internal/repository"
)

func privateFormatFetch(t *testing.T, fixture *proxyRedirectFixture, format repository.Format, target string, auth *repository.UpstreamAuth) (*http.Response, error) {
	t.Helper()
	repo := repository.HostedRepository{Endpoint: "https://" + redirectOrigin, AllowedHosts: []string{redirectOrigin, redirectOrigin + ":8443", redirectCDN}, UpstreamAuth: auth}
	headers := http.Header{"Authorization": {"Bearer gateway-client-secret"}, "Cookie": {"client-cookie"}, "Proxy-Authorization": {"Basic client-proxy-secret"}}
	switch format {
	case repository.FormatNPM:
		return fixture.client.FetchNPM(t.Context(), http.MethodGet, repo, target, headers)
	case repository.FormatPyPI:
		return fixture.client.FetchPyPI(t.Context(), http.MethodGet, repo, target, headers)
	case repository.FormatCargo:
		return fixture.client.FetchCargo(t.Context(), repo, target, headers)
	case repository.FormatAPT:
		return fixture.client.FetchAPT(t.Context(), http.MethodGet, repo, target, headers)
	case repository.FormatRaw:
		return fixture.client.FetchRaw(t.Context(), http.MethodGet, repository.Member{Type: repository.MemberProxy, Endpoint: repo.Endpoint, AllowedHosts: repo.AllowedHosts, UpstreamAuth: auth}, "initial", headers)
	case repository.FormatConan:
		return fixture.client.FetchConan(t.Context(), http.MethodGet, repository.Member{Type: repository.MemberProxy, Endpoint: repo.Endpoint, AllowedHosts: repo.AllowedHosts, UpstreamAuth: auth}, "initial", headers)
	default:
		t.Fatalf("unsupported transport fixture %s", format)
		return nil, nil
	}
}

func TestPrivateFormatRedirectCredentialBoundary(t *testing.T) {
	t.Setenv("GATEWAY_SETTINGS_ENCRYPTION_KEY", upstreamAuthTestKey)
	for _, format := range []repository.Format{repository.FormatNPM, repository.FormatPyPI, repository.FormatCargo, repository.FormatAPT} {
		for _, target := range []string{"same-origin", "cdn-return", "cdn-initial", "different-port"} {
			t.Run(string(format)+"/"+target, func(t *testing.T) {
				fixture := newProxyRedirectFixture(t, func(w http.ResponseWriter, r *http.Request) {
					switch r.URL.Path {
					case "/initial":
						host := redirectOrigin
						if target == "cdn-return" {
							host = redirectCDN
						}
						http.Redirect(w, r, "https://"+host+"/hop", http.StatusTemporaryRedirect)
					case "/hop":
						http.Redirect(w, r, "https://"+redirectOrigin+"/complete", http.StatusTemporaryRedirect)
					default:
						_, _ = io.WriteString(w, "complete-private-bytes")
					}
				})
				initial := "https://" + redirectOrigin + "/initial"
				switch target {
				case "cdn-initial":
					initial = "https://" + redirectCDN + "/hop"
				case "different-port":
					initial = "https://" + redirectOrigin + ":8443/complete"
				}
				auth := &repository.UpstreamAuth{Scheme: repository.UpstreamAuthSchemeBearer, Secret: sealedUpstreamSecret(t, "format-source-secret")}
				response, err := privateFormatFetch(t, fixture, format, initial, auth)
				if err != nil {
					t.Fatal(err)
				}
				defer closeProxyRedirectResponse(response)
				body, err := io.ReadAll(response.Body)
				if err != nil || string(body) != "complete-private-bytes" {
					t.Fatal("redirect changed complete bytes")
				}
				requests := fixture.snapshot()
				for i, request := range requests {
					want := ""
					if target == "same-origin" || target == "cdn-return" && i == 0 {
						want = "Bearer format-source-secret"
					}
					if request.headers.Get("Authorization") != want || request.headers.Get("Proxy-Authorization") != "" || request.headers.Get("Cookie") != "" {
						t.Fatalf("hop %d exceeded origin-bound credential scope", i)
					}
				}
			})
		}
	}
}

func TestPrivateRawConanRejectRedirectWithoutCredentialLeak(t *testing.T) {
	t.Setenv("GATEWAY_SETTINGS_ENCRYPTION_KEY", upstreamAuthTestKey)
	for _, format := range []repository.Format{repository.FormatRaw, repository.FormatConan} {
		t.Run(string(format), func(t *testing.T) {
			fixture := newProxyRedirectFixture(t, func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, "https://"+redirectCDN+"/unreachable", http.StatusTemporaryRedirect)
			})
			auth := &repository.UpstreamAuth{Scheme: repository.UpstreamAuthSchemeBearer, Secret: sealedUpstreamSecret(t, "format-source-secret")}
			response, err := privateFormatFetch(t, fixture, format, "https://"+redirectOrigin+"/initial", auth)
			defer closeProxyRedirectResponse(response)
			if err != nil && strings.Contains(err.Error(), "format-source-secret") {
				t.Fatal("credential leaked in redirect error")
			}
			requests := fixture.snapshot()
			if len(requests) != 1 || requests[0].host != redirectOrigin || requests[0].headers.Get("Authorization") != "Bearer format-source-secret" {
				t.Fatal("strict redirect policy contacted another origin")
			}
		})
	}
}
