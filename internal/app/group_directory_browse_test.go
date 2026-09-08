package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	adminopenapi "github.com/artifact-gateway/artifact-gateway/internal/admin/openapi"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

func groupDirectoryRequest(t *testing.T, handler http.Handler, token, id, parent, pageToken string, size int) (*httptest.ResponseRecorder, adminopenapi.GroupBrowsePage) {
	t.Helper()
	query := url.Values{"pageSize": {strconv.Itoa(size)}}
	if parent != "" {
		query.Set("parent", parent)
	}
	if pageToken != "" {
		query.Set("pageToken", pageToken)
	}
	request := httptest.NewRequest("GET", "/api/v2/groups/"+id+"/browse?"+query.Encode(), nil)
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	var page adminopenapi.GroupBrowsePage
	if response.Code == 200 {
		if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil {
			t.Fatal(err)
		}
	}
	return response, page
}

func newBrowseGroup(t *testing.T, store GatewayStore, format repository.Format, repos ...repository.HostedRepository) repository.HostedGroup {
	t.Helper()
	group := repository.HostedGroup{ID: uuid.NewString(), Name: "browse-" + uuid.NewString(), Format: format, AnonymousRead: true}
	for i, repo := range repos {
		group.Members = append(group.Members, repository.GroupMember{RepositoryID: repo.ID, Position: i})
	}
	saved, _, err := store.CreateHostedGroupIdempotently(context.Background(), group, "admin", uuid.NewString(), "browse-group")
	if err != nil {
		t.Fatal(err)
	}
	return saved
}

func TestGroupDirectoryMergesContributionsAndInvalidatesNavigation(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	auth := testAuthenticator()
	repos := make([]repository.HostedRepository, 3)
	for i := range repos {
		repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "raw-" + strconv.Itoa(i), Format: repository.FormatRaw, AnonymousRead: i < 2})
		if err != nil {
			t.Fatal(err)
		}
		repos[i] = repo
		for _, path := range []string{"docs/release%20notes.txt", "z.bin"} {
			_, err = store.PutRawAsset(ctx, repository.RawAsset{RepositoryID: repo.ID, Path: path, Digest: "sha256:" + strings.Repeat(strconv.Itoa(i+1), 64), ObjectKey: uuid.NewString(), Size: int64(i + 1)})
			if err != nil {
				t.Fatal(err)
			}
		}
		grants := []repository.RepositoryGrant{}
		if i < 2 {
			grants = append(grants, repository.RepositoryGrant{Principal: "reader", Scopes: []string{"repositories:read"}})
		}
		if _, err = store.ReplaceRepositoryGrants(ctx, repo.ID, grants, "1"); err != nil {
			t.Fatal(err)
		}
	}
	group := newBrowseGroup(t, store, repository.FormatRaw, repos...)
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, auth)
	token := auth.IssueToken("reader")
	res, root := groupDirectoryRequest(t, handler, token, group.ID, "", "", 1)
	if res.Code != 200 || len(root.Items) != 1 || root.Items[0].Name != "docs" || len(root.Candidates) != 2 || root.NextPageToken == nil {
		t.Fatalf("root: %d %s", res.Code, res.Body)
	}
	_, children := groupDirectoryRequest(t, handler, token, group.ID, root.Items[0].Id, "", 50)
	if len(children.Items) != 1 {
		t.Fatalf("children=%+v", children)
	}
	asset := children.Items[0]
	if asset.Name != "release notes.txt" || asset.Sources == nil || len(*asset.Sources) != 2 || asset.Digest != nil || asset.SourceRepositoryId != nil {
		t.Fatalf("asset=%+v", asset)
	}
	sources := *asset.Sources
	if sources[0].RepositoryName != "raw-0" || sources[1].RepositoryName != "raw-1" || *sources[0].Digest == *sources[1].Digest {
		t.Fatalf("sources=%+v", sources)
	}
	_, second := groupDirectoryRequest(t, handler, token, group.ID, "", *root.NextPageToken, 1)
	if len(second.Items) != 1 || second.Items[0].Name != "z.bin" || second.NextPageToken != nil {
		t.Fatalf("second=%+v", second)
	}
	for _, test := range []struct{ token, parent, page string }{
		{auth.AdminToken, root.Items[0].Id, ""}, {token, root.Items[0].Id, *root.NextPageToken}, {token, root.Items[0].Id + "x", ""},
	} {
		response, _ := groupDirectoryRequest(t, handler, test.token, group.ID, test.parent, test.page, 1)
		if response.Code != 400 {
			t.Fatalf("reused cursor: %d %s", response.Code, response.Body)
		}
	}
	if _, err := store.ReplaceRepositoryGrants(ctx, repos[1].ID, nil, "2"); err != nil {
		t.Fatal(err)
	}
	res, _ = groupDirectoryRequest(t, handler, token, group.ID, root.Items[0].Id, "", 1)
	if res.Code != 400 {
		t.Fatalf("grant change retained parent: %d", res.Code)
	}
	_, updated := groupDirectoryRequest(t, handler, token, group.ID, "", "", 50)
	if len(updated.Candidates) != 1 {
		t.Fatalf("revoked member exposed: %+v", updated.Candidates)
	}
	res, _ = groupDirectoryRequest(t, handler, "", group.ID, "", "", 50)
	if res.Code != 401 {
		t.Fatalf("anonymous global gate: %d", res.Code)
	}
	enableAnonymousAccess(t, store)
	res, anonymous := groupDirectoryRequest(t, handler, "", group.ID, "", "", 50)
	if res.Code != 200 || len(anonymous.Candidates) != 2 {
		t.Fatalf("anonymous: %d %s", res.Code, res.Body)
	}
	_, err := store.ReplaceHostedGroupMembers(ctx, group.ID, []repository.GroupMember{{RepositoryID: repos[1].ID, Position: 0}, {RepositoryID: repos[0].ID, Position: 1}, {RepositoryID: repos[2].ID, Position: 2}}, group.Version)
	if err != nil {
		t.Fatal(err)
	}
	res, _ = groupDirectoryRequest(t, handler, "", group.ID, anonymous.Items[0].Id, "", 50)
	if res.Code != 400 {
		t.Fatalf("order change retained parent: %d", res.Code)
	}
	for _, size := range []int{0, 201} {
		res, _ = groupDirectoryRequest(t, handler, token, group.ID, "", "", size)
		if res.Code != 400 {
			t.Fatalf("size %d: %d", size, res.Code)
		}
	}
}
