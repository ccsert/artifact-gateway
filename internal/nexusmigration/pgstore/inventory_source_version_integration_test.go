//go:build integration

package pgstore_test

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	m "github.com/ccsert/artifact-gateway/internal/nexusmigration"
	"github.com/ccsert/artifact-gateway/internal/nexusmigration/pgstore"
	"github.com/ccsert/artifact-gateway/internal/nexussource"
	sourcepg "github.com/ccsert/artifact-gateway/internal/nexussource/pgstore"
	"github.com/google/uuid"
)

// Source.Update must not return a new version and then allow any old-bound
// receipt or executable manifest to commit, even after the last HTTP check.
func TestPostgresMigrationInventoryRejectsSourceUpdateBeforeMutation(t *testing.T) {
	for _, phase := range []string{"begin", "append", "verify", "seal"} {
		t.Run(phase, func(t *testing.T) {
			f := newFixture(t, "raw", map[string][]byte{"a.bin": []byte("a")})
			if phase == "seal" {
				f.service = f.scanService(inventorySealControl{Store: f.ledger, control: func() error {
					_, err := f.source.Update(t.Context(), f.sourceID, nexussource.Input{Name: "owned-changed-source", BaseURL: "https://example.com"}, f.input.SourceVersion)
					return err
				}}, f.source)
				b := f.start()
				f.run(b)
				if b.LastError != "source_changed" {
					t.Fatalf("completed source update did not reject old manifest: %+v", b)
				}
				f.assertUnsealed(*b)
				return
			}

			b := f.start()
			owner, err := f.ledger.Claim(t.Context(), []string{"raw"})
			if err != nil || owner.ID != b.ID {
				t.Fatalf("claim=%+v err=%v", owner, err)
			}
			repos, err := f.source.InventoryRepositories(t.Context(), b.SourceID, b.SourceVersion, []string{"source"})
			if err != nil {
				t.Fatal(err)
			}
			initial := m.InventoryScan{BindingSHA256: m.InventoryBinding(owner, repos), Repositories: repos}
			if phase != "begin" {
				if err = f.ledger.BeginInventoryScan(t.Context(), owner, initial); err != nil {
					t.Fatal(err)
				}
			}
			page, err := f.source.ReadInventoryPage(t.Context(), b.SourceID, b.SourceVersion, repos[0], "")
			if err != nil {
				t.Fatal(err)
			}
			if phase == "verify" {
				if err = f.ledger.AppendInventoryPage(t.Context(), owner, f.scanCheckpoint(b.ID), page); err != nil {
					t.Fatal(err)
				}
			}
			updated, err := f.source.Update(t.Context(), f.sourceID, nexussource.Input{Name: "owned-changed-source", BaseURL: "https://example.com"}, owner.SourceVersion)
			if err != nil || updated.Version == owner.SourceVersion {
				t.Fatalf("source update=%+v err=%v", updated, err)
			}
			switch phase {
			case "begin":
				err = f.ledger.BeginInventoryScan(t.Context(), owner, initial)
			case "append":
				err = f.ledger.AppendInventoryPage(t.Context(), owner, f.scanCheckpoint(b.ID), page)
			case "verify":
				err = f.ledger.VerifyInventoryPage(t.Context(), owner, 0, m.InventoryPageDigest(page), page.MetadataBytes)
			}
			requireSourceChanged(t, err)
			if phase == "begin" {
				if _, err = f.ledger.InventoryScan(t.Context(), b.ID); !errors.Is(err, m.ErrNotFound) {
					t.Fatalf("old source initialized a scan: %v", err)
				}
			} else if scan := f.scanCheckpoint(b.ID); scan.VerifiedPages != 0 || phase == "append" && scan.PageCount != 0 {
				t.Fatalf("old source advanced a receipt: %+v", scan)
			}
			current, err := f.ledger.Get(t.Context(), b.ID)
			if err != nil {
				t.Fatal(err)
			}
			f.assertUnsealed(current)
		})
	}
}

// Both lock orders are observable through pg_blocking_pids. The winning
// transaction commits first; an update that wins prevents the old inventory
// mutation, while a receipt/Seal that wins holds its source binding until commit.
func TestPostgresMigrationInventorySourceUpdateSerializesWithTransactions(t *testing.T) {
	for _, phase := range []string{"append", "seal"} {
		for _, first := range []string{"source", "inventory"} {
			t.Run(phase+"/"+first+"-first", func(t *testing.T) {
				f := newFixture(t, "raw", map[string][]byte{"a.bin": []byte("a")})
				b := f.start()
				owner, err := f.ledger.Claim(t.Context(), []string{"raw"})
				if err != nil || owner.ID != b.ID {
					t.Fatalf("claim=%+v err=%v", owner, err)
				}
				repos, err := f.source.InventoryRepositories(t.Context(), b.SourceID, b.SourceVersion, []string{"source"})
				if err != nil {
					t.Fatal(err)
				}
				if err = f.ledger.BeginInventoryScan(t.Context(), owner, m.InventoryScan{BindingSHA256: m.InventoryBinding(owner, repos), Repositories: repos}); err != nil {
					t.Fatal(err)
				}
				page, err := f.source.ReadInventoryPage(t.Context(), b.SourceID, b.SourceVersion, repos[0], "")
				if err != nil {
					t.Fatal(err)
				}
				if phase == "seal" {
					if err = f.ledger.AppendInventoryPage(t.Context(), owner, f.scanCheckpoint(b.ID), page); err != nil {
						t.Fatal(err)
					}
					if err = f.ledger.VerifyInventoryPage(t.Context(), owner, 0, m.InventoryPageDigest(page), page.MetadataBytes); err != nil {
						t.Fatal(err)
					}
				}
				checkpoint := f.scanCheckpoint(b.ID)
				writer, writerPID := ownedInventoryConnection(t, f.connection)
				updater, updaterPID := ownedInventoryConnection(t, f.connection)
				source := nexussource.New(sourcepg.New(updater), f.native, nexussource.Options{})
				ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
				defer cancel()
				barrier, err := f.db.BeginTx(ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = barrier.Rollback() }()
				var barrierPID int
				key := "owned-source-race-" + uuid.NewString()
				if err = barrier.QueryRowContext(ctx, `SELECT pg_backend_pid() FROM pg_advisory_xact_lock(hashtextextended($1,0))`, key).Scan(&barrierPID); err != nil {
					t.Fatal(err)
				}
				name := "owned_source_" + strings.ReplaceAll(uuid.NewString(), "-", "")
				table, operation, condition := "nexus_migration_scan_paths", "INSERT", "NEW.batch_id='"+b.ID+"'::uuid"
				if phase == "seal" {
					table = "nexus_migration_entries"
				}
				if first == "source" {
					table, operation, condition = "nexus_migration_sources", "UPDATE", "NEW.id='"+f.sourceID+"'::uuid"
				}
				if _, err = f.db.ExecContext(ctx, `CREATE FUNCTION `+name+`() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF `+condition+` THEN PERFORM pg_advisory_xact_lock(hashtextextended('`+key+`',0)); END IF; RETURN NEW; END $$`); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if _, err := f.db.Exec(`DROP FUNCTION IF EXISTS ` + name + `() CASCADE`); err != nil {
						t.Errorf("remove owned source barrier: %v", err)
					}
				})
				if _, err = f.db.ExecContext(ctx, `CREATE TRIGGER `+name+` BEFORE `+operation+` ON `+table+` FOR EACH ROW EXECUTE FUNCTION `+name+`() `); err != nil {
					t.Fatal(err)
				}
				inventoryDone, sourceDone := make(chan error, 1), make(chan error, 1)
				mutate := func() {
					store := pgstore.New(writer)
					if phase == "append" {
						inventoryDone <- store.AppendInventoryPage(ctx, owner, checkpoint, page)
						return
					}
					asset := page.Assets[0]
					entry := m.Entry{ID: uuid.NewString(), BatchID: b.ID, SourceRepository: "source", TargetRepositoryID: f.repo.ID, Format: "raw", Identity: asset.Path, State: "pending", Assets: []m.Asset{{SourcePath: asset.Path, TargetPath: asset.Path, Size: asset.Size, SHA256: asset.Checksum["sha256"]}}}
					inventoryDone <- store.Seal(ctx, owner, []m.Entry{entry}, strings.Repeat("a", 64), 1, asset.Size)
				}
				update := func() {
					updated, err := source.Update(ctx, f.sourceID, nexussource.Input{Name: "owned-concurrent-source", BaseURL: "https://example.com"}, owner.SourceVersion)
					if err == nil && updated.Version == owner.SourceVersion {
						err = errors.New("source update did not change its version")
					}
					sourceDone <- err
				}
				if first == "source" {
					go update()
					waitInventoryBlocker(t, ctx, f.db, updaterPID, barrierPID, sourceDone)
					go mutate()
					waitInventoryBlocker(t, ctx, f.db, writerPID, updaterPID, inventoryDone)
				} else {
					go mutate()
					waitInventoryBlocker(t, ctx, f.db, writerPID, barrierPID, inventoryDone)
					go update()
					waitInventoryBlocker(t, ctx, f.db, updaterPID, writerPID, sourceDone)
				}
				if err = barrier.Rollback(); err != nil {
					t.Fatal(err)
				}
				if err = <-sourceDone; err != nil {
					t.Fatal(err)
				}
				err = <-inventoryDone
				current, readErr := f.ledger.Get(t.Context(), b.ID)
				if readErr != nil {
					t.Fatal(readErr)
				}
				if first == "source" {
					requireSourceChanged(t, err)
					f.assertUnsealed(current)
					if after := f.scanCheckpoint(b.ID); after.PageCount != checkpoint.PageCount || after.VerifiedPages != checkpoint.VerifiedPages {
						t.Fatalf("source update winner allowed stale progress: before=%+v after=%+v", checkpoint, after)
					}
				} else if err != nil {
					t.Fatal(err)
				} else if phase == "seal" {
					if !current.ScanComplete || current.EntryCount != 1 || len(f.entries(b.ID)) != 1 {
						t.Fatalf("winning seal was not atomic: %+v", current)
					}
				} else {
					if after := f.scanCheckpoint(b.ID); after.PageCount != 1 {
						t.Fatalf("winning page was not committed: %+v", after)
					}
					requireSourceChanged(t, f.ledger.VerifyInventoryPage(t.Context(), owner, 0, m.InventoryPageDigest(page), page.MetadataBytes))
					f.assertUnsealed(current)
				}
				t.Logf("%s %s-first: actual PostgreSQL blocking chain observed, source update and inventory transaction completed without deadlock", phase, first)
			})
		}
	}
}

func requireSourceChanged(t *testing.T, err error) {
	t.Helper()
	var failure *m.Failure
	if !errors.As(err, &failure) || failure.Code != "source_changed" {
		t.Fatalf("old source mutation must fail source_changed: %v", err)
	}
}

func ownedInventoryConnection(t *testing.T, connection string) (*sql.DB, int) {
	t.Helper()
	db, err := sql.Open("pgx", connection)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)
	var pid int
	if err = db.QueryRowContext(t.Context(), `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	return db, pid
}

func waitInventoryBlocker(t *testing.T, ctx context.Context, db *sql.DB, pid, blocker int, done <-chan error) {
	t.Helper()
	for {
		select {
		case err := <-done:
			t.Fatalf("transaction %d completed without waiting on %d: %v", pid, blocker, err)
		case <-ctx.Done():
			t.Fatalf("transaction %d never waited on %d: %v", pid, blocker, ctx.Err())
		default:
		}
		var blocked bool
		if err := db.QueryRowContext(ctx, `SELECT $2=ANY(pg_blocking_pids($1))`, pid, blocker).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
}
