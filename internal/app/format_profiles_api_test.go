package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	adminopenapi "github.com/artifact-gateway/artifact-gateway/internal/admin/openapi"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

func TestFormatProfilesAPINeedsAnAuthenticatedCallerAndReturnsCapabilities(t *testing.T) {
	store := repository.NewMemoryStore()
	authenticator := testAuthenticator()
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, authenticator)

	unauthenticated := httptest.NewRecorder()
	handler.ServeHTTP(unauthenticated, httptest.NewRequest(http.MethodGet, "/api/v2/formats", nil))
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated=%d %s", unauthenticated.Code, unauthenticated.Body.String())
	}

	// The format table carries no repository state, so an authenticated caller
	// that holds no platform administration may read it.
	nonAdminRequest := httptest.NewRequest(http.MethodGet, "/api/v2/formats", nil)
	authorize(nonAdminRequest, authenticator.IssueToken("reader"))
	nonAdmin := httptest.NewRecorder()
	handler.ServeHTTP(nonAdmin, nonAdminRequest)
	if nonAdmin.Code != http.StatusOK {
		t.Fatalf("non-admin=%d %s", nonAdmin.Code, nonAdmin.Body.String())
	}

	request := httptest.NewRequest(http.MethodGet, "/api/v2/formats", nil)
	authorize(request, "admin-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("list=%d %s", response.Code, response.Body.String())
	}
	var result adminopenapi.FormatProfileList
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != len(repository.SupportedFormats()) {
		t.Fatalf("profiles=%d want=%d", len(result.Items), len(repository.SupportedFormats()))
	}
	for _, item := range result.Items {
		if !item.AnonymousRead {
			t.Errorf("incomplete profile: %#v", item)
		}
		if item.Format == adminopenapi.Format("apt") {
			if !item.GroupSupported || len(item.RepositoryTypes) != 1 || item.RepositoryTypes[0] != adminopenapi.FormatProfileRepositoryTypesProxy || len(item.HostedOperations) != 0 || len(item.ProxyOperations) != 2 {
				t.Errorf("APT must expose only executable Proxy and Group capabilities: %#v", item)
			}
			continue
		}
		if item.Format == adminopenapi.FormatGo || item.Format == adminopenapi.FormatCargo {
			if !item.GroupSupported || len(item.RepositoryTypes) != 2 || len(item.HostedOperations) != 9 || len(item.ProxyOperations) != 2 {
				t.Errorf("%s must expose Hosted lifecycle plus Proxy and Group: %#v", item.Format, item)
			}
			continue
		}
		if item.Format == adminopenapi.FormatNpm {
			if !item.GroupSupported || len(item.RepositoryTypes) != 2 || len(item.ProxyOperations) != 2 {
				t.Errorf("npm must expose Hosted, Proxy, and Group read/browse: %#v", item)
			}
			continue
		}
		if !item.GroupSupported || len(item.RepositoryTypes) != 2 {
			t.Errorf("incomplete lifecycle profile: %#v", item)
		}
		operations := make(map[adminopenapi.RepositoryOperation]bool, len(item.HostedOperations))
		for _, operation := range item.HostedOperations {
			operations[operation] = true
		}
		for _, operation := range []adminopenapi.RepositoryOperation{
			adminopenapi.RepositoryOperationRetain,
			adminopenapi.RepositoryOperationRestore,
			adminopenapi.RepositoryOperationPromote,
			adminopenapi.RepositoryOperationReplicate,
		} {
			if !operations[operation] {
				t.Errorf("format %q missing %q", item.Format, operation)
			}
		}
	}
}

func TestCargoPublicAdmissionCreatesHostedProxyAndGroup(t *testing.T) {
	store := repository.NewMemoryStore()
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())
	call := func(method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		authorize(request, "admin-secret")
		if method == http.MethodPost {
			request.Header.Set("Idempotency-Key", uuid.NewString())
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	create := func(body string) repository.HostedRepository {
		t.Helper()
		response := call(http.MethodPost, "/api/v2/repositories", body)
		if response.Code != http.StatusCreated {
			t.Fatalf("create Cargo repository=%d %s", response.Code, response.Body.String())
		}
		var repo repository.HostedRepository
		if err := json.Unmarshal(response.Body.Bytes(), &repo); err != nil {
			t.Fatal(err)
		}
		return repo
	}
	hosted := create(`{"name":"cargo-public-hosted","format":"cargo"}`)
	if hosted.Format != repository.FormatCargo || hosted.Type != repository.RepositoryTypeHosted {
		t.Fatalf("Hosted=%+v", hosted)
	}
	if response := call(http.MethodPost, "/api/v2/repositories", `{"name":"cargo-no-hosts","format":"cargo","type":"proxy","endpoint":"https://index.crates.io"}`); response.Code != http.StatusBadRequest {
		t.Fatalf("Proxy without egress allowlist=%d %s", response.Code, response.Body.String())
	}
	proxy := create(`{"name":"cargo-public-proxy","format":"cargo","type":"proxy","endpoint":"https://index.crates.io","allowedHosts":["index.crates.io","static.crates.io"]}`)
	if proxy.Format != repository.FormatCargo || proxy.Type != repository.RepositoryTypeProxy {
		t.Fatalf("Proxy=%+v", proxy)
	}
	for _, fixture := range []struct {
		repo repository.HostedRepository
		want []adminopenapi.RepositoryOperation
	}{
		{hosted, []adminopenapi.RepositoryOperation{adminopenapi.RepositoryOperationRead, adminopenapi.RepositoryOperationPublish, adminopenapi.RepositoryOperationRetain, adminopenapi.RepositoryOperationPromote, adminopenapi.RepositoryOperationReplicate}},
		{proxy, []adminopenapi.RepositoryOperation{adminopenapi.RepositoryOperationRead, adminopenapi.RepositoryOperationBrowse}},
	} {
		response := call(http.MethodGet, "/api/v2/repositories/"+fixture.repo.ID+"/capabilities", "")
		var capabilities adminopenapi.RepositoryCapabilities
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &capabilities) != nil {
			t.Fatalf("capabilities=%d %s", response.Code, response.Body.String())
		}
		if capabilities.Format != adminopenapi.FormatCargo || len(capabilities.Operations) != len(fixture.want) && fixture.repo.Type == repository.RepositoryTypeProxy {
			t.Fatalf("capabilities=%+v", capabilities)
		}
		for _, operation := range fixture.want {
			found := false
			for _, got := range capabilities.Operations {
				found = found || got == operation
			}
			if !found {
				t.Fatalf("%s missing %s: %+v", fixture.repo.Type, operation, capabilities)
			}
		}
	}
	groupBody := `{"name":"cargo-public-group","format":"cargo","members":[{"repositoryId":"` + hosted.ID + `","position":0},{"repositoryId":"` + proxy.ID + `","position":1}]}`
	if response := call(http.MethodPost, "/api/v2/groups", groupBody); response.Code != http.StatusCreated {
		t.Fatalf("Cargo group=%d %s", response.Code, response.Body.String())
	}
}

func TestBackgroundOperationMetricFormatsTrackProfiles(t *testing.T) {
	formats := repository.SupportedFormats()
	if len(backgroundOperationFormats) != len(formats) || len(formats) != int(backgroundOperationFormatCount) {
		t.Fatalf("metric formats=%v profiles=%v count=%d", backgroundOperationFormats, formats, backgroundOperationFormatCount)
	}
	for index, format := range formats {
		if backgroundOperationFormats[index] != format {
			t.Fatalf("metric format[%d]=%q want=%q", index, backgroundOperationFormats[index], format)
		}
	}
}
