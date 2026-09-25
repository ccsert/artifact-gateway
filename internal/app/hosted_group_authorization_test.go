package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	adminopenapi "github.com/artifact-gateway/artifact-gateway/internal/admin/openapi"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

// A hosted group references repositories, so managing one needs authority over
// every repository it points at, and nothing about that answer is cached.
func TestHostedGroupMembershipFollowsRepositoryAdministration(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	managed, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "group-managed", Format: repository.FormatRaw})
	if err != nil {
		t.Fatal(err)
	}
	other, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "group-other", Format: repository.FormatRaw})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReplaceRepositoryGrants(ctx, managed.ID, []repository.RepositoryGrant{{Principal: "repo-admin", Scopes: []string{"repositories:admin"}}}, "1"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReplaceRepositoryGrants(ctx, other.ID, []repository.RepositoryGrant{{Principal: "other-admin", Scopes: []string{"repositories:admin"}}}, "1"); err != nil {
		t.Fatal(err)
	}
	authenticator := testAuthenticator()
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, authenticator)
	call := func(token, method, path, body, version string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		authorize(r, token)
		r.Header.Set("Idempotency-Key", uuid.NewString())
		if version != "" {
			r.Header.Set("If-Match", version)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	memberBody := func(repositoryIDs ...string) string {
		parts := make([]string, 0, len(repositoryIDs))
		for index, repositoryID := range repositoryIDs {
			parts = append(parts, `{"repositoryId":"`+repositoryID+`","position":`+string(rune('0'+index))+`}`)
		}
		return "[" + strings.Join(parts, ",") + "]"
	}
	createBody := func(name string, repositoryIDs ...string) string {
		return `{"name":"` + name + `","format":"raw","members":` + memberBody(repositoryIDs...) + `}`
	}

	repoAdmin := authenticator.IssueToken("repo-admin")
	otherAdmin := authenticator.IssueToken("other-admin")

	// A group over the administered repository is the repository administrator's
	// to create, replace, and delete.
	created := call(repoAdmin, http.MethodPost, "/api/v2/groups", createBody("managed-group", managed.ID), "")
	if created.Code != http.StatusCreated {
		t.Fatalf("create=%d body=%s", created.Code, created.Body.String())
	}
	var group adminopenapi.Group
	if err := json.Unmarshal(created.Body.Bytes(), &group); err != nil {
		t.Fatal(err)
	}
	groupPath := "/api/v2/groups/" + group.Id.String()
	replaced := call(repoAdmin, http.MethodPut, groupPath+"/members", memberBody(managed.ID), "1")
	if replaced.Code != http.StatusOK {
		t.Fatalf("replace=%d body=%s", replaced.Code, replaced.Body.String())
	}

	// A member outside the caller's administration refuses the whole request and
	// names the member that is out of bounds.
	for _, tc := range []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{name: "create with a foreign member", method: http.MethodPost, path: "/api/v2/groups", body: createBody("foreign-group", managed.ID, other.ID)},
		{name: "add a foreign member", method: http.MethodPut, path: groupPath + "/members", body: memberBody(managed.ID, other.ID)},
		{name: "drop the administered member for a foreign one", method: http.MethodPut, path: groupPath + "/members", body: memberBody(other.ID)},
		{name: "replace the group for a foreign one", method: http.MethodPut, path: groupPath, body: createBody("managed-group", other.ID)},
		{name: "another administrator deletes it", method: http.MethodDelete, path: groupPath, body: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			token := repoAdmin
			if tc.name == "another administrator deletes it" {
				token = otherAdmin
			}
			response := call(token, tc.method, tc.path, tc.body, "1")
			if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), `"code":"access_denied"`) {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
	if !strings.Contains(call(repoAdmin, http.MethodPost, "/api/v2/groups", createBody("named-member-group", managed.ID, other.ID), "").Body.String(), "group-other") {
		t.Fatal("the refusal must name the member that is out of bounds")
	}
	var deniedAudit bool
	for _, audit := range store.Audits {
		if audit.Actor == "repo-admin" && audit.Outcome == repository.AuditAccessDenied && audit.Format == "management" && audit.Operation == "group.membership_denied" {
			deniedAudit = true
		}
	}
	if !deniedAudit {
		t.Fatalf("missing membership denial audit: %#v", store.Audits)
	}

	// An account that administers that repository again may delete the group;
	// withdrawing the grant first takes the group away from it.
	if response := call(otherAdmin, http.MethodGet, groupPath, "", ""); response.Code != http.StatusForbidden {
		t.Fatalf("unrelated administrator read=%d body=%s", response.Code, response.Body.String())
	}
	if _, err := store.ReplaceRepositoryGrants(ctx, managed.ID, nil, "2"); err != nil {
		t.Fatal(err)
	}
	if response := call(repoAdmin, http.MethodDelete, groupPath, "", ""); response.Code != http.StatusForbidden {
		t.Fatalf("withdrawn grant delete=%d body=%s", response.Code, response.Body.String())
	}
	if _, err := store.ReplaceRepositoryGrants(ctx, managed.ID, []repository.RepositoryGrant{{Principal: "repo-admin", Scopes: []string{"repositories:admin"}}}, "3"); err != nil {
		t.Fatal(err)
	}
	if response := call(repoAdmin, http.MethodDelete, groupPath, "", ""); response.Code != http.StatusNoContent {
		t.Fatalf("restored grant delete=%d body=%s", response.Code, response.Body.String())
	}
}
