package repository

import (
	"context"
	"testing"
	"time"
)

func TestMemoryStoreListAuditsAppliesAllFilters(t *testing.T) {
	store := NewMemoryStore()
	when := time.Now().UTC()
	records := []AuditRecord{
		{Repository: "public", GroupName: "main", Outcome: AuditResolved, Format: "maven", Operation: "get", Actor: "alice", OccurredAt: when.Add(-2 * time.Minute)},
		{Repository: "public", GroupName: "main", Outcome: AuditAccessDenied, Format: "maven", Operation: "get", Actor: "bob", OccurredAt: when.Add(-time.Minute)},
		{Repository: "private", GroupName: "main", Outcome: AuditResolved, Format: "oci", Operation: "put", Actor: "alice", OccurredAt: when},
	}
	for _, record := range records {
		if err := store.RecordAudit(context.Background(), record); err != nil {
			t.Fatalf("RecordAudit() error = %v", err)
		}
	}

	got, err := store.ListAudits(context.Background(), AuditQuery{
		Repository: "public",
		Outcome:    string(AuditAccessDenied),
		Format:     "maven",
		Operation:  "get",
		Actor:      "bob",
		Limit:      10,
	})
	if err != nil {
		t.Fatalf("ListAudits() error = %v", err)
	}
	if len(got) != 1 || got[0].Actor != "bob" {
		t.Fatalf("ListAudits() = %#v, want only Bob's denied Maven read", got)
	}
}

func TestMemoryStoreAuditEvidenceIsImmutableAcrossCallers(t *testing.T) {
	store := NewMemoryStore()
	evidence := map[string]string{"keyFingerprint": "trusted"}
	if err := store.RecordAudit(context.Background(), AuditRecord{Repository: "apt", Outcome: AuditResolved, Evidence: evidence}); err != nil {
		t.Fatal(err)
	}
	evidence["keyFingerprint"] = "mutated-before-read"
	first, err := store.ListAudits(context.Background(), AuditQuery{Repository: "apt", Limit: 10})
	if err != nil || len(first) != 1 || first[0].Evidence["keyFingerprint"] != "trusted" {
		t.Fatalf("first audit=%#v err=%v", first, err)
	}
	first[0].Evidence["keyFingerprint"] = "mutated-after-read"
	second, err := store.ListAudits(context.Background(), AuditQuery{Repository: "apt", Limit: 10})
	if err != nil || len(second) != 1 || second[0].Evidence["keyFingerprint"] != "trusted" {
		t.Fatalf("second audit=%#v err=%v", second, err)
	}
}

func TestMemoryStoreAuditPageUsesStableCursorAndTimeBounds(t *testing.T) {
	store := NewMemoryStore()
	base := time.Date(2026, 8, 8, 10, 0, 0, 0, time.UTC)
	for index := 0; index < 3; index++ {
		if err := store.RecordAudit(context.Background(), AuditRecord{
			Repository: "public", GroupName: "main", Actor: "build", Outcome: AuditResolved,
			OccurredAt: base.Add(time.Duration(index) * time.Minute),
		}); err != nil {
			t.Fatal(err)
		}
	}
	page, err := store.ListAuditPage(context.Background(), AuditQuery{Repository: "public", Limit: 1, From: base.Add(time.Minute)})
	if err != nil || len(page.Items) != 1 || page.Items[0].OccurredAt != base.Add(2*time.Minute) || page.Next == nil {
		t.Fatalf("first page=%#v err=%v", page, err)
	}
	next, err := store.ListAuditPage(context.Background(), AuditQuery{Repository: "public", Limit: 1, From: base.Add(time.Minute), Before: *page.Next})
	if err != nil || len(next.Items) != 1 || next.Items[0].OccurredAt != base.Add(time.Minute) || next.Next != nil {
		t.Fatalf("second page=%#v err=%v", next, err)
	}
}
