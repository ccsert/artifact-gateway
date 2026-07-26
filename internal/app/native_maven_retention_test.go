package app

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

func TestNativeMavenRetentionKeepsMinimumVersionsPerModule(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "retention-maven", Format: repository.FormatMaven})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.ReplaceRepositoryRetentionPolicy(ctx, repo.ID, repository.RepositoryRetentionPolicy{KeepDays: 1, MinimumVersions: 1}, "1"); err != nil {
		t.Fatal(err)
	}
	for _, version := range []string{"1.0.0", "1.1.0", "1.2.0"} {
		session := repository.MavenPublishSession{ID: uuid.NewString(), RepositoryID: repo.ID, Coordinate: "org.example:widget:" + version, State: "open", ExpiresAt: time.Now().Add(time.Hour), Objects: []repository.MavenDeclaredObject{{Name: "widget-" + version + ".jar", Digest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Size: 1}}}
		if _, err = store.CreateMavenPublishSession(ctx, session); err != nil {
			t.Fatal(err)
		}
		key := "native/maven/sha256/retention-" + session.ID
		if err = store.MarkMavenPublishObject(ctx, session.ID, session.Objects[0].Name, key); err != nil {
			t.Fatal(err)
		}
		if _, err = store.CommitMavenPublishSession(ctx, session.ID, []repository.MavenAsset{{RepositoryID: repo.ID, Path: "org/example/widget/" + version + "/widget-" + version + ".jar", ObjectKey: key, Digest: session.Objects[0].Digest, Size: 1}}); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Millisecond)
	}
	before, err := store.ListMavenArtifacts(ctx, repo.ID)
	if err != nil {
		t.Fatal(err)
	}
	sort.Slice(before, func(i, j int) bool { return before[i].CreatedAt.After(before[j].CreatedAt) })
	metrics := &Metrics{}
	if err = (NativeMavenRetention{Store: store, Now: func() time.Time { return time.Now().Add(48 * time.Hour) }, Metrics: metrics}).Collect(ctx); err != nil {
		t.Fatal(err)
	}
	after, err := store.ListMavenArtifacts(ctx, repo.ID)
	if err != nil || len(after) != 1 || after[0].ID != before[0].ID {
		t.Fatalf("retained=%#v expected=%#v err=%v", after, before[0], err)
	}
	for _, artifact := range before[1:] {
		deleted, getErr := store.GetMavenArtifact(ctx, repo.ID, artifact.ID)
		if getErr != nil || deleted.State != "deleted" {
			t.Fatalf("artifact=%#v err=%v", deleted, getErr)
		}
	}
	if metrics.backgroundOperations[backgroundOperationLifecycle][backgroundOperationMaven][backgroundOperationStarted].Load() != 1 ||
		metrics.backgroundOperations[backgroundOperationLifecycle][backgroundOperationMaven][backgroundOperationCompleted].Load() != 1 ||
		metrics.backgroundInFlight[backgroundOperationLifecycle][backgroundOperationMaven].Load() != 0 {
		t.Fatalf("Maven retention lifecycle metrics were not recorded")
	}
}
