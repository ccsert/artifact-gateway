package app

import "github.com/ccsert/artifact-gateway/internal/objectstore"

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ccsert/artifact-gateway/internal/repository"
)

func TestRetiredNexusRepositoryRootsReturnNotFound(t *testing.T) {
	store := repository.NewMemoryStore()
	for _, format := range []repository.Format{repository.FormatMaven, repository.FormatRaw, repository.FormatNPM, repository.FormatPyPI, repository.FormatGo} {
		repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{
			ID: "native-" + string(format), Name: "native-" + string(format), Format: format,
		})
		if err != nil {
			t.Fatal(err)
		}
		createV2Group(t, store, "group-"+string(format), format, repository.GroupMember{RepositoryID: repo.ID, Position: 0})
	}
	handler := newTestGateway(t, GatewayOptions{Dependencies: Dependencies{}, Store: store, Adapter: TestAdapter{}, Authenticator: testAuthenticator(), Storage: ObjectStores{repository.FormatOCI: objectstore.NewMemoryStore(), repository.FormatRaw: objectstore.NewMemoryStore()}})
	for _, path := range []string{
		"/repository/", "/repository/native-maven/org/example/widget/1.0/widget-1.0.pom",
		"/repository/native-raw/releases/app.txt", "/repository/group-raw/releases/app.txt",
		"/repository/native-npm/@scope%2Fwidget", "/repository/group-npm/@scope%2Fwidget",
		"/repository/native-pypi/", "/repository/native-pypi/simple/widget/", "/repository/group-pypi/simple/widget/",
		"/repository/native-go/v1.2.3.zip", "/repository/native-go/example.com/widget/@v/list",
		"/repository/group-go/example.com/widget/@v/list", "/repository/group-maven/org/example/widget/1.0/widget-1.0.pom",
		"/go/native-go/v1.2.3.zip",
	} {
		for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodPut, http.MethodPost, http.MethodPatch, http.MethodDelete} {
			for _, authenticated := range []bool{false, true} {
				t.Run(method+path+map[bool]string{false: "/anonymous", true: "/authenticated"}[authenticated], func(t *testing.T) {
					request := httptest.NewRequest(method, path, strings.NewReader("retired route must not publish"))
					if authenticated {
						authorize(request, "resolver-secret")
					}
					response := httptest.NewRecorder()
					handler.ServeHTTP(response, request)
					if response.Code != http.StatusNotFound || response.Header().Get("Location") != "" || response.Header().Get("WWW-Authenticate") != "" {
						t.Fatalf("retired route status=%d headers=%v body=%q", response.Code, response.Header(), response.Body.String())
					}
				})
			}
		}
	}
}

func TestCanonicalRepositoryRoutesPreservesRawProtocolBehavior(t *testing.T) {
	store := repository.NewMemoryStore()
	if _, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{
		ID: "raw-migration-repository", Name: "raw-releases", Format: repository.FormatRaw,
	}); err != nil {
		t.Fatal(err)
	}
	handler := newTestGateway(t, GatewayOptions{Dependencies: Dependencies{}, Store: store, Adapter: TestAdapter{}, Authenticator: testAuthenticator(), Storage: ObjectStores{repository.FormatOCI: objectstore.NewMemoryStore(), repository.FormatRaw: objectstore.NewMemoryStore()}})

	request := func(method, path string, body []byte) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, bytes.NewReader(body))
		authorize(req, "resolver-secret")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		return response
	}

	put := request(http.MethodPut, "/raw/raw-releases/releases/app.txt", []byte("native raw artifact"))
	if put.Code != http.StatusCreated {
		t.Fatalf("canonical Raw PUT status=%d body=%q", put.Code, put.Body.String())
	}

	rangeRequest := httptest.NewRequest(http.MethodGet, "/raw/raw-releases/releases/app.txt", nil)
	rangeRequest.Header.Set("Range", "bytes=7-9")
	authorize(rangeRequest, "resolver-secret")
	ranged := httptest.NewRecorder()
	handler.ServeHTTP(ranged, rangeRequest)
	if ranged.Code != http.StatusPartialContent || ranged.Body.String() != "raw" {
		t.Fatalf("canonical Raw range status=%d body=%q", ranged.Code, ranged.Body.String())
	}

	canonical := request(http.MethodGet, "/raw/raw-releases/releases/app.txt", nil)
	if canonical.Code != http.StatusOK || canonical.Body.String() != "native raw artifact" {
		t.Fatalf("canonical Raw GET status=%d body=%q", canonical.Code, canonical.Body.String())
	}

	encodedSlash := request(http.MethodPut, "/raw/raw-releases/releases%2Fescaped.txt", []byte("must be rejected"))
	if encodedSlash.Code == http.StatusCreated {
		t.Fatalf("canonical Raw PUT accepted encoded slash: status=%d body=%q", encodedSlash.Code, encodedSlash.Body.String())
	}
}

func TestCanonicalRepositoryRoutesReportsResolvedProtocolMetrics(t *testing.T) {
	store := repository.NewMemoryStore()
	if _, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{
		ID: "raw-migration-metrics", Name: "raw-releases", Format: repository.FormatRaw,
	}); err != nil {
		t.Fatal(err)
	}
	metrics := &Metrics{}
	handler := metrics.Instrument(newTestGateway(t, GatewayOptions{Dependencies: Dependencies{}, Store: store, Adapter: TestAdapter{}, Authenticator: testAuthenticator(), Storage: ObjectStores{repository.FormatOCI: objectstore.NewMemoryStore(), repository.FormatRaw: objectstore.NewMemoryStore()}}))
	request := httptest.NewRequest(http.MethodGet, "/raw/raw-releases/missing.txt", nil)
	authorize(request, "resolver-secret")
	handler.ServeHTTP(httptest.NewRecorder(), request)

	response := httptest.NewRecorder()
	metrics.Handler(response, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := response.Body.String()
	if !strings.Contains(body, `artifact_gateway_http_requests_total{class="raw",status="4xx"} 1`) {
		t.Fatalf("canonical request was not classified as Raw:\n%s", body)
	}
	if !strings.Contains(body, `artifact_gateway_http_requests_total{class="other",status="4xx"} 0`) {
		t.Fatalf("canonical request leaked into other metrics:\n%s", body)
	}
}

func TestCanonicalRepositoryRoutesResolvesRawGroups(t *testing.T) {
	store := repository.NewMemoryStore()
	member, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{
		ID: "raw-migration-member", Name: "raw-releases", Format: repository.FormatRaw,
	})
	if err != nil {
		t.Fatal(err)
	}
	createV2Group(t, store, "raw-all", repository.FormatRaw,
		repository.GroupMember{RepositoryID: member.ID, Position: 0},
	)
	handler := newTestGateway(t, GatewayOptions{Dependencies: Dependencies{}, Store: store, Adapter: TestAdapter{}, Authenticator: testAuthenticator(), Storage: ObjectStores{repository.FormatOCI: objectstore.NewMemoryStore(), repository.FormatRaw: objectstore.NewMemoryStore()}})

	put := httptest.NewRequest(http.MethodPut, "/raw/raw-releases/releases/app.txt", strings.NewReader("group artifact"))
	authorize(put, "resolver-secret")
	putResponse := httptest.NewRecorder()
	handler.ServeHTTP(putResponse, put)
	if putResponse.Code != http.StatusCreated {
		t.Fatalf("canonical Raw PUT status=%d body=%q", putResponse.Code, putResponse.Body.String())
	}

	get := httptest.NewRequest(http.MethodGet, "/raw/raw-all/releases/app.txt", nil)
	authorize(get, "resolver-secret")
	getResponse := httptest.NewRecorder()
	handler.ServeHTTP(getResponse, get)
	if getResponse.Code != http.StatusOK || getResponse.Body.String() != "group artifact" {
		t.Fatalf("canonical Raw group GET status=%d body=%q", getResponse.Code, getResponse.Body.String())
	}
}

func TestCanonicalRepositoryRoutesKeepsRawDiscoveryURLsOnCanonicalRoot(t *testing.T) {
	store := repository.NewMemoryStore()
	if _, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{
		ID: "raw-migration-discovery", Name: "raw-releases", Format: repository.FormatRaw,
	}); err != nil {
		t.Fatal(err)
	}
	handler := newTestGateway(t, GatewayOptions{Dependencies: Dependencies{}, Store: store, Adapter: TestAdapter{}, Authenticator: testAuthenticator(), Storage: ObjectStores{repository.FormatOCI: objectstore.NewMemoryStore(), repository.FormatRaw: objectstore.NewMemoryStore()}})
	request := func(method, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		authorize(req, "resolver-secret")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		return response
	}

	for _, name := range []string{"alpha.txt", "beta.txt"} {
		response := request(http.MethodPut, "/raw/raw-releases/releases/"+name, name)
		if response.Code != http.StatusCreated {
			t.Fatalf("canonical Raw PUT %s status=%d body=%q", name, response.Code, response.Body.String())
		}
	}
	listing := request(http.MethodGet, "/raw/raw-releases/releases/?n=1", "")
	if listing.Code != http.StatusOK {
		t.Fatalf("canonical Raw listing status=%d body=%q", listing.Code, listing.Body.String())
	}
	if link := listing.Header().Get("Link"); !strings.HasPrefix(link, "</raw/raw-releases/releases/") || strings.Contains(link, "</repository/") {
		t.Fatalf("canonical Raw listing did not preserve canonical pagination link %q", link)
	}

	started := request(http.MethodPost, "/raw/raw-releases/releases/large.bin?resumable=1", "")
	if started.Code != http.StatusCreated {
		t.Fatalf("canonical Raw resumable start status=%d body=%q", started.Code, started.Body.String())
	}
	location := started.Header().Get("Location")
	if !strings.HasPrefix(location, "/raw/raw-releases/releases/large.bin?uploadId=") || strings.HasPrefix(location, "/repository/") {
		t.Fatalf("canonical Raw upload did not preserve canonical location %q", location)
	}
	cancelled := request(http.MethodDelete, location, "")
	if cancelled.Code != http.StatusNoContent {
		t.Fatalf("follow canonical Raw upload location status=%d body=%q", cancelled.Code, cancelled.Body.String())
	}
}

func TestCanonicalRepositoryRoutesPreservesMavenDirectPublication(t *testing.T) {
	store := repository.NewMemoryStore()
	if _, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{
		ID: "maven-migration-repository", Name: "releases", Format: repository.FormatMaven,
	}); err != nil {
		t.Fatal(err)
	}
	handler := newTestGateway(t, GatewayOptions{Dependencies: Dependencies{}, Store: store, Adapter: TestAdapter{}, Authenticator: testAuthenticator(), Storage: ObjectStores{repository.FormatMaven: objectstore.NewMemoryStore()}})

	const (
		assetPath = "/repository/maven/releases/org/example/widget/1.2.0/widget-1.2.0.pom"
		pom       = "<project><groupId>org.example</groupId><artifactId>widget</artifactId><version>1.2.0</version></project>"
	)
	put := httptest.NewRequest(http.MethodPut, assetPath, strings.NewReader(pom))
	put.SetBasicAuth("maven", "resolver-secret")
	putResponse := httptest.NewRecorder()
	handler.ServeHTTP(putResponse, put)
	if putResponse.Code != http.StatusCreated {
		t.Fatalf("canonical Maven PUT status=%d body=%q", putResponse.Code, putResponse.Body.String())
	}

	get := httptest.NewRequest(http.MethodGet, assetPath, nil)
	get.SetBasicAuth("maven", "resolver-secret")
	getResponse := httptest.NewRecorder()
	handler.ServeHTTP(getResponse, get)
	if getResponse.Code != http.StatusOK || getResponse.Body.String() != pom {
		t.Fatalf("canonical Maven GET status=%d body=%q", getResponse.Code, getResponse.Body.String())
	}
}

func TestCanonicalMavenPrefixDoesNotInferRepositoryName(t *testing.T) {
	store := repository.NewMemoryStore()
	if _, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{
		ID: "maven-name-collision-repository", Name: "maven", Format: repository.FormatMaven,
	}); err != nil {
		t.Fatal(err)
	}
	authenticator := testAuthenticator()
	authenticator.RepositoryWriters["maven"] = append(authenticator.RepositoryWriters["maven"], "maven")
	handler := newTestGateway(t, GatewayOptions{Dependencies: Dependencies{}, Store: store, Adapter: TestAdapter{}, Authenticator: authenticator, Storage: ObjectStores{repository.FormatMaven: objectstore.NewMemoryStore()}})

	const assetPath = "/repository/maven/missing/org/example/widget/1.2.0/widget-1.2.0.pom"
	const pom = "<project><groupId>org.example</groupId><artifactId>widget</artifactId><version>1.2.0</version></project>"
	put := httptest.NewRequest(http.MethodPut, assetPath, strings.NewReader(pom))
	put.SetBasicAuth("maven", "resolver-secret")
	putResponse := httptest.NewRecorder()
	handler.ServeHTTP(putResponse, put)
	if putResponse.Code != http.StatusNotFound {
		t.Fatalf("reserved canonical Maven prefix was reinterpreted as repository maven: status=%d body=%q", putResponse.Code, putResponse.Body.String())
	}
}

func TestCanonicalRepositoryRoutesResolvesMavenGroups(t *testing.T) {
	store := repository.NewMemoryStore()
	member, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{
		ID: "maven-migration-member", Name: "releases", Format: repository.FormatMaven,
	})
	if err != nil {
		t.Fatal(err)
	}
	createV2Group(t, store, "maven-all", repository.FormatMaven,
		repository.GroupMember{RepositoryID: member.ID, Position: 0},
	)
	handler := newTestGateway(t, GatewayOptions{Dependencies: Dependencies{}, Store: store, Adapter: TestAdapter{}, Authenticator: testAuthenticator(), Storage: ObjectStores{repository.FormatMaven: objectstore.NewMemoryStore()}})

	const (
		assetPath = "org/example/widget/1.2.0/widget-1.2.0.pom"
		pom       = "<project><groupId>org.example</groupId><artifactId>widget</artifactId><version>1.2.0</version></project>"
	)
	put := httptest.NewRequest(http.MethodPut, "/repository/maven/releases/"+assetPath, strings.NewReader(pom))
	put.SetBasicAuth("maven", "resolver-secret")
	putResponse := httptest.NewRecorder()
	handler.ServeHTTP(putResponse, put)
	if putResponse.Code != http.StatusCreated {
		t.Fatalf("canonical Maven PUT status=%d body=%q", putResponse.Code, putResponse.Body.String())
	}

	get := httptest.NewRequest(http.MethodGet, "/maven/maven-all/"+assetPath, nil)
	get.SetBasicAuth("maven", "resolver-secret")
	getResponse := httptest.NewRecorder()
	handler.ServeHTTP(getResponse, get)
	if getResponse.Code != http.StatusOK || getResponse.Body.String() != pom {
		t.Fatalf("canonical Maven group GET status=%d body=%q", getResponse.Code, getResponse.Body.String())
	}
}

func TestCanonicalRepositoryRoutesPreservesNPMScopedPackagePaths(t *testing.T) {
	store := repository.NewMemoryStore()
	if _, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{
		ID: "npm-migration-repository", Name: "releases", Format: repository.FormatNPM,
	}); err != nil {
		t.Fatal(err)
	}
	authenticator := testAuthenticator()
	authenticator.RepositoryWriters["build-agent"] = []string{"releases"}
	handler := newTestGateway(t, GatewayOptions{Dependencies: Dependencies{}, Store: store, Adapter: TestAdapter{}, Authenticator: authenticator, Storage: ObjectStores{repository.FormatNPM: objectstore.NewMemoryStore()}})

	tarball := npmFixtureTarball(t, "@scope/widget", "1.2.3")
	publishBody := npmFixturePublishDocument(t, "@scope/widget", "1.2.3", "@scope/widget-1.2.3.tgz", tarball)
	publish := httptest.NewRequest(http.MethodPut, "/npm/releases/@scope%2Fwidget", strings.NewReader(publishBody))
	authorize(publish, "resolver-secret")
	publishResponse := httptest.NewRecorder()
	handler.ServeHTTP(publishResponse, publish)
	if publishResponse.Code != http.StatusCreated {
		t.Fatalf("canonical npm publish status=%d body=%q", publishResponse.Code, publishResponse.Body.String())
	}

	metadata := httptest.NewRequest(http.MethodGet, "/npm/releases/@scope%2Fwidget", nil)
	authorize(metadata, "resolver-secret")
	metadataResponse := httptest.NewRecorder()
	handler.ServeHTTP(metadataResponse, metadata)
	if metadataResponse.Code != http.StatusOK || !strings.Contains(metadataResponse.Body.String(), `"1.2.3"`) {
		t.Fatalf("canonical npm metadata status=%d body=%q", metadataResponse.Code, metadataResponse.Body.String())
	}
	if !strings.Contains(metadataResponse.Body.String(), "/npm/releases/") || strings.Contains(metadataResponse.Body.String(), "/repository/") {
		t.Fatalf("canonical npm metadata leaked a canonical tarball URL: %q", metadataResponse.Body.String())
	}
}

func TestCanonicalRepositoryRoutesResolvesNPMGroups(t *testing.T) {
	store := repository.NewMemoryStore()
	member, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{
		ID: "npm-migration-member", Name: "releases", Format: repository.FormatNPM,
	})
	if err != nil {
		t.Fatal(err)
	}
	createV2Group(t, store, "npm-all", repository.FormatNPM,
		repository.GroupMember{RepositoryID: member.ID, Position: 0},
	)
	authenticator := testAuthenticator()
	authenticator.RepositoryWriters["build-agent"] = []string{"releases"}
	handler := newTestGateway(t, GatewayOptions{Dependencies: Dependencies{}, Store: store, Adapter: TestAdapter{}, Authenticator: authenticator, Storage: ObjectStores{repository.FormatNPM: objectstore.NewMemoryStore()}})

	tarball := npmFixtureTarball(t, "@scope/widget", "1.2.3")
	publishBody := npmFixturePublishDocument(t, "@scope/widget", "1.2.3", "@scope/widget-1.2.3.tgz", tarball)
	publish := httptest.NewRequest(http.MethodPut, "/npm/releases/@scope%2Fwidget", strings.NewReader(publishBody))
	authorize(publish, "resolver-secret")
	publishResponse := httptest.NewRecorder()
	handler.ServeHTTP(publishResponse, publish)
	if publishResponse.Code != http.StatusCreated {
		t.Fatalf("canonical npm publish status=%d body=%q", publishResponse.Code, publishResponse.Body.String())
	}

	metadata := httptest.NewRequest(http.MethodGet, "/npm/npm-all/@scope%2Fwidget", nil)
	authorize(metadata, "resolver-secret")
	metadataResponse := httptest.NewRecorder()
	handler.ServeHTTP(metadataResponse, metadata)
	if metadataResponse.Code != http.StatusOK || !strings.Contains(metadataResponse.Body.String(), `"1.2.3"`) {
		t.Fatalf("canonical npm group metadata status=%d body=%q", metadataResponse.Code, metadataResponse.Body.String())
	}
	if !strings.Contains(metadataResponse.Body.String(), "/npm/npm-all/") || strings.Contains(metadataResponse.Body.String(), "/repository/") {
		t.Fatalf("canonical npm group metadata leaked a canonical tarball URL: %q", metadataResponse.Body.String())
	}
}

func TestCanonicalRepositoryRoutesPreservesPyPISimpleReads(t *testing.T) {
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{
		ID: "pypi-migration-repository", Name: "python", Format: repository.FormatPyPI,
	})
	if err != nil {
		t.Fatal(err)
	}
	objects := objectstore.NewMemoryStore()
	file := publishPyPITestFile(t, store, objects, repo, "gateway-widget", "1.2.3")
	handler := newTestGateway(t, GatewayOptions{Dependencies: Dependencies{}, Store: store, Adapter: TestAdapter{}, Authenticator: testAuthenticator(), Storage: ObjectStores{repository.FormatPyPI: objects}})

	simple := httptest.NewRequest(http.MethodGet, "/pypi/python/simple/gateway-widget/", nil)
	authorize(simple, "resolver-secret")
	simpleResponse := httptest.NewRecorder()
	handler.ServeHTTP(simpleResponse, simple)
	if simpleResponse.Code != http.StatusOK || !strings.Contains(simpleResponse.Body.String(), file.Filename) {
		t.Fatalf("canonical PyPI Simple status=%d body=%q", simpleResponse.Code, simpleResponse.Body.String())
	}
}

func TestCanonicalRepositoryRoutesAcceptsPyPITwineLegacyUpload(t *testing.T) {
	store := repository.NewMemoryStore()
	if _, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{
		ID: "pypi-canonical-upload", Name: "python", Format: repository.FormatPyPI,
	}); err != nil {
		t.Fatal(err)
	}
	wheel := pypiFixtureWheel(t, "gateway_widget", "1.2.3")
	sum := sha256.Sum256(wheel)
	body := new(bytes.Buffer)
	writer := multipart.NewWriter(body)
	for key, value := range map[string]string{
		":action": "file_upload", "name": "gateway-widget", "version": "1.2.3",
		"filetype": "bdist_wheel", "pyversion": "py3", "sha256_digest": hex.EncodeToString(sum[:]),
	} {
		if err := writer.WriteField(key, value); err != nil {
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
	handler := newTestGateway(t, GatewayOptions{Dependencies: Dependencies{}, Store: store, Adapter: TestAdapter{}, Authenticator: testAuthenticator(), Storage: ObjectStores{repository.FormatPyPI: objectstore.NewMemoryStore()}})
	upload := httptest.NewRequest(http.MethodPost, "/pypi/python/legacy/", body)
	upload.Header.Set("Content-Type", writer.FormDataContentType())
	authorize(upload, "resolver-secret")
	uploaded := httptest.NewRecorder()
	handler.ServeHTTP(uploaded, upload)
	if uploaded.Code != http.StatusCreated {
		t.Fatalf("canonical PyPI root upload status=%d body=%q", uploaded.Code, uploaded.Body.String())
	}
	location := uploaded.Header().Get("Location")
	if !strings.HasPrefix(location, "/pypi/python/packages/") || strings.HasPrefix(location, "/repository/") {
		t.Fatalf("canonical PyPI upload did not preserve canonical location %q", location)
	}
	download := httptest.NewRequest(http.MethodGet, location, nil)
	authorize(download, "resolver-secret")
	downloaded := httptest.NewRecorder()
	handler.ServeHTTP(downloaded, download)
	if downloaded.Code != http.StatusOK || !bytes.Equal(downloaded.Body.Bytes(), wheel) {
		t.Fatalf("follow canonical PyPI location status=%d bytes=%d", downloaded.Code, downloaded.Body.Len())
	}
}

func TestCanonicalRepositoryRoutesResolvesPyPIGroups(t *testing.T) {
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{
		ID: "pypi-migration-member", Name: "python", Format: repository.FormatPyPI,
	})
	if err != nil {
		t.Fatal(err)
	}
	createV2Group(t, store, "python-all", repository.FormatPyPI,
		repository.GroupMember{RepositoryID: repo.ID, Position: 0},
	)
	objects := objectstore.NewMemoryStore()
	file := publishPyPITestFile(t, store, objects, repo, "gateway-widget", "1.2.3")
	handler := newTestGateway(t, GatewayOptions{Dependencies: Dependencies{}, Store: store, Adapter: TestAdapter{}, Authenticator: testAuthenticator(), Storage: ObjectStores{repository.FormatPyPI: objects}})

	simple := httptest.NewRequest(http.MethodGet, "/pypi/python-all/simple/gateway-widget/", nil)
	authorize(simple, "resolver-secret")
	simpleResponse := httptest.NewRecorder()
	handler.ServeHTTP(simpleResponse, simple)
	if simpleResponse.Code != http.StatusOK || !strings.Contains(simpleResponse.Body.String(), file.Filename) {
		t.Fatalf("canonical PyPI group Simple status=%d body=%q", simpleResponse.Code, simpleResponse.Body.String())
	}
}

func TestCanonicalRepositoryRoutesPreservesGOPROXYReads(t *testing.T) {
	const (
		modulePath    = "example.com/Acme/widget"
		escapedModule = "example.com/!acme/widget"
		version       = "v1.2.3"
	)
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{
		ID: "go-migration-repository", Name: "go-releases", Format: repository.FormatGo,
	})
	if err != nil {
		t.Fatal(err)
	}
	grantGoPublisher(t, store, repo.ID)
	handler := newTestGateway(t, GatewayOptions{Dependencies: Dependencies{}, Store: store, Adapter: TestAdapter{}, Authenticator: testAuthenticator(), Storage: ObjectStores{repository.FormatGo: objectstore.NewMemoryStore()}})
	archive := goModuleFixtureZip(t, modulePath, version, map[string]string{
		"go.mod":    "module " + modulePath + "\n\ngo 1.26\n",
		"widget.go": "package widget\n",
	})
	publishPath := "/go/" + repo.Name + "/" + escapedModule + "/@v/" + version + ".zip"
	publish := httptest.NewRequest(http.MethodPut, publishPath, bytes.NewReader(archive))
	authorize(publish, "resolver-secret")
	publishResponse := httptest.NewRecorder()
	handler.ServeHTTP(publishResponse, publish)
	if publishResponse.Code != http.StatusCreated {
		t.Fatalf("canonical Go publish status=%d body=%q", publishResponse.Code, publishResponse.Body.String())
	}
	if location := publishResponse.Header().Get("Location"); location != publishPath {
		t.Fatalf("canonical Go publish location=%q want=%q", location, publishPath)
	}

	list := httptest.NewRequest(http.MethodGet, "/go/"+repo.Name+"/"+escapedModule+"/@v/list", nil)
	authorize(list, "resolver-secret")
	listResponse := httptest.NewRecorder()
	handler.ServeHTTP(listResponse, list)
	if listResponse.Code != http.StatusOK || listResponse.Body.String() != version+"\n" {
		t.Fatalf("canonical GOPROXY list status=%d body=%q", listResponse.Code, listResponse.Body.String())
	}
}
