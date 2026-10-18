//go:build integration

package repository

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"

	"github.com/google/uuid"
)

func openPostgresNPMDistTagStores(t *testing.T) (*PostgresStore, *PostgresStore, HostedRepository) {
	t.Helper()
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
	repo, err := first.CreateHostedRepository(context.Background(), HostedRepository{
		ID: uuid.NewString(), Name: "npm-tags-" + uuid.NewString()[:8], Format: FormatNPM,
	})
	if err != nil {
		_ = first.Close()
		_ = second.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = first.db.ExecContext(context.Background(), `DELETE FROM hosted_repositories WHERE id=$1`, repo.ID)
		_ = first.Close()
		_ = second.Close()
	})
	return first, second, repo
}

func TestPostgresNPMDistTagContract(t *testing.T) {
	store, _, repo := openPostgresNPMDistTagStores(t)
	exerciseNPMDistTagContract(t, store, repo.ID)
	var audited, unchanged int
	if err := store.db.QueryRowContext(context.Background(), `
		SELECT count(*), count(*) FILTER (WHERE evidence->>'changed'='false')
		FROM resolver_audit_log WHERE repository=$1 AND operation='npm.dist-tag.set' AND evidence->>'distTag'='beta'`, repo.ID).Scan(&audited, &unchanged); err != nil {
		t.Fatal(err)
	}
	if audited != 5 || unchanged != 1 {
		t.Fatalf("dist-tag audits=%d unchanged=%d", audited, unchanged)
	}
}

// Two Gateway instances move the same tag concurrently. Every move commits
// with its own audit, and the final tag is the target of the last audited
// commit, so no interleaving leaves a lost or reverted pointer.
func TestPostgresNPMDistTagConcurrentMovesOfOneTag(t *testing.T) {
	first, second, repo := openPostgresNPMDistTagStores(t)
	ctx := context.Background()
	const name = "race-widget"
	versions := []string{"1.0.0", "1.1.0", "1.2.0", "2.0.0"}
	for _, version := range versions {
		publishNPMDistTagFixture(t, first, repo.ID, name, version, map[string]string{"latest": version})
	}
	const rounds = 24
	var wg sync.WaitGroup
	errs := make(chan error, rounds)
	for index := 0; index < rounds; index++ {
		store := first
		if index%2 == 1 {
			store = second
		}
		version := versions[index%len(versions)]
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := store.SetNPMDistTag(ctx, npmDistTagMutation(repo.ID, name, "beta", version)); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent move failed: %v", err)
	}
	pkg, err := second.GetNPMPackage(ctx, repo.ID, name)
	if err != nil {
		t.Fatal(err)
	}
	var lastVersion string
	var audited int
	if err = first.db.QueryRowContext(ctx, `
		SELECT (SELECT evidence->>'version' FROM resolver_audit_log
		        WHERE repository=$1 AND operation='npm.dist-tag.set' AND evidence->>'distTag'='beta'
		        ORDER BY id DESC LIMIT 1),
		       (SELECT count(*) FROM resolver_audit_log
		        WHERE repository=$1 AND operation='npm.dist-tag.set' AND evidence->>'distTag'='beta')`,
		repo.ID).Scan(&lastVersion, &audited); err != nil {
		t.Fatal(err)
	}
	if audited < rounds || pkg.DistTags["beta"] != lastVersion {
		t.Fatalf("final beta=%q last audited=%q audited=%d", pkg.DistTags["beta"], lastVersion, audited)
	}
	// Each committed move saw the previous commit's target: the chain of
	// previous→version pairs has no gap, which a lost update would leave.
	rows, err := first.db.QueryContext(ctx, `
		SELECT evidence->>'previous', evidence->>'version', evidence->>'changed' FROM resolver_audit_log
		WHERE repository=$1 AND operation='npm.dist-tag.set' AND evidence->>'distTag'='beta' ORDER BY id`, repo.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	current := ""
	for rows.Next() {
		var previous, version, changed string
		if err = rows.Scan(&previous, &version, &changed); err != nil {
			t.Fatal(err)
		}
		if previous != current || (changed == "true") != (previous != version) {
			t.Fatalf("move chain broke: current=%q previous=%q version=%q changed=%s", current, previous, version, changed)
		}
		current = version
	}
	if err = rows.Err(); err != nil || current != pkg.DistTags["beta"] {
		t.Fatalf("chain end=%q final=%q err=%v", current, pkg.DistTags["beta"], err)
	}
}

// A tag move racing a tombstone of its target either loses (the tag is
// refused) or wins and is then removed with the version. Neither order may
// leave a tag naming a tombstoned version.
func TestPostgresNPMDistTagMoveRacingTombstoneNeverDangles(t *testing.T) {
	first, second, repo := openPostgresNPMDistTagStores(t)
	ctx := context.Background()
	for round := 0; round < 12; round++ {
		name := "dangling-" + uuid.NewString()[:8]
		publishNPMDistTagFixture(t, first, repo.ID, name, "1.0.0", map[string]string{"latest": "1.0.0"})
		publishNPMDistTagFixture(t, first, repo.ID, name, "2.0.0", map[string]string{"next": "2.0.0"})
		var wg sync.WaitGroup
		var setErr, tombstoneErr error
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, setErr = first.SetNPMDistTag(ctx, npmDistTagMutation(repo.ID, name, "beta", "2.0.0"))
		}()
		go func() {
			defer wg.Done()
			_, tombstoneErr = second.TombstoneNPMVersion(ctx, repo.ID, name, "2.0.0")
		}()
		wg.Wait()
		if tombstoneErr != nil {
			t.Fatalf("round %d tombstone: %v", round, tombstoneErr)
		}
		if setErr != nil && !errors.Is(setErr, ErrNPMDistTagTargetUnavailable) {
			t.Fatalf("round %d set: %v", round, setErr)
		}
		pkg, err := first.GetNPMPackage(ctx, repo.ID, name)
		if err != nil {
			t.Fatal(err)
		}
		for tag, version := range pkg.DistTags {
			if version == "2.0.0" {
				t.Fatalf("round %d tag %s dangles at tombstoned 2.0.0: %#v", round, tag, pkg.DistTags)
			}
		}
	}
}
