package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

const ociBearerConfigJSON = `{"realm":"https://auth.example.test/token","service":"registry.example.test"}`

func ociBearerManagementRequest(handler http.Handler, method, path, body, version, key string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	authorize(r, "admin-secret")
	if version != "" {
		r.Header.Set("If-Match", version)
	}
	if key != "" {
		r.Header.Set("Idempotency-Key", key)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, r)
	return rec
}

func TestRepositoryOCIBearerConfigRoundTripAndIdempotency(t *testing.T) {
	store := repository.NewMemoryStore()
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())
	body := `{"name":"oci-proxy","format":"oci","type":"proxy","endpoint":"https://registry.example.test","allowedHosts":["registry.example.test"],"ociBearer":` + ociBearerConfigJSON + `}`
	created := ociBearerManagementRequest(handler, http.MethodPost, "/api/v2/repositories", body, "", "create-oci-bearer")
	if created.Code != http.StatusCreated {
		t.Fatalf("create=%d body=%s", created.Code, created.Body.String())
	}
	var response struct {
		ID           string          `json:"id"`
		Version      string          `json:"version"`
		AllowedHosts []string        `json:"allowedHosts"`
		OCIBearer    json.RawMessage `json:"ociBearer"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if string(response.OCIBearer) != ociBearerConfigJSON || len(response.AllowedHosts) != 1 || response.AllowedHosts[0] != "registry.example.test" {
		t.Fatalf("config or existing allow-list changed: %s", created.Body.String())
	}
	read := ociBearerManagementRequest(handler, http.MethodGet, "/api/v2/repositories/"+response.ID, "", "", "")
	if read.Code != http.StatusOK || !strings.Contains(read.Body.String(), `"ociBearer":`+ociBearerConfigJSON) {
		t.Fatalf("read=%d body=%s", read.Code, read.Body.String())
	}
	listed := ociBearerManagementRequest(handler, http.MethodGet, "/api/v2/repositories", "", "", "")
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), `"ociBearer":`+ociBearerConfigJSON) {
		t.Fatalf("list=%d body=%s", listed.Code, listed.Body.String())
	}
	replayed := ociBearerManagementRequest(handler, http.MethodPost, "/api/v2/repositories", body, "", "create-oci-bearer")
	if replayed.Code != http.StatusCreated || replayed.Body.String() != created.Body.String() {
		t.Fatalf("idempotent replay=%d body=%s", replayed.Code, replayed.Body.String())
	}
	reorderedBody := strings.Replace(body, ociBearerConfigJSON, `{ "service":"registry.example.test", "realm":"https://auth.example.test/token" }`, 1)
	reordered := ociBearerManagementRequest(handler, http.MethodPost, "/api/v2/repositories", reorderedBody, "", "create-oci-bearer")
	if reordered.Code != http.StatusCreated || reordered.Body.String() != created.Body.String() {
		t.Fatalf("semantically equivalent replay=%d body=%s", reordered.Code, reordered.Body.String())
	}
	conflict := ociBearerManagementRequest(handler, http.MethodPost, "/api/v2/repositories", strings.ReplaceAll(body, "auth.example.test", "other.example.test"), "", "create-oci-bearer")
	if conflict.Code != http.StatusConflict {
		t.Fatalf("changed configuration replay=%d body=%s", conflict.Code, conflict.Body.String())
	}
	configured := ociBearerManagementRequest(handler, http.MethodPatch, "/api/v2/repositories/"+response.ID, `{"ociBearer":{"realm":"https://auth.example.test/v2/token","service":"next-service"}}`, "1", "")
	if configured.Code != http.StatusOK || configured.Header().Get("ETag") != "2" {
		t.Fatalf("configure=%d body=%s", configured.Code, configured.Body.String())
	}
	kept := ociBearerManagementRequest(handler, http.MethodPatch, "/api/v2/repositories/"+response.ID, `{"anonymousRead":false}`, "2", "")
	if kept.Code != http.StatusOK || !strings.Contains(kept.Body.String(), `"service":"next-service"`) {
		t.Fatalf("omitted configuration=%d body=%s", kept.Code, kept.Body.String())
	}
	stale := ociBearerManagementRequest(handler, http.MethodPatch, "/api/v2/repositories/"+response.ID, `{"ociBearer":null}`, "2", "")
	if stale.Code != http.StatusPreconditionFailed {
		t.Fatalf("stale clear=%d body=%s", stale.Code, stale.Body.String())
	}
	cleared := ociBearerManagementRequest(handler, http.MethodPatch, "/api/v2/repositories/"+response.ID, `{"ociBearer":null}`, "3", "")
	if cleared.Code != http.StatusOK || strings.Contains(cleared.Body.String(), `"ociBearer"`) || cleared.Header().Get("ETag") != "4" {
		t.Fatalf("clear=%d body=%s", cleared.Code, cleared.Body.String())
	}
}

func TestRepositoryOCIBearerConfigValidation(t *testing.T) {
	for name, config := range map[string]string{
		"empty":              `{}`,
		"http realm":         `{"realm":"http://auth.example.test/token","service":"registry"}`,
		"relative realm":     `{"realm":"/token","service":"registry"}`,
		"missing host":       `{"realm":"https:///token","service":"registry"}`,
		"userinfo":           `{"realm":"https://user:secret@auth.example.test/token","service":"registry"}`,
		"fragment":           `{"realm":"https://auth.example.test/token#fragment","service":"registry"}`,
		"query":              `{"realm":"https://auth.example.test/token?a=b","service":"registry"}`,
		"empty query":        `{"realm":"https://auth.example.test/token?","service":"registry"}`,
		"empty fragment":     `{"realm":"https://auth.example.test/token#","service":"registry"}`,
		"invalid port":       `{"realm":"https://auth.example.test:0/token","service":"registry"}`,
		"blank service":      `{"realm":"https://auth.example.test/token","service":""}`,
		"space in service":   `{"realm":"https://auth.example.test/token","service":"a b"}`,
		"control in service": `{"realm":"https://auth.example.test/token","service":"a\u0000b"}`,
		"long service":       `{"realm":"https://auth.example.test/token","service":"` + strings.Repeat("a", 257) + `"}`,
		"credential field":   `{"realm":"https://auth.example.test/token","service":"registry","token":"synthetic-secret"}`,
		"array":              `[]`,
		"string":             `"none"`,
	} {
		t.Run(name, func(t *testing.T) {
			store := repository.NewMemoryStore()
			handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())
			body := `{"name":"oci-proxy","format":"oci","type":"proxy","endpoint":"https://registry.example.test","ociBearer":` + config + `}`
			created := ociBearerManagementRequest(handler, http.MethodPost, "/api/v2/repositories", body, "", "invalid-bearer")
			if created.Code != http.StatusBadRequest || strings.Contains(created.Body.String(), "synthetic-secret") {
				t.Fatalf("invalid create=%d body=%s", created.Code, created.Body.String())
			}
			items, _, err := store.ListHostedRepositories(t.Context(), 50, "")
			if err != nil || len(items) != 0 {
				t.Fatalf("invalid configuration persisted: items=%d err=%v", len(items), err)
			}
			repo, err := store.CreateHostedRepository(t.Context(), repository.HostedRepository{ID: "00000000-0000-0000-0000-000000000001", Name: "oci-existing", Format: repository.FormatOCI, Type: repository.RepositoryTypeProxy, Endpoint: "https://registry.example.test"})
			if err != nil {
				t.Fatal(err)
			}
			updated := ociBearerManagementRequest(handler, http.MethodPatch, "/api/v2/repositories/"+repo.ID, `{"ociBearer":`+config+`}`, "1", "")
			if updated.Code != http.StatusBadRequest {
				t.Fatalf("invalid update=%d body=%s", updated.Code, updated.Body.String())
			}
			stored, err := store.GetHostedRepository(t.Context(), repo.ID)
			if err != nil || stored.Version != "1" {
				t.Fatalf("invalid update changed version=%q err=%v", stored.Version, err)
			}
		})
	}
}

func TestRepositoryOCIBearerConfigIsOCIProxyOnlyAndOptIn(t *testing.T) {
	for _, fixture := range []struct {
		format repository.Format
		typeOf repository.RepositoryType
	}{
		{repository.FormatOCI, repository.RepositoryTypeHosted},
		{repository.FormatMaven, repository.RepositoryTypeProxy},
		{repository.FormatGo, repository.RepositoryTypeProxy},
	} {
		t.Run(string(fixture.format)+"-"+string(fixture.typeOf), func(t *testing.T) {
			store := repository.NewMemoryStore()
			handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())
			base := `{"name":"other-repo","format":"` + string(fixture.format) + `","type":"` + string(fixture.typeOf) + `"`
			if fixture.typeOf == repository.RepositoryTypeProxy {
				base += `,"endpoint":"https://registry.example.test","allowedHosts":["registry.example.test"]`
			}
			for _, config := range []string{ociBearerConfigJSON, "null"} {
				created := ociBearerManagementRequest(handler, http.MethodPost, "/api/v2/repositories", base+`,"ociBearer":`+config+`}`, "", "other-bearer")
				if created.Code != http.StatusBadRequest {
					t.Fatalf("unsupported create=%d body=%s", created.Code, created.Body.String())
				}
			}
			created := ociBearerManagementRequest(handler, http.MethodPost, "/api/v2/repositories", base+`}`, "", "create-other")
			if created.Code != http.StatusCreated || strings.Contains(created.Body.String(), `"ociBearer"`) {
				t.Fatalf("legacy config=%d body=%s", created.Code, created.Body.String())
			}
			var repo repository.HostedRepository
			if err := json.Unmarshal(created.Body.Bytes(), &repo); err != nil {
				t.Fatal(err)
			}
			for _, config := range []string{ociBearerConfigJSON, "null"} {
				updated := ociBearerManagementRequest(handler, http.MethodPatch, "/api/v2/repositories/"+repo.ID, `{"ociBearer":`+config+`}`, "1", "")
				if updated.Code != http.StatusBadRequest {
					t.Fatalf("unsupported update=%d body=%s", updated.Code, updated.Body.String())
				}
			}
		})
	}
}

// Legacy Group members share the strict JSON decoder used by management.
// Runtime-only projection must neither accept nor expose the V2 configuration.
func TestLegacyGroupMemberRejectsOCIBearerConfiguration(t *testing.T) {
	for _, value := range []string{ociBearerConfigJSON, "null"} {
		decoder := json.NewDecoder(strings.NewReader(`{"name":"oci-proxy","type":"proxy","ociBearer":` + value + `}`))
		decoder.DisallowUnknownFields()
		var member repository.Member
		if err := decoder.Decode(&member); err == nil {
			t.Fatal("legacy member accepted runtime-only Bearer configuration")
		}
	}
	encoded, err := json.Marshal(repository.Member{OCIBearer: &repository.OCIBearer{Realm: "https://auth.example.test/token", Service: "registry"}})
	if err != nil || strings.Contains(string(encoded), "ociBearer") {
		t.Fatalf("legacy member exposed runtime projection: %s err=%v", encoded, err)
	}
}
