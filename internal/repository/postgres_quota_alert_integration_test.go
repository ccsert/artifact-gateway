//go:build integration

package repository

import (
	"context"
	"github.com/artifact-gateway/artifact-gateway/internal/quotaalert"
	"github.com/google/uuid"
	"os"
	"testing"
)

func TestPostgresQuotaRuleReopensWithExplicitDisabledPolicy(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is required")
	}
	first, err := NewPostgresStore(url)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = first.Close() }()
	ctx := context.Background()
	name := "quota-pg-" + uuid.NewString()
	repo, err := first.CreateHostedRepository(ctx, HostedRepository{ID: uuid.NewString(), Name: name, Format: FormatRaw, Type: RepositoryTypeHosted, State: RepositoryActive})
	if err != nil {
		t.Fatal(err)
	}
	target, err := first.CreateEmailTarget(ctx, EmailTarget{ID: uuid.NewString(), Name: "Synthetic", Locale: "en", RecipientCiphertext: "synthetic"})
	if err != nil {
		t.Fatal(err)
	}
	want := RepositoryQuotaAlertRule{ID: uuid.NewString(), RepositoryID: repo.ID, TargetID: target.ID, TargetVersion: target.Version, Policy: quotaalert.Policy{WarningBasisPoints: 8500, CriticalBasisPoints: 9500, RecoveryBelowBasisPoints: 8000, WarningForSeconds: 300, CriticalForSeconds: 120, RecoveryForSeconds: 180, MaxSampleAgeSeconds: 60}}
	created, err := first.CreateRepositoryQuotaAlertRule(ctx, want)
	if err != nil {
		t.Fatalf("durable create: %v", err)
	}
	defer func() { _, _ = first.db.Exec(`DELETE FROM repository_quota_alert_rules WHERE id::text=$1`, created.ID) }()
	second, err := NewPostgresStore(url)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = second.Close() }()
	rules, err := second.ListRepositoryQuotaAlertRules(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range rules {
		if v.ID == created.ID {
			events, eventErr := second.ListRepositoryQuotaAlertEvents(ctx, created.ID)
			if eventErr != nil || len(events) != 0 {
				t.Fatalf("new rule events: %v %v", events, eventErr)
			}
			if v.Enabled || v.Version != "1" || v.State.Severity != "normal" || v.State.DataState != "disabled" || v.Policy != want.Policy {
				t.Fatalf("durability/default: %#v", v)
			}
			return
		}
	}
	t.Fatal("reopened process cannot query persisted rule")
}
