package app

import (
	"context"
	"sort"
	"strings"
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
	if _, err = store.ReplaceRepositoryRetentionPolicy(ctx, repo.ID, repository.RepositoryRetentionPolicy{Enabled: true, KeepDays: 1, MinimumVersions: 1}, "1"); err != nil {
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

func TestNativeMavenRetentionAppliesSnapshotCapAndCoordinateRules(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "retention-rules", Format: repository.FormatMaven})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.ReplaceRepositoryRetentionPolicy(ctx, repo.ID, repository.RepositoryRetentionPolicy{
		Enabled: true, KeepDays: 30, SnapshotKeepDays: 1, MinimumVersions: 1, MaximumVersions: 2,
		CoordinatePatterns: []string{`^org\.example:`}, ProtectedPatterns: []string{`org\.example:keep:`},
	}, "1"); err != nil {
		t.Fatal(err)
	}
	publish := func(coordinate string, suffix string) {
		session := repository.MavenPublishSession{ID: uuid.NewString(), RepositoryID: repo.ID, Coordinate: coordinate, State: "open", ExpiresAt: time.Now().Add(time.Hour), Objects: []repository.MavenDeclaredObject{{Name: suffix + ".jar", Digest: "sha256:" + strings.Repeat(suffix[:1], 64), Size: 1}}}
		if _, err := store.CreateMavenPublishSession(ctx, session); err != nil {
			t.Fatal(err)
		}
		key := "native/maven/retention-rules/" + session.ID
		if err := store.MarkMavenPublishObject(ctx, session.ID, session.Objects[0].Name, key); err != nil {
			t.Fatal(err)
		}
		if _, err := store.CommitMavenPublishSession(ctx, session.ID, []repository.MavenAsset{{RepositoryID: repo.ID, Path: strings.ReplaceAll(coordinate, ":", "/") + "/" + suffix + ".jar", ObjectKey: key, Digest: session.Objects[0].Digest, Size: 1}}); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Millisecond)
	}
	publish("org.example:release:1.0.0", "a1")
	publish("org.example:release:2.0.0", "b2")
	publish("org.example:release:3.0.0", "c3")
	publish("org.example:snapshot:1.0-SNAPSHOT", "d4")
	publish("org.example:snapshot:2.0-SNAPSHOT", "e5")
	publish("org.other:ignored:1.0.0", "f6")
	publish("org.example:keep:1.0.0", "g7")

	candidates, err := (NativeMavenRetention{Store: store, Now: func() time.Time { return time.Now().UTC().Add(48 * time.Hour) }}).PlanRepositoryDetailed(ctx, repo.ID)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]struct {
		reasons     []string
		versionType string
	}{}
	for _, candidate := range candidates {
		got[candidate.Artifact.Coordinate] = struct {
			reasons     []string
			versionType string
		}{reasons: candidate.Reasons, versionType: candidate.VersionType}
	}
	if candidate, ok := got["org.example:release:1.0.0"]; !ok || !containsRetentionReason(candidate.reasons, "maximum_versions") {
		t.Fatalf("release cap candidate=%#v", candidate)
	}
	if candidate, ok := got["org.example:snapshot:1.0-SNAPSHOT"]; !ok || !containsRetentionReason(candidate.reasons, "age") || candidate.versionType != "snapshot" {
		t.Fatalf("snapshot age candidate=%#v", candidate)
	}
	if _, ok := got["org.other:ignored:1.0.0"]; ok {
		t.Fatalf("coordinate pattern leaked ignored artifact: %#v", got)
	}
	if _, ok := got["org.example:keep:1.0.0"]; ok {
		t.Fatalf("protected coordinate became candidate: %#v", got)
	}
}

func containsRetentionReason(reasons []string, expected string) bool {
	for _, reason := range reasons {
		if reason == expected {
			return true
		}
	}
	return false
}
