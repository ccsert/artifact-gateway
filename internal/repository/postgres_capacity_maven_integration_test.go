//go:build integration

package repository

import (
	"context"
	"os"
	"testing"

	"github.com/artifact-gateway/artifact-gateway/internal/database"
)

func TestPostgresCapacityListPreservesMavenPrefixSemantics(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is required")
	}
	db, err := database.OpenPostgres(url, database.DefaultPoolConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	// Keep all temporary tables and reads on the same session. Closing it removes
	// the fixture without changing the database's persistent schema or data.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	ctx := context.Background()
	fixture, err := os.ReadFile("testdata/capacity_query_fixture.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, string(fixture)); err != nil {
		t.Fatal(err)
	}
	const first = "00000000-0000-0000-0000-000000000001"
	const second = "00000000-0000-0000-0000-000000000002"
	for _, id := range []string{first, second} {
		if _, err = db.ExecContext(ctx, `INSERT INTO hosted_repositories VALUES ($1,$2,'maven','hosted','','active')`, id, id); err != nil {
			t.Fatal(err)
		}
	}
	coordinates := []struct {
		repo, coordinate, state string
		build                   int
	}{
		{first, "org.example:widget:1.0", "visible", 0},
		{first, "org.example:widget:1.0", "visible", 1},        // SNAPSHOT builds share a prefix.
		{first, "org.example:widget:1.0/nested", "visible", 0}, // Overlap counts once.
		{first, "org.example:removed:1.0", "deleted", 0},
		{first, "org.%_example:包:1.0", "visible", 0},      // Literal %, _ and Unicode.
		{first, ".org:widget:1.0", "visible", 0},          // Leading slash is literal.
		{second, "org.example:removed:1.0", "visible", 0}, // Repository isolation.
	}
	for _, c := range coordinates {
		if _, err = db.ExecContext(ctx, `INSERT INTO native_maven_artifacts VALUES ($1,$2,$3,$4)`, c.repo, c.coordinate, c.state, c.build); err != nil {
			t.Fatal(err)
		}
	}
	assets := []struct {
		path string
		size int64
	}{
		{"org/example/widget/1.0/widget.jar", 11},
		{"org/example/widget/1.0/nested/extra.jar", 13},
		{"org/example/widget/1.0/", 17}, // Exactly the coordinate prefix.
		{"org/%_example/包/1.0/file", 19},
		{"/org/widget/1.0/file", 23},
		{"org/example/widget/1.01/file", 101}, // Prefix boundary must match.
		{"org/example/widget/maven-metadata.xml", 103},
		{"org/example/removed/1.0/file", 107},
		{"orphan.jar", 109},
	}
	for _, a := range assets {
		if _, err = db.ExecContext(ctx, `INSERT INTO native_maven_assets VALUES ($1,$2,$3)`, first, a.path, a.size); err != nil {
			t.Fatal(err)
		}
	}
	store := &PostgresStore{db: db}
	records, err := store.ListRepositoryCapacityRecords(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 {
		t.Fatalf("records: %#v", records)
	}
	for _, record := range records {
		// The individual capacity query retains the previous EXISTS predicate and
		// is an independent oracle for the batch optimization.
		individual, err := store.GetRepositoryCapacity(ctx, record.Repository.ID)
		if err != nil {
			t.Fatal(err)
		}
		if record.Capacity != individual {
			t.Fatalf("batch=%#v individual=%#v", record.Capacity, individual)
		}
		if record.Repository.ID == first && (individual.UsedBytes != 83 || individual.ObjectCount != 5) {
			t.Fatalf("prefix semantics changed: %#v", individual)
		}
		if record.Repository.ID == second && (individual.UsedBytes != 0 || individual.ObjectCount != 0) {
			t.Fatalf("another repository's assets leaked into the aggregate: %#v", individual)
		}
	}
}
