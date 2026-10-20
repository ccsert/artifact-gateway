//go:build integration

package pgstore_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	m "github.com/ccsert/artifact-gateway/internal/nexusmigration"
	"github.com/ccsert/artifact-gateway/internal/nexussource"
	sourcepg "github.com/ccsert/artifact-gateway/internal/nexussource/pgstore"
	"github.com/google/uuid"
)

func TestPostgresMigrationInventorySourceUpdateAfterFinalCheckCannotPublish(t *testing.T) {
	for _, format := range []string{"raw", "maven2"} {
		t.Run(format, func(t *testing.T) {
			f := guardedFixture(t, format)
			intercept := &interceptedPublication{Publication: f.publication, finalVerify: func(ctx context.Context) error {
				_, err := f.source.Update(ctx, f.sourceID, nexussource.Input{Name: "owned-final-verify-update", BaseURL: "https://example.com"}, f.input.SourceVersion)
				return err
			}}
			f.service = m.New(f.ledger, f.source, intercept, m.Options{TargetID: "owned-target", PoolID: "owned-pool", HeartbeatInterval: time.Hour})
			b := f.start()
			f.run(b)
			if intercept.verifies.Load() != 2 || !b.ScanComplete || b.VisibleIdentities != 0 {
				t.Fatalf("final verified source change crossed native publication: verifies=%d batch=%+v", intercept.verifies.Load(), b)
			}
			entries := f.entries(b.ID)
			// The native publication adapter deliberately exposes a finite error
			// code rather than its transaction guard's internal failure.
			if len(entries) != 1 || entries[0].State != "failed" || entries[0].LastError != "native_publish_failed" {
				t.Fatalf("source change did not fail the native transaction: %+v", entries)
			}
			requireNoNativePublication(t, f, format)
		})
	}
}

func TestPostgresMigrationInventoryPublicationSourceLockAndLease(t *testing.T) {
	for _, format := range []string{"raw", "maven2"} {
		for _, first := range []string{"source", "publication", "source-lock-lease-expired"} {
			t.Run(format+"/"+first, func(t *testing.T) {
				f := guardedFixture(t, format)
				ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
				defer cancel()
				verified, publish := make(chan struct{}), make(chan struct{})
				inNative, commit := make(chan int, 1), make(chan struct{})
				intercept := &interceptedPublication{Publication: f.publication, finalVerify: func(ctx context.Context) error {
					close(verified)
					select {
					case <-publish:
						return nil
					case <-ctx.Done():
						return ctx.Err()
					}
				}}
				if first == "publication" {
					intercept.nativeGuard = func(ctx context.Context, tx *sql.Tx) error {
						var pid int
						if err := tx.QueryRowContext(ctx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
							return err
						}
						inNative <- pid
						select {
						case <-commit:
							return nil
						case <-ctx.Done():
							return ctx.Err()
						}
					}
				}
				f.service = m.New(f.ledger, f.source, intercept, m.Options{TargetID: "owned-target", PoolID: "owned-pool", HeartbeatInterval: time.Hour})
				b := f.start()
				done := make(chan error, 1)
				go func() { done <- f.service.RunOnce(ctx, []string{"raw", "maven"}) }()
				select {
				case <-verified:
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
				updater, updaterPID := ownedInventoryConnection(t, f.connection)
				source := nexussource.New(sourcepg.New(updater), f.native, nexussource.Options{})
				updated := make(chan error, 1)
				update := func() {
					_, err := source.Update(ctx, f.sourceID, nexussource.Input{Name: "owned-publish-update", BaseURL: "https://example.com"}, b.SourceVersion)
					updated <- err
				}
				if first == "publication" {
					close(publish)
					var pid int
					select {
					case pid = <-inNative:
					case <-ctx.Done():
						t.Fatal(ctx.Err())
					}
					go update()
					waitInventoryBlocker(t, ctx, f.db, updaterPID, pid, updated)
					close(commit)
					if err := <-updated; err != nil {
						t.Fatal(err)
					}
					// Update could only return after the actual native transaction
					// committed its references, not merely after a separate lock.
					if format == "raw" {
						if _, err := f.native.GetRawAsset(ctx, f.repo.ID, "fence.bin"); err != nil {
							t.Fatal(err)
						}
					} else if _, err := f.native.GetMavenArtifactByCoordinate(ctx, f.repo.ID, "org.example:lib:1.0"); err != nil {
						t.Fatal(err)
					}
				} else {
					hold, err := f.db.BeginTx(ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer func() { _ = hold.Rollback() }()
					var holderPID int
					if first == "source" {
						key := "owned-publish-source-" + uuid.NewString()
						if err = hold.QueryRowContext(ctx, `SELECT pg_backend_pid() FROM pg_advisory_xact_lock(hashtextextended($1,0))`, key).Scan(&holderPID); err != nil {
							t.Fatal(err)
						}
						name := "owned_publish_" + strings.ReplaceAll(uuid.NewString(), "-", "")
						if _, err = f.db.ExecContext(ctx, `CREATE FUNCTION `+name+`() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.id='`+f.sourceID+`'::uuid THEN PERFORM pg_advisory_xact_lock(hashtextextended('`+key+`',0)); END IF; RETURN NEW; END $$`); err != nil {
							t.Fatal(err)
						}
						t.Cleanup(func() {
							if _, err := f.db.Exec(`DROP FUNCTION IF EXISTS ` + name + `() CASCADE`); err != nil {
								t.Errorf("remove owned publication barrier: %v", err)
							}
						})
						if _, err = f.db.ExecContext(ctx, `CREATE TRIGGER `+name+` BEFORE UPDATE ON nexus_migration_sources FOR EACH ROW EXECUTE FUNCTION `+name+`() `); err != nil {
							t.Fatal(err)
						}
						go update()
						waitInventoryBlocker(t, ctx, f.db, updaterPID, holderPID, updated)
						close(publish)
						waitPublicationSourceBlocker(t, ctx, f.db, updaterPID, done)
					} else {
						if err = hold.QueryRowContext(ctx, `SELECT pg_backend_pid() FROM nexus_migration_sources WHERE id=$1 FOR UPDATE`, f.sourceID).Scan(&holderPID); err != nil {
							t.Fatal(err)
						}
						if _, err = f.db.ExecContext(ctx, `UPDATE nexus_migration_batches SET lease_expires_at=clock_timestamp()+interval '1 second' WHERE id=$1`, b.ID); err != nil {
							t.Fatal(err)
						}
						close(publish)
						waitPublicationSourceBlocker(t, ctx, f.db, holderPID, done)
						for {
							var expired bool
							if err = f.db.QueryRowContext(ctx, `SELECT lease_expires_at<=clock_timestamp() FROM nexus_migration_batches WHERE id=$1`, b.ID).Scan(&expired); err != nil {
								t.Fatal(err)
							}
							if expired {
								break
							}
							time.Sleep(5 * time.Millisecond)
						}
						// Releasing the same unchanged source version is insufficient:
						// database lease time must be checked after the source lock.
					}
					if err = hold.Rollback(); err != nil {
						t.Fatal(err)
					}
					if first == "source" {
						if err = <-updated; err != nil {
							t.Fatal(err)
						}
					}
				}
				select {
				case err := <-done:
					if err != nil {
						t.Fatal(err)
					}
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
				if first != "publication" {
					requireNoNativePublication(t, f, format)
				}
				t.Logf("%s %s: actual native SQL transaction source/lease fence observed without deadlock", format, first)
			})
		}
	}
}

func waitPublicationSourceBlocker(t *testing.T, ctx context.Context, db *sql.DB, blocker int, done <-chan error) {
	t.Helper()
	for {
		select {
		case err := <-done:
			t.Fatalf("native worker finished before waiting on source transaction %d: %v", blocker, err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		default:
		}
		var blocked bool
		if err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid)))`, blocker).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
}
