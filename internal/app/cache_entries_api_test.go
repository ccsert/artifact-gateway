package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
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
	if _, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: "proxy-repo", Name: "central-proxy", Format: repository.FormatMaven, Type: repository.RepositoryTypeProxy, Endpoint: "https://repo.maven.apache.org/maven2"}); err != nil {
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

	proxyResponse := getCacheEntries(t, handler, "admin-secret", "?repository=central-proxy&format=maven")
	if proxyResponse.Code != http.StatusOK {
		t.Fatalf("proxy status = %d %s", proxyResponse.Code, proxyResponse.Body.String())
	}
	proxyEntries := decodeCacheEntries(t, proxyResponse)
	if len(proxyEntries) != 1 || proxyEntries[0].Repository != entry.Repository {
		t.Fatalf("proxy entries = %+v", proxyEntries)
	}
}

func TestV2ProxyCacheBrowseListsMavenVersionsWithPagination(t *testing.T) {
	handler, store, objectStore, _, mavenCache, _, _ := newCacheEntriesTestHandler(t)
	ctx := context.Background()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "maven-proxy", Format: repository.FormatMaven, Type: repository.RepositoryTypeProxy, Endpoint: "https://repo.maven.apache.org/maven2", AnonymousRead: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReplaceRepositoryGrants(ctx, repo.ID, []repository.RepositoryGrant{{Principal: "reader", Scopes: []string{"repositories:read"}}}, "1"); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		"aopalliance/aopalliance/1.0/aopalliance-1.0.jar",
		"aopalliance/aopalliance/1.0/aopalliance-1.0.pom",
		"aopalliance/aopalliance/1.0/aopalliance-1.0.jar.sha1",
		"junit/junit/4.13.2/junit-4.13.2.jar",
	} {
		if err := mavenCache.Store(ctx, mavenCache.Key(repo.Name, path), path, CachedMavenContent{Body: []byte(path), ContentType: "application/java-archive", Member: "central", Endpoint: repo.Endpoint, Repository: repo.Name}); err != nil {
			t.Fatal(err)
		}
	}
	expiredPath := "expired/example/1.0/example-1.0.pom"
	expiredKey := mavenCache.Key(repo.Name, expiredPath)
	if err := mavenCache.Store(ctx, expiredKey, expiredPath, CachedMavenContent{Body: []byte(expiredPath), ContentType: "text/xml", Member: "central", Endpoint: repo.Endpoint, Repository: repo.Name}); err != nil {
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
	if err := mavenCache.StoreNegativeForRepository(ctx, mavenCache.Key(repo.Name, "missing/example/1.0/example-1.0.pom"), repo.Name, repository.Member{Name: "central", Endpoint: repo.Endpoint}); err != nil {
		t.Fatal(err)
	}
	anonymousBrowse := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/"+repo.ID+"/cache/entries?groupBy=version&assetFilter=all&pageSize=1", nil)
	anonymousBrowseResponse := httptest.NewRecorder()
	handler.ServeHTTP(anonymousBrowseResponse, anonymousBrowse)
	if anonymousBrowseResponse.Code != http.StatusOK {
		t.Fatalf("anonymous browse = %d %s", anonymousBrowseResponse.Code, anonymousBrowseResponse.Body.String())
	}

	request := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/"+repo.ID+"/cache/entries?groupBy=version&assetFilter=all&pageSize=1", nil)
	authorize(request, testAuthenticator().IssueToken("reader"))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d %s", response.Code, response.Body.String())
	}
	var page proxyCacheBrowsePage
	if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if page.TotalEstimate != 2 || len(page.Items) != 1 || page.NextPageToken == nil {
		t.Fatalf("page = %+v", page)
	}
	first := page.Items[0]
	if first.Coordinate != "aopalliance:aopalliance:1.0" || first.AssetCount != 3 || first.PrimaryAssetCount != 2 || first.SidecarCount != 1 || len(first.Assets) != 3 {
		t.Fatalf("first item = %+v", first)
	}
	mismatched := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/"+repo.ID+"/cache/entries?groupBy=version&assetFilter=all&pageSize=1&q=junit&pageToken="+*page.NextPageToken, nil)
	authorize(mismatched, testAuthenticator().IssueToken("reader"))
	mismatchedResponse := httptest.NewRecorder()
	handler.ServeHTTP(mismatchedResponse, mismatched)
	if mismatchedResponse.Code != http.StatusBadRequest {
		t.Fatalf("mismatched cursor status = %d %s", mismatchedResponse.Code, mismatchedResponse.Body.String())
	}

	next := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/"+repo.ID+"/cache/entries?groupBy=version&assetFilter=all&pageSize=1&pageToken="+*page.NextPageToken, nil)
	authorize(next, testAuthenticator().IssueToken("reader"))
	nextResponse := httptest.NewRecorder()
	handler.ServeHTTP(nextResponse, next)
	if nextResponse.Code != http.StatusOK {
		t.Fatalf("next status = %d %s", nextResponse.Code, nextResponse.Body.String())
	}
	var nextPage proxyCacheBrowsePage
	if err := json.Unmarshal(nextResponse.Body.Bytes(), &nextPage); err != nil {
		t.Fatal(err)
	}
	if len(nextPage.Items) != 1 || nextPage.Items[0].Coordinate != "junit:junit:4.13.2" || nextPage.NextPageToken != nil {
		t.Fatalf("next page = %+v", nextPage)
	}

	capacityRequest := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/"+repo.ID+"/capacity", nil)
	authorize(capacityRequest, testAuthenticator().IssueToken("reader"))
	capacityResponse := httptest.NewRecorder()
	handler.ServeHTTP(capacityResponse, capacityRequest)
	if capacityResponse.Code != http.StatusOK {
		t.Fatalf("capacity status = %d %s", capacityResponse.Code, capacityResponse.Body.String())
	}
	var capacity repository.RepositoryCapacity
	if err := json.Unmarshal(capacityResponse.Body.Bytes(), &capacity); err != nil {
		t.Fatal(err)
	}
	if capacity.UsedBytes == 0 || capacity.ObjectCount != 4 || capacity.PrimaryBytes == 0 || capacity.SidecarBytes == 0 || capacity.NegativeCount != 1 || capacity.ExpiredObjectCount != 1 || capacity.ReclaimableBytes == 0 {
		t.Fatalf("capacity = %+v", capacity)
	}

	invalidate := httptest.NewRequest(http.MethodPost, "/api/v2/repositories/"+repo.ID+"/cache/invalidate", strings.NewReader(`{"path":"aopalliance/aopalliance/1.0/aopalliance-1.0.jar"}`))
	authorize(invalidate, testAuthenticator().AdminToken)
	invalidateResponse := httptest.NewRecorder()
	handler.ServeHTTP(invalidateResponse, invalidate)
	if invalidateResponse.Code != http.StatusOK || !strings.Contains(invalidateResponse.Body.String(), `"invalidated":1`) {
		t.Fatalf("invalidate = %d %s", invalidateResponse.Code, invalidateResponse.Body.String())
	}
}

func TestV2MavenProxyNegativeCacheClearScopesAndPreservesPositiveEntries(t *testing.T) {
	handler, store, objectStore, _, mavenCache, _, _ := newCacheEntriesTestHandler(t)
	ctx := context.Background()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "maven-proxy", Format: repository.FormatMaven, Type: repository.RepositoryTypeProxy, Endpoint: "https://repo.maven.apache.org/maven2"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReplaceRepositoryGrants(ctx, repo.ID, []repository.RepositoryGrant{{Principal: "reader", Scopes: []string{"repositories:read"}}}, "1"); err != nil {
		t.Fatal(err)
	}
	positivePath := "com/example/app/1.0/app-1.0.jar"
	negativePath := "com/example/app/1.0/app-1.0.pom"
	otherPath := "com/example/other/1.0/other-1.0.pom"
	if err := mavenCache.Store(ctx, mavenCache.Key(repo.Name, positivePath), positivePath, CachedMavenContent{Body: []byte("jar"), ContentType: "application/java-archive", Member: "central", Endpoint: repo.Endpoint, Repository: repo.Name}); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{negativePath, otherPath} {
		if err := mavenCache.StoreNegativeForRepositoryPath(ctx, mavenCache.Key(repo.Name, path), repo.Name, path, repository.Member{Name: "central", Endpoint: repo.Endpoint}); err != nil {
			t.Fatal(err)
		}
	}
	otherRepo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "other-maven-proxy", Format: repository.FormatMaven, Type: repository.RepositoryTypeProxy, Endpoint: "https://other.maven.example"})
	if err != nil {
		t.Fatal(err)
	}
	otherNegativePath := "com/example/app/1.0/app-1.0.pom"
	if err := mavenCache.StoreNegativeForRepositoryPath(ctx, mavenCache.Key(otherRepo.Name, otherNegativePath), otherRepo.Name, otherNegativePath, repository.Member{Name: "other", Endpoint: otherRepo.Endpoint}); err != nil {
		t.Fatal(err)
	}

	unauthorized := httptest.NewRequest(http.MethodPost, "/api/v2/repositories/"+repo.ID+"/cache/negative:clear", nil)
	unauthorizedResponse := httptest.NewRecorder()
	handler.ServeHTTP(unauthorizedResponse, unauthorized)
	if unauthorizedResponse.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized = %d %s", unauthorizedResponse.Code, unauthorizedResponse.Body.String())
	}
	forbidden := httptest.NewRequest(http.MethodPost, "/api/v2/repositories/"+repo.ID+"/cache/negative:clear", nil)
	authorize(forbidden, testAuthenticator().IssueToken("reader"))
	forbiddenResponse := httptest.NewRecorder()
	handler.ServeHTTP(forbiddenResponse, forbidden)
	if forbiddenResponse.Code != http.StatusForbidden {
		t.Fatalf("forbidden = %d %s", forbiddenResponse.Code, forbiddenResponse.Body.String())
	}

	clear := httptest.NewRequest(http.MethodPost, "/api/v2/repositories/"+repo.ID+"/cache/negative:clear", strings.NewReader(`{"path":"com/example/app/1.0","prefix":true}`))
	authorize(clear, testAuthenticator().AdminToken)
	clearResponse := httptest.NewRecorder()
	handler.ServeHTTP(clearResponse, clear)
	if clearResponse.Code != http.StatusOK || !strings.Contains(clearResponse.Body.String(), `"cleared":1`) {
		t.Fatalf("clear = %d %s", clearResponse.Code, clearResponse.Body.String())
	}
	if _, err := mavenCache.Load(ctx, mavenCache.Key(repo.Name, positivePath)); err != nil {
		t.Fatalf("positive entry was changed: %v", err)
	}
	if _, err := mavenCache.Load(ctx, mavenCache.Key(repo.Name, negativePath)); err == nil || errors.Is(err, errMavenCacheNegative) {
		t.Fatalf("cleared negative entry = %v", err)
	}
	if _, err := mavenCache.Load(ctx, mavenCache.Key(repo.Name, otherPath)); !errors.Is(err, errMavenCacheNegative) {
		t.Fatalf("unmatched negative entry = %v", err)
	}
	if _, err := objectStore.Get(ctx, mavenCache.Key(otherRepo.Name, otherNegativePath)); err != nil {
		t.Fatalf("other repository negative entry was changed: %v", err)
	}

	clearAll := httptest.NewRequest(http.MethodPost, "/api/v2/repositories/"+repo.ID+"/cache/negative:clear", nil)
	authorize(clearAll, testAuthenticator().AdminToken)
	clearAllResponse := httptest.NewRecorder()
	handler.ServeHTTP(clearAllResponse, clearAll)
	if clearAllResponse.Code != http.StatusOK || !strings.Contains(clearAllResponse.Body.String(), `"cleared":1`) {
		t.Fatalf("clear all = %d %s", clearAllResponse.Code, clearAllResponse.Body.String())
	}
	if _, err := mavenCache.Load(ctx, mavenCache.Key(repo.Name, otherPath)); err == nil || errors.Is(err, errMavenCacheNegative) {
		t.Fatalf("remaining negative entry = %v", err)
	}
}

func TestV2MavenProxyCacheRefreshForcesUpstreamAndPreservesOldCacheOnFailure(t *testing.T) {
	store := repository.NewMemoryStore()
	objectStore := NewMemoryOCIObjectStore()
	ctx := context.Background()
	assetPath := "org/example/widget/1.0.0/widget-1.0.0.jar"
	upstream := &proxyUpstream{bodies: map[string][]byte{"/" + assetPath: []byte("fresh-jar")}, calls: map[string]int{}}
	upstreamServer := httptest.NewServer(upstream)
	defer upstreamServer.Close()
	allowedHost := strings.TrimPrefix(upstreamServer.URL, "http://")
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "maven-proxy", Format: repository.FormatMaven, Type: repository.RepositoryTypeProxy, Endpoint: upstreamServer.URL, AllowedHosts: []string{allowedHost}})
	if err != nil {
		t.Fatal(err)
	}
	mavenCache := NewMavenCache(objectStore, time.Hour, time.Hour, time.Hour, time.Hour, []string{allowedHost})
	if err := mavenCache.Store(ctx, mavenCache.Key(repo.Name, assetPath), assetPath, CachedMavenContent{Body: []byte("stale-jar"), ContentType: "application/java-archive", Member: repo.Name, Endpoint: repo.Endpoint, Repository: repo.Name}); err != nil {
		t.Fatal(err)
	}
	maintenance := NewCacheMaintenance(objectStore, NewDefaultOCICache(objectStore, nil))
	handler := NewGatewayHandlerWithFormatCaches(Dependencies{}, store, TestAdapter{}, testAuthenticator(), NewDefaultOCICache(objectStore, nil), mavenCache, nil, nil, maintenance, UpstreamClient{HTTPClient: upstreamServer.Client()})

	upstream.handler = func(path string) (int, []byte) { return http.StatusServiceUnavailable, nil }
	failed := httptest.NewRequest(http.MethodPost, "/api/v2/repositories/"+repo.ID+"/cache/refresh", strings.NewReader(`{"path":"`+assetPath+`"}`))
	authorize(failed, testAuthenticator().AdminToken)
	failedResponse := httptest.NewRecorder()
	handler.ServeHTTP(failedResponse, failed)
	if failedResponse.Code != http.StatusBadGateway {
		t.Fatalf("failed refresh = %d %s", failedResponse.Code, failedResponse.Body.String())
	}
	cached, err := mavenCache.Load(ctx, mavenCache.Key(repo.Name, assetPath))
	if err != nil || string(cached.Body) != "stale-jar" {
		t.Fatalf("old cache after failure = %q err=%v", cached.Body, err)
	}

	upstream.handler = nil
	mavenCache.RecordUpstreamSuccess(ctx, repo.Endpoint)
	refreshed := httptest.NewRequest(http.MethodPost, "/api/v2/repositories/"+repo.ID+"/cache/refresh", strings.NewReader(`{"path":"`+assetPath+`"}`))
	authorize(refreshed, testAuthenticator().AdminToken)
	refreshedResponse := httptest.NewRecorder()
	handler.ServeHTTP(refreshedResponse, refreshed)
	if refreshedResponse.Code != http.StatusOK || !strings.Contains(refreshedResponse.Body.String(), `"refreshed":true`) {
		t.Fatalf("refresh = %d %s", refreshedResponse.Code, refreshedResponse.Body.String())
	}
	cached, err = mavenCache.Load(ctx, mavenCache.Key(repo.Name, assetPath))
	if err != nil || string(cached.Body) != "fresh-jar" {
		t.Fatalf("new cache after refresh = %q err=%v", cached.Body, err)
	}
	if got := upstream.callCount("/" + assetPath); got != 3 {
		t.Fatalf("upstream calls=%d, want 3 including retry and successful refresh", got)
	}
}

func TestV2MavenProxyCacheRefreshAcceptsGAVPomPath(t *testing.T) {
	store := repository.NewMemoryStore()
	objectStore := NewMemoryOCIObjectStore()
	ctx := context.Background()
	pomPath := "org/example/widget/1.0.0/widget-1.0.0.pom"
	upstream := &proxyUpstream{bodies: map[string][]byte{"/" + pomPath: []byte("<project/>")}, calls: map[string]int{}}
	upstreamServer := httptest.NewServer(upstream)
	defer upstreamServer.Close()
	allowedHost := strings.TrimPrefix(upstreamServer.URL, "http://")
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "maven-proxy", Format: repository.FormatMaven, Type: repository.RepositoryTypeProxy, Endpoint: upstreamServer.URL, AllowedHosts: []string{allowedHost}})
	if err != nil {
		t.Fatal(err)
	}
	mavenCache := NewMavenCache(objectStore, time.Hour, time.Hour, time.Hour, time.Hour, []string{allowedHost})
	handler := NewGatewayHandlerWithFormatCaches(Dependencies{}, store, TestAdapter{}, testAuthenticator(), NewDefaultOCICache(objectStore, nil), mavenCache, nil, nil, NewCacheMaintenance(objectStore, NewDefaultOCICache(objectStore, nil)), UpstreamClient{HTTPClient: upstreamServer.Client()})

	request := httptest.NewRequest(http.MethodPost, "/api/v2/repositories/"+repo.ID+"/cache/refresh", strings.NewReader(`{"gav":"org.example:widget:1.0.0"}`))
	authorize(request, testAuthenticator().AdminToken)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"path":"`+pomPath+`"`) {
		t.Fatalf("refresh = %d %s", response.Code, response.Body.String())
	}
	if cached, err := mavenCache.Load(ctx, mavenCache.Key(repo.Name, pomPath)); err != nil || string(cached.Body) != "<project/>" {
		t.Fatalf("cached pom = %q err=%v", cached.Body, err)
	}
}

func TestV2MavenProxyHealthReportsReachabilityAndCircuitCacheStatus(t *testing.T) {
	store := repository.NewMemoryStore()
	objectStore := NewMemoryOCIObjectStore()
	ctx := context.Background()
	upstream := &proxyUpstream{bodies: map[string][]byte{"/": []byte("ok")}, calls: map[string]int{}}
	upstreamServer := httptest.NewServer(upstream)
	defer upstreamServer.Close()
	allowedHost := strings.TrimPrefix(upstreamServer.URL, "http://")
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "maven-proxy", Format: repository.FormatMaven, Type: repository.RepositoryTypeProxy, Endpoint: upstreamServer.URL, AllowedHosts: []string{allowedHost}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReplaceRepositoryGrants(ctx, repo.ID, []repository.RepositoryGrant{{Principal: "reader", Scopes: []string{"repositories:read"}}}, "1"); err != nil {
		t.Fatal(err)
	}
	mavenCache := NewMavenCache(objectStore, time.Hour, time.Hour, time.Hour, time.Hour, []string{allowedHost})
	mavenCache.RecordUpstreamFailure(ctx, repo.Endpoint)
	handler := NewGatewayHandlerWithFormatCaches(Dependencies{}, store, TestAdapter{}, testAuthenticator(), NewDefaultOCICache(objectStore, nil), mavenCache, nil, nil, NewCacheMaintenance(objectStore, NewDefaultOCICache(objectStore, nil)), UpstreamClient{HTTPClient: upstreamServer.Client()})

	request := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/"+repo.ID+"/proxy/health", nil)
	authorize(request, testAuthenticator().IssueToken("reader"))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("health = %d %s", response.Code, response.Body.String())
	}
	var health mavenProxyHealthResponse
	if err := json.Unmarshal(response.Body.Bytes(), &health); err != nil {
		t.Fatal(err)
	}
	if !health.Reachable || health.Status != http.StatusOK || !health.ProxyAllowed || !health.CircuitOpen || !health.CacheEnabled {
		t.Fatalf("health = %+v", health)
	}
	if got := upstream.callCount("/"); got != 1 {
		t.Fatalf("health upstream calls=%d, want 1", got)
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
