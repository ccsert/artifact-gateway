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
