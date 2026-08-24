package app

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

type failNthGoPutStore struct {
	*MemoryOCIObjectStore
	failAt int
	puts   int
}

type failingGoReclaimEnqueueStore struct {
	*repository.MemoryStore
}

func (s *failingGoReclaimEnqueueStore) EnqueueLifecycleJob(ctx context.Context, job repository.LifecycleJob) (repository.LifecycleJob, bool, error) {
	if job.Kind == repository.LifecycleJobReclaim {
		return repository.LifecycleJob{}, false, errors.New("injected Go reclaim enqueue failure")
	}
	return s.MemoryStore.EnqueueLifecycleJob(ctx, job)
}

func (s *failNthGoPutStore) PutVerifiedReader(ctx context.Context, key string, reader io.Reader, size int64, digest string) error {
	s.puts++
	if s.puts == s.failAt {
		return errors.New("injected Go object write failure")
	}
	return s.MemoryOCIObjectStore.PutVerifiedReader(ctx, key, reader, size, digest)
}

func TestNativeGoProxyCachesModuleAndServesOffline(t *testing.T) {
	const (
		modulePath    = "example.com/Acme/widget"
		escapedModule = "example.com/!acme/widget"
		version       = "v1.2.3"
	)
	info := []byte(`{"Version":"v1.2.3","Time":"2026-08-09T09:00:00Z"}`)
	mod := []byte("module " + modulePath + "\n\ngo 1.26\n")
	archive := goModuleFixtureZip(t, modulePath, version, map[string]string{
		"go.mod":    string(mod),
		"widget.go": "package widget\n\nconst Version = \"v1.2.3\"\n",
		"README.md": "# Widget\n",
	})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/" + escapedModule + "/@v/list":
			_, _ = io.WriteString(w, version+"\n")
		case "/" + escapedModule + "/@v/" + version + ".info":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(info)
		case "/" + escapedModule + "/@v/" + version + ".mod":
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			_, _ = w.Write(mod)
		case "/" + escapedModule + "/@v/" + version + ".zip":
			w.Header().Set("Content-Type", "application/zip")
			_, _ = w.Write(archive)
		default:
			http.NotFound(w, r)
		}
	}))

	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{
		ID: "go-proxy", Name: "go-public", Format: repository.FormatGo,
		Type: repository.RepositoryTypeProxy, Endpoint: upstream.URL,
	})
	if err != nil {
		t.Fatal(err)
	}
	objects := NewMemoryOCIObjectStore()
	handler := NewGatewayHandler(Dependencies{NativeGoObjectStore: objects}, store, TestAdapter{}, testAuthenticator(), UpstreamClient{HTTPClient: upstream.Client()})
	get := func(path string) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(http.MethodGet, path, nil)
		authorize(request, "resolver-secret")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	base := "/go/" + repo.Name + "/" + escapedModule
	if response := get(base + "/@v/list"); response.Code != http.StatusOK || response.Body.String() != version+"\n" {
		t.Fatalf("list=%d body=%q", response.Code, response.Body.String())
	}
	for suffix, expected := range map[string][]byte{
		"/@v/" + version + ".info": info,
		"/@v/" + version + ".mod":  mod,
		"/@v/" + version + ".zip":  archive,
	} {
		response := get(base + suffix)
		if response.Code != http.StatusOK || !bytes.Equal(response.Body.Bytes(), expected) {
			t.Fatalf("%s=%d bytes=%d", suffix, response.Code, response.Body.Len())
		}
	}
	upstream.Close()
	for suffix, expected := range map[string][]byte{
		"/@v/" + version + ".info": info,
		"/@v/" + version + ".mod":  mod,
		"/@v/" + version + ".zip":  archive,
	} {
		response := get(base + suffix)
		if response.Code != http.StatusOK || !bytes.Equal(response.Body.Bytes(), expected) {
			t.Fatalf("offline %s=%d bytes=%d", suffix, response.Code, response.Body.Len())
		}
	}
}

func TestNativeGoHostedPublishesCanonicalZipAndServesModule(t *testing.T) {
	const (
		modulePath    = "example.com/Acme/hosted"
		escapedModule = "example.com/!acme/hosted"
		version       = "v1.2.3"
	)
	mod := []byte("module " + modulePath + "\n\ngo 1.26\n")
	archive := goModuleFixtureZip(t, modulePath, version, map[string]string{
		"go.mod":    string(mod),
		"hosted.go": "package hosted\n\nconst Version = \"v1.2.3\"\n",
	})
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{
		ID: "go-hosted", Name: "go-hosted", Format: repository.FormatGo,
		Type: repository.RepositoryTypeHosted,
	})
	if err != nil {
		t.Fatal(err)
	}
	enablePublicationScan(t, store, repo.ID)
	handler := NewGatewayHandler(
		publicationScanDependencies(Dependencies{NativeGoObjectStore: NewMemoryOCIObjectStore()}, repository.FormatGo),
		store, TestAdapter{}, testAuthenticator(),
	)
	base := "/go/" + repo.Name + "/" + escapedModule
	request := func(method, suffix string, body []byte) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, base+suffix, bytes.NewReader(body))
		authorize(req, "resolver-secret")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		return response
	}

	if response := request(http.MethodPut, "/@v/"+version+".zip", archive); response.Code != http.StatusForbidden {
		t.Fatalf("publish without write grant=%d body=%s", response.Code, response.Body.String())
	}
	grantGoPublisher(t, store, repo.ID)
	if response := request(http.MethodPut, "/@v/"+version+".zip", nil); response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("empty publish=%d body=%s", response.Code, response.Body.String())
	}
	if response := request(http.MethodPut, "/@v/"+version+".zip", archive); response.Code != http.StatusCreated {
		t.Fatalf("publish=%d body=%s", response.Code, response.Body.String())
	}
	if response := request(http.MethodGet, "/@v/list", nil); response.Code != http.StatusOK || response.Body.String() != version+"\n" {
		t.Fatalf("list=%d body=%q", response.Code, response.Body.String())
	}
	if response := request(http.MethodGet, "/@v/"+version+".mod", nil); response.Code != http.StatusOK || !bytes.Equal(response.Body.Bytes(), mod) {
		t.Fatalf("mod=%d body=%q", response.Code, response.Body.String())
	}
	if response := request(http.MethodGet, "/@v/"+version+".zip", nil); response.Code != http.StatusOK || !bytes.Equal(response.Body.Bytes(), archive) {
		t.Fatalf("zip=%d bytes=%d", response.Code, response.Body.Len())
	}
	if response := request(http.MethodGet, "/@v/"+version+".info", nil); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"Version":"v1.2.3"`) || !strings.Contains(response.Body.String(), `"Time":`) {
		t.Fatalf("info=%d body=%q", response.Code, response.Body.String())
	}
	if response := request(http.MethodPut, "/@v/"+version+".zip", archive); response.Code != http.StatusOK {
		t.Fatalf("idempotent replay=%d body=%s", response.Code, response.Body.String())
	}
	changed := goModuleFixtureZip(t, modulePath, version, map[string]string{
		"go.mod":    string(mod),
		"hosted.go": "package hosted\n\nconst Version = \"changed\"\n",
	})
	if response := request(http.MethodPut, "/@v/"+version+".zip", changed); response.Code != http.StatusConflict {
		t.Fatalf("conflicting publish=%d body=%s", response.Code, response.Body.String())
	}
	jobs, err := store.ListLifecycleJobs(context.Background(), repo.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	var scanJobs, reclaimJobs int
	for _, job := range jobs {
		switch job.Kind {
		case repository.LifecycleJobScan:
			scanJobs++
		case repository.LifecycleJobReclaim:
			reclaimJobs++
		}
	}
	if scanJobs != 1 || reclaimJobs != 3 {
		t.Fatalf("publication jobs=%#v", jobs)
	}
}

func TestNativeGoHostedTombstonesAndRestoresModuleVersion(t *testing.T) {
	const (
		modulePath    = "example.com/Acme/lifecycle"
		escapedModule = "example.com/!acme/lifecycle"
		version       = "v1.2.3"
	)
	ctx := context.Background()
	mod := []byte("module " + modulePath + "\n\ngo 1.26\n")
	archive := goModuleFixtureZip(t, modulePath, version, map[string]string{
		"go.mod":       string(mod),
		"lifecycle.go": "package lifecycle\n",
	})
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{
		ID: uuid.NewString(), Name: "go-lifecycle", Format: repository.FormatGo,
		Type: repository.RepositoryTypeHosted,
	})
	if err != nil {
		t.Fatal(err)
	}
	group, _, err := store.CreateHostedGroupIdempotently(ctx, repository.HostedGroup{
		ID: uuid.NewString(), Name: "go-lifecycle-group", Format: repository.FormatGo,
		Members: []repository.GroupMember{{RepositoryID: repo.ID, Position: 0}},
	}, "admin", "go-lifecycle-group", "go-lifecycle-group")
	if err != nil {
		t.Fatal(err)
	}
	enablePublicationScan(t, store, repo.ID)
	grantGoPublisher(t, store, repo.ID)
	handler := NewGatewayHandler(
		publicationScanDependencies(Dependencies{NativeGoObjectStore: NewMemoryOCIObjectStore()}, repository.FormatGo),
		store, TestAdapter{}, testAuthenticator(),
	)
	base := "/go/" + repo.Name + "/" + escapedModule
	protocolRequest := func(method, suffix string, body []byte) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, base+suffix, bytes.NewReader(body))
		authorize(req, "resolver-secret")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		return response
	}
	groupRequest := func(suffix string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/go/"+group.Name+"/"+escapedModule+suffix, nil)
		authorize(req, "resolver-secret")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		return response
	}
	managementRequest := func(path, body string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/api/v2/repositories/"+repo.ID+path, strings.NewReader(body))
		authorize(req, "admin-secret")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		return response
	}

	if response := protocolRequest(http.MethodPut, "/@v/"+version+".zip", archive); response.Code != http.StatusCreated {
		t.Fatalf("publish=%d body=%s", response.Code, response.Body.String())
	}
	if response := managementRequest("/tombstones", `{"coordinate":"`+modulePath+`@`+version+`"}`); response.Code != http.StatusNoContent {
		t.Fatalf("tombstone=%d body=%s", response.Code, response.Body.String())
	}
	if _, err = store.GetArtifactTombstone(ctx, repo.ID, repository.FormatGo, modulePath+"@"+version); err != nil {
		t.Fatalf("get tombstone: %v", err)
	}
	for _, suffix := range []string{"/@v/list", "/@latest", "/@v/" + version + ".info", "/@v/" + version + ".mod", "/@v/" + version + ".zip"} {
		if response := protocolRequest(http.MethodGet, suffix, nil); response.Code != http.StatusNotFound {
			t.Fatalf("deleted %s=%d body=%s", suffix, response.Code, response.Body.String())
		}
	}
	if response := groupRequest("/@v/" + version + ".zip"); response.Code != http.StatusNotFound {
		t.Fatalf("deleted group zip=%d body=%s", response.Code, response.Body.String())
	}
	if response := protocolRequest(http.MethodPut, "/@v/"+version+".zip", archive); response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "restore") {
		t.Fatalf("republish tombstoned version=%d body=%s", response.Code, response.Body.String())
	}
	search := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/"+repo.ID+"/artifact-search?q="+url.QueryEscape(modulePath), nil)
	authorize(search, "admin-secret")
	searchResponse := httptest.NewRecorder()
	handler.ServeHTTP(searchResponse, search)
	if searchResponse.Code != http.StatusOK || strings.Contains(searchResponse.Body.String(), modulePath) {
		t.Fatalf("deleted search=%d body=%s", searchResponse.Code, searchResponse.Body.String())
	}
	if response := managementRequest("/restore", `{"coordinate":"`+modulePath+`@`+version+`"}`); response.Code != http.StatusNoContent {
		t.Fatalf("restore=%d body=%s", response.Code, response.Body.String())
	}
	if _, err = store.GetArtifactTombstone(ctx, repo.ID, repository.FormatGo, modulePath+"@"+version); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("restored tombstone remained: %v", err)
	}
	if response := protocolRequest(http.MethodGet, "/@v/list", nil); response.Code != http.StatusOK || response.Body.String() != version+"\n" {
		t.Fatalf("restored list=%d body=%q", response.Code, response.Body.String())
	}
	if response := protocolRequest(http.MethodGet, "/@v/"+version+".zip", nil); response.Code != http.StatusOK || !bytes.Equal(response.Body.Bytes(), archive) {
		t.Fatalf("restored zip=%d bytes=%d", response.Code, response.Body.Len())
	}
	if response := groupRequest("/@v/" + version + ".zip"); response.Code != http.StatusOK || !bytes.Equal(response.Body.Bytes(), archive) {
		t.Fatalf("restored group zip=%d bytes=%d", response.Code, response.Body.Len())
	}
}

func TestGoModuleVersionCoordinateValidation(t *testing.T) {
	for _, coordinate := range []string{
		"example.com/team/widget@v1.2.3",
		"example.com/Acme/widget@v0.0.0-20260819090000-abcdefabcdef",
	} {
		if !validGoModuleVersionCoordinate(coordinate) {
			t.Errorf("valid coordinate rejected: %q", coordinate)
		}
	}
	for _, coordinate := range []string{
		"", "example.com/team/widget", "@v1.2.3", "example.com/team/widget@", "example.com/team/widget@1.2.3",
		"example.com/team/../widget@v1.2.3", "example.com/team/widget@v1.2.3\nignored",
	} {
		if validGoModuleVersionCoordinate(coordinate) {
			t.Errorf("invalid coordinate accepted: %q", coordinate)
		}
	}
}

func TestNativeGoHostedDoesNotWriteBeforeDurableReclaimIntent(t *testing.T) {
	const (
		modulePath = "example.com/team/durable-intent"
		version    = "v1.0.0"
	)
	store := &failingGoReclaimEnqueueStore{MemoryStore: repository.NewMemoryStore()}
	repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{
		ID: "go-hosted-durable-intent", Name: "go-hosted-durable-intent", Format: repository.FormatGo, Type: repository.RepositoryTypeHosted,
	})
	if err != nil {
		t.Fatal(err)
	}
	grantGoPublisher(t, store.MemoryStore, repo.ID)
	objects := NewMemoryOCIObjectStore()
	handler := NewGatewayHandler(Dependencies{NativeGoObjectStore: objects}, store, TestAdapter{}, testAuthenticator())
	archive := goModuleFixtureZip(t, modulePath, version, map[string]string{
		"go.mod": "module " + modulePath + "\n", "durable.go": "package durable\n",
	})
	request := httptest.NewRequest(http.MethodPut, "/go/"+repo.Name+"/"+modulePath+"/@v/"+version+".zip", bytes.NewReader(archive))
	authorize(request, "resolver-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("enqueue failure publish=%d body=%s", response.Code, response.Body.String())
	}
	keys, err := objects.List(context.Background(), "native/go/")
	if err != nil || len(keys) != 0 {
		t.Fatalf("enqueue failure wrote objects=%v err=%v", keys, err)
	}
}

func TestNativeGoHostedRejectsQuotaBeforeWritingObjects(t *testing.T) {
	const (
		modulePath = "example.com/team/quota"
		version    = "v1.0.0"
	)
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{
		ID: "go-hosted-quota", Name: "go-hosted-quota", Format: repository.FormatGo, Type: repository.RepositoryTypeHosted,
	})
	if err != nil {
		t.Fatal(err)
	}
	grantGoPublisher(t, store, repo.ID)
	if _, err = store.ReplaceRepositoryCapacityQuota(context.Background(), repo.ID, 1); err != nil {
		t.Fatal(err)
	}
	objects := NewMemoryOCIObjectStore()
	handler := NewGatewayHandler(Dependencies{NativeGoObjectStore: objects}, store, TestAdapter{}, testAuthenticator())
	archive := goModuleFixtureZip(t, modulePath, version, map[string]string{
		"go.mod": "module " + modulePath + "\n", "quota.go": "package quota\n",
	})
	request := httptest.NewRequest(http.MethodPut, "/go/"+repo.Name+"/"+modulePath+"/@v/"+version+".zip", bytes.NewReader(archive))
	authorize(request, "resolver-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusInsufficientStorage {
		t.Fatalf("quota publish=%d body=%s", response.Code, response.Body.String())
	}
	keys, err := objects.List(context.Background(), "native/go/")
	if err != nil || len(keys) != 0 {
		t.Fatalf("quota failure left objects=%v err=%v", keys, err)
	}
}

func TestNativeGoHostedCleansObjectsAfterPartialWriteFailure(t *testing.T) {
	const (
		modulePath = "example.com/team/failing-store"
		version    = "v1.0.0"
	)
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{
		ID: "go-hosted-failing-store", Name: "go-hosted-failing-store", Format: repository.FormatGo, Type: repository.RepositoryTypeHosted,
	})
	if err != nil {
		t.Fatal(err)
	}
	grantGoPublisher(t, store, repo.ID)
	objects := &failNthGoPutStore{MemoryOCIObjectStore: NewMemoryOCIObjectStore(), failAt: 2}
	handler := NewGatewayHandler(Dependencies{NativeGoObjectStore: objects}, store, TestAdapter{}, testAuthenticator())
	archive := goModuleFixtureZip(t, modulePath, version, map[string]string{
		"go.mod": "module " + modulePath + "\n", "failure.go": "package failure\n",
	})
	request := httptest.NewRequest(http.MethodPut, "/go/"+repo.Name+"/"+modulePath+"/@v/"+version+".zip", bytes.NewReader(archive))
	authorize(request, "resolver-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("failed publish=%d body=%s", response.Code, response.Body.String())
	}
	keys, err := objects.List(context.Background(), "native/go/")
	if err != nil || len(keys) != 0 {
		t.Fatalf("partial failure left objects=%v err=%v", keys, err)
	}
}

func TestNativeGoLatestSelectsHighestSemanticVersion(t *testing.T) {
	const modulePath = "example.com/team/latest"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/example.com/team/latest/@v/list":
			_, _ = io.WriteString(w, "v1.9.0\nv1.10.0\nv2.0.0-beta.1\n")
		case "/example.com/team/latest/@v/v1.10.0.info":
			_, _ = io.WriteString(w, `{"Version":"v1.10.0","Time":"2026-08-09T09:00:00Z"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{
		ID: "go-latest", Name: "go-latest", Format: repository.FormatGo,
		Type: repository.RepositoryTypeProxy, Endpoint: upstream.URL,
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewGatewayHandler(Dependencies{NativeGoObjectStore: NewMemoryOCIObjectStore()}, store, TestAdapter{}, testAuthenticator(), UpstreamClient{HTTPClient: upstream.Client()})
	request := httptest.NewRequest(http.MethodGet, "/go/"+repo.Name+"/"+modulePath+"/@latest", nil)
	authorize(request, "resolver-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Body.String() != `{"Version":"v1.10.0","Time":"2026-08-09T09:00:00Z"}` {
		t.Fatalf("latest=%d body=%q", response.Code, response.Body.String())
	}
}

func TestNativeGoProxyHonorsAnonymousPolicyAndRepositoryGrantPrefix(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "v1.0.0\n")
	}))
	defer upstream.Close()
	store := repository.NewMemoryStore()
	public, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{
		ID: "go-anonymous", Name: "go-anonymous", Format: repository.FormatGo,
		Type: repository.RepositoryTypeProxy, Endpoint: upstream.URL, AnonymousRead: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	private, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{
		ID: "go-granted", Name: "go-granted", Format: repository.FormatGo,
		Type: repository.RepositoryTypeProxy, Endpoint: upstream.URL,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.ReplaceRepositoryGrants(context.Background(), private.ID, []repository.RepositoryGrant{{
		Principal: "build-agent", Scopes: []string{"repositories:read"}, ResourcePrefix: "example.com/allowed/",
	}}, "1"); err != nil {
		t.Fatal(err)
	}
	handler := NewGatewayHandler(Dependencies{NativeGoObjectStore: NewMemoryOCIObjectStore()}, store, TestAdapter{}, testAuthenticator(), UpstreamClient{HTTPClient: upstream.Client()})
	request := func(repositoryName, modulePath, token string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/go/"+repositoryName+"/"+modulePath+"/@v/list", nil)
		if token != "" {
			authorize(req, token)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		return response
	}
	if response := request(public.Name, "example.com/public/widget", ""); response.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous without global policy=%d", response.Code)
	}
	if _, err = store.ReplaceAnonymousAccessPolicy(context.Background(), repository.AnonymousAccessPolicy{Enabled: true}, "1"); err != nil {
		t.Fatal(err)
	}
	if response := request(public.Name, "example.com/public/widget", ""); response.Code != http.StatusOK {
		t.Fatalf("anonymous with global policy=%d body=%s", response.Code, response.Body.String())
	}
	if response := request(private.Name, "example.com/allowed/widget", "resolver-secret"); response.Code != http.StatusOK {
		t.Fatalf("granted prefix=%d body=%s", response.Code, response.Body.String())
	}
	if response := request(private.Name, "example.com/denied/widget", "resolver-secret"); response.Code != http.StatusForbidden {
		t.Fatalf("denied prefix=%d body=%s", response.Code, response.Body.String())
	}
}

func TestNativeGoListTreatsGoneAsNotFound(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusGone)
	}))
	defer upstream.Close()
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{
		ID: "go-gone", Name: "go-gone", Format: repository.FormatGo,
		Type: repository.RepositoryTypeProxy, Endpoint: upstream.URL,
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator(), UpstreamClient{HTTPClient: upstream.Client()})
	request := httptest.NewRequest(http.MethodGet, "/go/"+repo.Name+"/example.com/missing/module/@v/list", nil)
	authorize(request, "resolver-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("gone list=%d body=%s", response.Code, response.Body.String())
	}
}

func TestNativeGoCacheRejectsChangedAssetIdentity(t *testing.T) {
	store := repository.NewMemoryStore()
	ctx := context.Background()
	version := repository.GoModuleVersion{RepositoryID: "go-cache", Module: "example.com/team/widget", Version: "v1.0.0"}
	if _, err := store.PutGoModuleVersion(ctx, version); err != nil {
		t.Fatal(err)
	}
	asset := repository.GoModuleAsset{
		RepositoryID: version.RepositoryID, Module: version.Module, Version: version.Version, Kind: "mod",
		Digest: "sha256:" + strings.Repeat("a", 64), ObjectKey: "native/go/sha256/" + strings.Repeat("a", 64),
		Size: 32, SourceURL: "https://proxy.example/one.mod",
	}
	if _, err := store.CacheGoModuleAsset(ctx, asset); err != nil {
		t.Fatal(err)
	}
	asset.Digest = "sha256:" + strings.Repeat("b", 64)
	asset.ObjectKey = "native/go/sha256/" + strings.Repeat("b", 64)
	if _, err := store.CacheGoModuleAsset(ctx, asset); !errors.Is(err, repository.ErrUpstreamChanged) {
		t.Fatalf("changed asset error=%v", err)
	}
}

func TestValidateGoModRejectsMalformedContent(t *testing.T) {
	if _, err := validateGoAsset("example.com/team/widget", "v1.0.0", "mod", []byte("module example.com/team/widget\nreplace (")); err == nil {
		t.Fatal("malformed go.mod was accepted")
	}
}

func TestGoHostedRejectsZipWithCorruptNonModFile(t *testing.T) {
	const (
		modulePath = "example.com/team/corrupt"
		version    = "v1.0.0"
	)
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for name, body := range map[string]string{
		"go.mod":     "module " + modulePath + "\n",
		"corrupt.go": "package corrupt\n",
	} {
		header := &zip.FileHeader{Name: modulePath + "@" + version + "/" + name, Method: zip.Store}
		entry, err := writer.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = io.WriteString(entry, body); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	corrupt := append([]byte(nil), buffer.Bytes()...)
	archive, err := zip.NewReader(bytes.NewReader(corrupt), int64(len(corrupt)))
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range archive.File {
		if !strings.HasSuffix(file.Name, "/corrupt.go") {
			continue
		}
		offset, offsetErr := file.DataOffset()
		if offsetErr != nil {
			t.Fatal(offsetErr)
		}
		corrupt[offset] ^= 0xff
	}
	if _, err = validateGoAsset(modulePath, version, "zip", corrupt); err != nil {
		t.Fatalf("metadata-only ZIP validation should reach content validation: %v", err)
	}
	if _, err = goModFromModuleZip(modulePath, version, corrupt); err == nil {
		t.Fatal("corrupt non-go.mod file was accepted")
	}
}

func TestNativeGoGroupMergesVersionsAndResolvesMemberAssets(t *testing.T) {
	const (
		modulePath    = "example.com/team/toolkit"
		escapedModule = "example.com/team/toolkit"
	)
	modV1 := []byte("module " + modulePath + "\n\ngo 1.25\n")
	modV2 := []byte("module " + modulePath + "\n\ngo 1.26\n")
	upstream := func(version string, mod []byte) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/" + escapedModule + "/@v/list":
				_, _ = io.WriteString(w, version+"\n")
			case "/" + escapedModule + "/@v/" + version + ".mod":
				_, _ = w.Write(mod)
			default:
				http.NotFound(w, r)
			}
		}))
	}
	firstUpstream := upstream("v1.0.0", modV1)
	defer firstUpstream.Close()
	secondUpstream := upstream("v1.1.0", modV2)

	ctx := context.Background()
	store := repository.NewMemoryStore()
	first, err := store.CreateHostedRepository(ctx, repository.HostedRepository{
		ID: "go-first", Name: "go-first", Format: repository.FormatGo,
		Type: repository.RepositoryTypeProxy, Endpoint: firstUpstream.URL,
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.CreateHostedRepository(ctx, repository.HostedRepository{
		ID: "go-second", Name: "go-second", Format: repository.FormatGo,
		Type: repository.RepositoryTypeProxy, Endpoint: secondUpstream.URL,
	})
	if err != nil {
		t.Fatal(err)
	}
	group, _, err := store.CreateHostedGroupIdempotently(ctx, repository.HostedGroup{
		ID: "go-group", Name: "go-all", Format: repository.FormatGo,
		Members: []repository.GroupMember{
			{RepositoryID: first.ID, Position: 0},
			{RepositoryID: second.ID, Position: 1},
		},
	}, "admin", "go-group", "payload")
	if err != nil {
		t.Fatal(err)
	}
	objects := NewMemoryOCIObjectStore()
	handler := NewGatewayHandler(Dependencies{NativeGoObjectStore: objects}, store, TestAdapter{}, testAuthenticator(), UpstreamClient{HTTPClient: firstUpstream.Client()})
	get := func(path string) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(http.MethodGet, path, nil)
		authorize(request, "resolver-secret")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	base := "/go/" + group.Name + "/" + escapedModule
	if response := get(base + "/@v/list"); response.Code != http.StatusOK || response.Body.String() != "v1.0.0\nv1.1.0\n" {
		t.Fatalf("group list=%d body=%q", response.Code, response.Body.String())
	}
	if response := get(base + "/@v/v1.1.0.mod"); response.Code != http.StatusOK || !bytes.Equal(response.Body.Bytes(), modV2) {
		t.Fatalf("group mod=%d body=%q", response.Code, response.Body.String())
	}
	secondUpstream.Close()
	if response := get(base + "/@v/v1.1.0.mod"); response.Code != http.StatusOK || !bytes.Equal(response.Body.Bytes(), modV2) {
		t.Fatalf("offline group mod=%d body=%q", response.Code, response.Body.String())
	}
}

func TestNativeGoGroupPrefersHostedModuleOverProxyConflict(t *testing.T) {
	const (
		modulePath = "example.com/team/priority"
		version    = "v1.0.0"
	)
	proxyMod := []byte("module " + modulePath + "\n\ngo 1.25\n")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/" + modulePath + "/@v/list":
			_, _ = io.WriteString(w, version+"\n")
		case "/" + modulePath + "/@v/" + version + ".mod":
			_, _ = w.Write(proxyMod)
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	ctx := context.Background()
	store := repository.NewMemoryStore()
	hosted, err := store.CreateHostedRepository(ctx, repository.HostedRepository{
		ID: "go-group-hosted", Name: "go-group-hosted", Format: repository.FormatGo, Type: repository.RepositoryTypeHosted,
	})
	if err != nil {
		t.Fatal(err)
	}
	grantGoPublisher(t, store, hosted.ID)
	proxy, err := store.CreateHostedRepository(ctx, repository.HostedRepository{
		ID: "go-group-proxy", Name: "go-group-proxy", Format: repository.FormatGo, Type: repository.RepositoryTypeProxy, Endpoint: upstream.URL,
	})
	if err != nil {
		t.Fatal(err)
	}
	group, _, err := store.CreateHostedGroupIdempotently(ctx, repository.HostedGroup{
		ID: "go-mixed-group", Name: "go-mixed-group", Format: repository.FormatGo,
		Members: []repository.GroupMember{{RepositoryID: proxy.ID, Position: 0}, {RepositoryID: hosted.ID, Position: 1}},
	}, "admin", "go-mixed-group", "payload")
	if err != nil {
		t.Fatal(err)
	}
	objects := NewMemoryOCIObjectStore()
	handler := NewGatewayHandler(Dependencies{NativeGoObjectStore: objects}, store, TestAdapter{}, testAuthenticator(), UpstreamClient{HTTPClient: upstream.Client()})
	hostedMod := []byte("module " + modulePath + "\n\ngo 1.26\n")
	archive := goModuleFixtureZip(t, modulePath, version, map[string]string{
		"go.mod": string(hostedMod), "priority.go": "package priority\n",
	})
	publish := httptest.NewRequest(http.MethodPut, "/go/"+hosted.Name+"/"+modulePath+"/@v/"+version+".zip", bytes.NewReader(archive))
	authorize(publish, "resolver-secret")
	published := httptest.NewRecorder()
	handler.ServeHTTP(published, publish)
	if published.Code != http.StatusCreated {
		t.Fatalf("publish=%d body=%s", published.Code, published.Body.String())
	}

	get := func(suffix string) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(http.MethodGet, "/go/"+group.Name+"/"+modulePath+suffix, nil)
		authorize(request, "resolver-secret")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	if response := get("/@v/list"); response.Code != http.StatusOK || response.Body.String() != version+"\n" {
		t.Fatalf("group list=%d body=%q", response.Code, response.Body.String())
	}
	if response := get("/@v/" + version + ".mod"); response.Code != http.StatusOK || !bytes.Equal(response.Body.Bytes(), hostedMod) {
		t.Fatalf("group hosted-first mod=%d body=%q", response.Code, response.Body.String())
	}
}

func TestNativeGoGroupFiltersMembersByRepositoryGrant(t *testing.T) {
	upstream := func(version string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, version+"\n")
		}))
	}
	deniedUpstream := upstream("v9.0.0")
	defer deniedUpstream.Close()
	allowedUpstream := upstream("v1.0.0")
	defer allowedUpstream.Close()
	ctx := context.Background()
	store := repository.NewMemoryStore()
	denied, err := store.CreateHostedRepository(ctx, repository.HostedRepository{
		ID: "go-group-denied", Name: "go-group-denied", Format: repository.FormatGo,
		Type: repository.RepositoryTypeProxy, Endpoint: deniedUpstream.URL,
	})
	if err != nil {
		t.Fatal(err)
	}
	allowed, err := store.CreateHostedRepository(ctx, repository.HostedRepository{
		ID: "go-group-allowed", Name: "go-group-allowed", Format: repository.FormatGo,
		Type: repository.RepositoryTypeProxy, Endpoint: allowedUpstream.URL,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.ReplaceRepositoryGrants(ctx, denied.ID, nil, "1"); err != nil {
		t.Fatal(err)
	}
	if _, err = store.ReplaceRepositoryGrants(ctx, allowed.ID, []repository.RepositoryGrant{{
		Principal: "build-agent", Scopes: []string{"repositories:read"}, ResourcePrefix: "example.com/team/",
	}}, "1"); err != nil {
		t.Fatal(err)
	}
	group, _, err := store.CreateHostedGroupIdempotently(ctx, repository.HostedGroup{
		ID: "go-authorized-group", Name: "go-authorized-group", Format: repository.FormatGo,
		Members: []repository.GroupMember{{RepositoryID: denied.ID}, {RepositoryID: allowed.ID, Position: 1}},
	}, "admin", "go-authorized-group", "payload")
	if err != nil {
		t.Fatal(err)
	}
	handler := NewGatewayHandler(Dependencies{NativeGoObjectStore: NewMemoryOCIObjectStore()}, store, TestAdapter{}, testAuthenticator(), UpstreamClient{HTTPClient: allowedUpstream.Client()})
	request := httptest.NewRequest(http.MethodGet, "/go/"+group.Name+"/example.com/team/widget/@v/list", nil)
	authorize(request, "resolver-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Body.String() != "v1.0.0\n" {
		t.Fatalf("authorized group list=%d body=%q", response.Code, response.Body.String())
	}
	for _, audit := range store.Audits {
		if audit.GroupName == group.Name && audit.MemberName == denied.Name && audit.Outcome == repository.AuditAccessDenied {
			return
		}
	}
	t.Fatalf("missing denied member audit: %#v", store.Audits)
}

func TestNativeGoRealClientDownloadsThroughProxyAndOfflineCache(t *testing.T) {
	if os.Getenv("ARTIFACT_GATEWAY_GO_CLI_E2E") == "" {
		t.Skip("set ARTIFACT_GATEWAY_GO_CLI_E2E=1 to run the real Go client acceptance test")
	}
	const (
		modulePath    = "example.com/Acme/widget"
		escapedModule = "example.com/!acme/widget"
		version       = "v1.2.3"
	)
	info := []byte(`{"Version":"v1.2.3","Time":"2026-08-09T09:00:00Z"}`)
	mod := []byte("module " + modulePath + "\n\ngo 1.26\n")
	archive := goModuleFixtureZip(t, modulePath, version, map[string]string{
		"go.mod":    string(mod),
		"widget.go": "package widget\n\nconst Version = \"v1.2.3\"\n",
	})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/" + escapedModule + "/@v/list":
			_, _ = io.WriteString(w, version+"\n")
		case "/" + escapedModule + "/@v/" + version + ".info":
			_, _ = w.Write(info)
		case "/" + escapedModule + "/@v/" + version + ".mod":
			_, _ = w.Write(mod)
		case "/" + escapedModule + "/@v/" + version + ".zip":
			_, _ = w.Write(archive)
		default:
			http.NotFound(w, r)
		}
	}))
	store := repository.NewMemoryStore()
	if _, err := store.ReplaceAnonymousAccessPolicy(context.Background(), repository.AnonymousAccessPolicy{Enabled: true}, "1"); err != nil {
		t.Fatal(err)
	}
	repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{
		ID: "go-cli", Name: "go-public", Format: repository.FormatGo,
		Type: repository.RepositoryTypeProxy, Endpoint: upstream.URL, AnonymousRead: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(NewGatewayHandler(
		Dependencies{NativeGoObjectStore: NewMemoryOCIObjectStore()}, store,
		TestAdapter{}, testAuthenticator(), UpstreamClient{HTTPClient: upstream.Client()},
	))
	defer server.Close()
	temporary := t.TempDir()
	t.Cleanup(func() {
		_ = filepath.WalkDir(temporary, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return nil
			}
			if entry.IsDir() {
				return os.Chmod(path, 0o700)
			}
			return os.Chmod(path, 0o600)
		})
	})
	run := func(cache string) string {
		t.Helper()
		command := exec.CommandContext(context.Background(), "go", "mod", "download", "-json", modulePath+"@"+version)
		command.Dir = temporary
		command.Env = append(os.Environ(),
			"GOPROXY="+server.URL+"/repository/"+repo.Name,
			"GOSUMDB=off",
			"GONOPROXY=none",
			"GOMODCACHE="+cache,
			"GOCACHE="+filepath.Join(temporary, "build-cache"),
		)
		output, runErr := command.CombinedOutput()
		if runErr != nil {
			t.Fatalf("go mod download failed: %v\n%s", runErr, output)
		}
		return string(output)
	}
	first := run(filepath.Join(temporary, "module-cache-first"))
	if !strings.Contains(first, `"Path": "`+modulePath+`"`) || !strings.Contains(first, `"Version": "`+version+`"`) {
		t.Fatalf("unexpected go download output: %s", first)
	}
	upstream.Close()
	second := run(filepath.Join(temporary, "module-cache-offline"))
	if !strings.Contains(second, `"Version": "`+version+`"`) {
		t.Fatalf("unexpected offline go download output: %s", second)
	}
}

func TestNativeGoRealClientDownloadsHostedPublication(t *testing.T) {
	if os.Getenv("ARTIFACT_GATEWAY_GO_CLI_E2E") == "" {
		t.Skip("set ARTIFACT_GATEWAY_GO_CLI_E2E=1 to run the real Go client acceptance test")
	}
	const (
		modulePath    = "example.com/Acme/hosted-client"
		escapedModule = "example.com/!acme/hosted-client"
		version       = "v1.3.0"
	)
	mod := []byte("module " + modulePath + "\n\ngo 1.26\n")
	archive := goModuleFixtureZip(t, modulePath, version, map[string]string{
		"go.mod": string(mod), "hosted.go": "package hostedclient\n\nconst Version = \"v1.3.0\"\n",
	})
	store := repository.NewMemoryStore()
	if _, err := store.ReplaceAnonymousAccessPolicy(context.Background(), repository.AnonymousAccessPolicy{Enabled: true}, "1"); err != nil {
		t.Fatal(err)
	}
	repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{
		ID: "go-hosted-cli", Name: "go-hosted-cli", Format: repository.FormatGo,
		Type: repository.RepositoryTypeHosted, AnonymousRead: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	grantGoPublisher(t, store, repo.ID)
	server := httptest.NewServer(NewGatewayHandler(
		Dependencies{NativeGoObjectStore: NewMemoryOCIObjectStore()}, store,
		TestAdapter{}, testAuthenticator(),
	))
	defer server.Close()
	publish, err := http.NewRequest(http.MethodPut, server.URL+"/repository/"+repo.Name+"/"+version+".zip", bytes.NewReader(archive))
	if err != nil {
		t.Fatal(err)
	}
	authorize(publish, "resolver-secret")
	response, err := server.Client().Do(publish)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("publish=%d", response.StatusCode)
	}
	temporary := t.TempDir()
	t.Cleanup(func() {
		_ = filepath.WalkDir(temporary, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return nil
			}
			if entry.IsDir() {
				return os.Chmod(path, 0o700)
			}
			return os.Chmod(path, 0o600)
		})
	})
	command := exec.CommandContext(context.Background(), "go", "mod", "download", "-json", modulePath+"@"+version)
	command.Dir = temporary
	command.Env = append(os.Environ(),
		"GOPROXY="+server.URL+"/repository/"+repo.Name,
		"GOSUMDB=off",
		"GONOSUMDB=*",
		"GOMODCACHE="+filepath.Join(temporary, "gomodcache"),
		"GOCACHE="+filepath.Join(temporary, "gocache"),
	)
	output, err := command.CombinedOutput()
	if err != nil || !bytes.Contains(output, []byte(`"Version": "v1.3.0"`)) {
		t.Fatalf("go mod download err=%v output=%s", err, output)
	}
}

func goModuleFixtureZip(t *testing.T, modulePath, version string, files map[string]string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		contents := files[name]
		entry, err := writer.Create(modulePath + "@" + version + "/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = io.WriteString(entry, contents); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func grantGoPublisher(t testing.TB, store repository.RepositoryGrantStore, repositoryID string) {
	t.Helper()
	if _, err := store.ReplaceRepositoryGrants(context.Background(), repositoryID, []repository.RepositoryGrant{{
		Principal: "build-agent", Scopes: []string{"repositories:write"},
	}}, "1"); err != nil {
		t.Fatal(err)
	}
}
