//go:build integration

package repository

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestPostgresArtifactUsageAggregatesDownloadAudits(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is required for PostgreSQL integration tests")
	}
	store, err := NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	repositoryName := "usage-npm-" + uuid.NewString()
	otherName := "usage-raw-" + uuid.NewString()
	first := time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC)
	last := time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)
	t.Cleanup(func() {
		_, _ = store.db.ExecContext(context.Background(), `DELETE FROM artifact_usage_stats WHERE repository IN ($1, $2)`, repositoryName, otherName)
		_, _ = store.db.ExecContext(context.Background(), `DELETE FROM resolver_audit_log WHERE repository IN ($1, $2)`, repositoryName, otherName)
		_ = store.Close()
	})

	download := func(repo string, at time.Time, resource, actor string, bytes int64) {
		t.Helper()
		if err := store.RecordAudit(ctx, AuditRecord{
			Repository: repo, Format: "npm", Resource: resource, Actor: actor,
			Outcome: AuditResolved, Operation: "get", Status: 200, Bytes: bytes, OccurredAt: at,
		}); err != nil {
			t.Fatalf("RecordAudit: %v", err)
		}
	}
	download(repositoryName, first, "widget@1.0.0", "ci", 512)
	download(repositoryName, last, "widget@1.0.0", "deploy", 512)
	download(repositoryName, first.Add(-time.Minute), "widget@1.0.0", "old-request", 0)
	download(repositoryName, last, "widget@2.0.0", "ci", 256)
	// Publishes and management operations never count as downloads.
	publishes := []AuditRecord{
		{Repository: repositoryName, Format: "npm", Resource: "widget@1.0.0", Outcome: AuditResolved, Operation: "put", Status: 201, OccurredAt: last},
		{Repository: repositoryName, Format: "management", Resource: "lifecycle-jobs/x", Outcome: AuditResolved, Operation: "get", Status: 200, OccurredAt: last},
	}
	for _, record := range publishes {
		if err := store.RecordAudit(ctx, record); err != nil {
			t.Fatalf("RecordAudit publish: %v", err)
		}
	}

	stats, err := store.ListArtifactUsage(ctx, repositoryName, 100)
	if err != nil {
		t.Fatalf("ListArtifactUsage: %v", err)
	}
	if len(stats) != 2 {
		t.Fatalf("stats=%#v", stats)
	}
	top := stats[0]
	if top.Resource != "widget@1.0.0" || top.DownloadCount != 3 || top.TotalBytes != 1024 || top.LastActor != "deploy" {
		t.Fatalf("top=%#v", top)
	}
	if !top.FirstDownloadedAt.Equal(first.Add(-time.Minute)) || !top.LastDownloadedAt.Equal(last) {
		t.Fatalf("window first=%v last=%v", top.FirstDownloadedAt, top.LastDownloadedAt)
	}

	totals, err := store.ArtifactUsageTotals(ctx, repositoryName)
	if err != nil {
		t.Fatalf("ArtifactUsageTotals: %v", err)
	}
	if totals.DownloadCount != 4 || totals.TotalBytes != 1280 || totals.Resources != 2 {
		t.Fatalf("totals=%#v", totals)
	}

	if err := store.RecordAudit(ctx, AuditRecord{
		Repository: otherName, Format: "raw", Resource: "a.txt",
		Outcome: AuditResolved, Operation: "get", Status: 200, OccurredAt: last,
	}); err != nil {
		t.Fatalf("RecordAudit raw: %v", err)
	}
	rawTotals, err := store.ArtifactUsageTotals(ctx, otherName)
	if err != nil {
		t.Fatalf("ArtifactUsageTotals raw: %v", err)
	}
	if rawTotals.DownloadCount != 1 || rawTotals.Resources != 1 {
		t.Fatalf("raw totals=%#v", rawTotals)
	}
}
