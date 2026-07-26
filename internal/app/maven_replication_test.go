package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

func TestMavenReplicationCopiesTargetOwnedAssetsAndMetadata(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	objects := NewMemoryOCIObjectStore()
	body := []byte("replicated Maven asset")
	digest := mavenReplicationDigest(body)
	sourceKey := "native/maven/sha256/" + digest[len("sha256:"):]
	targetKey := mavenReplicationTargetObjectKey("target", digest)
	coordinate := "org.example:widget:1.0.0"
	path := "org/example/widget/1.0.0/widget-1.0.0.jar"
	if err := objects.PutVerifiedReader(ctx, sourceKey, bytes.NewReader(body), int64(len(body)), digest); err != nil {
		t.Fatal(err)
	}
	session := repository.MavenPublishSession{ID: "source-artifact", RepositoryID: "source", Coordinate: coordinate, Publisher: "test", PomObject: "widget-1.0.0.jar", State: "open", ExpiresAt: time.Now().Add(time.Hour), Objects: []repository.MavenDeclaredObject{{Name: "widget-1.0.0.jar", Digest: digest, Size: int64(len(body))}}}
	if _, err := store.CreateMavenPublishSession(ctx, session); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkMavenPublishObject(ctx, session.ID, session.Objects[0].Name, sourceKey); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CommitMavenPublishSession(ctx, session.ID, []repository.MavenAsset{{RepositoryID: "source", Path: path, ObjectKey: sourceKey, Digest: digest, Size: int64(len(body))}}); err != nil {
		t.Fatal(err)
	}
	plan := repository.ReplicationPlan{ID: "maven-replication", SourceRepositoryID: "source", TargetRepositoryID: "target", Format: repository.FormatMaven, IdempotencyKey: "copy"}
	if _, _, err := store.CreateReplicationPlan(ctx, plan, []repository.ReplicationCheckpoint{{SourceObjectKey: sourceKey, ObjectKey: targetKey, Digest: digest, Size: int64(len(body))}}); err != nil {
		t.Fatal(err)
	}
	if err := (MavenReplication{Store: store, Source: objects, Destination: objects, ChunkBytes: 3}).RunJobs(ctx, 1); err != nil {
		t.Fatal(err)
	}
	asset, err := store.GetMavenAsset(ctx, "target", path)
	if err != nil || asset.ObjectKey != targetKey || asset.Digest != digest || asset.Size != int64(len(body)) {
		t.Fatalf("asset=%#v err=%v", asset, err)
	}
	if got, err := objects.Get(ctx, targetKey); err != nil || !bytes.Equal(got, body) {
		t.Fatalf("target bytes=%q err=%v", got, err)
	}
}

func TestMavenReplicationRejectsTombstonedSourceAndExistingTarget(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	objects := NewMemoryOCIObjectStore()
	body := []byte("source visibility")
	digest := mavenReplicationDigest(body)
	sourceKey, targetKey := "source", "target"
	if err := objects.PutVerifiedReader(ctx, sourceKey, bytes.NewReader(body), int64(len(body)), digest); err != nil {
		t.Fatal(err)
	}
	session := repository.MavenPublishSession{ID: "source-artifact", RepositoryID: "source", Coordinate: "org.example:widget:1.0.0", Publisher: "test", PomObject: "widget.jar", State: "open", ExpiresAt: time.Now().Add(time.Hour), Objects: []repository.MavenDeclaredObject{{Name: "widget.jar", Digest: digest, Size: int64(len(body))}}}
	if _, err := store.CreateMavenPublishSession(ctx, session); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkMavenPublishObject(ctx, session.ID, "widget.jar", sourceKey); err != nil {
		t.Fatal(err)
	}
	artifact, err := store.CommitMavenPublishSession(ctx, session.ID, []repository.MavenAsset{{RepositoryID: "source", Path: "org/example/widget/1.0.0/widget.jar", ObjectKey: sourceKey, Digest: digest, Size: int64(len(body))}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.TombstoneMavenArtifact(ctx, "source", artifact.ID); err != nil {
		t.Fatal(err)
	}
	err = (MavenReplication{Store: store, Source: objects, Destination: objects}).publish(ctx, repository.ReplicationPlan{ID: "plan", SourceRepositoryID: "source", TargetRepositoryID: "target", Format: repository.FormatMaven}, []repository.ReplicationCheckpoint{{SourceObjectKey: sourceKey, ObjectKey: targetKey, Digest: digest, Size: int64(len(body)), State: "verified"}})
	if err == nil {
		t.Fatalf("err=%v", err)
	}
}

func TestMavenReplicationSharesTargetObjectForPathsWithTheSameDigest(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	objects := NewMemoryOCIObjectStore()
	body := []byte("shared Maven asset")
	digest := mavenReplicationDigest(body)
	coordinate := "org.example:shared:1.0.0"
	first, second := "source-first", "source-second"
	for _, key := range []string{first, second} {
		if err := objects.PutVerifiedReader(ctx, key, bytes.NewReader(body), int64(len(body)), digest); err != nil {
			t.Fatal(err)
		}
	}
	session := repository.MavenPublishSession{ID: "shared-source", RepositoryID: "source", Coordinate: coordinate, Publisher: "test", PomObject: "shared.pom", State: "open", ExpiresAt: time.Now().Add(time.Hour), Objects: []repository.MavenDeclaredObject{{Name: "shared.pom", Digest: digest, Size: int64(len(body))}, {Name: "shared.jar", Digest: digest, Size: int64(len(body))}}}
	if _, err := store.CreateMavenPublishSession(ctx, session); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkMavenPublishObject(ctx, session.ID, "shared.pom", first); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkMavenPublishObject(ctx, session.ID, "shared.jar", second); err != nil {
		t.Fatal(err)
	}
	assets := []repository.MavenAsset{{RepositoryID: "source", Path: "org/example/shared/1.0.0/shared.pom", ObjectKey: first, Digest: digest, Size: int64(len(body))}, {RepositoryID: "source", Path: "org/example/shared/1.0.0/shared.jar", ObjectKey: second, Digest: digest, Size: int64(len(body))}}
	if _, err := store.CommitMavenPublishSession(ctx, session.ID, assets); err != nil {
		t.Fatal(err)
	}
	targetKey := mavenReplicationTargetObjectKey("target", digest)
	plan := repository.ReplicationPlan{ID: "shared-replication", SourceRepositoryID: "source", TargetRepositoryID: "target", Format: repository.FormatMaven, IdempotencyKey: "shared"}
	if _, _, err := store.CreateReplicationPlan(ctx, plan, []repository.ReplicationCheckpoint{{SourceObjectKey: first, ObjectKey: targetKey, Digest: digest, Size: int64(len(body))}}); err != nil {
		t.Fatal(err)
	}
	if err := (MavenReplication{Store: store, Source: objects, Destination: objects}).RunJobs(ctx, 1); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"org/example/shared/1.0.0/shared.pom", "org/example/shared/1.0.0/shared.jar"} {
		asset, err := store.GetMavenAsset(ctx, "target", path)
		if err != nil || asset.ObjectKey != targetKey {
			t.Fatalf("asset %s = %#v, %v", path, asset, err)
		}
	}
}

func mavenReplicationDigest(body []byte) string {
	sum := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(sum[:])
}
