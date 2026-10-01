//go:build integration

package repository

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestPostgresRepositoryRequestStatisticsAndIndex(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is required for PostgreSQL integration tests")
	}
	store, err := NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	first, second := "overview-"+uuid.NewString(), "overview-"+uuid.NewString()
	t.Cleanup(func() {
		_, _ = store.db.ExecContext(context.Background(), `DELETE FROM resolver_audit_log WHERE repository IN ($1, $2)`, first, second)
		_ = store.Close()
	})
	var indexName string
	if err := store.db.QueryRowContext(ctx, `SELECT indexname FROM pg_indexes WHERE tablename='resolver_audit_log' AND indexname='resolver_audit_log_repository_occurred_at_idx'`).Scan(&indexName); err != nil {
		t.Fatalf("statistics migration index: %v", err)
	}
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	for _, record := range []AuditRecord{
		{Repository: first, Format: "cargo", Outcome: AuditResolved, OccurredAt: now.Add(-24 * time.Hour)},
		{Repository: first, Format: "cargo", Outcome: AuditAccessDenied, OccurredAt: now.Add(-24*time.Hour - time.Microsecond)},
		{Repository: first, Format: "cargo", Outcome: AuditProxyDenied, OccurredAt: now.Add(-7 * 24 * time.Hour)},
		{Repository: first, Format: "cargo", Outcome: AuditResolved, OccurredAt: now.Add(-30*24*time.Hour - time.Microsecond)},
		{Repository: first, Format: "management", Outcome: AuditResolved, OccurredAt: now},
		{Repository: second, Format: "raw", Outcome: AuditResolved, OccurredAt: now},
	} {
		if err := store.RecordAudit(ctx, record); err != nil {
			t.Fatal(err)
		}
	}
	stats, err := store.ListRepositoryRequestStatistics(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	byRepository := make(map[string]RepositoryRequestStatistics, len(stats))
	for _, item := range stats {
		byRepository[item.Repository] = item
	}
	if got := byRepository[first]; got.Requests != (RequestWindowCounts{OneDay: 1, SevenDays: 3, ThirtyDays: 3}) || got.Denied != (RequestWindowCounts{OneDay: 0, SevenDays: 2, ThirtyDays: 2}) {
		t.Fatalf("first statistics=%#v", got)
	}
	if got := byRepository[second]; got.Requests != (RequestWindowCounts{OneDay: 1, SevenDays: 1, ThirtyDays: 1}) {
		t.Fatalf("second statistics=%#v", got)
	}
}
