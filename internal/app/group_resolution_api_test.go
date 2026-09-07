package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	adminopenapi "github.com/artifact-gateway/artifact-gateway/internal/admin/openapi"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

func TestGroupResolutionUsesRuntimeOrderAndExcludesUnavailableMembers(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	var members []repository.GroupMember
	for i, repoType := range []repository.RepositoryType{repository.RepositoryTypeProxy, repository.RepositoryTypeHosted, repository.RepositoryTypeProxy, repository.RepositoryTypeHosted} {
		repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "member-" + uuid.NewString(), Format: repository.FormatRaw, Type: repoType, Endpoint: "https://upstream.example"})
		if err != nil {
			t.Fatal(err)
		}
		members = append(members, repository.GroupMember{RepositoryID: repo.ID, Position: i})
	}
	missing := repository.GroupMember{RepositoryID: uuid.NewString(), Position: 4}
	group, _, err := store.CreateHostedGroupIdempotently(ctx, repository.HostedGroup{ID: uuid.NewString(), Name: "resolution", Format: repository.FormatRaw, Members: append(members, missing)}, "admin", "resolution-fixture", "payload")
	if err != nil {
		t.Fatal(err)
	}
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())
	request := func(token string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, "/api/v2/groups/"+group.ID+"/resolution", nil)
		if token != "" {
			authorize(r, token)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	for _, token := range []string{"", "resolver-secret"} {
		if w := request(token); w.Code != http.StatusUnauthorized {
			t.Fatalf("non-admin status=%d body=%s", w.Code, w.Body.String())
		}
	}
	w := request(testAuthenticator().AdminToken)
	var plan adminopenapi.GroupResolution
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &plan) != nil {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	resolved, err := (v2GroupResolver{groups: store, repos: store}).resolveMembers(ctx, group)
	if err != nil || len(plan.Members) != 4 || plan.ExcludedMemberCount != 1 || plan.Strategy != adminopenapi.HostedFirst {
		t.Fatalf("plan=%+v err=%v", plan, err)
	}
	for i, member := range resolved {
		got := plan.Members[i]
		if got.RepositoryId.String() != member.RepositoryID || got.RepositoryName != member.Name || got.ConfiguredPosition != member.Position || got.ResolutionOrder != i+1 {
			t.Fatalf("member %d=%+v want=%+v", i, got, member)
		}
	}
	if plan.Members[0].ConfiguredPosition != 1 || plan.Members[1].ConfiguredPosition != 3 || plan.Members[2].ConfiguredPosition != 0 {
		t.Fatalf("not Hosted-first: %+v", plan.Members)
	}
	updated, err := store.ReplaceHostedGroupMembers(ctx, group.ID, []repository.GroupMember{{RepositoryID: members[2].RepositoryID, Position: 0}, {RepositoryID: members[0].RepositoryID, Position: 1}}, group.Version)
	if err != nil {
		t.Fatal(err)
	}
	w = request(testAuthenticator().AdminToken)
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &plan) != nil || plan.GroupVersion != updated.Version || plan.Members[0].RepositoryId.String() != members[2].RepositoryID {
		t.Fatalf("refresh=%s", w.Body.String())
	}
}
