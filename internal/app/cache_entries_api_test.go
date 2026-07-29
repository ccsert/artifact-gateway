package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

func newCacheEntriesTestHandler(t *testing.T) (http.Handler, *repository.MemoryStore, OCIObjectStore, *OCICache, *MavenCache, *RawCache, *ConanCache) {
	t.Helper()
	store := repository.NewMemoryStore()
	objectStore := NewMemoryOCIObjectStore()
	ociCache := NewDefaultOCICache(objectStore, nil)
	mavenCache := NewDefaultMavenCache(objectStore, nil)
	rawCache := NewDefaultRawCache(objectStore, nil)
	conanCache := NewDefaultConanCache(objectStore, nil)
	maintenance := NewCacheMaintenanceWithRaw(objectStore, ociCache, rawCache).WithConan(conanCache)
	handler := NewGatewayHandlerWithFormatCaches(Dependencies{}, store, TestAdapter{}, testAuthenticator(), ociCache, mavenCache, rawCache, conanCache, maintenance)
	return handler, store, objectStore, ociCache, mavenCache, rawCache, conanCache
}

func getCacheEntries(t *testing.T, handler http.Handler, token, query string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/operations/cache/entries"+query, nil)
	if token != "" {
		authorize(request, token)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func decodeCacheEntries(t *testing.T, response *httptest.ResponseRecorder) []CacheEntry {
	t.Helper()
	var entries []CacheEntry
	if err := json.Unmarshal(response.Body.Bytes(), &entries); err != nil {
		t.Fatalf("decode entries: %v body=%s", err, response.Body.String())
	}
	return entries
}

func TestCacheEntriesRequiresAdmin(t *testing.T) {
	handler, _, _, _, _, _, _ := newCacheEntriesTestHandler(t)

	if response := getCacheEntries(t, handler, "", "?repository=team&format=oci"); response.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous = %d %s", response.Code, response.Body.String())
	}
	if response := getCacheEntries(t, handler, "resolver-secret", "?repository=team&format=oci"); response.Code != http.StatusForbidden {
		t.Fatalf("non-admin = %d %s", response.Code, response.Body.String())
	}
}

func TestCacheEntriesValidatesQuery(t *testing.T) {
	handler, _, _, _, _, _, _ := newCacheEntriesTestHandler(t)

	if response := getCacheEntries(t, handler, "admin-secret", "?format=oci"); response.Code != http.StatusBadRequest {
		t.Fatalf("missing repository = %d %s", response.Code, response.Body.String())
	}
	if response := getCacheEntries(t, handler, "admin-secret", "?repository=team"); response.Code != http.StatusBadRequest {
		t.Fatalf("missing format = %d %s", response.Code, response.Body.String())
	}
	if response := getCacheEntries(t, handler, "admin-secret", "?repository=team&format=npm"); response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), `"code":"invalid_format"`) {
		t.Fatalf("unsupported format = %d %s", response.Code, response.Body.String())
	}
	if response := getCacheEntries(t, handler, "admin-secret", "?repository=missing&format=oci"); response.Code != http.StatusNotFound {
		t.Fatalf("unknown group = %d %s", response.Code, response.Body.String())
	}
}

func TestCacheEntriesListsOCIEntriesForGroupMembers(t *testing.T) {
	handler, store, _, ociCache, _, _, _ := newCacheEntriesTestHandler(t)
	ctx := context.Background()
	if _, err := store.CreateGroup(ctx, repository.Group{Name: "team", Enabled: true, Members: []repository.Member{{Name: "dockerhub", Type: repository.MemberProxy, Endpoint: "https://registry-1.docker.io", Position: 0}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateGroup(ctx, repository.Group{Name: "other", Enabled: true, Members: []repository.Member{{Name: "mirror", Type: repository.MemberProxy, Endpoint: "https://mirror.example", Position: 0}}}); err != nil {
		t.Fatal(err)
	}
	manifest := []byte(`{"schemaVersion":2}`)
	blob := []byte("blob-bytes")
	if err := ociCache.Store(ctx, ociCache.Key("team", "library/alpine", ociManifest, "latest"), CachedOCIContent{Body: manifest, Digest: digestOf(manifest), Repository: "library/alpine", ContentType: "application/vnd.oci.image.manifest.v1+json", Member: "dockerhub", Endpoint: "https://registry-1.docker.io"}); err != nil {
		t.Fatal(err)
	}
	if err := ociCache.Store(ctx, ociCache.Key("team", "library/alpine", ociBlob, digestOf(blob)), CachedOCIContent{Body: blob, Digest: digestOf(blob), Repository: "library/alpine", ContentType: "application/octet-stream", Member: "dockerhub", Endpoint: "https://registry-1.docker.io"}); err != nil {
		t.Fatal(err)
	}
	if err := ociCache.Store(ctx, ociCache.Key("other", "library/busybox", ociManifest, "latest"), CachedOCIContent{Body: manifest, Digest: digestOf(manifest), Repository: "library/busybox", Member: "mirror", Endpoint: "https://mirror.example"}); err != nil {
		t.Fatal(err)
	}
	if err := ociCache.StoreNegative(ctx, ociCache.Key("team", "library/alpine", ociManifest, "missing"), repository.Member{Name: "dockerhub", Endpoint: "https://registry-1.docker.io"}); err != nil {
		t.Fatal(err)
	}

	response := getCacheEntries(t, handler, "admin-secret", "?repository=team&format=oci")
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d %s", response.Code, response.Body.String())
	}
	entries := decodeCacheEntries(t, response)
	if len(entries) != 2 {
		t.Fatalf("entries = %+v", entries)
	}
	for _, entry := range entries {
		if entry.Repository != "library/alpine" || entry.Format != "oci" || entry.Member != "dockerhub" || entry.Endpoint != "https://registry-1.docker.io" || entry.Digest == "" || entry.Size == 0 {
			t.Fatalf("unexpected entry %+v", entry)
		}
	}
	if entries[0].ContentType == entries[1].ContentType {
		t.Fatalf("expected manifest and blob entries, got %+v", entries)
	}

	other := getCacheEntries(t, handler, "admin-secret", "?repository=other&format=oci")
	if other.Code != http.StatusOK {
		t.Fatalf("other status = %d %s", other.Code, other.Body.String())
	}
	otherEntries := decodeCacheEntries(t, other)
	if len(otherEntries) != 1 || otherEntries[0].Repository != "library/busybox" {
		t.Fatalf("other entries = %+v", otherEntries)
	}
}

func TestCacheEntriesListsMavenEntriesAndSkipsExpired(t *testing.T) {
	handler, store, objectStore, _, mavenCache, _, _ := newCacheEntriesTestHandler(t)
	ctx := context.Background()
	if _, err := store.CreateMavenGroup(ctx, repository.Group{Name: "engineering", Enabled: true, Members: []repository.Member{{Name: "central", Type: repository.MemberProxy, Endpoint: "https://repo.maven.apache.org/maven2", Position: 0}}}); err != nil {
		t.Fatal(err)
	}
	pom := []byte("<project/>")
	if err := mavenCache.Store(ctx, mavenCache.Key("engineering", "junit/junit/4.13.2/junit-4.13.2.pom"), "junit/junit/4.13.2/junit-4.13.2.pom", CachedMavenContent{Body: pom, ContentType: "application/xml", Member: "central", Endpoint: "https://repo.maven.apache.org/maven2", Repository: "engineering"}); err != nil {
		t.Fatal(err)
	}
	// An index from another group whose endpoint does not match must be hidden.
	if err := mavenCache.Store(ctx, mavenCache.Key("sandbox", "com/example/a/1/a-1.pom"), "com/example/a/1/a-1.pom", CachedMavenContent{Body: pom, ContentType: "application/xml", Member: "central", Endpoint: "https://sandbox.example"}); err != nil {
		t.Fatal(err)
	}
	// Expired entries are filtered even when their endpoint matches.
	expiredKey := mavenCache.Key("engineering", "com/example/old/1/old-1.pom")
	if err := mavenCache.Store(ctx, expiredKey, "com/example/old/1/old-1.pom", CachedMavenContent{Body: pom, ContentType: "application/xml", Member: "central", Endpoint: "https://repo.maven.apache.org/maven2"}); err != nil {
		t.Fatal(err)
	}
	encoded, err := objectStore.Get(ctx, expiredKey)
	if err != nil {
		t.Fatal(err)
	}
	var expiredIndex map[string]any
	if err := json.Unmarshal(encoded, &expiredIndex); err != nil {
		t.Fatal(err)
	}
	expiredIndex["expires_at"] = time.Now().UTC().Add(-time.Hour).Format(time.RFC3339Nano)
	mutated, err := json.Marshal(expiredIndex)
	if err != nil {
		t.Fatal(err)
	}
	if err := objectStore.Put(ctx, expiredKey, mutated); err != nil {
		t.Fatal(err)
	}

	response := getCacheEntries(t, handler, "admin-secret", "?repository=engineering&format=maven")
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d %s", response.Code, response.Body.String())
	}
	entries := decodeCacheEntries(t, response)
	if len(entries) != 1 {
		t.Fatalf("entries = %+v", entries)
	}
	entry := entries[0]
	if entry.Repository != "junit/junit/4.13.2/junit-4.13.2.pom" || entry.Format != "maven" || entry.ContentType != "application/xml" || entry.Member != "central" || entry.Digest == "" || entry.Size != int64(len(pom)) {
		t.Fatalf("unexpected entry %+v", entry)
	}
}

func TestCacheEntriesListsRawEntries(t *testing.T) {
	handler, store, _, _, _, rawCache, _ := newCacheEntriesTestHandler(t)
	ctx := context.Background()
	if _, err := store.CreateRawGroup(ctx, repository.Group{Name: "downloads", Enabled: true, Members: []repository.Member{{Name: "proxy", Type: repository.MemberProxy, Endpoint: "https://proxy.example", Position: 0}}}); err != nil {
		t.Fatal(err)
	}
	if err := rawCache.Store(ctx, rawCache.Key("downloads", "release/app.txt", "proxy", "https://proxy.example"), RawContent{Body: []byte("raw-bytes"), Repository: "downloads", Path: "release/app.txt", Member: "proxy", Endpoint: "https://proxy.example", CacheQuotaBytes: 10000}); err != nil {
		t.Fatal(err)
	}

	response := getCacheEntries(t, handler, "admin-secret", "?repository=downloads&format=raw")
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d %s", response.Code, response.Body.String())
	}
	entries := decodeCacheEntries(t, response)
	if len(entries) != 1 {
		t.Fatalf("entries = %+v", entries)
	}
	entry := entries[0]
	if entry.Format != "raw" || entry.Repository != "release/app.txt" || entry.Member != "proxy" || entry.Endpoint != "https://proxy.example" || entry.Digest == "" || entry.Size != int64(len("raw-bytes")) {
		t.Fatalf("unexpected entry %+v", entry)
	}
}

// A cache index written before the artifact coordinate was persisted carries
// no path field, so the listing falls back to the proxy group name.
func TestCacheEntriesRawFallsBackToGroupWithoutPath(t *testing.T) {
	handler, store, objectStore, _, _, _, _ := newCacheEntriesTestHandler(t)
	ctx := context.Background()
	if _, err := store.CreateRawGroup(ctx, repository.Group{Name: "downloads", Enabled: true, Members: []repository.Member{{Name: "proxy", Type: repository.MemberProxy, Endpoint: "https://proxy.example", Position: 0}}}); err != nil {
		t.Fatal(err)
	}
	body := []byte("raw-bytes")
	sum := sha256.Sum256(body)
	digest := hex.EncodeToString(sum[:])
	if err := objectStore.Put(ctx, "raw/objects/"+digest, body); err != nil {
		t.Fatal(err)
	}
	legacy, err := json.Marshal(map[string]any{
		"object":       "raw/objects/" + digest,
		"digest":       digest,
		"content_type": "application/octet-stream",
		"member":       "proxy",
		"endpoint":     "https://proxy.example",
		"repository":   "downloads",
		"size":         int64(len(body)),
		"expires_at":   time.Now().UTC().Add(time.Hour).Format(time.RFC3339Nano),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := objectStore.Put(ctx, "raw/index/"+digest+".json", legacy); err != nil {
		t.Fatal(err)
	}

	response := getCacheEntries(t, handler, "admin-secret", "?repository=downloads&format=raw")
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d %s", response.Code, response.Body.String())
	}
	entries := decodeCacheEntries(t, response)
	if len(entries) != 1 || entries[0].Repository != "downloads" {
		t.Fatalf("entries = %+v", entries)
	}
}

func TestCacheEntriesListsConanEntriesByGroup(t *testing.T) {
	handler, store, _, _, _, _, conanCache := newCacheEntriesTestHandler(t)
	ctx := context.Background()
	if _, err := store.CreateConanGroup(ctx, repository.Group{Name: "conan-team", Enabled: true, Members: []repository.Member{{Name: "center", Type: repository.MemberProxy, Endpoint: "https://conan.example", Position: 0}}}); err != nil {
		t.Fatal(err)
	}
	member := repository.Member{Name: "center", Endpoint: "https://conan.example"}
	path := "fmt/9.1.0/_/_/latest"
	cached := conanCacheEntry{body: []byte("conan-index"), contentType: "application/json", member: member.Name, endpoint: member.Endpoint, status: http.StatusOK}
	if err := conanCache.store(ctx, conanCache.key("conan-team", path, member), cached, "conan-team", 10000, time.Hour, "conan-team", path, ""); err != nil {
		t.Fatal(err)
	}
	other := conanCacheEntry{body: []byte("other"), contentType: "application/json", member: member.Name, endpoint: member.Endpoint, status: http.StatusOK}
	if err := conanCache.store(ctx, conanCache.key("other-group", path, member), other, "other-group", 10000, time.Hour, "other-group", path, ""); err != nil {
		t.Fatal(err)
	}

	response := getCacheEntries(t, handler, "admin-secret", "?repository=conan-team&format=conan")
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d %s", response.Code, response.Body.String())
	}
	entries := decodeCacheEntries(t, response)
	if len(entries) != 1 {
		t.Fatalf("entries = %+v", entries)
	}
	entry := entries[0]
	if entry.Format != "conan" || entry.Repository != path || entry.Member != "center" || entry.Endpoint != "https://conan.example" || entry.Digest == "" {
		t.Fatalf("unexpected entry %+v", entry)
	}
}
