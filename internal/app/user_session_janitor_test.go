package app

import (
	"context"
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

func TestUserSessionJanitorRetainsRecentHistoryAndBoundsDeletes(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	user, err := store.CreateUser(ctx, repository.User{ID: "janitor-user", Name: "janitor", Role: "reader"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, session := range []repository.UserSession{
		{ID: "old-one", UserID: user.ID, Kind: repository.UserSessionLocal, CreatedAt: now.Add(-50 * 24 * time.Hour), ExpiresAt: now.Add(-49 * 24 * time.Hour)},
		{ID: "old-two", UserID: user.ID, Kind: repository.UserSessionOIDC, CreatedAt: now.Add(-40 * 24 * time.Hour), ExpiresAt: now.Add(-39 * 24 * time.Hour)},
		{ID: "recent", UserID: user.ID, Kind: repository.UserSessionLocal, CreatedAt: now.Add(-2 * 24 * time.Hour), ExpiresAt: now.Add(-24 * time.Hour)},
	} {
		if _, err = store.CreateUserSession(ctx, session); err != nil {
			t.Fatal(err)
		}
	}
	janitor := UserSessionJanitor{Store: store, Retention: 30 * 24 * time.Hour, BatchSize: 1, Now: func() time.Time { return now }}
	if deleted, err := janitor.Run(ctx); err != nil || deleted != 1 {
		t.Fatalf("first cleanup deleted=%d err=%v", deleted, err)
	}
	remaining, err := store.ListUserSessions(ctx, user.ID, true)
	if err != nil || len(remaining) != 2 {
		t.Fatalf("remaining=%+v err=%v", remaining, err)
	}
	if deleted, err := janitor.Run(ctx); err != nil || deleted != 1 {
		t.Fatalf("second cleanup deleted=%d err=%v", deleted, err)
	}
	remaining, err = store.ListUserSessions(ctx, user.ID, true)
	if err != nil || len(remaining) != 1 || remaining[0].ID != "recent" {
		t.Fatalf("recent history=%+v err=%v", remaining, err)
	}
}
