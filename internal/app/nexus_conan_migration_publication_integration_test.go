//go:build integration

package app

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/ccsert/artifact-gateway/internal/nexusmigration"
	"github.com/ccsert/artifact-gateway/internal/objectstore"
	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/ccsert/artifact-gateway/internal/testsupport"
	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
)

type nexusConanPGFixture struct {
	databaseURL string
	db          *sql.DB
	store       *repository.PostgresStore
	objects     *objectstore.RustFSStore
	repo        repository.HostedRepository
	entries     []nexusmigration.Entry
	bodies      map[string][]byte
}

func newNexusConanPGFixture(t *testing.T) nexusConanPGFixture {
	t.Helper()
	databaseURL, endpoint := os.Getenv("TEST_DATABASE_URL"), os.Getenv("TEST_RUSTFS_ENDPOINT")
	if databaseURL == "" || endpoint == "" {
		t.Skip("isolated PostgreSQL and RustFS required")
	}
	if err := testsupport.ValidateIsolatedDatabaseURL(databaseURL); err != nil {
		t.Fatal(err)
	}
	store, err := repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	objects, err := objectstore.NewRustFSStore(endpoint, os.Getenv("TEST_RUSTFS_ACCESS_KEY"), os.Getenv("TEST_RUSTFS_SECRET_KEY"), "conan-migrate-"+strings.ReplaceAll(uuid.NewString(), "-", ""))
	if err != nil {
		t.Fatal(err)
	}
	if err = objects.EnsureBucket(context.Background()); err != nil {
		t.Fatal(err)
	}
	repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "conan-migrate-" + uuid.NewString(), Format: repository.FormatConan})
	if err != nil {
		t.Fatal(err)
	}
	entries, bodies := nexusConanInventory(t, repo)
	return nexusConanPGFixture{databaseURL, db, store, objects, repo, entries, bodies}
}

func TestPostgresRustFSNexusConanMigrationCompleteRevisionReopen(t *testing.T) {
	f := newNexusConanPGFixture(t)
	publication := NewNexusConanMigrationPublication(f.store, f.store, f.objects)
	for i := range f.entries {
		entry := &f.entries[i]
		ctx, release, err := publication.Lock(context.Background(), *entry)
		if err != nil {
			t.Fatal(err)
		}
		stageNexusConan(t, ctx, publication, entry, f.bodies)
		release()
		_ = f.store.Close()
		other, err := repository.NewPostgresStore(f.databaseURL)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = other.Close() })
		f.store = other
		publication = NewNexusConanMigrationPublication(other, other, f.objects)
		ctx, release, err = publication.Lock(context.Background(), *entry)
		if err != nil {
			t.Fatal(err)
		}
		session, err := publication.Begin(ctx, *entry)
		if err != nil || session != entry.SessionID {
			t.Fatalf("reopened durable session=%s want=%s err=%v", session, entry.SessionID, err)
		}
		if state, err := publication.Readback(ctx, *entry); err != nil || state != nexusmigration.Absent {
			t.Fatalf("stage became visible after restart=%s %v", state, err)
		}
		if err = publication.Verify(ctx, *entry); err != nil {
			t.Fatal(err)
		}
		if err = publication.Publish(ctx, *entry); err != nil {
			t.Fatal(err)
		}
		release()
		if state, err := publication.Readback(context.Background(), *entry); err != nil || state != nexusmigration.Identical {
			t.Fatalf("reopened exact target bytes=%s %v", state, err)
		}
	}
	handler := newTestGateway(t, GatewayOptions{Store: f.store, ConanPublications: f.store, Adapter: TestAdapter{}, Authenticator: testAuthenticator(), Storage: ObjectStores{repository.FormatConan: f.objects}})
	assertNexusConanNativeBytes(t, handler, f.repo, f.entries, f.bodies)
	audits, err := f.store.ListAudits(context.Background(), repository.AuditQuery{Repository: f.repo.Name, Operation: "publish", Limit: 100})
	if err != nil || len(audits) != 2 {
		t.Fatalf("publication audits=%+v %v", audits, err)
	}
}

func TestPostgresRustFSNexusConanMigrationExpiredSessionUsesFreshObjects(t *testing.T) {
	f := newNexusConanPGFixture(t)
	p := NewNexusConanMigrationPublication(f.store, f.store, f.objects)
	entry := &f.entries[0]
	ctx, release, err := p.Lock(context.Background(), *entry)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	stageNexusConan(t, ctx, p, entry, f.bodies)
	previous := *entry
	if _, err = f.db.ExecContext(ctx, `UPDATE native_conan_publish_sessions SET expires_at=now()-interval '1 second' WHERE id=$1`, entry.SessionID); err != nil {
		t.Fatal(err)
	}
	for _, asset := range entry.Assets {
		key := nexusConanObjectKey(previous, asset)
		collect, err := f.store.BeginConanObjectCollection(ctx, key)
		if err != nil || !collect {
			t.Fatalf("expired private staging collection=%v %v", collect, err)
		}
		if err = f.objects.Delete(ctx, key); err != nil {
			t.Fatal(err)
		}
		if err = f.store.CompleteConanObjectCollection(ctx, key); err != nil {
			t.Fatal(err)
		}
	}
	stageNexusConan(t, ctx, p, entry, f.bodies)
	if entry.SessionID == previous.SessionID {
		t.Fatal("expired session or collected staging key was reused")
	}
	if err = p.Verify(ctx, *entry); err != nil {
		t.Fatal(err)
	}
	if err = p.Publish(ctx, *entry); err != nil {
		t.Fatal(err)
	}
	if state, err := p.Readback(ctx, *entry); err != nil || state != nexusmigration.Identical {
		t.Fatalf("retry after orphan reclamation=%s %v", state, err)
	}
}

func TestPostgresNexusConanMigrationGuardPreventsVisibilityAndAudit(t *testing.T) {
	for _, kind := range []string{"recipe", "package"} {
		t.Run(kind, func(t *testing.T) {
			f := newNexusConanPGFixture(t)
			p := NewNexusConanMigrationPublication(f.store, f.store, f.objects)
			for i := range f.entries {
				entry := &f.entries[i]
				ctx, release, err := p.Lock(context.Background(), *entry)
				if err != nil {
					t.Fatal(err)
				}
				stageNexusConan(t, ctx, p, entry, f.bodies)
				if err = p.Verify(ctx, *entry); err != nil {
					t.Fatal(err)
				}
				c, _ := nexusmigration.ParseConanIdentity(entry.Identity)
				if c.Kind != kind {
					if err = p.Publish(ctx, *entry); err != nil {
						t.Fatal(err)
					}
					release()
					continue
				}
				called := false
				guarded := repository.WithNativePublicationGuard(ctx, func(ctx context.Context, tx *sql.Tx) error {
					called = true
					var current bool
					if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM native_conan_publish_sessions WHERE id=$1 AND state='open')`, entry.SessionID).Scan(&current); err != nil || !current {
						t.Fatalf("actual native transaction cannot read pending session: %v %v", current, err)
					}
					return nexusmigration.ErrLeaseLost
				})
				if err = p.Publish(guarded, *entry); !errors.Is(err, nexusmigration.ErrLeaseLost) || !called {
					t.Fatalf("actual publication did not enforce transaction guard=%v called=%v", err, called)
				}
				if state, err := p.Readback(ctx, *entry); err != nil || state != nexusmigration.Absent {
					t.Fatalf("fenced native revision is visible=%s %v", state, err)
				}
				var auditCount int
				if err = f.db.QueryRowContext(ctx, `SELECT count(*) FROM resolver_audit_log WHERE repository=$1 AND operation='publish' AND resource=$2`, f.repo.Name, entry.Identity).Scan(&auditCount); err != nil || auditCount != 0 {
					t.Fatalf("fenced transaction emitted success audit=%d %v", auditCount, err)
				}
				if err = p.Publish(ctx, *entry); err != nil {
					t.Fatal(err)
				}
				release()
				return
			}
		})
	}
}

func TestPostgresRustFSNexusConanMigrationStagedBytesRemainImmutable(t *testing.T) {
	f := newNexusConanPGFixture(t)
	p := NewNexusConanMigrationPublication(f.store, f.store, f.objects)
	entry := &f.entries[0]
	ctx, release, err := p.Lock(context.Background(), *entry)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	stageNexusConan(t, ctx, p, entry, f.bodies)
	asset := entry.Assets[0]
	requireNexusConanCode(t, p.Stage(ctx, *entry, asset, bytes.NewReader([]byte("source changed"))), "source_bytes_mismatch")
	if err = p.Verify(ctx, *entry); err != nil {
		t.Fatal(err)
	}
	if err = p.Publish(ctx, *entry); err != nil {
		t.Fatal(err)
	}
	if state, err := p.Readback(ctx, *entry); err != nil || state != nexusmigration.Identical {
		t.Fatalf("invalid later input overwrote staged bytes=%s %v", state, err)
	}
}
