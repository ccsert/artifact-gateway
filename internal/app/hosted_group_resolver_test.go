package app

import (
	"bytes"
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

// conanGatewayClient adapts the Conan-only fixture client to the OCIClient
// shape the gateway constructor expects; the other formats are unused.
type conanGatewayClient struct{ *conanFixtureClient }

func (conanGatewayClient) Fetch(context.Context, string, repository.Member, string, string, string, http.Header) (*http.Response, error) {
	return nil, errors.New("OCI fetch is not used by Conan group tests")
}

func TestV2GroupNPMMergesHostedAndProxyVersions(t *testing.T) {
	store := repository.NewMemoryStore()
	objects := NewMemoryOCIObjectStore()
	hosted, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{
		ID: "npm-hosted", Name: "npm-hosted", Format: repository.FormatNPM,
	})
	if err != nil {
		t.Fatal(err)
	}
	hostedTarball := npmFixtureTarball(t, "widget", "1.0.0")
	hostedSHA256 := sha256.Sum256(hostedTarball)
	hostedSHA512 := sha512.Sum512(hostedTarball)
	hostedSHA1 := sha1.Sum(hostedTarball)
	hostedDigest := "sha256:" + hex.EncodeToString(hostedSHA256[:])
	hostedObjectKey := "native/npm/sha256/" + hex.EncodeToString(hostedSHA256[:])
	if err = objects.Put(context.Background(), hostedObjectKey, hostedTarball); err != nil {
		t.Fatal(err)
	}
	hostedManifest := json.RawMessage(`{"name":"widget","version":"1.0.0","description":"hosted version"}`)
	if _, err = store.PublishNPMVersion(context.Background(), repository.NPMVersion{
		RepositoryID: hosted.ID, PackageName: "widget", Version: "1.0.0",
		Digest: hostedDigest, Integrity: "sha512-" + base64.StdEncoding.EncodeToString(hostedSHA512[:]),
		Shasum: hex.EncodeToString(hostedSHA1[:]), TarballName: "widget-1.0.0.tgz",
		ObjectKey: hostedObjectKey, Size: int64(len(hostedTarball)), Manifest: hostedManifest, Publisher: "internal-build",
	}, map[string]string{"latest": "1.0.0"}); err != nil {
		t.Fatal(err)
	}

	proxyTarball := npmFixtureTarball(t, "widget", "2.0.0")
	proxySHA512 := sha512.Sum512(proxyTarball)
	proxySHA1 := sha1.Sum(proxyTarball)
	var metadataRequests, tarballRequests int
	var upstream *httptest.Server
	upstream = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/widget":
			metadataRequests++
			_ = json.NewEncoder(w).Encode(map[string]any{
				"name": "widget", "dist-tags": map[string]string{"latest": "2.0.0"},
				"versions": map[string]any{
					"1.0.0": map[string]any{
						"name": "widget", "version": "1.0.0", "description": "lower-priority proxy conflict",
						"dist": map[string]string{
							"tarball": upstream.URL + "/widget-1.0.0.tgz", "integrity": "sha512-" + base64.StdEncoding.EncodeToString(hostedSHA512[:]), "shasum": hex.EncodeToString(hostedSHA1[:]),
						},
					},
					"2.0.0": map[string]any{
						"name": "widget", "version": "2.0.0", "description": "proxy version",
						"dist": map[string]string{
							"tarball": upstream.URL + "/widget-2.0.0.tgz", "integrity": "sha512-" + base64.StdEncoding.EncodeToString(proxySHA512[:]), "shasum": hex.EncodeToString(proxySHA1[:]),
						},
					},
				},
			})
		case "/widget-2.0.0.tgz":
			tarballRequests++
			_, _ = w.Write(proxyTarball)
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	proxy, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{
		ID: "npm-proxy", Name: "npm-proxy", Format: repository.FormatNPM,
		Type: repository.RepositoryTypeProxy, Endpoint: upstream.URL, AllowedHosts: []string{"127.0.0.1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	createV2Group(t, store, "npm-group", repository.FormatNPM,
		repository.GroupMember{RepositoryID: proxy.ID, Position: 0},
		repository.GroupMember{RepositoryID: hosted.ID, Position: 1},
	)
	handler := NewGatewayHandler(
		Dependencies{NativeNPMObjectStore: objects}, store, TestAdapter{}, testAuthenticator(),
		UpstreamClient{HTTPClient: upstream.Client()},
	)

	metadataRequest := httptest.NewRequest(http.MethodGet, "/npm/npm-group/widget", nil)
	authorize(metadataRequest, "resolver-secret")
	metadata := httptest.NewRecorder()
	handler.ServeHTTP(metadata, metadataRequest)
	if metadata.Code != http.StatusOK {
		t.Fatalf("metadata=%d body=%s", metadata.Code, metadata.Body.String())
	}
	var packument struct {
		DistTags map[string]string `json:"dist-tags"`
		Versions map[string]struct {
			Description string `json:"description"`
			Dist        struct {
				Tarball string `json:"tarball"`
			} `json:"dist"`
		} `json:"versions"`
	}
	if err = json.NewDecoder(metadata.Body).Decode(&packument); err != nil {
		t.Fatal(err)
	}
	if len(packument.Versions) != 2 || packument.DistTags["latest"] != "1.0.0" {
		t.Fatalf("packument=%#v", packument)
	}
	if packument.Versions["1.0.0"].Description != "hosted version" {
		t.Fatalf("hosted conflict did not win: %#v", packument.Versions["1.0.0"])
	}
	tarballPaths := make(map[string]string)
	for version, expectedBody := range map[string][]byte{"1.0.0": hostedTarball, "2.0.0": proxyTarball} {
		tarballURL := packument.Versions[version].Dist.Tarball
		if !strings.Contains(tarballURL, "/npm/npm-group/widget/-/") {
			t.Fatalf("version %s tarball URL=%q", version, tarballURL)
		}
		path := strings.TrimPrefix(tarballURL, "http://example.com")
		tarballPaths[version] = path
		request := httptest.NewRequest(http.MethodGet, path, nil)
		authorize(request, "resolver-secret")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK || !bytes.Equal(response.Body.Bytes(), expectedBody) {
			t.Fatalf("version %s download=%d bytes=%d", version, response.Code, response.Body.Len())
		}
	}
	hitRequest := httptest.NewRequest(http.MethodGet, tarballPaths["2.0.0"], nil)
	authorize(hitRequest, "resolver-secret")
	hitResponse := httptest.NewRecorder()
	handler.ServeHTTP(hitResponse, hitRequest)
	if hitResponse.Code != http.StatusOK || !bytes.Equal(hitResponse.Body.Bytes(), proxyTarball) {
		t.Fatalf("proxy cache hit=%d bytes=%d", hitResponse.Code, hitResponse.Body.Len())
	}
	conditionalRequest := httptest.NewRequest(http.MethodGet, tarballPaths["1.0.0"], nil)
	conditionalRequest.Header.Set("If-None-Match", `"sha256-`+strings.TrimPrefix(hostedDigest, "sha256:")+`"`)
	authorize(conditionalRequest, "resolver-secret")
	conditionalResponse := httptest.NewRecorder()
	handler.ServeHTTP(conditionalResponse, conditionalRequest)
	if conditionalResponse.Code != http.StatusNotModified || conditionalResponse.Body.Len() != 0 {
		t.Fatalf("hosted conditional=%d bytes=%d", conditionalResponse.Code, conditionalResponse.Body.Len())
	}
	if metadataRequests != 1 || tarballRequests != 1 {
		t.Fatalf("upstream metadata=%d tarball=%d", metadataRequests, tarballRequests)
	}
	auditedDownloads := make(map[string]int)
	downloadCount := 0
	for _, audit := range store.Audits {
		if !strings.HasPrefix(audit.Resource, "widget@") || audit.Outcome != repository.AuditResolved {
			continue
		}
		downloadCount++
		if audit.GroupName != "npm-group" || audit.Repository != "npm-group" || audit.MemberName == "" {
			t.Fatalf("tarball audit escaped group ownership: %#v", audit)
		}
		key := audit.MemberName + ":" + audit.CacheDisposition + ":" + utoa(uint64(audit.Status))
		auditedDownloads[key]++
	}
	if downloadCount != 4 || auditedDownloads[hosted.Name+":bypass:200"] != 1 || auditedDownloads[proxy.Name+":miss:200"] != 1 || auditedDownloads[proxy.Name+":hit:200"] != 1 || auditedDownloads[hosted.Name+":bypass:304"] != 1 {
		t.Fatalf("group tarball audits=%#v count=%d all=%#v", auditedDownloads, downloadCount, store.Audits)
	}
}

func TestV2GroupNPMColdTarballResolvesThroughProxyWithoutPackumentRequest(t *testing.T) {
	store := repository.NewMemoryStore()
	objects := NewMemoryOCIObjectStore()
	hosted, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{
		ID: "npm-empty-hosted", Name: "npm-empty-hosted", Format: repository.FormatNPM,
	})
	if err != nil {
		t.Fatal(err)
	}

	packageName, version := "yocto-queue", "0.1.0"
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
	proxy, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{
		ID: "npm-real-shape-proxy", Name: "npm-real-shape-proxy", Format: repository.FormatNPM,
		Type: repository.RepositoryTypeProxy, Endpoint: upstream.URL, AllowedHosts: []string{"127.0.0.1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	createV2Group(t, store, "npm-real-shape-group", repository.FormatNPM,
		repository.GroupMember{RepositoryID: hosted.ID, Position: 0},
		repository.GroupMember{RepositoryID: proxy.ID, Position: 1},
	)
	handler := NewGatewayHandler(
		Dependencies{NativeNPMObjectStore: objects}, store, TestAdapter{}, testAuthenticator(),
		UpstreamClient{HTTPClient: upstream.Client()},
	)

	request := httptest.NewRequest(http.MethodGet, "/npm/npm-real-shape-group/"+packageName+"/-/"+packageName+"-"+version+".tgz", nil)
	authorize(request, "resolver-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !bytes.Equal(response.Body.Bytes(), tarball) {
		t.Fatalf("download=%d body=%s", response.Code, response.Body.String())
	}
}

func TestV2GroupNPMColdTarballMetadataFailuresAuditMemberExactlyOnce(t *testing.T) {
	const packageName, version = "group-lockfile-audit-widget", "1.2.3"
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
			proxy, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{
				ID: uuid.NewString(), Name: "npm-group-cold-audit-proxy", Format: repository.FormatNPM,
				Type: repository.RepositoryTypeProxy, Endpoint: upstream.URL, AllowedHosts: []string{parsed.Hostname()},
			})
			if err != nil {
				t.Fatal(err)
			}
			group := createV2Group(t, store, "npm-group-cold-audit", repository.FormatNPM,
				repository.GroupMember{RepositoryID: proxy.ID},
			)
			handler := NewGatewayHandler(
				Dependencies{NativeNPMObjectStore: NewMemoryOCIObjectStore()}, store, TestAdapter{}, testAuthenticator(),
				UpstreamClient{HTTPClient: upstream.Client()},
			)
			request := httptest.NewRequest(http.MethodGet, "/npm/"+group.Name+"/"+packageName+"/-/"+tarballName, nil)
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
			if audit.GroupName != group.Name || audit.Repository != group.Name || audit.MemberName != proxy.Name ||
				audit.Resource != packageName+"/-/"+tarballName || audit.Status != tt.wantStatus ||
				audit.Outcome != tt.wantOutcome || audit.CacheDisposition != "miss" {
				t.Fatalf("audit=%#v", audit)
			}
		})
	}
}

func TestV2GroupNPMAnonymousReadExcludesPrivateMembers(t *testing.T) {
	store := repository.NewMemoryStore()
	public, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{
		ID: "npm-public", Name: "npm-public", Format: repository.FormatNPM, AnonymousRead: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	private, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{
		ID: "npm-private", Name: "npm-private", Format: repository.FormatNPM,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, fixture := range []struct {
		repo    repository.HostedRepository
		version string
	}{{public, "1.0.0"}, {private, "9.0.0"}} {
		publishNPMGroupTestVersion(t, store, fixture.repo, "widget", fixture.version)
	}
	group := createV2Group(t, store, "npm-public-group", repository.FormatNPM,
		repository.GroupMember{RepositoryID: public.ID, Position: 0},
		repository.GroupMember{RepositoryID: private.ID, Position: 1},
	)
	group.AnonymousRead = true
	if _, err = store.ReplaceHostedGroup(context.Background(), group, group.Version); err != nil {
		t.Fatal(err)
	}
	enableAnonymousAccess(t, store)
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/npm/npm-public-group/widget", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("anonymous packument=%d body=%s", response.Code, response.Body.String())
	}
	var packument struct {
		Versions map[string]json.RawMessage `json:"versions"`
	}
	if err = json.NewDecoder(response.Body).Decode(&packument); err != nil {
		t.Fatal(err)
	}
	if len(packument.Versions) != 1 || packument.Versions["1.0.0"] == nil || packument.Versions["9.0.0"] != nil {
		t.Fatalf("anonymous member filtering leaked versions: %#v", packument.Versions)
	}
}

func TestV2GroupNPMFiltersMembersByManagedGrants(t *testing.T) {
	store := repository.NewMemoryStore()
	denied, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{
		ID: "npm-denied", Name: "npm-denied", Format: repository.FormatNPM,
	})
	if err != nil {
		t.Fatal(err)
	}
	allowed, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{
		ID: "npm-allowed", Name: "npm-allowed", Format: repository.FormatNPM,
	})
	if err != nil {
		t.Fatal(err)
	}
	publishNPMGroupTestVersion(t, store, denied, "widget", "9.0.0")
	publishNPMGroupTestVersion(t, store, allowed, "widget", "1.0.0")
	if _, err = store.ReplaceRepositoryGrants(context.Background(), denied.ID, nil, "1"); err != nil {
		t.Fatal(err)
	}
	if _, err = store.ReplaceRepositoryGrants(context.Background(), allowed.ID, []repository.RepositoryGrant{{
		Principal: "build-agent", Scopes: []string{"repositories:read"},
	}}, "1"); err != nil {
		t.Fatal(err)
	}
	createV2Group(t, store, "npm-authorized-group", repository.FormatNPM,
		repository.GroupMember{RepositoryID: denied.ID, Position: 0},
		repository.GroupMember{RepositoryID: allowed.ID, Position: 1},
	)
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())
	request := httptest.NewRequest(http.MethodGet, "/npm/npm-authorized-group/widget", nil)
	authorize(request, "resolver-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("authorized packument=%d body=%s", response.Code, response.Body.String())
	}
	var packument struct {
		Versions map[string]json.RawMessage `json:"versions"`
	}
	if err = json.NewDecoder(response.Body).Decode(&packument); err != nil {
		t.Fatal(err)
	}
	if len(packument.Versions) != 1 || packument.Versions["1.0.0"] == nil || packument.Versions["9.0.0"] != nil {
		t.Fatalf("managed grant filtering leaked versions: %#v", packument.Versions)
	}
	for _, audit := range store.Audits {
		if audit.GroupName == "npm-authorized-group" && audit.Repository == "npm-authorized-group" && audit.MemberName == denied.Name && audit.Outcome == repository.AuditAccessDenied && audit.AuthorizationSource == "repository_grants" && audit.AuthorizationReason == "scope_not_granted" {
			return
		}
	}
	t.Fatalf("missing managed denial audit: %#v", store.Audits)
}

func TestV2GroupNPMFiltersDefaultGrantMembersByStaticPolicy(t *testing.T) {
	store := repository.NewMemoryStore()
	denied, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{
		ID: "npm-static-denied", Name: "npm-static-denied", Format: repository.FormatNPM,
	})
	if err != nil {
		t.Fatal(err)
	}
	allowed, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{
		ID: "npm-static-allowed", Name: "npm-static-allowed", Format: repository.FormatNPM,
	})
	if err != nil {
		t.Fatal(err)
	}
	publishNPMGroupTestVersion(t, store, denied, "widget", "9.0.0")
	publishNPMGroupTestVersion(t, store, allowed, "widget", "1.0.0")
	createV2Group(t, store, "npm-static-group", repository.FormatNPM,
		repository.GroupMember{RepositoryID: denied.ID, Position: 0},
		repository.GroupMember{RepositoryID: allowed.ID, Position: 1},
	)
	authenticator := testAuthenticator()
	authenticator.RepositoryReaders = map[string][]string{"build-agent": {allowed.Name}}
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, authenticator)
	request := httptest.NewRequest(http.MethodGet, "/npm/npm-static-group/widget", nil)
	authorize(request, "resolver-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("static-policy packument=%d body=%s", response.Code, response.Body.String())
	}
	var packument struct {
		Versions map[string]json.RawMessage `json:"versions"`
	}
	if err = json.NewDecoder(response.Body).Decode(&packument); err != nil {
		t.Fatal(err)
	}
	if len(packument.Versions) != 1 || packument.Versions["1.0.0"] == nil || packument.Versions["9.0.0"] != nil {
		t.Fatalf("static repository policy leaked versions: %#v", packument.Versions)
	}
	for _, audit := range store.Audits {
		if audit.GroupName == "npm-static-group" && audit.Repository == "npm-static-group" && audit.MemberName == denied.Name && audit.Outcome == repository.AuditAccessDenied && audit.AuthorizationSource == "legacy_static" && audit.AuthorizationReason == "scope_not_granted" {
			return
		}
	}
	t.Fatalf("missing static-policy denial audit: %#v", store.Audits)
}

func TestV2GroupNPMAllMembersDeniedAuditsGroup(t *testing.T) {
	store := repository.NewMemoryStore()
	denied, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{
		ID: "npm-all-denied", Name: "npm-all-denied", Format: repository.FormatNPM,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.ReplaceRepositoryGrants(context.Background(), denied.ID, nil, "1"); err != nil {
		t.Fatal(err)
	}
	createV2Group(t, store, "npm-denied-group", repository.FormatNPM, repository.GroupMember{RepositoryID: denied.ID})
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())
	request := httptest.NewRequest(http.MethodGet, "/npm/npm-denied-group/widget", nil)
	authorize(request, "resolver-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("all members denied=%d body=%s", response.Code, response.Body.String())
	}
	for _, audit := range store.Audits {
		if audit.GroupName == "npm-denied-group" && audit.Repository == "npm-denied-group" && audit.MemberName == denied.Name && audit.Outcome == repository.AuditAccessDenied {
			return
		}
	}
	t.Fatalf("missing group denial audit: %#v", store.Audits)
}

func TestV2GroupNPMProxyMetadataFailureAuditsMemberAndGroup(t *testing.T) {
	store := repository.NewMemoryStore()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "upstream unavailable", http.StatusInternalServerError)
	}))
	defer upstream.Close()
	proxy, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{
		ID: "npm-failing-proxy", Name: "npm-failing-proxy", Format: repository.FormatNPM,
		Type: repository.RepositoryTypeProxy, Endpoint: upstream.URL, AllowedHosts: []string{"127.0.0.1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	createV2Group(t, store, "npm-failing-group", repository.FormatNPM, repository.GroupMember{RepositoryID: proxy.ID})
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator(), UpstreamClient{HTTPClient: upstream.Client()})
	request := httptest.NewRequest(http.MethodGet, "/npm/npm-failing-group/widget", nil)
	authorize(request, "resolver-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadGateway {
		t.Fatalf("proxy metadata failure=%d body=%s", response.Code, response.Body.String())
	}
	memberAudit, terminalAudit := false, false
	for _, audit := range store.Audits {
		if audit.GroupName != "npm-failing-group" || audit.Repository != "npm-failing-group" || audit.Outcome != repository.AuditUpstreamError || audit.Status != http.StatusBadGateway {
			continue
		}
		memberAudit = memberAudit || audit.MemberName == proxy.Name
		terminalAudit = terminalAudit || audit.MemberName == ""
	}
	if !memberAudit || !terminalAudit {
		t.Fatalf("missing group failure audits: %#v", store.Audits)
	}
}

func TestV2GroupNPMHostedTarballStorageFailureAuditsGroup(t *testing.T) {
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{
		ID: "npm-missing-object", Name: "npm-missing-object", Format: repository.FormatNPM,
	})
	if err != nil {
		t.Fatal(err)
	}
	publishNPMGroupTestVersion(t, store, repo, "widget", "1.0.0")
	createV2Group(t, store, "npm-storage-group", repository.FormatNPM, repository.GroupMember{RepositoryID: repo.ID})
	handler := NewGatewayHandler(Dependencies{NativeNPMObjectStore: NewMemoryOCIObjectStore()}, store, TestAdapter{}, testAuthenticator())
	request := httptest.NewRequest(http.MethodGet, "/npm/npm-storage-group/widget/-/widget-1.0.0.tgz", nil)
	authorize(request, "resolver-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("hosted tarball storage failure=%d body=%s", response.Code, response.Body.String())
	}
	for _, audit := range store.Audits {
		if audit.GroupName == "npm-storage-group" && audit.Repository == "npm-storage-group" && audit.MemberName == repo.Name && audit.Outcome == repository.AuditStorageError && audit.Status == http.StatusInternalServerError {
			return
		}
	}
	t.Fatalf("missing group storage audit: %#v", store.Audits)
}

func TestV2GroupNPMRepositoryNameTakesPrecedence(t *testing.T) {
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{
		ID: "npm-shared-repository", Name: "npm-shared", Format: repository.FormatNPM,
	})
	if err != nil {
		t.Fatal(err)
	}
	member, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{
		ID: "npm-shadowed-member", Name: "npm-shadowed-member", Format: repository.FormatNPM,
	})
	if err != nil {
		t.Fatal(err)
	}
	publishNPMGroupTestVersion(t, store, repo, "widget", "1.0.0")
	publishNPMGroupTestVersion(t, store, member, "widget", "9.0.0")
	createV2Group(t, store, repo.Name, repository.FormatNPM, repository.GroupMember{RepositoryID: member.ID, Position: 0})
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())
	request := httptest.NewRequest(http.MethodGet, "/npm/npm-shared/widget", nil)
	authorize(request, "resolver-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("repository precedence=%d body=%s", response.Code, response.Body.String())
	}
	var packument struct {
		Versions map[string]json.RawMessage `json:"versions"`
	}
	if err = json.NewDecoder(response.Body).Decode(&packument); err != nil {
		t.Fatal(err)
	}
	if len(packument.Versions) != 1 || packument.Versions["1.0.0"] == nil || packument.Versions["9.0.0"] != nil {
		t.Fatalf("group shadowed repository: %#v", packument.Versions)
	}
}

func publishNPMGroupTestVersion(t *testing.T, store *repository.MemoryStore, repo repository.HostedRepository, packageName, version string) {
	t.Helper()
	manifest := json.RawMessage(`{"name":"` + packageName + `","version":"` + version + `"}`)
	if _, err := store.PublishNPMVersion(context.Background(), repository.NPMVersion{
		RepositoryID: repo.ID, PackageName: packageName, Version: version,
		Digest: "sha256:" + strings.Repeat("a", 64), Integrity: "sha512-" + base64.StdEncoding.EncodeToString(make([]byte, sha512.Size)),
		Shasum: strings.Repeat("b", 40), TarballName: packageName + "-" + version + ".tgz",
		ObjectKey: "native/npm/" + repo.ID, Manifest: manifest, Publisher: repo.Name,
	}, map[string]string{"latest": version}); err != nil {
		t.Fatal(err)
	}
}

// createV2Group wires a V2 HostedGroup with the given members in the store.
func createV2Group(t *testing.T, store *repository.MemoryStore, name string, format repository.Format, members ...repository.GroupMember) repository.HostedGroup {
	t.Helper()
	group, _, err := store.CreateHostedGroupIdempotently(context.Background(), repository.HostedGroup{ID: "group-" + name, Name: name, Format: format, Members: members}, "test", "key-"+name, "payload")
	if err != nil {
		t.Fatal(err)
	}
	return group
}

func TestV2GroupOCIManifestHostedPreferredOverProxy(t *testing.T) {
	store := repository.NewMemoryStore()
	objects := NewMemoryOCIObjectStore()
	hosted, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: "oci-hosted", Name: "oci-hosted", Format: repository.FormatOCI})
	if err != nil {
		t.Fatal(err)
	}
	upstream := &proxyUpstream{bodies: map[string][]byte{}, calls: map[string]int{}}
	upstreamServer := httptest.NewServer(upstream)
	defer upstreamServer.Close()
	allowedHost := strings.TrimPrefix(upstreamServer.URL, "http://")
	proxy, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{
		ID: "oci-proxy", Name: "oci-proxy", Format: repository.FormatOCI,
		Type: repository.RepositoryTypeProxy, Endpoint: upstreamServer.URL, AllowedHosts: []string{allowedHost},
	})
	if err != nil {
		t.Fatal(err)
	}
	createV2Group(t, store, "oci-group", repository.FormatOCI,
		repository.GroupMember{RepositoryID: proxy.ID, Position: 0},
		repository.GroupMember{RepositoryID: hosted.ID, Position: 1},
	)

	manifest := []byte(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json"}`)
	sum := sha256.Sum256(manifest)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	manifestKey := "native/oci/manifests/oci-hosted/app/" + hex.EncodeToString(sum[:])
	if err := objects.Put(context.Background(), manifestKey, manifest); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PutOCIManifest(context.Background(), repository.OCIManifest{RepositoryID: hosted.ID, Name: "app", Digest: digest, ObjectKey: manifestKey, MediaType: "application/vnd.oci.image.manifest.v1+json", Size: int64(len(manifest))}, "latest"); err != nil {
		t.Fatal(err)
	}
	upstream.bodies["/v2/app/manifests/latest"] = []byte(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json","from":"upstream"}`)

	cache := NewOCICache(NewMemoryOCIObjectStore(), time.Hour, time.Hour, time.Hour, []string{allowedHost})
	handler := NewGatewayHandlerWithOCICache(Dependencies{NativeOCIObjectStore: objects}, store, TestAdapter{}, testAuthenticator(), cache, UpstreamClient{HTTPClient: upstreamServer.Client()})

	req := httptest.NewRequest(http.MethodGet, "/v2/oci-group/app/manifests/latest", nil)
	authorize(req, "resolver-secret")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK || w.Body.String() != string(manifest) {
		t.Fatalf("hosted read=%d body=%q", w.Code, w.Body.String())
	}
	if got := upstream.callCount("/v2/app/manifests/latest"); got != 0 {
		t.Fatalf("upstream calls=%d, want 0 (hosted member preferred)", got)
	}
}

func TestV2GroupOCIManifestFallsBackToProxy(t *testing.T) {
	store := repository.NewMemoryStore()
	objects := NewMemoryOCIObjectStore()
	hosted, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: "oci-hosted", Name: "oci-hosted", Format: repository.FormatOCI})
	if err != nil {
		t.Fatal(err)
	}
	upstream := &proxyUpstream{bodies: map[string][]byte{}, calls: map[string]int{}}
	upstreamServer := httptest.NewServer(upstream)
	defer upstreamServer.Close()
	allowedHost := strings.TrimPrefix(upstreamServer.URL, "http://")
	proxy, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{
		ID: "oci-proxy", Name: "oci-proxy", Format: repository.FormatOCI,
		Type: repository.RepositoryTypeProxy, Endpoint: upstreamServer.URL, AllowedHosts: []string{allowedHost},
	})
	if err != nil {
		t.Fatal(err)
	}
	createV2Group(t, store, "oci-group", repository.FormatOCI,
		repository.GroupMember{RepositoryID: hosted.ID, Position: 0},
		repository.GroupMember{RepositoryID: proxy.ID, Position: 1},
	)

	manifest := []byte(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json","from":"upstream"}`)
	upstream.bodies["/v2/app/manifests/latest"] = manifest

	cache := NewOCICache(NewMemoryOCIObjectStore(), time.Hour, time.Hour, time.Hour, []string{allowedHost})
	handler := NewGatewayHandlerWithOCICache(Dependencies{NativeOCIObjectStore: objects}, store, TestAdapter{}, testAuthenticator(), cache, UpstreamClient{HTTPClient: upstreamServer.Client()})

	get := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/v2/oci-group/app/manifests/latest", nil)
		authorize(req, "resolver-secret")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		return w
	}
	first := get()
	if first.Code != http.StatusOK || first.Body.String() != string(manifest) {
		t.Fatalf("proxy read=%d body=%q", first.Code, first.Body.String())
	}
	if got := upstream.callCount("/v2/app/manifests/latest"); got != 1 {
		t.Fatalf("upstream calls=%d, want 1 (hosted miss falls back to proxy)", got)
	}
	second := get()
	if second.Code != http.StatusOK || second.Body.String() != string(manifest) {
		t.Fatalf("cached read=%d body=%q", second.Code, second.Body.String())
	}
	if got := upstream.callCount("/v2/app/manifests/latest"); got != 1 {
		t.Fatalf("upstream calls after cache=%d, want 1 (cache hit)", got)
	}
}

func TestV2GroupOCIAnonymousTagListAggregatesAnonymousMembers(t *testing.T) {
	store := repository.NewMemoryStore()
	objects := NewMemoryOCIObjectStore()
	public, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: "oci-public", Name: "oci-public", Format: repository.FormatOCI, AnonymousRead: true})
	if err != nil {
		t.Fatal(err)
	}
	private, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: "oci-private", Name: "oci-private", Format: repository.FormatOCI})
	if err != nil {
		t.Fatal(err)
	}
	for _, fixture := range []struct {
		repositoryID string
		tag          string
	}{
		{public.ID, "1.0.0"},
		{public.ID, "latest"},
		{private.ID, "private"},
	} {
		body := []byte(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json"}`)
		sum := sha256.Sum256(append(body, fixture.tag...))
		digest := "sha256:" + hex.EncodeToString(sum[:])
		key := "native/oci/manifests/" + fixture.repositoryID + "/app/" + hex.EncodeToString(sum[:])
		if err := objects.Put(context.Background(), key, body); err != nil {
			t.Fatal(err)
		}
		if _, err := store.PutOCIManifest(context.Background(), repository.OCIManifest{RepositoryID: fixture.repositoryID, Name: "app", Digest: digest, ObjectKey: key, MediaType: "application/vnd.oci.image.manifest.v1+json", Size: int64(len(body))}, fixture.tag); err != nil {
			t.Fatal(err)
		}
	}
	group, _, err := store.CreateHostedGroupIdempotently(context.Background(), repository.HostedGroup{
		ID: "group-public", Name: "public-group", Format: repository.FormatOCI, AnonymousRead: true,
		Members: []repository.GroupMember{{RepositoryID: public.ID, Position: 0}, {RepositoryID: private.ID, Position: 1}},
	}, "test", "public-group", "public-group")
	if err != nil {
		t.Fatal(err)
	}
	enableAnonymousAccess(t, store)
	handler := NewGatewayHandler(Dependencies{NativeOCIObjectStore: objects}, store, TestAdapter{}, testAuthenticator())

	request := httptest.NewRequest(http.MethodGet, "/v2/"+group.Name+"/app/tags/list?n=50", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"1.0.0"`) || !strings.Contains(response.Body.String(), `"latest"`) {
		t.Fatalf("anonymous group tags=%d body=%s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), `"private"`) {
		t.Fatalf("private member tag leaked into anonymous group response: %s", response.Body.String())
	}
}

func TestV2GroupMavenHostedPreferredOverProxy(t *testing.T) {
	store := repository.NewMemoryStore()
	objects := NewMemoryOCIObjectStore()
	hosted, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: "maven-hosted", Name: "maven-hosted", Format: repository.FormatMaven})
	if err != nil {
		t.Fatal(err)
	}
	assetPath := "org/example/widget/1.0.0/widget-1.0.0.jar"
	asset := []byte("hosted-jar")
	sum := sha256.Sum256(asset)
	key := "native/maven/sha256/" + hex.EncodeToString(sum[:])
	if err := objects.Put(context.Background(), key, asset); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateMavenPublishSession(context.Background(), repository.MavenPublishSession{ID: "session", RepositoryID: hosted.ID, Coordinate: "org.example:widget:1.0.0", State: "open", Objects: []repository.MavenDeclaredObject{{Name: "widget-1.0.0.jar", Digest: "sha256:" + hex.EncodeToString(sum[:]), Size: int64(len(asset))}}, ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkMavenPublishObject(context.Background(), "session", "widget-1.0.0.jar", key); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CommitMavenPublishSession(context.Background(), "session", []repository.MavenAsset{{RepositoryID: hosted.ID, Path: assetPath, ObjectKey: key, Digest: "sha256:" + hex.EncodeToString(sum[:]), Size: int64(len(asset))}}); err != nil {
		t.Fatal(err)
	}

	upstream := &proxyUpstream{bodies: map[string][]byte{"/" + assetPath: []byte("upstream-jar")}, calls: map[string]int{}}
	upstreamServer := httptest.NewServer(upstream)
	defer upstreamServer.Close()
	allowedHost := strings.TrimPrefix(upstreamServer.URL, "http://")
	proxy, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{
		ID: "maven-proxy", Name: "maven-proxy", Format: repository.FormatMaven,
		Type: repository.RepositoryTypeProxy, Endpoint: upstreamServer.URL, AllowedHosts: []string{allowedHost},
	})
	if err != nil {
		t.Fatal(err)
	}
	createV2Group(t, store, "maven-group", repository.FormatMaven,
		repository.GroupMember{RepositoryID: proxy.ID, Position: 0},
		repository.GroupMember{RepositoryID: hosted.ID, Position: 1},
	)

	mavenCache := NewMavenCache(NewMemoryOCIObjectStore(), time.Hour, time.Hour, time.Hour, time.Hour, []string{allowedHost})
	handler := NewGatewayHandlerWithCaches(Dependencies{NativeMavenObjectStore: objects}, store, TestAdapter{}, testAuthenticator(), NewDefaultOCICache(NewMemoryOCIObjectStore(), nil), mavenCache, UpstreamClient{HTTPClient: upstreamServer.Client()})

	req := httptest.NewRequest(http.MethodGet, "/maven/maven-group/"+assetPath, nil)
	authorize(req, "resolver-secret")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK || w.Body.String() != "hosted-jar" {
		t.Fatalf("hosted read=%d body=%q", w.Code, w.Body.String())
	}
	if got := upstream.callCount("/" + assetPath); got != 0 {
		t.Fatalf("upstream calls=%d, want 0 (hosted member preferred)", got)
	}
}

func TestV2GroupMavenFallsBackToProxy(t *testing.T) {
	store := repository.NewMemoryStore()
	objects := NewMemoryOCIObjectStore()
	hosted, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: "maven-hosted", Name: "maven-hosted", Format: repository.FormatMaven})
	if err != nil {
		t.Fatal(err)
	}
	assetPath := "org/example/widget/1.0.0/widget-1.0.0.jar"
	upstream := &proxyUpstream{bodies: map[string][]byte{"/" + assetPath: []byte("upstream-jar")}, calls: map[string]int{}}
	upstreamServer := httptest.NewServer(upstream)
	defer upstreamServer.Close()
	allowedHost := strings.TrimPrefix(upstreamServer.URL, "http://")
	proxy, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{
		ID: "maven-proxy", Name: "maven-proxy", Format: repository.FormatMaven,
		Type: repository.RepositoryTypeProxy, Endpoint: upstreamServer.URL, AllowedHosts: []string{allowedHost},
	})
	if err != nil {
		t.Fatal(err)
	}
	createV2Group(t, store, "maven-group", repository.FormatMaven,
		repository.GroupMember{RepositoryID: hosted.ID, Position: 0},
		repository.GroupMember{RepositoryID: proxy.ID, Position: 1},
	)

	mavenCache := NewMavenCache(NewMemoryOCIObjectStore(), time.Hour, time.Hour, time.Hour, time.Hour, []string{allowedHost})
	handler := NewGatewayHandlerWithCaches(Dependencies{NativeMavenObjectStore: objects}, store, TestAdapter{}, testAuthenticator(), NewDefaultOCICache(NewMemoryOCIObjectStore(), nil), mavenCache, UpstreamClient{HTTPClient: upstreamServer.Client()})

	get := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/maven/maven-group/"+assetPath, nil)
		authorize(req, "resolver-secret")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		return w
	}
	first := get()
	if first.Code != http.StatusOK || first.Body.String() != "upstream-jar" {
		t.Fatalf("proxy read=%d body=%q", first.Code, first.Body.String())
	}
	if got := upstream.callCount("/" + assetPath); got != 1 {
		t.Fatalf("upstream calls=%d, want 1 (hosted miss falls back to proxy)", got)
	}
	second := get()
	if second.Code != http.StatusOK || second.Body.String() != "upstream-jar" {
		t.Fatalf("cached read=%d body=%q", second.Code, second.Body.String())
	}
	if got := upstream.callCount("/" + assetPath); got != 1 {
		t.Fatalf("upstream calls after cache=%d, want 1 (cache hit)", got)
	}
}

func TestV2GroupRawHostedPreferredOverProxy(t *testing.T) {
	store := repository.NewMemoryStore()
	objects := NewMemoryOCIObjectStore()
	hosted, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: "raw-hosted", Name: "raw-hosted", Format: repository.FormatRaw})
	if err != nil {
		t.Fatal(err)
	}
	body := []byte("hosted-raw")
	sum := sha256.Sum256(body)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	objectKey := "native/raw/sha256/" + hex.EncodeToString(sum[:])
	if err := objects.Put(context.Background(), objectKey, body); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PutRawAsset(context.Background(), repository.RawAsset{RepositoryID: hosted.ID, Path: "release/app.txt", Digest: digest, ObjectKey: objectKey, Size: int64(len(body)), ContentType: "text/plain"}); err != nil {
		t.Fatal(err)
	}
	proxy, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{
		ID: "raw-proxy", Name: "raw-proxy", Format: repository.FormatRaw,
		Type: repository.RepositoryTypeProxy, Endpoint: "https://proxy.example", AllowedHosts: []string{"proxy.example"},
	})
	if err != nil {
		t.Fatal(err)
	}
	createV2Group(t, store, "raw-group", repository.FormatRaw,
		repository.GroupMember{RepositoryID: proxy.ID, Position: 0},
		repository.GroupMember{RepositoryID: hosted.ID, Position: 1},
	)

	client := &rawFixtureClient{responses: map[string]int{"raw-proxy": http.StatusOK}, body: []byte("upstream-raw")}
	rawCache := NewRawCache(NewMemoryOCIObjectStore(), time.Hour, time.Hour, []string{"proxy.example"})
	handler := NewGatewayHandlerWithRawCache(Dependencies{NativeOCIObjectStore: objects}, store, TestAdapter{}, testAuthenticator(), NewDefaultOCICache(NewMemoryOCIObjectStore(), nil), nil, rawCache, nil, client)

	req := httptest.NewRequest(http.MethodGet, "/raw/raw-group/release/app.txt", nil)
	authorize(req, "resolver-secret")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK || w.Body.String() != "hosted-raw" {
		t.Fatalf("hosted read=%d body=%q", w.Code, w.Body.String())
	}
	if got := client.Calls(); len(got) != 0 {
		t.Fatalf("upstream calls=%v, want none (hosted member preferred)", got)
	}
}

func TestV2GroupRawFallsBackToProxy(t *testing.T) {
	store := repository.NewMemoryStore()
	objects := NewMemoryOCIObjectStore()
	hosted, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: "raw-hosted", Name: "raw-hosted", Format: repository.FormatRaw})
	if err != nil {
		t.Fatal(err)
	}
	proxy, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{
		ID: "raw-proxy", Name: "raw-proxy", Format: repository.FormatRaw,
		Type: repository.RepositoryTypeProxy, Endpoint: "https://proxy.example", AllowedHosts: []string{"proxy.example"},
	})
	if err != nil {
		t.Fatal(err)
	}
	createV2Group(t, store, "raw-group", repository.FormatRaw,
		repository.GroupMember{RepositoryID: hosted.ID, Position: 0},
		repository.GroupMember{RepositoryID: proxy.ID, Position: 1},
	)

	client := &rawFixtureClient{responses: map[string]int{"raw-proxy": http.StatusOK}, body: []byte("upstream-raw")}
	rawCache := NewRawCache(NewMemoryOCIObjectStore(), time.Hour, time.Hour, []string{"proxy.example"})
	handler := NewGatewayHandlerWithRawCache(Dependencies{NativeOCIObjectStore: objects}, store, TestAdapter{}, testAuthenticator(), NewDefaultOCICache(NewMemoryOCIObjectStore(), nil), nil, rawCache, nil, client)

	get := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/raw/raw-group/release/app.txt", nil)
		authorize(req, "resolver-secret")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		return w
	}
	first := get()
	if first.Code != http.StatusOK || first.Body.String() != "upstream-raw" {
		t.Fatalf("proxy read=%d body=%q", first.Code, first.Body.String())
	}
	if got := client.Calls(); len(got) != 1 || got[0] != "raw-proxy" {
		t.Fatalf("upstream calls=%v, want a single fetch through the proxy member", got)
	}
	second := get()
	if second.Code != http.StatusOK || second.Body.String() != "upstream-raw" {
		t.Fatalf("cached read=%d body=%q", second.Code, second.Body.String())
	}
	if got := client.Calls(); len(got) != 1 {
		t.Fatalf("upstream calls after cache=%v, want 1 (cache hit)", got)
	}
}

func TestV2GroupConanHostedPreferredOverProxy(t *testing.T) {
	store := repository.NewMemoryStore()
	objects := NewMemoryOCIObjectStore()
	hosted, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: "conan-hosted", Name: "conan-hosted", Format: repository.FormatConan})
	if err != nil {
		t.Fatal(err)
	}
	body := []byte("recipe")
	sum := sha256.Sum256(body)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	objectKey := "native/conan/objects/recipe"
	if err := store.StageConanObject(context.Background(), repository.ConanObjectIntent{RepositoryID: hosted.ID, ObjectKey: objectKey, Digest: digest, Size: int64(len(body))}); err != nil {
		t.Fatal(err)
	}
	if err := objects.PutVerifiedReader(context.Background(), objectKey, bytes.NewReader(body), int64(len(body)), digest); err != nil {
		t.Fatal(err)
	}
	reference := "pkg/1.0/user/stable"
	if _, err := store.PutConanRecipeRevision(context.Background(), repository.ConanRecipeRevision{RepositoryID: hosted.ID, Reference: reference, Revision: "rrev", Digest: digest}, []repository.ConanAsset{{RepositoryID: hosted.ID, Reference: reference, RecipeRevision: "rrev", Path: "conanfile.py", ObjectKey: objectKey, Digest: digest, Size: int64(len(body))}}); err != nil {
		t.Fatal(err)
	}

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"revisions":[{"revision":"upstream","time":"2024-01-01T00:00:00Z"}]}`))
	}))
	defer upstream.Close()
	proxy, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{
		ID: "conan-proxy", Name: "conan-proxy", Format: repository.FormatConan,
		Type: repository.RepositoryTypeProxy, Endpoint: "https://proxy.example", AllowedHosts: []string{"proxy.example"},
	})
	if err != nil {
		t.Fatal(err)
	}
	createV2Group(t, store, "conan-group", repository.FormatConan,
		repository.GroupMember{RepositoryID: proxy.ID, Position: 0},
		repository.GroupMember{RepositoryID: hosted.ID, Position: 1},
	)

	client := &conanFixtureClient{proxyURL: upstream.URL}
	handler := NewGatewayHandlerWithFormatCaches(Dependencies{NativeConanObjectStore: objects}, store, TestAdapter{}, testAuthenticator(), NewDefaultOCICache(NewMemoryOCIObjectStore(), nil), nil, nil, NewConanCache(nil), nil, conanGatewayClient{client})

	req := httptest.NewRequest(http.MethodGet, "/conan/v2/conan-group/conans/"+reference+"/revisions", nil)
	authorize(req, "resolver-secret")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"revision":"rrev"`) {
		t.Fatalf("hosted read=%d body=%q", w.Code, w.Body.String())
	}
	if client.calls != 0 {
		t.Fatalf("upstream calls=%d, want 0 (hosted member preferred)", client.calls)
	}
}

func TestV2GroupConanFallsBackToProxy(t *testing.T) {
	store := repository.NewMemoryStore()
	objects := NewMemoryOCIObjectStore()
	hosted, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: "conan-hosted", Name: "conan-hosted", Format: repository.FormatConan})
	if err != nil {
		t.Fatal(err)
	}
	revisionsBody := []byte(`{"revisions":[{"revision":"upstream","time":"2024-01-01T00:00:00Z"}]}`)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(revisionsBody)
	}))
	defer upstream.Close()
	proxy, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{
		ID: "conan-proxy", Name: "conan-proxy", Format: repository.FormatConan,
		Type: repository.RepositoryTypeProxy, Endpoint: "https://proxy.example", AllowedHosts: []string{"proxy.example"},
	})
	if err != nil {
		t.Fatal(err)
	}
	createV2Group(t, store, "conan-group", repository.FormatConan,
		repository.GroupMember{RepositoryID: hosted.ID, Position: 0},
		repository.GroupMember{RepositoryID: proxy.ID, Position: 1},
	)

	client := &conanFixtureClient{proxyURL: upstream.URL}
	handler := NewGatewayHandlerWithFormatCaches(Dependencies{NativeConanObjectStore: objects}, store, TestAdapter{}, testAuthenticator(), NewDefaultOCICache(NewMemoryOCIObjectStore(), nil), nil, nil, NewConanCache(nil), nil, conanGatewayClient{client})

	req := httptest.NewRequest(http.MethodGet, "/conan/v2/conan-group/conans/pkg/1.0/user/stable/revisions", nil)
	authorize(req, "resolver-secret")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK || w.Body.String() != string(revisionsBody) {
		t.Fatalf("proxy read=%d body=%q", w.Code, w.Body.String())
	}
	if client.calls != 1 {
		t.Fatalf("upstream calls=%d, want 1 (hosted miss falls back to proxy)", client.calls)
	}
}

func TestV2GroupNameDoesNotShadowV2RepositoryOrLegacyGroup(t *testing.T) {
	store := repository.NewMemoryStore()
	objects := NewMemoryOCIObjectStore()
	// A V2 repository and a V2 group share the same name; the repository must
	// keep priority because the native handler claims the path first.
	repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: "oci-repo", Name: "shared", Format: repository.FormatOCI})
	if err != nil {
		t.Fatal(err)
	}
	createV2Group(t, store, "shared", repository.FormatOCI, repository.GroupMember{RepositoryID: repo.ID, Position: 0})

	manifest := []byte(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json"}`)
	sum := sha256.Sum256(manifest)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	manifestKey := "native/oci/manifests/oci-repo/app/" + hex.EncodeToString(sum[:])
	if err := objects.Put(context.Background(), manifestKey, manifest); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PutOCIManifest(context.Background(), repository.OCIManifest{RepositoryID: repo.ID, Name: "app", Digest: digest, ObjectKey: manifestKey, MediaType: "application/vnd.oci.image.manifest.v1+json", Size: int64(len(manifest))}, "latest"); err != nil {
		t.Fatal(err)
	}
	handler := NewGatewayHandler(Dependencies{NativeOCIObjectStore: objects}, store, TestAdapter{}, testAuthenticator())

	req := httptest.NewRequest(http.MethodGet, "/v2/shared/app/manifests/latest", nil)
	authorize(req, "resolver-secret")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK || w.Body.String() != string(manifest) {
		t.Fatalf("v2 repository read=%d body=%q", w.Code, w.Body.String())
	}

	// A legacy group with no V2 group of the same name keeps resolving through
	// the legacy path.
	legacyStore := repository.NewMemoryStore()
	if _, err := legacyStore.CreateGroup(context.Background(), repository.Group{Name: "legacy", Enabled: true, Members: []repository.Member{{Name: "proxy", Type: repository.MemberProxy, Endpoint: "http://proxy.example", Position: 0}}}); err != nil {
		t.Fatal(err)
	}
	legacyCache := NewOCICache(NewMemoryOCIObjectStore(), time.Hour, time.Hour, time.Hour, []string{"proxy.example"})
	legacyHandler := NewGatewayHandlerWithOCICache(Dependencies{}, legacyStore, TestAdapter{}, testAuthenticator(), legacyCache)
	legacyReq := httptest.NewRequest(http.MethodGet, "/v2/legacy/app/manifests/latest", nil)
	authorize(legacyReq, "resolver-secret")
	legacyW := httptest.NewRecorder()
	legacyHandler.ServeHTTP(legacyW, legacyReq)
	// The legacy proxy member is unreachable, but the request must be resolved
	// by the legacy group path (502 from the upstream fetch), not rejected as
	// an unknown repository name.
	if legacyW.Code != http.StatusBadGateway {
		t.Fatalf("legacy group read=%d body=%q", legacyW.Code, legacyW.Body.String())
	}
}
