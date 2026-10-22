//go:build integration

package pgstore_test

import (
	"context"
	"database/sql"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/ccsert/artifact-gateway/internal/repositorypreset/pgstore"
	"github.com/ccsert/artifact-gateway/internal/testsupport"
	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
)

func presetDatabase(t *testing.T, existing bool) *sql.DB {
	t.Helper()
	raw := os.Getenv("TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("TEST_DATABASE_URL is required")
	}
	if err := testsupport.ValidateIsolatedDatabaseURL(raw); err != nil {
		t.Fatal(err)
	}
	control, err := sql.Open("pgx", raw)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = control.Close() })
	schema := "presets_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := control.Exec("CREATE SCHEMA " + schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := control.Exec("DROP SCHEMA " + schema + " CASCADE"); err != nil {
			t.Error(err)
		}
	})
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	db, err := sql.Open("pgx", parsed.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for _, table := range []string{"hosted_repositories", "hosted_groups", "hosted_group_members"} {
		if _, err := db.Exec("CREATE TABLE " + table + " (LIKE public." + table + " INCLUDING ALL)"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`ALTER TABLE hosted_group_members ADD FOREIGN KEY(group_id) REFERENCES hosted_groups(id) ON DELETE CASCADE;
		ALTER TABLE hosted_group_members ADD FOREIGN KEY(repository_id) REFERENCES hosted_repositories(id)`); err != nil {
		t.Fatal(err)
	}
	if existing {
		if _, err := db.Exec(`INSERT INTO hosted_repositories (id,name,format) VALUES ($1,'admin-owned','raw')`, uuid.NewString()); err != nil {
			t.Fatal(err)
		}
	}
	migration, err := os.ReadFile(filepath.Join("..", "..", "..", "migrations", "000174_installation_repository_presets.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(string(migration)); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestPostgresPresetsConcurrentInitializationAndAdministratorLifecycle(t *testing.T) {
	db := presetDatabase(t, false)
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := pgstore.New(db).Initialize(context.Background())
			if err != nil || result.State != "applied" || result.Repositories != 13 || result.Groups != 5 {
				t.Errorf("result=%+v err=%v", result, err)
			}
		}()
	}
	wg.Wait()
	var hosted, proxies, groups, members, anonymous int
	if err := db.QueryRow(`SELECT count(*) FILTER (WHERE repo_type='hosted'),
		count(*) FILTER (WHERE repo_type='proxy'), count(*) FILTER (WHERE anonymous_read)
		FROM hosted_repositories`).Scan(&hosted, &proxies, &anonymous); err != nil || hosted != 8 || proxies != 5 || anonymous != 0 {
		t.Fatalf("hosted=%d proxy=%d anonymous=%d err=%v", hosted, proxies, anonymous, err)
	}
	if err := db.QueryRow(`SELECT count(*),count(*) FILTER (WHERE anonymous_read) FROM hosted_groups`).Scan(&groups, &anonymous); err != nil || groups != 5 || anonymous != 0 {
		t.Fatalf("groups=%d anonymous=%d err=%v", groups, anonymous, err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM hosted_group_members`).Scan(&members); err != nil || members != 10 {
		t.Fatalf("group members=%d err=%v", members, err)
	}
	store, err := repository.NewPostgresStoreWithPools(db, db, db)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	group, err := store.GetHostedGroupByName(context.Background(), "npm-all")
	if err != nil || len(group.Members) != 2 || group.AnonymousRead {
		t.Fatalf("group=%+v err=%v", group, err)
	}
	for position, expected := range []string{"npm-hosted", "npmjs"} {
		member, err := store.GetHostedRepository(context.Background(), group.Members[position].RepositoryID)
		if err != nil || member.Name != expected || member.AnonymousRead {
			t.Fatalf("member=%+v err=%v", member, err)
		}
	}
	if _, err := db.Exec(`UPDATE hosted_repositories SET endpoint='https://operator.example',version=7 WHERE name='npmjs';
		UPDATE hosted_repositories SET state='deleted',version=9 WHERE name='raw-hosted';
		DELETE FROM hosted_groups WHERE name='maven-all'`); err != nil {
		t.Fatal(err)
	}
	// A newly constructed store models process restart; marker state, rather than
	// the absence of an individual default, determines whether initialization runs.
	if _, err := pgstore.New(db).Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	proxy, err := store.GetHostedRepositoryByName(context.Background(), "npmjs")
	if err != nil || proxy.Endpoint != "https://operator.example" || proxy.Version != "7" {
		t.Fatalf("proxy=%+v err=%v", proxy, err)
	}
	var deletedState string
	if err := db.QueryRow(`SELECT state FROM hosted_repositories WHERE name='raw-hosted'`).Scan(&deletedState); err != nil || deletedState != "deleted" {
		t.Fatalf("state=%s err=%v", deletedState, err)
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM hosted_groups WHERE name='maven-all'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("deleted group count=%d err=%v", count, err)
	}
}

func TestPostgresPresetsSkipAnExistingInstallation(t *testing.T) {
	db := presetDatabase(t, true)
	result, err := pgstore.New(db).Initialize(context.Background())
	if err != nil || result.State != "skipped_existing" || result.Repositories != 0 || result.Groups != 0 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM hosted_repositories`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("count=%d err=%v", count, err)
	}
}

func TestPostgresPresetsFailureRollsBackAndCanRetry(t *testing.T) {
	db := presetDatabase(t, false)
	if _, err := db.Exec(`ALTER TABLE hosted_groups ADD CONSTRAINT injected_failure CHECK(name <> 'npm-all')`); err != nil {
		t.Fatal(err)
	}
	if _, err := pgstore.New(db).Initialize(context.Background()); err == nil {
		t.Fatal("injected SQL failure succeeded")
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM hosted_repositories`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("partial repositories=%d err=%v", count, err)
	}
	var state string
	if err := db.QueryRow(`SELECT state FROM installation_repository_presets`).Scan(&state); err != nil || state != "pending" {
		t.Fatalf("state=%s err=%v", state, err)
	}
	if _, err := db.Exec(`ALTER TABLE hosted_groups DROP CONSTRAINT injected_failure`); err != nil {
		t.Fatal(err)
	}
	result, err := pgstore.New(db).Initialize(context.Background())
	if err != nil || result.State != "applied" || result.Repositories != 13 || result.Groups != 5 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestPostgresPresetsPreserveCatalogCreatedAfterMigration(t *testing.T) {
	db := presetDatabase(t, false)
	if _, err := db.Exec(`INSERT INTO hosted_repositories(id,name,format)
		VALUES ($1,'operator-created-after-upgrade','raw')`, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	result, err := pgstore.New(db).Initialize(context.Background())
	if err != nil || result.State != "skipped_existing" || result.Repositories != 0 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM hosted_repositories`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("count=%d err=%v", count, err)
	}
}
