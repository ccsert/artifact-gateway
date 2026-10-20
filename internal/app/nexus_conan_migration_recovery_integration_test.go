//go:build integration

package app

import (
	"bytes"
	"context"
	"testing"

	"github.com/ccsert/artifact-gateway/internal/nexusmigration"
	migrationpg "github.com/ccsert/artifact-gateway/internal/nexusmigration/pgstore"
	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

func TestPostgresRustFSNexusConanMigrationRecoversUploadAndCommitAckLoss(t *testing.T) {
	for _, mode := range []string{"after-upload", "after-publish"} {
		t.Run(mode, func(t *testing.T) {
			f := newNexusConanPGFixture(t)
			ctx := context.Background()
			files := map[string][]byte{}
			for _, asset := range f.entries[0].Assets {
				files[asset.SourcePath] = f.bodies[asset.SourcePath]
			}
			source, created, _, _ := conanMigrationSource(t, f, files)
			publication := NewNexusMigrationPublication(f.store, SharedObjectStores(f.objects), "conan-fixture-target", "conan-fixture-pool").WithConan(NewNexusConanMigrationPublication(f.store, f.store, f.objects))
			options := nexusmigration.Options{TargetID: "conan-fixture-target", PoolID: "conan-fixture-pool"}
			interrupted := false
			fault := func(context.Context, nexusmigration.Entry) error {
				if !interrupted {
					interrupted = true
					return nexusmigration.Failed("fixture_ack_lost")
				}
				return nil
			}
			if mode == "after-upload" {
				options.AfterUpload = fault
			} else {
				options.AfterPublish = fault
			}
			ledger := migrationpg.New(f.db)
			service := nexusmigration.New(ledger, source, publication, options)
			batch, err := service.Start(ctx, nexusmigration.StartInput{SourceID: created.ID, SourceVersion: created.Version, PolicyVersion: nexusmigration.PolicyVersion,
				Mappings: []nexusmigration.Mapping{{SourceRepository: "source", TargetRepositoryID: f.repo.ID}}, CapacityEvidence: conanMigrationEvidence()}, "actual-migration-admin", uuid.NewString())
			if err != nil {
				t.Fatal(err)
			}
			if err = service.RunOnce(ctx, []string{"conan"}); err != nil {
				t.Fatal(err)
			}
			batch, err = service.Get(ctx, batch.ID)
			if err != nil || batch.State != "failed" || !interrupted {
				t.Fatalf("fault was not recorded durably: %+v %v", batch, err)
			}
			if mode == "after-upload" {
				requireNoConanMigrationPublication(t, f)
			}
			_ = f.store.Close()
			f.store, err = repository.NewPostgresStore(f.databaseURL)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = f.store.Close() })
			publication = NewNexusMigrationPublication(f.store, SharedObjectStores(f.objects), "conan-fixture-target", "conan-fixture-pool").WithConan(NewNexusConanMigrationPublication(f.store, f.store, f.objects))
			service = nexusmigration.New(ledger, source, publication, nexusmigration.Options{TargetID: "conan-fixture-target", PoolID: "conan-fixture-pool"})
			evidence := conanMigrationEvidence()
			batch, err = service.Control(ctx, batch.ID, batch.Version, "actual-migration-admin", uuid.NewString(), "retry", &evidence)
			if err != nil {
				t.Fatal(err)
			}
			if err = service.RunOnce(ctx, []string{"conan"}); err != nil {
				t.Fatal(err)
			}
			batch, err = service.Get(ctx, batch.ID)
			if err != nil || batch.State != "completed" || batch.VisibleIdentities != 1 {
				t.Fatalf("restarted exact retry=%+v %v", batch, err)
			}
			if mode == "after-publish" && (batch.Counts.Skipped != 1 || batch.Counts.Committed != 0) {
				t.Fatalf("lost acknowledgement was republished instead of byte-verified skip: %+v", batch)
			}
			entries, _, err := service.Entries(ctx, batch.ID, 100, "")
			if err != nil || len(entries) != 1 {
				t.Fatalf("recovered durable identity count=%d %v", len(entries), err)
			}
			handler := newTestGateway(t, GatewayOptions{Store: f.store, ConanPublications: f.store, Storage: ObjectStores{repository.FormatConan: f.objects}, Adapter: TestAdapter{}, Authenticator: testAuthenticator()})
			assertNexusConanNativeBytes(t, handler, f.repo, entries, files)
			audits, err := f.store.ListAudits(ctx, repository.AuditQuery{Repository: f.repo.Name, Operation: "publish", Limit: 100})
			if err != nil || len(audits) != 1 {
				t.Fatalf("recovery duplicated or lost atomic audit=%+v %v", audits, err)
			}
		})
	}
}

func TestPostgresNexusConanMigrationRejectsSameSizeSourceDrift(t *testing.T) {
	f := newNexusConanPGFixture(t)
	ctx := context.Background()
	files := map[string][]byte{}
	for _, asset := range f.entries[0].Assets {
		files[asset.SourcePath] = f.bodies[asset.SourcePath]
	}
	source, created, _, remote := conanMigrationSource(t, f, files)
	remote.change = func(_ string, body []byte) []byte {
		changed := bytes.Clone(body)
		if len(changed) > 0 {
			changed[0] ^= 1
		}
		return changed
	}
	publication := NewNexusMigrationPublication(f.store, SharedObjectStores(f.objects), "conan-fixture-target", "conan-fixture-pool").WithConan(NewNexusConanMigrationPublication(f.store, f.store, f.objects))
	service := nexusmigration.New(migrationpg.New(f.db), source, publication, nexusmigration.Options{TargetID: "conan-fixture-target", PoolID: "conan-fixture-pool"})
	batch, err := service.Start(ctx, nexusmigration.StartInput{SourceID: created.ID, SourceVersion: created.Version, PolicyVersion: nexusmigration.PolicyVersion,
		Mappings: []nexusmigration.Mapping{{SourceRepository: "source", TargetRepositoryID: f.repo.ID}}, CapacityEvidence: conanMigrationEvidence()}, "actual-migration-admin", uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	if err = service.RunOnce(ctx, []string{"conan"}); err != nil {
		t.Fatal(err)
	}
	batch, err = service.Get(ctx, batch.ID)
	if err != nil || batch.State != "failed" || batch.VisibleIdentities != 0 || batch.Counts.Failed != 1 {
		t.Fatalf("source drift was not explicit failure=%+v %v", batch, err)
	}
	entries, _, err := service.Entries(ctx, batch.ID, 100, "")
	if err != nil || len(entries) != 1 || entries[0].LastError != "source_changed" {
		t.Fatalf("same-size source drift evidence=%+v %v", entries, err)
	}
	requireNoConanMigrationPublication(t, f)
}

func TestPostgresNexusConanMigrationQuarantineCannotBecomeVerifiedSkip(t *testing.T) {
	f := newNexusConanPGFixture(t)
	ctx := context.Background()
	p := NewNexusConanMigrationPublication(f.store, f.store, f.objects)
	for i := range f.entries {
		entry := &f.entries[i]
		locked, release, err := p.Lock(ctx, *entry)
		if err != nil {
			t.Fatal(err)
		}
		stageNexusConan(t, locked, p, entry, f.bodies)
		if err = p.Verify(locked, *entry); err != nil {
			t.Fatal(err)
		}
		if err = p.Publish(locked, *entry); err != nil {
			t.Fatal(err)
		}
		release()
	}
	c, _ := nexusmigration.ParseConanIdentity(f.entries[0].Identity)
	recipe, err := f.store.GetConanRecipeRevision(ctx, f.repo.ID, c.Reference, c.RecipeRevision)
	if err != nil {
		t.Fatal(err)
	}
	decision := repository.ArtifactQuarantine{RepositoryID: f.repo.ID, Format: repository.FormatConan, Coordinate: f.entries[0].Identity,
		Digest: recipe.Digest, State: repository.ArtifactQuarantineStateQuarantined, Reason: "migration gate fixture", UpdatedBy: "security-admin"}
	decision, err = f.store.ReplaceArtifactQuarantine(ctx, decision, "0")
	if err != nil {
		t.Fatal(err)
	}
	source, created, _, _ := conanMigrationSource(t, f, f.bodies)
	publication := NewNexusMigrationPublication(f.store, SharedObjectStores(f.objects), "conan-fixture-target", "conan-fixture-pool").WithConan(p)
	service := nexusmigration.New(migrationpg.New(f.db), source, publication, nexusmigration.Options{TargetID: "conan-fixture-target", PoolID: "conan-fixture-pool"})
	batch, err := service.Start(ctx, nexusmigration.StartInput{SourceID: created.ID, SourceVersion: created.Version, PolicyVersion: nexusmigration.PolicyVersion,
		Mappings: []nexusmigration.Mapping{{SourceRepository: "source", TargetRepositoryID: f.repo.ID}}, CapacityEvidence: conanMigrationEvidence()}, "actual-migration-admin", uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	if err = service.RunOnce(ctx, []string{"conan"}); err != nil {
		t.Fatal(err)
	}
	batch, err = service.Get(ctx, batch.ID)
	if err != nil || batch.State == "completed" || batch.Counts.Conflict != 2 || batch.Counts.Skipped != 0 || batch.VisibleIdentities != 0 {
		t.Fatalf("quarantined complete subtree was accepted as migrated=%+v %v", batch, err)
	}
	actual, err := f.store.GetArtifactQuarantine(ctx, f.repo.ID, repository.FormatConan, decision.Coordinate, decision.Digest)
	if err != nil || actual != decision {
		t.Fatalf("migration changed target quarantine decision=%+v want=%+v %v", actual, decision, err)
	}
}
