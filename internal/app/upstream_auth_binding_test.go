package app

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ccsert/artifact-gateway/internal/objectstore"
	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

func TestPrivateProxyCredentialRebinding(t *testing.T) {
	privateProxyCredentialRebindingContract(t, repository.NewMemoryStore())
}

func privateProxyCredentialRebindingContract(t *testing.T, store GatewayStore) {
	t.Helper()
	for _, scheme := range []repository.UpstreamAuthScheme{repository.UpstreamAuthSchemeBasic, repository.UpstreamAuthSchemeBearer} {
		for _, tc := range []struct {
			name   string
			format repository.Format
			issuer bool
			patch  string
		}{
			{"go-endpoint", repository.FormatGo, false, `{"endpoint":"https://` + redirectCDN + `"}`},
			{"maven-endpoint", repository.FormatMaven, false, `{"endpoint":"https://` + redirectCDN + `"}`},
			{"oci-direct-endpoint", repository.FormatOCI, false, `{"endpoint":"https://` + redirectCDN + `"}`},
			{"oci-issuer-endpoint", repository.FormatOCI, true, `{"endpoint":"https://` + redirectCDN + `"}`},
			{"oci-enable", repository.FormatOCI, false, `{"ociBearer":{"realm":"https://` + redirectOrigin + `/token","service":"registry"}}`},
			{"oci-clear", repository.FormatOCI, true, `{"ociBearer":null}`},
			{"oci-realm-host", repository.FormatOCI, true, `{"ociBearer":{"realm":"https://` + redirectCDN + `/token","service":"registry"}}`},
			{"oci-realm-path", repository.FormatOCI, true, `{"ociBearer":{"realm":"https://` + redirectOrigin + `/other-token","service":"registry"}}`},
			{"oci-service", repository.FormatOCI, true, `{"ociBearer":{"realm":"https://` + redirectOrigin + `/token","service":"other-registry"}}`},
		} {
			t.Run(string(scheme)+"/"+tc.name, func(t *testing.T) {
				const sourceSecret = "synthetic-old-bound-source-secret"
				var reading atomic.Bool
				var ociAssets map[string]redirectCacheAsset
				asset := redirectCacheAsset{body: []byte("synthetic-complete-object"), mediaType: "application/octet-stream"}
				switch tc.format {
				case repository.FormatGo:
					asset = redirectCacheAsset{body: []byte("v1.0.0\n"), mediaType: "text/plain"}
				case repository.FormatOCI:
					ociAssets = redirectCacheOCIImage(t)
					asset = ociAssets["/v2/demo/manifests/latest"]
				}
				fixture := newProxyRedirectFixture(t, func(w http.ResponseWriter, r *http.Request) {
					if !reading.Load() {
						t.Error("management credential rebinding must not contact any source")
						w.WriteHeader(http.StatusInternalServerError)
						return
					}
					if r.Host != redirectOrigin || r.URL.Path == "/other-token" {
						t.Error("rejected rebind moved a data request to the new target")
						w.WriteHeader(http.StatusForbidden)
						return
					}
					authorization := privateProxyAuthorization(scheme, sourceSecret)
					if tc.issuer && r.URL.Path == "/token" {
						if r.Header.Get("Authorization") != authorization || r.URL.Query().Get("service") != "registry" {
							t.Error("rejected rebind changed the source credential or issuer audience")
						}
						_, _ = io.WriteString(w, `{"token":"synthetic-original-pull-token","expires_in":300}`)
						return
					}
					if tc.issuer {
						if r.Header.Get("Authorization") != "Bearer synthetic-original-pull-token" {
							if r.Header.Get("Authorization") != "" {
								t.Error("source credential reached the registry after rejected issuer change")
							}
							w.Header().Set("WWW-Authenticate", `Bearer realm="https://`+redirectOrigin+`/token",service="registry",scope="repository:demo:pull"`)
							w.WriteHeader(http.StatusUnauthorized)
							return
						}
					} else if r.Header.Get("Authorization") != authorization {
						t.Error("rejected rebind changed the direct source credential")
					}
					responseAsset := asset
					if tc.format == repository.FormatOCI {
						var exists bool
						responseAsset, exists = ociAssets[r.URL.Path]
						if !exists {
							http.NotFound(w, r)
							return
						}
						w.Header().Set("Docker-Content-Digest", responseAsset.digest)
					}
					w.Header().Set("Content-Type", responseAsset.mediaType)
					_, _ = w.Write(responseAsset.body)
				})
				input := repository.HostedRepository{ID: uuid.NewString(), Name: "bound-" + uuid.NewString(), Format: tc.format, Type: repository.RepositoryTypeProxy, Endpoint: "https://" + redirectOrigin, AllowedHosts: []string{redirectOrigin, redirectCDN}, UpstreamAuth: &repository.UpstreamAuth{Scheme: scheme, Secret: sealedUpstreamSecret(t, sourceSecret)}}
				if scheme == repository.UpstreamAuthSchemeBasic {
					input.UpstreamAuth.Username = "source-reader"
				}
				if tc.issuer {
					input.OCIBearer = &repository.OCIBearer{Realm: "https://" + redirectOrigin + "/token", Service: "registry"}
				}
				repo, err := store.CreateHostedRepository(t.Context(), input)
				if err != nil {
					t.Fatal(err)
				}
				objects := objectstore.NewMemoryStore()
				handler := newTestGateway(t, GatewayOptions{Dependencies: Dependencies{}, Store: store, Adapter: TestAdapter{}, Authenticator: privateProxyAuthenticator(), Storage: SharedObjectStores(objects), Upstream: fixture.client, Caches: GatewayCaches{Maven: NewDefaultMavenCache(objects, []string{redirectOrigin, redirectCDN}), OCI: NewOCICache(objects, time.Hour, time.Hour, time.Hour, []string{redirectOrigin, redirectCDN})}})
				for _, explicitPreserve := range []bool{false, true} {
					var body map[string]any
					if err := json.Unmarshal([]byte(tc.patch), &body); err != nil {
						t.Fatal(err)
					}
					if explicitPreserve {
						body["upstreamAuth"] = map[string]string{"scheme": string(scheme), "username": input.UpstreamAuth.Username}
					}
					data, _ := json.Marshal(body)
					rejected := ociBearerManagementRequest(handler, http.MethodPatch, "/api/v2/repositories/"+repo.ID, string(data), repo.Version, "")
					if rejected.Code != http.StatusBadRequest || !strings.Contains(rejected.Body.String(), `"code":"upstream_credential_rebind_required"`) || strings.Contains(rejected.Body.String(), sourceSecret) {
						t.Fatalf("rebind preserve=%v status=%d body=%s", explicitPreserve, rejected.Code, rejected.Body.String())
					}
					current, err := store.GetHostedRepository(t.Context(), repo.ID)
					if err != nil || current.Version != repo.Version || current.Endpoint != repo.Endpoint || current.UpstreamAuth == nil || current.UpstreamAuth.Secret != repo.UpstreamAuth.Secret {
						t.Fatalf("rejected rebind changed stored configuration/version: %v", err)
					}
					beforeBinding, _ := json.Marshal(repo.OCIBearer)
					afterBinding, _ := json.Marshal(current.OCIBearer)
					if string(beforeBinding) != string(afterBinding) {
						t.Fatal("rejected rebind changed issuer binding")
					}
				}
				if len(fixture.snapshot()) != 0 {
					t.Fatal("rejected management updates contacted a source")
				}
				// A cold public protocol read still uses only the original binding.
				path := "/maven/" + repo.Name + "/org/example/widget/1.0/widget-1.0.jar"
				switch tc.format {
				case repository.FormatGo:
					path = "/go/" + repo.Name + "/example.com/widget/@v/list"
				case repository.FormatOCI:
					path = "/v2/" + repo.Name + "/demo/manifests/latest"
				}
				reading.Store(true)
				request := httptest.NewRequest(http.MethodGet, path, nil)
				authorize(request, "admin-secret")
				request.Header.Set("Accept", asset.mediaType)
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, request)
				reading.Store(false)
				if response.Code != http.StatusOK || !bytes.Equal(response.Body.Bytes(), asset.body) || len(fixture.snapshot()) == 0 {
					t.Fatalf("cold read after rejected rebind=%d body=%s", response.Code, response.Body.String())
				}
				readCount := len(fixture.snapshot())
				// An unchanged destination still preserves the existing ciphertext.
				kept := ociBearerManagementRequest(handler, http.MethodPatch, "/api/v2/repositories/"+repo.ID, `{"anonymousRead":false}`, repo.Version, "")
				if kept.Code != http.StatusOK {
					t.Fatalf("same binding update=%d %s", kept.Code, kept.Body.String())
				}
				current, err := store.GetHostedRepository(t.Context(), repo.ID)
				if err != nil || current.UpstreamAuth.Secret != repo.UpstreamAuth.Secret {
					t.Fatal("same binding did not preserve ciphertext")
				}
				// Explicit replacement permits the binding change without reusing ciphertext.
				var body map[string]any
				_ = json.Unmarshal([]byte(tc.patch), &body)
				body["upstreamAuth"] = map[string]string{"scheme": string(scheme), "username": input.UpstreamAuth.Username, "secret": "synthetic-new-bound-source-secret"}
				data, _ := json.Marshal(body)
				replaced := ociBearerManagementRequest(handler, http.MethodPatch, "/api/v2/repositories/"+repo.ID, string(data), current.Version, "")
				if replaced.Code != http.StatusOK {
					t.Fatalf("explicit fresh rebind=%d %s", replaced.Code, replaced.Body.String())
				}
				next, err := store.GetHostedRepository(t.Context(), repo.ID)
				if err != nil || next.UpstreamAuth == nil || next.UpstreamAuth.Secret == repo.UpstreamAuth.Secret {
					t.Fatal("explicit rebind reused old ciphertext")
				}
				// Returning to the original destination is valid only after explicit clear.
				restore := map[string]any{"endpoint": repo.Endpoint, "ociBearer": repo.OCIBearer, "upstreamAuth": map[string]string{"scheme": "none"}}
				if tc.format != repository.FormatOCI {
					delete(restore, "ociBearer")
				}
				data, _ = json.Marshal(restore)
				cleared := ociBearerManagementRequest(handler, http.MethodPatch, "/api/v2/repositories/"+repo.ID, string(data), next.Version, "")
				if cleared.Code != http.StatusOK {
					t.Fatalf("explicit clear rebind=%d %s", cleared.Code, cleared.Body.String())
				}
				final, err := store.GetHostedRepository(t.Context(), repo.ID)
				if err != nil || final.UpstreamAuth != nil {
					t.Fatal("explicit clear retained a source credential")
				}
				if len(fixture.snapshot()) != readCount {
					t.Fatal("credential management contacted a new or old source")
				}
			})
		}
	}
}
