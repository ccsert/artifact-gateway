package repository

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestMemoryMavenCommitRejectsCollectorClaim(t *testing.T) {
	store := NewMemoryStore()
	key := "native/maven/sha256/claimed"
	_, err := store.CreateMavenPublishSession(context.Background(), MavenPublishSession{ID: "session", RepositoryID: "repo", Coordinate: "org.example:widget:1.0.0", Publisher: "alice", State: "open", Objects: []MavenDeclaredObject{{Name: "widget-1.0.0.jar", Digest: "sha256:claimed", Size: 3}}, ExpiresAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if err = store.MarkMavenPublishObject(context.Background(), "session", "widget-1.0.0.jar", key); err != nil {
		t.Fatal(err)
	}
	store.mavenObjectIntents[key] = mavenObjectIntent{createdAt: time.Now().Add(-25 * time.Hour), claimedAt: time.Now()}
	_, err = store.CommitMavenPublishSession(context.Background(), "session", []MavenAsset{{RepositoryID: "repo", Path: "org/example/widget/1.0.0/widget-1.0.0.jar", ObjectKey: key, Digest: "sha256:claimed", Size: 3}})
	if !errors.Is(err, ErrDisabled) {
		t.Fatalf("commit must be fenced after collector claim, err=%v", err)
	}
}

func TestMemoryHostedGroupVersionAndIdempotency(t *testing.T) {
	store := NewMemoryStore()
	group := HostedGroup{ID: "group-1", Name: "releases", Format: FormatMaven, Members: []GroupMember{{RepositoryID: "repo-2", Position: 1}, {RepositoryID: "repo-1", Position: 0}}}
	created, replayed, err := store.CreateHostedGroupIdempotently(context.Background(), group, "admin", "create-key", "payload")
	if err != nil || replayed || created.Version != "1" || created.Members[0].RepositoryID != "repo-1" {
		t.Fatalf("created=%#v replayed=%t err=%v", created, replayed, err)
	}
	if _, replayed, err = store.CreateHostedGroupIdempotently(context.Background(), HostedGroup{ID: "other"}, "admin", "create-key", "payload"); err != nil || !replayed {
		t.Fatalf("replay=%t err=%v", replayed, err)
	}
	if _, _, err = store.CreateHostedGroupIdempotently(context.Background(), HostedGroup{ID: "other"}, "admin", "create-key", "different"); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("conflict err=%v", err)
	}
	created.Members = []GroupMember{{RepositoryID: "repo-3", Position: 0}}
	replaced, err := store.ReplaceHostedGroup(context.Background(), created, "1")
	if err != nil || replaced.Version != "2" {
		t.Fatalf("replaced=%#v err=%v", replaced, err)
	}
	if _, err = store.ReplaceHostedGroupMembers(context.Background(), created.ID, nil, "1"); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("stale replace err=%v", err)
	}
}

func TestMemoryConanGroupPreservesManagedRepositoryBinding(t *testing.T) {
	store := NewMemoryStore()
	repo, err := store.CreateHostedRepository(context.Background(), HostedRepository{ID: "conan-repository", Name: "central-remote", Format: FormatConan})
	if err != nil {
		t.Fatal(err)
	}
	created, err := store.CreateConanGroup(context.Background(), Group{Name: "central", Members: []Member{{Name: "remote", Type: MemberProxy, Endpoint: "https://conan.example", RepositoryID: repo.ID}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(created.Members) != 1 || created.Members[0].RepositoryID != repo.ID {
		t.Fatalf("created=%#v", created)
	}
	loaded, err := store.GetConanGroup(context.Background(), "central")
	if err != nil || len(loaded.Members) != 1 || loaded.Members[0].RepositoryID != repo.ID {
		t.Fatalf("loaded=%#v err=%v", loaded, err)
	}
}
