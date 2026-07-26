//go:build integration

package app

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

func TestPostgresRepositoryCapacityAcrossHostedFormats(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is required for PostgreSQL integration tests")
	}
	ctx := context.Background()
	store, err := repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	create := func(format repository.Format) repository.HostedRepository {
		t.Helper()
		repo, createErr := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "capacity-" + string(format) + "-" + uuid.NewString(), Format: format})
		if createErr != nil {
			t.Fatal(createErr)
		}
		return repo
	}
	assertCapacity := func(repo repository.HostedRepository, bytes, objects int64) {
		t.Helper()
		capacity, capacityErr := store.GetRepositoryCapacity(ctx, repo.ID)
		if capacityErr != nil || capacity.Format != repo.Format || capacity.UsedBytes != bytes || capacity.ObjectCount != objects {
			t.Fatalf("repository=%s capacity=%#v err=%v", repo.Format, capacity, capacityErr)
		}
	}

	raw := create(repository.FormatRaw)
	if _, err = store.PutRawAsset(ctx, repository.RawAsset{RepositoryID: raw.ID, Path: "widget", Digest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", ObjectKey: "native/raw/capacity-" + uuid.NewString(), Size: 3}); err != nil {
		t.Fatal(err)
	}
	assertCapacity(raw, 3, 1)
	capacity, err := store.ReplaceRepositoryCapacityQuota(ctx, raw.ID, 9)
	if err != nil || capacity.QuotaBytes != 9 || capacity.UsedBytes != 3 {
		t.Fatalf("raw quota capacity=%#v err=%v", capacity, err)
	}

	maven := create(repository.FormatMaven)
	mavenKey := "native/maven/capacity-" + uuid.NewString()
	session := repository.MavenPublishSession{ID: uuid.NewString(), RepositoryID: maven.ID, Coordinate: "org.example:widget:1.0.0", Publisher: "capacity", State: "open", ExpiresAt: time.Now().Add(time.Hour), Objects: []repository.MavenDeclaredObject{{Name: "widget.jar", Digest: "sha256:maven-" + uuid.NewString(), Size: 4}}}
	if _, err = store.CreateMavenPublishSession(ctx, session); err != nil {
		t.Fatal(err)
	}
	if err = store.MarkMavenPublishObject(ctx, session.ID, "widget.jar", mavenKey); err != nil {
		t.Fatal(err)
	}
	if _, err = store.CommitMavenPublishSession(ctx, session.ID, []repository.MavenAsset{{RepositoryID: maven.ID, Path: "org/example/widget/1.0.0/widget.jar", ObjectKey: mavenKey, Digest: session.Objects[0].Digest, Size: 4}}); err != nil {
		t.Fatal(err)
	}
	assertCapacity(maven, 4, 1)

	oci := create(repository.FormatOCI)
	blobDigest := "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	upload, err := store.CreateOCIUpload(ctx, repository.OCIUpload{ID: uuid.NewString(), RepositoryID: oci.ID, Name: "widget", ObjectKey: "native/oci/uploads/" + uuid.NewString(), State: "open", ExpiresAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.CompleteOCIUpload(ctx, upload.ID, repository.OCIBlob{Digest: blobDigest, ObjectKey: "native/oci/blobs/" + uuid.NewString(), Size: 6}); err != nil {
		t.Fatal(err)
	}
	manifestKey := "native/oci/manifests/" + uuid.NewString()
	manifestDigest := "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
	if err = store.StageOCIObjectIntent(ctx, repository.OCIObjectIntent{RepositoryID: oci.ID, ObjectKey: manifestKey, Digest: manifestDigest, Size: 5}); err != nil {
		t.Fatal(err)
	}
	if _, err = store.PutOCIManifest(ctx, repository.OCIManifest{RepositoryID: oci.ID, Name: "widget", Digest: manifestDigest, ObjectKey: manifestKey, MediaType: "application/vnd.oci.image.manifest.v1+json", Size: 5}, "latest"); err != nil {
		t.Fatal(err)
	}
	assertCapacity(oci, 11, 2)

	conan := create(repository.FormatConan)
	conanKey := "native/conan/objects/" + uuid.NewString()
	conanDigest := "sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
	if err = store.StageConanObject(ctx, repository.ConanObjectIntent{RepositoryID: conan.ID, ObjectKey: conanKey, Digest: conanDigest, Size: 7}); err != nil {
		t.Fatal(err)
	}
	if _, err = store.PutConanRecipeRevision(ctx, repository.ConanRecipeRevision{RepositoryID: conan.ID, Reference: "pkg/1.0/user/stable", Revision: "rrev", Digest: conanDigest}, []repository.ConanAsset{{RepositoryID: conan.ID, Reference: "pkg/1.0/user/stable", RecipeRevision: "rrev", Path: "conanfile.py", ObjectKey: conanKey, Digest: conanDigest, Size: 7}}); err != nil {
		t.Fatal(err)
	}
	assertCapacity(conan, 7, 1)
}

func TestPostgresRepositoryCapacityRejectsVisibleWritesAcrossFormats(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is required for PostgreSQL integration tests")
	}
	ctx := context.Background()
	store, err := repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	create := func(format repository.Format) repository.HostedRepository {
		t.Helper()
		repo, createErr := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "quota-" + string(format) + "-" + uuid.NewString(), Format: format})
		if createErr != nil {
			t.Fatal(createErr)
		}
		if _, createErr = store.ReplaceRepositoryCapacityQuota(ctx, repo.ID, 1); createErr != nil {
			t.Fatal(createErr)
		}
		return repo
	}

	raw := create(repository.FormatRaw)
	if _, err = store.PutRawAsset(ctx, repository.RawAsset{RepositoryID: raw.ID, Path: "widget", Digest: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", ObjectKey: "native/raw/quota-" + uuid.NewString(), Size: 2}); !repository.IsQuotaExceeded(err) {
		t.Fatalf("raw quota err=%v", err)
	}

	maven := create(repository.FormatMaven)
	mavenSession := repository.MavenPublishSession{ID: uuid.NewString(), RepositoryID: maven.ID, Coordinate: "org.example:quota:1.0.0", Publisher: "quota", State: "open", ExpiresAt: time.Now().Add(time.Hour), Objects: []repository.MavenDeclaredObject{{Name: "quota.jar", Digest: "sha256:maven-" + uuid.NewString(), Size: 2}}}
	if _, err = store.CreateMavenPublishSession(ctx, mavenSession); err != nil {
		t.Fatal(err)
	}
	mavenKey := "native/maven/quota-" + uuid.NewString()
	if err = store.MarkMavenPublishObject(ctx, mavenSession.ID, "quota.jar", mavenKey); err != nil {
		t.Fatal(err)
	}
	if _, err = store.CommitMavenPublishSession(ctx, mavenSession.ID, []repository.MavenAsset{{RepositoryID: maven.ID, Path: "org/example/quota/1.0.0/quota.jar", ObjectKey: mavenKey, Digest: mavenSession.Objects[0].Digest, Size: 2}}); !repository.IsQuotaExceeded(err) {
		t.Fatalf("maven quota err=%v", err)
	}

	oci := create(repository.FormatOCI)
	upload, err := store.CreateOCIUpload(ctx, repository.OCIUpload{ID: uuid.NewString(), RepositoryID: oci.ID, Name: "quota", ObjectKey: "native/oci/uploads/" + uuid.NewString(), State: "open", ExpiresAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.CompleteOCIUpload(ctx, upload.ID, repository.OCIBlob{Digest: "sha256:eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee", ObjectKey: "native/oci/blobs/" + uuid.NewString(), Size: 2}); !repository.IsQuotaExceeded(err) {
		t.Fatalf("oci quota err=%v", err)
	}

	conan := create(repository.FormatConan)
	conanKey := "native/conan/quota-" + uuid.NewString()
	conanDigest := "sha256:9999999999999999999999999999999999999999999999999999999999999999"
	if err = store.StageConanObject(ctx, repository.ConanObjectIntent{RepositoryID: conan.ID, ObjectKey: conanKey, Digest: conanDigest, Size: 2}); err != nil {
		t.Fatal(err)
	}
	if _, err = store.PutConanRecipeRevision(ctx, repository.ConanRecipeRevision{RepositoryID: conan.ID, Reference: "pkg/1.0/user/stable", Revision: "rrev", Digest: conanDigest}, []repository.ConanAsset{{RepositoryID: conan.ID, Reference: "pkg/1.0/user/stable", RecipeRevision: "rrev", Path: "conanfile.py", ObjectKey: conanKey, Digest: conanDigest, Size: 2}}); !repository.IsQuotaExceeded(err) {
		t.Fatalf("conan quota err=%v", err)
	}
}
