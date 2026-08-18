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

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

func publishQuarantineReadNPM(t *testing.T, store *repository.MemoryStore, objects *MemoryOCIObjectStore, repo repository.HostedRepository, packageName, version string, body []byte) repository.NPMVersion {
	t.Helper()
	sum := sha256.Sum256(body)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	ttarball := strings.TrimPrefix(packageName, "@scope/") + "-" + version + ".tgz"
	objectKey := "native/npm/sha256/" + hex.EncodeToString(sum[:])
	if err := objects.Put(context.Background(), objectKey, body); err != nil {
		t.Fatal(err)
	}
	item, err := store.PublishNPMVersion(context.Background(), repository.NPMVersion{
		RepositoryID: repo.ID, PackageName: packageName, Version: version,
		Digest: digest, TarballName: ttarball, ObjectKey: objectKey, Size: int64(len(body)),
		Manifest: json.RawMessage(`{"name":"` + packageName + `","version":"` + version + `"}`),
	}, map[string]string{"latest": version})
	if err != nil {
		t.Fatal(err)
	}
	return item
}

func TestNPMQuarantineReadPolicyHidesWholeVersionAndBlocksTarball(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	objects := NewMemoryOCIObjectStore()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: "npm-read-hosted", Name: "npm-read-hosted", Format: repository.FormatNPM})
	if err != nil {
		t.Fatal(err)
	}
	publishQuarantineReadNPM(t, store, objects, repo, "widget", "2.0.0", []byte("visible npm"))
	blocked := publishQuarantineReadNPM(t, store, objects, repo, "widget", "1.0.0", []byte("blocked npm"))
	enableQuarantineReadPolicy(t, store, repo.ID)
	quarantineReadIdentity(t, store, repo, "widget@1.0.0", blocked.Digest)
	handler := NewGatewayHandler(Dependencies{NativeNPMObjectStore: objects}, store, TestAdapter{}, testAuthenticator())

	packumentRequest := httptest.NewRequest(http.MethodGet, "/npm/"+repo.Name+"/widget", nil)
	authorize(packumentRequest, "resolver-secret")
	packumentResponse := httptest.NewRecorder()
	handler.ServeHTTP(packumentResponse, packumentRequest)
	if packumentResponse.Code != http.StatusOK {
		t.Fatalf("packument=%d body=%q", packumentResponse.Code, packumentResponse.Body.String())
	}
	var packument struct {
		Versions map[string]json.RawMessage `json:"versions"`
		DistTags map[string]string          `json:"dist-tags"`
	}
	if err = json.NewDecoder(packumentResponse.Body).Decode(&packument); err != nil {
		t.Fatal(err)
	}
	if packument.Versions["1.0.0"] != nil || packument.Versions["2.0.0"] == nil {
		t.Fatalf("filtered versions=%v", packument.Versions)
	}
	if _, leaked := packument.DistTags["latest"]; leaked {
		t.Fatalf("blocked dist-tag leaked: %v", packument.DistTags)
	}

	for _, method := range []string{http.MethodGet, http.MethodHead} {
		tarballRequest := httptest.NewRequest(method, "/npm/"+repo.Name+"/widget/-/"+blocked.TarballName, nil)
		authorize(tarballRequest, "resolver-secret")
		tarballResponse := httptest.NewRecorder()
		handler.ServeHTTP(tarballResponse, tarballRequest)
		if tarballResponse.Code != http.StatusForbidden || !strings.Contains(tarballResponse.Body.String(), repository.ArtifactQuarantinedReason) {
			t.Fatalf("%s tarball=%d body=%q", method, tarballResponse.Code, tarballResponse.Body.String())
		}
	}
	versionRequest := httptest.NewRequest(http.MethodGet, "/npm/"+repo.Name+"/widget/1.0.0", nil)
	authorize(versionRequest, "resolver-secret")
	versionResponse := httptest.NewRecorder()
	handler.ServeHTTP(versionResponse, versionRequest)
	if versionResponse.Code != http.StatusForbidden || !strings.Contains(versionResponse.Body.String(), repository.ArtifactQuarantinedReason) {
		t.Fatalf("version metadata=%d body=%q", versionResponse.Code, versionResponse.Body.String())
	}
	requireQuarantineReadDeniedAudit(t, store, repository.FormatNPM)
}

func TestNPMGroupDoesNotReintroduceQuarantinedVersionFromLowerPriorityMember(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	objects := NewMemoryOCIObjectStore()
	first, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: "npm-read-first", Name: "npm-read-first", Format: repository.FormatNPM})
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: "npm-read-second", Name: "npm-read-second", Format: repository.FormatNPM})
	if err != nil {
		t.Fatal(err)
	}
	blocked := publishQuarantineReadNPM(t, store, objects, first, "widget", "1.0.0", []byte("blocked member"))
	publishQuarantineReadNPM(t, store, objects, second, "widget", "1.0.0", []byte("fallback member"))
	enableQuarantineReadPolicy(t, store, first.ID)
	quarantineReadIdentity(t, store, first, "widget@1.0.0", blocked.Digest)
	createV2Group(t, store, "npm-read-group", repository.FormatNPM,
		repository.GroupMember{RepositoryID: first.ID},
		repository.GroupMember{RepositoryID: second.ID, Position: 1},
	)
	handler := NewGatewayHandler(Dependencies{NativeNPMObjectStore: objects}, store, TestAdapter{}, testAuthenticator())

	request := func(path string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		authorize(r, "resolver-secret")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	if response := request("/npm/npm-read-group/widget"); response.Code != http.StatusNotFound {
		t.Fatalf("group packument=%d body=%q", response.Code, response.Body.String())
	}
	if response := request("/npm/npm-read-group/widget/-/" + blocked.TarballName); response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), repository.ArtifactQuarantinedReason) {
		t.Fatalf("group tarball=%d body=%q", response.Code, response.Body.String())
	}
	if response := request("/npm/npm-read-group/widget/1.0.0"); response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), repository.ArtifactQuarantinedReason) {
		t.Fatalf("group version metadata=%d body=%q", response.Code, response.Body.String())
	}
}

func TestNPMGroupQuarantinedVersionClaimsDifferentTarballName(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	objects := NewMemoryOCIObjectStore()
	first, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: "npm-read-name-first", Name: "npm-read-name-first", Format: repository.FormatNPM})
	if err != nil {
		t.Fatal(err)
	}
	blocked := publishQuarantineReadNPM(t, store, objects, first, "widget", "1.0.0", []byte("blocked member"))
	second, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: "npm-read-name-second", Name: "npm-read-name-second", Format: repository.FormatNPM})
	if err != nil {
		t.Fatal(err)
	}
	lowerBody := []byte("lower alternate tarball")
	sum := sha256.Sum256(lowerBody)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	key := "native/npm/sha256/" + hex.EncodeToString(sum[:])
	if err = objects.Put(ctx, key, lowerBody); err != nil {
		t.Fatal(err)
	}
	if _, err = store.PublishNPMVersion(ctx, repository.NPMVersion{RepositoryID: second.ID, PackageName: "widget", Version: "1.0.0", Digest: digest, TarballName: "alternate.tgz", ObjectKey: key, Size: int64(len(lowerBody)), Manifest: json.RawMessage(`{"name":"widget","version":"1.0.0"}`)}, nil); err != nil {
		t.Fatal(err)
	}
	enableQuarantineReadPolicy(t, store, first.ID)
	quarantineReadIdentity(t, store, first, "widget@1.0.0", blocked.Digest)
	createV2Group(t, store, "npm-read-name-group", repository.FormatNPM, repository.GroupMember{RepositoryID: first.ID}, repository.GroupMember{RepositoryID: second.ID, Position: 1})
	handler := NewGatewayHandler(Dependencies{NativeNPMObjectStore: objects}, store, TestAdapter{}, testAuthenticator())
	request := httptest.NewRequest(http.MethodGet, "/npm/npm-read-name-group/widget/-/alternate.tgz", nil)
	authorize(request, "resolver-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), repository.ArtifactQuarantinedReason) {
		t.Fatalf("tarball-name bypass=%d body=%q", response.Code, response.Body.String())
	}
}
