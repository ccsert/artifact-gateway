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

// publishQuarantineReadGoVersion publishes one Hosted module version and
// returns its authoritative ZIP digest, which is the distribution anchor used
// by the quarantine API.
func publishQuarantineReadGoVersion(t *testing.T, store *repository.MemoryStore, objects *MemoryOCIObjectStore, repo repository.HostedRepository, modulePath, version string) string {
	t.Helper()
	archive := goModuleFixtureZip(t, modulePath, version, map[string]string{
		"go.mod": "module " + modulePath + "\n\ngo 1.26\n",
	})
	handler := NewGatewayHandler(Dependencies{NativeGoObjectStore: objects}, store, TestAdapter{}, testAuthenticator())
	request := httptest.NewRequest(
		http.MethodPut,
		"/go/"+repo.Name+"/"+modulePath+"/@v/"+version+".zip",
		bytes.NewReader(archive),
	)
	authorize(request, "resolver-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated && response.Code != http.StatusOK {
		t.Fatalf("publish %s@%s=%d body=%s", modulePath, version, response.Code, response.Body.String())
	}
	asset, err := store.GetGoModuleAsset(context.Background(), repo.ID, modulePath, version, "zip")
	if err != nil {
		t.Fatal(err)
	}
	return asset.Digest
}

func TestGoQuarantineReadPolicyHidesVersionAndBlocksEveryRepresentation(t *testing.T) {
	const (
		modulePath = "example.com/acme/widget"
		version    = "v1.0.0"
	)
	ctx := context.Background()
	store := repository.NewMemoryStore()
	objects := NewMemoryOCIObjectStore()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{
		ID: "go-read-hosted", Name: "go-read-hosted", Format: repository.FormatGo,
		Type: repository.RepositoryTypeHosted,
	})
	if err != nil {
		t.Fatal(err)
	}
	grantGoPublisher(t, store, repo.ID)
	visibleDigest := publishQuarantineReadGoVersion(t, store, objects, repo, modulePath, "v1.1.0")
	blockedDigest := publishQuarantineReadGoVersion(t, store, objects, repo, modulePath, version)
	if visibleDigest == blockedDigest {
		t.Fatal("fixture digests must differ")
	}

	// Without an enabled read policy the hosted repository stays
	// backward-compatible even after a quarantine is recorded.
	if _, err = store.ReplaceArtifactQuarantine(ctx, repository.ArtifactQuarantine{
		RepositoryID: repo.ID, Format: repository.FormatGo, Coordinate: modulePath + "@" + version,
		Digest: blockedDigest, State: repository.ArtifactQuarantineStateQuarantined,
		Reason: "malware confirmed", UpdatedBy: "security-admin",
	}, "0"); err != nil {
		t.Fatal(err)
	}
	handler := NewGatewayHandler(Dependencies{NativeGoObjectStore: objects}, store, TestAdapter{}, testAuthenticator())
	get := func(path string) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(http.MethodGet, path, nil)
		authorize(request, "resolver-secret")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	base := "/go/" + repo.Name + "/" + modulePath + "/@v"
	if response := get(base + "/" + version + ".zip"); response.Code != http.StatusOK {
		t.Fatalf("compatible read=%d body=%q", response.Code, response.Body.String())
	}
	if response := get(base + "/list"); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), version) {
		t.Fatalf("compatible list=%d body=%q", response.Code, response.Body.String())
	}

	enableQuarantineReadPolicy(t, store, repo.ID)
	if response := get(base + "/list"); response.Code != http.StatusOK ||
		response.Body.String() != "v1.1.0\n" {
		t.Fatalf("filtered list=%d body=%q", response.Code, response.Body.String())
	}
	for _, kind := range []string{"info", "mod", "zip"} {
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			response := goQuarantineReadRequest(t, handler, method, base+"/"+version+"."+kind)
			if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), repository.ArtifactQuarantinedReason) {
				t.Fatalf("%s %s=%d body=%q", method, kind, response.Code, response.Body.String())
			}
		}
	}
	// /@latest must not resolve to the quarantined version.
	latest := get("/go/" + repo.Name + "/" + modulePath + "/@latest")
	if latest.Code == http.StatusOK && strings.Contains(latest.Body.String(), version) {
		t.Fatalf("latest leaked quarantined version=%q", latest.Body.String())
	}
	requireQuarantineReadDeniedAudit(t, store, repository.FormatGo)
}

func goQuarantineReadRequest(t *testing.T, handler http.Handler, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, nil)
	authorize(request, "resolver-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func TestGoGroupDoesNotReintroduceQuarantinedVersionFromLowerPriorityMember(t *testing.T) {
	const modulePath = "example.com/acme/grouped"
	ctx := context.Background()
	store := repository.NewMemoryStore()
	objects := NewMemoryOCIObjectStore()
	first, err := store.CreateHostedRepository(ctx, repository.HostedRepository{
		ID: "go-read-first", Name: "go-read-first", Format: repository.FormatGo,
		Type: repository.RepositoryTypeHosted,
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.CreateHostedRepository(ctx, repository.HostedRepository{
		ID: "go-read-second", Name: "go-read-second", Format: repository.FormatGo,
		Type: repository.RepositoryTypeHosted,
	})
	if err != nil {
		t.Fatal(err)
	}
	grantGoPublisher(t, store, first.ID)
	grantGoPublisher(t, store, second.ID)
	digest := publishQuarantineReadGoVersion(t, store, objects, first, modulePath, "v1.0.0")
	publishQuarantineReadGoVersion(t, store, objects, second, modulePath, "v1.0.0")

	group, _, err := store.CreateHostedGroupIdempotently(ctx, repository.HostedGroup{
		ID: "go-read-group", Name: "go-read-all", Format: repository.FormatGo,
		Members: []repository.GroupMember{
			{RepositoryID: first.ID, Position: 0},
			{RepositoryID: second.ID, Position: 1},
		},
	}, "admin", "go-read-group", "payload")
	if err != nil {
		t.Fatal(err)
	}
	enableQuarantineReadPolicy(t, store, first.ID)
	if _, err = store.ReplaceArtifactQuarantine(ctx, repository.ArtifactQuarantine{
		RepositoryID: first.ID, Format: repository.FormatGo, Coordinate: modulePath + "@v1.0.0",
		Digest: digest, State: repository.ArtifactQuarantineStateQuarantined,
		Reason: "malware confirmed", UpdatedBy: "security-admin",
	}, "0"); err != nil {
		t.Fatal(err)
	}
	handler := NewGatewayHandler(Dependencies{NativeGoObjectStore: objects}, store, TestAdapter{}, testAuthenticator())
	listRequest := httptest.NewRequest(http.MethodGet, "/go/"+group.Name+"/"+modulePath+"/@v/list", nil)
	authorize(listRequest, "resolver-secret")
	listResponse := httptest.NewRecorder()
	handler.ServeHTTP(listResponse, listRequest)
	if listResponse.Code != http.StatusNotFound {
		t.Fatalf("group list=%d body=%q", listResponse.Code, listResponse.Body.String())
	}
	assetRequest := httptest.NewRequest(http.MethodGet, "/go/"+group.Name+"/"+modulePath+"/@v/v1.0.0.zip", nil)
	authorize(assetRequest, "resolver-secret")
	assetResponse := httptest.NewRecorder()
	handler.ServeHTTP(assetResponse, assetRequest)
	if assetResponse.Code != http.StatusForbidden || !strings.Contains(assetResponse.Body.String(), repository.ArtifactQuarantinedReason) {
		t.Fatalf("group asset=%d body=%q", assetResponse.Code, assetResponse.Body.String())
	}
}
