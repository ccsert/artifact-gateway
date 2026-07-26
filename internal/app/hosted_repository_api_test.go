package app

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

func TestHostedRepositoryManagementLifecycle(t *testing.T) {
	store := repository.NewMemoryStore()
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())
	request := httptest.NewRequest(http.MethodPost, "/api/v2/repositories", strings.NewReader(`{"name":"releases","format":"maven"}`))
	authorize(request, "admin-secret")
	request.Header.Set("Idempotency-Key", "create-releases")
	created := httptest.NewRecorder()
	handler.ServeHTTP(created, request)
	if created.Code != http.StatusCreated {
		t.Fatalf("create=%d body=%s", created.Code, created.Body.String())
	}
	if !strings.Contains(created.Body.String(), `"state":"active"`) {
		t.Fatalf("created=%s", created.Body.String())
	}
	id := strings.Split(strings.Split(created.Body.String(), `"id":"`)[1], `"`)[0]
	list := httptest.NewRequest(http.MethodGet, "/api/v2/repositories", nil)
	authorize(list, "admin-secret")
	listed := httptest.NewRecorder()
	handler.ServeHTTP(listed, list)
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), "releases") {
		t.Fatalf("list=%d body=%s", listed.Code, listed.Body.String())
	}
	disable := httptest.NewRequest(http.MethodDelete, "/api/v2/repositories/"+id, nil)
	authorize(disable, "admin-secret")
	disabled := httptest.NewRecorder()
	handler.ServeHTTP(disabled, disable)
	if disabled.Code != http.StatusAccepted || !strings.Contains(disabled.Body.String(), `"state":"pending"`) {
		t.Fatalf("disable=%d body=%s", disabled.Code, disabled.Body.String())
	}
}

func TestRepositoryCapabilitiesReportImplementedFormatOperations(t *testing.T) {
	store := repository.NewMemoryStore()
	conan, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "capabilities-conan", Format: repository.FormatConan})
	if err != nil {
		t.Fatal(err)
	}
	maven, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "capabilities-maven", Format: repository.FormatMaven})
	if err != nil {
		t.Fatal(err)
	}
	authenticator := testAuthenticator()
	if _, err = store.ReplaceRepositoryGrants(context.Background(), conan.ID, []repository.RepositoryGrant{{Principal: "reader", Scopes: []string{"repositories:read"}}}, "1"); err != nil {
		t.Fatal(err)
	}
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, authenticator)
	request := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/"+conan.ID+"/capabilities", nil)
	authorize(request, authenticator.IssueToken("reader"))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"format":"conan"`) || !strings.Contains(response.Body.String(), `"restore"`) || strings.Contains(response.Body.String(), `"retain"`) {
		t.Fatalf("Conan capabilities=%d %s", response.Code, response.Body.String())
	}
	adminRequest := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/"+maven.ID+"/capabilities", nil)
	authorize(adminRequest, "admin-secret")
	adminResponse := httptest.NewRecorder()
	handler.ServeHTTP(adminResponse, adminRequest)
	if adminResponse.Code != http.StatusOK || !strings.Contains(adminResponse.Body.String(), `"retain"`) || strings.Contains(adminResponse.Body.String(), `"restore"`) {
		t.Fatalf("Maven capabilities=%d %s", adminResponse.Code, adminResponse.Body.String())
	}
}

func TestV2AuditAPIExposesOptionalGrantDecisionFields(t *testing.T) {
	store := repository.NewMemoryStore()
	store.Audits = []repository.AuditRecord{{
		GroupName: "releases", Repository: "releases", Actor: "reader", Outcome: repository.AuditAccessDenied,
		OccurredAt: time.Date(2026, time.July, 25, 12, 0, 0, 0, time.UTC), Format: "maven", Operation: "get", Status: http.StatusForbidden,
		AuthorizationSource: "repository_grants", AuthorizationReason: "scope_not_granted",
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
		AuthorizationSource string    `json:"authorizationSource"`
		AuthorizationReason string    `json:"authorizationReason"`
		OccurredAt          time.Time `json:"occurredAt"`
	}
	if err := json.NewDecoder(response.Body).Decode(&audits); err != nil {
		t.Fatal(err)
	}
	if len(audits) != 1 || audits[0].AuthorizationSource != "repository_grants" || audits[0].AuthorizationReason != "scope_not_granted" || audits[0].OccurredAt.IsZero() {
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
	replace := httptest.NewRequest(http.MethodPut, "/api/v2/repositories/"+repo.ID+"/grants", strings.NewReader(`[{"principal":"build-agent","scopes":["repositories:read","repositories:write"]}]`))
	authorize(replace, "admin-secret")
	replace.Header.Set("If-Match", "1")
	replaced := httptest.NewRecorder()
	handler.ServeHTTP(replaced, replace)
	if replaced.Code != http.StatusOK || replaced.Header().Get("ETag") != "2" || !strings.Contains(replaced.Body.String(), "build-agent") {
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

func TestRepositoryRetentionPolicyManagementUsesVersioning(t *testing.T) {
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "retention-target", Format: repository.FormatMaven})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())
	get := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/"+repo.ID+"/retention-policy", nil)
	authorize(get, "admin-secret")
	defaultPolicy := httptest.NewRecorder()
	handler.ServeHTTP(defaultPolicy, get)
	if defaultPolicy.Code != http.StatusOK || !strings.Contains(defaultPolicy.Body.String(), `"version":"1"`) || !strings.Contains(defaultPolicy.Body.String(), `"keepDays":30`) {
		t.Fatalf("default policy=%d body=%s", defaultPolicy.Code, defaultPolicy.Body.String())
	}
	replace := httptest.NewRequest(http.MethodPut, "/api/v2/repositories/"+repo.ID+"/retention-policy", strings.NewReader(`{"version":"1","keepDays":14,"minimumVersions":3}`))
	authorize(replace, "admin-secret")
	replace.Header.Set("If-Match", "1")
	replaced := httptest.NewRecorder()
	handler.ServeHTTP(replaced, replace)
	if replaced.Code != http.StatusOK || !strings.Contains(replaced.Body.String(), `"version":"2"`) || !strings.Contains(replaced.Body.String(), `"minimumVersions":3`) {
		t.Fatalf("replace=%d body=%s", replaced.Code, replaced.Body.String())
	}
	stale := httptest.NewRequest(http.MethodPut, "/api/v2/repositories/"+repo.ID+"/retention-policy", strings.NewReader(`{"version":"1","keepDays":7,"minimumVersions":1}`))
	authorize(stale, "admin-secret")
	stale.Header.Set("If-Match", "1")
	staleResult := httptest.NewRecorder()
	handler.ServeHTTP(staleResult, stale)
	if staleResult.Code != http.StatusPreconditionFailed {
		t.Fatalf("stale=%d body=%s", staleResult.Code, staleResult.Body.String())
	}
	invalid := httptest.NewRequest(http.MethodPut, "/api/v2/repositories/"+repo.ID+"/retention-policy", strings.NewReader(`{"version":"2","keepDays":0,"minimumVersions":1}`))
	authorize(invalid, "admin-secret")
	invalid.Header.Set("If-Match", "2")
	invalidResult := httptest.NewRecorder()
	handler.ServeHTTP(invalidResult, invalid)
	if invalidResult.Code != http.StatusBadRequest {
		t.Fatalf("invalid=%d body=%s", invalidResult.Code, invalidResult.Body.String())
	}
}

func TestMavenArtifactDetailAndTombstoneManagement(t *testing.T) {
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "artifact-target", Format: repository.FormatMaven})
	if err != nil {
		t.Fatal(err)
	}
	const key = "native/maven/sha256/artifact-target"
	session := repository.MavenPublishSession{ID: uuid.NewString(), RepositoryID: repo.ID, Coordinate: "org.example:widget:1.0.0", State: "open", ExpiresAt: time.Now().Add(time.Hour), Objects: []repository.MavenDeclaredObject{{Name: "widget-1.0.0.jar", Digest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Size: 3}}}
	if _, err = store.CreateMavenPublishSession(context.Background(), session); err != nil {
		t.Fatal(err)
	}
	if err = store.MarkMavenPublishObject(context.Background(), session.ID, session.Objects[0].Name, key); err != nil {
		t.Fatal(err)
	}
	artifact, err := store.CommitMavenPublishSession(context.Background(), session.ID, []repository.MavenAsset{{RepositoryID: repo.ID, Path: "org/example/widget/1.0.0/widget-1.0.0.jar", ObjectKey: key, Digest: session.Objects[0].Digest, Size: 3}})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())
	detail := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/"+repo.ID+"/artifacts/"+artifact.ID, nil)
	authorize(detail, "admin-secret")
	detailed := httptest.NewRecorder()
	handler.ServeHTTP(detailed, detail)
	if detailed.Code != http.StatusOK || !strings.Contains(detailed.Body.String(), `"state":"visible"`) {
		t.Fatalf("detail=%d body=%s", detailed.Code, detailed.Body.String())
	}
	deleteRequest := httptest.NewRequest(http.MethodDelete, "/api/v2/repositories/"+repo.ID+"/artifacts/"+artifact.ID, nil)
	authorize(deleteRequest, "admin-secret")
	deleted := httptest.NewRecorder()
	handler.ServeHTTP(deleted, deleteRequest)
	if deleted.Code != http.StatusAccepted || !strings.Contains(deleted.Body.String(), `"state":"pending"`) {
		t.Fatalf("delete=%d body=%s", deleted.Code, deleted.Body.String())
	}
	repeated := httptest.NewRecorder()
	repeatRequest := httptest.NewRequest(http.MethodDelete, "/api/v2/repositories/"+repo.ID+"/artifacts/"+artifact.ID, nil)
	authorize(repeatRequest, "admin-secret")
	handler.ServeHTTP(repeated, repeatRequest)
	if repeated.Code != http.StatusAccepted {
		t.Fatalf("repeat delete=%d body=%s", repeated.Code, repeated.Body.String())
	}
	list := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/"+repo.ID+"/artifacts", nil)
	authorize(list, "admin-secret")
	listed := httptest.NewRecorder()
	handler.ServeHTTP(listed, list)
	if listed.Code != http.StatusOK || strings.Contains(listed.Body.String(), artifact.ID) {
		t.Fatalf("list=%d body=%s", listed.Code, listed.Body.String())
	}
	protocolRead := httptest.NewRequest(http.MethodGet, "/repository/maven/artifact-target/org/example/widget/1.0.0/widget-1.0.0.jar", nil)
	protocolRead.SetBasicAuth("maven", "resolver-secret")
	protocolResponse := httptest.NewRecorder()
	handler.ServeHTTP(protocolResponse, protocolRead)
	if protocolResponse.Code != http.StatusNotFound {
		t.Fatalf("protocol read=%d body=%s", protocolResponse.Code, protocolResponse.Body.String())
	}
	detailAfterDelete := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/"+repo.ID+"/artifacts/"+artifact.ID, nil)
	authorize(detailAfterDelete, "admin-secret")
	detailedAfterDelete := httptest.NewRecorder()
	handler.ServeHTTP(detailedAfterDelete, detailAfterDelete)
	if detailedAfterDelete.Code != http.StatusOK || !strings.Contains(detailedAfterDelete.Body.String(), `"state":"deleted"`) {
		t.Fatalf("detail after delete=%d body=%s", detailedAfterDelete.Code, detailedAfterDelete.Body.String())
	}
}

func TestHostedRepositoryManagementRejectsAnonymousAndInvalidRequests(t *testing.T) {
	handler := NewGatewayHandler(Dependencies{}, repository.NewMemoryStore(), TestAdapter{}, testAuthenticator())
	denied := httptest.NewRecorder()
	handler.ServeHTTP(denied, httptest.NewRequest(http.MethodGet, "/api/v2/repositories", nil))
	if denied.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous status=%d", denied.Code)
	}
	anonymousInvalidSession := httptest.NewRecorder()
	handler.ServeHTTP(anonymousInvalidSession, httptest.NewRequest(http.MethodPost, "/api/v2/publish-sessions/not-a-uuid:commit", nil))
	if anonymousInvalidSession.Code != http.StatusUnauthorized || !strings.Contains(anonymousInvalidSession.Body.String(), `"code":"access_denied"`) {
		t.Fatalf("anonymous invalid session=%d body=%s", anonymousInvalidSession.Code, anonymousInvalidSession.Body.String())
	}
	bad := httptest.NewRequest(http.MethodPost, "/api/v2/repositories", strings.NewReader(`{"name":"Bad Name","format":"npm"}`))
	authorize(bad, "admin-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, bad)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("invalid status=%d body=%s", response.Code, response.Body.String())
	}
	page := httptest.NewRequest(http.MethodGet, "/api/v2/repositories?pageToken=unknown", nil)
	authorize(page, "admin-secret")
	paged := httptest.NewRecorder()
	handler.ServeHTTP(paged, page)
	if paged.Code != http.StatusBadRequest || !strings.Contains(paged.Body.String(), `"code":"invalid_page_token"`) {
		t.Fatalf("invalid page token=%d body=%s", paged.Code, paged.Body.String())
	}
	invalidID := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/not-a-uuid", nil)
	authorize(invalidID, "admin-secret")
	invalidIDResponse := httptest.NewRecorder()
	handler.ServeHTTP(invalidIDResponse, invalidID)
	if invalidIDResponse.Code != http.StatusBadRequest || !strings.Contains(invalidIDResponse.Body.String(), `"code":"invalid_request"`) {
		t.Fatalf("invalid id=%d body=%s", invalidIDResponse.Code, invalidIDResponse.Body.String())
	}
	invalidArtifactRepositoryID := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/not-a-uuid/artifacts", nil)
	authorize(invalidArtifactRepositoryID, "admin-secret")
	invalidArtifactRepositoryIDResponse := httptest.NewRecorder()
	handler.ServeHTTP(invalidArtifactRepositoryIDResponse, invalidArtifactRepositoryID)
	if invalidArtifactRepositoryIDResponse.Code != http.StatusBadRequest || !strings.Contains(invalidArtifactRepositoryIDResponse.Body.String(), `"code":"invalid_request"`) {
		t.Fatalf("invalid artifact repository id=%d body=%s", invalidArtifactRepositoryIDResponse.Code, invalidArtifactRepositoryIDResponse.Body.String())
	}
	invalidSessionID := httptest.NewRequest(http.MethodPost, "/api/v2/publish-sessions/not-a-uuid:commit", nil)
	authorize(invalidSessionID, "admin-secret")
	invalidSessionIDResponse := httptest.NewRecorder()
	handler.ServeHTTP(invalidSessionIDResponse, invalidSessionID)
	if invalidSessionIDResponse.Code != http.StatusBadRequest || !strings.Contains(invalidSessionIDResponse.Body.String(), `"code":"invalid_request"`) {
		t.Fatalf("invalid session id=%d body=%s", invalidSessionIDResponse.Code, invalidSessionIDResponse.Body.String())
	}
	nonCommitSessionPost := httptest.NewRequest(http.MethodPost, "/api/v2/publish-sessions/"+uuid.NewString(), nil)
	authorize(nonCommitSessionPost, "admin-secret")
	nonCommitSessionPostResponse := httptest.NewRecorder()
	handler.ServeHTTP(nonCommitSessionPostResponse, nonCommitSessionPost)
	if nonCommitSessionPostResponse.Code != http.StatusMethodNotAllowed {
		t.Fatalf("non-commit session post=%d body=%s", nonCommitSessionPostResponse.Code, nonCommitSessionPostResponse.Body.String())
	}
}

func TestHostedRepositoryIdempotencyAndPagination(t *testing.T) {
	store := repository.NewMemoryStore()
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())
	create := func(name, key string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/api/v2/repositories", strings.NewReader(`{"name":"`+name+`","format":"raw"}`))
		authorize(r, "admin-secret")
		r.Header.Set("Idempotency-Key", key)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	if missing := create("missing", ""); missing.Code != http.StatusBadRequest {
		t.Fatalf("missing key=%d", missing.Code)
	}
	first := create("one", "same-key")
	if first.Code != http.StatusCreated {
		t.Fatalf("first=%d %s", first.Code, first.Body.String())
	}
	replay := create("one", "same-key")
	if replay.Code != http.StatusCreated || replay.Body.String() != first.Body.String() {
		t.Fatalf("replay=%d %s", replay.Code, replay.Body.String())
	}
	if conflict := create("two", "same-key"); conflict.Code != http.StatusConflict || !strings.Contains(conflict.Body.String(), "idempotency_conflict") {
		t.Fatalf("conflict=%d %s", conflict.Code, conflict.Body.String())
	}
	if second := create("two", "two-key"); second.Code != http.StatusCreated {
		t.Fatalf("second=%d", second.Code)
	}
	list := httptest.NewRequest(http.MethodGet, "/api/v2/repositories?pageSize=1", nil)
	authorize(list, "admin-secret")
	pageOne := httptest.NewRecorder()
	handler.ServeHTTP(pageOne, list)
	if pageOne.Code != http.StatusOK {
		t.Fatalf("page one=%d", pageOne.Code)
	}
	var decoded repositoryPage
	if err := json.NewDecoder(pageOne.Body).Decode(&decoded); err != nil || len(decoded.Items) != 1 || decoded.NextPageToken == "" {
		t.Fatalf("page=%#v err=%v", decoded, err)
	}
	pageTwo := httptest.NewRequest(http.MethodGet, "/api/v2/repositories?pageSize=1&pageToken="+decoded.NextPageToken, nil)
	authorize(pageTwo, "admin-secret")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, pageTwo)
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), decoded.Items[0].ID) {
		t.Fatalf("page two=%d %s", w.Code, w.Body.String())
	}
	payload, _ := json.Marshal(repositoryPageCursor{Endpoint: "repositories", ID: decoded.Items[0].ID, ExpiresAt: time.Now().Add(-time.Second).Unix()})
	mac := hmac.New(sha256.New, []byte("admin-secret"))
	_, _ = mac.Write(payload)
	expired := base64.RawURLEncoding.EncodeToString(append(payload, mac.Sum(nil)...))
	expiredRequest := httptest.NewRequest(http.MethodGet, "/api/v2/repositories?pageToken="+expired, nil)
	authorize(expiredRequest, "admin-secret")
	expiredResponse := httptest.NewRecorder()
	handler.ServeHTTP(expiredResponse, expiredRequest)
	if expiredResponse.Code != http.StatusBadRequest || !strings.Contains(expiredResponse.Body.String(), "invalid_page_token") {
		t.Fatalf("expired token=%d %s", expiredResponse.Code, expiredResponse.Body.String())
	}
}

func TestNativeRepositoryGuardDeniesAnonymousAndDisabledProtocols(t *testing.T) {
	store := repository.NewMemoryStore()
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())
	for _, format := range []repository.Format{repository.FormatRaw, repository.FormatOCI, repository.FormatMaven} {
		repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: string(format) + "-native", Format: format})
		if err != nil {
			t.Fatal(err)
		}
		path := map[repository.Format]string{repository.FormatRaw: "/raw/raw-native/a", repository.FormatOCI: "/v2/oci-native/manifests/latest", repository.FormatMaven: "/maven/maven-native/a.pom"}[format]
		anonymous := httptest.NewRecorder()
		handler.ServeHTTP(anonymous, httptest.NewRequest(http.MethodGet, path, nil))
		if anonymous.Code != http.StatusUnauthorized {
			t.Fatalf("%s anonymous=%d", format, anonymous.Code)
		}
		if _, err := store.DisableHostedRepository(context.Background(), repo.ID); err != nil {
			t.Fatal(err)
		}
		disabled := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, path, nil)
		authorize(r, "resolver-secret")
		handler.ServeHTTP(disabled, r)
		if disabled.Code != http.StatusForbidden {
			t.Fatalf("%s disabled=%d", format, disabled.Code)
		}
	}
}

func TestMemoryHostedRepositoryHonorsPageSize200(t *testing.T) {
	store := repository.NewMemoryStore()
	for i := 0; i < 201; i++ {
		if _, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: fmt.Sprintf("repo-%03d", i), Format: repository.FormatRaw}); err != nil {
			t.Fatal(err)
		}
	}
	items, next, err := store.ListHostedRepositories(context.Background(), 200, "")
	if err != nil || len(items) != 200 || next == "" {
		t.Fatalf("items=%d next=%q err=%v", len(items), next, err)
	}
}
