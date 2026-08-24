package app

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

func TestNexusRepositoryCompatibilityAliasPreservesRawProtocolBehavior(t *testing.T) {
	store := repository.NewMemoryStore()
	if _, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{
		ID: "raw-migration-repository", Name: "raw-releases", Format: repository.FormatRaw,
	}); err != nil {
		t.Fatal(err)
	}
	handler := NewGatewayHandler(
		Dependencies{NativeOCIObjectStore: NewMemoryOCIObjectStore()},
		store,
		TestAdapter{},
		testAuthenticator(),
	)

	request := func(method, path string, body []byte) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, bytes.NewReader(body))
		authorize(req, "resolver-secret")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		return response
	}

	put := request(http.MethodPut, "/repository/raw-releases/releases/app.txt", []byte("native raw artifact"))
	if put.Code != http.StatusCreated {
		t.Fatalf("Nexus-compatible Raw PUT status=%d body=%q", put.Code, put.Body.String())
	}

	rangeRequest := httptest.NewRequest(http.MethodGet, "/repository/raw-releases/releases/app.txt", nil)
	rangeRequest.Header.Set("Range", "bytes=7-9")
	authorize(rangeRequest, "resolver-secret")
	ranged := httptest.NewRecorder()
	handler.ServeHTTP(ranged, rangeRequest)
	if ranged.Code != http.StatusPartialContent || ranged.Body.String() != "raw" {
		t.Fatalf("Nexus-compatible Raw range status=%d body=%q", ranged.Code, ranged.Body.String())
	}

	canonical := request(http.MethodGet, "/raw/raw-releases/releases/app.txt", nil)
	if canonical.Code != http.StatusOK || canonical.Body.String() != "native raw artifact" {
		t.Fatalf("canonical Raw GET status=%d body=%q", canonical.Code, canonical.Body.String())
	}
}

func TestNexusRepositoryCompatibilityAliasResolvesRawGroups(t *testing.T) {
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
	handler := NewGatewayHandler(
		Dependencies{NativeOCIObjectStore: NewMemoryOCIObjectStore()},
		store,
		TestAdapter{},
		testAuthenticator(),
	)

	put := httptest.NewRequest(http.MethodPut, "/raw/raw-releases/releases/app.txt", strings.NewReader("group artifact"))
	authorize(put, "resolver-secret")
	putResponse := httptest.NewRecorder()
	handler.ServeHTTP(putResponse, put)
	if putResponse.Code != http.StatusCreated {
		t.Fatalf("canonical Raw PUT status=%d body=%q", putResponse.Code, putResponse.Body.String())
	}

	get := httptest.NewRequest(http.MethodGet, "/repository/raw-all/releases/app.txt", nil)
	authorize(get, "resolver-secret")
	getResponse := httptest.NewRecorder()
	handler.ServeHTTP(getResponse, get)
	if getResponse.Code != http.StatusOK || getResponse.Body.String() != "group artifact" {
		t.Fatalf("Nexus-compatible Raw group GET status=%d body=%q", getResponse.Code, getResponse.Body.String())
	}
}

func TestNexusRepositoryCompatibilityAliasPreservesMavenDirectPublication(t *testing.T) {
	store := repository.NewMemoryStore()
	if _, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{
		ID: "maven-migration-repository", Name: "releases", Format: repository.FormatMaven,
	}); err != nil {
		t.Fatal(err)
	}
	handler := NewGatewayHandler(
		Dependencies{NativeMavenObjectStore: NewMemoryOCIObjectStore()},
		store,
		TestAdapter{},
		testAuthenticator(),
	)

	const (
		assetPath = "/repository/releases/org/example/widget/1.2.0/widget-1.2.0.pom"
		pom       = "<project><groupId>org.example</groupId><artifactId>widget</artifactId><version>1.2.0</version></project>"
	)
	put := httptest.NewRequest(http.MethodPut, assetPath, strings.NewReader(pom))
	put.SetBasicAuth("maven", "resolver-secret")
	putResponse := httptest.NewRecorder()
	handler.ServeHTTP(putResponse, put)
	if putResponse.Code != http.StatusCreated {
		t.Fatalf("Nexus-compatible Maven PUT status=%d body=%q", putResponse.Code, putResponse.Body.String())
	}

	get := httptest.NewRequest(http.MethodGet, assetPath, nil)
	get.SetBasicAuth("maven", "resolver-secret")
	getResponse := httptest.NewRecorder()
	handler.ServeHTTP(getResponse, get)
	if getResponse.Code != http.StatusOK || getResponse.Body.String() != pom {
		t.Fatalf("Nexus-compatible Maven GET status=%d body=%q", getResponse.Code, getResponse.Body.String())
	}
}

func TestNexusRepositoryCompatibilityAliasAllowsRepositoryNamedMaven(t *testing.T) {
	store := repository.NewMemoryStore()
	if _, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{
		ID: "maven-name-collision-repository", Name: "maven", Format: repository.FormatMaven,
	}); err != nil {
		t.Fatal(err)
	}
	authenticator := testAuthenticator()
	authenticator.RepositoryWriters["maven"] = append(authenticator.RepositoryWriters["maven"], "maven")
	handler := NewGatewayHandler(
		Dependencies{NativeMavenObjectStore: NewMemoryOCIObjectStore()},
		store,
		TestAdapter{},
		authenticator,
	)

	const (
		assetPath = "/repository/maven/org/example/widget/1.2.0/widget-1.2.0.pom"
		pom       = "<project><groupId>org.example</groupId><artifactId>widget</artifactId><version>1.2.0</version></project>"
	)
	put := httptest.NewRequest(http.MethodPut, assetPath, strings.NewReader(pom))
	put.SetBasicAuth("maven", "resolver-secret")
	putResponse := httptest.NewRecorder()
	handler.ServeHTTP(putResponse, put)
	if putResponse.Code != http.StatusCreated {
		t.Fatalf("Nexus-compatible repository named maven PUT status=%d body=%q", putResponse.Code, putResponse.Body.String())
	}

	get := httptest.NewRequest(http.MethodGet, assetPath, nil)
	get.SetBasicAuth("maven", "resolver-secret")
	getResponse := httptest.NewRecorder()
	handler.ServeHTTP(getResponse, get)
	if getResponse.Code != http.StatusOK || getResponse.Body.String() != pom {
		t.Fatalf("Nexus-compatible repository named maven GET status=%d body=%q", getResponse.Code, getResponse.Body.String())
	}
}

func TestNexusRepositoryCompatibilityAliasResolvesMavenGroups(t *testing.T) {
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
	handler := NewGatewayHandler(
		Dependencies{NativeMavenObjectStore: NewMemoryOCIObjectStore()},
		store,
		TestAdapter{},
		testAuthenticator(),
	)

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

	get := httptest.NewRequest(http.MethodGet, "/repository/maven-all/"+assetPath, nil)
	get.SetBasicAuth("maven", "resolver-secret")
	getResponse := httptest.NewRecorder()
	handler.ServeHTTP(getResponse, get)
	if getResponse.Code != http.StatusOK || getResponse.Body.String() != pom {
		t.Fatalf("Nexus-compatible Maven group GET status=%d body=%q", getResponse.Code, getResponse.Body.String())
	}
}

func TestNexusRepositoryCompatibilityAliasPreservesNPMScopedPackagePaths(t *testing.T) {
	store := repository.NewMemoryStore()
	if _, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{
		ID: "npm-migration-repository", Name: "releases", Format: repository.FormatNPM,
	}); err != nil {
		t.Fatal(err)
	}
	authenticator := testAuthenticator()
	authenticator.RepositoryWriters["build-agent"] = []string{"releases"}
	handler := NewGatewayHandler(
		Dependencies{NativeNPMObjectStore: NewMemoryOCIObjectStore()},
		store,
		TestAdapter{},
		authenticator,
	)

	tarball := npmFixtureTarball(t, "@scope/widget", "1.2.3")
	publishBody := npmFixturePublishDocument(t, "@scope/widget", "1.2.3", "@scope/widget-1.2.3.tgz", tarball)
	publish := httptest.NewRequest(http.MethodPut, "/repository/releases/@scope%2Fwidget", strings.NewReader(publishBody))
	authorize(publish, "resolver-secret")
	publishResponse := httptest.NewRecorder()
	handler.ServeHTTP(publishResponse, publish)
	if publishResponse.Code != http.StatusCreated {
		t.Fatalf("Nexus-compatible npm publish status=%d body=%q", publishResponse.Code, publishResponse.Body.String())
	}

	metadata := httptest.NewRequest(http.MethodGet, "/repository/releases/@scope%2Fwidget", nil)
	authorize(metadata, "resolver-secret")
	metadataResponse := httptest.NewRecorder()
	handler.ServeHTTP(metadataResponse, metadata)
	if metadataResponse.Code != http.StatusOK || !strings.Contains(metadataResponse.Body.String(), `"1.2.3"`) {
		t.Fatalf("Nexus-compatible npm metadata status=%d body=%q", metadataResponse.Code, metadataResponse.Body.String())
	}
	if !strings.Contains(metadataResponse.Body.String(), "/repository/releases/") || strings.Contains(metadataResponse.Body.String(), "/npm/releases/") {
		t.Fatalf("Nexus-compatible npm metadata leaked a canonical tarball URL: %q", metadataResponse.Body.String())
	}
}

func TestNexusRepositoryCompatibilityAliasResolvesNPMGroups(t *testing.T) {
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
	handler := NewGatewayHandler(
		Dependencies{NativeNPMObjectStore: NewMemoryOCIObjectStore()},
		store,
		TestAdapter{},
		authenticator,
	)

	tarball := npmFixtureTarball(t, "@scope/widget", "1.2.3")
	publishBody := npmFixturePublishDocument(t, "@scope/widget", "1.2.3", "@scope/widget-1.2.3.tgz", tarball)
	publish := httptest.NewRequest(http.MethodPut, "/repository/releases/@scope%2Fwidget", strings.NewReader(publishBody))
	authorize(publish, "resolver-secret")
	publishResponse := httptest.NewRecorder()
	handler.ServeHTTP(publishResponse, publish)
	if publishResponse.Code != http.StatusCreated {
		t.Fatalf("Nexus-compatible npm publish status=%d body=%q", publishResponse.Code, publishResponse.Body.String())
	}

	metadata := httptest.NewRequest(http.MethodGet, "/repository/npm-all/@scope%2Fwidget", nil)
	authorize(metadata, "resolver-secret")
	metadataResponse := httptest.NewRecorder()
	handler.ServeHTTP(metadataResponse, metadata)
	if metadataResponse.Code != http.StatusOK || !strings.Contains(metadataResponse.Body.String(), `"1.2.3"`) {
		t.Fatalf("Nexus-compatible npm group metadata status=%d body=%q", metadataResponse.Code, metadataResponse.Body.String())
	}
	if !strings.Contains(metadataResponse.Body.String(), "/repository/npm-all/") || strings.Contains(metadataResponse.Body.String(), "/npm/npm-all/") {
		t.Fatalf("Nexus-compatible npm group metadata leaked a canonical tarball URL: %q", metadataResponse.Body.String())
	}
}

func TestNexusRepositoryCompatibilityAliasPreservesPyPISimpleReads(t *testing.T) {
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{
		ID: "pypi-migration-repository", Name: "python", Format: repository.FormatPyPI,
	})
	if err != nil {
		t.Fatal(err)
	}
	objects := NewMemoryOCIObjectStore()
	file := publishPyPITestFile(t, store, objects, repo, "gateway-widget", "1.2.3")
	handler := NewGatewayHandler(
		Dependencies{NativePyPIObjectStore: objects},
		store,
		TestAdapter{},
		testAuthenticator(),
	)

	simple := httptest.NewRequest(http.MethodGet, "/repository/python/simple/gateway-widget/", nil)
	authorize(simple, "resolver-secret")
	simpleResponse := httptest.NewRecorder()
	handler.ServeHTTP(simpleResponse, simple)
	if simpleResponse.Code != http.StatusOK || !strings.Contains(simpleResponse.Body.String(), file.Filename) {
		t.Fatalf("Nexus-compatible PyPI Simple status=%d body=%q", simpleResponse.Code, simpleResponse.Body.String())
	}
}

func TestNexusRepositoryCompatibilityAliasResolvesPyPIGroups(t *testing.T) {
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
	objects := NewMemoryOCIObjectStore()
	file := publishPyPITestFile(t, store, objects, repo, "gateway-widget", "1.2.3")
	handler := NewGatewayHandler(
		Dependencies{NativePyPIObjectStore: objects},
		store,
		TestAdapter{},
		testAuthenticator(),
	)

	simple := httptest.NewRequest(http.MethodGet, "/repository/python-all/simple/gateway-widget/", nil)
	authorize(simple, "resolver-secret")
	simpleResponse := httptest.NewRecorder()
	handler.ServeHTTP(simpleResponse, simple)
	if simpleResponse.Code != http.StatusOK || !strings.Contains(simpleResponse.Body.String(), file.Filename) {
		t.Fatalf("Nexus-compatible PyPI group Simple status=%d body=%q", simpleResponse.Code, simpleResponse.Body.String())
	}
}

func TestNexusRepositoryCompatibilityAliasPreservesGOPROXYReads(t *testing.T) {
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
	handler := NewGatewayHandler(
		Dependencies{NativeGoObjectStore: NewMemoryOCIObjectStore()},
		store,
		TestAdapter{},
		testAuthenticator(),
	)
	archive := goModuleFixtureZip(t, modulePath, version, map[string]string{
		"go.mod":    "module " + modulePath + "\n\ngo 1.26\n",
		"widget.go": "package widget\n",
	})
	publish := httptest.NewRequest(http.MethodPut, "/go/"+repo.Name+"/"+escapedModule+"/@v/"+version+".zip", bytes.NewReader(archive))
	authorize(publish, "resolver-secret")
	publishResponse := httptest.NewRecorder()
	handler.ServeHTTP(publishResponse, publish)
	if publishResponse.Code != http.StatusCreated {
		t.Fatalf("canonical Go publish status=%d body=%q", publishResponse.Code, publishResponse.Body.String())
	}

	list := httptest.NewRequest(http.MethodGet, "/repository/"+repo.Name+"/"+escapedModule+"/@v/list", nil)
	authorize(list, "resolver-secret")
	listResponse := httptest.NewRecorder()
	handler.ServeHTTP(listResponse, list)
	if listResponse.Code != http.StatusOK || listResponse.Body.String() != version+"\n" {
		t.Fatalf("Nexus-compatible GOPROXY list status=%d body=%q", listResponse.Code, listResponse.Body.String())
	}
}
