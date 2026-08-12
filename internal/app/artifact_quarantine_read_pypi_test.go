package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

func publishQuarantineReadPyPIVersion(t *testing.T, store *repository.MemoryStore, objects *MemoryOCIObjectStore, repo repository.HostedRepository, project, version, suffix string) []repository.PyPIFile {
	t.Helper()
	filenames := []string{project + "-" + version + "-py3-none-any" + suffix + ".whl", project + "-" + version + suffix + ".tar.gz"}
	files := make([]repository.PyPIFile, 0, len(filenames))
	for _, filename := range filenames {
		body := []byte("pypi:" + filename)
		sum := sha256.Sum256(body)
		digest := "sha256:" + hex.EncodeToString(sum[:])
		key := "native/pypi/sha256/" + hex.EncodeToString(sum[:])
		if err := objects.Put(context.Background(), key, body); err != nil {
			t.Fatal(err)
		}
		files = append(files, repository.PyPIFile{RepositoryID: repo.ID, Project: project, Version: version, Filename: filename, Digest: digest, ObjectKey: key, Size: int64(len(body))})
	}
	stored, err := store.PublishPyPIVersion(context.Background(), files)
	if err != nil {
		t.Fatal(err)
	}
	return stored
}

func TestPyPIQuarantineReadPolicyBlocksEveryFileInVersionAndHidesMetadata(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	objects := NewMemoryOCIObjectStore()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: "pypi-read-hosted", Name: "pypi-read-hosted", Format: repository.FormatPyPI})
	if err != nil {
		t.Fatal(err)
	}
	blocked := publishQuarantineReadPyPIVersion(t, store, objects, repo, "widget", "1.0.0", "")
	visible := publishQuarantineReadPyPIVersion(t, store, objects, repo, "widget", "2.0.0", "")
	enableQuarantineReadPolicy(t, store, repo.ID)
	quarantineReadIdentity(t, store, repo, "widget@1.0.0", blocked[1].Digest)
	handler := NewGatewayHandler(Dependencies{NativePyPIObjectStore: objects}, store, TestAdapter{}, testAuthenticator())

	projectRequest := httptest.NewRequest(http.MethodGet, "/pypi/"+repo.Name+"/simple/widget/", nil)
	authorize(projectRequest, "resolver-secret")
	projectResponse := httptest.NewRecorder()
	handler.ServeHTTP(projectResponse, projectRequest)
	if projectResponse.Code != http.StatusOK || strings.Contains(projectResponse.Body.String(), blocked[0].Filename) || strings.Contains(projectResponse.Body.String(), blocked[1].Filename) || !strings.Contains(projectResponse.Body.String(), visible[0].Filename) {
		t.Fatalf("project=%d body=%q", projectResponse.Code, projectResponse.Body.String())
	}

	for _, file := range blocked {
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			request := httptest.NewRequest(method, "/pypi/"+repo.Name+"/packages/"+file.Filename, nil)
			authorize(request, "resolver-secret")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), repository.ArtifactQuarantinedReason) {
				t.Fatalf("%s download %s=%d body=%q", method, file.Filename, response.Code, response.Body.String())
			}
		}
	}
	requireQuarantineReadDeniedAudit(t, store, repository.FormatPyPI)
}

func TestPyPIGroupDoesNotReintroduceQuarantinedVersion(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	objects := NewMemoryOCIObjectStore()
	first, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: "pypi-read-first", Name: "pypi-read-first", Format: repository.FormatPyPI})
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: "pypi-read-second", Name: "pypi-read-second", Format: repository.FormatPyPI})
	if err != nil {
		t.Fatal(err)
	}
	blocked := publishQuarantineReadPyPIVersion(t, store, objects, first, "widget", "1.0.0", "")
	publishQuarantineReadPyPIVersion(t, store, objects, second, "widget", "1.0.0", "-fallback")
	enableQuarantineReadPolicy(t, store, first.ID)
	quarantineReadIdentity(t, store, first, "widget@1.0.0", blocked[0].Digest)
	createV2Group(t, store, "pypi-read-group", repository.FormatPyPI,
		repository.GroupMember{RepositoryID: first.ID},
		repository.GroupMember{RepositoryID: second.ID, Position: 1},
	)
	handler := NewGatewayHandler(Dependencies{NativePyPIObjectStore: objects}, store, TestAdapter{}, testAuthenticator())

	projectRequest := httptest.NewRequest(http.MethodGet, "/pypi/pypi-read-group/simple/widget/", nil)
	authorize(projectRequest, "resolver-secret")
	projectResponse := httptest.NewRecorder()
	handler.ServeHTTP(projectResponse, projectRequest)
	if projectResponse.Code != http.StatusNotFound {
		t.Fatalf("group project=%d body=%q", projectResponse.Code, projectResponse.Body.String())
	}

	downloadRequest := httptest.NewRequest(http.MethodGet, "/pypi/pypi-read-group/packages/"+blocked[1].Filename, nil)
	authorize(downloadRequest, "resolver-secret")
	downloadResponse := httptest.NewRecorder()
	handler.ServeHTTP(downloadResponse, downloadRequest)
	if downloadResponse.Code != http.StatusForbidden || !strings.Contains(downloadResponse.Body.String(), repository.ArtifactQuarantinedReason) {
		t.Fatalf("group download=%d body=%q", downloadResponse.Code, downloadResponse.Body.String())
	}
}

func TestPyPIGroupQuarantinedVersionClaimsDifferentFilename(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	objects := NewMemoryOCIObjectStore()
	first, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: "pypi-read-name-first", Name: "pypi-read-name-first", Format: repository.FormatPyPI})
	if err != nil {
		t.Fatal(err)
	}
	blocked := publishQuarantineReadPyPIVersion(t, store, objects, first, "widget", "1.0.0", "")
	second, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: "pypi-read-name-second", Name: "pypi-read-name-second", Format: repository.FormatPyPI})
	if err != nil {
		t.Fatal(err)
	}
	lower := publishQuarantineReadPyPIVersion(t, store, objects, second, "widget", "1.0.0", "-alternate")
	enableQuarantineReadPolicy(t, store, first.ID)
	quarantineReadIdentity(t, store, first, "widget@1.0.0", blocked[0].Digest)
	createV2Group(t, store, "pypi-read-name-group", repository.FormatPyPI, repository.GroupMember{RepositoryID: first.ID}, repository.GroupMember{RepositoryID: second.ID, Position: 1})
	handler := NewGatewayHandler(Dependencies{NativePyPIObjectStore: objects}, store, TestAdapter{}, testAuthenticator())
	request := httptest.NewRequest(http.MethodGet, "/pypi/pypi-read-name-group/packages/"+lower[0].Filename, nil)
	authorize(request, "resolver-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), repository.ArtifactQuarantinedReason) {
		t.Fatalf("filename bypass=%d body=%q", response.Code, response.Body.String())
	}
}
