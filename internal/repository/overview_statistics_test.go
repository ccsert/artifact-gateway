package repository

import (
	"context"
	"testing"
	"time"
)

func TestMemoryRepositoryRequestStatisticsWindows(t *testing.T) {
	store := NewMemoryStore()
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	for _, record := range []AuditRecord{
		{Repository: "first", Format: "cargo", Outcome: AuditResolved, OccurredAt: now.Add(-24 * time.Hour)},
		{Repository: "first", Format: "cargo", Outcome: AuditAccessDenied, OccurredAt: now.Add(-24*time.Hour - time.Nanosecond)},
		{Repository: "first", Format: "cargo", Outcome: AuditProxyDenied, OccurredAt: now.Add(-7 * 24 * time.Hour)},
		{Repository: "first", Format: "cargo", Outcome: AuditResolved, OccurredAt: now.Add(-30 * 24 * time.Hour)},
		{Repository: "first", Format: "cargo", Outcome: AuditResolved, OccurredAt: now.Add(-30*24*time.Hour - time.Nanosecond)},
		{Repository: "first", Format: "management", Outcome: AuditAccessDenied, OccurredAt: now},
		{Repository: "", Format: "cargo", Outcome: AuditAccessDenied, OccurredAt: now},
		{Repository: "first", Format: "cargo", Outcome: AuditResolved, OccurredAt: now.Add(time.Nanosecond)},
		{Repository: "second", Format: "raw", Outcome: AuditResolved, OccurredAt: now},
	} {
		if err := store.RecordAudit(context.Background(), record); err != nil {
			t.Fatal(err)
		}
	}
	stats, err := store.ListRepositoryRequestStatistics(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	if len(stats) != 2 || stats[0].Repository != "first" || stats[1].Repository != "second" {
		t.Fatalf("statistics: %#v", stats)
	}
	if want := (RequestWindowCounts{OneDay: 1, SevenDays: 3, ThirtyDays: 4}); stats[0].Requests != want {
		t.Fatalf("requests=%#v, want %#v", stats[0].Requests, want)
	}
	if want := (RequestWindowCounts{OneDay: 0, SevenDays: 2, ThirtyDays: 2}); stats[0].Denied != want {
		t.Fatalf("denied=%#v, want %#v", stats[0].Denied, want)
	}
	if stats[1].Requests != (RequestWindowCounts{1, 1, 1}) {
		t.Fatalf("second requests=%#v", stats[1].Requests)
	}
}
