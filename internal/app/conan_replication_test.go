package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

func TestConanReplicationCopiesRecipeAndPackageToTargetOwnedKeys(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	objects := NewMemoryOCIObjectStore()
	reference, revision := "widget/1.0/user/stable", "rrev"
	recipeBody, packageBody := []byte("recipe"), []byte("package")
	recipeDigest, packageDigest := conanReplicationDigest(recipeBody), conanReplicationDigest(packageBody)
	recipeKey, packageKey := "native/conan/source/recipe", "native/conan/source/package"
	for _, item := range []struct {
		key    string
		body   []byte
		digest string
	}{{recipeKey, recipeBody, recipeDigest}, {packageKey, packageBody, packageDigest}} {
		if err := objects.PutVerifiedReader(ctx, item.key, bytes.NewReader(item.body), int64(len(item.body)), item.digest); err != nil {
			t.Fatal(err)
		}
		if err := store.StageConanObject(ctx, repository.ConanObjectIntent{RepositoryID: "source", ObjectKey: item.key, Digest: item.digest, Size: int64(len(item.body))}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.PutConanRecipeRevision(ctx, repository.ConanRecipeRevision{RepositoryID: "source", Reference: reference, Revision: revision, Digest: recipeDigest}, []repository.ConanAsset{{RepositoryID: "source", Reference: reference, RecipeRevision: revision, Path: "conanfile.py", ObjectKey: recipeKey, Digest: recipeDigest, Size: int64(len(recipeBody))}}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PutConanPackageRevision(ctx, repository.ConanPackageRevision{RepositoryID: "source", Reference: reference, RecipeRevision: revision, PackageID: "package-id", Revision: "prev", Digest: packageDigest}, []repository.ConanAsset{{RepositoryID: "source", Reference: reference, RecipeRevision: revision, PackageID: "package-id", PackageRevision: "prev", Path: "package.tgz", ObjectKey: packageKey, Digest: packageDigest, Size: int64(len(packageBody))}}); err != nil {
		t.Fatal(err)
	}
	checks, err := conanReplicationCheckpoints(ctx, store, "source", "target", reference, revision)
	if err != nil || len(checks) != 2 || checks[0].SourceObjectKey == checks[0].ObjectKey {
		t.Fatalf("checkpoints=%#v err=%v", checks, err)
	}
	plan := repository.ReplicationPlan{ID: "conan-copy", SourceRepositoryID: "source", TargetRepositoryID: "target", Format: repository.FormatConan, Coordinate: reference + "#" + revision, Digest: recipeDigest, IdempotencyKey: "copy"}
	if _, _, err = store.CreateReplicationPlan(ctx, plan, checks); err != nil {
		t.Fatal(err)
	}
	if err = (ConanReplication{Store: store, Source: objects, Destination: objects, ChunkBytes: 2}).RunJobs(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if target, err := store.GetConanRecipeRevision(ctx, "target", reference, revision); err != nil || target.Digest != recipeDigest || target.State != "visible" {
		t.Fatalf("target recipe=%#v err=%v", target, err)
	}
	asset, err := store.GetConanPackageAsset(ctx, "target", reference, revision, "package-id", "prev", "package.tgz")
	if err != nil || asset.RepositoryID != "target" || asset.ObjectKey != conanReplicationTargetObjectKey("target", packageKey) {
		t.Fatalf("target asset=%#v err=%v", asset, err)
	}
	if got, err := objects.Get(ctx, asset.ObjectKey); err != nil || !bytes.Equal(got, packageBody) {
		t.Fatalf("target bytes=%q err=%v", got, err)
	}
}

func TestConanReplicationRejectsTombstonedSource(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	objects := NewMemoryOCIObjectStore()
	body := []byte("source visibility")
	digest := conanReplicationDigest(body)
	reference, revision, key := "widget/1.0/user/stable", "rrev", "native/conan/source/recipe"
	if err := objects.PutVerifiedReader(ctx, key, bytes.NewReader(body), int64(len(body)), digest); err != nil {
		t.Fatal(err)
	}
	if err := store.StageConanObject(ctx, repository.ConanObjectIntent{RepositoryID: "source", ObjectKey: key, Digest: digest, Size: int64(len(body))}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PutConanRecipeRevision(ctx, repository.ConanRecipeRevision{RepositoryID: "source", Reference: reference, Revision: revision, Digest: digest}, []repository.ConanAsset{{RepositoryID: "source", Reference: reference, RecipeRevision: revision, Path: "conanfile.py", ObjectKey: key, Digest: digest, Size: int64(len(body))}}); err != nil {
		t.Fatal(err)
	}
	checks, err := conanReplicationCheckpoints(ctx, store, "source", "target", reference, revision)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.TombstoneConanRecipeRevision(ctx, "source", reference, revision); err != nil {
		t.Fatal(err)
	}
	if err = (ConanReplication{Store: store, Source: objects, Destination: objects}).publish(ctx, repository.ReplicationPlan{ID: "plan", SourceRepositoryID: "source", TargetRepositoryID: "target", Format: repository.FormatConan}, checks); err == nil {
		t.Fatal("expected source visibility rejection")
	}
}

func TestConanReplicationDoesNotPublishRecipeWhenAnyTargetObjectIsUnavailable(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	objects := NewMemoryOCIObjectStore()
	reference, revision := "widget/1.0/user/stable", "rrev"
	recipeBody, packageBody := []byte("recipe"), []byte("package")
	recipeDigest, packageDigest := conanReplicationDigest(recipeBody), conanReplicationDigest(packageBody)
	for _, item := range []struct {
		key    string
		body   []byte
		digest string
	}{{"source-recipe", recipeBody, recipeDigest}, {"source-package", packageBody, packageDigest}} {
		if err := objects.PutVerifiedReader(ctx, item.key, bytes.NewReader(item.body), int64(len(item.body)), item.digest); err != nil {
			t.Fatal(err)
		}
		if err := store.StageConanObject(ctx, repository.ConanObjectIntent{RepositoryID: "source", ObjectKey: item.key, Digest: item.digest, Size: int64(len(item.body))}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.PutConanRecipeRevision(ctx, repository.ConanRecipeRevision{RepositoryID: "source", Reference: reference, Revision: revision, Digest: recipeDigest}, []repository.ConanAsset{{RepositoryID: "source", Reference: reference, RecipeRevision: revision, Path: "conanfile.py", ObjectKey: "source-recipe", Digest: recipeDigest, Size: int64(len(recipeBody))}}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PutConanPackageRevision(ctx, repository.ConanPackageRevision{RepositoryID: "source", Reference: reference, RecipeRevision: revision, PackageID: "id", Revision: "prev", Digest: packageDigest}, []repository.ConanAsset{{RepositoryID: "source", Reference: reference, RecipeRevision: revision, PackageID: "id", PackageRevision: "prev", Path: "package.tgz", ObjectKey: "source-package", Digest: packageDigest, Size: int64(len(packageBody))}}); err != nil {
		t.Fatal(err)
	}
	checks, err := conanReplicationCheckpoints(ctx, store, "source", "target", reference, revision)
	if err != nil {
		t.Fatal(err)
	}
	// Claim one target key through another visible recipe. The replication must
	// leave no target recipe behind when its atomic publication cannot claim it.
	blocked := checks[1]
	if err := store.StageConanObject(ctx, repository.ConanObjectIntent{RepositoryID: "target", ObjectKey: blocked.ObjectKey, Digest: blocked.Digest, Size: blocked.Size}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PutConanRecipeRevision(ctx, repository.ConanRecipeRevision{RepositoryID: "target", Reference: "other/1.0/user/stable", Revision: "rrev", Digest: blocked.Digest}, []repository.ConanAsset{{RepositoryID: "target", Reference: "other/1.0/user/stable", RecipeRevision: "rrev", Path: "blocked", ObjectKey: blocked.ObjectKey, Digest: blocked.Digest, Size: blocked.Size}}); err != nil {
		t.Fatal(err)
	}
	err = (ConanReplication{Store: store, Source: objects, Destination: objects}).publish(ctx, repository.ReplicationPlan{ID: "plan", SourceRepositoryID: "source", TargetRepositoryID: "target", Format: repository.FormatConan}, checks)
	if err == nil {
		t.Fatal("expected target object conflict")
	}
	if _, err := store.GetConanRecipeRevision(ctx, "target", reference, revision); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("partial target recipe err=%v", err)
	}
}

func conanReplicationDigest(body []byte) string {
	sum := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(sum[:])
}
