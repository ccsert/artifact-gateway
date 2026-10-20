package app

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
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

type privateProxyCase struct {
	format repository.Format
	scheme repository.UpstreamAuthScheme
	route  string
	issuer bool
}

type privateProxyBackend struct {
	store   GatewayStore
	objects objectstore.Store
	cache   objectstore.Store
	reload  func() (GatewayStore, objectstore.Store)
	assets  map[string]redirectCacheAsset
	client  func(*testing.T, http.Handler, string, string, map[string]redirectCacheAsset)
}

func privateProxyAuthenticator(names ...string) Authenticator {
	return Authenticator{AdminToken: "admin-secret", AdminActor: "private-proxy-admin", ResolverToken: "private-client-secret", ResolverActor: "private-client", RepositoryReaders: map[string][]string{"private-client": names}}
}

func privateProxyAuthorization(scheme repository.UpstreamAuthScheme, secret string) string {
	if scheme == repository.UpstreamAuthSchemeBasic {
		return "Basic " + base64.StdEncoding.EncodeToString([]byte("source-reader:"+secret))
	}
	return "Bearer " + secret
}

func TestPrivateProxyUpstreamCredentials(t *testing.T) {
	for _, format := range []repository.Format{repository.FormatMaven, repository.FormatOCI} {
		for _, scheme := range []repository.UpstreamAuthScheme{repository.UpstreamAuthSchemeBasic, repository.UpstreamAuthSchemeBearer} {
			for _, route := range []string{"proxy", "group"} {
				t.Run(string(format)+"/"+string(scheme)+"/"+route, func(t *testing.T) {
					privateProxyCredentialsContract(t, privateProxyCase{format, scheme, route, false}, privateProxyBackend{store: repository.NewMemoryStore(), objects: objectstore.NewMemoryStore(), cache: objectstore.NewMemoryStore()})
				})
			}
		}
	}
}

func TestPrivateOCIIssuerCredentials(t *testing.T) {
	for _, scheme := range []repository.UpstreamAuthScheme{repository.UpstreamAuthSchemeBasic, repository.UpstreamAuthSchemeBearer} {
		for _, route := range []string{"proxy", "group"} {
			t.Run(string(scheme)+"/"+route, func(t *testing.T) {
				privateProxyCredentialsContract(t, privateProxyCase{repository.FormatOCI, scheme, route, true}, privateProxyBackend{store: repository.NewMemoryStore(), objects: objectstore.NewMemoryStore(), cache: objectstore.NewMemoryStore()})
			})
		}
	}
}

// The same native HTTP contract is reused by the isolated PostgreSQL/RustFS
// gate. reload replaces both the repository store and Gateway after a cold
// read, so credentials and cache metadata must survive store reconstruction.
func privateProxyCredentialsContract(t *testing.T, tc privateProxyCase, backend privateProxyBackend) {
	t.Helper()
	format, scheme, route, issuer := tc.format, tc.scheme, tc.route, tc.issuer
	store, objects, cacheObjects := backend.store, backend.objects, backend.cache
	t.Setenv("GATEWAY_SETTINGS_ENCRYPTION_KEY", upstreamAuthTestKey)
	const firstSecret = "synthetic-private-source-v1"
	const secondSecret = "synthetic-private-source-v2"
	var sourceAuth atomic.Value
	sourceAuth.Store(privateProxyAuthorization(scheme, firstSecret))
	var tokenNumber atomic.Int32
	assets := map[string]redirectCacheAsset{
		"/org/example/widget/1.0/widget-1.0.pom": {body: []byte("<project><modelVersion>4.0.0</modelVersion><groupId>org.example</groupId><artifactId>widget</artifactId><version>1.0</version></project>"), mediaType: "application/xml"},
	}
	if format == repository.FormatOCI {
		assets = redirectCacheOCIImage(t)
	}
	if backend.assets != nil {
		assets = backend.assets
	}
	extraSourcePath := "/org/example/widget/1.0/widget-1.0-extra.pom"
	extraAsset := assets["/org/example/widget/1.0/widget-1.0.pom"]
	if format == repository.FormatOCI {
		extraSourcePath = "/v2/demo/manifests/appeared"
		extraAsset = assets["/v2/demo/manifests/latest"]
	}
	var extraAvailable atomic.Bool
	upstream := newProxyRedirectFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if issuer && r.Host == redirectCDN && r.URL.Path == "/token" {
			if r.Header.Get("Authorization") != sourceAuth.Load().(string) {
				http.Error(w, firstSecret, http.StatusForbidden)
				return
			}
			if r.URL.Query().Get("service") != "private-registry" || r.URL.Query().Get("scope") != "repository:demo:pull" || len(r.URL.Query()) != 2 {
				t.Error("private issuer request escaped its declared audience/pull scope")
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			number := tokenNumber.Add(1)
			_, _ = fmt.Fprintf(w, `{"token":"synthetic-issued-%d","expires_in":300}`, number)
			return
		}
		expected := sourceAuth.Load().(string)
		if issuer {
			expected = fmt.Sprintf("Bearer synthetic-issued-%d", tokenNumber.Load())
		}
		if r.Header.Get("Authorization") != expected || (issuer && tokenNumber.Load() == 0) {
			if issuer {
				if strings.Contains(r.Header.Get("Authorization"), firstSecret) || strings.Contains(r.Header.Get("Authorization"), secondSecret) || strings.HasPrefix(r.Header.Get("Authorization"), "Basic ") {
					t.Error("source credential reached the registry instead of the bound issuer")
				}
				w.Header().Set("WWW-Authenticate", `Bearer realm="https://`+redirectCDN+`/token",service="private-registry",scope="repository:demo:pull"`)
			}
			http.Error(w, firstSecret, http.StatusUnauthorized)
			return
		}
		asset, exists := assets[r.URL.Path]
		if r.URL.Path == extraSourcePath && extraAvailable.Load() {
			asset, exists = extraAsset, true
		}
		if !exists {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", asset.mediaType)
		if format == repository.FormatOCI {
			w.Header().Set("Docker-Content-Digest", asset.digest)
		}
		_, _ = w.Write(asset.body)
	})
	upstream.client.OCIBearerCache = NewOCIBearerTokenCache()
	proxy, err := store.CreateHostedRepository(t.Context(), repository.HostedRepository{
		ID: uuid.NewString(), Name: "private-" + uuid.NewString(), Format: format, Type: repository.RepositoryTypeProxy,
		Endpoint: "https://" + redirectOrigin, AllowedHosts: []string{redirectOrigin, redirectCDN},
		UpstreamAuth: &repository.UpstreamAuth{Scheme: scheme, Username: map[bool]string{true: "source-reader"}[scheme == repository.UpstreamAuthSchemeBasic], Secret: sealedUpstreamSecret(t, firstSecret)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if issuer {
		proxy.OCIBearer = &repository.OCIBearer{Realm: "https://" + redirectCDN + "/token", Service: "private-registry"}
		proxy, err = store.UpdateHostedRepository(t.Context(), proxy, proxy.Version)
		if err != nil {
			t.Fatal(err)
		}
	}
	name := proxy.Name
	if route == "group" {
		group, _, err := store.CreateHostedGroupIdempotently(t.Context(), repository.HostedGroup{
			ID: uuid.NewString(), Name: "private-group-" + uuid.NewString(), Format: format,
			Members: []repository.GroupMember{{RepositoryID: proxy.ID}},
		}, "synthetic-fixture", uuid.NewString(), "synthetic-fixture")
		if err != nil {
			t.Fatal(err)
		}
		name = group.Name
	}
	newHandler := func() http.Handler {
		return newTestGateway(t, GatewayOptions{Dependencies: Dependencies{}, Store: store, Adapter: TestAdapter{}, Authenticator: privateProxyAuthenticator(name),
			Storage: ObjectStores{format: objects}, Upstream: upstream.client,
			Caches: GatewayCaches{Maven: NewDefaultMavenCache(cacheObjects, []string{redirectOrigin, redirectCDN}), OCI: NewOCICache(cacheObjects, time.Hour, time.Hour, time.Hour, []string{redirectOrigin, redirectCDN})}})
	}
	handler := newHandler()
	deniedRequest := httptest.NewRequest(http.MethodPatch, "/api/v2/repositories/"+proxy.ID, strings.NewReader(`{"upstreamAuth":{"scheme":"bearer","secret":"synthetic-denied-secret"}}`))
	authorize(deniedRequest, "private-client-secret")
	deniedRequest.Header.Set("If-Match", proxy.Version)
	deniedResponse := httptest.NewRecorder()
	handler.ServeHTTP(deniedResponse, deniedRequest)
	if deniedResponse.Code != http.StatusForbidden {
		t.Fatalf("non-administrator source credential update=%d", deniedResponse.Code)
	}
	path := "/maven/" + name + "/org/example/widget/1.0/widget-1.0.pom"
	asset := assets["/org/example/widget/1.0/widget-1.0.pom"]
	if format == repository.FormatOCI {
		path = "/v2/" + name + "/demo/manifests/latest"
		asset = assets["/v2/demo/manifests/latest"]
	}
	read := func(status int) {
		t.Helper()
		request := httptest.NewRequest(http.MethodGet, path, nil)
		authorize(request, "admin-secret")
		request.Header.Set("Accept", asset.mediaType)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != status {
			t.Fatalf("private %s %s issuer=%v: status=%d want=%d body=%s", format, route, issuer, response.Code, status, response.Body.String())
		}
		if status == http.StatusOK && (!bytes.Equal(response.Body.Bytes(), asset.body) || redirectCacheSHA256(response.Body.Bytes()) != redirectCacheSHA256(asset.body)) {
			t.Fatal("private upstream object was not read back completely")
		}
		if strings.Contains(response.Body.String(), firstSecret) || strings.Contains(response.Body.String(), secondSecret) {
			t.Fatal("native response leaked a source credential or upstream error body")
		}
	}
	if backend.client != nil {
		backend.client(t, handler, name, "cold", assets)
	}
	read(http.StatusOK)
	primaryPath, primaryAsset := path, asset
	readClosure := func() {
		t.Helper()
		for sourcePath, expected := range assets {
			if format == repository.FormatOCI {
				path = "/v2/" + name + "/" + strings.TrimPrefix(sourcePath, "/v2/")
			} else {
				path = "/maven/" + name + sourcePath
			}
			asset = expected
			read(http.StatusOK)
		}
		path, asset = primaryPath, primaryAsset
	}
	readClosure()
	extraPath := "/maven/" + name + extraSourcePath
	if format == repository.FormatOCI {
		extraPath = "/v2/" + name + "/demo/manifests/appeared"
	}
	path = extraPath
	read(http.StatusNotFound)
	negativeCold := len(upstream.snapshot())
	read(http.StatusNotFound)
	if len(upstream.snapshot()) != negativeCold {
		t.Fatal("negative cache did not retain the authenticated source identity")
	}
	path = primaryPath
	cold := len(upstream.snapshot())
	if cold == 0 {
		t.Fatal("cold read did not reach the protected source")
	}
	if backend.reload != nil {
		store, cacheObjects = backend.reload()
		upstream.client.OCIBearerCache = NewOCIBearerTokenCache()
		handler = newHandler()
	}
	if backend.client != nil {
		backend.client(t, handler, name, "hot", assets)
	}
	readClosure()
	path = extraPath
	read(http.StatusNotFound)
	path = primaryPath
	if len(upstream.snapshot()) != cold {
		t.Fatal("hot cache unexpectedly fetched the protected source")
	}
	patch := func(secret, version string) *httptest.ResponseRecorder {
		t.Helper()
		auth := map[string]string{"scheme": string(scheme), "secret": secret}
		if scheme == repository.UpstreamAuthSchemeBasic {
			auth["username"] = "source-reader"
		}
		body, _ := json.Marshal(map[string]any{"upstreamAuth": auth})
		return ociBearerManagementRequest(handler, http.MethodPatch, "/api/v2/repositories/"+proxy.ID, string(body), version, "")
	}
	sourceAuth.Store(privateProxyAuthorization(scheme, secondSecret))
	rotated := patch(secondSecret, proxy.Version)
	if rotated.Code != http.StatusOK || strings.Contains(rotated.Body.String(), secondSecret) {
		t.Fatalf("rotate=%d body=%s", rotated.Code, rotated.Body.String())
	}
	extraAvailable.Store(true)
	read(http.StatusOK)
	path, asset = extraPath, extraAsset
	read(http.StatusOK)
	path, asset = primaryPath, primaryAsset
	if len(upstream.snapshot()) <= cold || (issuer && tokenNumber.Load() != 2) {
		t.Fatal("credential rotation reused the previous content or issuer-token cache")
	}
	stale := patch(firstSecret, proxy.Version)
	if stale.Code != http.StatusPreconditionFailed {
		t.Fatalf("stale credential update=%d", stale.Code)
	}
	cleared := ociBearerManagementRequest(handler, http.MethodPatch, "/api/v2/repositories/"+proxy.ID, `{"upstreamAuth":{"scheme":"none"}}`, rotated.Header().Get("ETag"), "")
	if cleared.Code != http.StatusOK || strings.Contains(cleared.Body.String(), `"upstreamAuth"`) {
		t.Fatalf("clear=%d body=%s", cleared.Code, cleared.Body.String())
	}
	beforeClearRead := len(upstream.snapshot())
	read(http.StatusBadGateway)
	if len(upstream.snapshot()) <= beforeClearRead {
		t.Fatal("cleared credential reused private cached content")
	}
	for _, request := range upstream.snapshot()[beforeClearRead:] {
		if request.headers.Get("Authorization") != "" {
			t.Fatal("cleared credential reused an issuer token or source authorization")
		}
	}
	audits, err := store.ListAudits(t.Context(), repository.AuditQuery{GroupName: name, Limit: 100})
	if err != nil || len(audits) == 0 {
		t.Fatalf("private source audit evidence missing: %v", err)
	}
	encodedAudits, err := json.Marshal(audits)
	if err != nil || strings.Contains(string(encodedAudits), firstSecret) || strings.Contains(string(encodedAudits), secondSecret) || strings.Contains(string(encodedAudits), privateProxyAuthorization(scheme, firstSecret)) {
		t.Fatal("private source credential leaked into audit records")
	}

}

func TestPrivateProxyAPIAdmitsOnlyImplementedFormats(t *testing.T) {
	t.Setenv("GATEWAY_SETTINGS_ENCRYPTION_KEY", upstreamAuthTestKey)
	for _, format := range []repository.Format{repository.FormatGo, repository.FormatMaven, repository.FormatOCI, repository.FormatNPM, repository.FormatPyPI, repository.FormatCargo, repository.FormatRaw, repository.FormatAPT, repository.FormatConan} {
		t.Run(string(format), func(t *testing.T) {
			store := repository.NewMemoryStore()
			handler := newTestGateway(t, GatewayOptions{Dependencies: Dependencies{}, Store: store, Adapter: TestAdapter{}, Authenticator: privateProxyAuthenticator()})
			body := fmt.Sprintf(`{"name":"private-proxy","format":%q,"type":"proxy","endpoint":"https://%s","allowedHosts":["%s"],"upstreamAuth":{"scheme":"basic","username":"source-reader","secret":"synthetic-private-secret"}}`, format, redirectOrigin, redirectOrigin)
			created := ociBearerManagementRequest(handler, http.MethodPost, "/api/v2/repositories", body, "", "create-private-proxy")
			if created.Code != http.StatusCreated || strings.Contains(created.Body.String(), "synthetic-private-secret") || !strings.Contains(created.Body.String(), `"credentialsConfigured":true`) {
				t.Fatalf("create=%d body=%s", created.Code, created.Body.String())
			}
			var repo repository.HostedRepository
			if err := json.Unmarshal(created.Body.Bytes(), &repo); err != nil {
				t.Fatal(err)
			}
			stored, err := store.GetHostedRepository(t.Context(), repo.ID)
			if err != nil || stored.UpstreamAuth == nil || stored.UpstreamAuth.Secret == "" || stored.UpstreamAuth.Secret == "synthetic-private-secret" {
				t.Fatal("private upstream credential was not persisted encrypted")
			}
		})
	}
}

func TestPrivateProxyInvalidCiphertextFailsBeforeNetwork(t *testing.T) {
	for _, format := range []repository.Format{repository.FormatMaven, repository.FormatOCI} {
		t.Run(string(format), func(t *testing.T) {
			upstream := newProxyRedirectFixture(t, func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "anonymous-success") })
			store := repository.NewMemoryStore()
			repo, err := store.CreateHostedRepository(t.Context(), repository.HostedRepository{
				ID: uuid.NewString(), Name: "invalid-private-proxy", Format: format, Type: repository.RepositoryTypeProxy,
				Endpoint: "https://" + redirectOrigin, AllowedHosts: []string{redirectOrigin},
				UpstreamAuth: &repository.UpstreamAuth{Scheme: repository.UpstreamAuthSchemeBearer, Secret: "invalid-ciphertext"},
			})
			if err != nil {
				t.Fatal(err)
			}
			objects := objectstore.NewMemoryStore()
			handler := newTestGateway(t, GatewayOptions{Dependencies: Dependencies{}, Store: store, Adapter: TestAdapter{}, Authenticator: privateProxyAuthenticator(), Storage: ObjectStores{format: objects}, Upstream: upstream.client, Caches: GatewayCaches{Maven: NewDefaultMavenCache(objects, []string{redirectOrigin}), OCI: NewOCICache(objects, time.Hour, time.Hour, time.Hour, []string{redirectOrigin})}})
			path := "/maven/" + repo.Name + "/org/example/widget/1.0/widget-1.0.jar"
			if format == repository.FormatOCI {
				path = "/v2/" + repo.Name + "/demo/manifests/latest"
			}
			request := httptest.NewRequest(http.MethodGet, path, nil)
			authorize(request, "admin-secret")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusBadGateway || len(upstream.snapshot()) != 0 || strings.Contains(response.Body.String(), "invalid-ciphertext") {
				t.Fatalf("invalid credential status=%d calls=%d body=%s", response.Code, len(upstream.snapshot()), response.Body.String())
			}
		})
	}
}

func TestPrivateProxyCredentialRedirectBoundary(t *testing.T) {
	for _, format := range []string{"maven", "oci"} {
		for _, crossOrigin := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/cross=%v", format, crossOrigin), func(t *testing.T) {
				var visits atomic.Int32
				fixture := newProxyRedirectFixture(t, func(w http.ResponseWriter, r *http.Request) {
					if visits.Add(1) == 1 {
						if r.Header.Get("Authorization") != "Bearer synthetic-source-secret" {
							t.Error("initial bound request omitted its source credential")
						}
						host := redirectOrigin
						if crossOrigin {
							host = redirectCDN
						}
						http.Redirect(w, r, "https://"+host+"/download", http.StatusTemporaryRedirect)
						return
					}
					want := "Bearer synthetic-source-secret"
					if crossOrigin {
						want = ""
					}
					if r.Header.Get("Authorization") != want || r.Header.Get("Cookie") != "" || r.Header.Get("Proxy-Authorization") != "" {
						t.Error("redirect exceeded its source credential boundary")
					}
					_, _ = io.WriteString(w, "synthetic-complete-object")
				})
				member := proxyRedirectMember(nil)
				member.UpstreamAuth = &repository.UpstreamAuth{Scheme: repository.UpstreamAuthSchemeBearer, Secret: sealedUpstreamSecret(t, "synthetic-source-secret")}
				headers := http.Header{"Authorization": {"Bearer gateway-client-secret"}, "Cookie": {"gateway-cookie"}, "Proxy-Authorization": {"Basic gateway-proxy-secret"}}
				response, err := proxyRedirectFetch(t.Context(), fixture.client, format, http.MethodGet, member, headers)
				if err != nil {
					t.Fatal(err)
				}
				defer closeProxyRedirectResponse(response)
				body, err := io.ReadAll(response.Body)
				if err != nil || string(body) != "synthetic-complete-object" || visits.Load() != 2 {
					t.Fatalf("redirect incomplete: visits=%d err=%v", visits.Load(), err)
				}
			})
		}
	}
}

func TestPrivateOCIIssuerRedirectNeverReattachesCredential(t *testing.T) {
	for _, crossOrigin := range []bool{false, true} {
		t.Run(fmt.Sprintf("cross=%v", crossOrigin), func(t *testing.T) {
			fixture := newProxyRedirectFixture(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/token" {
					if r.Header.Get("Authorization") != "Bearer synthetic-issuer-secret" {
						t.Error("bound issuer omitted its source credential")
					}
					host := redirectOrigin
					if crossOrigin {
						host = redirectCDN
					}
					http.Redirect(w, r, "https://"+host+"/issue", http.StatusTemporaryRedirect)
					return
				}
				if r.URL.Path == "/issue" {
					want := "Bearer synthetic-issuer-secret"
					if crossOrigin {
						want = ""
					}
					if r.Header.Get("Authorization") != want {
						t.Error("issuer redirect exceeded its credential boundary")
					}
					_, _ = io.WriteString(w, `{"token":"issued-pull-token"}`)
					return
				}
				if r.Header.Get("Authorization") == "Bearer issued-pull-token" {
					_, _ = io.WriteString(w, "complete-object")
					return
				}
				if r.Header.Get("Authorization") != "" {
					t.Error("private issuer credential reached the content request")
				}
				w.Header().Set("WWW-Authenticate", `Bearer realm="https://`+redirectOrigin+`/token",service="registry"`)
				w.WriteHeader(http.StatusUnauthorized)
			})
			member := bearerTestMember()
			member.UpstreamAuth = &repository.UpstreamAuth{Scheme: repository.UpstreamAuthSchemeBearer, Secret: sealedUpstreamSecret(t, "synthetic-issuer-secret")}
			if err := bearerRead(t.Context(), fixture.client, member); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestPrivateProxyUnauthorizedDoesNotContactAnonymousGroupFallback(t *testing.T) {
	for _, format := range []repository.Format{repository.FormatMaven, repository.FormatOCI} {
		for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
			t.Run(fmt.Sprintf("%s/%d", format, status), func(t *testing.T) {
				var fallback atomic.Int32
				asset := redirectCacheOCIImage(t)["/v2/demo/manifests/latest"]
				fixture := newProxyRedirectFixture(t, func(w http.ResponseWriter, r *http.Request) {
					if r.Host == redirectOrigin {
						http.Error(w, "synthetic-private-upstream-error-secret", status)
						return
					}
					fallback.Add(1)
					w.Header().Set("Docker-Content-Digest", asset.digest)
					_, _ = w.Write(asset.body)
				})
				store := repository.NewMemoryStore()
				var members []repository.GroupMember
				for i, host := range []string{redirectOrigin, redirectCDN} {
					repo := repository.HostedRepository{ID: uuid.NewString(), Name: fmt.Sprintf("private-fallback-%d", i), Format: format, Type: repository.RepositoryTypeProxy, Endpoint: "https://" + host, AllowedHosts: []string{host}}
					if i == 0 {
						repo.UpstreamAuth = &repository.UpstreamAuth{Scheme: repository.UpstreamAuthSchemeBearer, Secret: sealedUpstreamSecret(t, "synthetic-private-source-secret")}
					}
					if _, err := store.CreateHostedRepository(t.Context(), repo); err != nil {
						t.Fatal(err)
					}
					members = append(members, repository.GroupMember{RepositoryID: repo.ID, Position: i})
				}
				group, _, err := store.CreateHostedGroupIdempotently(t.Context(), repository.HostedGroup{ID: uuid.NewString(), Name: "private-fallback-group", Format: format, Members: members}, "fixture", uuid.NewString(), "fixture")
				if err != nil {
					t.Fatal(err)
				}
				objects := objectstore.NewMemoryStore()
				handler := newTestGateway(t, GatewayOptions{Dependencies: Dependencies{}, Store: store, Adapter: TestAdapter{}, Authenticator: privateProxyAuthenticator(), Storage: ObjectStores{format: objects}, Upstream: fixture.client,
					Caches: GatewayCaches{Maven: NewDefaultMavenCache(objects, []string{redirectOrigin, redirectCDN}), OCI: NewOCICache(objects, time.Hour, time.Hour, time.Hour, []string{redirectOrigin, redirectCDN})}})
				path := "/maven/" + group.Name + "/org/example/widget/1.0/widget-1.0.pom"
				if format == repository.FormatOCI {
					path = "/v2/" + group.Name + "/demo/manifests/latest"
				}
				request := httptest.NewRequest(http.MethodGet, path, nil)
				authorize(request, "admin-secret")
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, request)
				if response.Code != http.StatusBadGateway || fallback.Load() != 0 || strings.Contains(response.Body.String(), "synthetic-private-upstream-error-secret") {
					t.Fatalf("private source denial hidden: status=%d anonymousFallback=%d", response.Code, fallback.Load())
				}
			})
		}
	}
}

func TestPrivateOCICacheRechecksEgressAndRequiresAudit(t *testing.T) {
	for _, route := range []string{"proxy", "group"} {
		for _, sourceStatus := range []int{http.StatusOK, http.StatusNotFound} {
			t.Run(fmt.Sprintf("%s/%d", route, sourceStatus), func(t *testing.T) {
				asset := redirectCacheOCIImage(t)["/v2/demo/manifests/latest"]
				fixture := newProxyRedirectFixture(t, func(w http.ResponseWriter, r *http.Request) {
					if r.Header.Get("Authorization") != "Bearer private-cache-source" {
						t.Error("protected cache origin omitted source authentication")
					}
					w.Header().Set("Docker-Content-Digest", asset.digest)
					w.WriteHeader(sourceStatus)
					_, _ = w.Write(asset.body)
				})
				store := repository.NewMemoryStore()
				proxy, err := store.CreateHostedRepository(t.Context(), repository.HostedRepository{
					ID: uuid.NewString(), Name: "private-cache-proxy", Format: repository.FormatOCI, Type: repository.RepositoryTypeProxy,
					Endpoint: "https://" + redirectOrigin, AllowedHosts: []string{redirectOrigin},
					UpstreamAuth: &repository.UpstreamAuth{Scheme: repository.UpstreamAuthSchemeBearer, Secret: sealedUpstreamSecret(t, "private-cache-source")},
				})
				if err != nil {
					t.Fatal(err)
				}
				name := proxy.Name
				if route == "group" {
					group, _, err := store.CreateHostedGroupIdempotently(t.Context(), repository.HostedGroup{ID: uuid.NewString(), Name: "private-cache-group", Format: repository.FormatOCI, Members: []repository.GroupMember{{RepositoryID: proxy.ID}}}, "fixture", uuid.NewString(), "fixture")
					if err != nil {
						t.Fatal(err)
					}
					name = group.Name
				}
				objects := objectstore.NewMemoryStore()
				cache := NewOCICache(objects, time.Hour, time.Hour, time.Hour, []string{redirectOrigin})
				newHandler := func(metadata GatewayStore) http.Handler {
					return newTestGateway(t, GatewayOptions{Dependencies: Dependencies{}, Store: metadata, Adapter: TestAdapter{}, Authenticator: privateProxyAuthenticator(), Storage: ObjectStores{repository.FormatOCI: objects}, Caches: GatewayCaches{OCI: cache}, Upstream: fixture.client})
				}
				handler := newHandler(store)
				read := func(want int) {
					t.Helper()
					request := httptest.NewRequest(http.MethodGet, "/v2/"+name+"/demo/manifests/latest", nil)
					authorize(request, "admin-secret")
					response := httptest.NewRecorder()
					handler.ServeHTTP(response, request)
					if response.Code != want {
						t.Fatalf("cache policy/audit status=%d want=%d body=%s", response.Code, want, response.Body.String())
					}
				}
				read(sourceStatus)
				cache.SetAllowedProxyHosts(nil)
				read(http.StatusForbidden)
				cache.SetAllowedProxyHosts([]string{redirectOrigin})
				read(sourceStatus)
				handler = newHandler(failingAuditStore{store})
				read(http.StatusBadGateway)
				if len(fixture.snapshot()) != 1 {
					t.Fatal("cache gate contacted the origin after egress revocation or audit failure")
				}
			})
		}
	}
}
