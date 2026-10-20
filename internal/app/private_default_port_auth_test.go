package app

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ccsert/artifact-gateway/internal/objectstore"
	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

func TestPrivateFormatDefaultPortCredentials(t *testing.T) {
	privateDefaultPortCredentials(t, func(*testing.T) privateProxyBackend {
		return privateProxyBackend{store: repository.NewMemoryStore(), objects: objectstore.NewMemoryStore()}
	})
}

func privateDefaultPortCredentials(t *testing.T, newBackend func(*testing.T) privateProxyBackend) {
	t.Helper()
	t.Setenv("GATEWAY_SETTINGS_ENCRYPTION_KEY", upstreamAuthTestKey)
	for _, format := range []repository.Format{repository.FormatNPM, repository.FormatPyPI} {
		for _, scheme := range []repository.UpstreamAuthScheme{repository.UpstreamAuthSchemeBasic, repository.UpstreamAuthSchemeBearer} {
			for _, direction := range []string{"asset-explicit", "endpoint-explicit"} {
				for _, group := range []bool{false, true} {
					t.Run(fmt.Sprintf("%s/%s/%s/group=%v", format, scheme, direction, group), func(t *testing.T) {
						privateDefaultPortCredentialContract(t, format, scheme, direction, group, newBackend(t))
					})
				}
			}
		}
	}
}

func privateDefaultPortCredentialContract(t *testing.T, format repository.Format, scheme repository.UpstreamAuthScheme, direction string, group bool, backend privateProxyBackend) {
	t.Helper()
	fixture := privateNativeFixture(t, format)
	origin := "https://" + redirectOrigin
	endpoint, objectHost := origin, redirectOrigin+":443"
	if direction == "endpoint-explicit" {
		endpoint, objectHost = origin+":443", redirectOrigin
	} else {
		metadata := fixture.assets[fixture.metadata]
		metadata.body = bytes.ReplaceAll(metadata.body, []byte(origin+"/"), []byte(origin+":443/"))
		fixture.assets[fixture.metadata] = metadata
	}
	expected := privateProxyAuthorization(scheme, "default-port-source-secret")
	upstream := newProxyRedirectFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != expected {
			w.WriteHeader(http.StatusUnauthorized)
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
	store := backend.store
	auth := &repository.UpstreamAuth{Scheme: scheme, Secret: sealedUpstreamSecret(t, "default-port-source-secret")}
	if scheme == repository.UpstreamAuthSchemeBasic {
		auth.Username = "source-reader"
	}
	repo, err := store.CreateHostedRepository(t.Context(), repository.HostedRepository{ID: uuid.NewString(), Name: "default-port-" + uuid.NewString(), Format: format, Type: repository.RepositoryTypeProxy, Endpoint: endpoint, AllowedHosts: []string{redirectOrigin}, UpstreamAuth: auth})
	if err != nil {
		t.Fatal(err)
	}
	name := repo.Name
	if group {
		configured, _, err := store.CreateHostedGroupIdempotently(t.Context(), repository.HostedGroup{ID: uuid.NewString(), Name: "default-port-group-" + uuid.NewString(), Format: format, Members: []repository.GroupMember{{RepositoryID: repo.ID}}}, "default-port-fixture", uuid.NewString(), "default-port-fixture")
		if err != nil {
			t.Fatal(err)
		}
		name = configured.Name
	}
	makeHandler := func() http.Handler {
		return newTestGateway(t, GatewayOptions{Store: store, Adapter: TestAdapter{}, Authenticator: testAuthenticator(), Storage: SharedObjectStores(backend.objects), Upstream: upstream.client})
	}
	handler := makeHandler()
	get := func(resource string, full bool) {
		t.Helper()
		request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/"+string(format)+"/"+name+resource, nil)
		authorize(request, "admin-secret")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK || full && !bytes.Equal(response.Body.Bytes(), fixture.body) {
			t.Fatalf("native %s response=%d, complete bytes=%v", resource, response.Code, bytes.Equal(response.Body.Bytes(), fixture.body))
		}
		if strings.Contains(response.Body.String(), "default-port-source-secret") {
			t.Fatal("source credential leaked in the native response")
		}
	}
	get(fixture.metadata, false)
	get(fixture.object, true)
	requests := upstream.snapshot()
	objectCalls := 0
	for _, request := range requests {
		if request.headers.Get("Authorization") != expected || request.headers.Get("Proxy-Authorization") != "" {
			t.Fatal("metadata or distribution did not use its origin credential")
		}
		if request.path == fixture.object {
			objectCalls++
			if request.host != objectHost {
				t.Fatalf("fixture did not exercise the alternate port spelling: %s", request.host)
			}
		}
	}
	if objectCalls != 1 {
		t.Fatalf("cold distribution source reads=%d, want1", objectCalls)
	}
	digest := fmt.Sprintf("sha256:%x", sha256.Sum256(fixture.body))
	if format == repository.FormatNPM {
		version, err := store.GetNPMVersion(t.Context(), repo.ID, "private-demo", "1.0.0")
		if err != nil || version.Digest != digest || version.ObjectKey == "" || version.Size != int64(len(fixture.body)) {
			t.Fatalf("cached complete npm identity=%#v error=%v", version, err)
		}
	} else {
		file, err := store.GetPyPIFile(t.Context(), repo.ID, strings.TrimPrefix(fixture.object, "/packages/"))
		if err != nil || file.Digest != digest || file.SourceVersion != repo.Version || file.ObjectKey == "" || file.Size != int64(len(fixture.body)) {
			t.Fatalf("cached complete PyPI identity=%#v error=%v", file, err)
		}
	}
	get(fixture.object, true)
	if backend.reload != nil {
		store, backend.cache = backend.reload()
		handler = makeHandler()
		get(fixture.object, true)
	}
	if len(upstream.snapshot()) != len(requests) {
		t.Fatal("verified warm distribution unexpectedly fetched the upstream")
	}
}
