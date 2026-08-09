package app

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

func TestNativePyPIHostedUploadAndSimpleRead(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	objects := NewMemoryOCIObjectStore()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: "pypi-hosted", Name: "python", Format: repository.FormatPyPI})
	if err != nil {
		t.Fatal(err)
	}
	wheel := pypiFixtureWheel(t, "gateway_widget", "1.2.3")
	sum := sha256.Sum256(wheel)
	digest := hex.EncodeToString(sum[:])
	body := new(bytes.Buffer)
	writer := multipart.NewWriter(body)
	for key, value := range map[string]string{":action": "file_upload", "name": "gateway-widget", "version": "1.2.3", "filetype": "bdist_wheel", "pyversion": "py3", "sha256_digest": digest} {
		if err = writer.WriteField(key, value); err != nil {
			t.Fatal(err)
		}
	}
	part, err := writer.CreateFormFile("content", "gateway_widget-1.2.3-py3-none-any.whl")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = part.Write(wheel); err != nil {
		t.Fatal(err)
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	handler := NewGatewayHandler(Dependencies{NativePyPIObjectStore: objects}, store, TestAdapter{}, testAuthenticator())
	upload := httptest.NewRequest(http.MethodPost, "/pypi/"+repo.Name+"/legacy/", body)
	upload.Header.Set("Content-Type", writer.FormDataContentType())
	authorize(upload, "resolver-secret")
	uploaded := httptest.NewRecorder()
	handler.ServeHTTP(uploaded, upload)
	if uploaded.Code != http.StatusCreated {
		t.Fatalf("upload=%d body=%s", uploaded.Code, uploaded.Body.String())
	}
	simpleRequest := httptest.NewRequest(http.MethodGet, "/pypi/"+repo.Name+"/simple/gateway-widget/", nil)
	authorize(simpleRequest, "resolver-secret")
	simple := httptest.NewRecorder()
	handler.ServeHTTP(simple, simpleRequest)
	filename := "gateway_widget-1.2.3-py3-none-any.whl"
	if simple.Code != http.StatusOK || !strings.Contains(simple.Body.String(), filename+`#sha256=`+digest) {
		t.Fatalf("simple=%d body=%s", simple.Code, simple.Body.String())
	}
	downloadRequest := httptest.NewRequest(http.MethodGet, "/pypi/"+repo.Name+"/packages/"+filename, nil)
	authorize(downloadRequest, "resolver-secret")
	download := httptest.NewRecorder()
	handler.ServeHTTP(download, downloadRequest)
	if download.Code != http.StatusOK || !bytes.Equal(download.Body.Bytes(), wheel) {
		t.Fatalf("download=%d bytes=%d", download.Code, download.Body.Len())
	}
	foundUpload, foundSimple, foundDownload := false, false, false
	for _, audit := range store.Audits {
		if audit.Format != "pypi" || audit.Repository != repo.Name || audit.Actor != "build-agent" || audit.Outcome != repository.AuditResolved {
			continue
		}
		foundUpload = foundUpload || audit.Operation == "post" && audit.Resource == "gateway-widget@1.2.3" && audit.Representation == "distribution" && audit.Status == http.StatusCreated && audit.Bytes == int64(len(wheel))
		foundSimple = foundSimple || audit.Operation == "get" && audit.Resource == "gateway-widget" && audit.Representation == "project" && audit.Status == http.StatusOK && audit.CacheDisposition == "bypass"
		foundDownload = foundDownload || audit.Operation == "get" && audit.Resource == "gateway-widget@1.2.3" && audit.Representation == "distribution" && audit.Status == http.StatusOK && audit.Bytes == int64(len(wheel))
	}
	if !foundUpload || !foundSimple || !foundDownload {
		t.Fatalf("incomplete hosted PyPI audit: upload=%t simple=%t download=%t audits=%#v", foundUpload, foundSimple, foundDownload, store.Audits)
	}
}

func TestNativePyPIRealTwineUploadAndPipDownload(t *testing.T) {
	if os.Getenv("ARTIFACT_GATEWAY_PYPI_CLI_E2E") == "" {
		t.Skip("set ARTIFACT_GATEWAY_PYPI_CLI_E2E=1 to run real twine/pip acceptance")
	}
	python := os.Getenv("ARTIFACT_GATEWAY_PYTHON")
	if python == "" {
		python = "python3"
	}
	ctx := context.Background()
	store := repository.NewMemoryStore()
	objects := NewMemoryOCIObjectStore()
	hosted, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: "pypi-cli", Name: "python", Format: repository.FormatPyPI})
	if err != nil {
		t.Fatal(err)
	}
	proxyWheel := pypiFixtureWheel(t, "proxy_widget", "2.4.0")
	proxySum := sha256.Sum256(proxyWheel)
	proxyFilename := "proxy_widget-2.4.0-py3-none-any.whl"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/simple/proxy-widget/":
			w.Header().Set("Content-Type", "text/html")
			_, _ = io.WriteString(w, `<a href="/packages/`+proxyFilename+`#sha256=`+hex.EncodeToString(proxySum[:])+`">`+proxyFilename+`</a>`)
		case "/packages/" + proxyFilename:
			_, _ = w.Write(proxyWheel)
		default:
			http.NotFound(w, r)
		}
	}))
	proxy, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: "pypi-cli-proxy", Name: "python-proxy", Format: repository.FormatPyPI, Type: repository.RepositoryTypeProxy, Endpoint: upstream.URL})
	if err != nil {
		t.Fatal(err)
	}
	group, _, err := store.CreateHostedGroupIdempotently(ctx, repository.HostedGroup{ID: "pypi-cli-group", Name: "python-all", Format: repository.FormatPyPI, Members: []repository.GroupMember{{RepositoryID: hosted.ID, Position: 0}, {RepositoryID: proxy.ID, Position: 1}}}, "admin", "pypi-cli-group", "payload")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(NewGatewayHandler(Dependencies{NativePyPIObjectStore: objects}, store, TestAdapter{}, testAuthenticator(), UpstreamClient{HTTPClient: upstream.Client()}))
	defer server.Close()
	temporary := t.TempDir()
	wheelPath := filepath.Join(temporary, "gateway_widget-1.2.3-py3-none-any.whl")
	if err = os.WriteFile(wheelPath, pypiFixtureWheel(t, "gateway_widget", "1.2.3"), 0o600); err != nil {
		t.Fatal(err)
	}
	run := func(arguments ...string) string {
		t.Helper()
		command := exec.CommandContext(ctx, python, arguments...)
		command.Env = append(os.Environ(), "TWINE_NON_INTERACTIVE=1")
		output, runErr := command.CombinedOutput()
		if runErr != nil {
			t.Fatalf("%s %v failed: %v\n%s", python, arguments, runErr, output)
		}
		return string(output)
	}
	run("-m", "twine", "upload", "--disable-progress-bar", "--repository-url", server.URL+"/pypi/python/legacy/", "--username", "ci", "--password", "resolver-secret", wheelPath)
	downloadDirectory := filepath.Join(temporary, "download")
	if err = os.Mkdir(downloadDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	authenticatedURL := strings.Replace(server.URL, "http://", "http://ci:resolver-secret@", 1)
	run("-m", "pip", "download", "--disable-pip-version-check", "--no-deps", "--keyring-provider", "disabled", "--index-url", authenticatedURL+"/pypi/python/simple/", "--trusted-host", strings.TrimPrefix(server.URL, "http://"), "--dest", downloadDirectory, "gateway-widget==1.2.3")
	downloaded, err := os.ReadFile(filepath.Join(downloadDirectory, filepath.Base(wheelPath)))
	if err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(wheelPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(downloaded, original) {
		t.Fatal("pip downloaded bytes do not match twine upload")
	}
	groupDownload := filepath.Join(temporary, "group-download")
	if err = os.Mkdir(groupDownload, 0o700); err != nil {
		t.Fatal(err)
	}
	authenticatedGroupURL := strings.Replace(server.URL, "http://", "http://ci:resolver-secret@", 1) + "/pypi/" + group.Name + "/simple/"
	run("-m", "pip", "download", "--disable-pip-version-check", "--no-deps", "--keyring-provider", "disabled", "--index-url", authenticatedGroupURL, "--trusted-host", strings.TrimPrefix(server.URL, "http://"), "--dest", groupDownload, "proxy-widget==2.4.0")
	upstream.Close()
	offlineDownload := filepath.Join(temporary, "offline-download")
	if err = os.Mkdir(offlineDownload, 0o700); err != nil {
		t.Fatal(err)
	}
	authenticatedProxyURL := strings.Replace(server.URL, "http://", "http://ci:resolver-secret@", 1) + "/pypi/" + proxy.Name + "/simple/"
	run("-m", "pip", "download", "--disable-pip-version-check", "--no-deps", "--keyring-provider", "disabled", "--index-url", authenticatedProxyURL, "--trusted-host", strings.TrimPrefix(server.URL, "http://"), "--dest", offlineDownload, "proxy-widget==2.4.0")
	offlineBytes, err := os.ReadFile(filepath.Join(offlineDownload, proxyFilename))
	if err != nil || !bytes.Equal(offlineBytes, proxyWheel) {
		t.Fatalf("offline proxy bytes=%d err=%v", len(offlineBytes), err)
	}
}

func TestNativePyPIProxyCachesVerifiedDistributionAndServesOffline(t *testing.T) {
	wheel := pypiFixtureWheel(t, "proxy_widget", "2.4.0")
	sum := sha256.Sum256(wheel)
	digest := hex.EncodeToString(sum[:])
	filename := "proxy_widget-2.4.0-py3-none-any.whl"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/simple/proxy-widget/":
			w.Header().Set("Content-Type", "text/html")
			_, _ = io.WriteString(w, `<a href="/packages/`+filename+`#sha256=`+digest+`">`+filename+`</a>`)
		case "/packages/" + filename:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(wheel)
		default:
			http.NotFound(w, r)
		}
	}))
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: "pypi-proxy", Name: "python-proxy", Format: repository.FormatPyPI, Type: repository.RepositoryTypeProxy, Endpoint: upstream.URL})
	if err != nil {
		t.Fatal(err)
	}
	objects := NewMemoryOCIObjectStore()
	handler := NewGatewayHandler(Dependencies{NativePyPIObjectStore: objects}, store, TestAdapter{}, testAuthenticator(), UpstreamClient{HTTPClient: upstream.Client()})
	request := func(path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		authorize(req, "resolver-secret")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		return response
	}
	if response := request("/pypi/" + repo.Name + "/simple/proxy-widget/"); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), filename+`#sha256=`+digest) {
		t.Fatalf("simple=%d %s", response.Code, response.Body.String())
	}
	if response := request("/pypi/" + repo.Name + "/packages/" + filename); response.Code != http.StatusOK || !bytes.Equal(response.Body.Bytes(), wheel) {
		t.Fatalf("download=%d bytes=%d", response.Code, response.Body.Len())
	}
	upstream.Close()
	if response := request("/pypi/" + repo.Name + "/simple/proxy-widget/"); response.Code != http.StatusOK || response.Header().Get("Warning") == "" {
		t.Fatalf("offline simple=%d warning=%q body=%s", response.Code, response.Header().Get("Warning"), response.Body.String())
	}
	if response := request("/pypi/" + repo.Name + "/packages/" + filename); response.Code != http.StatusOK || !bytes.Equal(response.Body.Bytes(), wheel) {
		t.Fatalf("offline download=%d bytes=%d", response.Code, response.Body.Len())
	}
	dispositions := make(map[string]int)
	for _, audit := range store.Audits {
		if audit.Format == "pypi" && audit.Repository == repo.Name && audit.Outcome == repository.AuditResolved {
			dispositions[audit.Representation+":"+audit.CacheDisposition]++
		}
	}
	if dispositions["project:miss"] != 1 || dispositions["distribution:miss"] != 1 || dispositions["project:stale"] != 1 || dispositions["distribution:hit"] != 1 {
		t.Fatalf("unexpected proxy PyPI audit dispositions: %#v audits=%#v", dispositions, store.Audits)
	}
}

func TestNativePyPIAnonymousReadAndManagedGrantBoundaries(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	objects := NewMemoryOCIObjectStore()
	public, err := store.CreateHostedRepository(ctx, repository.HostedRepository{
		ID: "pypi-public", Name: "pypi-public", Format: repository.FormatPyPI, AnonymousRead: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	private, err := store.CreateHostedRepository(ctx, repository.HostedRepository{
		ID: "pypi-private", Name: "pypi-private", Format: repository.FormatPyPI,
	})
	if err != nil {
		t.Fatal(err)
	}
	publicFile := publishPyPITestFile(t, store, objects, public, "public-widget", "1.0.0")
	_ = publishPyPITestFile(t, store, objects, private, "private-widget", "1.0.0")
	if _, err = store.ReplaceRepositoryGrants(ctx, private.ID, nil, "1"); err != nil {
		t.Fatal(err)
	}
	handler := NewGatewayHandler(Dependencies{NativePyPIObjectStore: objects}, store, TestAdapter{}, testAuthenticator())
	request := func(path, token string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		if token != "" {
			authorize(req, token)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		return response
	}
	publicProject := "/pypi/" + public.Name + "/simple/public-widget/"
	if response := request(publicProject, ""); response.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous without global policy=%d", response.Code)
	}
	if _, err = store.ReplaceAnonymousAccessPolicy(ctx, repository.AnonymousAccessPolicy{Enabled: true}, "1"); err != nil {
		t.Fatal(err)
	}
	if response := request(publicProject, ""); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), publicFile.Filename) {
		t.Fatalf("anonymous project=%d body=%s", response.Code, response.Body.String())
	}
	if response := request("/pypi/"+public.Name+"/packages/"+publicFile.Filename, ""); response.Code != http.StatusOK {
		t.Fatalf("anonymous distribution=%d body=%s", response.Code, response.Body.String())
	}
	if response := request("/pypi/"+private.Name+"/simple/private-widget/", ""); response.Code != http.StatusUnauthorized {
		t.Fatalf("private anonymous=%d", response.Code)
	}
	if response := request("/pypi/"+private.Name+"/simple/private-widget/", "resolver-secret"); response.Code != http.StatusForbidden {
		t.Fatalf("ungranted authenticated read=%d body=%s", response.Code, response.Body.String())
	}
}

func TestNativePyPIGroupFiltersMembersByManagedGrant(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	objects := NewMemoryOCIObjectStore()
	denied, err := store.CreateHostedRepository(ctx, repository.HostedRepository{
		ID: "pypi-group-denied", Name: "pypi-group-denied", Format: repository.FormatPyPI,
	})
	if err != nil {
		t.Fatal(err)
	}
	allowed, err := store.CreateHostedRepository(ctx, repository.HostedRepository{
		ID: "pypi-group-allowed", Name: "pypi-group-allowed", Format: repository.FormatPyPI,
	})
	if err != nil {
		t.Fatal(err)
	}
	deniedFile := publishPyPITestFile(t, store, objects, denied, "shared-widget", "9.0.0")
	allowedFile := publishPyPITestFile(t, store, objects, allowed, "shared-widget", "1.0.0")
	if _, err = store.ReplaceRepositoryGrants(ctx, denied.ID, nil, "1"); err != nil {
		t.Fatal(err)
	}
	if _, err = store.ReplaceRepositoryGrants(ctx, allowed.ID, []repository.RepositoryGrant{{
		Principal: "build-agent", Scopes: []string{"repositories:read"}, ResourcePrefix: "shared-widget",
	}}, "1"); err != nil {
		t.Fatal(err)
	}
	group, _, err := store.CreateHostedGroupIdempotently(ctx, repository.HostedGroup{
		ID: "pypi-authorized-group", Name: "pypi-authorized-group", Format: repository.FormatPyPI,
		Members: []repository.GroupMember{{RepositoryID: denied.ID}, {RepositoryID: allowed.ID, Position: 1}},
	}, "admin", "pypi-authorized-group", "payload")
	if err != nil {
		t.Fatal(err)
	}
	handler := NewGatewayHandler(Dependencies{NativePyPIObjectStore: objects}, store, TestAdapter{}, testAuthenticator())
	request := httptest.NewRequest(http.MethodGet, "/pypi/"+group.Name+"/simple/shared-widget/", nil)
	authorize(request, "resolver-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), allowedFile.Filename) || strings.Contains(response.Body.String(), deniedFile.Filename) {
		t.Fatalf("authorized group project=%d body=%s", response.Code, response.Body.String())
	}
	for _, audit := range store.Audits {
		if audit.GroupName == group.Name && audit.MemberName == denied.Name && audit.Outcome == repository.AuditAccessDenied {
			return
		}
	}
	t.Fatalf("missing PyPI group member denial audit: %#v", store.Audits)
}

func TestNativePyPIGroupMergesMembersAndPrefersHostedConflict(t *testing.T) {
	ctx := context.Background()
	project := "group-widget"
	hostedFilename := "group_widget-1.0.0-py3-none-any.whl"
	hostedBytes := []byte("hosted-copy")
	hostedSum := sha256.Sum256(hostedBytes)
	proxyConflict := pypiFixtureWheel(t, "group_widget", "1.0.0")
	proxyLatest := pypiFixtureWheel(t, "group_widget", "2.0.0")
	latestFilename := "group_widget-2.0.0-py3-none-any.whl"
	conflictSum := sha256.Sum256(proxyConflict)
	latestSum := sha256.Sum256(proxyLatest)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/simple/group-widget/":
			w.Header().Set("Content-Type", "text/html")
			_, _ = io.WriteString(w,
				`<a href="/packages/`+hostedFilename+`#sha256=`+hex.EncodeToString(conflictSum[:])+`">conflict</a>`+
					`<a href="/packages/`+latestFilename+`#sha256=`+hex.EncodeToString(latestSum[:])+`">latest</a>`)
		case "/packages/" + hostedFilename:
			_, _ = w.Write(proxyConflict)
		case "/packages/" + latestFilename:
			_, _ = w.Write(proxyLatest)
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	store := repository.NewMemoryStore()
	hosted, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: "pypi-group-hosted", Name: "python-private", Format: repository.FormatPyPI})
	if err != nil {
		t.Fatal(err)
	}
	proxy, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: "pypi-group-proxy", Name: "python-upstream", Format: repository.FormatPyPI, Type: repository.RepositoryTypeProxy, Endpoint: upstream.URL})
	if err != nil {
		t.Fatal(err)
	}
	objects := NewMemoryOCIObjectStore()
	hostedKey := "native/pypi/sha256/" + hex.EncodeToString(hostedSum[:])
	if err = objects.Put(ctx, hostedKey, hostedBytes); err != nil {
		t.Fatal(err)
	}
	if _, err = store.PublishPyPIFile(ctx, repository.PyPIFile{RepositoryID: hosted.ID, Project: project, Version: "1.0.0", Filename: hostedFilename, FileType: "bdist_wheel", Digest: "sha256:" + hex.EncodeToString(hostedSum[:]), ObjectKey: hostedKey, Size: int64(len(hostedBytes))}); err != nil {
		t.Fatal(err)
	}
	group, _, err := store.CreateHostedGroupIdempotently(ctx, repository.HostedGroup{ID: "pypi-group", Name: "python-all", Format: repository.FormatPyPI, Members: []repository.GroupMember{{RepositoryID: proxy.ID, Position: 0}, {RepositoryID: hosted.ID, Position: 1}}}, "admin", "pypi-group", "payload")
	if err != nil {
		t.Fatal(err)
	}
	handler := NewGatewayHandler(Dependencies{NativePyPIObjectStore: objects}, store, TestAdapter{}, testAuthenticator(), UpstreamClient{HTTPClient: upstream.Client()})
	get := func(path string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		authorize(request, "resolver-secret")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	response := get("/pypi/" + group.Name + "/simple/" + project + "/")
	if response.Code != http.StatusOK || strings.Count(response.Body.String(), hostedFilename) != 2 || !strings.Contains(response.Body.String(), latestFilename) {
		// Each filename appears in href and link text; the conflict must not add a second anchor.
		t.Fatalf("simple=%d body=%s", response.Code, response.Body.String())
	}
	if downloaded := get("/pypi/" + group.Name + "/packages/" + hostedFilename); downloaded.Code != http.StatusOK || !bytes.Equal(downloaded.Body.Bytes(), hostedBytes) {
		t.Fatalf("hosted conflict=%d body=%q", downloaded.Code, downloaded.Body.Bytes())
	}
	if downloaded := get("/pypi/" + group.Name + "/packages/" + latestFilename); downloaded.Code != http.StatusOK || !bytes.Equal(downloaded.Body.Bytes(), proxyLatest) {
		t.Fatalf("proxy latest=%d bytes=%d", downloaded.Code, downloaded.Body.Len())
	}
	foundGroupResolution, foundHostedDownload, foundProxyDownload := false, false, false
	for _, audit := range store.Audits {
		if audit.Format != "pypi" || audit.Repository != group.Name || audit.Outcome != repository.AuditResolved {
			continue
		}
		foundGroupResolution = foundGroupResolution || audit.Resource == project && audit.MemberName == "" && audit.Representation == ""
		foundHostedDownload = foundHostedDownload || audit.Resource == project+"@1.0.0" && audit.MemberName == hosted.Name && audit.CacheDisposition == "bypass"
		foundProxyDownload = foundProxyDownload || audit.Resource == project+"@2.0.0" && audit.MemberName == proxy.Name && audit.CacheDisposition == "miss"
	}
	if !foundGroupResolution || !foundHostedDownload || !foundProxyDownload {
		t.Fatalf("incomplete group PyPI audit: resolution=%t hosted=%t proxy=%t audits=%#v", foundGroupResolution, foundHostedDownload, foundProxyDownload, store.Audits)
	}
}

func pypiFixtureWheel(t *testing.T, distribution, version string) []byte {
	t.Helper()
	buffer := new(bytes.Buffer)
	archive := zip.NewWriter(buffer)
	files := map[string]string{
		distribution + "/__init__.py":                        "__version__ = \"" + version + "\"\n",
		distribution + "-" + version + ".dist-info/METADATA": "Metadata-Version: 2.1\nName: " + strings.ReplaceAll(distribution, "_", "-") + "\nVersion: " + version + "\n",
		distribution + "-" + version + ".dist-info/WHEEL":    "Wheel-Version: 1.0\nGenerator: artifact-gateway-test\nRoot-Is-Purelib: true\nTag: py3-none-any\n",
		distribution + "-" + version + ".dist-info/RECORD":   "",
	}
	for name, contents := range files {
		entry, err := archive.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = io.WriteString(entry, contents); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func publishPyPITestFile(t *testing.T, store *repository.MemoryStore, objects OCIObjectStore, repo repository.HostedRepository, project, version string) repository.PyPIFile {
	t.Helper()
	distribution := strings.ReplaceAll(project, "-", "_")
	data := pypiFixtureWheel(t, distribution, version)
	sum := sha256.Sum256(data)
	digest := hex.EncodeToString(sum[:])
	file := repository.PyPIFile{
		RepositoryID: repo.ID, Project: project, Version: version,
		Filename: distribution + "-" + version + "-py3-none-any.whl", FileType: "bdist_wheel",
		Digest: "sha256:" + digest, ObjectKey: "native/pypi/sha256/" + digest, Size: int64(len(data)),
	}
	if err := objects.Put(context.Background(), file.ObjectKey, data); err != nil {
		t.Fatal(err)
	}
	stored, err := store.PublishPyPIFile(context.Background(), file)
	if err != nil {
		t.Fatal(err)
	}
	return stored
}
