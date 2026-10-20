package app

import (
	"bytes"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ccsert/artifact-gateway/internal/objectstore"
	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

func TestPrivateFormatNPMTarballPublication(t *testing.T) {
	for _, change := range []string{"credential-rotation", "metadata-revalidation"} {
		t.Run(change, func(t *testing.T) {
			privateNPMTarballPublication(t, change, privateProxyBackend{store: repository.NewMemoryStore(), objects: objectstore.NewMemoryStore()})
		})
	}
}

func privateNPMTarballPublication(t *testing.T, change string, backend privateProxyBackend) {
	t.Helper()
	t.Setenv("GATEWAY_SETTINGS_ENCRYPTION_KEY", upstreamAuthTestKey)
	const packageName, tarballName = "private-demo", "private-demo-1.0.0.tgz"
	oldBody := npmFixtureTarball(t, packageName, "1.0.0")
	newBody := npmFixtureTarballWithRoot(t, "new-source-package", packageName, "1.0.0")
	if bytes.Equal(oldBody, newBody) {
		t.Fatal("race fixture requires distinct complete publication bytes")
	}
	integrity := func(body []byte) string {
		sum := sha512.Sum512(body)
		return "sha512-" + base64.StdEncoding.EncodeToString(sum[:])
	}
	var generation atomic.Int32
	started, release := make(chan struct{}), make(chan struct{})
	var startOnce, releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
	upstream := newProxyRedirectFixture(t, func(w http.ResponseWriter, r *http.Request) {
		current := generation.Load()
		secret := "npm-race-first-secret"
		if change == "credential-rotation" && current != 0 {
			secret = "npm-race-second-secret"
		}
		if r.Header.Get("Authorization") != "Bearer "+secret {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		body := oldBody
		if current == 1 {
			body = newBody
		}
		switch r.URL.Path {
		case "/" + packageName:
			sha := sha1.Sum(body)
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"name":%q,"dist-tags":{"latest":"1.0.0"},"versions":{"1.0.0":{"name":%q,"version":"1.0.0","dist":{"tarball":%q,"integrity":%q,"shasum":%q}}}}`, packageName, packageName, "https://"+redirectOrigin+"/"+packageName+"/-/"+tarballName, integrity(body), hex.EncodeToString(sha[:]))
		case "/" + packageName + "/-/" + tarballName:
			if current == 0 {
				startOnce.Do(func() { close(started) })
				select {
				case <-release:
				case <-r.Context().Done():
					return
				}
			}
			_, _ = w.Write(body)
		default:
			http.NotFound(w, r)
		}
	})
	store := backend.store
	repo, err := store.CreateHostedRepository(t.Context(), repository.HostedRepository{ID: uuid.NewString(), Name: "npm-race-" + uuid.NewString(), Format: repository.FormatNPM, Type: repository.RepositoryTypeProxy, Endpoint: "https://" + redirectOrigin, AllowedHosts: []string{redirectOrigin}, UpstreamAuth: &repository.UpstreamAuth{Scheme: repository.UpstreamAuthSchemeBearer, Secret: sealedUpstreamSecret(t, "npm-race-first-secret")}})
	if err != nil {
		t.Fatal(err)
	}
	makeHandler := func() http.Handler {
		return newTestGateway(t, GatewayOptions{Store: store, Adapter: TestAdapter{}, Authenticator: testAuthenticator(), Storage: SharedObjectStores(backend.objects), Upstream: upstream.client})
	}
	handler := makeHandler()
	request := func(method, path, body, version string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		authorize(r, "admin-secret")
		if version != "" {
			r.Header.Set("If-Match", version)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	metadataURL := "/npm/" + repo.Name + "/" + packageName
	tarballURL := metadataURL + "/-/" + tarballName
	if initial := request(http.MethodGet, metadataURL, "", ""); initial.Code != http.StatusOK {
		t.Fatalf("initial metadata=%d %s", initial.Code, initial.Body.String())
	}
	completed := make(chan *httptest.ResponseRecorder, 1)
	go func() { completed <- request(http.MethodGet, tarballURL, "", "") }()
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("old authenticated tarball fetch did not reach the controlled source")
	}
	generation.Store(1)
	if change == "credential-rotation" {
		updated := request(http.MethodPatch, "/api/v2/repositories/"+repo.ID, `{"upstreamAuth":{"scheme":"bearer","secret":"npm-race-second-secret"}}`, repo.Version)
		if updated.Code != http.StatusOK {
			t.Fatalf("rotation=%d %s", updated.Code, updated.Body.String())
		}
		repo, err = store.GetHostedRepository(t.Context(), repo.ID)
		if err != nil {
			t.Fatal(err)
		}
		// Knowing the new repository version cannot authorize an old package
		// snapshot: its packument still carries the previous source binding.
		stale, loadErr := store.GetNPMVersion(t.Context(), repo.ID, packageName, "1.0.0")
		if loadErr != nil {
			t.Fatal(loadErr)
		}
		stale.Digest = fmt.Sprintf("sha256:%x", sha256.Sum256(oldBody))
		stale.ObjectKey = "native/npm/sha256/" + strings.TrimPrefix(stale.Digest, "sha256:")
		stale.Size = int64(len(oldBody))
		stale.SourceVersion = repo.Version
		if _, err = store.CacheNPMProxyTarball(t.Context(), stale); !errors.Is(err, repository.ErrVersionConflict) {
			t.Fatalf("current repository version authorized a stale package snapshot: %v", err)
		}
	} else {
		// Expire the authorized metadata without changing its binding or fields.
		// The next public HTTP request performs the actual metadata revalidation.
		pkg, loadErr := store.GetNPMPackage(t.Context(), repo.ID, packageName)
		if loadErr != nil {
			t.Fatal(loadErr)
		}
		pkg.MetadataExpiresAt = time.Now().UTC().Add(-time.Hour)
		if _, err = store.SyncNPMProxyPackage(t.Context(), pkg); err != nil {
			t.Fatal(err)
		}
	}
	refreshed := request(http.MethodGet, metadataURL, "", "")
	if refreshed.Code != http.StatusOK || !bytes.Contains(refreshed.Body.Bytes(), []byte(integrity(newBody))) {
		t.Fatalf("new metadata=%d %s", refreshed.Code, refreshed.Body.String())
	}
	current, err := store.GetNPMVersion(t.Context(), repo.ID, packageName, "1.0.0")
	if err != nil || current.ObjectKey != "" || current.Integrity != integrity(newBody) {
		t.Fatalf("new authorized uncached metadata=%#v err=%v", current, err)
	}
	releaseOnce.Do(func() { close(release) })
	select {
	case old := <-completed:
		if old.Code < 500 || bytes.Equal(old.Body.Bytes(), oldBody) {
			t.Errorf("old authenticated download committed after %s: status=%d", change, old.Code)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("old tarball fetch did not complete after release")
	}
	after, err := store.GetNPMVersion(t.Context(), repo.ID, packageName, "1.0.0")
	if err != nil || after.ObjectKey != "" || after.Digest != "" || after.Integrity != current.Integrity {
		t.Fatalf("old tarball changed current metadata/object reference: %#v err=%v", after, err)
	}
	full := request(http.MethodGet, tarballURL, "", "")
	if full.Code != http.StatusOK || !bytes.Equal(full.Body.Bytes(), newBody) {
		t.Fatalf("current authorized complete bytes=%d len=%d", full.Code, full.Body.Len())
	}
	digest := fmt.Sprintf("sha256:%x", sha256.Sum256(newBody))
	cached, err := store.GetNPMVersion(t.Context(), repo.ID, packageName, "1.0.0")
	if err != nil || cached.Digest != digest || cached.Size != int64(len(newBody)) || cached.Integrity != integrity(newBody) || cached.SourceVersion != "" {
		t.Fatalf("current complete object identity=%#v err=%v", cached, err)
	}
	if _, err = store.CacheNPMProxyTarball(t.Context(), cached); !errors.Is(err, repository.ErrVersionConflict) {
		t.Fatalf("stored publication implicitly authorized another cache commit: %v", err)
	}
	command := cached
	command.SourceVersion = repo.Version
	encodedCommand, err := json.Marshal(command)
	if err != nil || bytes.Contains(encodedCommand, []byte("SourceVersion")) || bytes.Contains(encodedCommand, []byte("sourceVersion")) {
		t.Fatalf("cache-only source version leaked into serialized publication: %s %v", encodedCommand, err)
	}
	if backend.reload != nil {
		store, backend.cache = backend.reload()
		handler = makeHandler()
	}
	if hot := request(http.MethodGet, tarballURL, "", ""); hot.Code != http.StatusOK || !bytes.Equal(hot.Body.Bytes(), newBody) {
		t.Fatalf("reopened authorized bytes=%d", hot.Code)
	}
	// Once complete bytes are cached, metadata cannot substitute a different
	// integrity tuple while preserving the existing object reference.
	generation.Store(2)
	pkg, err := store.GetNPMPackage(t.Context(), repo.ID, packageName)
	if err != nil {
		t.Fatal(err)
	}
	pkg.MetadataExpiresAt = time.Now().UTC().Add(-time.Hour)
	if _, err = store.SyncNPMProxyPackage(t.Context(), pkg); err != nil {
		t.Fatal(err)
	}
	if changed := request(http.MethodGet, metadataURL, "", ""); changed.Code != http.StatusBadGateway {
		t.Fatalf("cached immutable metadata replacement=%d", changed.Code)
	}
	retained, err := store.GetNPMVersion(t.Context(), repo.ID, packageName, "1.0.0")
	before, _ := json.Marshal(cached)
	next, _ := json.Marshal(retained)
	if err != nil || !bytes.Equal(before, next) {
		t.Fatalf("metadata replacement mixed old object and new integrity: %#v err=%v", retained, err)
	}
	if final := request(http.MethodGet, tarballURL, "", ""); final.Code != http.StatusOK || !bytes.Equal(final.Body.Bytes(), newBody) {
		t.Fatalf("retained complete bytes=%d", final.Code)
	}
}
