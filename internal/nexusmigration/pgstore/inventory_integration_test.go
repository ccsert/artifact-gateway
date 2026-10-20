//go:build integration

package pgstore_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	m "github.com/ccsert/artifact-gateway/internal/nexusmigration"
	"github.com/ccsert/artifact-gateway/internal/nexusmigration/pgstore"
	"github.com/ccsert/artifact-gateway/internal/nexussource"
	"github.com/google/uuid"
)

type inventoryStoreHook struct {
	m.Store
	beforeAppend func(context.Context, m.Batch, m.InventoryScan, nexussource.InventoryPage) error
	afterAppend  func(m.InventoryScan) error
	afterVerify  func(int64) error
	afterSeal    func(m.Batch) error
}

func (s inventoryStoreHook) AppendInventoryPage(ctx context.Context, b m.Batch, scan m.InventoryScan, page nexussource.InventoryPage) error {
	if s.beforeAppend != nil {
		if err := s.beforeAppend(ctx, b, scan, page); err != nil {
			return err
		}
	}
	if err := s.Store.AppendInventoryPage(ctx, b, scan, page); err != nil {
		return err
	}
	if s.afterAppend != nil {
		return s.afterAppend(scan)
	}
	return nil
}
func (s inventoryStoreHook) VerifyInventoryPage(ctx context.Context, b m.Batch, ordinal int64, hash string, metadataBytes int64) error {
	if err := s.Store.VerifyInventoryPage(ctx, b, ordinal, hash, metadataBytes); err != nil {
		return err
	}
	if s.afterVerify != nil {
		return s.afterVerify(ordinal)
	}
	return nil
}
func (s inventoryStoreHook) Seal(ctx context.Context, b m.Batch, entries []m.Entry, hash string, metadata, total int64) error {
	if err := s.Store.Seal(ctx, b, entries, hash, metadata, total); err != nil {
		return err
	}
	if s.afterSeal != nil {
		return s.afterSeal(b)
	}
	return nil
}

type inventorySourceHook struct {
	m.Source
	read func(context.Context, string, string, nexussource.Repository, string) (nexussource.InventoryPage, error)
}

func (s inventorySourceHook) ReadInventoryPage(ctx context.Context, id, version string, repo nexussource.Repository, cursor string) (nexussource.InventoryPage, error) {
	return s.read(ctx, id, version, repo, cursor)
}

func (f *fixture) scanService(store m.Store, source m.Source) *m.Service {
	return m.New(store, source, f.publication, m.Options{TargetID: "owned-target", PoolID: "owned-pool", HeartbeatInterval: time.Hour})
}
func (f *fixture) pauseOnSeal() m.Store {
	return inventoryStoreHook{Store: f.ledger, afterSeal: func(b m.Batch) error {
		return f.scanControl(b.ID, "pause")
	}}
}
func (f *fixture) scanControl(id, action string) error {
	b, err := f.ledger.Get(context.Background(), id)
	if err != nil {
		return err
	}
	_, err = f.ledger.Control(context.Background(), id, b.Version, "owned-scan-test", uuid.NewString(), action, uuid.NewString(), nil)
	return err
}
func (f *fixture) assertUnsealed(b m.Batch) {
	f.t.Helper()
	if b.ScanComplete || b.TotalBytes != nil || b.EntryCount != 0 || b.ManifestSHA256 != "" || len(f.entries(b.ID)) != 0 || b.TransferredBytes != 0 || b.VisibleIdentities != 0 {
		f.t.Fatalf("partial inventory became executable: %+v", b)
	}
	keys, err := f.objects.List(context.Background(), "native/")
	if err != nil || len(keys) != 0 {
		f.t.Fatalf("partial inventory wrote target objects: count=%d err=%v", len(keys), err)
	}
}
func (f *fixture) scanCheckpoint(id string) m.InventoryScan {
	f.t.Helper()
	scan, err := f.ledger.InventoryScan(context.Background(), id)
	if err != nil {
		f.t.Fatal(err)
	}
	return scan
}
func (f *fixture) reopenScanLedger() {
	f.t.Helper()
	fresh, err := sql.Open("pgx", f.connection)
	if err != nil {
		f.t.Fatal(err)
	}
	f.t.Cleanup(func() { _ = fresh.Close() })
	f.ledger = pgstore.New(fresh)
}

// This public worker reads 101 pages / 10002 assets, beyond both former limits.
// The checkpoint is committed before its acknowledgement is lost. A new PG
// connection and worker resume at the next cursor, then compare the final
// complete manifest with a separate uninterrupted scan of the same source.
func TestPostgresMigrationInventoryLargeResumeMatchesUninterruptedManifest(t *testing.T) {
	files := map[string][]byte{}
	var totalBytes int64
	for i := 0; i < 10002; i++ {
		payload := []byte(fmt.Sprintf("owned-fixed-inventory-%05d", i))
		files[fmt.Sprintf("large/%05d.bin", i)] = payload
		totalBytes += int64(len(payload))
	}
	f := newFixture(t, "raw", files)
	f.remote.pageSize = 100
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f.service = f.scanService(inventoryStoreHook{Store: f.ledger, afterAppend: func(scan m.InventoryScan) error {
		if scan.PageCount == 49 {
			cancel()
			return context.Canceled
		}
		return nil
	}}, f.source)
	b := f.start()
	if err := f.service.RunOnce(ctx, []string{"raw"}); err != nil {
		t.Fatal(err)
	}
	current, err := f.ledger.Get(context.Background(), b.ID)
	if err != nil {
		t.Fatal(err)
	}
	f.assertUnsealed(current)
	requireState(t, current, "scanning")
	checkpoint := f.scanCheckpoint(b.ID)
	if checkpoint.PageCount != 50 || checkpoint.Cursor != "5000" || checkpoint.AssetCount != 5000 || current.MetadataDiscovered != 5000 {
		t.Fatalf("lost acknowledgement did not retain atomic receipt: scan=%+v batch=%+v", checkpoint, current)
	}
	public, _ := json.Marshal(current)
	if strings.Contains(string(public), "cursor") || strings.Contains(string(public), "5000\"") {
		t.Fatalf("private continuation cursor escaped batch response: %s", public)
	}
	f.reopenScanLedger()
	firstCursor := ""
	reads := 0
	source := inventorySourceHook{Source: f.source, read: func(ctx context.Context, id, version string, repo nexussource.Repository, cursor string) (nexussource.InventoryPage, error) {
		if reads == 0 {
			firstCursor = cursor
		}
		reads++
		return f.source.ReadInventoryPage(ctx, id, version, repo, cursor)
	}}
	f.service = f.scanService(f.pauseOnSeal(), source)
	f.run(&current)
	requireState(t, current, "paused")
	if firstCursor != "5000" || reads != 51+101 || !current.ScanComplete || current.EntryCount != 10002 || current.MetadataDiscovered != 10002 || current.TotalBytes == nil || *current.TotalBytes != totalBytes {
		t.Fatalf("resume did not exhaust exact inventory: first=%q reads=%d batch=%+v", firstCursor, reads, current)
	}
	if scan := f.scanCheckpoint(b.ID); !scan.Verified() || scan.PageCount != 101 || scan.AssetCount != 10002 {
		t.Fatalf("sealed scan is incomplete: %+v", scan)
	}
	var entries int
	if err := f.db.QueryRow(`SELECT count(*) FROM nexus_migration_entries WHERE batch_id=$1`, b.ID).Scan(&entries); err != nil || entries != 10002 {
		t.Fatalf("complete manifest entries=%d err=%v", entries, err)
	}
	f.service = f.scanService(f.pauseOnSeal(), f.source)
	whole := f.start()
	f.run(whole)
	if !whole.ScanComplete || whole.ManifestSHA256 != current.ManifestSHA256 || whole.EntryCount != current.EntryCount {
		t.Fatalf("resumed and uninterrupted manifests differ: resumed=%s whole=%+v", current.ManifestSHA256, whole)
	}
	t.Logf("source pages=101 assets=10002 checkpoint=5000 resumedReads=%d completeSHA256=%s", reads, current.ManifestSHA256)
}

func TestPostgresMigrationInventoryCheckpointAckWindowsAndVerificationResume(t *testing.T) {
	for _, phase := range []string{"before-page-commit", "after-page-commit", "after-verification-commit"} {
		t.Run(phase, func(t *testing.T) {
			f := newFixture(t, "raw", map[string][]byte{"a.bin": []byte("a"), "b.bin": []byte("b"), "c.bin": []byte("c")})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			hook := inventoryStoreHook{Store: f.ledger}
			interrupt := func() error { cancel(); return context.Canceled }
			switch phase {
			case "before-page-commit":
				hook.beforeAppend = func(context.Context, m.Batch, m.InventoryScan, nexussource.InventoryPage) error { return interrupt() }
			case "after-page-commit":
				hook.afterAppend = func(m.InventoryScan) error { return interrupt() }
			case "after-verification-commit":
				hook.afterVerify = func(int64) error { return interrupt() }
			}
			f.service = f.scanService(hook, f.source)
			b := f.start()
			if err := f.service.RunOnce(ctx, []string{"raw"}); err != nil {
				t.Fatal(err)
			}
			current, err := f.ledger.Get(context.Background(), b.ID)
			if err != nil {
				t.Fatal(err)
			}
			f.assertUnsealed(current)
			scan := f.scanCheckpoint(b.ID)
			wantPages, wantVerified := int64(0), int64(0)
			if phase == "after-page-commit" {
				wantPages = 1
			}
			if phase == "after-verification-commit" {
				wantPages, wantVerified = 2, 1
			}
			if scan.PageCount != wantPages || scan.VerifiedPages != wantVerified {
				t.Fatalf("atomic checkpoint=%+v", scan)
			}
			f.reopenScanLedger()
			f.service = f.newService(m.Options{})
			f.run(&current)
			requireState(t, current, "completed")
			if current.VisibleIdentities != 3 || current.EntryCount != 3 {
				t.Fatalf("recovery lost identities: %+v", current)
			}
		})
	}
}

func TestPostgresMigrationInventoryPauseCancelFencePageAndSeal(t *testing.T) {
	for _, action := range []string{"pause", "cancel"} {
		for _, boundary := range []string{"page", "seal"} {
			t.Run(action+"/"+boundary, func(t *testing.T) {
				f := newFixture(t, "raw", map[string][]byte{"a.bin": []byte("a")})
				hook := inventoryStoreHook{Store: f.ledger}
				f.service = f.scanService(hook, f.source)
				b := f.start()
				if boundary == "page" {
					hook.beforeAppend = func(context.Context, m.Batch, m.InventoryScan, nexussource.InventoryPage) error {
						return f.scanControl(b.ID, action)
					}
					f.service = f.scanService(hook, f.source)
				} else {
					f.service = f.scanService(inventorySealControl{Store: f.ledger, control: func() error { return f.scanControl(b.ID, action) }}, f.source)
				}
				f.run(b)
				f.assertUnsealed(*b)
				if action == "pause" {
					requireState(t, *b, "paused")
				} else {
					requireState(t, *b, "cancelled")
				}
				if scan := f.scanCheckpoint(b.ID); boundary == "page" && scan.PageCount != 0 {
					t.Fatalf("acknowledged control allowed a receipt: %+v", scan)
				}
				if action == "pause" {
					ev := f.evidence()
					resumed, err := f.service.Control(context.Background(), b.ID, b.Version, "owned-scan-test", uuid.NewString(), "resume", &ev)
					if err != nil {
						t.Fatal(err)
					}
					f.service = f.newService(m.Options{})
					f.run(&resumed)
					requireState(t, resumed, "completed")
				}
			})
		}
	}
}

type inventorySealControl struct {
	m.Store
	control func() error
}

func (s inventorySealControl) Seal(ctx context.Context, b m.Batch, entries []m.Entry, hash string, metadata, total int64) error {
	if err := s.control(); err != nil {
		return err
	}
	return s.Store.Seal(ctx, b, entries, hash, metadata, total)
}

func TestPostgresMigrationInventoryLeaseTakeoverAndStaleWriter(t *testing.T) {
	f := newFixture(t, "raw", map[string][]byte{"a.bin": []byte("a"), "b.bin": []byte("b"), "c.bin": []byte("c")})
	b := f.start()
	owner, err := f.ledger.Claim(context.Background(), []string{"raw"})
	if err != nil || owner.ID != b.ID {
		t.Fatalf("claim=%+v err=%v", owner, err)
	}
	repos, err := f.source.InventoryRepositories(context.Background(), b.SourceID, b.SourceVersion, []string{"source"})
	if err != nil {
		t.Fatal(err)
	}
	if err = f.ledger.BeginInventoryScan(context.Background(), owner, m.InventoryScan{BindingSHA256: m.InventoryBinding(owner, repos), Repositories: repos}); err != nil {
		t.Fatal(err)
	}
	scan := f.scanCheckpoint(b.ID)
	page, err := f.source.ReadInventoryPage(context.Background(), b.SourceID, b.SourceVersion, repos[0], "")
	if err != nil {
		t.Fatal(err)
	}
	if err = f.ledger.AppendInventoryPage(context.Background(), owner, scan, page); err != nil {
		t.Fatal(err)
	}
	if _, err = f.ledger.Claim(context.Background(), []string{"raw"}); !errors.Is(err, m.ErrNotFound) {
		t.Fatalf("live lease duplicated: %v", err)
	}
	if err = f.ledger.Seal(context.Background(), owner, nil, strings.Repeat("a", 64), 2, 0); err == nil {
		t.Fatal("partial inventory could be sealed")
	}
	if _, err = f.db.Exec(`UPDATE nexus_migration_batches SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`, b.ID); err != nil {
		t.Fatal(err)
	}
	f.reopenScanLedger()
	newOwner, err := f.ledger.Claim(context.Background(), []string{"raw"})
	if err != nil || newOwner.ID != b.ID || newOwner.LeaseToken == owner.LeaseToken || newOwner.LeaseEpoch != 2 {
		t.Fatalf("expired scan did not resume: owner=%+v err=%v", newOwner, err)
	}
	if err = f.ledger.AppendInventoryPage(context.Background(), owner, f.scanCheckpoint(b.ID), page); !errors.Is(err, m.ErrLeaseLost) {
		t.Fatalf("stale receipt accepted: %v", err)
	}
	if err = f.ledger.Seal(context.Background(), owner, nil, strings.Repeat("a", 64), 2, 0); !errors.Is(err, m.ErrLeaseLost) {
		t.Fatalf("stale seal accepted: %v", err)
	}
	if err = f.ledger.Finish(context.Background(), newOwner.ID, newOwner.LeaseToken, "scanning", "owned-restart"); err != nil {
		t.Fatal(err)
	}
	f.service = f.newService(m.Options{})
	f.run(b)
	requireState(t, *b, "completed")
	if b.VisibleIdentities != 3 || b.MetadataDiscovered != 3 {
		t.Fatalf("takeover lost/duplicated source: %+v", b)
	}
}

func TestPostgresMigrationInventoryRejectsDriftDuplicateCycleAndIncompletePages(t *testing.T) {
	for _, scenario := range []string{"observed-drift", "duplicate-path", "cursor-cycle", "page-unavailable", "page-budget", "asset-budget", "metadata-budget", "verification-metadata-budget"} {
		t.Run(scenario, func(t *testing.T) {
			f := newFixture(t, "raw", map[string][]byte{"a.bin": []byte("a"), "b.bin": []byte("b"), "c.bin": []byte("c"), "d.bin": []byte("d"), "e.bin": []byte("e")})
			calls := map[string]int{}
			source := inventorySourceHook{Source: f.source, read: func(ctx context.Context, id, version string, repo nexussource.Repository, cursor string) (nexussource.InventoryPage, error) {
				calls[cursor]++
				if scenario == "page-unavailable" && cursor == "2" {
					return nexussource.InventoryPage{}, &nexussource.Failure{Code: "source_http_error"}
				}
				page, err := f.source.ReadInventoryPage(ctx, id, version, repo, cursor)
				if err != nil {
					return page, err
				}
				if scenario == "verification-metadata-budget" && cursor == "" && calls[cursor] == 2 {
					page.MetadataBytes = m.MaxInventoryMetadataBytes
				}
				if scenario == "observed-drift" && cursor == "" && calls[cursor] == 2 {
					page.Assets[0].Checksum["sha256"] = strings.Repeat("a", 64)
				}
				if scenario == "duplicate-path" && cursor == "2" {
					page.Assets[0].Path = "a.bin"
				}
				if scenario == "cursor-cycle" && cursor == "4" {
					page.NextCursor = "2"
				}
				return page, nil
			}}
			hook := inventoryStoreHook{Store: f.ledger}
			if strings.HasSuffix(scenario, "budget") && scenario != "verification-metadata-budget" {
				hook.beforeAppend = func(ctx context.Context, b m.Batch, scan m.InventoryScan, page nexussource.InventoryPage) error {
					switch scenario {
					case "page-budget":
						_, err := f.db.ExecContext(ctx, `UPDATE nexus_migration_scans SET page_count=$2 WHERE batch_id=$1`, b.ID, m.MaxInventoryPages)
						if err != nil {
							return err
						}
					case "asset-budget":
						_, err := f.db.ExecContext(ctx, `UPDATE nexus_migration_scans SET asset_count=$2 WHERE batch_id=$1`, b.ID, m.MaxInventoryAssets)
						if err != nil {
							return err
						}
					case "metadata-budget":
						_, err := f.db.ExecContext(ctx, `UPDATE nexus_migration_scans SET metadata_bytes=$2 WHERE batch_id=$1`, b.ID, m.MaxInventoryMetadataBytes)
						if err != nil {
							return err
						}
					}
					if scenario == "page-budget" {
						current, err := f.ledger.InventoryScan(ctx, b.ID)
						if err != nil {
							return err
						}
						return f.ledger.AppendInventoryPage(ctx, b, current, page)
					}
					return nil
				}
			}
			f.service = f.scanService(hook, source)
			b := f.start()
			f.run(b)
			requireState(t, *b, "failed")
			f.assertUnsealed(*b)
			want := map[string]string{"observed-drift": "source_changed", "duplicate-path": "source_duplicate_asset", "cursor-cycle": "source_pagination_cycle", "page-unavailable": "source_http_error", "page-budget": "source_limit_exceeded", "asset-budget": "source_limit_exceeded", "metadata-budget": "source_limit_exceeded", "verification-metadata-budget": "source_limit_exceeded"}[scenario]
			if b.LastError != want {
				t.Fatalf("failure=%q want=%q diagnostic=%+v", b.LastError, want, b.LastScanFailure)
			}
			if scenario == "duplicate-path" || scenario == "cursor-cycle" {
				wantPages := int64(1)
				if scenario == "cursor-cycle" {
					wantPages = 2
				}
				if scan := f.scanCheckpoint(b.ID); scan.PageCount != wantPages {
					t.Fatalf("rejected page partly advanced: %+v", scan)
				}
			}
		})
	}
}

func TestPostgresMigrationInventoryCheckpointBindsSourceShapeMappingAndPolicy(t *testing.T) {
	for _, change := range []string{"connection-version", "repository-shape", "mapping", "policy"} {
		t.Run(change, func(t *testing.T) {
			f := newFixture(t, "raw", map[string][]byte{"a.bin": []byte("a"), "b.bin": []byte("b"), "c.bin": []byte("c")})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			f.service = f.scanService(inventoryStoreHook{Store: f.ledger, afterAppend: func(m.InventoryScan) error { cancel(); return context.Canceled }}, f.source)
			b := f.start()
			if err := f.service.RunOnce(ctx, []string{"raw"}); err != nil {
				t.Fatal(err)
			}
			switch change {
			case "connection-version":
				if _, err := f.source.Update(context.Background(), f.sourceID, nexussource.Input{Name: "owned-new-connection", BaseURL: "https://example.com"}, "1"); err != nil {
					t.Fatal(err)
				}
			case "repository-shape":
				f.remote.mu.Lock()
				f.remote.format = "maven2"
				f.remote.mu.Unlock()
			case "mapping":
				if _, err := f.db.Exec(`UPDATE nexus_migration_batches SET request=jsonb_set(request,'{mappings,0,targetRepositoryId}',to_jsonb($2::text)) WHERE id=$1`, b.ID, uuid.NewString()); err != nil {
					t.Fatal(err)
				}
			case "policy":
				if _, err := f.db.Exec(`UPDATE nexus_migration_batches SET request=jsonb_set(request,'{policyVersion}','"different-native-policy"'::jsonb) WHERE id=$1`, b.ID); err != nil {
					t.Fatal(err)
				}
			}
			f.reopenScanLedger()
			f.service = f.newService(m.Options{})
			f.run(b)
			f.assertUnsealed(*b)
			if change == "connection-version" {
				requireState(t, *b, "blocked")
				if b.LastError != "source_version_changed" {
					t.Fatalf("source binding failure=%s", b.LastError)
				}
			} else if b.LastError != "source_changed" {
				t.Fatalf("checkpoint binding failure=%q", b.LastError)
			}
			if scan := f.scanCheckpoint(b.ID); scan.PageCount != 1 || scan.AssetCount != 2 {
				t.Fatalf("binding drift advanced cursor: %+v", scan)
			}
		})
	}
}

// Kill the actual SQL backend while a private path insertion is waiting inside
// its transaction. Neither the path nor page nor cursor survives the rollback;
// a newly opened ledger must replay that unacknowledged page successfully.
func TestPostgresMigrationInventoryBackendLossRollsBackWholePage(t *testing.T) {
	f := newFixture(t, "raw", map[string][]byte{"a.bin": []byte("a")})
	b := f.start()
	owner, err := f.ledger.Claim(context.Background(), []string{"raw"})
	if err != nil {
		t.Fatal(err)
	}
	repos, err := f.source.InventoryRepositories(context.Background(), b.SourceID, b.SourceVersion, []string{"source"})
	if err != nil {
		t.Fatal(err)
	}
	if err = f.ledger.BeginInventoryScan(context.Background(), owner, m.InventoryScan{BindingSHA256: m.InventoryBinding(owner, repos), Repositories: repos}); err != nil {
		t.Fatal(err)
	}
	page, err := f.source.ReadInventoryPage(context.Background(), b.SourceID, b.SourceVersion, repos[0], "")
	if err != nil {
		t.Fatal(err)
	}
	name := "owned_scan_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = f.db.Exec(`CREATE FUNCTION ` + name + `() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.batch_id='` + b.ID + `'::uuid THEN PERFORM pg_sleep(30); END IF; RETURN NEW; END $$`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = f.db.Exec(`DROP FUNCTION IF EXISTS ` + name + `() CASCADE`) })
	if _, err = f.db.Exec(`CREATE TRIGGER ` + name + ` BEFORE INSERT ON nexus_migration_scan_paths FOR EACH ROW EXECUTE FUNCTION ` + name + `() `); err != nil {
		t.Fatal(err)
	}
	writer, err := sql.Open("pgx", f.connection)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = writer.Close() }()
	writer.SetMaxOpenConns(1)
	var pid int
	if err = writer.QueryRow(`SELECT pg_backend_pid()`).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	checkpoint := f.scanCheckpoint(b.ID)
	go func() {
		done <- pgstore.New(writer).AppendInventoryPage(context.Background(), owner, checkpoint, page)
	}()
	deadline := time.Now().Add(10 * time.Second)
	for {
		var waiting bool
		if err = f.db.QueryRow(`SELECT coalesce(wait_event='PgSleep',false) FROM pg_stat_activity WHERE pid=$1`, pid).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("append did not enter its database transaction")
		}
		time.Sleep(10 * time.Millisecond)
	}
	var killed bool
	if err = f.db.QueryRow(`SELECT pg_terminate_backend($1)`, pid).Scan(&killed); err != nil || !killed {
		t.Fatalf("kill owned backend=%v err=%v", killed, err)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("lost backend reported committed page")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("lost SQL transaction did not return")
	}
	if _, err = f.db.Exec(`DROP FUNCTION ` + name + `() CASCADE`); err != nil {
		t.Fatal(err)
	}
	if scan := f.scanCheckpoint(b.ID); scan.PageCount != 0 || scan.AssetCount != 0 || scan.Cursor != "" {
		t.Fatalf("aborted transaction partly persisted: %+v", scan)
	}
	var paths int
	if err = f.db.QueryRow(`SELECT count(*) FROM nexus_migration_scan_paths WHERE batch_id=$1`, b.ID).Scan(&paths); err != nil || paths != 0 {
		t.Fatalf("orphan private paths=%d err=%v", paths, err)
	}
	if err = f.ledger.Finish(context.Background(), owner.ID, owner.LeaseToken, "scanning", "owned-backend-loss"); err != nil {
		t.Fatal(err)
	}
	f.reopenScanLedger()
	f.service = f.newService(m.Options{})
	f.run(b)
	requireState(t, *b, "completed")
	if b.VisibleIdentities != 1 {
		t.Fatalf("replayed page did not publish exactly once: %+v", b)
	}
}

func TestPostgresMigrationInventoryLeaseExpiryInsideTransactionRollsBackPage(t *testing.T) {
	f := newFixture(t, "raw", map[string][]byte{"a.bin": []byte("a")})
	b := f.start()
	owner, err := f.ledger.Claim(context.Background(), []string{"raw"})
	if err != nil {
		t.Fatal(err)
	}
	repos, err := f.source.InventoryRepositories(context.Background(), b.SourceID, b.SourceVersion, []string{"source"})
	if err != nil {
		t.Fatal(err)
	}
	if err = f.ledger.BeginInventoryScan(context.Background(), owner, m.InventoryScan{BindingSHA256: m.InventoryBinding(owner, repos), Repositories: repos}); err != nil {
		t.Fatal(err)
	}
	page, err := f.source.ReadInventoryPage(context.Background(), b.SourceID, b.SourceVersion, repos[0], "")
	if err != nil {
		t.Fatal(err)
	}
	name := "owned_expiry_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	// Expire this owned lease after the transaction's initial guard, inside the
	// first path insertion. This proves the final database-time guard is live.
	if _, err = f.db.Exec(`CREATE FUNCTION ` + name + `() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.batch_id='` + b.ID + `'::uuid THEN UPDATE nexus_migration_batches SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE id=NEW.batch_id; END IF; RETURN NEW; END $$`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = f.db.Exec(`DROP FUNCTION IF EXISTS ` + name + `() CASCADE`) })
	if _, err = f.db.Exec(`CREATE TRIGGER ` + name + ` BEFORE INSERT ON nexus_migration_scan_paths FOR EACH ROW EXECUTE FUNCTION ` + name + `() `); err != nil {
		t.Fatal(err)
	}
	if err = f.ledger.AppendInventoryPage(context.Background(), owner, f.scanCheckpoint(b.ID), page); !errors.Is(err, m.ErrLeaseLost) {
		t.Fatalf("expired SQL transaction committed a page: %v", err)
	}
	if scan := f.scanCheckpoint(b.ID); scan.PageCount != 0 || scan.AssetCount != 0 {
		t.Fatalf("expired SQL transaction partly persisted: %+v", scan)
	}
	var paths int
	if err = f.db.QueryRow(`SELECT count(*) FROM nexus_migration_scan_paths WHERE batch_id=$1`, b.ID).Scan(&paths); err != nil || paths != 0 {
		t.Fatalf("expired transaction left paths=%d err=%v", paths, err)
	}
	if _, err = f.db.Exec(`DROP FUNCTION ` + name + `() CASCADE`); err != nil {
		t.Fatal(err)
	}
	if err = f.ledger.Finish(context.Background(), owner.ID, owner.LeaseToken, "scanning", "owned-expired-transaction"); err != nil {
		t.Fatal(err)
	}
	f.service = f.newService(m.Options{})
	f.run(b)
	requireState(t, *b, "completed")
}
