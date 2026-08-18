package app

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha1"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	adminopenapi "github.com/artifact-gateway/artifact-gateway/internal/admin/openapi"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

func TestNativeNPMHostedPublishInstallAndAnonymousBrowse(t *testing.T) {
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{
		ID: uuid.NewString(), Name: "npm-releases", Format: repository.FormatNPM, AnonymousRead: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	enablePublicationScan(t, store, repo.ID)
	if _, err = store.ReplaceAnonymousAccessPolicy(context.Background(), repository.AnonymousAccessPolicy{Enabled: true}, "1"); err != nil {
		t.Fatal(err)
	}
	objects := NewMemoryOCIObjectStore()
	handler := NewGatewayHandler(publicationScanDependencies(Dependencies{NativeNPMObjectStore: objects}, repository.FormatNPM), store, TestAdapter{}, testAuthenticator())
	tarball := npmFixtureTarball(t, "@scope/widget", "1.2.3")
	publishBody := npmFixturePublishDocument(t, "@scope/widget", "1.2.3", "@scope/widget-1.2.3.tgz", tarball)

	publish := httptest.NewRequest(http.MethodPut, "/npm/npm-releases/@scope%2Fwidget", strings.NewReader(publishBody))
	publish.Header.Set("Authorization", "Bearer resolver-secret")
	published := httptest.NewRecorder()
	handler.ServeHTTP(published, publish)
	if published.Code != http.StatusCreated {
		t.Fatalf("publish=%d %s", published.Code, published.Body.String())
	}
	requirePublicationScan(t, store, repo.ID, repository.FormatNPM, "@scope/widget@1.2.3", testScanDigest(string(tarball)))

	duplicate := httptest.NewRequest(http.MethodPut, "/npm/npm-releases/@scope%2Fwidget", strings.NewReader(publishBody))
	duplicate.Header.Set("Authorization", "Bearer resolver-secret")
	duplicateResult := httptest.NewRecorder()
	handler.ServeHTTP(duplicateResult, duplicate)
	if duplicateResult.Code != http.StatusConflict {
		t.Fatalf("duplicate=%d %s", duplicateResult.Code, duplicateResult.Body.String())
	}

	nextTarball := npmFixtureTarball(t, "@scope/widget", "2.0.0-beta.1")
	nextBody := npmFixturePublishDocumentWithTags(t, "@scope/widget", "2.0.0-beta.1", "@scope/widget-2.0.0-beta.1.tgz", nextTarball, map[string]string{"next": "2.0.0-beta.1"})
	nextPublish := httptest.NewRequest(http.MethodPut, "/npm/npm-releases/@scope%2Fwidget", strings.NewReader(nextBody))
	nextPublish.Header.Set("Authorization", "Bearer resolver-secret")
	nextPublished := httptest.NewRecorder()
	handler.ServeHTTP(nextPublished, nextPublish)
	if nextPublished.Code != http.StatusCreated {
		t.Fatalf("next publish=%d %s", nextPublished.Code, nextPublished.Body.String())
	}

	metadata := httptest.NewRecorder()
	handler.ServeHTTP(metadata, httptest.NewRequest(http.MethodGet, "/npm/npm-releases/@scope%2Fwidget", nil))
	if metadata.Code != http.StatusOK {
		t.Fatalf("metadata=%d %s", metadata.Code, metadata.Body.String())
	}
	var packument struct {
		DistTags map[string]string `json:"dist-tags"`
		Versions map[string]struct {
			Dist struct {
				Tarball   string `json:"tarball"`
				Integrity string `json:"integrity"`
				Shasum    string `json:"shasum"`
			} `json:"dist"`
			ArtifactGateway struct {
				Digest    string `json:"digest"`
				Publisher string `json:"publisher"`
				Size      int64  `json:"size"`
			} `json:"_artifactGateway"`
		} `json:"versions"`
	}
	if err = json.NewDecoder(metadata.Body).Decode(&packument); err != nil {
		t.Fatal(err)
	}
	version := packument.Versions["1.2.3"]
	if packument.DistTags["latest"] != "1.2.3" || packument.DistTags["next"] != "2.0.0-beta.1" || len(packument.Versions) != 2 || !strings.HasPrefix(version.Dist.Integrity, "sha512-") || len(version.Dist.Shasum) != 40 || !strings.HasSuffix(version.Dist.Tarball, "/npm/npm-releases/@scope/widget/-/widget-1.2.3.tgz") || !strings.HasPrefix(version.ArtifactGateway.Digest, "sha256:") || version.ArtifactGateway.Publisher != "build-agent" || version.ArtifactGateway.Size != int64(len(tarball)) {
		t.Fatalf("packument=%#v", packument)
	}
	versionMetadata := httptest.NewRecorder()
	handler.ServeHTTP(versionMetadata, httptest.NewRequest(http.MethodGet, "/npm/npm-releases/@scope%2Fwidget/1.2.3", nil))
	if versionMetadata.Code != http.StatusOK {
		t.Fatalf("version metadata=%d %s", versionMetadata.Code, versionMetadata.Body.String())
	}
	var hostedVersion struct {
		Name    string `json:"name"`
		Version string `json:"version"`
		Dist    struct {
			Tarball string `json:"tarball"`
		} `json:"dist"`
	}
	if err = json.NewDecoder(versionMetadata.Body).Decode(&hostedVersion); err != nil {
		t.Fatal(err)
	}
	if hostedVersion.Name != "@scope/widget" || hostedVersion.Version != "1.2.3" || !strings.HasSuffix(hostedVersion.Dist.Tarball, "/npm/npm-releases/@scope/widget/-/widget-1.2.3.tgz") {
		t.Fatalf("hosted version metadata=%#v", hostedVersion)
	}
	versionHead := httptest.NewRecorder()
	handler.ServeHTTP(versionHead, httptest.NewRequest(http.MethodHead, "/npm/npm-releases/@scope%2Fwidget/1.2.3", nil))
	if versionHead.Code != http.StatusOK || versionHead.Body.Len() != 0 || versionHead.Header().Get("Content-Length") == "" {
		t.Fatalf("version HEAD=%d bytes=%d headers=%v", versionHead.Code, versionHead.Body.Len(), versionHead.Header())
	}
	var headAudit *repository.AuditRecord
	for index := range store.Audits {
		if store.Audits[index].Resource == "@scope/widget@1.2.3" && store.Audits[index].Operation == "head" {
			headAudit = &store.Audits[index]
		}
	}
	if headAudit == nil || headAudit.Status != http.StatusOK || headAudit.Bytes != 0 {
		t.Fatalf("HEAD audit=%#v all=%#v", headAudit, store.Audits)
	}

	auditRequest := httptest.NewRequest(http.MethodPost, "/npm/npm-releases/-/npm/v1/security/advisories/bulk", strings.NewReader(`{}`))
	auditResult := httptest.NewRecorder()
	handler.ServeHTTP(auditResult, auditRequest)
	if auditResult.Code != http.StatusOK {
		t.Fatalf("anonymous audit=%d %s", auditResult.Code, auditResult.Body.String())
	}

	download := httptest.NewRecorder()
	handler.ServeHTTP(download, httptest.NewRequest(http.MethodGet, "/npm/npm-releases/@scope/widget/-/widget-1.2.3.tgz", nil))
	if download.Code != http.StatusOK || !bytes.Equal(download.Body.Bytes(), tarball) {
		t.Fatalf("download=%d bytes=%d", download.Code, download.Body.Len())
	}

	search := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/"+repo.ID+"/artifact-search?q=%40scope%2Fwidget", nil)
	search.Header.Set("Authorization", "Bearer admin-secret")
	searchResult := httptest.NewRecorder()
	handler.ServeHTTP(searchResult, search)
	if searchResult.Code != http.StatusOK {
		t.Fatalf("search=%d %s", searchResult.Code, searchResult.Body.String())
	}
	var page adminopenapi.ArtifactSummaryPage
	if err = json.NewDecoder(searchResult.Body).Decode(&page); err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].Coordinate != "@scope/widget" || page.Items[0].Version == nil || *page.Items[0].Version != "1.2.3" || page.Items[0].VersionCount == nil || *page.Items[0].VersionCount != 2 {
		t.Fatalf("search page=%#v", page)
	}
}

func TestNativeNPMHostedRejectsTarballIdentityMismatch(t *testing.T) {
	store := repository.NewMemoryStore()
	if _, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "npm-private", Format: repository.FormatNPM}); err != nil {
		t.Fatal(err)
	}
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())
	validTarball := npmFixtureTarball(t, "widget", "1.0.0")
	trailingJSON := httptest.NewRequest(http.MethodPut, "/npm/npm-private/widget", strings.NewReader(npmFixturePublishDocument(t, "widget", "1.0.0", "widget-1.0.0.tgz", validTarball)+` {}`))
	trailingJSON.Header.Set("Authorization", "Bearer resolver-secret")
	trailingResult := httptest.NewRecorder()
	handler.ServeHTTP(trailingResult, trailingJSON)
	if trailingResult.Code != http.StatusBadRequest {
		t.Fatalf("trailing json=%d %s", trailingResult.Code, trailingResult.Body.String())
	}

	tarball := npmFixtureTarball(t, "wrong-package", "1.0.0")
	publish := httptest.NewRequest(http.MethodPut, "/npm/npm-private/widget", strings.NewReader(npmFixturePublishDocument(t, "widget", "1.0.0", "widget-1.0.0.tgz", tarball)))
	publish.Header.Set("Authorization", "Bearer resolver-secret")
	result := httptest.NewRecorder()
	handler.ServeHTTP(result, publish)
	if result.Code != http.StatusUnprocessableEntity {
		t.Fatalf("publish=%d %s", result.Code, result.Body.String())
	}
}

func TestNativeNPMProxyCachesPackumentAndVerifiedTarball(t *testing.T) {
	packageName, version := "@scope/proxy-widget", "1.2.3"
	tarball := npmFixtureTarball(t, packageName, version)
	sha512Sum := sha512.Sum512(tarball)
	sha1Sum := sha1.Sum(tarball)
	integrity := "sha512-" + base64.StdEncoding.EncodeToString(sha512Sum[:])
	shasum := hex.EncodeToString(sha1Sum[:])
	var metadataRequests, tarballRequests atomic.Int64

	var upstream *httptest.Server
	upstream = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.EscapedPath() {
		case "/@scope%2Fproxy-widget":
			metadataRequests.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"name":      packageName,
				"dist-tags": map[string]string{"latest": version},
				"versions": map[string]any{version: map[string]any{
					"name": packageName, "version": version, "description": "proxied fixture",
					"dist": map[string]string{
						"tarball":   upstream.URL + "/tarballs/proxy-widget-1.2.3.tgz",
						"integrity": integrity,
						"shasum":    shasum,
					},
				}},
				"time": map[string]string{version: "2026-08-09T00:00:00Z"},
			})
		case "/tarballs/proxy-widget-1.2.3.tgz":
			tarballRequests.Add(1)
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = w.Write(tarball)
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	parsedUpstream, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{
		ID: uuid.NewString(), Name: "npm-proxy", Format: repository.FormatNPM,
		Type: repository.RepositoryTypeProxy, Endpoint: upstream.URL,
		AllowedHosts: []string{parsedUpstream.Hostname()}, AnonymousRead: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.ReplaceAnonymousAccessPolicy(context.Background(), repository.AnonymousAccessPolicy{Enabled: true}, "1"); err != nil {
		t.Fatal(err)
	}
	objects := NewMemoryOCIObjectStore()
	metrics := &Metrics{}
	handler := NewGatewayHandlerWithFormatCachesAndMetrics(
		Dependencies{NativeNPMObjectStore: objects}, store, TestAdapter{}, testAuthenticator(),
		NewDefaultOCICache(NewMemoryOCIObjectStore(), nil), nil, nil, NewConanCache(nil), nil, metrics,
		UpstreamClient{HTTPClient: upstream.Client()},
	)

	metadataPath := "/npm/" + repo.Name + "/@scope%2Fproxy-widget"
	metadata := httptest.NewRecorder()
	handler.ServeHTTP(metadata, httptest.NewRequest(http.MethodGet, metadataPath, nil))
	if metadata.Code != http.StatusOK {
		t.Fatalf("metadata=%d %s", metadata.Code, metadata.Body.String())
	}
	var packument struct {
		Versions map[string]struct {
			Dist struct {
				Tarball string `json:"tarball"`
			} `json:"dist"`
			ArtifactGateway struct {
				Source      string `json:"source"`
				CacheStatus string `json:"cacheStatus"`
				Digest      string `json:"digest"`
				Size        *int64 `json:"size"`
			} `json:"_artifactGateway"`
		} `json:"versions"`
	}
	if err = json.NewDecoder(metadata.Body).Decode(&packument); err != nil {
		t.Fatal(err)
	}
	localTarball := packument.Versions[version].Dist.Tarball
	metadataState := packument.Versions[version].ArtifactGateway
	if metadataState.Source != "proxy" || metadataState.CacheStatus != "metadata" || metadataState.Digest != "" || metadataState.Size != nil {
		t.Fatalf("metadata cache state = %#v", metadataState)
	}
	if !strings.HasSuffix(localTarball, "/npm/npm-proxy/@scope/proxy-widget/-/proxy-widget-1.2.3.tgz") {
		t.Fatalf("rewritten tarball=%q", localTarball)
	}

	secondMetadata := httptest.NewRecorder()
	handler.ServeHTTP(secondMetadata, httptest.NewRequest(http.MethodGet, metadataPath, nil))
	if secondMetadata.Code != http.StatusOK || metadataRequests.Load() != 1 {
		t.Fatalf("second metadata=%d upstream requests=%d", secondMetadata.Code, metadataRequests.Load())
	}

	tarballURL, err := url.Parse(localTarball)
	if err != nil {
		t.Fatal(err)
	}
	download := httptest.NewRecorder()
	handler.ServeHTTP(download, httptest.NewRequest(http.MethodGet, tarballURL.RequestURI(), nil))
	if download.Code != http.StatusOK || !bytes.Equal(download.Body.Bytes(), tarball) || tarballRequests.Load() != 1 {
		t.Fatalf("download=%d bytes=%d upstream requests=%d", download.Code, download.Body.Len(), tarballRequests.Load())
	}
	capacity, err := store.GetRepositoryCapacity(context.Background(), repo.ID)
	if err != nil || capacity.UsedBytes != int64(len(tarball)) || capacity.ObjectCount != 1 {
		t.Fatalf("capacity after tarball cache = %#v, %v", capacity, err)
	}
	cachedMetadata := httptest.NewRecorder()
	handler.ServeHTTP(cachedMetadata, httptest.NewRequest(http.MethodGet, metadataPath, nil))
	var cachedPackument struct {
		Versions map[string]struct {
			ArtifactGateway struct {
				CacheStatus string `json:"cacheStatus"`
				Digest      string `json:"digest"`
				Size        int64  `json:"size"`
				CachedAt    string `json:"cachedAt"`
			} `json:"_artifactGateway"`
		} `json:"versions"`
	}
	if json.NewDecoder(cachedMetadata.Body).Decode(&cachedPackument) != nil {
		t.Fatal("decode cached packument")
	}
	cachedState := cachedPackument.Versions[version].ArtifactGateway
	if cachedState.CacheStatus != "cached" || cachedState.Digest == "" || cachedState.Size != int64(len(tarball)) || cachedState.CachedAt == "" {
		t.Fatalf("cached state = %#v", cachedState)
	}

	upstream.Close()
	offline := httptest.NewRecorder()
	handler.ServeHTTP(offline, httptest.NewRequest(http.MethodGet, tarballURL.RequestURI(), nil))
	if offline.Code != http.StatusOK || !bytes.Equal(offline.Body.Bytes(), tarball) || tarballRequests.Load() != 1 {
		t.Fatalf("offline=%d bytes=%d upstream requests=%d", offline.Code, offline.Body.Len(), tarballRequests.Load())
	}
	metricResponse := httptest.NewRecorder()
	metrics.Handler(metricResponse, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	for _, expectedMetric := range []string{
		`artifact_gateway_npm_requests_total{method="get"} 5`,
		`artifact_gateway_npm_cache_requests_total{outcome="hit"} 3`,
		`artifact_gateway_npm_cache_requests_total{outcome="miss"} 2`,
		`artifact_gateway_npm_response_bytes_total`,
	} {
		if !strings.Contains(metricResponse.Body.String(), expectedMetric) {
			t.Fatalf("metrics missing %q\n%s", expectedMetric, metricResponse.Body.String())
		}
	}
	foundMiss, foundHit := false, false
	for _, audit := range store.Audits {
		if audit.Format != "npm" || audit.MemberType != "proxy" || audit.UpstreamHost != parsedUpstream.Hostname() {
			continue
		}
		foundMiss = foundMiss || audit.CacheDisposition == "miss"
		foundHit = foundHit || audit.CacheDisposition == "hit"
	}
	if !foundMiss || !foundHit {
		t.Fatalf("npm proxy audits missing hit/miss: %#v", store.Audits)
	}
}

func TestNativeNPMProxyColdVersionMetadataResolvesForCorepack(t *testing.T) {
	packageName, versionName := "pnpm", "10.7.1"
	tarball := npmFixtureTarball(t, packageName, versionName)
	sha512Sum := sha512.Sum512(tarball)
	sha1Sum := sha1.Sum(tarball)
	metadataRequests := 0
	var upstream *httptest.Server
	upstream = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/pnpm" {
			http.NotFound(w, r)
			return
		}
		metadataRequests++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"name": packageName, "dist-tags": map[string]string{"latest": versionName},
			"versions": map[string]any{versionName: map[string]any{
				"name": packageName, "version": versionName,
				"dist": map[string]string{
					"tarball":   upstream.URL + "/pnpm-10.7.1.tgz",
					"integrity": "sha512-" + base64.StdEncoding.EncodeToString(sha512Sum[:]),
					"shasum":    hex.EncodeToString(sha1Sum[:]),
				},
			}},
		})
	}))
	defer upstream.Close()
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{
		ID: "npm-corepack-direct", Name: "npm-corepack-direct", Format: repository.FormatNPM,
		Type: repository.RepositoryTypeProxy, Endpoint: upstream.URL, AllowedHosts: []string{"127.0.0.1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewGatewayHandler(
		Dependencies{NativeNPMObjectStore: NewMemoryOCIObjectStore()}, store, TestAdapter{}, testAuthenticator(),
		UpstreamClient{HTTPClient: upstream.Client()},
	)
	request := httptest.NewRequest(http.MethodGet, "/npm/"+repo.Name+"/pnpm/10.7.1", nil)
	authorize(request, "resolver-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("version metadata=%d body=%s", response.Code, response.Body.String())
	}
	var version struct {
		Name    string `json:"name"`
		Version string `json:"version"`
		Dist    struct {
			Tarball string `json:"tarball"`
		} `json:"dist"`
	}
	if err = json.NewDecoder(response.Body).Decode(&version); err != nil {
		t.Fatal(err)
	}
	if version.Name != packageName || version.Version != versionName || !strings.Contains(version.Dist.Tarball, "/npm/npm-corepack-direct/pnpm/-/pnpm-10.7.1.tgz") || metadataRequests != 1 {
		t.Fatalf("version metadata=%#v requests=%d", version, metadataRequests)
	}
}

func TestNativeNPMProxyColdTarballResolvesWithoutPackumentRequest(t *testing.T) {
	packageName, version := "lockfile-widget", "1.2.3"
	tarball := npmFixtureTarball(t, packageName, version)
	sha512Sum := sha512.Sum512(tarball)
	sha1Sum := sha1.Sum(tarball)
	var upstream *httptest.Server
	upstream = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/" + packageName:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"name": packageName, "dist-tags": map[string]string{"latest": version},
				"versions": map[string]any{version: map[string]any{
					"name": packageName, "version": version,
					"dist": map[string]string{
						"tarball":   upstream.URL + "/" + packageName + "/-/" + packageName + "-" + version + ".tgz",
						"integrity": "sha512-" + base64.StdEncoding.EncodeToString(sha512Sum[:]),
						"shasum":    hex.EncodeToString(sha1Sum[:]),
					},
				}},
			})
		case "/" + packageName + "/-/" + packageName + "-" + version + ".tgz":
			_, _ = w.Write(tarball)
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	parsedUpstream, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{
		ID: uuid.NewString(), Name: "npm-lockfile-proxy", Format: repository.FormatNPM,
		Type: repository.RepositoryTypeProxy, Endpoint: upstream.URL, AllowedHosts: []string{parsedUpstream.Hostname()},
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewGatewayHandler(
		Dependencies{NativeNPMObjectStore: NewMemoryOCIObjectStore()}, store, TestAdapter{}, testAuthenticator(),
		UpstreamClient{HTTPClient: upstream.Client()},
	)
	request := httptest.NewRequest(http.MethodGet, "/npm/"+repo.Name+"/"+packageName+"/-/"+packageName+"-"+version+".tgz", nil)
	authorize(request, "resolver-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !bytes.Equal(response.Body.Bytes(), tarball) {
		t.Fatalf("download=%d body=%s", response.Code, response.Body.String())
	}
}

func TestNativeNPMProxyKeepsValidVersionsWhenLegacyMetadataLacksIntegrity(t *testing.T) {
	const packageName, validVersion, legacyVersion = "color-convert", "2.0.1", "0.3.0"
	validTarball := npmFixtureTarball(t, packageName, validVersion)
	sha512Sum := sha512.Sum512(validTarball)
	sha1Sum := sha1.Sum(validTarball)
	var upstream *httptest.Server
	upstream = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/"+packageName {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"name":      packageName,
			"dist-tags": map[string]string{"latest": validVersion, "legacy": legacyVersion},
			"versions": map[string]any{
				legacyVersion: map[string]any{
					"name": packageName, "version": legacyVersion,
					"dist": map[string]string{
						"tarball": upstream.URL + "/" + packageName + "-" + legacyVersion + ".tgz",
						"shasum":  strings.Repeat("a", 40),
					},
				},
				validVersion: map[string]any{
					"name": packageName, "version": validVersion,
					"dist": map[string]string{
						"tarball":   upstream.URL + "/" + packageName + "-" + validVersion + ".tgz",
						"integrity": "sha512-" + base64.StdEncoding.EncodeToString(sha512Sum[:]),
						"shasum":    hex.EncodeToString(sha1Sum[:]),
					},
				},
			},
		})
	}))
	defer upstream.Close()
	parsedUpstream, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{
		ID: uuid.NewString(), Name: "npm-legacy-metadata-proxy", Format: repository.FormatNPM,
		Type: repository.RepositoryTypeProxy, Endpoint: upstream.URL, AllowedHosts: []string{parsedUpstream.Hostname()},
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator(), UpstreamClient{HTTPClient: upstream.Client()})
	versionRequest := httptest.NewRequest(http.MethodGet, "/npm/"+repo.Name+"/"+packageName+"/"+validVersion, nil)
	authorize(versionRequest, "resolver-secret")
	versionResponse := httptest.NewRecorder()
	handler.ServeHTTP(versionResponse, versionRequest)
	if versionResponse.Code != http.StatusOK {
		t.Fatalf("version metadata=%d %s", versionResponse.Code, versionResponse.Body.String())
	}
	packumentRequest := httptest.NewRequest(http.MethodGet, "/npm/"+repo.Name+"/"+packageName, nil)
	authorize(packumentRequest, "resolver-secret")
	packumentResponse := httptest.NewRecorder()
	handler.ServeHTTP(packumentResponse, packumentRequest)
	var packument struct {
		DistTags map[string]string          `json:"dist-tags"`
		Versions map[string]json.RawMessage `json:"versions"`
	}
	if packumentResponse.Code != http.StatusOK || json.NewDecoder(packumentResponse.Body).Decode(&packument) != nil {
		t.Fatalf("packument=%d %s", packumentResponse.Code, packumentResponse.Body.String())
	}
	if _, ok := packument.Versions[validVersion]; !ok || len(packument.Versions) != 1 {
		t.Fatalf("versions=%#v", packument.Versions)
	}
	if packument.DistTags["latest"] != validVersion {
		t.Fatalf("dist-tags=%#v", packument.DistTags)
	}
	if _, ok := packument.DistTags["legacy"]; ok {
		t.Fatalf("invalid legacy dist-tag remained: %#v", packument.DistTags)
	}
}

func TestNativeNPMProxyRequestsBoundedInstallMetadata(t *testing.T) {
	const packageName, version = "large-install-metadata", "1.0.0"
	tarball := npmFixtureTarball(t, packageName, version)
	sha512Sum := sha512.Sum512(tarball)
	sha1Sum := sha1.Sum(tarball)
	upstreamAccept := ""
	var upstream *httptest.Server
	upstream = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamAccept = r.Header.Get("Accept")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"name": packageName, "dist-tags": map[string]string{"latest": version},
			"versions": map[string]any{version: map[string]any{
				"name": packageName, "version": version,
				"dist": map[string]string{
					"tarball":   upstream.URL + "/" + packageName + "-" + version + ".tgz",
					"integrity": "sha512-" + base64.StdEncoding.EncodeToString(sha512Sum[:]),
					"shasum":    hex.EncodeToString(sha1Sum[:]),
				},
			}},
		})
	}))
	defer upstream.Close()
	parsedUpstream, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{
		ID: uuid.NewString(), Name: "npm-install-metadata-proxy", Format: repository.FormatNPM,
		Type: repository.RepositoryTypeProxy, Endpoint: upstream.URL, AllowedHosts: []string{parsedUpstream.Hostname()},
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator(), UpstreamClient{HTTPClient: upstream.Client()})
	request := httptest.NewRequest(http.MethodGet, "/npm/"+repo.Name+"/"+packageName+"/"+version, nil)
	authorize(request, "resolver-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("version metadata=%d %s", response.Code, response.Body.String())
	}
	if upstreamAccept != "application/vnd.npm.install-v1+json" {
		t.Fatalf("upstream Accept=%q", upstreamAccept)
	}
}

func TestNPMProxyPackumentLimitCoversLargePublicInstallMetadata(t *testing.T) {
	if npmPackumentLimit < 64<<20 {
		t.Fatalf("npmPackumentLimit=%d, want at least 64 MiB", npmPackumentLimit)
	}
}

func TestNativeNPMProxyColdScopedLegacyRootTarballResolvesWithoutPackumentRequest(t *testing.T) {
	packageName, version := "@types/json-schema", "7.0.15"
	tarballName := "json-schema-" + version + ".tgz"
	tarball := npmFixtureTarballWithRoot(t, "json-schema", packageName, version)
	sha512Sum := sha512.Sum512(tarball)
	sha1Sum := sha1.Sum(tarball)
	var upstream *httptest.Server
	upstream = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/" + packageName:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"name": packageName, "dist-tags": map[string]string{"latest": version},
				"versions": map[string]any{version: map[string]any{
					"name": packageName, "version": version,
					"dist": map[string]string{
						"tarball":   upstream.URL + "/" + packageName + "/-/" + tarballName,
						"integrity": "sha512-" + base64.StdEncoding.EncodeToString(sha512Sum[:]),
						"shasum":    hex.EncodeToString(sha1Sum[:]),
					},
				}},
			})
		case "/" + packageName + "/-/" + tarballName:
			_, _ = w.Write(tarball)
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	parsedUpstream, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{
		ID: uuid.NewString(), Name: "npm-scoped-lockfile-proxy", Format: repository.FormatNPM,
		Type: repository.RepositoryTypeProxy, Endpoint: upstream.URL, AllowedHosts: []string{parsedUpstream.Hostname()},
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewGatewayHandler(
		Dependencies{NativeNPMObjectStore: NewMemoryOCIObjectStore()}, store, TestAdapter{}, testAuthenticator(),
		UpstreamClient{HTTPClient: upstream.Client()},
	)
	request := httptest.NewRequest(http.MethodGet, "/npm/"+repo.Name+"/"+packageName+"/-/"+tarballName, nil)
	authorize(request, "resolver-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !bytes.Equal(response.Body.Bytes(), tarball) {
		t.Fatalf("download=%d body=%s", response.Code, response.Body.String())
	}
}

func TestNativeNPMProxyColdTarballMetadataFailuresAuditExactlyOnce(t *testing.T) {
	const packageName, version = "lockfile-audit-widget", "1.2.3"
	tarballName := packageName + "-" + version + ".tgz"
	checksum := sha512.Sum512([]byte("fixture"))
	tests := []struct {
		name        string
		upstream    http.HandlerFunc
		wantStatus  int
		wantOutcome repository.AuditOutcome
	}{
		{
			name: "first upstream not found",
			upstream: func(w http.ResponseWriter, _ *http.Request) {
				http.NotFound(w, nil)
			},
			wantStatus:  http.StatusNotFound,
			wantOutcome: repository.AuditNotFound,
		},
		{
			name: "tarball host rejected by allowlist",
			upstream: func(w http.ResponseWriter, _ *http.Request) {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"name": packageName, "dist-tags": map[string]string{"latest": version},
					"versions": map[string]any{version: map[string]any{
						"name": packageName, "version": version,
						"dist": map[string]string{
							"tarball":   "https://blocked.example/" + tarballName,
							"integrity": "sha512-" + base64.StdEncoding.EncodeToString(checksum[:]),
							"shasum":    strings.Repeat("a", 40),
						},
					}},
				})
			},
			wantStatus:  http.StatusBadGateway,
			wantOutcome: repository.AuditProxyDenied,
		},
		{
			name: "metadata upstream unavailable",
			upstream: func(w http.ResponseWriter, _ *http.Request) {
				http.Error(w, "unavailable", http.StatusInternalServerError)
			},
			wantStatus:  http.StatusBadGateway,
			wantOutcome: repository.AuditUpstreamError,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			upstream := httptest.NewServer(tt.upstream)
			defer upstream.Close()
			parsed, err := url.Parse(upstream.URL)
			if err != nil {
				t.Fatal(err)
			}
			store := repository.NewMemoryStore()
			repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{
				ID: uuid.NewString(), Name: "npm-cold-audit", Format: repository.FormatNPM,
				Type: repository.RepositoryTypeProxy, Endpoint: upstream.URL, AllowedHosts: []string{parsed.Hostname()},
			})
			if err != nil {
				t.Fatal(err)
			}
			handler := NewGatewayHandler(
				Dependencies{NativeNPMObjectStore: NewMemoryOCIObjectStore()}, store, TestAdapter{}, testAuthenticator(),
				UpstreamClient{HTTPClient: upstream.Client()},
			)
			request := httptest.NewRequest(http.MethodGet, "/npm/"+repo.Name+"/"+packageName+"/-/"+tarballName, nil)
			authorize(request, "resolver-secret")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != tt.wantStatus {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			if len(store.Audits) != 1 {
				t.Fatalf("audits=%#v", store.Audits)
			}
			audit := store.Audits[0]
			if audit.GroupName != repo.Name || audit.Repository != repo.Name || audit.MemberName != "" ||
				audit.Resource != packageName+"/-/"+tarballName || audit.Status != tt.wantStatus ||
				audit.Outcome != tt.wantOutcome || audit.CacheDisposition != "miss" {
				t.Fatalf("audit=%#v", audit)
			}
		})
	}
}

func TestNativeNPMProxyNegativeCachesAndServesStaleMetadata(t *testing.T) {
	const version = "1.0.0"
	tarball := npmFixtureTarball(t, "stale-widget", version)
	sha512Sum := sha512.Sum512(tarball)
	sha1Sum := sha1.Sum(tarball)
	var requests atomic.Int64
	var upstreamFailing atomic.Bool
	var upstream *httptest.Server
	upstream = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if upstreamFailing.Load() {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
			return
		}
		if r.URL.EscapedPath() == "/missing-widget" {
			http.NotFound(w, r)
			return
		}
		if r.URL.EscapedPath() != "/stale-widget" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"name": "stale-widget", "dist-tags": map[string]string{"latest": version},
			"versions": map[string]any{version: map[string]any{
				"name": "stale-widget", "version": version,
				"dist": map[string]string{
					"tarball":   upstream.URL + "/stale-widget-1.0.0.tgz",
					"integrity": "sha512-" + base64.StdEncoding.EncodeToString(sha512Sum[:]),
					"shasum":    hex.EncodeToString(sha1Sum[:]),
				},
			}},
		})
	}))
	defer upstream.Close()
	parsed, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{
		ID: uuid.NewString(), Name: "npm-stale", Format: repository.FormatNPM,
		Type: repository.RepositoryTypeProxy, Endpoint: upstream.URL,
		AllowedHosts: []string{parsed.Hostname()}, AnonymousRead: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.ReplaceAnonymousAccessPolicy(context.Background(), repository.AnonymousAccessPolicy{Enabled: true}, "1"); err != nil {
		t.Fatal(err)
	}
	handler := NewGatewayHandler(
		Dependencies{
			NativeNPMObjectStore: NewMemoryOCIObjectStore(),
			NPMMetadataTTL:       10 * time.Millisecond,
			NPMNegativeTTL:       time.Minute,
		},
		store, TestAdapter{}, testAuthenticator(), UpstreamClient{HTTPClient: upstream.Client()},
	)

	for range 2 {
		result := httptest.NewRecorder()
		handler.ServeHTTP(result, httptest.NewRequest(http.MethodGet, "/npm/"+repo.Name+"/missing-widget", nil))
		if result.Code != http.StatusNotFound {
			t.Fatalf("missing=%d %s", result.Code, result.Body.String())
		}
	}
	if requests.Load() != 1 {
		t.Fatalf("negative cache upstream requests=%d", requests.Load())
	}

	fresh := httptest.NewRecorder()
	handler.ServeHTTP(fresh, httptest.NewRequest(http.MethodGet, "/npm/"+repo.Name+"/stale-widget", nil))
	if fresh.Code != http.StatusOK {
		t.Fatalf("fresh=%d %s", fresh.Code, fresh.Body.String())
	}
	time.Sleep(15 * time.Millisecond)
	upstreamFailing.Store(true)
	stale := httptest.NewRecorder()
	handler.ServeHTTP(stale, httptest.NewRequest(http.MethodGet, "/npm/"+repo.Name+"/stale-widget", nil))
	if stale.Code != http.StatusOK || stale.Header().Get("Warning") == "" {
		t.Fatalf("stale=%d warning=%q body=%s", stale.Code, stale.Header().Get("Warning"), stale.Body.String())
	}
}

func TestNativeNPMProxyRetriesAndOpensUpstreamCircuit(t *testing.T) {
	var requests atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer upstream.Close()
	parsed, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{
		ID: uuid.NewString(), Name: "npm-protected", Format: repository.FormatNPM,
		Type: repository.RepositoryTypeProxy, Endpoint: upstream.URL,
		AllowedHosts: []string{parsed.Hostname()}, AnonymousRead: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.ReplaceAnonymousAccessPolicy(context.Background(), repository.AnonymousAccessPolicy{Enabled: true}, "1"); err != nil {
		t.Fatal(err)
	}
	handler := NewGatewayHandler(
		Dependencies{NativeNPMObjectStore: NewMemoryOCIObjectStore()},
		store, TestAdapter{}, testAuthenticator(), UpstreamClient{HTTPClient: upstream.Client()},
	)
	path := "/npm/" + repo.Name + "/unavailable-widget"
	first := httptest.NewRecorder()
	handler.ServeHTTP(first, httptest.NewRequest(http.MethodGet, path, nil))
	if first.Code != http.StatusBadGateway || requests.Load() != 2 {
		t.Fatalf("first=%d upstream requests=%d", first.Code, requests.Load())
	}
	second := httptest.NewRecorder()
	handler.ServeHTTP(second, httptest.NewRequest(http.MethodGet, path, nil))
	if second.Code != http.StatusServiceUnavailable || requests.Load() != 2 {
		t.Fatalf("second=%d upstream requests=%d", second.Code, requests.Load())
	}
}

func TestNPMProxyPackageRejectsDisallowedTarballHost(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		checksum := sha512.Sum512([]byte("fixture"))
		_ = json.NewEncoder(w).Encode(map[string]any{
			"name": "widget", "dist-tags": map[string]string{"latest": "1.0.0"},
			"versions": map[string]any{"1.0.0": map[string]any{
				"name": "widget", "version": "1.0.0",
				"dist": map[string]string{
					"tarball":   "https://attacker.example/widget.tgz",
					"integrity": "sha512-" + base64.StdEncoding.EncodeToString(checksum[:]),
					"shasum":    strings.Repeat("a", 40),
				},
			}},
		})
	}))
	defer upstream.Close()
	parsed, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{
		ID: uuid.NewString(), Name: "npm-disallowed-tarball", Format: repository.FormatNPM,
		Type: repository.RepositoryTypeProxy, Endpoint: upstream.URL,
		AllowedHosts: []string{parsed.Hostname()},
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewGatewayHandler(
		Dependencies{NativeNPMObjectStore: NewMemoryOCIObjectStore()}, store, TestAdapter{}, testAuthenticator(),
		UpstreamClient{HTTPClient: upstream.Client()},
	)
	request := httptest.NewRequest(http.MethodGet, "/npm/"+repo.Name+"/widget", nil)
	request.Header.Set("Authorization", "Bearer resolver-secret")
	result := httptest.NewRecorder()
	handler.ServeHTTP(result, request)
	if result.Code != http.StatusBadGateway || !strings.Contains(result.Body.String(), "not allowed") {
		t.Fatalf("status=%d body=%s", result.Code, result.Body.String())
	}
}

func TestNativeNPMProxyRevalidatesExpiredMetadata(t *testing.T) {
	const packageName = "revalidated-widget"
	const version = "1.0.0"
	tarball := npmFixtureTarball(t, packageName, version)
	sha512Sum := sha512.Sum512(tarball)
	sha1Sum := sha1.Sum(tarball)
	var requests, conditional atomic.Int64
	var upstream *httptest.Server
	upstream = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Header.Get("If-None-Match") == `"fixture-v1"` {
			conditional.Add(1)
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", `"fixture-v1"`)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"name": packageName, "dist-tags": map[string]string{"latest": version},
			"versions": map[string]any{version: map[string]any{
				"name": packageName, "version": version,
				"dist": map[string]string{
					"tarball":   upstream.URL + "/revalidated-widget-1.0.0.tgz",
					"integrity": "sha512-" + base64.StdEncoding.EncodeToString(sha512Sum[:]),
					"shasum":    hex.EncodeToString(sha1Sum[:]),
				},
			}},
		})
	}))
	defer upstream.Close()
	parsed, _ := url.Parse(upstream.URL)
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{
		ID: uuid.NewString(), Name: "npm-revalidate", Format: repository.FormatNPM,
		Type: repository.RepositoryTypeProxy, Endpoint: upstream.URL,
		AllowedHosts: []string{parsed.Hostname()}, AnonymousRead: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = store.ReplaceAnonymousAccessPolicy(context.Background(), repository.AnonymousAccessPolicy{Enabled: true}, "1")
	handler := NewGatewayHandler(
		Dependencies{NativeNPMObjectStore: NewMemoryOCIObjectStore(), NPMMetadataTTL: time.Millisecond},
		store, TestAdapter{}, testAuthenticator(), UpstreamClient{HTTPClient: upstream.Client()},
	)
	requestPath := "/npm/" + repo.Name + "/" + packageName
	for range 2 {
		result := httptest.NewRecorder()
		handler.ServeHTTP(result, httptest.NewRequest(http.MethodGet, requestPath, nil))
		if result.Code != http.StatusOK {
			t.Fatalf("packument=%d %s", result.Code, result.Body.String())
		}
		time.Sleep(2 * time.Millisecond)
	}
	if requests.Load() != 2 || conditional.Load() != 1 {
		t.Fatalf("requests=%d conditional=%d", requests.Load(), conditional.Load())
	}
}

func TestNativeNPMProxyDoesNotCacheIntegrityMismatch(t *testing.T) {
	const packageName = "integrity-widget"
	const version = "1.0.0"
	expected := npmFixtureTarball(t, packageName, version)
	corrupted := append([]byte(nil), expected...)
	corrupted[len(corrupted)-1] ^= 0xff
	sha512Sum := sha512.Sum512(expected)
	sha1Sum := sha1.Sum(expected)
	var tarballRequests atomic.Int64
	var upstream *httptest.Server
	upstream = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/integrity-widget-1.0.0.tgz" {
			tarballRequests.Add(1)
			_, _ = w.Write(corrupted)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"name": packageName, "dist-tags": map[string]string{"latest": version},
			"versions": map[string]any{version: map[string]any{
				"name": packageName, "version": version,
				"dist": map[string]string{
					"tarball":   upstream.URL + "/integrity-widget-1.0.0.tgz",
					"integrity": "sha512-" + base64.StdEncoding.EncodeToString(sha512Sum[:]),
					"shasum":    hex.EncodeToString(sha1Sum[:]),
				},
			}},
		})
	}))
	defer upstream.Close()
	parsed, _ := url.Parse(upstream.URL)
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{
		ID: uuid.NewString(), Name: "npm-integrity", Format: repository.FormatNPM,
		Type: repository.RepositoryTypeProxy, Endpoint: upstream.URL,
		AllowedHosts: []string{parsed.Hostname()}, AnonymousRead: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = store.ReplaceAnonymousAccessPolicy(context.Background(), repository.AnonymousAccessPolicy{Enabled: true}, "1")
	handler := NewGatewayHandler(Dependencies{NativeNPMObjectStore: NewMemoryOCIObjectStore()}, store, TestAdapter{}, testAuthenticator(), UpstreamClient{HTTPClient: upstream.Client()})
	metadata := httptest.NewRecorder()
	handler.ServeHTTP(metadata, httptest.NewRequest(http.MethodGet, "/npm/"+repo.Name+"/"+packageName, nil))
	if metadata.Code != http.StatusOK {
		t.Fatalf("metadata=%d %s", metadata.Code, metadata.Body.String())
	}
	requestPath := "/npm/" + repo.Name + "/" + packageName + "/-/integrity-widget-1.0.0.tgz"
	for range 2 {
		result := httptest.NewRecorder()
		handler.ServeHTTP(result, httptest.NewRequest(http.MethodGet, requestPath, nil))
		if result.Code != http.StatusBadGateway {
			t.Fatalf("tarball=%d %s", result.Code, result.Body.String())
		}
	}
	capacity, err := store.GetRepositoryCapacity(context.Background(), repo.ID)
	if err != nil || capacity.UsedBytes != 0 || capacity.ObjectCount != 0 || tarballRequests.Load() != 2 {
		t.Fatalf("capacity=%#v requests=%d err=%v", capacity, tarballRequests.Load(), err)
	}
}

func npmFixturePublishDocument(t *testing.T, name, version, tarballName string, tarball []byte) string {
	return npmFixturePublishDocumentWithTags(t, name, version, tarballName, tarball, map[string]string{"latest": version})
}

func npmFixturePublishDocumentWithTags(t *testing.T, name, version, tarballName string, tarball []byte, tags map[string]string) string {
	t.Helper()
	document := map[string]any{
		"_id":       name,
		"name":      name,
		"dist-tags": tags,
		"versions": map[string]any{version: map[string]any{
			"name": name, "version": version,
			"dist": map[string]string{"tarball": "http://registry.invalid/" + tarballName},
		}},
		"_attachments": map[string]any{tarballName: map[string]any{
			"content_type": "application/octet-stream", "length": len(tarball),
			"data": base64.StdEncoding.EncodeToString(tarball),
		}},
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func npmFixtureTarball(t *testing.T, name, version string) []byte {
	return npmFixtureTarballWithRoot(t, "package", name, version)
}

func npmFixtureTarballWithRoot(t *testing.T, root, name, version string) []byte {
	t.Helper()
	manifest, err := json.Marshal(map[string]string{"name": name, "version": version})
	if err != nil {
		t.Fatal(err)
	}
	var compressed bytes.Buffer
	gzipWriter := gzip.NewWriter(&compressed)
	tarWriter := tar.NewWriter(gzipWriter)
	if err = tarWriter.WriteHeader(&tar.Header{Name: root + "/package.json", Mode: 0o644, Size: int64(len(manifest))}); err != nil {
		t.Fatal(err)
	}
	if _, err = tarWriter.Write(manifest); err != nil {
		t.Fatal(err)
	}
	if err = tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err = gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	return compressed.Bytes()
}
