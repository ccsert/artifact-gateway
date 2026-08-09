package app

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
	if _, err = store.ReplaceAnonymousAccessPolicy(context.Background(), repository.AnonymousAccessPolicy{Enabled: true}, "1"); err != nil {
		t.Fatal(err)
	}
	objects := NewMemoryOCIObjectStore()
	handler := NewGatewayHandler(Dependencies{NativeNPMObjectStore: objects}, store, TestAdapter{}, testAuthenticator())
	tarball := npmFixtureTarball(t, "@scope/widget", "1.2.3")
	publishBody := npmFixturePublishDocument(t, "@scope/widget", "1.2.3", "@scope/widget-1.2.3.tgz", tarball)

	publish := httptest.NewRequest(http.MethodPut, "/npm/npm-releases/@scope%2Fwidget", strings.NewReader(publishBody))
	publish.Header.Set("Authorization", "Bearer resolver-secret")
	published := httptest.NewRecorder()
	handler.ServeHTTP(published, publish)
	if published.Code != http.StatusCreated {
		t.Fatalf("publish=%d %s", published.Code, published.Body.String())
	}

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
	t.Helper()
	manifest, err := json.Marshal(map[string]string{"name": name, "version": version})
	if err != nil {
		t.Fatal(err)
	}
	var compressed bytes.Buffer
	gzipWriter := gzip.NewWriter(&compressed)
	tarWriter := tar.NewWriter(gzipWriter)
	if err = tarWriter.WriteHeader(&tar.Header{Name: "package/package.json", Mode: 0o644, Size: int64(len(manifest))}); err != nil {
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
