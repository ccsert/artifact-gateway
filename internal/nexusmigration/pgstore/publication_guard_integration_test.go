//go:build integration

package pgstore_test

import (
	"context"
	"database/sql"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	m "github.com/ccsert/artifact-gateway/internal/nexusmigration"
	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

type interceptedPublication struct {
	m.Publication
	verifies    atomic.Int32
	finalVerify func(context.Context) error
	nativeGuard repository.NativePublicationGuard
}

func (p *interceptedPublication) Verify(ctx context.Context, entry m.Entry) error {
	if err := p.Publication.Verify(ctx, entry); err != nil {
		return err
	}
	if p.finalVerify != nil && p.verifies.Add(1) == 2 {
		return p.finalVerify(ctx)
	}
	return nil
}

func (p *interceptedPublication) Publish(ctx context.Context, entry m.Entry) error {
	return p.Publication.Publish(repository.WithNativePublicationGuard(ctx, p.nativeGuard), entry)
}

func guardedFixture(t *testing.T, format string) *fixture {
	t.Helper()
	files := map[string][]byte{"fence.bin": []byte("must-never-publish-after-cancellation")}
	if format == "maven2" {
		files = map[string][]byte{
			"org/example/lib/1.0/lib-1.0.pom": []byte(`<project><modelVersion>4.0.0</modelVersion><groupId>org.example</groupId><artifactId>lib</artifactId><version>1.0</version></project>`),
			"org/example/lib/1.0/lib-1.0.jar": []byte("owned-complete-release"),
		}
	}
	return newFixture(t, format, files)
}

func requireNoNativePublication(t *testing.T, f *fixture, format string) {
	t.Helper()
	var err error
	if format == "raw" {
		_, err = f.native.GetRawAsset(context.Background(), f.repo.ID, "fence.bin")
	} else {
		_, err = f.native.GetMavenArtifactByCoordinate(context.Background(), f.repo.ID, "org.example:lib:1.0")
	}
	if !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("stale owner made native %s publication visible: %v", format, err)
	}
}

func TestPostgresMigrationFinalVerificationCannotOutliveCancelledOrExpiredOwner(t *testing.T) {
	for _, format := range []string{"raw", "maven2"} {
		for _, stop := range []string{"cancel", "expire"} {
			t.Run(format+"/"+stop, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
				defer cancel()
				f := guardedFixture(t, format)
				entered, release := make(chan struct{}), make(chan struct{})
				intercept := &interceptedPublication{Publication: f.publication, finalVerify: func(ctx context.Context) error {
					close(entered)
					select {
					case <-release:
						return nil
					case <-ctx.Done():
						return ctx.Err()
					}
				}}
				f.service = m.New(f.ledger, f.source, intercept, m.Options{TargetID: "owned-target", PoolID: "owned-pool", HeartbeatInterval: time.Hour})
				b := f.start()
				done := make(chan error, 1)
				go func() { done <- f.service.RunOnce(ctx, []string{"raw", "maven"}) }()
				select {
				case <-entered:
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
				requireNoNativePublication(t, f, format)
				if stop == "cancel" {
					current, err := f.service.Get(ctx, b.ID)
					if err != nil {
						t.Fatal(err)
					}
					if _, err = f.service.Control(ctx, b.ID, current.Version, "synthetic-admin", uuid.NewString(), "cancel", nil); err != nil {
						t.Fatal(err)
					}
				} else if _, err := f.db.ExecContext(ctx, `UPDATE nexus_migration_batches SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`, b.ID); err != nil {
					t.Fatal(err)
				}
				close(release)
				select {
				case err := <-done:
					if err != nil {
						t.Fatal(err)
					}
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
				requireNoNativePublication(t, f, format)
				if stop == "expire" {
					f.service = f.newService(m.Options{})
					f.run(b)
					requireState(t, *b, "completed")
					if b.VisibleIdentities != 1 {
						t.Fatalf("new owner duplicated/lost publication: %+v", *b)
					}
				}
			})
		}
	}
}

func TestPostgresMigrationNativeTransactionLossCannotPublishAfterCancellation(t *testing.T) {
	for _, format := range []string{"raw", "maven2"} {
		t.Run(format, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			f := guardedFixture(t, format)
			entered, release := make(chan int, 1), make(chan struct{})
			intercept := &interceptedPublication{Publication: f.publication, nativeGuard: func(ctx context.Context, tx *sql.Tx) error {
				var pid int
				if err := tx.QueryRowContext(ctx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
					return err
				}
				entered <- pid
				select {
				case <-release:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			}}
			f.service = m.New(f.ledger, f.source, intercept, m.Options{TargetID: "owned-target", PoolID: "owned-pool", HeartbeatInterval: time.Hour})
			b := f.start()
			done := make(chan error, 1)
			go func() { done <- f.service.RunOnce(ctx, []string{"raw", "maven"}) }()
			var pid int
			select {
			case pid = <-entered:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			current, err := f.service.Get(ctx, b.ID)
			if err != nil {
				t.Fatal(err)
			}
			var ownsFence bool
			if err = f.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_locks WHERE pid=$1 AND locktype='advisory' AND classid=((hashtextextended('nexus-migration-lease:'||$2,0)>>32)&4294967295)::oid AND objid=(hashtextextended('nexus-migration-lease:'||$2,0)&4294967295)::oid AND objsubid=1 AND granted)`, pid, b.ID).Scan(&ownsFence); err != nil || !ownsFence {
				t.Fatalf("actual native transaction does not own the migration fence: %v %v", ownsFence, err)
			}
			// This must be the native commit's physical transaction, not an
			// independent lease connection. While it lives, cancel cannot pass.
			blockedCtx, stop := context.WithTimeout(ctx, 30*time.Millisecond)
			_, err = f.service.Control(blockedCtx, b.ID, current.Version, "synthetic-admin", uuid.NewString(), "cancel", nil)
			stop()
			if err == nil {
				t.Fatal("cancellation crossed the actual native publication transaction")
			}
			var terminated bool
			if err = f.db.QueryRowContext(ctx, `SELECT pg_terminate_backend($1,1000)`, pid).Scan(&terminated); err != nil || !terminated {
				t.Fatalf("terminate actual native backend pid=%d: %v %v", pid, terminated, err)
			}
			cancelled, err := f.service.Control(ctx, b.ID, current.Version, "synthetic-admin", uuid.NewString(), "cancel", nil)
			if err != nil || cancelled.State != "cancelled" {
				t.Fatalf("cancel after connection loss=%+v %v", cancelled, err)
			}
			requireNoNativePublication(t, f, format)
			close(release)
			select {
			case err = <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			requireNoNativePublication(t, f, format)
		})
	}
}
