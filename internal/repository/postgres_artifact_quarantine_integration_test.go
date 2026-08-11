//go:build integration

package repository

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestPostgresArtifactQuarantinePersistsCASState(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is required for PostgreSQL integration tests")
	}
	store, err := NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	repo, err := store.CreateHostedRepository(ctx, HostedRepository{
		ID: uuid.NewString(), Name: "artifact-quarantine-" + uuid.NewString(), Format: FormatRaw,
	})
	if err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = store.db.ExecContext(context.Background(), `DELETE FROM artifact_quarantines WHERE repository_id=$1`, repo.ID)
		_, _ = store.db.ExecContext(context.Background(), `DELETE FROM hosted_repositories WHERE id=$1`, repo.ID)
		_ = store.Close()
	})

	value := ArtifactQuarantine{
		RepositoryID: repo.ID,
		Format:       repo.Format,
		Coordinate:   "releases/widget.bin",
		Digest:       "sha256:" + strings.Repeat("d", 64),
		State:        ArtifactQuarantineStateQuarantined,
		Reason:       "critical vulnerability under investigation",
		UpdatedBy:    "user:alice",
	}
	created, err := store.ReplaceArtifactQuarantine(ctx, value, "0")
	if err != nil || created.Version != "1" || created.QuarantinedAt.IsZero() || !created.ReleasedAt.IsZero() || created.UpdatedAt.IsZero() {
		t.Fatalf("created=%#v err=%v", created, err)
	}
	if _, err = store.ReplaceArtifactQuarantine(ctx, value, "0"); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("stale create err=%v", err)
	}

	release := value
	release.State = ArtifactQuarantineStateReleased
	release.Reason = "finding accepted after review"
	release.UpdatedBy = "user:bob"
	released, err := store.ReplaceArtifactQuarantine(ctx, release, created.Version)
	if err != nil || released.Version != "2" || released.ReleasedAt.IsZero() || !released.QuarantinedAt.Equal(created.QuarantinedAt) {
		t.Fatalf("released=%#v err=%v", released, err)
	}
	if _, err = store.ReplaceArtifactQuarantine(ctx, release, created.Version); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("stale release err=%v", err)
	}
	loaded, err := store.GetArtifactQuarantine(ctx, repo.ID, repo.Format, value.Coordinate, value.Digest)
	if err != nil || loaded != released {
		t.Fatalf("loaded=%#v want=%#v err=%v", loaded, released, err)
	}
}

func TestPostgresArtifactQuarantineCASAcrossConnectionsAndCascade(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is required for PostgreSQL integration tests")
	}
	first, err := NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewPostgresStore(databaseURL)
	if err != nil {
		_ = first.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = first.Close()
		_ = second.Close()
	})

	ctx := context.Background()
	repo, err := first.CreateHostedRepository(ctx, HostedRepository{
		ID: uuid.NewString(), Name: "artifact-quarantine-cas-" + uuid.NewString(), Format: FormatRaw,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = first.db.ExecContext(context.Background(), `DELETE FROM hosted_repositories WHERE id=$1`, repo.ID)
	})

	value := ArtifactQuarantine{
		RepositoryID: repo.ID,
		Format:       FormatRaw,
		Coordinate:   "releases/concurrent.bin",
		Digest:       "sha256:" + strings.Repeat("e", 64),
		State:        ArtifactQuarantineStateQuarantined,
		Reason:       "concurrent security review",
		UpdatedBy:    "user:alice",
	}
	created, err := first.ReplaceArtifactQuarantine(ctx, value, "0")
	if err != nil {
		t.Fatal(err)
	}

	stores := []*PostgresStore{first, second}
	results := make(chan error, len(stores))
	var start sync.WaitGroup
	start.Add(1)
	for index, store := range stores {
		index, store := index, store
		go func() {
			start.Wait()
			release := value
			release.State = ArtifactQuarantineStateReleased
			release.Reason = "review completed"
			release.UpdatedBy = "user:reviewer-" + string(rune('a'+index))
			_, replaceErr := store.ReplaceArtifactQuarantine(ctx, release, created.Version)
			results <- replaceErr
		}()
	}
	start.Done()

	var succeeded, conflicted int
	for range stores {
		switch replaceErr := <-results; {
		case replaceErr == nil:
			succeeded++
		case errors.Is(replaceErr, ErrVersionConflict):
			conflicted++
		default:
			t.Fatalf("replace across connections: %v", replaceErr)
		}
	}
	if succeeded != 1 || conflicted != 1 {
		t.Fatalf("succeeded=%d conflicted=%d", succeeded, conflicted)
	}

	if _, err = first.db.ExecContext(ctx, `DELETE FROM hosted_repositories WHERE id=$1`, repo.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = second.GetArtifactQuarantine(ctx, repo.ID, value.Format, value.Coordinate, value.Digest); !errors.Is(err, ErrNotFound) {
		t.Fatalf("quarantine should cascade with repository deletion, err=%v", err)
	}
}

func TestPostgresArtifactQuarantineDatabaseConstraints(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is required for PostgreSQL integration tests")
	}
	store, err := NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	repo, err := store.CreateHostedRepository(ctx, HostedRepository{
		ID: uuid.NewString(), Name: "aq-constraints-" + uuid.NewString(), Format: FormatRaw,
	})
	if err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = store.db.ExecContext(context.Background(), `DELETE FROM hosted_repositories WHERE id=$1`, repo.ID)
		_ = store.Close()
	})

	validDigest := "sha256:" + strings.Repeat("f", 64)
	tests := []struct {
		name, format, coordinate, digest, state, reason, actor string
		releasedAt                                             any
	}{
		{name: "format", format: "unsupported", coordinate: "releases/widget.bin", digest: validDigest, state: "quarantined", reason: "review", actor: "user:alice"},
		{name: "coordinate", format: string(FormatRaw), coordinate: "", digest: validDigest, state: "quarantined", reason: "review", actor: "user:alice"},
		{name: "digest", format: string(FormatRaw), coordinate: "releases/widget.bin", digest: "sha256:bad", state: "quarantined", reason: "review", actor: "user:alice"},
		{name: "reason", format: string(FormatRaw), coordinate: "releases/widget.bin", digest: validDigest, state: "quarantined", reason: "   ", actor: "user:alice"},
		{name: "actor", format: string(FormatRaw), coordinate: "releases/widget.bin", digest: validDigest, state: "quarantined", reason: "review", actor: "   "},
		{name: "released_at", format: string(FormatRaw), coordinate: "releases/widget.bin", digest: validDigest, state: "released", reason: "review", actor: "user:alice"},
		{name: "Conan package anchor", format: string(FormatConan), coordinate: "widget/1.0/team/stable#rrev/package-id#prev", digest: validDigest, state: "quarantined", reason: "review", actor: "user:alice"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, insertErr := store.db.ExecContext(ctx, `INSERT INTO artifact_quarantines
				(repository_id,format,coordinate,digest,state,reason,updated_by,released_at)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`, repo.ID, test.format, test.coordinate, test.digest, test.state, test.reason, test.actor, test.releasedAt)
			if insertErr == nil {
				t.Fatal("invalid quarantine row was accepted")
			}
		})
	}
}

func TestPostgresArtifactQuarantineSerializesWithDistributionAdmissionAcrossConnections(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is required for PostgreSQL integration tests")
	}
	workerStore, err := NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	workerStore.db.SetMaxOpenConns(1)
	workerStore.db.SetMaxIdleConns(1)
	governanceStore, err := NewPostgresStore(databaseURL)
	if err != nil {
		_ = workerStore.Close()
		t.Fatal(err)
	}
	governanceStore.db.SetMaxOpenConns(1)
	governanceStore.db.SetMaxIdleConns(1)
	t.Cleanup(func() {
		_ = workerStore.Close()
		_ = governanceStore.Close()
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	repo, err := workerStore.CreateHostedRepository(ctx, HostedRepository{
		ID: uuid.NewString(), Name: "artifact-quarantine-lock-" + uuid.NewString(), Format: FormatRaw,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = workerStore.db.ExecContext(context.Background(), `DELETE FROM hosted_repositories WHERE id=$1`, repo.ID)
	})
	coordinate := "releases/serialized.bin"
	digestA := "sha256:" + strings.Repeat("a", 64)
	digestB := "sha256:" + strings.Repeat("b", 64)
	releaseAdmission, err := LockArtifactDistributionAdmissionForDigests(ctx, workerStore, repo.ID, repo.Format, coordinate, []string{digestB, digestA})
	if err != nil {
		t.Fatal(err)
	}
	if loaded, queryErr := workerStore.GetHostedRepository(ctx, repo.ID); queryErr != nil || loaded.ID != repo.ID {
		releaseAdmission()
		t.Fatalf("main pool query while distribution locks are held: loaded=%#v err=%v", loaded, queryErr)
	}

	started := make(chan struct{})
	transitioned := make(chan error, 1)
	go func() {
		close(started)
		releaseTransition, lockErr := LockArtifactQuarantineTransition(ctx, governanceStore, repo.ID, repo.Format, coordinate, digestA)
		if lockErr != nil {
			transitioned <- lockErr
			return
		}
		defer releaseTransition()
		_, replaceErr := governanceStore.ReplaceArtifactQuarantine(ctx, ArtifactQuarantine{
			RepositoryID: repo.ID,
			Format:       repo.Format,
			Coordinate:   coordinate,
			Digest:       digestA,
			State:        ArtifactQuarantineStateQuarantined,
			Reason:       "serialize governance with publication",
			UpdatedBy:    "user:security-admin",
		}, "0")
		transitioned <- replaceErr
	}()
	<-started
	select {
	case transitionErr := <-transitioned:
		releaseAdmission()
		t.Fatalf("quarantine transition bypassed distribution lock: %v", transitionErr)
	case <-time.After(150 * time.Millisecond):
	}

	otherCoordinate := "releases/independent.bin"
	otherDigest := "sha256:" + strings.Repeat("c", 64)
	releaseOther, err := LockArtifactQuarantineTransition(ctx, workerStore, repo.ID, repo.Format, otherCoordinate, otherDigest)
	if err != nil {
		releaseAdmission()
		t.Fatal(err)
	}
	other, err := workerStore.ReplaceArtifactQuarantine(ctx, ArtifactQuarantine{
		RepositoryID: repo.ID,
		Format:       repo.Format,
		Coordinate:   otherCoordinate,
		Digest:       otherDigest,
		State:        ArtifactQuarantineStateQuarantined,
		Reason:       "independent governance transition",
		UpdatedBy:    "user:security-admin",
	}, "0")
	releaseOther()
	if err != nil || other.Version != "1" {
		releaseAdmission()
		t.Fatalf("independent transition while multiple locks are held: value=%#v err=%v", other, err)
	}
	if loaded, queryErr := workerStore.GetArtifactQuarantine(ctx, repo.ID, repo.Format, otherCoordinate, otherDigest); queryErr != nil || loaded != other {
		releaseAdmission()
		t.Fatalf("query independent transition: loaded=%#v want=%#v err=%v", loaded, other, queryErr)
	}

	releaseAdmission()
	select {
	case transitionErr := <-transitioned:
		if transitionErr != nil {
			t.Fatal(transitionErr)
		}
	case <-ctx.Done():
		t.Fatal("quarantine transition did not resume after distribution admission released")
	}

	if release, lockErr := LockArtifactDistributionAdmissionForDigests(ctx, workerStore, repo.ID, repo.Format, coordinate, []string{digestA, digestB}); !errors.Is(lockErr, ErrArtifactQuarantined) {
		if release != nil {
			release()
		}
		t.Fatalf("admission after quarantine err=%v", lockErr)
	}
}

func TestPostgresPyPIDistributionCoordinatesReuseOneLockSessionAtTargetPublish(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is required for PostgreSQL integration tests")
	}
	store, err := NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	store.db.SetMaxOpenConns(1)
	store.db.SetMaxIdleConns(1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	source, err := store.CreateHostedRepository(ctx, HostedRepository{ID: uuid.NewString(), Name: "pypi-lock-source-" + uuid.NewString(), Format: FormatPyPI})
	if err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	target, err := store.CreateHostedRepository(ctx, HostedRepository{ID: uuid.NewString(), Name: "pypi-lock-target-" + uuid.NewString(), Format: FormatPyPI})
	if err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = store.db.ExecContext(context.Background(), `DELETE FROM hosted_repositories WHERE id IN ($1,$2)`, source.ID, target.ID)
		_ = store.Close()
	})

	coordinate := "widget@1.0.0"
	lockedCtx, release, err := LockArtifactDistributionCoordinates(ctx, store, []ArtifactDistributionCoordinate{
		{RepositoryID: source.ID, Format: FormatPyPI, Coordinate: coordinate},
		{RepositoryID: target.ID, Format: FormatPyPI, Coordinate: coordinate},
	})
	if err != nil {
		t.Fatal(err)
	}
	if inUse := store.lockDB.Stats().InUse; inUse != 1 {
		release()
		t.Fatalf("two coordinate locks used %d lock sessions, want 1", inUse)
	}
	if loaded, queryErr := store.GetHostedRepository(ctx, source.ID); queryErr != nil || loaded.ID != source.ID {
		release()
		t.Fatalf("main pool query while coordinate locks are held: loaded=%#v err=%v", loaded, queryErr)
	}
	file := PyPIFile{
		RepositoryID: target.ID, Project: "widget", Version: "1.0.0", Filename: "widget-1.0.0.tar.gz",
		Digest: "sha256:" + strings.Repeat("9", 64), ObjectKey: "native/pypi/widget-1.0.0.tar.gz", Size: 12,
	}
	stored, err := store.PublishPyPIFile(lockedCtx, file)
	release()
	if err != nil || stored.Filename != file.Filename {
		t.Fatalf("nested target publication=%#v err=%v", stored, err)
	}
	if loaded, loadErr := store.GetPyPIFile(ctx, target.ID, file.Filename); loadErr != nil || loaded.Digest != file.Digest {
		t.Fatalf("published target file=%#v err=%v", loaded, loadErr)
	}
}

func TestPostgresPyPIObjectAndCoordinateLocksReuseOneSession(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is required for PostgreSQL integration tests")
	}
	store, err := NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	store.db.SetMaxOpenConns(1)
	store.db.SetMaxIdleConns(1)
	store.lockDB.SetMaxOpenConns(1)
	store.lockDB.SetMaxIdleConns(1)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	objectKeys := make([]string, 40)
	for index := range objectKeys {
		objectKeys[index] = fmt.Sprintf("native/pypi/widget-%02d.whl", index)
	}
	objectCtx, releaseObjects, err := LockObjectKeys(ctx, objectKeys, store, FormatPyPI, store.LockPyPIObject)
	if err != nil {
		t.Fatal(err)
	}
	defer releaseObjects()
	coordinateCtx, releaseCoordinates, err := LockArtifactDistributionCoordinates(objectCtx, store, []ArtifactDistributionCoordinate{
		{RepositoryID: uuid.NewString(), Format: FormatPyPI, Coordinate: "widget@1.0.0"},
		{RepositoryID: uuid.NewString(), Format: FormatPyPI, Coordinate: "widget@1.0.0"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer releaseCoordinates()
	if inUse := store.lockDB.Stats().InUse; inUse != 1 {
		t.Fatalf("object and coordinate locks used %d sessions, want 1", inUse)
	}
	var one int
	if err = store.db.QueryRowContext(coordinateCtx, `SELECT 1`).Scan(&one); err != nil || one != 1 {
		t.Fatalf("primary pool query while 42 locks are held: one=%d err=%v", one, err)
	}
}

func TestPostgresRawAndNPMOperationLocksReuseArtifactSession(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is required for PostgreSQL integration tests")
	}
	store, err := NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	store.db.SetMaxOpenConns(1)
	store.db.SetMaxIdleConns(1)
	store.lockDB.SetMaxOpenConns(1)
	store.lockDB.SetMaxIdleConns(1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	t.Run("raw object then admission", func(t *testing.T) {
		objectCtx, releaseObject, lockErr := LockObjectKeys(ctx, []string{"promotion:target:widget.bin"}, store, FormatRaw, store.LockRawObject)
		if lockErr != nil {
			t.Fatal(lockErr)
		}
		releaseAdmission, lockErr := LockArtifactDistributionUnit(objectCtx, store, uuid.NewString(), FormatRaw, "widget.bin", []string{"sha256:" + strings.Repeat("a", 64)})
		if lockErr != nil {
			releaseObject()
			t.Fatal(lockErr)
		}
		assertSingleArtifactLockSession(t, ctx, store)
		releaseAdmission()
		releaseObject()
	})

	t.Run("npm proxy object then admission", func(t *testing.T) {
		proxyCtx, releaseProxy, lockErr := LockNPMProxyWithContext(ctx, store, "promotion:target:widget:1.0.0")
		if lockErr != nil {
			t.Fatal(lockErr)
		}
		objectCtx, releaseObject, lockErr := LockObjectKeys(proxyCtx, []string{"native/npm/widget.tgz"}, store, FormatNPM, store.LockNPMObject)
		if lockErr != nil {
			releaseProxy()
			t.Fatal(lockErr)
		}
		releaseAdmission, lockErr := LockArtifactDistributionUnit(objectCtx, store, uuid.NewString(), FormatNPM, "widget@1.0.0", []string{"sha256:" + strings.Repeat("b", 64)})
		if lockErr != nil {
			releaseObject()
			releaseProxy()
			t.Fatal(lockErr)
		}
		assertSingleArtifactLockSession(t, ctx, store)
		releaseAdmission()
		releaseObject()
		releaseProxy()
	})
}

func assertSingleArtifactLockSession(t *testing.T, ctx context.Context, store *PostgresStore) {
	t.Helper()
	if inUse := store.lockDB.Stats().InUse; inUse != 1 {
		t.Fatalf("operation locks used %d artifact sessions, want 1", inUse)
	}
	var one int
	if err := store.db.QueryRowContext(ctx, `SELECT 1`).Scan(&one); err != nil || one != 1 {
		t.Fatalf("primary pool query while operation locks are held: one=%d err=%v", one, err)
	}
}
