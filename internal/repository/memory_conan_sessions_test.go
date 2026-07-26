package repository

import (
	"context"
	"testing"
	"time"
)

func TestMemoryConanPublishSessionRequiresEveryDeclaredObject(t *testing.T) {
	store := NewMemoryStore()
	session, err := store.CreateConanPublishSession(context.Background(), ConanPublishSession{
		ID: "session", RepositoryID: "repo", Publisher: "publisher", Kind: "recipe",
		Reference: "pkg/1.0/user/stable", RecipeRevision: "rrev", State: "open", ExpiresAt: time.Now().Add(time.Hour),
		Objects: []MavenDeclaredObject{{Name: "conanfile.py", Digest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Size: 1}, {Name: "manifest.txt", Digest: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", Size: 1}},
	})
	if err != nil || session.State != "open" {
		t.Fatalf("create session=%#v err=%v", session, err)
	}
	if err = store.MarkConanPublishObject(context.Background(), session.ID, "conanfile.py", "native/conan/session/conanfile.py"); err != nil {
		t.Fatal(err)
	}
	if err = store.CommitConanPublishSession(context.Background(), session.ID); err != ErrDisabled {
		t.Fatalf("incomplete commit error=%v, want %v", err, ErrDisabled)
	}
	if err = store.MarkConanPublishObject(context.Background(), session.ID, "manifest.txt", "native/conan/session/manifest.txt"); err != nil {
		t.Fatal(err)
	}
	if err = store.CommitConanPublishSession(context.Background(), session.ID); err != nil {
		t.Fatal(err)
	}
	committed, err := store.GetConanPublishSession(context.Background(), session.ID)
	if err != nil || committed.State != "committed" {
		t.Fatalf("committed session=%#v err=%v", committed, err)
	}
}
