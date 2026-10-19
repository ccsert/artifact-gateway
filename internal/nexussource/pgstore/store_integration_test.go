//go:build integration

package pgstore_test

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"

	source "github.com/ccsert/artifact-gateway/internal/nexussource"
	"github.com/ccsert/artifact-gateway/internal/nexussource/pgstore"
	"github.com/ccsert/artifact-gateway/internal/secrets"
	"github.com/ccsert/artifact-gateway/internal/testsupport"
	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
)

func TestPostgresSourcePagingCASAndCredentialKeyLifecycle(t *testing.T) {
	raw := os.Getenv("TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("TEST_DATABASE_URL is required")
	}
	if err := testsupport.ValidateIsolatedDatabaseURL(raw); err != nil {
		t.Fatal(err)
	}
	t.Setenv(secrets.KeyEnv, "0123456789abcdef0123456789abcdef")
	ctx := context.Background()
	control, err := sql.Open("pgx", raw)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = control.Close() })
	schema := "source_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := control.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := control.ExecContext(ctx, "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Error(err)
		}
	})
	if _, err := control.ExecContext(ctx, "CREATE TABLE "+schema+".nexus_migration_sources (LIKE public.nexus_migration_sources INCLUDING ALL)"); err != nil {
		t.Fatal(err)
	}
	parsed, _ := url.Parse(raw)
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	db, err := sql.Open("pgx", parsed.String())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	service := source.New(pgstore.New(db), nil, source.Options{})
	created := []source.Source{}
	for _, name := range []string{"one", "two", "three"} {
		v, err := service.Create(ctx, source.Input{Name: name, BaseURL: "https://example.com", Credentials: &source.Credentials{Username: "synthetic-reader", Password: "synthetic-password"}})
		if err != nil {
			t.Fatal(err)
		}
		created = append(created, v)
	}
	first, next, err := service.List(ctx, 2, "")
	if err != nil || len(first) != 2 || next == "" {
		t.Fatalf("first=%v next=%s err=%v", first, next, err)
	}
	last, end, err := service.List(ctx, 2, next)
	if err != nil || len(last) != 1 || end != "" || last[0].ID == first[0].ID || last[0].ID == first[1].ID {
		t.Fatalf("last=%v end=%s err=%v", last, end, err)
	}
	id := created[0].ID
	// Two API instances race the same version; exactly one can commit.
	second, err := sql.Open("pgx", parsed.String())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = second.Close() }()
	other := source.New(pgstore.New(second), nil, source.Options{})
	start := make(chan struct{})
	results := make(chan error, 2)
	var workers sync.WaitGroup
	for _, client := range []*source.Service{service, other} {
		workers.Add(1)
		go func(client *source.Service) {
			defer workers.Done()
			<-start
			_, err := client.Update(ctx, id, source.Input{Name: "renamed", BaseURL: "https://example.com"}, "1")
			results <- err
		}(client)
	}
	close(start)
	workers.Wait()
	close(results)
	success, conflicts := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else if errors.Is(err, source.ErrVersionConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || conflicts != 1 {
		t.Fatalf("success=%d conflicts=%d", success, conflicts)
	}
	got, err := other.Get(ctx, id)
	if err != nil || got.Version != "2" || got.CredentialCiphertext != created[0].CredentialCiphertext {
		t.Fatalf("after CAS=%v err=%v", got, err)
	}
	// Wrong/missing key cannot turn encrypted source credentials into anonymous access.
	t.Setenv(secrets.KeyEnv, "abcdef0123456789abcdef0123456789ab")
	_, err = other.Preflight(ctx, id)
	var failure *source.Failure
	if !errors.As(err, &failure) || failure.Code != "encryption_key_unavailable" {
		t.Fatalf("wrong key=%v", err)
	}
	if err := other.Delete(ctx, id, "1"); !errors.Is(err, source.ErrVersionConflict) {
		t.Fatalf("stale delete=%v", err)
	}
	if err := other.Delete(ctx, id, "2"); err != nil {
		t.Fatal(err)
	}
	if _, err := other.Get(ctx, id); !errors.Is(err, source.ErrNotFound) {
		t.Fatalf("deleted credentials remain=%v", err)
	}
}
