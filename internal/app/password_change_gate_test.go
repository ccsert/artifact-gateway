package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

func TestPasswordChangeRequiredBlocksRepositorySurface(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "reset-target", Format: repository.FormatRaw})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReplaceRepositoryGrants(ctx, repo.ID, []repository.RepositoryGrant{
		{Principal: "user:reset", Scopes: []string{"repositories:admin"}},
	}, "1"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateUser(ctx, repository.User{
		ID: uuid.NewString(), Name: "reset", Role: string(RoleReader), SecretHash: "hash", MustChangePassword: true,
	}); err != nil {
		t.Fatal(err)
	}
	authenticator := testAuthenticatorWithUsers(store)
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, authenticator)
	token := authenticator.IssueToken("user:reset")

	for _, path := range []string{
		"/api/v2/repositories",
		"/api/v2/repositories/" + repo.ID,
		"/api/v2/repositories/" + repo.ID + "/artifacts",
		"/api/v2/repositories/" + repo.ID + "/effective-access",
		"/api/v2/artifact-search?q=release",
	} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		authorize(request, token)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), `"code":"password_change_required"`) {
			t.Fatalf("%s=%d body=%s", path, response.Code, response.Body.String())
		}
	}

	// Clearing the forced change restores the repository authority the account
	// already held, proving the block tracks the password state alone.
	user, err := store.GetUserByName(ctx, "reset")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateUserPassword(ctx, user.ID, "hash", user.Version, false); err != nil {
		t.Fatal(err)
	}
	allowed := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/"+repo.ID, nil)
	authorize(allowed, token)
	allowedResponse := httptest.NewRecorder()
	handler.ServeHTTP(allowedResponse, allowed)
	if allowedResponse.Code != http.StatusOK {
		t.Fatalf("after clearing the forced change=%d body=%s", allowedResponse.Code, allowedResponse.Body.String())
	}
}
