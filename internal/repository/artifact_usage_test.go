package repository

import (
	"context"
	"testing"
	"time"
)

func TestAuditRecordIsArtifactDownload(t *testing.T) {
	base := AuditRecord{
		Repository: "npm-hosted", Format: "npm", Resource: "widget@1.0.0",
		Outcome: AuditResolved, Operation: "get", Status: 200, OccurredAt: time.Now().UTC(),
	}
	cases := []struct {
		name   string
		mutate func(*AuditRecord)
		want   bool
	}{
		{"resolved get 200", func(*AuditRecord) {}, true},
		{"conditional revalidation 304", func(a *AuditRecord) { a.Status = 304 }, false},
		{"head probe", func(a *AuditRecord) { a.Operation = "head" }, false},
		{"publish", func(a *AuditRecord) { a.Operation = "put"; a.Status = 201 }, false},
		{"not found", func(a *AuditRecord) { a.Outcome = AuditNotFound; a.Status = 404 }, false},
		{"storage error", func(a *AuditRecord) { a.Outcome = AuditStorageError; a.Status = 503 }, false},
		{"management operation", func(a *AuditRecord) { a.Format = "management" }, false},
		{"legacy audit without format", func(a *AuditRecord) { a.Format = "" }, false},
		{"missing resource", func(a *AuditRecord) { a.Resource = "" }, false},
		{"missing repository", func(a *AuditRecord) { a.Repository = "" }, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			record := base
			tc.mutate(&record)
			if got := record.IsArtifactDownload(); got != tc.want {
				t.Fatalf("IsArtifactDownload=%v want %v", got, tc.want)
			}
		})
	}
}

func TestMemoryStoreAuditAggregatesArtifactUsage(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()
	first := time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC)
	last := time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)

	download := func(at time.Time, resource, actor string, bytes int64) {
		if err := store.RecordAudit(ctx, AuditRecord{
			Repository: "npm-hosted", Format: "npm", Resource: resource, Actor: actor,
			Outcome: AuditResolved, Operation: "get", Status: 200, Bytes: bytes, OccurredAt: at,
		}); err != nil {
			t.Fatalf("RecordAudit: %v", err)
		}
	}
	download(first, "widget@1.0.0", "ci", 512)
	download(first.Add(time.Minute), "widget@1.0.0", "ci", 512)
	download(last, "widget@1.0.0", "deploy", 512)
	download(first.Add(-time.Minute), "widget@1.0.0", "old-request", 0)
	download(last, "widget@2.0.0", "deploy", 256)
	// Non-downloads must not inflate usage.
	noise := []AuditRecord{
		{Repository: "npm-hosted", Format: "npm", Resource: "widget@1.0.0", Outcome: AuditResolved, Operation: "head", Status: 200, OccurredAt: last},
		{Repository: "npm-hosted", Format: "npm", Resource: "widget@1.0.0", Outcome: AuditResolved, Operation: "get", Status: 304, OccurredAt: last},
		{Repository: "npm-hosted", Format: "management", Resource: "lifecycle-jobs/x", Outcome: AuditResolved, Operation: "get", Status: 200, OccurredAt: last},
		{Repository: "npm-hosted", Format: "npm", Resource: "widget@1.0.0", Outcome: AuditNotFound, Operation: "get", Status: 404, OccurredAt: last},
		{Repository: "other-repo", Format: "raw", Resource: "a.txt", Outcome: AuditResolved, Operation: "get", Status: 200, OccurredAt: last},
	}
	for _, record := range noise {
		if err := store.RecordAudit(ctx, record); err != nil {
			t.Fatalf("RecordAudit noise: %v", err)
		}
	}

	stats, err := store.ListArtifactUsage(ctx, "npm-hosted", 100)
	if err != nil {
		t.Fatalf("ListArtifactUsage: %v", err)
	}
	if len(stats) != 2 {
		t.Fatalf("stats=%#v", stats)
	}
	top := stats[0]
	if top.Resource != "widget@1.0.0" || top.DownloadCount != 4 || top.TotalBytes != 1536 {
		t.Fatalf("top=%#v", top)
	}
	if !top.FirstDownloadedAt.Equal(first.Add(-time.Minute)) || !top.LastDownloadedAt.Equal(last) {
		t.Fatalf("window first=%v last=%v", top.FirstDownloadedAt, top.LastDownloadedAt)
	}
	if top.LastActor != "deploy" {
		t.Fatalf("lastActor=%q", top.LastActor)
	}
	if stats[1].Resource != "widget@2.0.0" || stats[1].DownloadCount != 1 {
		t.Fatalf("second=%#v", stats[1])
	}

	totals, err := store.ArtifactUsageTotals(ctx, "npm-hosted")
	if err != nil {
		t.Fatalf("ArtifactUsageTotals: %v", err)
	}
	if totals.DownloadCount != 5 || totals.TotalBytes != 1792 || totals.Resources != 2 {
		t.Fatalf("totals=%#v", totals)
	}

	other, err := store.ListArtifactUsage(ctx, "other-repo", 100)
	if err != nil {
		t.Fatalf("ListArtifactUsage other: %v", err)
	}
	if len(other) != 1 || other[0].Resource != "a.txt" || other[0].DownloadCount != 1 {
		t.Fatalf("other=%#v", other)
	}

	limited, err := store.ListArtifactUsage(ctx, "", 1)
	if err != nil {
		t.Fatalf("ListArtifactUsage limited: %v", err)
	}
	if len(limited) != 1 {
		t.Fatalf("limit ignored: %#v", limited)
	}
}
