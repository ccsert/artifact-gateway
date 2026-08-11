package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	adminopenapi "github.com/artifact-gateway/artifact-gateway/internal/admin/openapi"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

func TestFormatProfilesAPIRequiresAdministratorAndReturnsCapabilities(t *testing.T) {
	store := repository.NewMemoryStore()
	authenticator := testAuthenticator()
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, authenticator)

	unauthenticated := httptest.NewRecorder()
	handler.ServeHTTP(unauthenticated, httptest.NewRequest(http.MethodGet, "/api/v2/formats", nil))
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated=%d %s", unauthenticated.Code, unauthenticated.Body.String())
	}

	nonAdminRequest := httptest.NewRequest(http.MethodGet, "/api/v2/formats", nil)
	authorize(nonAdminRequest, authenticator.IssueToken("reader"))
	nonAdmin := httptest.NewRecorder()
	handler.ServeHTTP(nonAdmin, nonAdminRequest)
	if nonAdmin.Code != http.StatusUnauthorized {
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
		if item.Format == adminopenapi.Format("go") || item.Format == adminopenapi.Format("apt") {
			if !item.GroupSupported || len(item.RepositoryTypes) != 1 || item.RepositoryTypes[0] != adminopenapi.FormatProfileRepositoryTypesProxy || len(item.HostedOperations) != 0 || len(item.ProxyOperations) != 2 {
				t.Errorf("go must expose only executable Proxy and Group capabilities: %#v", item)
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
