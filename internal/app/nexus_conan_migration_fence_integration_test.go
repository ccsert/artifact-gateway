//go:build integration

package app

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/ccsert/artifact-gateway/internal/nexusmigration"
	migrationpg "github.com/ccsert/artifact-gateway/internal/nexusmigration/pgstore"
	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

type interceptedConanMigration struct {
	nexusmigration.Publication
	verifications int
	beforeFinal   func(context.Context) error
	guard         repository.NativePublicationGuard
}

func (p *interceptedConanMigration) Verify(ctx context.Context, entry nexusmigration.Entry) error {
	if err := p.Publication.Verify(ctx, entry); err != nil {
		return err
	}
	p.verifications++
	if p.verifications == 2 && p.beforeFinal != nil {
		return p.beforeFinal(ctx)
	}
	return nil
}

func (p *interceptedConanMigration) Publish(ctx context.Context, entry nexusmigration.Entry) error {
	return p.Publication.Publish(repository.WithNativePublicationGuard(ctx, p.guard), entry)
}

func nexusConanFenceService(t *testing.T, f nexusConanPGFixture, intercepted *interceptedConanMigration) (*nexusmigration.Service, nexusmigration.Batch) {
	t.Helper()
	files := map[string][]byte{}
	for _, asset := range f.entries[0].Assets {
		files[asset.SourcePath] = f.bodies[asset.SourcePath]
	}
	source, created, _, _ := conanMigrationSource(t, f, files)
	intercepted.Publication = NewNexusMigrationPublication(f.store, SharedObjectStores(f.objects), "conan-fixture-target", "conan-fixture-pool").WithConan(NewNexusConanMigrationPublication(f.store, f.store, f.objects))
	service := nexusmigration.New(migrationpg.New(f.db), source, intercepted, nexusmigration.Options{TargetID: "conan-fixture-target", PoolID: "conan-fixture-pool", HeartbeatInterval: time.Hour})
	batch, err := service.Start(context.Background(), nexusmigration.StartInput{SourceID: created.ID, SourceVersion: created.Version, PolicyVersion: nexusmigration.PolicyVersion,
		Mappings: []nexusmigration.Mapping{{SourceRepository: "source", TargetRepositoryID: f.repo.ID}}, CapacityEvidence: conanMigrationEvidence()}, "actual-migration-admin", uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	return service, batch
}

func requireNoConanMigrationPublication(t *testing.T, f nexusConanPGFixture) {
	t.Helper()
	c, _ := nexusmigration.ParseConanIdentity(f.entries[0].Identity)
	if _, err := f.store.GetConanRecipeRevision(context.Background(), f.repo.ID, c.Reference, c.RecipeRevision); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("stale owner published Conan metadata: %v", err)
	}
	audits, err := f.store.ListAudits(context.Background(), repository.AuditQuery{Repository: f.repo.Name, Operation: "publish", Limit: 100})
	if err != nil || len(audits) != 0 {
		t.Fatalf("stale owner emitted publication audit: %+v %v", audits, err)
	}
}

func TestPostgresNexusConanMigrationFinalVerifyCannotOutliveOwner(t *testing.T) {
	for _, stop := range []string{"cancel", "expire"} {
		t.Run(stop, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
			defer cancel()
			f := newNexusConanPGFixture(t)
			entered, release := make(chan struct{}), make(chan struct{})
			intercepted := &interceptedConanMigration{beforeFinal: func(ctx context.Context) error {
				close(entered)
				select {
				case <-release:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			}}
			service, batch := nexusConanFenceService(t, f, intercepted)
			done := make(chan error, 1)
			go func() { done <- service.RunOnce(ctx, []string{"conan"}) }()
			select {
			case <-entered:
			case err := <-done:
				current, _ := service.Get(ctx, batch.ID)
				t.Fatalf("worker did not reach Conan final verification: %+v %v", current, err)
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			requireNoConanMigrationPublication(t, f)
			if stop == "cancel" {
				current, err := service.Get(ctx, batch.ID)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = service.Control(ctx, batch.ID, current.Version, "actual-migration-admin", uuid.NewString(), "cancel", nil); err != nil {
					t.Fatal(err)
				}
			} else if _, err := f.db.ExecContext(ctx, `UPDATE nexus_migration_batches SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`, batch.ID); err != nil {
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
			requireNoConanMigrationPublication(t, f)
			if stop == "expire" {
				intercepted.beforeFinal = nil
				if err := service.RunOnce(ctx, []string{"conan"}); err != nil {
					t.Fatal(err)
				}
				current, err := service.Get(ctx, batch.ID)
				if err != nil || current.State != "completed" || current.VisibleIdentities != 1 {
					t.Fatalf("new owner cannot resume exact Conan revision: %+v %v", current, err)
				}
			}
		})
	}
}

func TestPostgresNexusConanMigrationNativeTransactionLossCannotPublish(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	f := newNexusConanPGFixture(t)
	entered, release := make(chan int, 1), make(chan struct{})
	intercepted := &interceptedConanMigration{guard: func(ctx context.Context, tx *sql.Tx) error {
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
	service, batch := nexusConanFenceService(t, f, intercepted)
	done := make(chan error, 1)
	go func() { done <- service.RunOnce(ctx, []string{"conan"}) }()
	var pid int
	select {
	case pid = <-entered:
	case err := <-done:
		current, _ := service.Get(ctx, batch.ID)
		t.Fatalf("worker did not reach actual Conan transaction: %+v %v", current, err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	current, err := service.Get(ctx, batch.ID)
	if err != nil {
		t.Fatal(err)
	}
	var ownsFence bool
	if err = f.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_locks WHERE pid=$1 AND locktype='advisory' AND classid=((hashtextextended('nexus-migration-lease:'||$2,0)>>32)&4294967295)::oid AND objid=(hashtextextended('nexus-migration-lease:'||$2,0)&4294967295)::oid AND objsubid=1 AND granted)`, pid, batch.ID).Scan(&ownsFence); err != nil || !ownsFence {
		t.Fatalf("actual Conan visibility transaction does not own migration fence: %v %v", ownsFence, err)
	}
	blocked, stop := context.WithTimeout(ctx, 30*time.Millisecond)
	_, err = service.Control(blocked, batch.ID, current.Version, "actual-migration-admin", uuid.NewString(), "cancel", nil)
	stop()
	if err == nil {
		t.Fatal("cancellation crossed the actual Conan publication transaction")
	}
	var terminated bool
	if err = f.db.QueryRowContext(ctx, `SELECT pg_terminate_backend($1,1000)`, pid).Scan(&terminated); err != nil || !terminated {
		t.Fatalf("terminate actual owned Conan transaction=%v %v", terminated, err)
	}
	cancelled, err := service.Control(ctx, batch.ID, current.Version, "actual-migration-admin", uuid.NewString(), "cancel", nil)
	if err != nil || cancelled.State != "cancelled" {
		t.Fatalf("cancel after transaction loss=%+v %v", cancelled, err)
	}
	requireNoConanMigrationPublication(t, f)
	close(release)
	select {
	case err = <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	requireNoConanMigrationPublication(t, f)
}
