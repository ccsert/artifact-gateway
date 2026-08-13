package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

func TestNativeAPTHostedReadsSwitchOnlyAfterSignedSnapshotIsVisible(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{
		ID: uuid.NewString(), Name: "apt-hosted-read", Format: repository.FormatAPT, Type: repository.RepositoryTypeHosted,
	})
	if err != nil {
		t.Fatal(err)
	}
	digest := "sha256:" + strings.Repeat("c", 64)
	session := repository.APTPublicationSession{
		ID: "apt-hosted-session", RepositoryID: repo.ID, Suite: "stable", Component: "main", Publisher: "ci",
		ObjectName: "widget_1.0-1_amd64.deb", DeclaredDigest: digest, DeclaredSize: 8,
		State: repository.APTPublicationSessionOpen, ExpiresAt: time.Now().Add(time.Hour),
	}
	if _, _, err = store.CreateAPTPublicationSessionIdempotently(ctx, session, "ci", "apt", "hosted-read", "hosted-read"); err != nil {
		t.Fatal(err)
	}
	objectKey := "native/apt/sha256/" + strings.Repeat("c", 64)
	if err = store.BeginAPTPackageUpload(ctx, session.ID, objectKey); err != nil {
		t.Fatal(err)
	}
	revision, err := store.CompleteAPTPackageUpload(ctx, session.ID, repository.APTPackageRevision{
		ID: "apt-hosted-revision", RepositoryID: repo.ID, Package: "widget", Version: "1.0-1", Architecture: "amd64",
		CanonicalIdentity: "widget@1.0-1#amd64", Digest: digest, ObjectKey: objectKey, Size: 8,
		ObjectName: session.ObjectName, Publisher: "ci",
	})
	if err != nil {
		t.Fatal(err)
	}
	objects := NewMemoryOCIObjectStore()
	publish := func(id string, sequence int64, body string, visible bool) {
		t.Helper()
		snapshot, createErr := store.CreateAPTRepositorySnapshot(ctx, repository.APTRepositorySnapshot{
			ID: id, RepositoryID: repo.ID, Suite: "stable", Sequence: sequence, State: repository.APTRepositorySnapshotBuilding,
		}, []repository.APTSnapshotPackage{{PublicationSessionID: session.ID, PackageRevisionID: revision.ID, Component: "main", Architecture: "amd64"}})
		if createErr != nil {
			t.Fatal(createErr)
		}
		if !visible {
			return
		}
		indexPath := "dists/stable/main/binary-amd64/Packages"
		indexBody := []byte(body + ":Packages")
		indexSum := sha256.Sum256(indexBody)
		indexDigest := "sha256:" + hex.EncodeToString(indexSum[:])
		releaseBody := []byte(fmt.Sprintf("Suite: stable\nAcquire-By-Hash: yes\nSHA256:\n %s %16d main/binary-amd64/Packages\n", strings.TrimPrefix(indexDigest, "sha256:"), len(indexBody)))
		assetBodies := map[string][]byte{
			"dists/stable/Release": releaseBody, "dists/stable/InRelease": []byte(body + ":InRelease"),
			"dists/stable/Release.gpg": []byte(body + ":Release.gpg"), indexPath: indexBody,
			"dists/stable/main/binary-amd64/by-hash/SHA256/" + strings.TrimPrefix(indexDigest, "sha256:"): indexBody,
		}
		assets := make([]repository.APTSnapshotAsset, 0, len(assetBodies))
		for path, assetBody := range assetBodies {
			sum := sha256.Sum256(assetBody)
			assetDigest := "sha256:" + hex.EncodeToString(sum[:])
			assetObjectKey := "native/apt/sha256/" + strings.TrimPrefix(assetDigest, "sha256:")
			if putErr := objects.Put(ctx, assetObjectKey, assetBody); putErr != nil {
				t.Fatal(putErr)
			}
			assets = append(assets, repository.APTSnapshotAsset{
				SnapshotID: id, RepositoryID: repo.ID, Path: path,
				Digest: assetDigest, ObjectKey: assetObjectKey, Size: int64(len(assetBody)), ContentType: "text/plain",
			})
		}
		assets = append(assets, repository.APTSnapshotAsset{
			SnapshotID: id, RepositoryID: repo.ID, Path: "pool/main/w/widget/widget_1.0-1_amd64.deb",
			Digest: revision.Digest, ObjectKey: revision.ObjectKey, Size: revision.Size, ContentType: "application/vnd.debian.binary-package",
		})
		snapshot.State = repository.APTRepositorySnapshotVisible
		for _, asset := range assets {
			if asset.Path == "dists/stable/Release" {
				snapshot.ReleaseDigest = asset.Digest
			}
			if asset.Path == "dists/stable/InRelease" {
				snapshot.InReleaseDigest = asset.Digest
			}
		}
		snapshot.SignerIdentity = "fixture"
		snapshot.KeyFingerprint = strings.Repeat("a", 40)
		snapshot.SignatureAlgorithm = "fixture"
		if _, publishErr := store.PublishAPTRepositorySnapshotWithAudit(ctx, snapshot, assets, releaseBody, repository.AuditRecord{Actor: "test", Operation: "apt.repository_snapshot.publish"}); publishErr != nil {
			t.Fatal(publishErr)
		}
	}
	publish("apt-snapshot-one", 1, "first", true)
	publish("apt-snapshot-two", 2, "second", false)

	handler := NewGatewayHandler(Dependencies{NativeAPTObjectStore: objects}, store, TestAdapter{}, testAuthenticator())
	request := func(method string, headers http.Header) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "/apt/"+repo.Name+"/dists/stable/InRelease", nil)
		if headers != nil {
			req.Header = headers.Clone()
		}
		authorize(req, "resolver-secret")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		return response
	}
	old := request(http.MethodGet, nil)
	if old.Code != http.StatusOK || old.Body.String() != "first:InRelease" {
		t.Fatalf("building snapshot leaked: status=%d body=%q", old.Code, old.Body.String())
	}
	if err = store.FailAPTRepositorySnapshot(ctx, "apt-snapshot-two"); err != nil {
		t.Fatal(err)
	}
	publish("apt-snapshot-three", 3, "third", true)
	current := request(http.MethodGet, http.Header{"Range": []string{"bytes=0-4"}})
	if current.Code != http.StatusPartialContent || current.Body.String() != "third" {
		t.Fatalf("visible range status=%d body=%q headers=%v", current.Code, current.Body.String(), current.Header())
	}
	head := request(http.MethodHead, nil)
	if head.Code != http.StatusOK || head.Body.Len() != 0 || head.Header().Get("ETag") == "" {
		t.Fatalf("visible HEAD status=%d body=%q headers=%v", head.Code, head.Body.String(), head.Header())
	}
	searchRequest := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/"+repo.ID+"/artifact-search?q=pool/main/w/widget/", nil)
	authorize(searchRequest, "admin-secret")
	searchResponse := httptest.NewRecorder()
	handler.ServeHTTP(searchResponse, searchRequest)
	if searchResponse.Code != http.StatusOK || !strings.Contains(searchResponse.Body.String(), `"coordinate":"pool/main/w/widget/widget_1.0-1_amd64.deb"`) || strings.Contains(searchResponse.Body.String(), `"sourceUrl"`) {
		t.Fatalf("Hosted snapshot search=%d body=%s", searchResponse.Code, searchResponse.Body.String())
	}
}

func TestAPTArtifactSearchReturnsEmptyAndCachedAssetPages(t *testing.T) {
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{
		ID: uuid.NewString(), Name: "apt-search", Format: repository.FormatAPT,
		Type: repository.RepositoryTypeProxy, Endpoint: "https://deb.example.test/debian", AllowedHosts: []string{"deb.example.test"},
	})
	if err != nil {
		t.Fatal(err)
	}
	authenticator := testAuthenticator()
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, authenticator)
	search := func(query string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/"+repo.ID+"/artifact-search?q="+query, nil)
		authorize(request, authenticator.IssueToken("apt-reader"))
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}

	empty := search("")
	if empty.Code != http.StatusOK || !strings.Contains(empty.Body.String(), `"items":[]`) {
		t.Fatalf("empty search=%d body=%s", empty.Code, empty.Body.String())
	}

	createdAt := time.Date(2026, time.August, 11, 4, 30, 0, 0, time.UTC)
	digest := "sha256:" + strings.Repeat("a", 64)
	asset, err := store.CacheAPTAsset(context.Background(), repository.APTAsset{
		RepositoryID: repo.ID,
		Path:         "pool/main/h/hello/hello_2.10_amd64.deb",
		Digest:       digest,
		ObjectKey:    "apt/hello",
		Size:         1234,
		ContentType:  "application/vnd.debian.binary-package",
		SourceURL:    "https://deb.example.test/debian/pool/main/h/hello/hello_2.10_amd64.deb",
		CachedAt:     createdAt,
		CreatedAt:    createdAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !asset.CreatedAt.Equal(createdAt) {
		t.Fatalf("createdAt=%s", asset.CreatedAt)
	}

	cached := search("pool/main/h/hello/")
	if cached.Code != http.StatusOK {
		t.Fatalf("cached search=%d body=%s", cached.Code, cached.Body.String())
	}
	var page struct {
		Items []struct {
			Coordinate  string    `json:"coordinate"`
			Digest      string    `json:"digest"`
			Size        int64     `json:"size"`
			ContentType string    `json:"contentType"`
			CreatedAt   time.Time `json:"createdAt"`
			CachedAt    time.Time `json:"cachedAt"`
			SourceURL   string    `json:"sourceUrl"`
		} `json:"items"`
	}
	if err = json.NewDecoder(cached.Body).Decode(&page); err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].Coordinate != asset.Path || page.Items[0].Digest != digest || page.Items[0].Size != 1234 || page.Items[0].ContentType != asset.ContentType || !page.Items[0].CreatedAt.Equal(createdAt) || !page.Items[0].CachedAt.Equal(createdAt) || page.Items[0].SourceURL != asset.SourceURL {
		t.Fatalf("page=%#v", page)
	}
}

type aptProxyTestFixture struct {
	handler             http.Handler
	repositoryName      string
	metadataPayload     []byte
	packagePayload      []byte
	lastModified        string
	requests            *atomic.Int32
	conditionalRequests *atomic.Int32
}

func newAPTProxyTestFixture(t *testing.T) aptProxyTestFixture {
	t.Helper()
	requests := &atomic.Int32{}
	conditionalRequests := &atomic.Int32{}
	metadataPayload := []byte("Origin: Debian\nSuite: bookworm\n")
	packagePayload := []byte("debian-package-payload")
	lastModified := "Mon, 10 Aug 2026 04:30:00 GMT"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path == "/pool/main/h/hello/hello_2.10_amd64.deb" {
			w.Header().Set("Content-Type", "application/vnd.debian.binary-package")
			w.Header().Set("Last-Modified", lastModified)
			_, _ = w.Write(packagePayload)
			return
		}
		if r.URL.Path != "/dists/bookworm/InRelease" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("If-None-Match") == `"release-v1"` {
			conditionalRequests.Add(1)
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", `"release-v1"`)
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(metadataPayload)
	}))
	t.Cleanup(upstream.Close)
	store := repository.NewMemoryStore()
	_, _ = store.ReplaceAnonymousAccessPolicy(context.Background(), repository.AnonymousAccessPolicy{Enabled: true}, "1")
	repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: "apt-proxy", Name: "debian", Format: repository.FormatAPT, Type: repository.RepositoryTypeProxy, Endpoint: upstream.URL, AllowedHosts: []string{"127.0.0.1"}, AnonymousRead: true})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewGatewayHandler(Dependencies{NativeAPTObjectStore: NewMemoryOCIObjectStore()}, store, TestAdapter{}, testAuthenticator(), UpstreamClient{HTTPClient: upstream.Client()})
	return aptProxyTestFixture{handler: handler, repositoryName: repo.Name, metadataPayload: metadataPayload, packagePayload: packagePayload, lastModified: lastModified, requests: requests, conditionalRequests: conditionalRequests}
}

func (f aptProxyTestFixture) request(method, resource string, headers http.Header) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, "/apt/"+f.repositoryName+"/"+resource, nil)
	request.Header = headers.Clone()
	response := httptest.NewRecorder()
	f.handler.ServeHTTP(response, request)
	return response
}

func (f aptProxyTestFixture) cachePackage(t *testing.T) string {
	t.Helper()
	path := "pool/main/h/hello/hello_2.10_amd64.deb"
	response := f.request(http.MethodGet, path, nil)
	if response.Code != http.StatusOK || !bytes.Equal(response.Body.Bytes(), f.packagePayload) {
		t.Fatalf("package response=%d body=%q", response.Code, response.Body.String())
	}
	return path
}

func TestNativeAPTProxyCachesAndRevalidatesMutableMetadata(t *testing.T) {
	fixture := newAPTProxyTestFixture(t)
	first := fixture.request(http.MethodGet, "dists/bookworm/InRelease", nil)
	if first.Code != http.StatusOK || !bytes.Equal(first.Body.Bytes(), fixture.metadataPayload) || first.Header().Get("ETag") == "" {
		t.Fatalf("first response=%d body=%q headers=%v", first.Code, first.Body.String(), first.Header())
	}
	second := fixture.request(http.MethodGet, "dists/bookworm/InRelease", nil)
	if second.Code != http.StatusOK || !bytes.Equal(second.Body.Bytes(), fixture.metadataPayload) || fixture.requests.Load() != 2 || fixture.conditionalRequests.Load() != 1 {
		t.Fatalf("cached response=%d body=%q upstreamRequests=%d conditional=%d", second.Code, second.Body.String(), fixture.requests.Load(), fixture.conditionalRequests.Load())
	}
}

func TestNativeAPTProxyServesHeadWithoutBody(t *testing.T) {
	fixture := newAPTProxyTestFixture(t)
	fixture.cachePackage(t)
	head := fixture.request(http.MethodHead, "pool/main/h/hello/hello_2.10_amd64.deb", nil)
	if head.Code != http.StatusOK || head.Body.Len() != 0 || head.Header().Get("Content-Length") != fmt.Sprintf("%d", len(fixture.packagePayload)) {
		t.Fatalf("head response=%d bytes=%d length=%q", head.Code, head.Body.Len(), head.Header().Get("Content-Length"))
	}
}

func TestNativeAPTProxyServesSingleByteRange(t *testing.T) {
	fixture := newAPTProxyTestFixture(t)
	path := fixture.cachePackage(t)
	ranged := fixture.request(http.MethodGet, path, http.Header{"Range": []string{"bytes=7-13"}})
	if ranged.Code != http.StatusPartialContent || ranged.Body.String() != string(fixture.packagePayload[7:14]) || ranged.Header().Get("Content-Range") != fmt.Sprintf("bytes 7-13/%d", len(fixture.packagePayload)) || ranged.Header().Get("Accept-Ranges") != "bytes" {
		t.Fatalf("range response=%d body=%q headers=%v", ranged.Code, ranged.Body.String(), ranged.Header())
	}
}

func TestNativeAPTProxyEvaluatesClientValidatorsAgainstCachedAsset(t *testing.T) {
	fixture := newAPTProxyTestFixture(t)
	path := fixture.cachePackage(t)
	notModified := fixture.request(http.MethodGet, path, http.Header{"If-Modified-Since": []string{fixture.lastModified}})
	if notModified.Code != http.StatusNotModified || notModified.Body.Len() != 0 || notModified.Header().Get("ETag") == "" {
		t.Fatalf("conditional response=%d body=%q headers=%v", notModified.Code, notModified.Body.String(), notModified.Header())
	}
}

func TestNativeAPTProxyRejectsMultipleRanges(t *testing.T) {
	fixture := newAPTProxyTestFixture(t)
	path := fixture.cachePackage(t)
	invalid := fixture.request(http.MethodGet, path, http.Header{"Range": []string{"bytes=1-2,4-5"}})
	if invalid.Code != http.StatusRequestedRangeNotSatisfiable || invalid.Header().Get("Content-Range") != fmt.Sprintf("bytes */%d", len(fixture.packagePayload)) {
		t.Fatalf("invalid range=%d headers=%v", invalid.Code, invalid.Header())
	}
}

func TestNativeAPTProxyRejectsPathTraversal(t *testing.T) {
	fixture := newAPTProxyTestFixture(t)
	traversal := fixture.request(http.MethodGet, "dists/%2e%2e/secret", nil)
	if traversal.Code != http.StatusNotFound {
		t.Fatalf("traversal status=%d body=%q", traversal.Code, traversal.Body.String())
	}
}

func TestNativeAPTProxyDoesNotForwardClientValidatorsToColdUpstream(t *testing.T) {
	var sawConditional atomic.Bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("If-None-Match") != "" || r.Header.Get("If-Modified-Since") != "" {
			sawConditional.Store(true)
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", `"upstream-v1"`)
		_, _ = io.WriteString(w, "Origin: Debian\n")
	}))
	defer upstream.Close()
	store := repository.NewMemoryStore()
	_, _ = store.ReplaceAnonymousAccessPolicy(context.Background(), repository.AnonymousAccessPolicy{Enabled: true}, "1")
	repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: "apt-cold", Name: "apt-cold", Format: repository.FormatAPT, Type: repository.RepositoryTypeProxy, Endpoint: upstream.URL, AllowedHosts: []string{"127.0.0.1"}, AnonymousRead: true})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewGatewayHandler(Dependencies{NativeAPTObjectStore: NewMemoryOCIObjectStore()}, store, TestAdapter{}, testAuthenticator(), UpstreamClient{HTTPClient: upstream.Client()})
	request := httptest.NewRequest(http.MethodGet, "/apt/"+repo.Name+"/dists/stable/InRelease", nil)
	request.Header.Set("If-None-Match", `"client-cache"`)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || sawConditional.Load() || response.Body.String() != "Origin: Debian\n" {
		t.Fatalf("cold conditional response=%d body=%q forwarded=%v", response.Code, response.Body.String(), sawConditional.Load())
	}
}

func TestNativeAPTProxyServesStaleAssetWhenUpstreamBodyIsTruncated(t *testing.T) {
	var requests atomic.Int32
	payload := []byte("Origin: Debian\n")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if requests.Add(1) == 1 {
			_, _ = w.Write(payload)
			return
		}
		w.Header().Set("Content-Length", "100")
		_, _ = w.Write([]byte("short"))
	}))
	defer upstream.Close()
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: "apt-truncated", Name: "apt-truncated", Format: repository.FormatAPT, Type: repository.RepositoryTypeProxy, Endpoint: upstream.URL, AllowedHosts: []string{"127.0.0.1"}})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewGatewayHandler(Dependencies{NativeAPTObjectStore: NewMemoryOCIObjectStore()}, store, TestAdapter{}, testAuthenticator(), UpstreamClient{HTTPClient: upstream.Client()})
	path := "/apt/" + repo.Name + "/dists/stable/InRelease"
	first := httptest.NewRecorder()
	firstRequest := httptest.NewRequest(http.MethodGet, path, nil)
	authorize(firstRequest, "resolver-secret")
	handler.ServeHTTP(first, firstRequest)
	second := httptest.NewRecorder()
	secondRequest := httptest.NewRequest(http.MethodGet, path, nil)
	authorize(secondRequest, "resolver-secret")
	handler.ServeHTTP(second, secondRequest)
	if first.Code != http.StatusOK || second.Code != http.StatusOK || second.Body.String() != string(payload) || second.Header().Get("Warning") == "" {
		t.Fatalf("truncated response first=%d second=%d body=%q headers=%v", first.Code, second.Code, second.Body.String(), second.Header())
	}
}

func TestNativeAPTProxyReturnsNotFoundWhenUpstreamRemovesCachedAsset(t *testing.T) {
	var requests atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if requests.Add(1) == 1 {
			_, _ = io.WriteString(w, "Origin: Debian\n")
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer upstream.Close()
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: "apt-removed", Name: "apt-removed", Format: repository.FormatAPT, Type: repository.RepositoryTypeProxy, Endpoint: upstream.URL, AllowedHosts: []string{"127.0.0.1"}})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewGatewayHandler(Dependencies{NativeAPTObjectStore: NewMemoryOCIObjectStore()}, store, TestAdapter{}, testAuthenticator(), UpstreamClient{HTTPClient: upstream.Client()})
	path := "/apt/" + repo.Name + "/dists/stable/InRelease"
	first := httptest.NewRecorder()
	firstRequest := httptest.NewRequest(http.MethodGet, path, nil)
	authorize(firstRequest, "resolver-secret")
	handler.ServeHTTP(first, firstRequest)
	second := httptest.NewRecorder()
	secondRequest := httptest.NewRequest(http.MethodGet, path, nil)
	authorize(secondRequest, "resolver-secret")
	handler.ServeHTTP(second, secondRequest)
	if first.Code != http.StatusOK || second.Code != http.StatusNotFound || second.Header().Get("Warning") != "" {
		t.Fatalf("removed response first=%d second=%d headers=%v", first.Code, second.Code, second.Header())
	}
}

func TestNativeAPTProxyAnonymousReadRequiresGlobalAndRepositoryPolicy(t *testing.T) {
	store := repository.NewMemoryStore()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "Origin: Debian\n") }))
	defer upstream.Close()
	repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: "apt-public", Name: "apt-public", Format: repository.FormatAPT, Type: repository.RepositoryTypeProxy, Endpoint: upstream.URL, AllowedHosts: []string{"127.0.0.1"}, AnonymousRead: true})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewGatewayHandler(Dependencies{NativeAPTObjectStore: NewMemoryOCIObjectStore()}, store, TestAdapter{}, testAuthenticator(), UpstreamClient{HTTPClient: upstream.Client()})
	path := "/apt/" + repo.Name + "/dists/stable/InRelease"
	denied := httptest.NewRecorder()
	handler.ServeHTTP(denied, httptest.NewRequest(http.MethodGet, path, nil))
	if denied.Code != http.StatusUnauthorized {
		t.Fatalf("global policy disabled=%d", denied.Code)
	}
	if _, err = store.ReplaceAnonymousAccessPolicy(context.Background(), repository.AnonymousAccessPolicy{Enabled: true}, "1"); err != nil {
		t.Fatal(err)
	}
	allowed := httptest.NewRecorder()
	handler.ServeHTTP(allowed, httptest.NewRequest(http.MethodGet, path, nil))
	if allowed.Code != http.StatusOK {
		t.Fatalf("anonymous allowed=%d body=%s", allowed.Code, allowed.Body.String())
	}
}

func TestNativeAPTProxyAcceptsBasicAndBearerCredentials(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "Origin: Debian\n") }))
	defer upstream.Close()
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: "apt-auth", Name: "apt-auth", Format: repository.FormatAPT, Type: repository.RepositoryTypeProxy, Endpoint: upstream.URL, AllowedHosts: []string{"127.0.0.1"}})
	if err != nil {
		t.Fatal(err)
	}
	authenticator := testAuthenticator()
	handler := NewGatewayHandler(Dependencies{NativeAPTObjectStore: NewMemoryOCIObjectStore()}, store, TestAdapter{}, authenticator, UpstreamClient{HTTPClient: upstream.Client()})
	path := "/apt/" + repo.Name + "/dists/stable/InRelease"
	basic := httptest.NewRequest(http.MethodGet, path, nil)
	basic.SetBasicAuth("apt-client", "resolver-secret")
	basicResponse := httptest.NewRecorder()
	handler.ServeHTTP(basicResponse, basic)
	bearer := httptest.NewRequest(http.MethodGet, path, nil)
	authorize(bearer, authenticator.IssueToken("apt-client"))
	bearerResponse := httptest.NewRecorder()
	handler.ServeHTTP(bearerResponse, bearer)
	if basicResponse.Code != http.StatusOK || bearerResponse.Code != http.StatusOK {
		t.Fatalf("basic=%d bearer=%d", basicResponse.Code, bearerResponse.Code)
	}
}

func TestNativeAPTProxyEnforcesResourcePrefixGrant(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "Origin: Debian\n") }))
	defer upstream.Close()
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: "apt-grant", Name: "apt-grant", Format: repository.FormatAPT, Type: repository.RepositoryTypeProxy, Endpoint: upstream.URL, AllowedHosts: []string{"127.0.0.1"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.ReplaceRepositoryGrants(context.Background(), repo.ID, []repository.RepositoryGrant{{Principal: "apt-reader", Scopes: []string{"repositories:read"}, ResourcePrefix: "dists/allowed/"}}, "1"); err != nil {
		t.Fatal(err)
	}
	authenticator := testAuthenticator()
	handler := NewGatewayHandler(Dependencies{NativeAPTObjectStore: NewMemoryOCIObjectStore()}, store, TestAdapter{}, authenticator, UpstreamClient{HTTPClient: upstream.Client()})
	request := func(resource string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/apt/"+repo.Name+"/"+resource, nil)
		authorize(req, authenticator.IssueToken("apt-reader"))
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		return response
	}
	if allowed := request("dists/allowed/InRelease"); allowed.Code != http.StatusOK {
		t.Fatalf("allowed prefix=%d body=%s", allowed.Code, allowed.Body.String())
	}
	if denied := request("dists/blocked/InRelease"); denied.Code != http.StatusForbidden {
		t.Fatalf("blocked prefix=%d body=%s", denied.Code, denied.Body.String())
	}
}

func TestAPTUpstreamRejectsRedirectOutsideAllowedHosts(t *testing.T) {
	allowed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "Origin: Debian\n") }))
	defer allowed.Close()
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, allowed.URL, http.StatusTemporaryRedirect)
	}))
	defer target.Close()
	targetURL, _ := url.Parse(target.URL)
	client := UpstreamClient{HTTPClient: target.Client()}
	endpoint := "http://localhost:" + targetURL.Port()
	response, err := client.FetchAPT(context.Background(), http.MethodGet, repository.HostedRepository{Endpoint: endpoint, AllowedHosts: []string{"localhost"}}, endpoint, nil)
	if response != nil {
		_ = response.Body.Close()
	}
	if err == nil {
		t.Fatal("redirect to an unlisted host was accepted")
	}
}

func TestV2GroupAPTUsesOrderedProxyFallback(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/dists/stable/InRelease" {
			_, _ = io.WriteString(w, "deb-from-second")
			return
		}
		http.NotFound(w, r)
	}))
	store := repository.NewMemoryStore()
	first, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: "apt-first", Name: "apt-first", Format: repository.FormatAPT, Type: repository.RepositoryTypeProxy, Endpoint: "http://127.0.0.1:1", AllowedHosts: []string{"127.0.0.1"}})
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: "apt-second", Name: "apt-second", Format: repository.FormatAPT, Type: repository.RepositoryTypeProxy, Endpoint: upstream.URL, AllowedHosts: []string{"127.0.0.1"}})
	if err != nil {
		t.Fatal(err)
	}
	group, _, err := store.CreateHostedGroupIdempotently(context.Background(), repository.HostedGroup{ID: "apt-group", Name: "debian-all", Format: repository.FormatAPT, Members: []repository.GroupMember{{RepositoryID: first.ID, Position: 0}, {RepositoryID: second.ID, Position: 1}}}, "test", "apt-group", "apt-group")
	if err != nil {
		t.Fatal(err)
	}
	handler := NewGatewayHandler(Dependencies{NativeAPTObjectStore: NewMemoryOCIObjectStore()}, store, TestAdapter{}, testAuthenticator(), UpstreamClient{HTTPClient: upstream.Client()})
	path := "/apt/" + group.Name + "/dists/stable/InRelease"
	req := httptest.NewRequest(http.MethodGet, path, nil)
	authorize(req, "resolver-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	if response.Code != http.StatusOK || response.Body.String() != "deb-from-second" {
		t.Fatalf("group response=%d body=%q", response.Code, response.Body.String())
	}
	upstream.Close()
	staleRequest := httptest.NewRequest(http.MethodGet, path, nil)
	authorize(staleRequest, "resolver-secret")
	stale := httptest.NewRecorder()
	handler.ServeHTTP(stale, staleRequest)
	if stale.Code != http.StatusOK || stale.Body.String() != "deb-from-second" || stale.Header().Get("Warning") == "" {
		t.Fatalf("stale group response=%d body=%q headers=%v", stale.Code, stale.Body.String(), stale.Header())
	}
}
