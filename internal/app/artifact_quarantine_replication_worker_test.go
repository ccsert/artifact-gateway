package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

func TestMavenReplicationWorkerParksArtifactQuarantinedAfterPlanCreation(t *testing.T) {
	ctx := context.Background()
	store, objects := repository.NewMemoryStore(), NewMemoryOCIObjectStore()
	body := []byte("quarantined Maven replication")
	digest := quarantineReplicationDigest(body)
	coordinate := "org.example:quarantined:1.0.0"
	path := "org/example/quarantined/1.0.0/quarantined-1.0.0.jar"
	sourceKey := "native/maven/source/quarantined"
	targetKey := mavenReplicationTargetObjectKey("target", digest)
	if err := objects.PutVerifiedReader(ctx, sourceKey, bytes.NewReader(body), int64(len(body)), digest); err != nil {
		t.Fatal(err)
	}
	session := repository.MavenPublishSession{
		ID: "maven-quarantine-source", RepositoryID: "source", Coordinate: coordinate,
		Publisher: "test", State: "open", ExpiresAt: time.Now().Add(time.Hour),
		Objects: []repository.MavenDeclaredObject{{Name: "quarantined-1.0.0.jar", Digest: digest, Size: int64(len(body))}},
	}
	if _, err := store.CreateMavenPublishSession(ctx, session); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkMavenPublishObject(ctx, session.ID, session.Objects[0].Name, sourceKey); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CommitMavenPublishSession(ctx, session.ID, []repository.MavenAsset{{
		RepositoryID: "source", Path: path, ObjectKey: sourceKey, Digest: digest, Size: int64(len(body)),
	}}); err != nil {
		t.Fatal(err)
	}
	plan := repository.ReplicationPlan{
		ID: "maven-quarantine-plan", SourceRepositoryID: "source", TargetRepositoryID: "target",
		Format: repository.FormatMaven, Coordinate: coordinate, Digest: digest, IdempotencyKey: "maven-quarantine",
	}
	if _, _, err := store.CreateReplicationPlan(ctx, plan, []repository.ReplicationCheckpoint{{
		SourceObjectKey: sourceKey, ObjectKey: targetKey, Digest: digest, Size: int64(len(body)),
	}}); err != nil {
		t.Fatal(err)
	}
	destination := &quarantineBeforeReplicationPublishStore{
		OCIObjectStore: objects, Repository: store, Plan: plan, Reason: "block queued Maven replication",
	}

	if err := (MavenReplication{Store: store, Source: objects, Destination: destination}).RunJobs(ctx, 1); err != nil {
		t.Fatalf("run Maven replication worker: %v", err)
	}
	requireQuarantineBeforeReplicationPublish(t, destination)
	if _, err := store.GetMavenAsset(ctx, "target", path); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("target Maven artifact should remain unpublished, err=%v", err)
	}
	requireQuarantineReplicationPlanParked(t, ctx, store, plan)
}

func TestOCIReplicationWorkerParksArtifactQuarantinedAfterPlanCreation(t *testing.T) {
	ctx := context.Background()
	store, objects := repository.NewMemoryStore(), NewMemoryOCIObjectStore()
	body := []byte(`{"schemaVersion":2}`)
	digest := quarantineReplicationDigest(body)
	name, sourceKey := "team/quarantined", "native/oci/source/quarantined"
	targetKey := ociReplicationTargetObjectKey("target", name, digest)
	if err := objects.PutVerifiedReader(ctx, sourceKey, bytes.NewReader(body), int64(len(body)), digest); err != nil {
		t.Fatal(err)
	}
	if err := store.StageOCIObjectIntent(ctx, repository.OCIObjectIntent{
		RepositoryID: "source", ObjectKey: sourceKey, Digest: digest, Size: int64(len(body)),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PutOCIManifest(ctx, repository.OCIManifest{
		RepositoryID: "source", Name: name, Digest: digest, ObjectKey: sourceKey, Size: int64(len(body)),
	}, digest); err != nil {
		t.Fatal(err)
	}
	plan := repository.ReplicationPlan{
		ID: "oci-quarantine-plan", SourceRepositoryID: "source", TargetRepositoryID: "target",
		Format: repository.FormatOCI, Coordinate: name, Digest: digest, IdempotencyKey: "oci-quarantine",
	}
	if _, _, err := store.CreateReplicationPlan(ctx, plan, []repository.ReplicationCheckpoint{{
		SourceObjectKey: sourceKey, ObjectKey: targetKey, Digest: digest, Size: int64(len(body)),
	}}); err != nil {
		t.Fatal(err)
	}
	destination := &quarantineBeforeReplicationPublishStore{
		OCIObjectStore: objects, Repository: store, Plan: plan, Reason: "block queued OCI replication",
	}

	if err := (OCIReplication{Store: store, Source: objects, Destination: destination}).RunJobs(ctx, 1); err != nil {
		t.Fatalf("run OCI replication worker: %v", err)
	}
	requireQuarantineBeforeReplicationPublish(t, destination)
	if _, err := store.GetOCIManifest(ctx, "target", name, digest); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("target OCI manifest should remain unpublished, err=%v", err)
	}
	requireQuarantineReplicationPlanParked(t, ctx, store, plan)
}

func TestNPMReplicationWorkerParksArtifactQuarantinedAfterPlanCreation(t *testing.T) {
	ctx := context.Background()
	store, objects := repository.NewMemoryStore(), NewMemoryOCIObjectStore()
	body := []byte("quarantined npm replication")
	digest := quarantineReplicationDigest(body)
	packageName, version := "quarantined-widget", "1.0.0"
	coordinate := packageName + "@" + version
	sourceKey := "native/npm/source/quarantined"
	targetKey := npmReplicationTargetObjectKey("target", digest)
	if err := objects.PutVerifiedReader(ctx, sourceKey, bytes.NewReader(body), int64(len(body)), digest); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PublishNPMVersion(ctx, repository.NPMVersion{
		RepositoryID: "source", PackageName: packageName, Version: version,
		Digest: digest, ObjectKey: sourceKey, Size: int64(len(body)),
	}, map[string]string{"latest": version}); err != nil {
		t.Fatal(err)
	}
	plan := repository.ReplicationPlan{
		ID: "npm-quarantine-plan", SourceRepositoryID: "source", TargetRepositoryID: "target",
		Format: repository.FormatNPM, Coordinate: coordinate, Digest: digest, IdempotencyKey: "npm-quarantine",
	}
	if _, _, err := store.CreateReplicationPlan(ctx, plan, []repository.ReplicationCheckpoint{{
		SourceObjectKey: sourceKey, ObjectKey: targetKey, Digest: digest, Size: int64(len(body)),
	}}); err != nil {
		t.Fatal(err)
	}
	destination := &quarantineBeforeReplicationPublishStore{
		OCIObjectStore: objects, Repository: store, Plan: plan, Reason: "block queued npm replication",
	}

	if err := (NPMReplication{Store: store, Source: objects, Destination: destination}).RunJobs(ctx, 1); err != nil {
		t.Fatalf("run npm replication worker: %v", err)
	}
	requireQuarantineBeforeReplicationPublish(t, destination)
	if _, err := store.GetNPMVersion(ctx, "target", packageName, version); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("target npm version should remain unpublished, err=%v", err)
	}
	requireQuarantineReplicationPlanParked(t, ctx, store, plan)
}

func TestConanReplicationWorkerParksRecipeClosureQuarantinedAfterPlanCreation(t *testing.T) {
	ctx := context.Background()
	store, objects := repository.NewMemoryStore(), NewMemoryOCIObjectStore()
	reference, revision := "quarantined/1.0/user/stable", "rrev"
	recipeBody, packageBody := []byte("quarantined recipe"), []byte("quarantined package")
	recipeDigest, packageDigest := quarantineReplicationDigest(recipeBody), quarantineReplicationDigest(packageBody)
	recipeKey, packageKey := "native/conan/source/quarantined-recipe", "native/conan/source/quarantined-package"
	for _, item := range []struct {
		key    string
		body   []byte
		digest string
	}{{recipeKey, recipeBody, recipeDigest}, {packageKey, packageBody, packageDigest}} {
		if err := objects.PutVerifiedReader(ctx, item.key, bytes.NewReader(item.body), int64(len(item.body)), item.digest); err != nil {
			t.Fatal(err)
		}
		if err := store.StageConanObject(ctx, repository.ConanObjectIntent{
			RepositoryID: "source", ObjectKey: item.key, Digest: item.digest, Size: int64(len(item.body)),
		}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.PutConanRecipeRevision(ctx, repository.ConanRecipeRevision{
		RepositoryID: "source", Reference: reference, Revision: revision, Digest: recipeDigest,
	}, []repository.ConanAsset{{
		RepositoryID: "source", Reference: reference, RecipeRevision: revision,
		Path: "conanfile.py", ObjectKey: recipeKey, Digest: recipeDigest, Size: int64(len(recipeBody)),
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PutConanPackageRevision(ctx, repository.ConanPackageRevision{
		RepositoryID: "source", Reference: reference, RecipeRevision: revision,
		PackageID: "package-id", Revision: "prev", Digest: packageDigest,
	}, []repository.ConanAsset{{
		RepositoryID: "source", Reference: reference, RecipeRevision: revision,
		PackageID: "package-id", PackageRevision: "prev", Path: "package.tgz",
		ObjectKey: packageKey, Digest: packageDigest, Size: int64(len(packageBody)),
	}}); err != nil {
		t.Fatal(err)
	}
	checks, err := conanReplicationCheckpoints(ctx, store, "source", "target", reference, revision)
	if err != nil {
		t.Fatal(err)
	}
	plan := repository.ReplicationPlan{
		ID: "conan-quarantine-plan", SourceRepositoryID: "source", TargetRepositoryID: "target",
		Format: repository.FormatConan, Coordinate: reference + "#" + revision,
		Digest: recipeDigest, IdempotencyKey: "conan-quarantine",
	}
	if _, _, err = store.CreateReplicationPlan(ctx, plan, checks); err != nil {
		t.Fatal(err)
	}
	destination := &quarantineBeforeReplicationPublishStore{
		OCIObjectStore: objects, Repository: store, Plan: plan, Reason: "block queued Conan recipe closure",
	}

	if err = (ConanReplication{Store: store, Source: objects, Destination: destination}).RunJobs(ctx, 1); err != nil {
		t.Fatalf("run Conan replication worker: %v", err)
	}
	requireQuarantineBeforeReplicationPublish(t, destination)
	if _, err = store.GetConanRecipeRevision(ctx, "target", reference, revision); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("target Conan recipe should remain unpublished, err=%v", err)
	}
	if _, err = store.GetConanPackageRevision(ctx, "target", reference, revision, "package-id", "prev"); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("target Conan package closure should remain unpublished, err=%v", err)
	}
	requireQuarantineReplicationPlanParked(t, ctx, store, plan)
}

type quarantineBeforeReplicationPublishStore struct {
	OCIObjectStore
	Repository *repository.MemoryStore
	Plan       repository.ReplicationPlan
	Reason     string
	Triggered  bool
	Err        error
}

func (s *quarantineBeforeReplicationPublishStore) SetVerifiedDigest(ctx context.Context, key, digest string) error {
	if err := s.OCIObjectStore.SetVerifiedDigest(ctx, key, digest); err != nil {
		return err
	}
	if s.Triggered {
		return nil
	}
	s.Triggered = true
	_, s.Err = s.Repository.ReplaceArtifactQuarantine(ctx, repository.ArtifactQuarantine{
		RepositoryID: s.Plan.SourceRepositoryID, Format: s.Plan.Format,
		Coordinate: s.Plan.Coordinate, Digest: s.Plan.Digest,
		State: repository.ArtifactQuarantineStateQuarantined, Reason: s.Reason, UpdatedBy: "security-admin",
	}, "0")
	return nil
}

func requireQuarantineBeforeReplicationPublish(t *testing.T, destination *quarantineBeforeReplicationPublishStore) {
	t.Helper()
	if !destination.Triggered || destination.Err != nil {
		t.Fatalf("quarantine transition before final publication triggered=%t err=%v", destination.Triggered, destination.Err)
	}
}

func requireQuarantineReplicationPlanParked(t *testing.T, ctx context.Context, store *repository.MemoryStore, plan repository.ReplicationPlan) {
	t.Helper()
	persisted, err := store.GetReplicationPlan(ctx, plan.TargetRepositoryID, plan.ID)
	if err != nil || persisted.State != "failed" || persisted.LastError != repository.ArtifactQuarantinedReason || !persisted.NextAttemptAt.IsZero() || persisted.Attempts != 0 {
		t.Fatalf("replication plan should be parked, plan=%#v err=%v", persisted, err)
	}
}

func quarantineReplicationDigest(body []byte) string {
	sum := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(sum[:])
}
