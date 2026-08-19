package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	adminopenapi "github.com/artifact-gateway/artifact-gateway/internal/admin/openapi"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

func retentionPolicyForTest(t *testing.T, store *repository.MemoryStore, repositoryID string, policy repository.RepositoryRetentionPolicy) {
	t.Helper()
	policy.Enabled = true
	if policy.KeepDays == 0 {
		policy.KeepDays = 365
	}
	if policy.SnapshotKeepDays == 0 {
		policy.SnapshotKeepDays = policy.KeepDays
	}
	if policy.MinimumVersions == 0 {
		policy.MinimumVersions = 1
	}
	if _, err := store.ReplaceRepositoryRetentionPolicy(context.Background(), repositoryID, policy, "1"); err != nil {
		t.Fatal(err)
	}
}

func retentionRepositoriesForFormats(t *testing.T, store *repository.MemoryStore, formats ...repository.Format) map[repository.Format]repository.HostedRepository {
	t.Helper()
	result := make(map[repository.Format]repository.HostedRepository, len(formats))
	for _, format := range formats {
		repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "retention-" + string(format) + "-" + uuid.NewString(), Format: format})
		if err != nil {
			t.Fatal(err)
		}
		retentionPolicyForTest(t, store, repo.ID, repository.RepositoryRetentionPolicy{KeepDays: 30})
		result[format] = repo
	}
	return result
}

func TestNativeRepositoryRetentionSchedulerIgnoresWorkerFormatFilters(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	repositories := retentionRepositoriesForFormats(t, store, repository.FormatMaven, repository.FormatOCI, repository.FormatConan, repository.FormatRaw)

	retention := NativeRepositoryRetention{Store: store, WorkerFormats: []string{"oci"}}
	if err := retention.Schedule(ctx); err != nil {
		t.Fatal(err)
	}
	for format, repo := range repositories {
		jobs, err := store.ListLifecycleJobs(ctx, repo.ID, 10)
		if err != nil || len(jobs) != 1 {
			t.Fatalf("%s scheduled jobs=%#v err=%v", format, jobs, err)
		}
	}
}

func TestNativeRepositoryRetentionWorkerClaimsOnlyConfiguredFormats(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	repositories := retentionRepositoriesForFormats(t, store, repository.FormatOCI, repository.FormatRaw)
	if err := (NativeRepositoryRetention{Store: store}).Schedule(ctx); err != nil {
		t.Fatal(err)
	}

	worker := NativeRepositoryRetention{Store: store, WorkerFormats: []string{"oci"}}
	if err := worker.RunJobs(ctx, 10); err != nil {
		t.Fatal(err)
	}
	for format, want := range map[repository.Format]repository.LifecycleJobState{
		repository.FormatOCI: repository.LifecycleJobCompleted,
		repository.FormatRaw: repository.LifecycleJobPending,
	} {
		jobs, err := store.ListLifecycleJobs(ctx, repositories[format].ID, 10)
		if err != nil || len(jobs) != 1 || jobs[0].State != want {
			t.Fatalf("%s jobs=%#v err=%v, want state %s", format, jobs, err, want)
		}
	}
}

func TestNativeRepositoryRetentionPlansOCIByImageAndRestoresTombstones(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "retention-oci", Format: repository.FormatOCI})
	if err != nil {
		t.Fatal(err)
	}
	retentionPolicyForTest(t, store, repo.ID, repository.RepositoryRetentionPolicy{KeepDays: 365, MinimumVersions: 1, MaximumVersions: 2, ProtectedPatterns: []string{`^team/protected:stable$`}})
	now := time.Date(2026, 8, 4, 0, 0, 0, 0, time.UTC)
	put := func(name, digest, tag string, createdAt time.Time) {
		t.Helper()
		if _, err := store.PutOCIManifest(ctx, repository.OCIManifest{RepositoryID: repo.ID, Name: name, Digest: digest, ObjectKey: "native/oci/" + strings.TrimPrefix(digest, "sha256:"), MediaType: "application/vnd.oci.image.manifest.v1+json", Size: 1, CreatedAt: createdAt}, tag); err != nil {
			t.Fatal(err)
		}
	}
	put("team/app", "sha256:"+strings.Repeat("1", 64), "v1", now.Add(-72*time.Hour))
	put("team/app", "sha256:"+strings.Repeat("2", 64), "v2", now.Add(-48*time.Hour))
	put("team/app", "sha256:"+strings.Repeat("3", 64), "v3", now.Add(-24*time.Hour))
	put("team/protected", "sha256:"+strings.Repeat("4", 64), "stable", now.Add(-72*time.Hour))
	put("team/protected", "sha256:"+strings.Repeat("5", 64), "v2", now.Add(-48*time.Hour))

	retention := NativeRepositoryRetention{Store: store, Now: func() time.Time { return now }}
	candidates, err := retention.PlanRepositoryDetailed(ctx, repo.ID, repository.FormatOCI)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 || candidates[0].Coordinate != "team/app@sha256:"+strings.Repeat("1", 64) {
		t.Fatalf("OCI candidates=%#v", candidates)
	}
	if !containsRetentionReason(candidates[0].Reasons, "maximum_versions") || candidates[0].VersionType != "version" {
		t.Fatalf("OCI candidate reasons=%#v", candidates[0])
	}
	if _, _, err = retention.EnqueueRepository(ctx, repo.ID, "oci-retention"); err != nil {
		t.Fatal(err)
	}
	if err = retention.RunJobs(ctx, 10); err != nil {
		t.Fatal(err)
	}
	jobs, err := store.ListLifecycleJobs(ctx, repo.ID, 10)
	if err != nil || len(jobs) != 1 || jobs[0].ProgressCurrent != 1 || jobs[0].ProgressTotal != 1 {
		t.Fatalf("retention progress=%#v err=%v", jobs, err)
	}
	if _, err = store.GetOCIManifest(ctx, repo.ID, "team/app", "v1"); err == nil {
		t.Fatal("expected old OCI manifest to be hidden")
	}
	if _, err = store.RestoreOCIManifest(ctx, repo.ID, "team/app", "sha256:"+strings.Repeat("1", 64)); err != nil {
		t.Fatalf("restore OCI manifest: %v", err)
	}
	if _, err = store.GetOCIManifest(ctx, repo.ID, "team/app", "v1"); err != nil {
		t.Fatalf("restored OCI tag unavailable: %v", err)
	}
}

func TestNativeRepositoryRetentionPlansGoModuleVersions(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "retention-go", Format: repository.FormatGo})
	if err != nil {
		t.Fatal(err)
	}
	retentionPolicyForTest(t, store, repo.ID, repository.RepositoryRetentionPolicy{KeepDays: 365, MinimumVersions: 1, MaximumVersions: 2})
	now := time.Date(2026, 8, 19, 0, 0, 0, 0, time.UTC)
	modulePath := "example.com/team/widget"
	for index, version := range []string{"v1.0.0", "v1.1.0", "v1.2.0"} {
		createdAt := now.Add(time.Duration(index-3) * 24 * time.Hour)
		digest := "sha256:" + strings.Repeat(string(rune('a'+index)), 64)
		if _, _, err = store.PublishGoModule(ctx, repository.GoModulePublication{
			Version: repository.GoModuleVersion{RepositoryID: repo.ID, Module: modulePath, Version: version, PublishedAt: createdAt, CreatedAt: createdAt},
			Assets: []repository.GoModuleAsset{
				{RepositoryID: repo.ID, Module: modulePath, Version: version, Kind: "info", Digest: digest, ObjectKey: "native/go/" + version + "/info", Size: 1, CreatedAt: createdAt},
				{RepositoryID: repo.ID, Module: modulePath, Version: version, Kind: "mod", Digest: digest, ObjectKey: "native/go/" + version + "/mod", Size: 2, CreatedAt: createdAt},
				{RepositoryID: repo.ID, Module: modulePath, Version: version, Kind: "zip", Digest: digest, ObjectKey: "native/go/" + version + "/zip", Size: 3, CreatedAt: createdAt},
			},
		}); err != nil {
			t.Fatal(err)
		}
	}

	retention := NativeRepositoryRetention{Store: store, Now: func() time.Time { return now }}
	candidates, err := retention.PlanRepositoryDetailed(ctx, repo.ID, repository.FormatGo)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 || candidates[0].Coordinate != modulePath+"@v1.0.0" || candidates[0].Digest != "sha256:"+strings.Repeat("a", 64) {
		t.Fatalf("Go candidates=%#v", candidates)
	}
	if !containsRetentionReason(candidates[0].Reasons, "maximum_versions") || candidates[0].VersionType != "version" {
		t.Fatalf("Go candidate reasons=%#v", candidates[0])
	}
	if _, _, err = retention.EnqueueRepository(ctx, repo.ID, "go-retention"); err != nil {
		t.Fatal(err)
	}
	if err = retention.RunJobs(ctx, 10); err != nil {
		t.Fatal(err)
	}
	if _, err = store.GetGoModuleVersion(ctx, repo.ID, modulePath, "v1.0.0"); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("retained Go version remains visible: %v", err)
	}
}

func TestRepositoryRetentionAPIDryRunsAndExecutesOCI(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "retention-oci-api", Format: repository.FormatOCI})
	if err != nil {
		t.Fatal(err)
	}
	retentionPolicyForTest(t, store, repo.ID, repository.RepositoryRetentionPolicy{KeepDays: 365, MinimumVersions: 1, MaximumVersions: 1})
	oldDigest := "sha256:" + strings.Repeat("6", 64)
	for index, digest := range []string{oldDigest, "sha256:" + strings.Repeat("7", 64)} {
		if _, err = store.PutOCIManifest(ctx, repository.OCIManifest{RepositoryID: repo.ID, Name: "team/api", Digest: digest, ObjectKey: "native/oci/api-" + digest, MediaType: "application/vnd.oci.image.manifest.v1+json", Size: 1, CreatedAt: time.Now().UTC().Add(time.Duration(index-2) * time.Hour)}, "v"+string(rune('1'+index))); err != nil {
			t.Fatal(err)
		}
	}
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())
	dryRunRequest := httptest.NewRequest(http.MethodPost, "/api/v2/repositories/"+repo.ID+"/retention:dry-run", nil)
	authorize(dryRunRequest, "admin-secret")
	dryRunResponse := httptest.NewRecorder()
	handler.ServeHTTP(dryRunResponse, dryRunRequest)
	if dryRunResponse.Code != http.StatusOK {
		t.Fatalf("OCI dry run=%d body=%s", dryRunResponse.Code, dryRunResponse.Body.String())
	}
	var preview adminopenapi.RetentionDryRun
	if err = json.NewDecoder(dryRunResponse.Body).Decode(&preview); err != nil {
		t.Fatal(err)
	}
	if preview.TotalCandidates != 1 || len(preview.Candidates) != 1 || preview.Candidates[0].Format != adminopenapi.FormatOci || preview.Candidates[0].VersionType != "version" || preview.Candidates[0].Coordinate != "team/api@"+oldDigest {
		t.Fatalf("OCI preview=%#v", preview)
	}
	executeRequest := httptest.NewRequest(http.MethodPost, "/api/v2/repositories/"+repo.ID+"/retention:execute", nil)
	authorize(executeRequest, "admin-secret")
	executeRequest.Header.Set("Idempotency-Key", "oci-api-retention")
	executeRequest.Header.Set("If-Match", preview.PolicyVersion)
	executeResponse := httptest.NewRecorder()
	handler.ServeHTTP(executeResponse, executeRequest)
	if executeResponse.Code != http.StatusAccepted {
		t.Fatalf("OCI execute=%d body=%s", executeResponse.Code, executeResponse.Body.String())
	}
	if err = (NativeRepositoryRetention{Store: store}).RunJobs(ctx, 10); err != nil {
		t.Fatal(err)
	}
	if _, err = store.GetOCIManifest(ctx, repo.ID, "team/api", oldDigest); err == nil {
		t.Fatal("OCI retention API left the candidate visible")
	}
}

func TestNativeRepositoryRetentionPlansConanRecipeRevisions(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "retention-conan", Format: repository.FormatConan})
	if err != nil {
		t.Fatal(err)
	}
	retentionPolicyForTest(t, store, repo.ID, repository.RepositoryRetentionPolicy{KeepDays: 365, MinimumVersions: 1, MaximumVersions: 2})
	reference := "pkg/1.0/user/stable"
	for index, revision := range []string{"rrev-a", "rrev-b", "rrev-c"} {
		if _, err := store.PutConanRecipeRevision(ctx, repository.ConanRecipeRevision{RepositoryID: repo.ID, Reference: reference, Revision: revision, Digest: "sha256:" + strings.Repeat(string(rune('a'+index)), 64)}, nil); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Millisecond)
	}
	retention := NativeRepositoryRetention{Store: store, Now: func() time.Time { return time.Now().UTC().Add(24 * time.Hour) }}
	candidates, err := retention.PlanRepositoryDetailed(ctx, repo.ID, repository.FormatConan)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 || candidates[0].Coordinate != reference+"#rrev-a" || candidates[0].VersionType != "version" {
		t.Fatalf("Conan candidates=%#v", candidates)
	}
	if _, _, err = retention.EnqueueRepository(ctx, repo.ID, "conan-retention"); err != nil {
		t.Fatal(err)
	}
	if err = retention.RunJobs(ctx, 10); err != nil {
		t.Fatal(err)
	}
	deleted, err := store.GetConanRecipeRevision(ctx, repo.ID, reference, "rrev-a")
	if err != nil || deleted.State != "deleted" {
		t.Fatalf("Conan recipe state=%#v err=%v", deleted, err)
	}
	visible, err := store.ListConanRecipeRevisions(ctx, repo.ID, reference)
	if err != nil || len(visible) != 2 {
		t.Fatalf("visible Conan revisions=%#v err=%v", visible, err)
	}
}

func TestNativeRepositoryRetentionPlansRawAssetsWithoutVersionCounts(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "retention-raw", Format: repository.FormatRaw})
	if err != nil {
		t.Fatal(err)
	}
	retentionPolicyForTest(t, store, repo.ID, repository.RepositoryRetentionPolicy{KeepDays: 1, MinimumVersions: 100, MaximumVersions: 100, CoordinatePatterns: []string{`^releases/`}})
	if _, err := store.PutRawAsset(ctx, repository.RawAsset{RepositoryID: repo.ID, Path: "releases/old.txt", Digest: "sha256:" + strings.Repeat("a", 64), ObjectKey: "native/raw/old", Size: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PutRawAsset(ctx, repository.RawAsset{RepositoryID: repo.ID, Path: "other/current.txt", Digest: "sha256:" + strings.Repeat("b", 64), ObjectKey: "native/raw/current", Size: 1}); err != nil {
		t.Fatal(err)
	}
	retention := NativeRepositoryRetention{Store: store, Now: func() time.Time { return time.Now().UTC().Add(48 * time.Hour) }}
	candidates, err := retention.PlanRepositoryDetailed(ctx, repo.ID, repository.FormatRaw)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 || candidates[0].Coordinate != "releases/old.txt" || candidates[0].VersionType != "asset" {
		t.Fatalf("Raw candidates=%#v", candidates)
	}
	if containsRetentionReason(candidates[0].Reasons, "maximum_versions") {
		t.Fatalf("Raw asset incorrectly used version cap: %#v", candidates[0])
	}
	if _, _, err = retention.EnqueueRepository(ctx, repo.ID, "raw-retention"); err != nil {
		t.Fatal(err)
	}
	if err = retention.RunJobs(ctx, 10); err != nil {
		t.Fatal(err)
	}
	if _, err = store.GetRawAsset(ctx, repo.ID, "releases/old.txt"); err == nil {
		t.Fatal("expected old Raw asset to be hidden")
	}
	if _, err = store.RestoreRawAsset(ctx, repo.ID, "releases/old.txt"); err != nil {
		t.Fatalf("restore Raw asset: %v", err)
	}
	if _, err = store.GetRawAsset(ctx, repo.ID, "releases/old.txt"); err != nil {
		t.Fatalf("restored Raw asset unavailable: %v", err)
	}
}

func TestNativeRepositoryRetentionCompletesSupersededPolicyJobWithoutRetry(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "retention-superseded", Format: repository.FormatRaw})
	if err != nil {
		t.Fatal(err)
	}
	retentionPolicyForTest(t, store, repo.ID, repository.RepositoryRetentionPolicy{KeepDays: 30})
	retention := NativeRepositoryRetention{Store: store}
	if _, _, err = retention.EnqueueRepository(ctx, repo.ID, "superseded"); err != nil {
		t.Fatal(err)
	}
	policy, err := store.GetRepositoryRetentionPolicy(ctx, repo.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.ReplaceRepositoryRetentionPolicy(ctx, repo.ID, repository.RepositoryRetentionPolicy{Enabled: true, KeepDays: 60, SnapshotKeepDays: 60, MinimumVersions: 1}, policy.Version); err != nil {
		t.Fatal(err)
	}
	if err = retention.RunJobs(ctx, 1); err != nil {
		t.Fatalf("superseded job returned error: %v", err)
	}
	jobs, err := store.ListLifecycleJobs(ctx, repo.ID, 10)
	if err != nil || len(jobs) != 1 || jobs[0].State != repository.LifecycleJobCompleted || !strings.Contains(jobs[0].ProgressMessage, "superseded") {
		t.Fatalf("superseded job=%#v err=%v", jobs, err)
	}
}
