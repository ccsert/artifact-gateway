package app

import (
	"bytes"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
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

type privateNativeAssets struct {
	assets           map[string]redirectCacheAsset
	metadata, object string
	body             []byte
}

func privateNativeFixture(t *testing.T, format repository.Format) privateNativeAssets {
	t.Helper()
	assets := map[string]redirectCacheAsset{}
	f := privateNativeAssets{assets: assets}
	origin := "https://" + redirectOrigin
	switch format {
	case repository.FormatNPM:
		f.body = npmFixtureTarball(t, "private-demo", "1.0.0")
		f.metadata, f.object = "/private-demo", "/private-demo/-/private-demo-1.0.0.tgz"
		sum := sha512.Sum512(f.body)
		sha := sha1.Sum(f.body)
		assets[f.metadata] = redirectCacheAsset{mediaType: "application/json", body: []byte(fmt.Sprintf(`{"name":"private-demo","dist-tags":{"latest":"1.0.0"},"versions":{"1.0.0":{"name":"private-demo","version":"1.0.0","dist":{"tarball":%q,"integrity":%q,"shasum":%q}}}}`, origin+f.object, "sha512-"+base64.StdEncoding.EncodeToString(sum[:]), hex.EncodeToString(sha[:])))}
	case repository.FormatPyPI:
		f.body = pypiFixtureWheel(t, "private_demo", "1.0.0")
		f.metadata, f.object = "/simple/private-demo/", "/packages/private_demo-1.0.0-py3-none-any.whl"
		sum := sha256.Sum256(f.body)
		assets[f.metadata] = redirectCacheAsset{mediaType: "text/html", body: []byte(fmt.Sprintf(`<a href="%s#sha256=%s">private_demo-1.0.0-py3-none-any.whl</a>`, origin+f.object, hex.EncodeToString(sum[:])))}
	case repository.FormatCargo:
		_, f.body = cargoC0PublishFixture(t, "demo", "1.0.0", "demo")
		f.metadata, f.object = "/de/mo/demo", "/api/v1/crates/demo/1.0.0/download"
		sum := sha256.Sum256(f.body)
		assets[f.metadata] = redirectCacheAsset{mediaType: "text/plain", body: []byte(fmt.Sprintf(`{"name":"demo","vers":"1.0.0","deps":[],"cksum":%q,"features":{},"yanked":false}`+"\n", hex.EncodeToString(sum[:])))}
		assets["/config.json"] = redirectCacheAsset{mediaType: "application/json", body: []byte(fmt.Sprintf(`{"dl":%q,"api":%q}`, origin+"/api/v1/crates", origin))}
	case repository.FormatAPT:
		f.body = aptManagementDebianPackage(t, "Package: private-demo\nVersion: 1.0.0\nArchitecture: amd64\nMaintainer: Fixture <fixture@example.invalid>\nDescription: private fixture\n")
		f.metadata, f.object = "/dists/stable/Release", "/pool/private-demo.deb"
		sum := sha256.Sum256(f.body)
		assets[f.metadata] = redirectCacheAsset{mediaType: "text/plain", body: []byte(fmt.Sprintf("Suite: stable\nSHA256:\n %x %d pool/private-demo.deb\n", sum, len(f.body)))}
	default:
		t.Fatalf("unsupported private fixture %s", format)
	}
	assets[f.object] = redirectCacheAsset{mediaType: "application/octet-stream", body: f.body}
	return f
}

func TestPrivateNativeFormatCredentials(t *testing.T) {
	for _, format := range []repository.Format{repository.FormatNPM, repository.FormatPyPI, repository.FormatCargo, repository.FormatAPT} {
		for _, scheme := range []repository.UpstreamAuthScheme{repository.UpstreamAuthSchemeBasic, repository.UpstreamAuthSchemeBearer} {
			for _, group := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/group=%v", format, scheme, group), func(t *testing.T) {
					privateNativeCredentialsContract(t, format, scheme, group, privateProxyBackend{store: repository.NewMemoryStore(), objects: objectstore.NewMemoryStore(), cache: objectstore.NewMemoryStore()})
				})
			}
		}
	}
}

func privateNativeCredentialsContract(t *testing.T, format repository.Format, scheme repository.UpstreamAuthScheme, group bool, backend privateProxyBackend) {
	t.Helper()
	t.Setenv("GATEWAY_SETTINGS_ENCRYPTION_KEY", upstreamAuthTestKey)
	store := backend.store
	fixture := privateNativeFixture(t, format)
	if backend.assets != nil {
		fixture.assets = backend.assets
		fixture.body = fixture.assets[fixture.object].body
	}
	var expected atomic.Value
	expected.Store(privateProxyAuthorization(scheme, "native-first-source-secret"))
	var sourceCalls, fallbackCalls, refusalStatus atomic.Int32
	refusalStatus.Store(http.StatusUnauthorized)
	upstream := newProxyRedirectFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Host == redirectCDN {
			fallbackCalls.Add(1)
		} else {
			sourceCalls.Add(1)
		}
		if r.Host != redirectCDN && r.Header.Get("Authorization") != expected.Load().(string) {
			http.Error(w, "native-first-source-secret", int(refusalStatus.Load()))
			return
		}
		asset, found := fixture.assets[r.URL.Path]
		if !found {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", asset.mediaType)
		_, _ = w.Write(asset.body)
	})
	repo, err := store.CreateHostedRepository(t.Context(), repository.HostedRepository{ID: uuid.NewString(), Name: "private-" + string(format) + "-" + uuid.NewString(), Format: format, Type: repository.RepositoryTypeProxy, Endpoint: "https://" + redirectOrigin, AllowedHosts: []string{redirectOrigin}, UpstreamAuth: &repository.UpstreamAuth{Scheme: scheme, Username: map[bool]string{true: "source-reader"}[scheme == repository.UpstreamAuthSchemeBasic], Secret: sealedUpstreamSecret(t, "native-first-source-secret")}})
	if err != nil {
		t.Fatal(err)
	}
	name := repo.Name
	if group {
		fallback, err := store.CreateHostedRepository(t.Context(), repository.HostedRepository{ID: uuid.NewString(), Name: "public-fallback-" + uuid.NewString(), Format: format, Type: repository.RepositoryTypeProxy, Endpoint: "https://" + redirectCDN, AllowedHosts: []string{redirectCDN}})
		if err != nil {
			t.Fatal(err)
		}
		// Cargo requires full identical immutable indexes. A successful Group
		// includes both; failure must not contact the second member at all.
		configured, _, err := store.CreateHostedGroupIdempotently(t.Context(), repository.HostedGroup{ID: uuid.NewString(), Name: "native-private-group-" + uuid.NewString(), Format: format, Members: []repository.GroupMember{{RepositoryID: repo.ID, Position: 0}, {RepositoryID: fallback.ID, Position: 1}}}, "private-fixture", uuid.NewString(), "private-auth")
		if err != nil {
			t.Fatal(err)
		}
		name = configured.Name
	}
	makeHandler := func() http.Handler {
		return newTestGateway(t, GatewayOptions{Dependencies: Dependencies{CargoUpstreamClient: upstream.client.HTTPClient}, Store: store, Adapter: TestAdapter{}, Authenticator: testAuthenticator(), Storage: SharedObjectStores(backend.objects), Upstream: upstream.client})
	}
	handler := makeHandler()
	path := func(resource string) string { return "/" + string(format) + "/" + name + resource }
	get := func(resource string, want int, full bool) {
		t.Helper()
		r := httptest.NewRequest(http.MethodGet, path(resource), nil)
		authorize(r, "admin-secret")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s got=%d want=%d body=%s", resource, w.Code, want, w.Body.String())
		}
		if full && !bytes.Equal(w.Body.Bytes(), fixture.body) {
			t.Fatal("private complete object bytes changed")
		}
		if strings.Contains(w.Body.String(), "native-first-source-secret") {
			t.Fatal("source secret leaked in response")
		}
	}
	if backend.client != nil {
		backend.client(t, handler, name, "cold", fixture.assets)
	}
	get(fixture.metadata, 200, false)
	get(fixture.object, 200, true)
	get(fixture.object, 200, true)
	witness := privateCacheVersionWitness(t, format, store, repo, fixture)
	if backend.reload != nil {
		store, backend.cache = backend.reload()
		handler = makeHandler()
		get(fixture.object, 200, true)
	}
	if backend.client != nil {
		backend.client(t, handler, name, "hot", fixture.assets)
	}
	beforeMissingKey := sourceCalls.Load()
	t.Setenv("GATEWAY_SETTINGS_ENCRYPTION_KEY", "")
	get(fixture.metadata, 502, false)
	get(fixture.object, 502, false)
	if sourceCalls.Load() != beforeMissingKey {
		t.Fatal("missing decryption key reached the upstream")
	}
	t.Setenv("GATEWAY_SETTINGS_ENCRYPTION_KEY", upstreamAuthTestKey)
	privateRejectSourceRebinding(t, handler, store, repo, func() int32 { return sourceCalls.Load() + fallbackCalls.Load() })
	beforeCorruption := sourceCalls.Load()
	repo = privateCorruptSourceCredential(t, store, repo, func() {
		get(fixture.metadata, 502, false)
		get(fixture.object, 502, false)
	})
	if sourceCalls.Load() != beforeCorruption {
		t.Fatal("damaged credential reached source")
	}
	patch := func(secret string, clear bool) {
		t.Helper()
		auth := map[string]string{"scheme": string(scheme), "username": repo.UpstreamAuth.Username, "secret": secret}
		if clear {
			auth = map[string]string{"scheme": "none"}
		}
		data, _ := json.Marshal(map[string]any{"upstreamAuth": auth})
		r := httptest.NewRequest(http.MethodPatch, "/api/v2/repositories/"+repo.ID, bytes.NewReader(data))
		authorize(r, "admin-secret")
		r.Header.Set("If-Match", repo.Version)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("credential patch=%d %s", w.Code, w.Body.String())
		}
		repo, err = store.GetHostedRepository(t.Context(), repo.ID)
		if err != nil {
			t.Fatal(err)
		}
	}
	expected.Store(privateProxyAuthorization(scheme, "native-second-source-secret"))
	patch("incorrect-source-secret", false)
	beforeFallback := fallbackCalls.Load()
	for i := 0; i < 4; i++ {
		get(fixture.metadata, 502, false)
		get(fixture.object, 502, false)
	}
	if fallbackCalls.Load() != beforeFallback {
		t.Fatal("private authentication refusal reached anonymous Group fallback")
	}
	patch("native-second-source-secret", false)
	before := sourceCalls.Load()
	get(fixture.metadata, 200, false)
	get(fixture.object, 200, true)
	if sourceCalls.Load() == before {
		t.Fatal("rotated configuration reused an old authorization cache")
	}
	witness(store, repo)
	refusalStatus.Store(http.StatusForbidden)
	patch("", true)
	beforeFallback = fallbackCalls.Load()
	for i := 0; i < 4; i++ {
		get(fixture.metadata, 502, false)
		get(fixture.object, 502, false)
	}
	if fallbackCalls.Load() != beforeFallback {
		t.Fatal("credential clear reached anonymous fallback")
	}
	privateAssertRedactedManagementAndAudit(t, handler, store, repo, "native-first-source-secret", "native-second-source-secret", "incorrect-source-secret", "damaged-source-ciphertext")
}
