package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

func publishQuarantineReadMaven(t *testing.T, store *repository.MemoryStore, objects *MemoryOCIObjectStore, repo repository.HostedRepository) (repository.MavenArtifact, string) {
	t.Helper()
	ctx := context.Background()
	coordinate := "org.example:widget:1.0.0"
	path := "org/example/widget/1.0.0/widget-1.0.0.jar"
	body := []byte("maven quarantine bytes")
	sum := sha256.Sum256(body)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	key := "native/maven/sha256/" + hex.EncodeToString(sum[:])
	if err := objects.Put(ctx, key, body); err != nil {
		t.Fatal(err)
	}
	session := repository.MavenPublishSession{ID: "quarantine-read-maven-session", RepositoryID: repo.ID, Coordinate: coordinate, State: "open", Objects: []repository.MavenDeclaredObject{{Name: "widget-1.0.0.jar", Digest: digest, Size: int64(len(body))}}, ExpiresAt: time.Now().Add(time.Hour)}
	if _, err := store.CreateMavenPublishSession(ctx, session); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkMavenPublishObject(ctx, session.ID, session.Objects[0].Name, key); err != nil {
		t.Fatal(err)
	}
	artifact, err := store.CommitMavenPublishSession(ctx, session.ID, []repository.MavenAsset{{RepositoryID: repo.ID, Path: path, ObjectKey: key, Digest: digest, Size: int64(len(body))}})
	if err != nil {
		t.Fatal(err)
	}
	return artifact, path
}

func quarantineReadIdentity(t *testing.T, store *repository.MemoryStore, repo repository.HostedRepository, coordinate, digest string) {
	t.Helper()
	if _, err := store.ReplaceArtifactQuarantine(context.Background(), repository.ArtifactQuarantine{RepositoryID: repo.ID, Format: repo.Format, Coordinate: coordinate, Digest: digest, State: repository.ArtifactQuarantineStateQuarantined, Reason: "malware confirmed", UpdatedBy: "security-admin"}, "0"); err != nil {
		t.Fatal(err)
	}
}

func TestMavenQuarantineReadPolicyBlocksDirectAndGroupWithoutProxyFallback(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	objects := NewMemoryOCIObjectStore()
	hosted, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: "maven-read-hosted", Name: "maven-read-hosted", Format: repository.FormatMaven})
	if err != nil {
		t.Fatal(err)
	}
	artifact, assetPath := publishQuarantineReadMaven(t, store, objects, hosted)
	enableQuarantineReadPolicy(t, store, hosted.ID)
	quarantineReadIdentity(t, store, hosted, artifact.Coordinate, artifact.Digest)

	metadataPath := "/org/example/widget/maven-metadata.xml"
	upstream := &proxyUpstream{bodies: map[string][]byte{"/" + assetPath: []byte("proxy bypass"), metadataPath: []byte("<metadata><version>1.0.0</version></metadata>")}, calls: map[string]int{}}
	server := httptest.NewServer(upstream)
	defer server.Close()
	allowedHost := strings.TrimPrefix(server.URL, "http://")
	proxy, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: "maven-read-proxy", Name: "maven-read-proxy", Format: repository.FormatMaven, Type: repository.RepositoryTypeProxy, Endpoint: server.URL, AllowedHosts: []string{allowedHost}})
	if err != nil {
		t.Fatal(err)
	}
	createV2Group(t, store, "maven-read-group", repository.FormatMaven, repository.GroupMember{RepositoryID: hosted.ID}, repository.GroupMember{RepositoryID: proxy.ID, Position: 1})
	handler := NewGatewayHandlerWithCaches(Dependencies{NativeMavenObjectStore: objects}, store, TestAdapter{}, testAuthenticator(), NewDefaultOCICache(NewMemoryOCIObjectStore(), nil), NewMavenCache(NewMemoryOCIObjectStore(), time.Hour, time.Hour, time.Hour, time.Hour, []string{allowedHost}), UpstreamClient{HTTPClient: server.Client()})

	for _, path := range []string{"/maven/" + hosted.Name + "/" + assetPath, "/maven/maven-read-group/" + assetPath} {
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			request := httptest.NewRequest(method, path, nil)
			authorize(request, "resolver-secret")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), repository.ArtifactQuarantinedReason) {
				t.Fatalf("%s %s=%d body=%q", method, path, response.Code, response.Body.String())
			}
		}
	}
	if calls := upstream.callCount("/" + assetPath); calls != 0 {
		t.Fatalf("proxy fallback calls=%d", calls)
	}
	requireQuarantineReadDeniedAudit(t, store, repository.FormatMaven)
	for _, path := range []string{"/maven/" + hosted.Name + metadataPath, "/maven/maven-read-group" + metadataPath} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		authorize(request, "resolver-secret")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusNotFound || strings.Contains(response.Body.String(), "1.0.0") {
			t.Fatalf("metadata %s=%d body=%q", path, response.Code, response.Body.String())
		}
	}
	if calls := upstream.callCount(metadataPath); calls != 0 {
		t.Fatalf("metadata proxy fallback calls=%d", calls)
	}
}

func TestMavenGroupQuarantinedCoordinateClaimsDifferentClassifier(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	objects := NewMemoryOCIObjectStore()
	first, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: "maven-read-classifier-first", Name: "maven-read-classifier-first", Format: repository.FormatMaven})
	if err != nil {
		t.Fatal(err)
	}
	artifact, _ := publishQuarantineReadMaven(t, store, objects, first)
	second, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: "maven-read-classifier-second", Name: "maven-read-classifier-second", Format: repository.FormatMaven})
	if err != nil {
		t.Fatal(err)
	}
	classifierPath := "org/example/widget/1.0.0/widget-1.0.0-sources.jar"
	body := []byte("lower member sources")
	sum := sha256.Sum256(body)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	key := "native/maven/sha256/" + hex.EncodeToString(sum[:])
	if err = objects.Put(ctx, key, body); err != nil {
		t.Fatal(err)
	}
	session := repository.MavenPublishSession{ID: "maven-read-classifier-session", RepositoryID: second.ID, Coordinate: artifact.Coordinate, State: "open", Objects: []repository.MavenDeclaredObject{{Name: "widget-1.0.0-sources.jar", Digest: digest, Size: int64(len(body))}}, ExpiresAt: time.Now().Add(time.Hour)}
	if _, err = store.CreateMavenPublishSession(ctx, session); err != nil {
		t.Fatal(err)
	}
	if err = store.MarkMavenPublishObject(ctx, session.ID, session.Objects[0].Name, key); err != nil {
		t.Fatal(err)
	}
	if _, err = store.CommitMavenPublishSession(ctx, session.ID, []repository.MavenAsset{{RepositoryID: second.ID, Path: classifierPath, ObjectKey: key, Digest: digest, Size: int64(len(body))}}); err != nil {
		t.Fatal(err)
	}
	enableQuarantineReadPolicy(t, store, first.ID)
	quarantineReadIdentity(t, store, first, artifact.Coordinate, artifact.Digest)
	createV2Group(t, store, "maven-read-classifier-group", repository.FormatMaven, repository.GroupMember{RepositoryID: first.ID}, repository.GroupMember{RepositoryID: second.ID, Position: 1})
	handler := NewGatewayHandler(Dependencies{NativeMavenObjectStore: objects}, store, TestAdapter{}, testAuthenticator())
	request := httptest.NewRequest(http.MethodGet, "/maven/maven-read-classifier-group/"+classifierPath, nil)
	authorize(request, "resolver-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), repository.ArtifactQuarantinedReason) {
		t.Fatalf("classifier bypass=%d body=%q", response.Code, response.Body.String())
	}
}

func publishQuarantineReadOCI(t *testing.T, store *repository.MemoryStore, objects *MemoryOCIObjectStore, repo repository.HostedRepository) repository.OCIManifest {
	t.Helper()
	body := []byte(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json"}`)
	sum := sha256.Sum256(body)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	key := "native/oci/manifests/" + repo.ID + "/app/" + hex.EncodeToString(sum[:])
	if err := objects.Put(context.Background(), key, body); err != nil {
		t.Fatal(err)
	}
	manifest, err := store.PutOCIManifest(context.Background(), repository.OCIManifest{RepositoryID: repo.ID, Name: "app", Digest: digest, ObjectKey: key, MediaType: "application/vnd.oci.image.manifest.v1+json", Size: int64(len(body))}, "latest")
	if err != nil {
		t.Fatal(err)
	}
	return manifest
}

func TestOCIQuarantineReadPolicyUsesDeniedAndPreventsGroupFallback(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	objects := NewMemoryOCIObjectStore()
	hosted, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: "oci-read-hosted", Name: "oci-read-hosted", Format: repository.FormatOCI})
	if err != nil {
		t.Fatal(err)
	}
	manifest := publishQuarantineReadOCI(t, store, objects, hosted)
	enableQuarantineReadPolicy(t, store, hosted.ID)
	quarantineReadIdentity(t, store, hosted, manifest.Name, manifest.Digest)

	upstream := &proxyUpstream{bodies: map[string][]byte{"/v2/app/manifests/latest": []byte(`{"schemaVersion":2,"from":"proxy"}`)}, calls: map[string]int{}}
	server := httptest.NewServer(upstream)
	defer server.Close()
	allowedHost := strings.TrimPrefix(server.URL, "http://")
	proxy, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: "oci-read-proxy", Name: "oci-read-proxy", Format: repository.FormatOCI, Type: repository.RepositoryTypeProxy, Endpoint: server.URL, AllowedHosts: []string{allowedHost}})
	if err != nil {
		t.Fatal(err)
	}
	createV2Group(t, store, "oci-read-group", repository.FormatOCI, repository.GroupMember{RepositoryID: hosted.ID}, repository.GroupMember{RepositoryID: proxy.ID, Position: 1})
	handler := NewGatewayHandlerWithOCICache(Dependencies{NativeOCIObjectStore: objects}, store, TestAdapter{}, testAuthenticator(), NewOCICache(NewMemoryOCIObjectStore(), time.Hour, time.Hour, time.Hour, []string{allowedHost}), UpstreamClient{HTTPClient: server.Client()})

	for _, path := range []string{"/v2/" + hosted.Name + "/app/manifests/latest", "/v2/oci-read-group/app/manifests/latest"} {
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			request := httptest.NewRequest(method, path, nil)
			authorize(request, "resolver-secret")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), `"code":"DENIED"`) || !strings.Contains(response.Body.String(), repository.ArtifactQuarantinedReason) {
				t.Fatalf("%s %s=%d body=%q", method, path, response.Code, response.Body.String())
			}
		}
	}
	if calls := upstream.callCount("/v2/app/manifests/latest"); calls != 0 {
		t.Fatalf("proxy fallback calls=%d", calls)
	}
	requireQuarantineReadDeniedAudit(t, store, repository.FormatOCI)
	for _, path := range []string{"/v2/" + hosted.Name + "/app/tags/list", "/v2/oci-read-group/app/tags/list"} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		authorize(request, "resolver-secret")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK || strings.Contains(response.Body.String(), `"latest"`) {
			t.Fatalf("tags %s=%d body=%q", path, response.Code, response.Body.String())
		}
	}
	catalogRequest := httptest.NewRequest(http.MethodGet, "/v2/_catalog", nil)
	authorize(catalogRequest, "admin-secret")
	catalogResponse := httptest.NewRecorder()
	handler.ServeHTTP(catalogResponse, catalogRequest)
	if catalogResponse.Code != http.StatusOK || strings.Contains(catalogResponse.Body.String(), hosted.Name+"/app") {
		t.Fatalf("catalog=%d body=%q", catalogResponse.Code, catalogResponse.Body.String())
	}
}

func TestOCIQuarantineReadPolicyBlocksBlobClosure(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	objects := NewMemoryOCIObjectStore()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: "oci-read-blob", Name: "oci-read-blob", Format: repository.FormatOCI})
	if err != nil {
		t.Fatal(err)
	}
	blobBody := []byte("quarantined layer")
	blobSum := sha256.Sum256(blobBody)
	blobDigest := "sha256:" + hex.EncodeToString(blobSum[:])
	blobKey := "native/oci/blobs/" + hex.EncodeToString(blobSum[:])
	if err = objects.Put(ctx, blobKey, blobBody); err != nil {
		t.Fatal(err)
	}
	upload := repository.OCIUpload{ID: "oci-read-upload", RepositoryID: repo.ID, Name: "app", State: "open", ExpiresAt: time.Now().Add(time.Hour)}
	if _, err = store.CreateOCIUpload(ctx, upload); err != nil {
		t.Fatal(err)
	}
	if _, err = store.CompleteOCIUpload(ctx, upload.ID, repository.OCIBlob{Digest: blobDigest, ObjectKey: blobKey, Size: int64(len(blobBody))}); err != nil {
		t.Fatal(err)
	}
	manifestBody := []byte(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json","config":{"mediaType":"application/vnd.oci.empty.v1+json","digest":"` + blobDigest + `","size":` + utoa(uint64(len(blobBody))) + `},"layers":[]}`)
	manifestSum := sha256.Sum256(manifestBody)
	manifestDigest := "sha256:" + hex.EncodeToString(manifestSum[:])
	manifestKey := "native/oci/manifests/" + hex.EncodeToString(manifestSum[:])
	if err = objects.Put(ctx, manifestKey, manifestBody); err != nil {
		t.Fatal(err)
	}
	manifest, err := store.PutOCIManifest(ctx, repository.OCIManifest{RepositoryID: repo.ID, Name: "app", Digest: manifestDigest, ObjectKey: manifestKey, MediaType: "application/vnd.oci.image.manifest.v1+json", Size: int64(len(manifestBody))}, "latest")
	if err != nil {
		t.Fatal(err)
	}
	enableQuarantineReadPolicy(t, store, repo.ID)
	quarantineReadIdentity(t, store, repo, manifest.Name, manifest.Digest)
	handler := NewGatewayHandler(Dependencies{NativeOCIObjectStore: objects}, store, TestAdapter{}, testAuthenticator())
	request := httptest.NewRequest(http.MethodGet, "/v2/"+repo.Name+"/app/blobs/"+blobDigest, nil)
	authorize(request, "resolver-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), `"code":"DENIED"`) || !strings.Contains(response.Body.String(), repository.ArtifactQuarantinedReason) {
		t.Fatalf("blob closure=%d body=%q", response.Code, response.Body.String())
	}
}

func TestOCIQuarantineReadPolicyRecursivelyBlocksIndexClosure(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	objects := NewMemoryOCIObjectStore()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: "oci-read-index", Name: "oci-read-index", Format: repository.FormatOCI})
	if err != nil {
		t.Fatal(err)
	}
	putObject := func(prefix string, body []byte) (string, string) {
		t.Helper()
		sum := sha256.Sum256(body)
		digest := "sha256:" + hex.EncodeToString(sum[:])
		key := prefix + hex.EncodeToString(sum[:])
		if err := objects.Put(ctx, key, body); err != nil {
			t.Fatal(err)
		}
		return digest, key
	}
	layerBody := []byte("nested quarantined layer")
	layerDigest, layerKey := putObject("native/oci/blobs/", layerBody)
	upload := repository.OCIUpload{ID: "oci-read-index-upload", RepositoryID: repo.ID, Name: "app", State: "open", ExpiresAt: time.Now().Add(time.Hour)}
	if _, err = store.CreateOCIUpload(ctx, upload); err != nil {
		t.Fatal(err)
	}
	if _, err = store.CompleteOCIUpload(ctx, upload.ID, repository.OCIBlob{Digest: layerDigest, ObjectKey: layerKey, Size: int64(len(layerBody))}); err != nil {
		t.Fatal(err)
	}
	childBody := []byte(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json","config":{"digest":"` + layerDigest + `","size":` + utoa(uint64(len(layerBody))) + `},"layers":[]}`)
	childDigest, childKey := putObject("native/oci/manifests/", childBody)
	if _, err = store.PutOCIManifest(ctx, repository.OCIManifest{RepositoryID: repo.ID, Name: "app", Digest: childDigest, ObjectKey: childKey, MediaType: "application/vnd.oci.image.manifest.v1+json", Size: int64(len(childBody))}, "amd64"); err != nil {
		t.Fatal(err)
	}
	indexBody := []byte(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.index.v1+json","manifests":[{"digest":"` + childDigest + `","size":` + utoa(uint64(len(childBody))) + `}]}`)
	indexDigest, indexKey := putObject("native/oci/manifests/", indexBody)
	index, err := store.PutOCIManifest(ctx, repository.OCIManifest{RepositoryID: repo.ID, Name: "app", Digest: indexDigest, ObjectKey: indexKey, MediaType: "application/vnd.oci.image.index.v1+json", Size: int64(len(indexBody))}, "latest")
	if err != nil {
		t.Fatal(err)
	}
	enableQuarantineReadPolicy(t, store, repo.ID)
	quarantineReadIdentity(t, store, repo, "app", index.Digest)
	if value, lookupErr := store.GetArtifactQuarantine(ctx, repo.ID, repository.FormatOCI, "app", index.Digest); lookupErr != nil || value.State != repository.ArtifactQuarantineStateQuarantined {
		t.Fatalf("index quarantine=%#v err=%v", value, lookupErr)
	}
	readHandler := nativeOCIHandler{store: store, objects: objects, readPolicies: store, quarantine: store}
	if blocked, blockErr := readHandler.ociDigestReadBlocked(ctx, repo, "app", childDigest); blockErr != nil || !blocked {
		t.Fatalf("child closure blocked=%v err=%v", blocked, blockErr)
	}
	createV2Group(t, store, "oci-read-index-group", repository.FormatOCI, repository.GroupMember{RepositoryID: repo.ID})
	handler := NewGatewayHandler(Dependencies{NativeOCIObjectStore: objects}, store, TestAdapter{}, testAuthenticator())
	for _, path := range []string{
		"/v2/" + repo.Name + "/app/manifests/" + childDigest,
		"/v2/oci-read-index-group/app/manifests/" + childDigest,
		"/v2/" + repo.Name + "/app/blobs/" + layerDigest,
		"/v2/oci-read-index-group/app/blobs/" + layerDigest,
	} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		authorize(request, "resolver-secret")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), `"code":"DENIED"`) {
			t.Fatalf("recursive closure %s=%d body=%q", path, response.Code, response.Body.String())
		}
	}
	requireQuarantineReadDeniedAudit(t, store, repository.FormatOCI)
	audits, err := store.ListAudits(ctx, repository.AuditQuery{Repository: "oci-read-index-group"})
	if err != nil {
		t.Fatal(err)
	}
	foundGroupMember := false
	for _, audit := range audits {
		foundGroupMember = foundGroupMember || audit.MemberName == repo.Name && audit.AuthorizationSource == "quarantine_read_policy" && audit.Outcome == repository.AuditAccessDenied
	}
	if !foundGroupMember {
		t.Fatalf("missing group/member quarantine audit: %#v", audits)
	}
}

func TestOCIQuarantineReadPolicyPaginationSkipsBlockedTagsAndReferrers(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	objects := NewMemoryOCIObjectStore()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: "oci-read-pagination", Name: "oci-read-pagination", Format: repository.FormatOCI})
	if err != nil {
		t.Fatal(err)
	}
	enableQuarantineReadPolicy(t, store, repo.ID)

	digest := func(marker byte) string { return "sha256:" + strings.Repeat(string(marker), 64) }
	for index, tag := range []string{"a-blocked", "b-blocked", "c-visible"} {
		manifestDigest := digest(byte('1' + index))
		objectKey := "manifest-" + tag
		if err = objects.Put(ctx, objectKey, []byte(`{"schemaVersion":2}`)); err != nil {
			t.Fatal(err)
		}
		if _, err = store.PutOCIManifest(ctx, repository.OCIManifest{RepositoryID: repo.ID, Name: "app", Digest: manifestDigest, ObjectKey: objectKey, MediaType: "application/vnd.oci.image.manifest.v1+json", Size: 2}, tag); err != nil {
			t.Fatal(err)
		}
		if index < 2 {
			quarantineReadIdentity(t, store, repo, "app", manifestDigest)
		}
	}

	subject := digest('9')
	if _, err = store.PutOCIManifest(ctx, repository.OCIManifest{RepositoryID: repo.ID, Name: "app", Digest: subject, ObjectKey: "subject", MediaType: "application/vnd.oci.image.manifest.v1+json", Size: 2}, "subject"); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 3; index++ {
		manifestDigest := digest(byte('4' + index))
		objectKey := "referrer-" + string(rune('a'+index))
		if err = objects.Put(ctx, objectKey, []byte(`{"schemaVersion":2}`)); err != nil {
			t.Fatal(err)
		}
		if _, err = store.PutOCIManifest(ctx, repository.OCIManifest{RepositoryID: repo.ID, Name: "app", Digest: manifestDigest, ObjectKey: objectKey, MediaType: "application/vnd.oci.image.manifest.v1+json", Size: 2, SubjectDigest: subject, ArtifactType: "application/vnd.example.attestation"}, objectKey); err != nil {
			t.Fatal(err)
		}
		if index < 2 {
			quarantineReadIdentity(t, store, repo, "app", manifestDigest)
		}
	}
	for index, imageName := range []string{"a-blocked-image", "b-blocked-image", "c-visible-image"} {
		manifestDigest := digest(byte('a' + index))
		objectKey := "catalog-" + imageName
		body := []byte(`{"schemaVersion":2}`)
		if err = objects.Put(ctx, objectKey, body); err != nil {
			t.Fatal(err)
		}
		if _, err = store.PutOCIManifest(ctx, repository.OCIManifest{RepositoryID: repo.ID, Name: imageName, Digest: manifestDigest, ObjectKey: objectKey, MediaType: "application/vnd.oci.image.manifest.v1+json", Size: int64(len(body))}, "latest"); err != nil {
			t.Fatal(err)
		}
		if index < 2 {
			quarantineReadIdentity(t, store, repo, imageName, manifestDigest)
		}
	}

	createV2Group(t, store, "oci-read-pagination-group", repository.FormatOCI, repository.GroupMember{RepositoryID: repo.ID})
	handler := NewGatewayHandler(Dependencies{NativeOCIObjectStore: objects}, store, TestAdapter{}, testAuthenticator())
	for _, path := range []string{
		"/v2/" + repo.Name + "/app/tags/list?n=1",
		"/v2/oci-read-pagination-group/app/tags/list?n=1",
	} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		authorize(request, "resolver-secret")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "c-visible") || strings.Contains(response.Body.String(), "a-blocked") || strings.Contains(response.Body.String(), "b-blocked") {
			t.Fatalf("tags %s=%d body=%q", path, response.Code, response.Body.String())
		}
	}

	referrersRequest := httptest.NewRequest(http.MethodGet, "/v2/"+repo.Name+"/app/referrers/"+subject+"?n=1", nil)
	authorize(referrersRequest, "resolver-secret")
	referrersResponse := httptest.NewRecorder()
	handler.ServeHTTP(referrersResponse, referrersRequest)
	if referrersResponse.Code != http.StatusOK || !strings.Contains(referrersResponse.Body.String(), digest('6')) || strings.Contains(referrersResponse.Body.String(), digest('4')) || strings.Contains(referrersResponse.Body.String(), digest('5')) {
		t.Fatalf("referrers=%d body=%q", referrersResponse.Code, referrersResponse.Body.String())
	}

	catalogRequest := httptest.NewRequest(http.MethodGet, "/v2/_catalog?n=1&last=oci-read-pagination%2Fapp", nil)
	authorize(catalogRequest, "admin-secret")
	catalogResponse := httptest.NewRecorder()
	handler.ServeHTTP(catalogResponse, catalogRequest)
	if catalogResponse.Code != http.StatusOK || !strings.Contains(catalogResponse.Body.String(), "c-visible-image") || strings.Contains(catalogResponse.Body.String(), "a-blocked-image") || strings.Contains(catalogResponse.Body.String(), "b-blocked-image") {
		t.Fatalf("catalog=%d body=%q", catalogResponse.Code, catalogResponse.Body.String())
	}
}
