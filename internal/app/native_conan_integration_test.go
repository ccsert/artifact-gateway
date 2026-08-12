//go:build integration

package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	conanprotocol "github.com/artifact-gateway/artifact-gateway/internal/protocol/conan"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

func TestPostgresRustFSConanPromotionSharesVisibleRevision(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	s3Endpoint := os.Getenv("TEST_S3_ENDPOINT")
	accessKey := os.Getenv("TEST_S3_ACCESS_KEY")
	secretKey := os.Getenv("TEST_S3_SECRET_KEY")
	if databaseURL == "" || s3Endpoint == "" || accessKey == "" || secretKey == "" {
		t.Skip("PostgreSQL and S3 integration environment is required")
	}
	ctx := context.Background()
	store, err := repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	objects, err := NewS3OCIObjectStore(s3Endpoint, accessKey, secretKey, "promotion-conan-"+strings.ReplaceAll(uuid.NewString(), "-", "")[:20])
	if err != nil {
		t.Fatal(err)
	}
	if err = objects.EnsureBucket(ctx); err != nil {
		t.Fatal(err)
	}
	source, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "promotion-conan-source-" + uuid.NewString(), Format: repository.FormatConan})
	if err != nil {
		t.Fatal(err)
	}
	target, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "promotion-conan-target-" + uuid.NewString(), Format: repository.FormatConan})
	if err != nil {
		t.Fatal(err)
	}
	body := []byte("Conan promotion backed by RustFS")
	sum := sha256.Sum256(body)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	key := "native/conan/objects/" + hex.EncodeToString(sum[:])
	if err = objects.PutVerifiedReader(ctx, key, strings.NewReader(string(body)), int64(len(body)), digest); err != nil {
		t.Fatal(err)
	}
	if err = store.StageConanObject(ctx, repository.ConanObjectIntent{RepositoryID: source.ID, ObjectKey: key, Digest: digest, Size: int64(len(body))}); err != nil {
		t.Fatal(err)
	}
	reference, revision := "pkg/1.0/user/stable", "rrev"
	if _, err = store.PutConanRecipeRevision(ctx, repository.ConanRecipeRevision{RepositoryID: source.ID, Reference: reference, Revision: revision, Digest: digest}, []repository.ConanAsset{{RepositoryID: source.ID, Reference: reference, RecipeRevision: revision, Path: "conanfile.py", ObjectKey: key, Digest: digest, Size: int64(len(body))}}); err != nil {
		t.Fatal(err)
	}
	job, _, err := (conanprotocol.NativePromotion{Store: store}).Enqueue(ctx, target.ID, "postgres-rustfs-conan-promotion", conanprotocol.PromotionPayload{SourceRepositoryID: source.ID, Reference: reference, Revision: revision, Digest: digest})
	if err != nil {
		t.Fatal(err)
	}
	if err = (conanprotocol.NativePromotion{Store: store}).RunJobs(ctx, 1); err != nil {
		t.Fatal(err)
	}
	jobs, err := store.ListLifecycleJobs(ctx, target.ID, 10)
	if err != nil || len(jobs) != 1 || jobs[0].ID != job.ID || jobs[0].State != repository.LifecycleJobCompleted {
		t.Fatalf("jobs=%#v err=%v", jobs, err)
	}
	targetRevision, err := store.GetConanRecipeRevision(ctx, target.ID, reference, revision)
	if err != nil || targetRevision.Digest != digest || targetRevision.State != "visible" {
		t.Fatalf("target revision=%#v err=%v", targetRevision, err)
	}
	if info, err := objects.Stat(ctx, key); err != nil || info.Digest != digest || info.Size != int64(len(body)) {
		t.Fatalf("RustFS object=%#v err=%v", info, err)
	}
}

func TestPostgresRustFSConanReplicationPublishesTargetOwnedRevision(t *testing.T) {
	databaseURL, endpoint := os.Getenv("TEST_DATABASE_URL"), os.Getenv("TEST_S3_ENDPOINT")
	accessKey, secretKey := os.Getenv("TEST_S3_ACCESS_KEY"), os.Getenv("TEST_S3_SECRET_KEY")
	if databaseURL == "" || endpoint == "" || accessKey == "" || secretKey == "" {
		t.Skip("PostgreSQL and S3 integration environment is required")
	}
	ctx := context.Background()
	store, err := repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	objects, err := NewS3OCIObjectStore(endpoint, accessKey, secretKey, "conan-replication-"+strings.ReplaceAll(uuid.NewString(), "-", "")[:20])
	if err != nil {
		t.Fatal(err)
	}
	if err = objects.EnsureBucket(ctx); err != nil {
		t.Fatal(err)
	}
	source, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "replication-conan-source-" + uuid.NewString(), Format: repository.FormatConan})
	if err != nil {
		t.Fatal(err)
	}
	target, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "replication-conan-target-" + uuid.NewString(), Format: repository.FormatConan})
	if err != nil {
		t.Fatal(err)
	}
	body := []byte("Conan replication backed by PostgreSQL and RustFS")
	sum := sha256.Sum256(body)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	key := "native/conan/source/" + hex.EncodeToString(sum[:])
	if err = objects.PutVerifiedReader(ctx, key, strings.NewReader(string(body)), int64(len(body)), digest); err != nil {
		t.Fatal(err)
	}
	if err = store.StageConanObject(ctx, repository.ConanObjectIntent{RepositoryID: source.ID, ObjectKey: key, Digest: digest, Size: int64(len(body))}); err != nil {
		t.Fatal(err)
	}
	reference, revision := "pkg/1.0/user/stable", "rrev"
	if _, err = store.PutConanRecipeRevision(ctx, repository.ConanRecipeRevision{RepositoryID: source.ID, Reference: reference, Revision: revision, Digest: digest}, []repository.ConanAsset{{RepositoryID: source.ID, Reference: reference, RecipeRevision: revision, Path: "conanfile.py", ObjectKey: key, Digest: digest, Size: int64(len(body))}}); err != nil {
		t.Fatal(err)
	}
	checks, err := conanReplicationCheckpoints(ctx, store, source.ID, target.ID, reference, revision)
	if err != nil || len(checks) != 1 {
		t.Fatalf("checkpoints=%#v err=%v", checks, err)
	}
	plan := repository.ReplicationPlan{ID: uuid.NewString(), SourceRepositoryID: source.ID, TargetRepositoryID: target.ID, Format: repository.FormatConan, Coordinate: reference + "#" + revision, Digest: digest, IdempotencyKey: "postgres-rustfs-conan-replication"}
	if _, _, err = store.CreateReplicationPlan(ctx, plan, checks); err != nil {
		t.Fatal(err)
	}
	if err = (ConanReplication{Store: store, Source: objects, Destination: objects, ChunkBytes: 5}).RunJobs(ctx, 1); err != nil {
		t.Fatal(err)
	}
	asset, err := store.GetConanRecipeAsset(ctx, target.ID, reference, revision, "conanfile.py")
	if err != nil || asset.ObjectKey != conanReplicationTargetObjectKey(target.ID, key) || asset.Digest != digest {
		t.Fatalf("target asset=%#v err=%v", asset, err)
	}
	if got, err := objects.Get(ctx, asset.ObjectKey); err != nil || string(got) != string(body) {
		t.Fatalf("target object=%q err=%v", got, err)
	}
	plans, err := store.ListReplicationPlans(ctx, target.ID, 10)
	if err != nil || len(plans) != 1 || plans[0].State != "completed" {
		t.Fatalf("plans=%#v err=%v", plans, err)
	}
}

type failOnceConanReclaimStore struct {
	OCIObjectStore
	fail bool
}

func (s *failOnceConanReclaimStore) Delete(ctx context.Context, key string) error {
	if s.fail {
		s.fail = false
		return errors.New("simulated object store delete failure")
	}
	return s.OCIObjectStore.Delete(ctx, key)
}

type repositoryScopedConanReclaimStore struct {
	*repository.PostgresStore
	repositoryID string
}

func (s repositoryScopedConanReclaimStore) ListReclaimableConanObjects(ctx context.Context, before time.Time, limit int) ([]repository.ConanObjectIntent, error) {
	objects, err := s.PostgresStore.ListReclaimableConanObjects(ctx, before, limit)
	if err != nil {
		return nil, err
	}
	scoped := make([]repository.ConanObjectIntent, 0, len(objects))
	for _, object := range objects {
		if object.RepositoryID == s.repositoryID {
			scoped = append(scoped, object)
		}
	}
	return scoped, nil
}

func TestPostgresNativeConanLifecycleStateTransitions(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is required for PostgreSQL integration tests")
	}
	store, err := repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "conan-native-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:20], Format: repository.FormatConan})
	if err != nil {
		t.Fatal(err)
	}
	recipeObject := repository.ConanObjectIntent{RepositoryID: repo.ID, ObjectKey: "native/conan/objects/" + uuid.NewString(), Digest: "sha256:" + strings.Repeat("d", 64), Size: 3}
	if err = store.StageConanObject(ctx, recipeObject); err != nil {
		t.Fatal(err)
	}
	recipe := repository.ConanRecipeRevision{RepositoryID: repo.ID, Reference: "pkg/1.0/user/stable", Revision: "rrev", Digest: recipeObject.Digest}
	recipeAsset := repository.ConanAsset{RepositoryID: repo.ID, Reference: recipe.Reference, RecipeRevision: recipe.Revision, Path: "conanfile.py", ObjectKey: recipeObject.ObjectKey, Digest: recipeObject.Digest, Size: recipeObject.Size}
	publishedRecipe, err := store.PutConanRecipeRevision(ctx, recipe, []repository.ConanAsset{recipeAsset})
	if err != nil || publishedRecipe.State != "visible" || publishedRecipe.CreatedAt.IsZero() {
		t.Fatalf("recipe=%#v err=%v", publishedRecipe, err)
	}
	packageObject := repository.ConanObjectIntent{RepositoryID: repo.ID, ObjectKey: "native/conan/objects/" + uuid.NewString(), Digest: "sha256:" + strings.Repeat("e", 64), Size: 9}
	if err = store.StageConanObject(ctx, packageObject); err != nil {
		t.Fatal(err)
	}
	pkg := repository.ConanPackageRevision{RepositoryID: repo.ID, Reference: recipe.Reference, RecipeRevision: recipe.Revision, PackageID: "package-id", Revision: "prev", Digest: packageObject.Digest}
	pkgAsset := repository.ConanAsset{RepositoryID: repo.ID, Reference: recipe.Reference, RecipeRevision: recipe.Revision, PackageID: pkg.PackageID, PackageRevision: pkg.Revision, Path: "package.tgz", ObjectKey: packageObject.ObjectKey, Digest: packageObject.Digest, Size: packageObject.Size}
	publishedPackage, err := store.PutConanPackageRevision(ctx, pkg, []repository.ConanAsset{pkgAsset})
	if err != nil || publishedPackage.State != "visible" {
		t.Fatalf("package=%#v err=%v", publishedPackage, err)
	}
	if _, err = store.PutConanPackageRevision(ctx, pkg, []repository.ConanAsset{pkgAsset}); err != nil {
		t.Fatalf("package publish replay: %v", err)
	}
	cascadeObject := repository.ConanObjectIntent{RepositoryID: repo.ID, ObjectKey: "native/conan/objects/" + uuid.NewString(), Digest: "sha256:" + strings.Repeat("f", 64), Size: 11}
	if err = store.StageConanObject(ctx, cascadeObject); err != nil {
		t.Fatal(err)
	}
	cascadePackage := repository.ConanPackageRevision{RepositoryID: repo.ID, Reference: recipe.Reference, RecipeRevision: recipe.Revision, PackageID: "cascade-package", Revision: "prev-cascade", Digest: cascadeObject.Digest}
	if _, err = store.PutConanPackageRevision(ctx, cascadePackage, []repository.ConanAsset{{RepositoryID: repo.ID, Reference: recipe.Reference, RecipeRevision: recipe.Revision, PackageID: cascadePackage.PackageID, PackageRevision: cascadePackage.Revision, Path: "package.tgz", ObjectKey: cascadeObject.ObjectKey, Digest: cascadeObject.Digest, Size: cascadeObject.Size}}); err != nil {
		t.Fatal(err)
	}
	tombstonedPackage, err := store.TombstoneConanPackageRevision(ctx, repo.ID, recipe.Reference, recipe.Revision, pkg.PackageID, pkg.Revision)
	if err != nil || tombstonedPackage.State != "deleted" {
		t.Fatalf("package tombstone=%#v err=%v", tombstonedPackage, err)
	}
	tombstonedRecipe, err := store.TombstoneConanRecipeRevision(ctx, repo.ID, recipe.Reference, recipe.Revision)
	if err != nil || tombstonedRecipe.State != "deleted" {
		t.Fatalf("recipe tombstone=%#v err=%v", tombstonedRecipe, err)
	}
	if _, err = store.RestoreConanRecipeRevision(ctx, repo.ID, recipe.Reference, recipe.Revision); err != nil {
		t.Fatal(err)
	}
	explicitPackage, err := store.GetConanPackageRevision(ctx, repo.ID, recipe.Reference, recipe.Revision, pkg.PackageID, pkg.Revision)
	if err != nil || explicitPackage.State != "deleted" {
		t.Fatalf("explicit package=%#v err=%v", explicitPackage, err)
	}
	restoredPackage, err := store.GetConanPackageRevision(ctx, repo.ID, recipe.Reference, recipe.Revision, cascadePackage.PackageID, cascadePackage.Revision)
	if err != nil || restoredPackage.State != "visible" {
		t.Fatalf("cascaded package=%#v err=%v", restoredPackage, err)
	}
}

func TestPostgresConanReferenceSearchProjection(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is required for PostgreSQL integration tests")
	}
	store, err := repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "conan-search-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:20], Format: repository.FormatConan})
	if err != nil {
		t.Fatal(err)
	}
	for i, reference := range []string{"pkg/2.0/user/stable", "pkg/1.0/user/stable", "other/1.0/user/stable"} {
		key := "native/conan/search/" + uuid.NewString()
		digest := "sha256:" + strings.Repeat(string(rune('a'+i)), 64)
		if err = store.StageConanObject(ctx, repository.ConanObjectIntent{RepositoryID: repo.ID, ObjectKey: key, Digest: digest, Size: 1}); err != nil {
			t.Fatal(err)
		}
		if _, err = store.PutConanRecipeRevision(ctx, repository.ConanRecipeRevision{RepositoryID: repo.ID, Reference: reference, Revision: "rrev", Digest: digest}, []repository.ConanAsset{{RepositoryID: repo.ID, Reference: reference, RecipeRevision: "rrev", Path: "conanfile.py", ObjectKey: key, Digest: digest, Size: 1}}); err != nil {
			t.Fatal(err)
		}
	}
	references, err := store.SearchConanReferences(ctx, repo.ID, "pkg/", 2, "")
	if err != nil || len(references) != 2 || references[0].Reference != "pkg/1.0/user/stable" || references[1].Reference != "pkg/2.0/user/stable" {
		t.Fatalf("references=%#v err=%v", references, err)
	}
	next, err := store.SearchConanReferences(ctx, repo.ID, "pkg/", 2, references[0].Reference)
	if err != nil || len(next) != 1 || next[0].Reference != "pkg/2.0/user/stable" {
		t.Fatalf("next=%#v err=%v", next, err)
	}
}

func TestPostgresAndRustFSConanReclaimRetriesAndPreventsRestore(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	s3Endpoint := os.Getenv("TEST_S3_ENDPOINT")
	accessKey := os.Getenv("TEST_S3_ACCESS_KEY")
	secretKey := os.Getenv("TEST_S3_SECRET_KEY")
	if databaseURL == "" || s3Endpoint == "" || accessKey == "" || secretKey == "" {
		t.Skip("PostgreSQL and S3 integration environment is required")
	}
	store, err := repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	bucket := "conan-reclaim-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:20]
	objects, err := NewS3OCIObjectStore(s3Endpoint, accessKey, secretKey, bucket)
	if err != nil {
		t.Fatal(err)
	}
	if err = objects.EnsureBucket(ctx); err != nil {
		t.Fatal(err)
	}
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "conan-reclaim-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:20], Format: repository.FormatConan})
	if err != nil {
		t.Fatal(err)
	}
	key := "native/conan/reclaim/" + uuid.NewString()
	digest := "sha256:" + strings.Repeat("a", 64)
	if err = store.StageConanObject(ctx, repository.ConanObjectIntent{RepositoryID: repo.ID, ObjectKey: key, Digest: digest, Size: 3}); err != nil {
		t.Fatal(err)
	}
	if err = objects.Put(ctx, key, []byte("old")); err != nil {
		t.Fatal(err)
	}
	revision := repository.ConanRecipeRevision{RepositoryID: repo.ID, Reference: "pkg/1.0/user/stable", Revision: "rrev", Digest: digest}
	asset := repository.ConanAsset{RepositoryID: repo.ID, Reference: revision.Reference, RecipeRevision: revision.Revision, Path: "conanfile.py", ObjectKey: key, Digest: digest, Size: 3}
	if _, err = store.PutConanRecipeRevision(ctx, revision, []repository.ConanAsset{asset}); err != nil {
		t.Fatal(err)
	}
	if _, err = store.TombstoneConanRecipeRevision(ctx, repo.ID, revision.Reference, revision.Revision); err != nil {
		t.Fatal(err)
	}
	handler := NewGatewayHandler(Dependencies{NativeConanObjectStore: objects}, store, TestAdapter{}, testAuthenticator())
	restore := func() *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(http.MethodPost, "/api/v2/repositories/"+repo.ID+"/restore", strings.NewReader(`{"coordinate":"`+revision.Reference+`#`+revision.Revision+`"}`))
		authorize(request, "admin-secret")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	if response := restore(); response.Code != http.StatusNoContent {
		t.Fatalf("restore before reclaim=%d body=%s", response.Code, response.Body.String())
	}
	if restored, getErr := store.GetConanRecipeRevision(ctx, repo.ID, revision.Reference, revision.Revision); getErr != nil || restored.State != "visible" {
		t.Fatalf("restored=%#v err=%v", restored, getErr)
	}
	if _, err = store.TombstoneConanRecipeRevision(ctx, repo.ID, revision.Reference, revision.Revision); err != nil {
		t.Fatal(err)
	}
	maintenance := NativeConanMaintenance{Store: repositoryScopedConanReclaimStore{PostgresStore: store, repositoryID: repo.ID}, Objects: &failOnceConanReclaimStore{OCIObjectStore: objects, fail: true}, Now: func() time.Time { return time.Now().Add(25 * time.Hour) }}
	if err = maintenance.Collect(ctx); err == nil {
		t.Fatal("first reclaim must fail")
	}
	jobs, err := store.ListLifecycleJobs(ctx, repo.ID, 10)
	if err != nil || len(jobs) != 1 || jobs[0].Kind != repository.LifecycleJobReclaim || jobs[0].State != repository.LifecycleJobRetrying {
		t.Fatalf("failed reclaim jobs=%#v err=%v", jobs, err)
	}
	if _, err = store.RunLifecycleJobNow(ctx, repo.ID, jobs[0].ID); err != nil {
		t.Fatal(err)
	}
	if err = maintenance.Collect(ctx); err != nil {
		t.Fatalf("retry reclaim: %v", err)
	}
	jobs, err = store.ListLifecycleJobs(ctx, repo.ID, 10)
	if err != nil || len(jobs) != 1 || jobs[0].State != repository.LifecycleJobCompleted {
		t.Fatalf("completed reclaim jobs=%#v err=%v", jobs, err)
	}
	if _, err = objects.Get(ctx, key); err == nil {
		t.Fatal("RustFS object remains after reclaim")
	}
	if response := restore(); response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "restore_unavailable") {
		t.Fatalf("restore collected revision=%d body=%s", response.Code, response.Body.String())
	}
}
