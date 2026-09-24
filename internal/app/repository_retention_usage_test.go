package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

func seedRetentionDownloadAudit(t *testing.T, store *repository.MemoryStore, repoName, format, resource string, downloadedAt time.Time) {
	t.Helper()
	if err := store.RecordAudit(context.Background(), repository.AuditRecord{
		Repository: repoName, Format: format, Resource: resource, Actor: "ci",
		Outcome: repository.AuditResolved, Operation: "get", Status: http.StatusOK,
		Bytes: 128, OccurredAt: downloadedAt,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestRetentionUsageMatchersFoldAuditResourcesIntoCoordinates(t *testing.T) {
	store := repository.NewMemoryStore()
	now := time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)
	repoName := "retention-usage"
	seedRetentionDownloadAudit(t, store, repoName, "npm", "widget@1.0.0", now.Add(-time.Hour))
	seedRetentionDownloadAudit(t, store, repoName, "pypi", "hello@2.0", now.Add(-2*time.Hour))
	seedRetentionDownloadAudit(t, store, repoName, "go", "example.com/m@v1.2.3", now.Add(-3*time.Hour))
	seedRetentionDownloadAudit(t, store, repoName, "maven", "org/example/app/1.0/app-1.0.jar", now.Add(-4*time.Hour))
	seedRetentionDownloadAudit(t, store, repoName, "maven", "org/example/app/1.0/app-1.0.jar.sha1", now.Add(-4*time.Hour))
	seedRetentionDownloadAudit(t, store, repoName, "raw", "dist/tool.tar.gz", now.Add(-5*time.Hour))
	seedRetentionDownloadAudit(t, store, repoName, "oci", "/v2/team/app/manifests/sha256:"+strings.Repeat("a", 64), now.Add(-6*time.Hour))
	seedRetentionDownloadAudit(t, store, repoName, "conan", "widget/1.0/team/stable/revisions/rrev/files/conanfile.py", now.Add(-7*time.Hour))
	seedRetentionDownloadAudit(t, store, repoName, "oci", "/v2/team/app/manifests/latest", now.Add(-time.Hour)) // tag pull: not attributable
	seedRetentionDownloadAudit(t, store, "other-repo", "npm", "widget@9.9.9", now.Add(-time.Hour))              // other repository

	candidates := []RepositoryRetentionCandidate{
		{Format: repository.FormatNPM, Coordinate: "widget@1.0.0"},
		{Format: repository.FormatPyPI, Coordinate: "hello@2.0"},
		{Format: repository.FormatGo, Coordinate: "example.com/m@v1.2.3"},
		{Format: repository.FormatMaven, Coordinate: "org.example:app:1.0"},
		{Format: repository.FormatRaw, Coordinate: "dist/tool.tar.gz", rawPath: "dist/tool.tar.gz"},
		{Format: repository.FormatOCI, Digest: "sha256:" + strings.Repeat("a", 64)},
		{Format: repository.FormatConan, conanRef: "widget/1.0@team/stable", conanRevision: "rrev"},
	}
	index, err := (NativeRepositoryRetention{Store: store}).loadRetentionUsageIndex(context.Background(), repoName, candidates)
	if err != nil {
		t.Fatal(err)
	}

	assertEvidence := func(evidence usageEvidence, wantCount int64, wantAge time.Duration) {
		t.Helper()
		if evidence.DownloadCount != wantCount {
			t.Fatalf("downloadCount=%d want %d", evidence.DownloadCount, wantCount)
		}
		if wantAge == 0 {
			if !evidence.LastDownloadedAt.IsZero() {
				t.Fatalf("lastDownloadedAt=%v want zero", evidence.LastDownloadedAt)
			}
			return
		}
		if !evidence.LastDownloadedAt.Equal(now.Add(-wantAge)) {
			t.Fatalf("lastDownloadedAt=%v want %v", evidence.LastDownloadedAt, now.Add(-wantAge))
		}
	}

	assertEvidence(index.match(&RepositoryRetentionCandidate{Format: repository.FormatNPM, Coordinate: "widget@1.0.0"}), 1, time.Hour)
	assertEvidence(index.match(&RepositoryRetentionCandidate{Format: repository.FormatPyPI, Coordinate: "hello@2.0"}), 1, 2*time.Hour)
	assertEvidence(index.match(&RepositoryRetentionCandidate{Format: repository.FormatGo, Coordinate: "example.com/m@v1.2.3"}), 1, 3*time.Hour)
	assertEvidence(index.match(&RepositoryRetentionCandidate{Format: repository.FormatMaven, Coordinate: "org.example:app:1.0"}), 2, 4*time.Hour)
	assertEvidence(index.match(&RepositoryRetentionCandidate{Format: repository.FormatRaw, Coordinate: "dist/tool.tar.gz", rawPath: "dist/tool.tar.gz"}), 1, 5*time.Hour)
	assertEvidence(index.match(&RepositoryRetentionCandidate{Format: repository.FormatOCI, Digest: "sha256:" + strings.Repeat("a", 64)}), 1, 6*time.Hour)
	assertEvidence(index.match(&RepositoryRetentionCandidate{Format: repository.FormatConan, conanRef: "widget/1.0@team/stable", conanRevision: "rrev"}), 1, 7*time.Hour)
	assertEvidence(index.match(&RepositoryRetentionCandidate{Format: repository.FormatNPM, Coordinate: "widget@2.0.0"}), 0, 0)
}

func TestRetentionUsageIndexIncludesRowsBeyondManagementLimit(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	when := time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)
	for i := range 500 {
		for range 2 {
			seedRetentionDownloadAudit(t, store, "many-usage", "npm", fmt.Sprintf("popular-%03d@1.0.0", i), when.Add(-time.Hour))
		}
	}
	seedRetentionDownloadAudit(t, store, "many-usage", "npm", "target@1.0.0", when)
	index, err := (NativeRepositoryRetention{Store: store}).loadRetentionUsageIndex(ctx, "many-usage", []RepositoryRetentionCandidate{{Format: repository.FormatNPM, Coordinate: "target@1.0.0"}})
	if err != nil {
		t.Fatal(err)
	}
	got := index.match(&RepositoryRetentionCandidate{Format: repository.FormatNPM, Coordinate: "target@1.0.0"})
	if got.DownloadCount != 1 || !got.LastDownloadedAt.Equal(when) {
		t.Fatalf("usage past first 500 rows was lost: %#v", got)
	}
}

type failedUsageWalkStore struct {
	*repository.MemoryStore
	err error
}

func (s failedUsageWalkStore) WalkArtifactUsage(context.Context, string, func(repository.ArtifactUsageStat) error) error {
	return s.err
}

func TestRetentionPlanFailsClosedWhenUsageCannotBeRead(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{
		ID: uuid.NewString(), Name: "retention-usage-unavailable", Format: repository.FormatNPM,
	})
	if err != nil {
		t.Fatal(err)
	}
	retentionPolicyForTest(t, store, repo.ID, repository.RepositoryRetentionPolicy{KeepDays: 1, MinimumVersions: 1, MaximumVersions: 2, KeepDownloadedDays: 7})
	for _, version := range []string{"1.0.0", "2.0.0", "3.0.0"} {
		if _, err := store.PublishNPMVersion(ctx, repository.NPMVersion{
			RepositoryID: repo.ID, PackageName: "widget", Version: version, Digest: "sha256:" + strings.Repeat(version[:1], 64), ObjectKey: "npm/widget/" + version, Size: 10,
		}, nil); err != nil {
			t.Fatal(err)
		}
	}
	wantErr := errors.New("usage unavailable")
	retention := NativeRepositoryRetention{Store: failedUsageWalkStore{MemoryStore: store, err: wantErr}, Now: func() time.Time { return time.Now().UTC().Add(48 * time.Hour) }}
	if _, err := retention.PlanRepositoryDetailed(ctx, repo.ID, repository.FormatNPM); !errors.Is(err, wantErr) {
		t.Fatalf("retention should stop on usage read failure, got %v", err)
	}
}

func TestRetentionDownloadedWithinWindow(t *testing.T) {
	now := time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)
	never := usageEvidence{}
	if never.downloadedWithin(7, now) {
		t.Fatal("never downloaded must not be protected")
	}
	recent := usageEvidence{DownloadCount: 1, LastDownloadedAt: now.Add(-24 * time.Hour)}
	if !recent.downloadedWithin(7, now) {
		t.Fatal("recent download must be protected")
	}
	if recent.downloadedWithin(0, now) {
		t.Fatal("disabled window must not protect")
	}
	old := usageEvidence{DownloadCount: 5, LastDownloadedAt: now.Add(-8 * 24 * time.Hour)}
	if old.downloadedWithin(7, now) {
		t.Fatal("download outside the window must not be protected")
	}
}

func TestRetentionPlanProtectsRecentlyDownloadedCandidatesAndAttachesEvidence(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{
		ID: uuid.NewString(), Name: "retention-npm-usage", Format: repository.FormatNPM,
	})
	if err != nil {
		t.Fatal(err)
	}
	retentionPolicyForTest(t, store, repo.ID, repository.RepositoryRetentionPolicy{
		KeepDays: 1, MinimumVersions: 1, MaximumVersions: 2, KeepDownloadedDays: 7,
	})
	now := time.Now().UTC().Add(48 * time.Hour)
	publish := func(version string) {
		t.Helper()
		if _, err := store.PublishNPMVersion(ctx, repository.NPMVersion{
			RepositoryID: repo.ID, PackageName: "widget", Version: version,
			Digest: "sha256:" + strings.Repeat(version[len(version)-1:], 64), ObjectKey: "npm/widget/" + version, Size: 10,
		}, map[string]string{}); err != nil {
			t.Fatal(err)
		}
	}
	// Publish order fixes the newest-first index: 3.0.0 is protected by
	// MinimumVersions, 2.0.0 becomes an age candidate, and 1.0.0 exceeds
	// MaximumVersions. Only 1.0.0 was recently downloaded.
	publish("1.0.0")
	publish("2.0.0")
	publish("3.0.0")
	seedRetentionDownloadAudit(t, store, repo.Name, "npm", "widget@1.0.0", now.Add(-time.Hour))

	retention := NativeRepositoryRetention{Store: store, Now: func() time.Time { return now }}
	candidates, err := retention.PlanRepositoryDetailed(ctx, repo.ID, repository.FormatNPM)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 {
		t.Fatalf("candidates=%#v", candidates)
	}
	if candidates[0].Coordinate != "widget@2.0.0" {
		t.Fatalf("unexpected survivor %#v", candidates[0])
	}
	if candidates[0].DownloadCount != 0 || !candidates[0].LastDownloadedAt.IsZero() {
		t.Fatalf("survivor evidence=%#v", candidates[0])
	}

	// With the protection window disabled the downloaded version becomes a
	// candidate again, carrying its download evidence.
	policy, err := store.GetRepositoryRetentionPolicy(ctx, repo.ID)
	if err != nil {
		t.Fatal(err)
	}
	policy.KeepDownloadedDays = 0
	if _, err = store.ReplaceRepositoryRetentionPolicy(ctx, repo.ID, policy, policy.Version); err != nil {
		t.Fatal(err)
	}
	candidates, err = retention.PlanRepositoryDetailed(ctx, repo.ID, repository.FormatNPM)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 2 {
		t.Fatalf("unfiltered candidates=%#v", candidates)
	}
	byCoordinate := make(map[string]RepositoryRetentionCandidate, len(candidates))
	for _, candidate := range candidates {
		byCoordinate[candidate.Coordinate] = candidate
	}
	downloaded := byCoordinate["widget@1.0.0"]
	if downloaded.DownloadCount != 1 || !downloaded.LastDownloadedAt.Equal(now.Add(-time.Hour)) {
		t.Fatalf("downloaded evidence=%#v", downloaded)
	}
}

func TestRetentionDryRunResponseCarriesDownloadEvidence(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{
		ID: uuid.NewString(), Name: "retention-raw-usage", Format: repository.FormatRaw,
	})
	if err != nil {
		t.Fatal(err)
	}
	retentionPolicyForTest(t, store, repo.ID, repository.RepositoryRetentionPolicy{KeepDays: 1, MinimumVersions: 1})
	now := time.Now().UTC().Add(48 * time.Hour)
	if _, err = store.PutRawAsset(ctx, repository.RawAsset{
		RepositoryID: repo.ID, Path: "dist/old.bin", Digest: "sha256:" + strings.Repeat("c", 64),
		ObjectKey: "native/raw/old", Size: 10,
	}); err != nil {
		t.Fatal(err)
	}
	seedRetentionDownloadAudit(t, store, repo.Name, "raw", "dist/old.bin", now.Add(-2*time.Hour))

	candidates, err := (NativeRepositoryRetention{Store: store, Now: func() time.Time { return now }}).PlanRepositoryDetailed(ctx, repo.ID, repository.FormatRaw)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 || candidates[0].DownloadCount != 1 {
		t.Fatalf("candidates=%#v", candidates)
	}
}
