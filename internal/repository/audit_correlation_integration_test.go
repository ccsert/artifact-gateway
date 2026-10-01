//go:build integration

package repository

import (
	"context"
	"os"
	"testing"

	"github.com/artifact-gateway/artifact-gateway/internal/requestcontext"
	"github.com/google/uuid"
)

func TestPostgresAuditCarriesRequestCorrelation(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is required")
	}
	store, err := NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	group := "correlation-" + uuid.NewString()
	ctx, ids := requestcontext.WithRequest(context.Background(), "request-42")
	if err := store.RecordAudit(ctx, AuditRecord{GroupName: group, Repository: group, Actor: "test", Outcome: AuditResolved, Format: "raw", Resource: "test.txt", Operation: "get"}); err != nil {
		t.Fatal(err)
	}
	page, err := store.ListAuditPage(context.Background(), AuditQuery{GroupName: group, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].RequestID != ids.RequestID || page.Items[0].TraceID != ids.TraceID {
		t.Fatalf("persisted correlation=%#v", page.Items)
	}
}
