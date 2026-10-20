package app

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ccsert/artifact-gateway/internal/cache"
	"github.com/ccsert/artifact-gateway/internal/objectstore"
	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

type privateProxyCircuitBackend struct {
	privateProxyBackend
	coordinator cache.Coordinator
	reopen      func() privateProxyCircuitBackend
}

func TestPrivateProxyCircuitDoesNotFallThrough(t *testing.T) {
	privateProxyCircuitContract(t, func(t *testing.T) privateProxyCircuitBackend {
		return privateProxyCircuitBackend{privateProxyBackend: privateProxyBackend{store: repository.NewMemoryStore(), objects: objectstore.NewMemoryStore(), cache: objectstore.NewMemoryStore()}}
	})
}

func privateProxyCircuitContract(t *testing.T, backend func(*testing.T) privateProxyCircuitBackend) {
	t.Helper()
	for _, format := range []repository.Format{repository.FormatMaven, repository.FormatOCI} {
		for _, issuer := range []bool{false, true} {
			if issuer && format != repository.FormatOCI {
				continue
			}
			for _, scheme := range []repository.UpstreamAuthScheme{repository.UpstreamAuthSchemeBasic, repository.UpstreamAuthSchemeBearer, repository.UpstreamAuthSchemeNone} {
				for _, cleared := range []bool{false, true} {
					if cleared && scheme != repository.UpstreamAuthSchemeNone {
						continue
					}
					for _, failure := range []string{"401", "403", "invalid-ciphertext"} {
						if scheme == repository.UpstreamAuthSchemeNone && failure == "invalid-ciphertext" {
							continue
						}
						t.Run(fmt.Sprintf("%s/issuer=%v/%s/cleared=%v/%s", format, issuer, scheme, cleared, failure), func(t *testing.T) {
							privateProxyCircuitCase(t, format, issuer, scheme, failure, cleared, backend(t))
						})
					}
				}
			}
		}
	}
}

func privateProxyCircuitCase(t *testing.T, format repository.Format, issuer bool, scheme repository.UpstreamAuthScheme, failure string, cleared bool, backend privateProxyCircuitBackend) {
	t.Helper()
	const sourceSecret = "synthetic-private-circuit-source-secret"
	const sourceError = "synthetic-private-circuit-source-error"
	var sourceVisits, fallbackVisits atomic.Int32
	asset := redirectCacheOCIImage(t)["/v2/demo/manifests/latest"]
	status := http.StatusUnauthorized
	if failure == "403" {
		status = http.StatusForbidden
	}
	fixture := newProxyRedirectFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Host == redirectCDN {
			fallbackVisits.Add(1)
			w.Header().Set("Content-Type", asset.mediaType)
			w.Header().Set("Docker-Content-Digest", asset.digest)
			_, _ = w.Write(asset.body)
			return
		}
		sourceVisits.Add(1)
		if issuer && r.URL.Path != "/token" {
			if r.Header.Get("Authorization") != "" {
				t.Error("issuer-bound source credential reached registry content")
			}
			w.Header().Set("WWW-Authenticate", `Bearer realm="https://`+redirectOrigin+`/token",service="private-registry",scope="repository:demo:pull"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		expected := ""
		if scheme != repository.UpstreamAuthSchemeNone {
			expected = privateProxyAuthorization(scheme, sourceSecret)
		}
		if r.Header.Get("Authorization") != expected {
			t.Error("source credential did not stay bound to the configured target")
		}
		http.Error(w, sourceError, status)
	})
	var members []repository.GroupMember
	var primary repository.HostedRepository
	for i, host := range []string{redirectOrigin, redirectCDN} {
		repo := repository.HostedRepository{ID: uuid.NewString(), Name: "circuit-source-" + uuid.NewString(), Format: format, Type: repository.RepositoryTypeProxy, Endpoint: "https://" + host, AllowedHosts: []string{host}}
		if i == 0 {
			if scheme != repository.UpstreamAuthSchemeNone {
				repo.UpstreamAuth = &repository.UpstreamAuth{Scheme: scheme, Secret: sealedUpstreamSecret(t, sourceSecret)}
				if scheme == repository.UpstreamAuthSchemeBasic {
					repo.UpstreamAuth.Username = "source-reader"
				}
				if failure == "invalid-ciphertext" {
					repo.UpstreamAuth.Secret = "synthetic-invalid-ciphertext"
				}
			}
			if issuer {
				repo.OCIBearer = &repository.OCIBearer{Realm: "https://" + redirectOrigin + "/token", Service: "private-registry"}
			}
			if cleared {
				repo.UpstreamAuth = &repository.UpstreamAuth{Scheme: repository.UpstreamAuthSchemeBearer, Secret: sealedUpstreamSecret(t, sourceSecret)}
			}
		}
		created, err := backend.store.CreateHostedRepository(t.Context(), repo)
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			primary = created
		}
		members = append(members, repository.GroupMember{RepositoryID: repo.ID, Position: i})
	}
	group, _, err := backend.store.CreateHostedGroupIdempotently(t.Context(), repository.HostedGroup{ID: uuid.NewString(), Name: "circuit-group-" + uuid.NewString(), Format: format, Members: members}, "fixture", uuid.NewString(), "fixture")
	if err != nil {
		t.Fatal(err)
	}
	newHandler := func() http.Handler {
		return newTestGateway(t, GatewayOptions{Dependencies: Dependencies{}, Store: backend.store, Adapter: TestAdapter{}, Authenticator: privateProxyAuthenticator(), Storage: ObjectStores{format: backend.objects}, Upstream: fixture.client,
			Caches: GatewayCaches{Maven: NewMavenCache(backend.cache, time.Hour, time.Hour, time.Hour, time.Hour, []string{redirectOrigin, redirectCDN}).WithCoordinator(backend.coordinator), OCI: NewOCICache(backend.cache, time.Hour, time.Hour, time.Hour, []string{redirectOrigin, redirectCDN}).WithCoordinator(backend.coordinator)}})
	}
	handler := newHandler()
	if cleared {
		response := ociBearerManagementRequest(handler, http.MethodPatch, "/api/v2/repositories/"+primary.ID, `{"upstreamAuth":{"scheme":"none"}}`, primary.Version, "")
		if response.Code != http.StatusOK {
			t.Fatalf("explicit credential clear=%d", response.Code)
		}
		stored, err := backend.store.GetHostedRepository(t.Context(), primary.ID)
		if err != nil || stored.UpstreamAuth != nil || sourceVisits.Load() != 0 {
			t.Fatal("explicit clear must remove the stored source credential without contacting it")
		}
	}
	path := "/maven/" + group.Name + "/org/example/widget/1.0/widget-1.0.pom"
	if format == repository.FormatOCI {
		path = "/v2/" + group.Name + "/demo/manifests/latest"
	}
	var openedAt int32
	// One recorded failure opens the current breaker. Continue beyond that
	// threshold, rebuilding the real PG owner after the first request when present.
	for attempt := range 4 {
		if attempt == 1 && backend.reopen != nil {
			backend = backend.reopen()
			handler = newHandler()
		}
		request := httptest.NewRequest(http.MethodGet, path, nil)
		authorize(request, "admin-secret")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusBadGateway || fallbackVisits.Load() != 0 {
			t.Fatalf("authentication bypass attempt=%d status=%d anonymousRequests=%d", attempt+1, response.Code, fallbackVisits.Load())
		}
		for _, secret := range []string{sourceSecret, sourceError, "synthetic-invalid-ciphertext"} {
			if strings.Contains(response.Body.String(), secret) {
				t.Fatal("source credential or private error leaked to a protocol response")
			}
		}
		if attempt == 0 {
			openedAt = sourceVisits.Load()
		} else if sourceVisits.Load() != openedAt {
			t.Fatal("open circuit was not retained across requests or owner reconstruction")
		}
	}
	audits, err := backend.store.ListAudits(t.Context(), repository.AuditQuery{GroupName: group.Name, Limit: 100})
	if err != nil || len(audits) < 4 {
		t.Fatalf("blocked circuit requests must retain audit evidence: records=%d err=%v", len(audits), err)
	}
}
