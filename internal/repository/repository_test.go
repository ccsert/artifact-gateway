package repository

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestMemoryReplicationPlanIsIdempotentAndResumable(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	digest := "sha256:" + strings.Repeat("a", 64)
	plan := ReplicationPlan{ID: "plan", SourceRepositoryID: "source", TargetRepositoryID: "target", Format: FormatRaw, IdempotencyKey: "replicate-widget"}
	checks := []ReplicationCheckpoint{{ObjectKey: "native/raw/widget", Digest: digest, Size: 12}}
	created, replayed, err := store.CreateReplicationPlan(ctx, plan, checks)
	if err != nil || replayed || created.State != "pending" {
		t.Fatalf("created=%#v replayed=%t err=%v", created, replayed, err)
	}
	if replay, replayed, err := store.CreateReplicationPlan(ctx, plan, checks); err != nil || !replayed || replay.ID != plan.ID {
		t.Fatalf("replay=%#v replayed=%t err=%v", replay, replayed, err)
	}
	if _, _, err := store.CreateReplicationPlan(ctx, plan, []ReplicationCheckpoint{{ObjectKey: "native/raw/other", Digest: digest, Size: 12}}); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("conflicting replay err=%v", err)
	}
	claimed, err := store.ClaimReplicationPlans(ctx, 1)
	if err != nil || len(claimed) != 1 || claimed[0].State != "running" {
		t.Fatalf("claimed=%#v err=%v", claimed, err)
	}
	checkpoint := ReplicationCheckpoint{PlanID: plan.ID, ObjectKey: checks[0].ObjectKey, Digest: digest, Size: 12, ByteOffset: 12, State: "verified", Attempts: 1, VerifiedAt: time.Now()}
	if err = store.UpdateReplicationCheckpoint(ctx, checkpoint); err != nil {
		t.Fatal(err)
	}
	if err = store.CompleteReplicationPlan(ctx, plan.ID); err != nil {
		t.Fatal(err)
	}
	plans, err := store.ListReplicationPlans(ctx, "target", 10)
	if err != nil || len(plans) != 1 || plans[0].ID != plan.ID || plans[0].State != "completed" {
		t.Fatalf("plans=%#v err=%v", plans, err)
	}
	if _, err = store.ClaimReplicationPlans(ctx, 1); err != nil {
		t.Fatal(err)
	}
}

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

func TestMemoryMavenTombstoneRestoresUntilObjectIsCollected(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	key := "native/maven/sha256/restore"
	session := MavenPublishSession{ID: "restore-session", RepositoryID: "repo", Coordinate: "org.example:widget:1.0.0", Publisher: "alice", State: "open", ExpiresAt: time.Now().Add(time.Hour), Objects: []MavenDeclaredObject{{Name: "widget-1.0.0.jar", Digest: "sha256:restore", Size: 3}}}
	if _, err := store.CreateMavenPublishSession(ctx, session); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkMavenPublishObject(ctx, session.ID, session.Objects[0].Name, key); err != nil {
		t.Fatal(err)
	}
	artifact, err := store.CommitMavenPublishSession(ctx, session.ID, []MavenAsset{{RepositoryID: "repo", Path: "org/example/widget/1.0.0/widget-1.0.0.jar", ObjectKey: key, Digest: "sha256:restore", Size: 3}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.TombstoneMavenArtifact(ctx, "repo", artifact.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = store.GetMavenAsset(ctx, "repo", "org/example/widget/1.0.0/widget-1.0.0.jar"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("tombstoned asset remained readable: %v", err)
	}
	if _, err = store.RestoreMavenArtifact(ctx, "repo", artifact.ID); err != nil {
		t.Fatalf("restore before collection: %v", err)
	}
	if _, err = store.GetMavenAsset(ctx, "repo", "org/example/widget/1.0.0/widget-1.0.0.jar"); err != nil {
		t.Fatalf("restored asset unavailable: %v", err)
	}
	if _, err = store.TombstoneMavenArtifact(ctx, "repo", artifact.ID); err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	intent := store.mavenObjectIntents[key]
	intent.createdAt = time.Now().Add(-25 * time.Hour)
	store.mavenObjectIntents[key] = intent
	store.mu.Unlock()
	claimed, err := store.ClaimExpiredMavenObjectIntents(ctx, time.Now(), 1)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim=%#v err=%v", claimed, err)
	}
	if err = store.DeleteClaimedMavenObjectIntent(ctx, key, claimed[0].ClaimToken); err != nil {
		t.Fatal(err)
	}
	if _, err = store.RestoreMavenArtifact(ctx, "repo", artifact.ID); !errors.Is(err, ErrDisabled) {
		t.Fatalf("restore after collection err=%v", err)
	}
}

func TestMemoryMavenPromotionCopiesVisibleMetadataOnly(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	if _, err := store.CreateMavenPublishSession(ctx, MavenPublishSession{ID: "source", RepositoryID: "source-repo", Coordinate: "org.example:widget:1.0.0", Publisher: "alice", State: "open", ExpiresAt: time.Now().Add(time.Hour), Objects: []MavenDeclaredObject{{Name: "widget.jar", Digest: "sha256:widget", Size: 1}}}); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkMavenPublishObject(ctx, "source", "widget.jar", "native/maven/widget"); err != nil {
		t.Fatal(err)
	}
	source, err := store.CommitMavenPublishSession(ctx, "source", []MavenAsset{{RepositoryID: "source-repo", Path: "org/example/widget/1.0.0/widget.jar", ObjectKey: "native/maven/widget", Digest: "sha256:widget", Size: 1}})
	if err != nil {
		t.Fatal(err)
	}
	promoted, err := store.PromoteMavenArtifact(ctx, MavenPromotion{ID: "target", SourceRepositoryID: "source-repo", TargetRepositoryID: "target-repo", Coordinate: source.Coordinate, Digest: source.Digest})
	if err != nil || promoted.RepositoryID != "target-repo" {
		t.Fatalf("promoted=%#v err=%v", promoted, err)
	}
	if _, err = store.GetMavenAsset(ctx, "target-repo", "org/example/widget/1.0.0/widget.jar"); err != nil {
		t.Fatalf("target asset=%v", err)
	}
	if _, err = store.PromoteMavenArtifact(ctx, MavenPromotion{ID: "again", SourceRepositoryID: "source-repo", TargetRepositoryID: "target-repo", Coordinate: source.Coordinate, Digest: source.Digest}); !errors.Is(err, ErrNameExists) {
		t.Fatalf("duplicate promotion=%v", err)
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

func TestMemoryStorePreservesLegacyGroupMemberRepositoryBindings(t *testing.T) {
	store := NewMemoryStore()
	groups := []struct {
		name   string
		create func(context.Context, Group) (Group, error)
		get    func(context.Context, string) (Group, error)
	}{
		{name: "oci", create: store.CreateGroup, get: store.GetGroup},
		{name: "maven", create: store.CreateMavenGroup, get: store.GetMavenGroup},
		{name: "raw", create: store.CreateRawGroup, get: store.GetRawGroup},
	}
	for _, test := range groups {
		t.Run(test.name, func(t *testing.T) {
			created, err := test.create(context.Background(), Group{Name: test.name, Members: []Member{{Name: "member", Type: MemberHosted, Endpoint: "https://example.invalid", RepositoryID: "repository-id"}}})
			if err != nil || len(created.Members) != 1 || created.Members[0].RepositoryID != "repository-id" {
				t.Fatalf("created=%#v err=%v", created, err)
			}
			loaded, err := test.get(context.Background(), test.name)
			if err != nil || len(loaded.Members) != 1 || loaded.Members[0].RepositoryID != "repository-id" {
				t.Fatalf("loaded=%#v err=%v", loaded, err)
			}
		})
	}
}
