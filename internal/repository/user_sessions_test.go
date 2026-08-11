package repository

import (
	"context"
	"testing"
	"time"
)

func TestMemoryUserSessionsSupportIndependentRevocationAndPruning(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	firstUser, err := store.CreateUser(ctx, User{ID: "first", Name: "first", Role: "reader"})
	if err != nil {
		t.Fatal(err)
	}
	secondUser, err := store.CreateUser(ctx, User{ID: "second", Name: "second", Role: "reader"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	first, err := store.CreateUserSession(ctx, UserSession{ID: "first-session", UserID: firstUser.ID, Kind: UserSessionLocal, CreatedAt: now, ExpiresAt: now.Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.CreateUserSession(ctx, UserSession{ID: "expired-session", UserID: firstUser.ID, Kind: UserSessionOIDC, CreatedAt: now.Add(-2 * time.Hour), ExpiresAt: now.Add(-time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.CreateUserSession(ctx, UserSession{ID: "other-session", UserID: secondUser.ID, Kind: UserSessionLocal, CreatedAt: now, ExpiresAt: now.Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}

	active, err := store.ListUserSessions(ctx, firstUser.ID, false)
	if err != nil || len(active) != 1 || active[0].ID != first.ID {
		t.Fatalf("active=%+v err=%v", active, err)
	}
	if _, err := store.GetUserSession(ctx, firstUser.ID, "other-session"); err != ErrNotFound {
		t.Fatalf("cross-user lookup err=%v", err)
	}
	revoked, err := store.RevokeUserSession(ctx, firstUser.ID, first.ID)
	if err != nil || revoked.RevokedAt == nil {
		t.Fatalf("revoked=%+v err=%v", revoked, err)
	}
	active, err = store.ListUserSessions(ctx, firstUser.ID, false)
	if err != nil || len(active) != 0 {
		t.Fatalf("active after revoke=%+v err=%v", active, err)
	}
	all, err := store.ListUserSessions(ctx, firstUser.ID, true)
	if err != nil || len(all) != 2 {
		t.Fatalf("all=%+v err=%v", all, err)
	}
	pruned, err := store.PruneExpiredUserSessions(ctx, now, 1)
	if err != nil || pruned != 1 {
		t.Fatalf("pruned=%d err=%v", pruned, err)
	}
	all, err = store.ListUserSessions(ctx, firstUser.ID, true)
	if err != nil || len(all) != 1 || all[0].ID != first.ID {
		t.Fatalf("remaining=%+v err=%v", all, err)
	}
}
