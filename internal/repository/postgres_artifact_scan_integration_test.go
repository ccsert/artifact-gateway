//go:build integration

package repository

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

type postgresArtifactScanFixture struct {
	ctx   context.Context
	store *PostgresStore
	repo  HostedRepository
}

func newPostgresArtifactScanFixture(t *testing.T) postgresArtifactScanFixture {
	t.Helper()
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
		ID: uuid.NewString(), Name: "artifact-scan-" + uuid.NewString(), Format: FormatRaw,
	})
	if err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = store.db.ExecContext(context.Background(), `DELETE FROM lifecycle_jobs WHERE repository_id=$1`, repo.ID)
		_, _ = store.db.ExecContext(context.Background(), `DELETE FROM native_raw_assets WHERE repository_id=$1`, repo.ID)
		_, _ = store.db.ExecContext(context.Background(), `DELETE FROM native_raw_objects WHERE repository_id=$1`, repo.ID)
		_, _ = store.db.ExecContext(context.Background(), `DELETE FROM hosted_repositories WHERE id=$1`, repo.ID)
		_ = store.Close()
	})
	return postgresArtifactScanFixture{ctx: ctx, store: store, repo: repo}
}

func (f postgresArtifactScanFixture) putRawAsset(t *testing.T, path, digest string) RawAsset {
	t.Helper()
	asset, err := f.store.PutRawAsset(f.ctx, RawAsset{
		RepositoryID: f.repo.ID,
		Path:         path,
		Digest:       digest,
		ObjectKey:    "integration/artifact-scan/" + uuid.NewString(),
		Size:         7,
	})
	if err != nil {
		t.Fatal(err)
	}
	return asset
}

func TestPostgresArtifactScanCandidatesPrioritizeMissingScans(t *testing.T) {
	fixture := newPostgresArtifactScanFixture(t)
	missing := fixture.putRawAsset(t, "releases/missing.bin", "sha256:"+strings.Repeat("a", 64))
	active := fixture.putRawAsset(t, "releases/active.bin", "sha256:"+strings.Repeat("b", 64))
	if _, _, err := EnqueueArtifactScanJob(fixture.ctx, fixture.store, fixture.repo.ID, "scan-active", ArtifactScanPayload{
		Format: FormatRaw, Coordinate: active.Path, Digest: active.Digest,
	}); err != nil {
		t.Fatal(err)
	}

	prioritized, err := fixture.store.ListArtifactScanCandidates(fixture.ctx, fixture.repo.ID, FormatRaw, 1)
	if err != nil || len(prioritized) != 1 || prioritized[0].Coordinate != missing.Path || prioritized[0].Digest != missing.Digest {
		t.Fatalf("prioritized=%#v err=%v", prioritized, err)
	}
}

func TestPostgresArtifactIntelligencePersistsVulnerabilityFindings(t *testing.T) {
	fixture := newPostgresArtifactScanFixture(t)
	score := 9.8
	coordinate := "releases/widget.bin"
	digest := "sha256:" + strings.Repeat("e", 64)
	created, err := fixture.store.ReplaceArtifactIntelligence(fixture.ctx, ArtifactIntelligence{
		RepositoryID: fixture.repo.ID, Format: FormatRaw, Coordinate: coordinate, Digest: digest,
		Vulnerability: &ArtifactVulnerabilitySummary{
			Scanner: "grype", Status: "affected", Critical: 1,
			Findings: []ArtifactVulnerabilityFinding{{
				ID: "CVE-2026-1234", Source: "nvd", Severity: ArtifactVulnerabilitySeverityCritical,
				Component: "pkg:generic/widget@1.0.0", Version: "1.0.0", FixedVersion: "1.0.1",
				Location: "widget.bin", CVSSScore: &score, CVSSVector: "CVSS:3.1/AV:N/AC:L",
			}},
		},
		UpdatedBy: "scanner:grype",
	}, "")
	if err != nil || created.Version != "1" {
		t.Fatalf("created=%#v err=%v", created, err)
	}

	loaded, err := fixture.store.GetArtifactIntelligence(fixture.ctx, fixture.repo.ID, FormatRaw, coordinate, digest)
	if err != nil || loaded.Vulnerability == nil || len(loaded.Vulnerability.Findings) != 1 {
		t.Fatalf("loaded=%#v err=%v", loaded, err)
	}
	if finding := loaded.Vulnerability.Findings[0]; finding.ID != "CVE-2026-1234" || finding.FixedVersion != "1.0.1" || finding.CVSSScore == nil || *finding.CVSSScore != 9.8 {
		t.Fatalf("finding=%#v", finding)
	}
}

func TestPostgresLatestArtifactScanJobUsesImmutableIdentity(t *testing.T) {
	fixture := newPostgresArtifactScanFixture(t)
	payload := ArtifactScanPayload{
		Format: FormatRaw, Coordinate: "releases/widget.bin", Digest: "sha256:" + strings.Repeat("c", 64),
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	older, _, err := fixture.store.EnqueueLifecycleJob(fixture.ctx, LifecycleJob{
		ID: uuid.NewString(), RepositoryID: fixture.repo.ID, Kind: LifecycleJobScan, IdempotencyKey: "scan-older", Payload: body,
	})
	if err != nil {
		t.Fatal(err)
	}
	newer, _, err := fixture.store.EnqueueLifecycleJob(fixture.ctx, LifecycleJob{
		ID: uuid.NewString(), RepositoryID: fixture.repo.ID, Kind: LifecycleJobScan, IdempotencyKey: "scan-newer", Payload: body,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = fixture.store.db.ExecContext(fixture.ctx,
		`UPDATE lifecycle_jobs SET created_at=CASE WHEN id=$1 THEN $3::timestamptz ELSE $4::timestamptz END WHERE id IN ($1,$2)`,
		older.ID, newer.ID, time.Now().UTC().Add(-time.Minute), time.Now().UTC(),
	); err != nil {
		t.Fatal(err)
	}
	latest, err := fixture.store.GetLatestArtifactScanJob(fixture.ctx, fixture.repo.ID, FormatRaw, payload.Coordinate, payload.Digest)
	if err != nil || latest.ID != newer.ID {
		t.Fatalf("latest=%#v newer=%s err=%v", latest, newer.ID, err)
	}
}

func TestPostgresArtifactScanIdentityLockSerializesConnections(t *testing.T) {
	fixture := newPostgresArtifactScanFixture(t)
	coordinate := "releases/widget.bin"
	digest := "sha256:" + strings.Repeat("d", 64)
	unlockFirst, err := fixture.store.LockArtifactScanIdentity(fixture.ctx, fixture.repo.ID, FormatRaw, coordinate, digest)
	if err != nil {
		t.Fatal(err)
	}
	firstReleased := false
	t.Cleanup(func() {
		if !firstReleased {
			unlockFirst()
		}
	})
	secondAcquired := make(chan func(), 1)
	secondErr := make(chan error, 1)
	go func() {
		unlockSecond, lockErr := fixture.store.LockArtifactScanIdentity(fixture.ctx, fixture.repo.ID, FormatRaw, coordinate, digest)
		if lockErr != nil {
			secondErr <- lockErr
			return
		}
		secondAcquired <- unlockSecond
	}()
	select {
	case unlockSecond := <-secondAcquired:
		unlockSecond()
		t.Fatal("second connection acquired the same artifact scan lock")
	case lockErr := <-secondErr:
		t.Fatal(lockErr)
	case <-time.After(100 * time.Millisecond):
	}
	unlockFirst()
	firstReleased = true
	select {
	case unlockSecond := <-secondAcquired:
		unlockSecond()
	case lockErr := <-secondErr:
		t.Fatal(lockErr)
	case <-time.After(2 * time.Second):
		t.Fatal("second connection did not acquire the released artifact scan lock")
	}
}
