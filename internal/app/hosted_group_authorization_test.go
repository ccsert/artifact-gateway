package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	adminopenapi "github.com/ccsert/artifact-gateway/internal/admin/openapi"
	"github.com/ccsert/artifact-gateway/internal/repository"
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
	var replacedGroup adminopenapi.Group
	if replaced.Code != http.StatusOK || json.Unmarshal(replaced.Body.Bytes(), &replacedGroup) != nil || replacedGroup.Id != group.Id || replacedGroup.Version != "2" || len(replacedGroup.Members) != 1 {
		t.Fatalf("a member replacement answers the whole updated group: replace=%d body=%s", replaced.Code, replaced.Body.String())
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
	// The rule that lets a caller write a group lets it read the group back;
	// any other caller cannot tell the group exists.
	for _, suffix := range []string{"", "/members", "/resolution", "/capacity"} {
		if response := call(repoAdmin, http.MethodGet, groupPath+suffix, "", ""); response.Code != http.StatusOK {
			t.Fatalf("member administrator read %q=%d body=%s", suffix, response.Code, response.Body.String())
		}
		if response := call(otherAdmin, http.MethodGet, groupPath+suffix, "", ""); response.Code != http.StatusNotFound || !strings.Contains(response.Body.String(), `"code":"not_found"`) {
			t.Fatalf("unrelated administrator read %q=%d body=%s", suffix, response.Code, response.Body.String())
		}
	}
	listed := func(token string) []string {
		var names []string
		pageToken := ""
		for pages := 0; ; pages++ {
			path := "/api/v2/groups?pageSize=1"
			if pageToken != "" {
				path += "&pageToken=" + pageToken
			}
			response := call(token, http.MethodGet, path, "", "")
			var page struct {
				Items         []adminopenapi.Group `json:"items"`
				NextPageToken string               `json:"nextPageToken"`
			}
			if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &page) != nil || len(page.Items) > 1 || pages > 10 {
				t.Fatalf("list=%d body=%s", response.Code, response.Body.String())
			}
			for _, item := range page.Items {
				names = append(names, item.Name)
			}
			if page.NextPageToken == "" {
				return names
			}
			pageToken = page.NextPageToken
		}
	}
	// A group the caller only partly administers stays hidden, and filtering
	// does not cut a page short of the groups after it.
	for _, body := range []string{createBody("mixed-group", managed.ID, other.ID), createBody("other-group", other.ID), createBody("second-managed-group", managed.ID)} {
		if response := call("admin-secret", http.MethodPost, "/api/v2/groups", body, ""); response.Code != http.StatusCreated {
			t.Fatalf("platform create=%d body=%s", response.Code, response.Body.String())
		}
	}
	if names := strings.Join(sortedStrings(listed(repoAdmin)), ","); names != "managed-group,second-managed-group" {
		t.Fatalf("member administrator lists %s", names)
	}
	if names := strings.Join(sortedStrings(listed(otherAdmin)), ","); names != "other-group" {
		t.Fatalf("other administrator lists %s", names)
	}
	if names := listed("admin-secret"); len(names) != 4 {
		t.Fatalf("platform tier lists %v", names)
	}
	// One request checks a bounded number of Groups: with a budget of one, a
	// page can be empty while its token continues the scan, and walking the
	// pages still finds every readable Group.
	previous := groupListScanBudget
	groupListScanBudget = 1
	t.Cleanup(func() { groupListScanBudget = previous })
	var first struct {
		Items         []adminopenapi.Group `json:"items"`
		NextPageToken string               `json:"nextPageToken"`
	}
	if response := call(otherAdmin, http.MethodGet, "/api/v2/groups?pageSize=50", "", ""); json.Unmarshal(response.Body.Bytes(), &first) != nil || len(first.Items) > 1 || first.NextPageToken == "" {
		t.Fatalf("a bounded scan returns at most what it checked and a token: %s", response.Body.String())
	}
	if names := strings.Join(sortedStrings(listed(repoAdmin)), ","); names != "managed-group,second-managed-group" {
		t.Fatalf("member administrator lists %s under a bounded scan", names)
	}
	if names := strings.Join(sortedStrings(listed(otherAdmin)), ","); names != "other-group" {
		t.Fatalf("other administrator lists %s under a bounded scan", names)
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

func sortedStrings(values []string) []string {
	sort.Strings(values)
	return values
}
