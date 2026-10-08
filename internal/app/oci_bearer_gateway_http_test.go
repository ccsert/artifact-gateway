package app

import (
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

func TestOCIBearerNativeAndGroupColdHot(t *testing.T) {
	for _, route := range []string{"proxy", "group"} {
		t.Run(route, func(t *testing.T) {
			asset := redirectCacheOCIImage(t)["/v2/demo/manifests/latest"]
			var exchanges atomic.Int32
			upstream := newProxyRedirectFixture(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/token" {
					exchanges.Add(1)
					if r.URL.Query().Get("service") != "synthetic-registry" || r.URL.Query().Get("scope") != "repository:demo:pull" || r.Header.Get("Authorization") != "" {
						t.Error("token request is outside the synthetic pull binding")
					}
					_, _ = io.WriteString(w, `{"token":"synthetic-gateway-token","expires_in":60}`)
					return
				}
				if r.Header.Get("Authorization") != "Bearer synthetic-gateway-token" {
					w.Header().Set("WWW-Authenticate", `Bearer realm="https://`+redirectOrigin+`/token",service="synthetic-registry",scope="repository:demo:pull"`)
					w.WriteHeader(http.StatusUnauthorized)
					return
				}
				w.Header().Set("Content-Type", asset.mediaType)
				w.Header().Set("Docker-Content-Digest", asset.digest)
				_, _ = w.Write(asset.body)
			})
			store := repository.NewMemoryStore()
			enableAnonymousAccess(t, store)
			proxy, err := store.CreateHostedRepository(t.Context(), repository.HostedRepository{
				ID: "synthetic-bearer-proxy", Name: "synthetic-bearer-proxy", Format: repository.FormatOCI,
				Type: repository.RepositoryTypeProxy, Endpoint: "https://" + redirectOrigin,
				AllowedHosts: []string{redirectOrigin}, AnonymousRead: true,
				OCIBearer: &repository.OCIBearer{Realm: "https://" + redirectOrigin + "/token", Service: "synthetic-registry"},
			})
			if err != nil {
				t.Fatal(err)
			}
			name := proxy.Name
			if route == "group" {
				group, _, err := store.CreateHostedGroupIdempotently(t.Context(), repository.HostedGroup{
					ID: "synthetic-bearer-group", Name: "synthetic-bearer-group", Format: repository.FormatOCI, AnonymousRead: true,
					Members: []repository.GroupMember{{RepositoryID: proxy.ID}},
				}, "synthetic-fixture", "synthetic-bearer-group", "synthetic-fixture")
				if err != nil {
					t.Fatal(err)
				}
				name = group.Name
			}
			objects := NewMemoryOCIObjectStore()
			handler := NewGatewayHandlerWithOCICache(Dependencies{NativeOCIObjectStore: objects}, store, TestAdapter{}, testAuthenticator(),
				NewOCICache(objects, time.Hour, time.Hour, time.Hour, []string{redirectOrigin}), upstream.client)
			for _, round := range []string{"cold", "hot"} {
				request := httptest.NewRequest(http.MethodGet, "/v2/"+name+"/demo/manifests/latest", nil)
				request.Header.Set("Accept", asset.mediaType)
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, request)
				if response.Code != http.StatusOK || response.Body.String() != string(asset.body) || response.Header().Get("Docker-Content-Digest") != asset.digest {
					t.Fatalf("%s %s: status=%d body=%q", route, round, response.Code, response.Body.String())
				}
				if exchanges.Load() != 1 || len(upstream.snapshot()) != 3 {
					t.Fatalf("%s %s: exchanges=%d upstream=%d; want one exchange and three cold-only requests", route, round, exchanges.Load(), len(upstream.snapshot()))
				}
			}
		})
	}
}
