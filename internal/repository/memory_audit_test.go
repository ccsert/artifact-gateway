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
