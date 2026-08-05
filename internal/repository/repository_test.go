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
	if err = store.UpdateReplicationCheckpointWithLease(ctx, checkpoint, claimed[0].LeaseToken); err != nil {
		t.Fatal(err)
	}
	if err = store.CompleteReplicationPlanWithLease(ctx, plan.ID, claimed[0].LeaseToken); err != nil {
		t.Fatal(err)
	}
	plans, err := store.ListReplicationPlans(ctx, "target", 10)
	if err != nil || len(plans) != 1 || plans[0].ID != plan.ID || plans[0].State != "completed" {
		t.Fatalf("plans=%#v err=%v", plans, err)
	}
	if got, err := store.GetReplicationPlan(ctx, "target", plan.ID); err != nil || got.ID != plan.ID {
		t.Fatalf("scoped plan=%#v err=%v", got, err)
	}
	if _, err := store.GetReplicationPlan(ctx, "other", plan.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unscoped plan err=%v", err)
	}
	if _, err = store.ClaimReplicationPlans(ctx, 1); err != nil {
		t.Fatal(err)
	}
}

func TestMemoryDisableHostedRepositoryIsIdempotentWhileDeleting(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	created, err := store.CreateHostedRepository(ctx, HostedRepository{
		ID:     "deleting-repository",
		Name:   "deleting-repository",
		Format: FormatOCI,
	})
	if err != nil {
		t.Fatal(err)
	}

	first, err := store.DisableHostedRepository(ctx, created.ID)
	if err != nil {
		t.Fatalf("first disable: %v", err)
	}
	if first.State != RepositoryDeleting {
		t.Fatalf("first disable state=%q, want deleting", first.State)
	}

	second, err := store.DisableHostedRepository(ctx, created.ID)
	if err != nil {
		t.Fatalf("repeated disable: %v", err)
	}
	if second.State != RepositoryDeleting || second.ID != created.ID {
		t.Fatalf("repeated disable=%#v, want the existing deleting repository", second)
	}
}

func TestMemoryReplicationLeaseRecoveryFencesStaleWorker(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	digest := "sha256:" + strings.Repeat("b", 64)
	plan := ReplicationPlan{ID: "lease-plan", SourceRepositoryID: "source", TargetRepositoryID: "target", Format: FormatRaw, IdempotencyKey: "lease"}
	if _, _, err := store.CreateReplicationPlan(ctx, plan, []ReplicationCheckpoint{{ObjectKey: "object", Digest: digest, Size: 1}}); err != nil {
		t.Fatal(err)
	}
	first, err := store.ClaimReplicationPlans(ctx, 1)
	if err != nil || len(first) != 1 || first[0].LeaseToken == "" {
		t.Fatalf("first claim=%#v err=%v", first, err)
	}
	oldToken := first[0].LeaseToken
	expired := store.replicationPlans[plan.ID]
	expired.LeaseExpiresAt = time.Now().UTC().Add(-time.Second)
	store.replicationPlans[plan.ID] = expired
	if err := store.CompleteReplicationPlanWithLease(ctx, plan.ID, oldToken); !errors.Is(err, ErrNotFound) {
		t.Fatalf("stale worker complete err=%v", err)
	}
	if err := store.FailReplicationPlanWithLease(ctx, plan.ID, "stale failure", oldToken); !errors.Is(err, ErrNotFound) {
		t.Fatalf("stale worker fail err=%v", err)
	}
	if recovered, err := store.RecoverExpiredReplicationPlans(ctx, time.Now().UTC()); err != nil || recovered != 1 {
		t.Fatalf("recovered=%d err=%v", recovered, err)
	}
	second, err := store.ClaimReplicationPlans(ctx, 1)
	if err != nil || len(second) != 1 || second[0].LeaseToken == oldToken || second[0].Attempts != 2 {
		t.Fatalf("second claim=%#v err=%v", second, err)
	}
	checkpoint := ReplicationCheckpoint{PlanID: plan.ID, ObjectKey: "object", Digest: digest, Size: 1, ByteOffset: 1, State: "verified"}
	if err := store.UpdateReplicationCheckpointWithLease(ctx, checkpoint, oldToken); !errors.Is(err, ErrNotFound) {
		t.Fatalf("stale worker update err=%v", err)
	}
	if err := store.UpdateReplicationCheckpointWithLease(ctx, checkpoint, second[0].LeaseToken); err != nil {
		t.Fatalf("current worker update err=%v", err)
	}
}

func TestMemoryReplicationPlanStopsAfterMaximumAttempts(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	plan := ReplicationPlan{ID: "bounded-retry-plan", SourceRepositoryID: "source", TargetRepositoryID: "target", Format: FormatRaw, IdempotencyKey: "bounded-retry", MaxAttempts: 2}
	checkpoint := ReplicationCheckpoint{ObjectKey: "object", Digest: "sha256:" + strings.Repeat("c", 64), Size: 1}
	if _, _, err := store.CreateReplicationPlan(ctx, plan, []ReplicationCheckpoint{checkpoint}); err != nil {
		t.Fatal(err)
	}
	for attempt := 1; attempt <= plan.MaxAttempts; attempt++ {
		claimed, err := store.ClaimReplicationPlans(ctx, 1)
		if err != nil || len(claimed) != 1 || claimed[0].Attempts != attempt {
			t.Fatalf("attempt %d claim=%#v err=%v", attempt, claimed, err)
		}
		if err = store.FailReplicationPlanWithLease(ctx, plan.ID, "temporary failure", claimed[0].LeaseToken); err != nil {
			t.Fatal(err)
		}
	}
	if claimed, err := store.ClaimReplicationPlans(ctx, 1); err != nil || len(claimed) != 0 {
		t.Fatalf("exhausted claim=%#v err=%v", claimed, err)
	}
}

func TestMemoryRepositoryCapacityUsesLogicalVisibleReferences(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	if _, err := store.CreateHostedRepository(ctx, HostedRepository{ID: "raw", Name: "capacity-raw", Format: FormatRaw}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PutRawAsset(ctx, RawAsset{RepositoryID: "raw", Path: "a", Digest: "sha256:a", ObjectKey: "a", Size: 4}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PutRawAsset(ctx, RawAsset{RepositoryID: "raw", Path: "b", Digest: "sha256:a", ObjectKey: "a", Size: 4}); err != nil {
		t.Fatal(err)
	}
	capacity, err := store.ReplaceRepositoryCapacityQuota(ctx, "raw", 10)
	if err != nil || capacity.UsedBytes != 8 || capacity.ObjectCount != 2 || capacity.QuotaBytes != 10 {
		t.Fatalf("capacity=%#v err=%v", capacity, err)
	}
}

func TestMemoryRepositoryCapacityExcludesTombstonedMavenAssets(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	if _, err := store.CreateHostedRepository(ctx, HostedRepository{ID: "maven", Name: "capacity-maven", Format: FormatMaven}); err != nil {
		t.Fatal(err)
	}
	session := MavenPublishSession{ID: "capacity-session", RepositoryID: "maven", Coordinate: "org.example:widget:1.0.0", Publisher: "alice", State: "open", ExpiresAt: time.Now().Add(time.Hour), Objects: []MavenDeclaredObject{{Name: "widget.jar", Digest: "sha256:widget", Size: 4}}}
	if _, err := store.CreateMavenPublishSession(ctx, session); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkMavenPublishObject(ctx, session.ID, "widget.jar", "native/maven/widget"); err != nil {
		t.Fatal(err)
	}
	artifact, err := store.CommitMavenPublishSession(ctx, session.ID, []MavenAsset{{RepositoryID: "maven", Path: "org/example/widget/1.0.0/widget.jar", ObjectKey: "native/maven/widget", Digest: "sha256:widget", Size: 4}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.TombstoneMavenArtifact(ctx, "maven", artifact.ID); err != nil {
		t.Fatal(err)
	}
	capacity, err := store.GetRepositoryCapacity(ctx, "maven")
	if err != nil || capacity.UsedBytes != 0 || capacity.ObjectCount != 0 {
		t.Fatalf("capacity=%#v err=%v", capacity, err)
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

func TestMemoryHostedRepositoryProxyFieldsRoundTrip(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	proxy := HostedRepository{
		ID:           "proxy-raw",
		Name:         "proxy-raw",
		Format:       FormatRaw,
		Type:         RepositoryTypeProxy,
		Endpoint:     "https://upstream.example",
		AllowedHosts: []string{"upstream.example", "cdn.example"},
	}
	created, err := store.CreateHostedRepository(ctx, proxy)
	if err != nil {
		t.Fatal(err)
	}
	if created.Type != RepositoryTypeProxy || created.Endpoint != proxy.Endpoint || len(created.AllowedHosts) != 2 {
		t.Fatalf("created=%#v", created)
	}
	loaded, err := store.GetHostedRepository(ctx, proxy.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Type != RepositoryTypeProxy || loaded.Endpoint != proxy.Endpoint || loaded.AllowedHosts[0] != "upstream.example" || loaded.AllowedHosts[1] != "cdn.example" {
		t.Fatalf("loaded=%#v", loaded)
	}
	byName, err := store.GetHostedRepositoryByName(ctx, proxy.Name)
	if err != nil || byName.Type != RepositoryTypeProxy || byName.Endpoint != proxy.Endpoint {
		t.Fatalf("byName=%#v err=%v", byName, err)
	}
	listed, _, err := store.ListHostedRepositories(ctx, 10, "")
	if err != nil || len(listed) != 1 || listed[0].Type != RepositoryTypeProxy || len(listed[0].AllowedHosts) != 2 {
		t.Fatalf("listed=%#v err=%v", listed, err)
	}
}

func TestMemoryHostedRepositoryDefaultsToHostedType(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	created, err := store.CreateHostedRepository(ctx, HostedRepository{ID: "hosted", Name: "hosted", Format: FormatRaw, Type: RepositoryTypeHosted})
	if err != nil {
		t.Fatal(err)
	}
	if created.Type != RepositoryTypeHosted || created.Endpoint != "" || len(created.AllowedHosts) != 0 {
		t.Fatalf("created=%#v", created)
	}
}

func TestMemoryMavenArtifactSearchCursorPreservesSnapshotBuilds(t *testing.T) {
	store := NewMemoryStore()
	coordinate := "org.example:demo:1.0-SNAPSHOT"
	for build := 1; build <= 3; build++ {
		artifact := MavenArtifact{ID: string(rune('a' + build)), RepositoryID: "repo", Coordinate: coordinate, BuildNumber: build, State: "visible"}
		store.mavenArtifacts[artifact.ID] = artifact
	}

	first, err := store.SearchMavenArtifacts(context.Background(), "repo", coordinate, 2, MavenArtifactCursor{})
	if err != nil || len(first) != 2 || first[0].BuildNumber != 1 || first[1].BuildNumber != 2 {
		t.Fatalf("first=%#v err=%v", first, err)
	}
	second, err := store.SearchMavenArtifacts(context.Background(), "repo", coordinate, 2, MavenArtifactCursor{Coordinate: first[1].Coordinate, BuildNumber: first[1].BuildNumber})
	if err != nil || len(second) != 1 || second[0].BuildNumber != 3 {
		t.Fatalf("second=%#v err=%v", second, err)
	}
}
