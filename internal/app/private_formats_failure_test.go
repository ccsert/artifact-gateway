package app

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ccsert/artifact-gateway/internal/objectstore"
	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

func TestPrivateFormatSourceFailureDoesNotFallThrough(t *testing.T) {
	t.Setenv("GATEWAY_SETTINGS_ENCRYPTION_KEY", upstreamAuthTestKey)
	for _, format := range []repository.Format{repository.FormatNPM, repository.FormatPyPI, repository.FormatCargo, repository.FormatAPT, repository.FormatRaw, repository.FormatConan} {
		t.Run(string(format), func(t *testing.T) {
			var fallbackCalls atomic.Int32
			fixture := newProxyRedirectFixture(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Host == redirectCDN {
					fallbackCalls.Add(1)
					_, _ = w.Write([]byte(`{}`))
					return
				}
				http.Error(w, "synthetic-protected-source-error", http.StatusServiceUnavailable)
			})
			store := repository.NewMemoryStore()
			private, err := store.CreateHostedRepository(t.Context(), repository.HostedRepository{ID: uuid.NewString(), Name: "private-" + uuid.NewString(), Format: format, Type: repository.RepositoryTypeProxy, Endpoint: "https://" + redirectOrigin, AllowedHosts: []string{redirectOrigin}, UpstreamAuth: &repository.UpstreamAuth{Scheme: repository.UpstreamAuthSchemeBearer, Secret: sealedUpstreamSecret(t, "protected-source-secret")}})
			if err != nil {
				t.Fatal(err)
			}
			public, err := store.CreateHostedRepository(t.Context(), repository.HostedRepository{ID: uuid.NewString(), Name: "public-" + uuid.NewString(), Format: format, Type: repository.RepositoryTypeProxy, Endpoint: "https://" + redirectCDN, AllowedHosts: []string{redirectCDN}})
			if err != nil {
				t.Fatal(err)
			}
			group, _, err := store.CreateHostedGroupIdempotently(t.Context(), repository.HostedGroup{ID: uuid.NewString(), Name: "failure-" + uuid.NewString(), Format: format, Members: []repository.GroupMember{{RepositoryID: private.ID, Position: 0}, {RepositoryID: public.ID, Position: 1}}}, "fixture", uuid.NewString(), "fixture")
			if err != nil {
				t.Fatal(err)
			}
			objects := objectstore.NewMemoryStore()
			handler := newTestGateway(t, GatewayOptions{Store: store, Adapter: TestAdapter{}, Authenticator: testAuthenticator(), Storage: SharedObjectStores(objects), Upstream: fixture.client, Dependencies: Dependencies{CargoUpstreamClient: fixture.client.HTTPClient}, Caches: GatewayCaches{Raw: NewDefaultRawCache(objects, []string{redirectOrigin, redirectCDN}), Conan: NewDefaultConanCache(objects, []string{redirectOrigin, redirectCDN})}})
			path := map[repository.Format]string{repository.FormatNPM: "/private-demo", repository.FormatPyPI: "/simple/private-demo/", repository.FormatCargo: "/de/mo/demo", repository.FormatAPT: "/dists/stable/Release", repository.FormatRaw: "/private.bin", repository.FormatConan: "/v2/conans/pkg/1.0/user/stable/revisions"}[format]
			for i := 0; i < 4; i++ {
				r := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/%s/%s%s", format, group.Name, path), nil)
				authorize(r, "admin-secret")
				w := httptest.NewRecorder()
				handler.ServeHTTP(w, r)
				if w.Code < 500 || fallbackCalls.Load() != 0 || strings.Contains(w.Body.String(), "synthetic-protected-source-error") {
					t.Fatalf("private %s source failure status=%d fallback=%d", format, w.Code, fallbackCalls.Load())
				}
			}
		})
	}
}
