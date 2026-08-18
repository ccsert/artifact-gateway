package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/authorization"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

func TestOCIClientLoginExchangesServiceAccountBasicCredentialForStablePrincipalToken(t *testing.T) {
	store := repository.NewMemoryStore()
	account, err := store.CreateServiceAccount(context.Background(), repository.ServiceAccount{
		ID: uuid.NewString(), Name: "oci-release-bot",
	})
	if err != nil {
		t.Fatal(err)
	}
	credentialToken := "agc_oci-service-account-token"
	expiresAt := time.Now().UTC().Add(time.Hour)
	if _, err := store.CreateAPIKey(context.Background(), repository.APIKey{
		ID: uuid.NewString(), ServiceAccountID: account.ID, Name: "docker-login",
		SecretHash: authorization.HashAPIKey(credentialToken), ExpiresAt: &expiresAt,
	}); err != nil {
		t.Fatal(err)
	}
	handler := NewGatewayHandler(
		Dependencies{}, store, TestAdapter{},
		Authenticator{ResolverToken: "principal-token-signing-secret", APIKeys: store},
	)

	tokenRequest := httptest.NewRequest(http.MethodGet, "/auth/token", nil)
	tokenRequest.SetBasicAuth("ci", credentialToken)
	tokenResponse := httptest.NewRecorder()
	handler.ServeHTTP(tokenResponse, tokenRequest)
	if tokenResponse.Code != http.StatusOK {
		t.Fatalf("OCI token exchange status=%d body=%s", tokenResponse.Code, tokenResponse.Body.String())
	}
	var issued struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(tokenResponse.Body).Decode(&issued); err != nil || issued.Token == "" {
		t.Fatalf("decode OCI token: token=%q err=%v", issued.Token, err)
	}

	identityRequest := httptest.NewRequest(http.MethodGet, "/api/v2/identity", nil)
	identityRequest.Header.Set("Authorization", "Bearer "+issued.Token)
	identityResponse := httptest.NewRecorder()
	handler.ServeHTTP(identityResponse, identityRequest)
	if identityResponse.Code != http.StatusOK {
		t.Fatalf("issued principal token status=%d body=%s", identityResponse.Code, identityResponse.Body.String())
	}
	var identity struct {
		Actor string `json:"actor"`
		Kind  string `json:"kind"`
	}
	if err := json.NewDecoder(identityResponse.Body).Decode(&identity); err != nil {
		t.Fatal(err)
	}
	if identity.Actor != "service-account:"+account.ID || identity.Kind != "service_account_credential" {
		t.Fatalf("identity=%#v", identity)
	}
	disabled := repository.ServiceAccountDisabled
	if _, err := store.UpdateServiceAccount(context.Background(), repository.ServiceAccountUpdate{
		ID: account.ID, State: &disabled,
	}, account.Version); err != nil {
		t.Fatal(err)
	}
	identityAfterDisable := httptest.NewRecorder()
	handler.ServeHTTP(identityAfterDisable, identityRequest.Clone(context.Background()))
	if identityAfterDisable.Code != http.StatusUnauthorized {
		t.Fatalf("issued OCI token survived account disable: status=%d body=%s", identityAfterDisable.Code, identityAfterDisable.Body.String())
	}
}

func TestServiceAccountCredentialsKeepOneStablePrincipalAcrossRotation(t *testing.T) {
	store := repository.NewMemoryStore()
	handler := NewGatewayHandler(
		Dependencies{},
		store,
		TestAdapter{},
		Authenticator{AdminToken: "root", AdminActor: "root", APIKeys: store},
	)

	request := func(method, path, token, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		return response
	}

	created := request(
		http.MethodPost,
		"/api/v2/service-accounts",
		"root",
		`{"name":"pipeone-ci","description":"Publishes PipeOne artifacts"}`,
	)
	if created.Code != http.StatusCreated {
		t.Fatalf("create service account status=%d body=%s", created.Code, created.Body.String())
	}
	var account struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		Description string `json:"description"`
		State       string `json:"state"`
	}
	if err := json.NewDecoder(created.Body).Decode(&account); err != nil {
		t.Fatal(err)
	}
	if account.ID == "" || account.Name != "pipeone-ci" || account.Description != "Publishes PipeOne artifacts" || account.State != "active" {
		t.Fatalf("created account=%#v", account)
	}

	type credentialResponse struct {
		ID               string `json:"id"`
		ServiceAccountID string `json:"serviceAccountId"`
		Name             string `json:"name"`
		Token            string `json:"token"`
	}
	createCredential := func(name string) credentialResponse {
		response := request(
			http.MethodPost,
			"/api/v2/service-accounts/"+account.ID+"/credentials",
			"root",
			`{"name":"`+name+`"}`,
		)
		if response.Code != http.StatusCreated {
			t.Fatalf("create credential %q status=%d body=%s", name, response.Code, response.Body.String())
		}
		var credential credentialResponse
		if err := json.NewDecoder(response.Body).Decode(&credential); err != nil {
			t.Fatal(err)
		}
		if credential.ID == "" || credential.ServiceAccountID != account.ID || credential.Name != name || credential.Token == "" {
			t.Fatalf("created credential=%#v", credential)
		}
		return credential
	}

	oldCredential := createCredential("jenkins-old")
	newCredential := createCredential("jenkins-new")
	wantActor := "service-account:" + account.ID
	assertIdentity := func(token string, wantStatus int) {
		response := request(http.MethodGet, "/api/v2/identity", token, "")
		if response.Code != wantStatus {
			t.Fatalf("identity status=%d body=%s want=%d", response.Code, response.Body.String(), wantStatus)
		}
		if wantStatus != http.StatusOK {
			return
		}
		var identity struct {
			Actor string `json:"actor"`
			Kind  string `json:"kind"`
		}
		if err := json.NewDecoder(response.Body).Decode(&identity); err != nil {
			t.Fatal(err)
		}
		if identity.Actor != wantActor || identity.Kind != "service_account_credential" {
			t.Fatalf("identity=%#v want actor=%q", identity, wantActor)
		}
	}

	assertIdentity(oldCredential.Token, http.StatusOK)
	assertIdentity(newCredential.Token, http.StatusOK)
	repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{
		ID: uuid.NewString(), Name: "service-account-releases", Format: repository.FormatRaw,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReplaceRepositoryGrants(context.Background(), repo.ID, []repository.RepositoryGrant{{
		Principal: wantActor,
		Scopes:    []string{"repositories:read"},
	}}, "1"); err != nil {
		t.Fatal(err)
	}
	assertRepositoryRead := func(token string, wantStatus int) {
		response := request(http.MethodGet, "/api/v2/repositories/"+repo.ID, token, "")
		if response.Code != wantStatus {
			t.Fatalf("repository read status=%d body=%s want=%d", response.Code, response.Body.String(), wantStatus)
		}
	}
	assertRepositoryRead(oldCredential.Token, http.StatusOK)
	assertRepositoryRead(newCredential.Token, http.StatusOK)

	revoked := request(
		http.MethodDelete,
		"/api/v2/service-accounts/"+account.ID+"/credentials/"+oldCredential.ID,
		"root",
		"",
	)
	if revoked.Code != http.StatusOK {
		t.Fatalf("revoke old credential status=%d body=%s", revoked.Code, revoked.Body.String())
	}
	assertIdentity(oldCredential.Token, http.StatusUnauthorized)
	assertIdentity(newCredential.Token, http.StatusOK)
	assertRepositoryRead(oldCredential.Token, http.StatusUnauthorized)
	assertRepositoryRead(newCredential.Token, http.StatusOK)
}

func TestServiceAccountListUsesBoundedSignedIDPagination(t *testing.T) {
	store := repository.NewMemoryStore()
	handler := NewGatewayHandler(
		Dependencies{},
		store,
		TestAdapter{},
		Authenticator{AdminToken: "root", AdminActor: "root", APIKeys: store},
	)
	for _, account := range []repository.ServiceAccount{
		{ID: "00000000-0000-0000-0000-000000000002", Name: "release-bot"},
		{ID: "00000000-0000-0000-0000-000000000001", Name: "build-bot"},
	} {
		if _, err := store.CreateServiceAccount(context.Background(), account); err != nil {
			t.Fatal(err)
		}
	}

	request := func(path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Authorization", "Bearer root")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		return response
	}
	type page struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
		NextPageToken string `json:"nextPageToken"`
	}

	firstResponse := request("/api/v2/service-accounts?pageSize=1")
	if firstResponse.Code != http.StatusOK {
		t.Fatalf("first page status=%d body=%s", firstResponse.Code, firstResponse.Body.String())
	}
	var first page
	if err := json.NewDecoder(firstResponse.Body).Decode(&first); err != nil {
		t.Fatal(err)
	}
	if len(first.Items) != 1 || first.Items[0].ID != "00000000-0000-0000-0000-000000000001" || first.NextPageToken == "" {
		t.Fatalf("first page=%#v", first)
	}

	secondResponse := request("/api/v2/service-accounts?pageSize=1&pageToken=" + first.NextPageToken)
	if secondResponse.Code != http.StatusOK {
		t.Fatalf("second page status=%d body=%s", secondResponse.Code, secondResponse.Body.String())
	}
	var second page
	if err := json.NewDecoder(secondResponse.Body).Decode(&second); err != nil {
		t.Fatal(err)
	}
	if len(second.Items) != 1 || second.Items[0].ID != "00000000-0000-0000-0000-000000000002" || second.NextPageToken != "" {
		t.Fatalf("second page=%#v", second)
	}

	expiresAt := time.Now().UTC().Add(time.Hour)
	for _, credential := range []repository.APIKey{
		{ID: "00000000-0000-0000-0000-000000000012", Name: "green", SecretHash: "green", ServiceAccountID: first.Items[0].ID, ExpiresAt: &expiresAt},
		{ID: "00000000-0000-0000-0000-000000000011", Name: "blue", SecretHash: "blue", ServiceAccountID: first.Items[0].ID, ExpiresAt: &expiresAt},
	} {
		if _, err := store.CreateServiceAccountCredential(context.Background(), credential); err != nil {
			t.Fatal(err)
		}
	}
	credentialResponse := request("/api/v2/service-accounts/" + first.Items[0].ID + "/credentials?pageSize=1")
	if credentialResponse.Code != http.StatusOK {
		t.Fatalf("credential first page status=%d body=%s", credentialResponse.Code, credentialResponse.Body.String())
	}
	var credentialFirst page
	if err := json.NewDecoder(credentialResponse.Body).Decode(&credentialFirst); err != nil {
		t.Fatal(err)
	}
	if len(credentialFirst.Items) != 1 || credentialFirst.Items[0].ID != "00000000-0000-0000-0000-000000000011" || credentialFirst.NextPageToken == "" {
		t.Fatalf("credential first page=%#v", credentialFirst)
	}
	credentialSecondResponse := request("/api/v2/service-accounts/" + first.Items[0].ID + "/credentials?pageSize=1&pageToken=" + credentialFirst.NextPageToken)
	if credentialSecondResponse.Code != http.StatusOK {
		t.Fatalf("credential second page status=%d body=%s", credentialSecondResponse.Code, credentialSecondResponse.Body.String())
	}
	var credentialSecond page
	if err := json.NewDecoder(credentialSecondResponse.Body).Decode(&credentialSecond); err != nil {
		t.Fatal(err)
	}
	if len(credentialSecond.Items) != 1 || credentialSecond.Items[0].ID != "00000000-0000-0000-0000-000000000012" || credentialSecond.NextPageToken != "" {
		t.Fatalf("credential second page=%#v", credentialSecond)
	}
	crossAccount := request("/api/v2/service-accounts/" + second.Items[0].ID + "/credentials?pageSize=1&pageToken=" + credentialFirst.NextPageToken)
	if crossAccount.Code != http.StatusBadRequest || !strings.Contains(crossAccount.Body.String(), `"code":"invalid_page_token"`) {
		t.Fatalf("cross-account token status=%d body=%s", crossAccount.Code, crossAccount.Body.String())
	}

	invalid := request("/api/v2/service-accounts?pageSize=1&pageToken=not-a-signed-token")
	if invalid.Code != http.StatusBadRequest || !strings.Contains(invalid.Body.String(), `"code":"invalid_page_token"`) {
		t.Fatalf("invalid token status=%d body=%s", invalid.Code, invalid.Body.String())
	}
	oversized := request("/api/v2/service-accounts?pageSize=201")
	if oversized.Code != http.StatusBadRequest || !strings.Contains(oversized.Body.String(), `"code":"invalid_request"`) {
		t.Fatalf("oversized page status=%d body=%s", oversized.Code, oversized.Body.String())
	}
}

func TestDisablingServiceAccountRejectsEveryExistingCredential(t *testing.T) {
	store := repository.NewMemoryStore()
	handler := NewGatewayHandler(
		Dependencies{},
		store,
		TestAdapter{},
		Authenticator{AdminToken: "root", AdminActor: "root", APIKeys: store},
	)
	request := func(method, path, token, body, version string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		if version != "" {
			req.Header.Set("If-Match", version)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		return response
	}

	created := request(http.MethodPost, "/api/v2/service-accounts", "root", `{"name":"release-bot"}`, "")
	if created.Code != http.StatusCreated {
		t.Fatalf("create service account status=%d body=%s", created.Code, created.Body.String())
	}
	var account struct {
		ID      string `json:"id"`
		Version string `json:"version"`
	}
	if err := json.NewDecoder(created.Body).Decode(&account); err != nil {
		t.Fatal(err)
	}
	credentialResponse := request(http.MethodPost, "/api/v2/service-accounts/"+account.ID+"/credentials", "root", `{"name":"jenkins"}`, "")
	if credentialResponse.Code != http.StatusCreated {
		t.Fatalf("create credential status=%d body=%s", credentialResponse.Code, credentialResponse.Body.String())
	}
	var credential struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(credentialResponse.Body).Decode(&credential); err != nil {
		t.Fatal(err)
	}
	if got := request(http.MethodGet, "/api/v2/identity", credential.Token, "", ""); got.Code != http.StatusOK {
		t.Fatalf("active identity status=%d body=%s", got.Code, got.Body.String())
	}

	disabled := request(
		http.MethodPut,
		"/api/v2/service-accounts/"+account.ID,
		"root",
		`{"state":"disabled"}`,
		account.Version,
	)
	if disabled.Code != http.StatusOK {
		t.Fatalf("disable service account status=%d body=%s", disabled.Code, disabled.Body.String())
	}
	if got := request(http.MethodGet, "/api/v2/identity", credential.Token, "", ""); got.Code != http.StatusUnauthorized {
		t.Fatalf("disabled identity status=%d body=%s", got.Code, got.Body.String())
	}
}

func TestDisabledServiceAccountCredentialDoesNotRecordRejectedUse(t *testing.T) {
	store := repository.NewMemoryStore()
	handler := NewGatewayHandler(
		Dependencies{},
		store,
		TestAdapter{},
		Authenticator{AdminToken: "root", AdminActor: "root", APIKeys: store},
	)
	request := func(method, path, token, body, version string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		if version != "" {
			req.Header.Set("If-Match", version)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		return response
	}

	created := request(http.MethodPost, "/api/v2/service-accounts", "root", `{"name":"disabled-bot"}`, "")
	if created.Code != http.StatusCreated {
		t.Fatalf("create service account status=%d body=%s", created.Code, created.Body.String())
	}
	var account struct {
		ID      string `json:"id"`
		Version string `json:"version"`
	}
	if err := json.NewDecoder(created.Body).Decode(&account); err != nil {
		t.Fatal(err)
	}
	credentialResponse := request(http.MethodPost, "/api/v2/service-accounts/"+account.ID+"/credentials", "root", `{"name":"jenkins"}`, "")
	if credentialResponse.Code != http.StatusCreated {
		t.Fatalf("create credential status=%d body=%s", credentialResponse.Code, credentialResponse.Body.String())
	}
	var credential struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(credentialResponse.Body).Decode(&credential); err != nil {
		t.Fatal(err)
	}
	disabled := request(http.MethodPut, "/api/v2/service-accounts/"+account.ID, "root", `{"state":"disabled"}`, account.Version)
	if disabled.Code != http.StatusOK {
		t.Fatalf("disable service account status=%d body=%s", disabled.Code, disabled.Body.String())
	}
	if got := request(http.MethodGet, "/api/v2/identity", credential.Token, "", ""); got.Code != http.StatusUnauthorized {
		t.Fatalf("disabled identity status=%d body=%s", got.Code, got.Body.String())
	}

	credentials := request(http.MethodGet, "/api/v2/service-accounts/"+account.ID+"/credentials", "root", "", "")
	if credentials.Code != http.StatusOK {
		t.Fatalf("list credentials status=%d body=%s", credentials.Code, credentials.Body.String())
	}
	var listed struct {
		Items []struct {
			LastUsedAt *string `json:"lastUsedAt"`
		} `json:"items"`
	}
	if err := json.NewDecoder(credentials.Body).Decode(&listed); err != nil {
		t.Fatal(err)
	}
	if len(listed.Items) != 1 || listed.Items[0].LastUsedAt != nil {
		t.Fatalf("rejected credential use must not update lastUsedAt: %#v", listed.Items)
	}
}

func TestServiceAccountCredentialsStayOutOfStandaloneAPIKeyManagement(t *testing.T) {
	store := repository.NewMemoryStore()
	handler := NewGatewayHandler(
		Dependencies{},
		store,
		TestAdapter{},
		Authenticator{AdminToken: "root", AdminActor: "root", APIKeys: store},
	)
	request := func(method, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
		req.Header.Set("Authorization", "Bearer root")
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		return response
	}

	created := request(http.MethodPost, "/api/v2/service-accounts", `{"name":"separate-credential-domain"}`)
	if created.Code != http.StatusCreated {
		t.Fatalf("create service account status=%d body=%s", created.Code, created.Body.String())
	}
	var account struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(created.Body).Decode(&account); err != nil {
		t.Fatal(err)
	}
	credentialResponse := request(http.MethodPost, "/api/v2/service-accounts/"+account.ID+"/credentials", `{"name":"jenkins"}`)
	if credentialResponse.Code != http.StatusCreated {
		t.Fatalf("create credential status=%d body=%s", credentialResponse.Code, credentialResponse.Body.String())
	}
	var credential struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(credentialResponse.Body).Decode(&credential); err != nil {
		t.Fatal(err)
	}

	standaloneKeys := request(http.MethodGet, "/api/v2/api-keys", "")
	if standaloneKeys.Code != http.StatusOK {
		t.Fatalf("list standalone keys status=%d body=%s", standaloneKeys.Code, standaloneKeys.Body.String())
	}
	var list struct {
		Items []json.RawMessage `json:"items"`
	}
	if err := json.NewDecoder(standaloneKeys.Body).Decode(&list); err != nil {
		t.Fatal(err)
	}
	if len(list.Items) != 0 {
		t.Fatalf("service account credential leaked into standalone API keys: %s", standaloneKeys.Body.String())
	}
	wrongRevoke := request(http.MethodDelete, "/api/v2/api-keys/"+credential.ID, "")
	if wrongRevoke.Code != http.StatusNotFound {
		t.Fatalf("standalone API key endpoint revoked service account credential: status=%d body=%s", wrongRevoke.Code, wrongRevoke.Body.String())
	}
}

func TestServiceAccountLifecycleWritesManagementAuditWithoutCredentialSecret(t *testing.T) {
	store := repository.NewMemoryStore()
	handler := NewGatewayHandler(
		Dependencies{},
		store,
		TestAdapter{},
		Authenticator{AdminToken: "root", AdminActor: "root", APIKeys: store},
	)
	request := func(method, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
		req.Header.Set("Authorization", "Bearer root")
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		return response
	}

	created := request(http.MethodPost, "/api/v2/service-accounts", `{"name":"audited-ci"}`)
	if created.Code != http.StatusCreated {
		t.Fatalf("create service account status=%d body=%s", created.Code, created.Body.String())
	}
	var account struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(created.Body).Decode(&account); err != nil {
		t.Fatal(err)
	}
	credentialResponse := request(http.MethodPost, "/api/v2/service-accounts/"+account.ID+"/credentials", `{"name":"jenkins"}`)
	if credentialResponse.Code != http.StatusCreated {
		t.Fatalf("create credential status=%d body=%s", credentialResponse.Code, credentialResponse.Body.String())
	}
	var credential struct {
		ID    string `json:"id"`
		Token string `json:"token"`
	}
	if err := json.NewDecoder(credentialResponse.Body).Decode(&credential); err != nil {
		t.Fatal(err)
	}
	if credential.Token == "" {
		t.Fatal("credential response did not include one-time token")
	}
	revoked := request(http.MethodDelete, "/api/v2/service-accounts/"+account.ID+"/credentials/"+credential.ID, "")
	if revoked.Code != http.StatusOK {
		t.Fatalf("revoke credential status=%d body=%s", revoked.Code, revoked.Body.String())
	}

	wantOperations := []string{
		"service_account.create",
		"service_account.credential.create",
		"service_account.credential.revoke",
	}
	if len(store.Audits) != len(wantOperations) {
		t.Fatalf("audit count=%d records=%#v", len(store.Audits), store.Audits)
	}
	for index, wantOperation := range wantOperations {
		audit := store.Audits[index]
		if audit.Actor != "root" || audit.Operation != wantOperation || audit.Format != "management" || audit.Status < 200 || audit.Status >= 300 {
			t.Fatalf("audit[%d]=%#v want operation=%q", index, audit, wantOperation)
		}
		encoded, err := json.Marshal(audit)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(encoded, []byte(credential.Token)) {
			t.Fatalf("credential token leaked into audit: %s", encoded)
		}
	}
}
