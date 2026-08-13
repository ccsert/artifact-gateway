package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	adminopenapi "github.com/artifact-gateway/artifact-gateway/internal/admin/openapi"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

func TestV2AuditAPIExposesOptionalGrantDecisionFields(t *testing.T) {
	store := repository.NewMemoryStore()
	store.Audits = []repository.AuditRecord{{
		GroupName: "releases", Repository: "releases", Actor: "reader", Outcome: repository.AuditAccessDenied,
		OccurredAt: time.Date(2026, time.July, 25, 12, 0, 0, 0, time.UTC), Format: "maven", Operation: "get", Status: http.StatusForbidden,
		AuthorizationSource: "repository_grants", AuthorizationReason: "scope_not_granted",
		Evidence: map[string]string{"policyVersion": "v2"},
	}}
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())

	request := httptest.NewRequest(http.MethodGet, "/api/v2/audits?group=releases&repository=releases&limit=1", nil)
	authorize(request, "admin-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var audits []struct {
		AuthorizationSource string            `json:"authorizationSource"`
		AuthorizationReason string            `json:"authorizationReason"`
		Evidence            map[string]string `json:"evidence"`
		OccurredAt          time.Time         `json:"occurredAt"`
	}
	if err := json.NewDecoder(response.Body).Decode(&audits); err != nil {
		t.Fatal(err)
	}
	if len(audits) != 1 || audits[0].AuthorizationSource != "repository_grants" || audits[0].AuthorizationReason != "scope_not_granted" ||
		audits[0].Evidence["policyVersion"] != "v2" || audits[0].OccurredAt.IsZero() {
		t.Fatalf("audits=%#v", audits)
	}

	nonAdmin := httptest.NewRequest(http.MethodGet, "/api/v2/audits", nil)
	authorize(nonAdmin, "resolver-secret")
	denied := httptest.NewRecorder()
	handler.ServeHTTP(denied, nonAdmin)
	if denied.Code != http.StatusUnauthorized {
		t.Fatalf("non-admin status=%d body=%s", denied.Code, denied.Body.String())
	}
}

func TestHostedGroupManagementLifecycle(t *testing.T) {
	store := repository.NewMemoryStore()
	first, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "group-first", Format: repository.FormatMaven})
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "group-second", Format: repository.FormatMaven})
	if err != nil {
		t.Fatal(err)
	}
	other, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "group-other", Format: repository.FormatRaw})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())
	create := httptest.NewRequest(http.MethodPost, "/api/v2/groups", strings.NewReader(`{"name":"maven-group","format":"maven","members":[{"repositoryId":"`+second.ID+`","position":1},{"repositoryId":"`+first.ID+`","position":0}]}`))
	authorize(create, "admin-secret")
	create.Header.Set("Idempotency-Key", "group-create")
	created := httptest.NewRecorder()
	handler.ServeHTTP(created, create)
	if created.Code != http.StatusCreated {
		t.Fatalf("create=%d body=%s", created.Code, created.Body.String())
	}
	var group repository.HostedGroup
	if err := json.NewDecoder(created.Body).Decode(&group); err != nil || group.Version != "1" || len(group.Members) != 2 || group.Members[0].RepositoryID != first.ID {
		t.Fatalf("group=%#v err=%v", group, err)
	}
	get := httptest.NewRequest(http.MethodGet, "/api/v2/groups/"+group.ID, nil)
	authorize(get, "admin-secret")
	got := httptest.NewRecorder()
	handler.ServeHTTP(got, get)
	if got.Code != http.StatusOK {
		t.Fatalf("get=%d body=%s", got.Code, got.Body.String())
	}
	replace := httptest.NewRequest(http.MethodPut, "/api/v2/groups/"+group.ID+"/members", strings.NewReader(`[{"repositoryId":"`+second.ID+`","position":0}]`))
	authorize(replace, "admin-secret")
	replace.Header.Set("If-Match", "1")
	replaced := httptest.NewRecorder()
	handler.ServeHTTP(replaced, replace)
	if replaced.Code != http.StatusOK || !strings.Contains(replaced.Body.String(), `"version":"2"`) {
		t.Fatalf("replace=%d body=%s", replaced.Code, replaced.Body.String())
	}
	stale := httptest.NewRequest(http.MethodPut, "/api/v2/groups/"+group.ID+"/members", strings.NewReader(`[{"repositoryId":"`+first.ID+`","position":0}]`))
	authorize(stale, "admin-secret")
	stale.Header.Set("If-Match", "1")
	staleResult := httptest.NewRecorder()
	handler.ServeHTTP(staleResult, stale)
	if staleResult.Code != http.StatusPreconditionFailed {
		t.Fatalf("stale=%d body=%s", staleResult.Code, staleResult.Body.String())
	}
	mismatch := httptest.NewRequest(http.MethodPost, "/api/v2/groups", strings.NewReader(`{"name":"invalid-group","format":"maven","members":[{"repositoryId":"`+other.ID+`","position":0}]}`))
	authorize(mismatch, "admin-secret")
	mismatch.Header.Set("Idempotency-Key", "invalid-group")
	mismatchResult := httptest.NewRecorder()
	handler.ServeHTTP(mismatchResult, mismatch)
	if mismatchResult.Code != http.StatusBadRequest {
		t.Fatalf("mismatch=%d body=%s", mismatchResult.Code, mismatchResult.Body.String())
	}
	deleteRequest := httptest.NewRequest(http.MethodDelete, "/api/v2/groups/"+group.ID, nil)
	authorize(deleteRequest, "admin-secret")
	deleted := httptest.NewRecorder()
	handler.ServeHTTP(deleted, deleteRequest)
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("delete=%d body=%s", deleted.Code, deleted.Body.String())
	}
}

func TestRepositoryGrantManagementUsesETagVersioning(t *testing.T) {
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "grant-target", Format: repository.FormatRaw})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())
	list := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/"+repo.ID+"/grants", nil)
	authorize(list, "admin-secret")
	listed := httptest.NewRecorder()
	handler.ServeHTTP(listed, list)
	if listed.Code != http.StatusOK || listed.Header().Get("ETag") != "1" || listed.Body.String() != "[]\n" {
		t.Fatalf("list=%d etag=%q body=%s", listed.Code, listed.Header().Get("ETag"), listed.Body.String())
	}
	replace := httptest.NewRequest(http.MethodPut, "/api/v2/repositories/"+repo.ID+"/grants", strings.NewReader(`[{"principal":"build-agent","scopes":["repositories:read"],"resourcePrefix":"releases/"},{"principal":"build-agent","scopes":["repositories:write"],"resourcePrefix":"snapshots/"}]`))
	authorize(replace, "admin-secret")
	replace.Header.Set("If-Match", "1")
	replaced := httptest.NewRecorder()
	handler.ServeHTTP(replaced, replace)
	if replaced.Code != http.StatusOK || replaced.Header().Get("ETag") != "2" || !strings.Contains(replaced.Body.String(), `"resourcePrefix":"releases/"`) {
		t.Fatalf("replace=%d etag=%q body=%s", replaced.Code, replaced.Header().Get("ETag"), replaced.Body.String())
	}
	stale := httptest.NewRequest(http.MethodPut, "/api/v2/repositories/"+repo.ID+"/grants", strings.NewReader(`[]`))
	authorize(stale, "admin-secret")
	stale.Header.Set("If-Match", "1")
	staleResult := httptest.NewRecorder()
	handler.ServeHTTP(staleResult, stale)
	if staleResult.Code != http.StatusPreconditionFailed {
		t.Fatalf("stale=%d body=%s", staleResult.Code, staleResult.Body.String())
	}
	invalid := httptest.NewRequest(http.MethodPut, "/api/v2/repositories/"+repo.ID+"/grants", strings.NewReader(`[{"principal":"build-agent","scopes":["unknown"]}]`))
	authorize(invalid, "admin-secret")
	invalid.Header.Set("If-Match", "2")
	invalidResult := httptest.NewRecorder()
	handler.ServeHTTP(invalidResult, invalid)
	if invalidResult.Code != http.StatusBadRequest {
		t.Fatalf("invalid=%d body=%s", invalidResult.Code, invalidResult.Body.String())
	}
	duplicate := httptest.NewRequest(http.MethodPut, "/api/v2/repositories/"+repo.ID+"/grants", strings.NewReader(`[{"principal":"build-agent","scopes":["repositories:read"],"resourcePrefix":"releases/"},{"principal":"build-agent","scopes":["repositories:write"],"resourcePrefix":"releases/"}]`))
	authorize(duplicate, "admin-secret")
	duplicate.Header.Set("If-Match", "2")
	duplicateResult := httptest.NewRecorder()
	handler.ServeHTTP(duplicateResult, duplicate)
	if duplicateResult.Code != http.StatusBadRequest {
		t.Fatalf("duplicate=%d body=%s", duplicateResult.Code, duplicateResult.Body.String())
	}
	badPrefix := httptest.NewRequest(http.MethodPut, "/api/v2/repositories/"+repo.ID+"/grants", strings.NewReader(`[{"principal":"build-agent","scopes":["repositories:read"],"resourcePrefix":"/absolute"}]`))
	authorize(badPrefix, "admin-secret")
	badPrefix.Header.Set("If-Match", "2")
	badPrefixResult := httptest.NewRecorder()
	handler.ServeHTTP(badPrefixResult, badPrefix)
	if badPrefixResult.Code != http.StatusBadRequest {
		t.Fatalf("bad prefix=%d body=%s", badPrefixResult.Code, badPrefixResult.Body.String())
	}
}

func TestRepositoryConsoleAggregatesRequireAdminAndReturnCrossRepositoryData(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	first, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "aggregate-first", Format: repository.FormatRaw})
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "aggregate-second", Format: repository.FormatMaven})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.ReplaceRepositoryGrants(ctx, first.ID, []repository.RepositoryGrant{{Principal: "build-agent", Scopes: []string{"repositories:read"}, ResourcePrefix: "releases/"}}, "1"); err != nil {
		t.Fatal(err)
	}
	if _, err = store.PutRawAsset(ctx, repository.RawAsset{RepositoryID: first.ID, Path: "release.txt", Digest: "sha256:aggregate", Size: 17}); err != nil {
		t.Fatal(err)
	}
	job := repository.LifecycleJob{ID: uuid.NewString(), RepositoryID: second.ID, Kind: repository.LifecycleJobRetention, IdempotencyKey: "aggregate-retention"}
	if _, _, err = store.EnqueueLifecycleJob(ctx, job); err != nil {
		t.Fatal(err)
	}

	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())
	request := func(path, token string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		if token != "" {
			authorize(req, token)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		return response
	}
	if response := request("/api/v2/repository-grants", ""); response.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous grants=%d body=%s", response.Code, response.Body.String())
	}

	grantsResponse := request("/api/v2/repository-grants", "admin-secret")
	var grants adminopenapi.RepositoryGrantRecordList
	if grantsResponse.Code != http.StatusOK || json.Unmarshal(grantsResponse.Body.Bytes(), &grants) != nil || len(grants) != 1 || grants[0].RepositoryId.String() != first.ID || grants[0].Principal != "build-agent" {
		t.Fatalf("grants=%d body=%s decoded=%#v", grantsResponse.Code, grantsResponse.Body.String(), grants)
	}

	capacitiesResponse := request("/api/v2/repository-capacities", "admin-secret")
	var capacities adminopenapi.RepositoryCapacityList
	if capacitiesResponse.Code != http.StatusOK || json.Unmarshal(capacitiesResponse.Body.Bytes(), &capacities) != nil || len(capacities) != 2 {
		t.Fatalf("capacities=%d body=%s decoded=%#v", capacitiesResponse.Code, capacitiesResponse.Body.String(), capacities)
	}
	capacityByID := make(map[string]adminopenapi.RepositoryCapacity, len(capacities))
	for _, capacity := range capacities {
		capacityByID[capacity.RepositoryId.String()] = capacity
	}
	if capacityByID[first.ID].UsedBytes != 17 || capacityByID[first.ID].ObjectCount != 1 {
		t.Fatalf("raw capacity=%#v", capacityByID[first.ID])
	}

	jobsResponse := request("/api/v2/lifecycle-jobs?limit=10", "admin-secret")
	var jobs adminopenapi.RepositoryLifecycleJobList
	if jobsResponse.Code != http.StatusOK || json.Unmarshal(jobsResponse.Body.Bytes(), &jobs) != nil || len(jobs) != 1 || jobs[0].RepositoryId.String() != second.ID || jobs[0].RepositoryName != second.Name || jobs[0].Job.Id != job.ID {
		t.Fatalf("jobs=%d body=%s decoded=%#v", jobsResponse.Code, jobsResponse.Body.String(), jobs)
	}
}

func TestRepositoryManagementUsesScopedGrants(t *testing.T) {
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "scoped-target", Format: repository.FormatRaw})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReplaceRepositoryGrants(context.Background(), repo.ID, []repository.RepositoryGrant{
		{Principal: "reader", Scopes: []string{"repositories:read"}},
		{Principal: "writer", Scopes: []string{"repositories:write"}},
		{Principal: "manager", Scopes: []string{"repositories:admin"}},
	}, "1"); err != nil {
		t.Fatal(err)
	}
	authenticator := testAuthenticator()
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, authenticator)
	request := func(method, path, actor, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		authorize(r, authenticator.IssueToken(actor))
		if method == http.MethodPut {
			r.Header.Set("If-Match", "2")
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	if response := request(http.MethodGet, "/api/v2/repositories/"+repo.ID, "reader", ""); response.Code != http.StatusOK {
		t.Fatalf("reader get=%d body=%s", response.Code, response.Body.String())
	}
	if response := request(http.MethodGet, "/api/v2/repositories/"+repo.ID+"/retention-policy", "reader", ""); response.Code != http.StatusOK {
		t.Fatalf("reader policy=%d body=%s", response.Code, response.Body.String())
	}
	if response := request(http.MethodPut, "/api/v2/repositories/"+repo.ID+"/grants", "reader", `[]`); response.Code != http.StatusForbidden {
		t.Fatalf("reader grants=%d body=%s", response.Code, response.Body.String())
	}
	if response := request(http.MethodGet, "/api/v2/repositories/"+repo.ID+"/grants", "manager", ""); response.Code != http.StatusOK {
		t.Fatalf("manager grants=%d body=%s", response.Code, response.Body.String())
	}
	if response := request(http.MethodDelete, "/api/v2/repositories/"+repo.ID, "reader", ""); response.Code != http.StatusForbidden {
		t.Fatalf("reader delete=%d body=%s", response.Code, response.Body.String())
	}
	if len(store.Audits) == 0 {
		t.Fatal("expected authorization audit")
	}
	audit := store.Audits[len(store.Audits)-1]
	if audit.Outcome != repository.AuditAccessDenied || audit.AuthorizationSource != "repository_grants" || audit.AuthorizationReason != "scope_not_granted" || audit.Format != "management" {
		t.Fatalf("audit=%#v", audit)
	}
	metrics := httptest.NewRecorder()
	handler.ServeHTTP(metrics, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if !strings.Contains(metrics.Body.String(), `artifact_gateway_repository_authorization_denials_total{format="management",authorization_source="repository_grants",authorization_reason="scope_not_granted"} 2`) {
		t.Fatalf("management authorization metric=%s", metrics.Body.String())
	}
	if response := request(http.MethodDelete, "/api/v2/repositories/"+repo.ID, "writer", ""); response.Code != http.StatusAccepted {
		t.Fatalf("writer delete=%d body=%s", response.Code, response.Body.String())
	}
}

func TestRepositoryEffectiveAccessReportsPermissionsAndAnonymousPolicy(t *testing.T) {
	store := repository.NewMemoryStore()
	enableAnonymousAccess(t, store)
	repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "effective-raw", Format: repository.FormatRaw, AnonymousRead: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReplaceRepositoryGrants(context.Background(), repo.ID, []repository.RepositoryGrant{{Principal: "reader", Scopes: []string{"repositories:read"}}}, "1"); err != nil {
		t.Fatal(err)
	}
	authenticator := testAuthenticator()
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, authenticator)

	request := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/"+repo.ID+"/effective-access", nil)
	authorize(request, authenticator.IssueToken("reader"))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("effective access = %d %s", response.Code, response.Body.String())
	}
	var body struct {
		Actor    string `json:"actor"`
		Identity struct {
			Kind string `json:"kind"`
		} `json:"identity"`
		AnonymousRead struct {
			Allowed bool   `json:"allowed"`
			Reason  string `json:"reason"`
		} `json:"anonymousRead"`
		Permissions struct {
			Read  struct{ Allowed bool } `json:"read"`
			Write struct{ Allowed bool } `json:"write"`
			Admin struct{ Allowed bool } `json:"admin"`
		} `json:"permissions"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Actor != "reader" || body.Identity.Kind != "static_resolver" || !body.AnonymousRead.Allowed || body.AnonymousRead.Reason != "repository_anonymous_read_enabled" || !body.Permissions.Read.Allowed || body.Permissions.Write.Allowed || body.Permissions.Admin.Allowed {
		t.Fatalf("effective access body=%#v", body)
	}

	denied := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/"+repo.ID+"/effective-access", nil)
	authorize(denied, authenticator.IssueToken("stranger"))
	deniedResponse := httptest.NewRecorder()
	handler.ServeHTTP(deniedResponse, denied)
	if deniedResponse.Code != http.StatusOK || !strings.Contains(deniedResponse.Body.String(), `"actor":"stranger"`) || !strings.Contains(deniedResponse.Body.String(), `"read":{"allowed":false`) {
		t.Fatalf("denied effective access = %d %s", deniedResponse.Code, deniedResponse.Body.String())
	}

	if _, err := store.DisableHostedRepository(context.Background(), repo.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.FinalizeHostedRepositoryDeletion(context.Background(), repo.ID); err != nil {
		t.Fatal(err)
	}
	deleted := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/"+repo.ID+"/effective-access", nil)
	authorize(deleted, authenticator.IssueToken("reader"))
	deletedResponse := httptest.NewRecorder()
	handler.ServeHTTP(deletedResponse, deleted)
	if deletedResponse.Code != http.StatusOK || !strings.Contains(deletedResponse.Body.String(), `"anonymousRead":{"allowed":false,"reason":"repository_not_active"`) {
		t.Fatalf("deleted effective access = %d %s", deletedResponse.Code, deletedResponse.Body.String())
	}
}

func TestRepositoryEffectiveAccessSupportsAdministratorSimulation(t *testing.T) {
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "simulation-raw", Format: repository.FormatRaw})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReplaceRepositoryGrants(context.Background(), repo.ID, []repository.RepositoryGrant{{
		Principal: "build-agent", Scopes: []string{"repositories:read"}, ResourcePrefix: "releases/",
	}}, "1"); err != nil {
		t.Fatal(err)
	}
	authenticator := testAuthenticator()
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, authenticator)

	request := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/"+repo.ID+"/effective-access?actor=build-agent&resource=releases%2Fapp.bin", nil)
	authorize(request, "admin-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"simulated":true`) || !strings.Contains(response.Body.String(), `"resource":"releases/app.bin"`) || !strings.Contains(response.Body.String(), `"read":{"allowed":true,"reason":"scope_granted","source":"repository_grants"}`) {
		t.Fatalf("simulated grant = %d %s", response.Code, response.Body.String())
	}

	wrongResource := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/"+repo.ID+"/effective-access?actor=build-agent&resource=snapshots%2Fapp.bin", nil)
	authorize(wrongResource, "admin-secret")
	wrongResourceResponse := httptest.NewRecorder()
	handler.ServeHTTP(wrongResourceResponse, wrongResource)
	if wrongResourceResponse.Code != http.StatusOK || !strings.Contains(wrongResourceResponse.Body.String(), `"read":{"allowed":false`) {
		t.Fatalf("wrong resource = %d %s", wrongResourceResponse.Code, wrongResourceResponse.Body.String())
	}

	globalRole := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/"+repo.ID+"/effective-access?actor=release-bot&role=writer", nil)
	authorize(globalRole, "admin-secret")
	globalRoleResponse := httptest.NewRecorder()
	handler.ServeHTTP(globalRoleResponse, globalRole)
	if globalRoleResponse.Code != http.StatusOK || !strings.Contains(globalRoleResponse.Body.String(), `"read":{"allowed":true,"reason":"role_writer","source":"role"}`) || !strings.Contains(globalRoleResponse.Body.String(), `"write":{"allowed":true,"reason":"role_writer","source":"role"}`) || !strings.Contains(globalRoleResponse.Body.String(), `"admin":{"allowed":false`) {
		t.Fatalf("simulated role = %d %s", globalRoleResponse.Code, globalRoleResponse.Body.String())
	}

	forbidden := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/"+repo.ID+"/effective-access?actor=build-agent", nil)
	authorize(forbidden, authenticator.IssueToken("reader"))
	forbiddenResponse := httptest.NewRecorder()
	handler.ServeHTTP(forbiddenResponse, forbidden)
	if forbiddenResponse.Code != http.StatusForbidden {
		t.Fatalf("non-admin simulation = %d %s", forbiddenResponse.Code, forbiddenResponse.Body.String())
	}

	invalid := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/"+repo.ID+"/effective-access?role=reader", nil)
	authorize(invalid, "admin-secret")
	invalidResponse := httptest.NewRecorder()
	handler.ServeHTTP(invalidResponse, invalid)
	if invalidResponse.Code != http.StatusBadRequest {
		t.Fatalf("role without actor = %d %s", invalidResponse.Code, invalidResponse.Body.String())
	}
}

func TestCurrentIdentityReportsSafeCredentialMetadata(t *testing.T) {
	store := repository.NewMemoryStore()
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())

	request := httptest.NewRequest(http.MethodGet, "/api/v2/identity", nil)
	authorize(request, "admin-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"actor":"alice"`) || !strings.Contains(response.Body.String(), `"kind":"static_admin"`) || !strings.Contains(response.Body.String(), `"role":"admin"`) || !strings.Contains(response.Body.String(), `"administrator":true`) {
		t.Fatalf("identity = %d %s", response.Code, response.Body.String())
	}

	unauthenticated := httptest.NewRecorder()
	handler.ServeHTTP(unauthenticated, httptest.NewRequest(http.MethodGet, "/api/v2/identity", nil))
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated identity = %d %s", unauthenticated.Code, unauthenticated.Body.String())
	}
}

func TestAnonymousRepositoryBrowseAllowsReadOnlyQueries(t *testing.T) {
	store := repository.NewMemoryStore()
	enableAnonymousAccess(t, store)
	repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "public-oci", Format: repository.FormatOCI, AnonymousRead: true})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewGatewayHandler(Dependencies{NativeOCIObjectStore: NewMemoryOCIObjectStore()}, store, TestAdapter{}, testAuthenticator())
	manifest := []byte(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json"}`)
	put := httptest.NewRequest(http.MethodPut, "/v2/public-oci/app/manifests/latest", strings.NewReader(string(manifest)))
	put.Header.Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
	authorize(put, "resolver-secret")
	published := httptest.NewRecorder()
	handler.ServeHTTP(published, put)
	if published.Code != http.StatusCreated {
		t.Fatalf("publish = %d %s", published.Code, published.Body.String())
	}

	browse := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/"+repo.ID+"/oci/images", nil)
	browseResponse := httptest.NewRecorder()
	handler.ServeHTTP(browseResponse, browse)
	if browseResponse.Code != http.StatusOK || !strings.Contains(browseResponse.Body.String(), `"app"`) {
		t.Fatalf("anonymous browse = %d %s", browseResponse.Code, browseResponse.Body.String())
	}

	private, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "private-oci", Format: repository.FormatOCI})
	if err != nil {
		t.Fatal(err)
	}
	privateBrowse := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/"+private.ID+"/oci/images", nil)
	privateResponse := httptest.NewRecorder()
	handler.ServeHTTP(privateResponse, privateBrowse)
	if privateResponse.Code != http.StatusUnauthorized {
		t.Fatalf("private browse = %d %s", privateResponse.Code, privateResponse.Body.String())
	}
}

func TestGroupManagementAnonymousReadPolicyDefaultsAndUpdates(t *testing.T) {
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "group-policy-repo", Format: repository.FormatRaw})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())

	create := httptest.NewRequest(http.MethodPost, "/api/v2/groups", strings.NewReader(`{"name":"group-policy","format":"raw","members":[{"repositoryId":"`+repo.ID+`","position":0}]}`))
	authorize(create, "admin-secret")
	create.Header.Set("Idempotency-Key", "group-policy")
	created := httptest.NewRecorder()
	handler.ServeHTTP(created, create)
	if created.Code != http.StatusCreated {
		t.Fatalf("create=%d body=%s", created.Code, created.Body.String())
	}
	var group repository.HostedGroup
	if err := json.NewDecoder(created.Body).Decode(&group); err != nil || group.AnonymousRead {
		t.Fatalf("group=%#v err=%v", group, err)
	}

	replace := httptest.NewRequest(http.MethodPut, "/api/v2/groups/"+group.ID, strings.NewReader(`{"name":"group-policy","format":"raw","anonymousRead":true,"members":[{"repositoryId":"`+repo.ID+`","position":0}]}`))
	authorize(replace, "admin-secret")
	replace.Header.Set("If-Match", "1")
	replaced := httptest.NewRecorder()
	handler.ServeHTTP(replaced, replace)
	if replaced.Code != http.StatusOK || !strings.Contains(replaced.Body.String(), `"anonymousRead":true`) {
		t.Fatalf("replace=%d body=%s", replaced.Code, replaced.Body.String())
	}

	membersOnly := httptest.NewRequest(http.MethodPut, "/api/v2/groups/"+group.ID+"/members", strings.NewReader(`[{"repositoryId":"`+repo.ID+`","position":0}]`))
	authorize(membersOnly, "admin-secret")
	membersOnly.Header.Set("If-Match", "2")
	membersOnlyResponse := httptest.NewRecorder()
	handler.ServeHTTP(membersOnlyResponse, membersOnly)
	if membersOnlyResponse.Code != http.StatusOK || !strings.Contains(membersOnlyResponse.Body.String(), `"anonymousRead":true`) {
		t.Fatalf("members replace=%d body=%s", membersOnlyResponse.Code, membersOnlyResponse.Body.String())
	}
}

func TestAPIKeyRolesEnforceScopedManagementAccess(t *testing.T) {
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{
		ID:           uuid.NewString(),
		Name:         "ci-proxy",
		Format:       repository.FormatRaw,
		Type:         repository.RepositoryTypeProxy,
		Endpoint:     "https://upstream.example",
		AllowedHosts: []string{"upstream.example"},
	})
	if err != nil {
		t.Fatal(err)
	}
	authenticator := testAuthenticator()
	authenticator.APIKeys = store
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, authenticator)

	createKey := func(t *testing.T, roles string) string {
		t.Helper()
		body := `{"name":"` + roles + `","roles":["` + roles + `"]}"`
		req := httptest.NewRequest(http.MethodPost, "/api/v2/api-keys", strings.NewReader(body))
		authorize(req, "admin-secret")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusCreated {
			t.Fatalf("create %s key=%d body=%s", roles, rec.Code, rec.Body.String())
		}
		var created struct {
			Token string `json:"token"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil || created.Token == "" {
			t.Fatalf("parse %s key token: %s", roles, rec.Body.String())
		}
		return created.Token
	}

	readerToken := createKey(t, "reader")
	writerToken := createKey(t, "writer")

	patch := func(token string) int {
		req := httptest.NewRequest(http.MethodPatch, "/api/v2/repositories/"+repo.ID, strings.NewReader(`{"endpoint":"https://cdn.example","allowedHosts":["cdn.example"]}`))
		authorize(req, token)
		req.Header.Set("If-Match", "1")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec.Code
	}
	get := func(token string) int {
		req := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/"+repo.ID, nil)
		authorize(req, token)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec.Code
	}

	// Reader: read allowed by role, write denied.
	if code := get(readerToken); code != http.StatusOK {
		t.Fatalf("reader get=%d", code)
	}
	if code := patch(readerToken); code != http.StatusForbidden {
		t.Fatalf("reader patch=%d want 403", code)
	}

	// Writer: read and write allowed by role.
	if code := get(writerToken); code != http.StatusOK {
		t.Fatalf("writer get=%d", code)
	}
	if code := patch(writerToken); code != http.StatusOK {
		t.Fatalf("writer patch=%d want 200", code)
	}

	// Neither reader nor writer may mint new keys (administrator-only).
	for _, token := range []string{readerToken, writerToken} {
		req := httptest.NewRequest(http.MethodPost, "/api/v2/api-keys", strings.NewReader(`{"name":"x","roles":["admin"]}`))
		authorize(req, token)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s role minted key=%d want 401", token, rec.Code)
		}
	}
}
