//go:build integration

package repository

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	discovery "github.com/ccsert/artifact-gateway/internal/ocidiscovery"
	"github.com/google/uuid"
)

func openDiscoveryPostgres(t *testing.T) *PostgresStore {
	t.Helper()
	endpoint := os.Getenv("TEST_DATABASE_URL")
	if endpoint == "" {
		if os.Getenv("OCI_DISCOVERY_REQUIRED") == "1" {
			t.Fatal("owned TEST_DATABASE_URL is required")
		}
		t.Skip("PostgreSQL integration environment is required")
	}
	store, err := NewPostgresStore(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})
	return store
}

func TestPostgresOCIDiscoveryPublicationGuardsSerialize(t *testing.T) {
	for _, kind := range []string{"source", "first-grant", "group"} {
		t.Run(kind, func(t *testing.T) {
			store := openDiscoveryPostgres(t)
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			repo, err := store.CreateHostedRepository(ctx, HostedRepository{ID: uuid.NewString(), Name: "discovery-lock-" + uuid.NewString()[:8], Format: FormatOCI})
			if err != nil {
				t.Fatal(err)
			}
			guards := []discovery.Guard{{Kind: "repository", ID: repo.ID, Version: repo.Version}, {Kind: "grants", ID: repo.ID, Version: "1"}}
			group, _, err := store.CreateHostedGroupIdempotently(ctx, HostedGroup{ID: uuid.NewString(), Name: "discovery-group-" + uuid.NewString()[:8], Format: FormatOCI, Members: []GroupMember{{RepositoryID: repo.ID}}}, "fixture-admin", uuid.NewString(), "fixture")
			if err != nil {
				t.Fatal(err)
			}
			guards = append(guards, discovery.Guard{Kind: "group", ID: group.ID, Version: group.Version})
			snapshot := discovery.Snapshot{ID: uuid.NewString(), Owner: uuid.NewString(), Actor: "fixture-admin", Kind: "catalog", Guards: guards, Entries: []discovery.CatalogEntry{}, Coverage: []discovery.Coverage{}, ExpiresAt: time.Now().UTC().Add(discovery.SnapshotTTL)}
			key := int64(time.Now().UnixNano() & 0x7fffffff)
			gate, err := store.db.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				_, _ = gate.ExecContext(context.Background(), `SELECT pg_advisory_unlock($1)`, key)
				_ = gate.Close()
			}()
			if _, err := gate.ExecContext(ctx, `SELECT pg_advisory_lock($1)`, key); err != nil {
				t.Fatal(err)
			}
			name := "oci_discovery_gate_" + strings.ReplaceAll(uuid.NewString(), "-", "")
			var workers sync.WaitGroup
			query := fmt.Sprintf(`CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN PERFORM pg_advisory_xact_lock(%d::bigint); RETURN NEW; END; $$; CREATE TRIGGER %s BEFORE INSERT ON oci_discovery_snapshots FOR EACH ROW WHEN (NEW.id='%s'::uuid) EXECUTE FUNCTION %s()`, name, key, name, snapshot.ID, name)
			if _, err := store.db.ExecContext(ctx, query); err != nil {
				t.Fatal(err)
			}
			defer func() {
				cancel()
				_, _ = gate.ExecContext(context.Background(), `SELECT pg_advisory_unlock($1)`, key)
				workers.Wait()
				if _, err := store.db.ExecContext(context.Background(), fmt.Sprintf(`DROP TRIGGER IF EXISTS %s ON oci_discovery_snapshots; DROP FUNCTION IF EXISTS %s()`, name, name)); err != nil {
					t.Error(err)
				}
			}()
			published := make(chan error, 1)
			workers.Add(1)
			go func() { defer workers.Done(); published <- store.SaveOCIDiscoverySnapshot(ctx, snapshot) }()
			waitDiscoverySQL(t, ctx, store, `SELECT EXISTS(SELECT 1 FROM pg_locks WHERE locktype='advisory' AND NOT granted AND classid=0 AND objid=$1)`, key)
			application := "discovery-mutation-" + uuid.NewString()
			dsn, err := url.Parse(os.Getenv("TEST_DATABASE_URL"))
			if err != nil {
				t.Fatal(err)
			}
			q := dsn.Query()
			q.Set("application_name", application)
			dsn.RawQuery = q.Encode()
			mutator, err := NewPostgresStore(dsn.String())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := mutator.Close(); err != nil {
					t.Error(err)
				}
			})
			mutated := make(chan error, 1)
			workers.Add(1)
			go func() {
				defer workers.Done()
				var err error
				switch kind {
				case "source":
					_, err = mutator.UpdateHostedRepository(ctx, repo, repo.Version)
				case "first-grant":
					_, err = mutator.ReplaceRepositoryGrants(ctx, repo.ID, []RepositoryGrant{}, "1")
				case "group":
					_, err = mutator.ReplaceHostedGroup(ctx, group, group.Version)
				}
				mutated <- err
			}()
			waitDiscoverySQL(t, ctx, store, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE application_name=$1 AND wait_event_type='Lock')`, application)
			select {
			case err := <-mutated:
				t.Fatalf("mutation passed publication guard: %v", err)
			default:
			}
			if _, err := gate.ExecContext(ctx, `SELECT pg_advisory_unlock($1)`, key); err != nil {
				t.Fatal(err)
			}
			if err := <-published; err != nil {
				t.Fatal(err)
			}
			if err := <-mutated; err != nil {
				t.Fatal(err)
			}
			if _, err := store.GetOCIDiscoverySnapshot(ctx, snapshot.ID, snapshot.Owner); !errors.Is(err, discovery.ErrChanged) {
				t.Fatalf("post-mutation snapshot=%v", err)
			}
			snapshot.ID = uuid.NewString()
			if err := store.SaveOCIDiscoverySnapshot(ctx, snapshot); !errors.Is(err, discovery.ErrChanged) {
				t.Fatalf("source-first publication=%v", err)
			}
			var count int
			if err := store.db.QueryRowContext(ctx, `SELECT count(*) FROM oci_discovery_snapshots WHERE id=$1`, snapshot.ID).Scan(&count); err != nil || count != 0 {
				t.Fatalf("stale publication visible count=%d err=%v", count, err)
			}
		})
	}
}

func waitDiscoverySQL(t *testing.T, ctx context.Context, store *PostgresStore, query string, value any) {
	t.Helper()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var done bool
		if err := store.db.QueryRowContext(ctx, query, value).Scan(&done); err != nil {
			t.Fatal(err)
		}
		if done {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("database lock witness missing: %v", ctx.Err())
		case <-ticker.C:
		}
	}
}

func TestPostgresOCIDiscoverySnapshotAndBinding(t *testing.T) {
	store := openDiscoveryPostgres(t)
	testOCIDiscoveryStore(t, store, store)
}

func TestPostgresOCIDiscoverySnapshotReopenAndGrantFence(t *testing.T) {
	store := openDiscoveryPostgres(t)
	ctx := t.Context()
	repo, err := store.CreateHostedRepository(ctx, HostedRepository{ID: uuid.NewString(), Name: "discovery-" + uuid.NewString()[:8], Format: FormatOCI})
	if err != nil {
		t.Fatal(err)
	}
	snapshot := discovery.Snapshot{ID: uuid.NewString(), Owner: uuid.NewString(), Actor: "discovery-reader", Kind: "catalog", Guards: []discovery.Guard{{Kind: "repository", ID: repo.ID, Version: repo.Version}, {Kind: "grants", ID: repo.ID, Version: "1"}}, Entries: []discovery.CatalogEntry{{Name: repo.Name + "/app", Sources: []string{repo.Name}}}, Coverage: []discovery.Coverage{{Namespace: repo.Name, State: "complete"}}, ExpiresAt: time.Now().UTC().Add(discovery.SnapshotTTL)}
	if err := store.SaveOCIDiscoverySnapshot(ctx, snapshot); err != nil {
		t.Fatal(err)
	}
	reopened := openDiscoveryPostgres(t)
	if _, err := reopened.GetOCIDiscoverySnapshot(ctx, snapshot.ID, snapshot.Owner); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReplaceRepositoryGrants(ctx, repo.ID, []RepositoryGrant{}, "1"); err != nil {
		t.Fatal(err)
	}
	if _, err := reopened.GetOCIDiscoverySnapshot(ctx, snapshot.ID, snapshot.Owner); !errors.Is(err, discovery.ErrChanged) {
		t.Fatalf("reopened stale grant snapshot=%v", err)
	}
	if err := reopened.SaveOCIDiscoverySnapshot(ctx, snapshot); !errors.Is(err, discovery.ErrChanged) {
		t.Fatalf("stale grant save=%v", err)
	}
	var count int
	if err := store.db.QueryRowContext(ctx, `SELECT count(*) FROM oci_discovery_snapshots WHERE id=$1`, snapshot.ID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("immutable row count=%d err=%v", count, err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	snapshot.ID = uuid.NewString()
	if err := reopened.SaveOCIDiscoverySnapshot(cancelled, snapshot); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled snapshot=%v", err)
	}
}
