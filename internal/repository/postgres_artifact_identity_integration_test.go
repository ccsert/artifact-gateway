//go:build integration

package repository

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestPostgresArtifactIdentitiesReturnCanonicalNPMVersions(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is required for PostgreSQL integration tests")
	}
	store, err := NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	repo, err := store.CreateHostedRepository(ctx, HostedRepository{
		ID: uuid.NewString(), Name: "artifact-identity-" + uuid.NewString(), Format: FormatNPM,
	})
	if err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = store.db.ExecContext(context.Background(), `DELETE FROM artifact_intelligence WHERE repository_id=$1`, repo.ID)
		_, _ = store.db.ExecContext(context.Background(), `DELETE FROM native_npm_versions WHERE repository_id=$1`, repo.ID)
		_, _ = store.db.ExecContext(context.Background(), `DELETE FROM native_npm_packages WHERE repository_id=$1`, repo.ID)
		_, _ = store.db.ExecContext(context.Background(), `DELETE FROM hosted_repositories WHERE id=$1`, repo.ID)
		_ = store.Close()
	})

	digest1 := "sha256:" + strings.Repeat("1", 64)
	digest2 := "sha256:" + strings.Repeat("2", 64)
	for _, version := range []NPMVersion{
		{RepositoryID: repo.ID, PackageName: "@team/widget", Version: "1.0.0", Digest: digest1, ObjectKey: "npm/widget/1", TarballName: "widget-1.0.0.tgz", Size: 10, Manifest: []byte(`{"name":"@team/widget","version":"1.0.0"}`)},
		{RepositoryID: repo.ID, PackageName: "@team/widget", Version: "2.0.0", Digest: digest2, ObjectKey: "npm/widget/2", TarballName: "widget-2.0.0.tgz", Size: 20, Manifest: []byte(`{"name":"@team/widget","version":"2.0.0"}`)},
	} {
		if _, err = store.PublishNPMVersion(ctx, version, map[string]string{"latest": version.Version}); err != nil {
			t.Fatal(err)
		}
		publishedAt := time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC)
		if version.Version == "2.0.0" {
			publishedAt = publishedAt.Add(24 * time.Hour)
		}
		if _, err = store.db.ExecContext(ctx, `UPDATE native_npm_versions SET created_at=$4 WHERE repository_id=$1 AND package_name=$2 AND version=$3`, repo.ID, version.PackageName, version.Version, publishedAt); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = store.ReplaceArtifactIntelligence(ctx, ArtifactIntelligence{
		RepositoryID: repo.ID, Format: FormatNPM, Coordinate: "@team/widget@1.0.0", Digest: digest1,
		Vulnerability: &ArtifactVulnerabilitySummary{Status: "clean"}, UpdatedBy: "integration-test",
	}, ""); err != nil {
		t.Fatal(err)
	}

	identities, err := store.ListArtifactIdentities(ctx, repo.ID, FormatNPM, ArtifactIdentityDistribution, "widget", 50)
	if err != nil || len(identities) != 2 {
		t.Fatalf("identities=%#v err=%v", identities, err)
	}
	if identities[0].Coordinate != "@team/widget@2.0.0" || identities[0].Digest != digest2 || identities[0].Size == nil || *identities[0].Size != 20 {
		t.Fatalf("latest identity=%#v", identities[0])
	}
	if identities[1].Coordinate != "@team/widget@1.0.0" || identities[1].Intelligence == nil || identities[1].Intelligence.VulnerabilityStatus != "clean" {
		t.Fatalf("historical identity=%#v", identities[1])
	}
}
