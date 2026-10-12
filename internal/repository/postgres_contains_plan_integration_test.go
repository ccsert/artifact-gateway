//go:build integration

package repository

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Exercise the complete production projection, including folding, joins and
// keyset ordering. Never force an index or explain a simplified replacement.
func TestPostgresContainsSearchPlansAndCancellation(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is required")
	}
	store, err := NewPostgresStore(url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	store.db.SetMaxOpenConns(1) // Capture pgx's exact prepared SQL on this connection.
	ctx := context.Background()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := store.db.ExecContext(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	create := func(format Format) HostedRepository {
		t.Helper()
		r, err := store.CreateHostedRepository(ctx, HostedRepository{ID: uuid.NewString(), Name: "search-plan-" + uuid.NewString(), Format: format})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = store.ReplaceRepositoryGrants(ctx, r.ID, []RepositoryGrant{{Principal: "plan-fixture-owner", Scopes: []string{"repositories:read"}}}, "1"); err != nil {
			t.Fatal(err)
		}
		return r
	}
	raw, maven := create(FormatRaw), create(FormatMaven)
	digest := fmt.Sprintf("sha256:%x", sha256.Sum256([]byte("contains-plan-"+raw.ID)))
	if _, err := store.PutRawAsset(ctx, RawAsset{RepositoryID: raw.ID, Path: "needle/alpha.bin", Digest: digest, ObjectKey: "plan/" + raw.ID, Size: 7}); err != nil {
		t.Fatal(err)
	}
	// Fixed budget before measurement: 20,001 Raw paths, 2,000 Maven builds,
	// <=5s per full query; rows and history are metadata-only synthetic fixtures.
	exec(`INSERT INTO native_raw_assets(repository_id,path,digest,content_type) SELECT $1,'releases/artifact-'||lpad(i::text,6,'0')||'/a:b/1.0.bin',$2,'application/octet-stream' FROM generate_series(1,20000) i`, raw.ID, digest)
	exec(`INSERT INTO native_maven_artifacts(id,repository_id,coordinate,digest,state,build_number) SELECT gen_random_uuid(),$1,'org.example:alpha:1.0-SNAPSHOT',$2,'visible',i FROM generate_series(1,2000) i`, maven.ID, digest)
	exec(`ANALYZE native_raw_assets`)
	exec(`ANALYZE native_maven_artifacts`)
	items, err := store.SearchArtifactProjection(ctx, raw.ID, FormatRaw, ArtifactSearchQuery{Mode: ArtifactSearchByCoordinate, Value: "needle", Match: ArtifactSearchContains}, 51, ArtifactSearchPosition{})
	if err != nil || len(items) != 1 || items[0].Coordinate != "needle/alpha.bin" {
		t.Fatalf("selective result=%v err=%v", items, err)
	}
	statements := map[ArtifactSearchMatch]string{}
	for _, match := range []ArtifactSearchMatch{ArtifactSearchPrefix, ArtifactSearchContains} {
		if _, err = store.SearchArtifactProjection(ctx, raw.ID, FormatRaw, ArtifactSearchQuery{Mode: ArtifactSearchByCoordinate, Value: "needle", Match: match}, 51, ArtifactSearchPosition{}); err != nil {
			t.Fatal(err)
		}
		operator := "coordinate LIKE $4"
		if match == ArtifactSearchContains {
			operator = "coordinate ILIKE $4"
		}
		var sqlText string
		if err = store.db.QueryRowContext(ctx, `SELECT statement FROM pg_prepared_statements WHERE statement LIKE '%WITH ranked AS%' AND position($1 in statement)>0 LIMIT 1`, operator).Scan(&sqlText); err != nil {
			t.Fatal(err)
		}
		statements[match] = sqlText
	}
	type measurement struct {
		Format Format              `json:"format"`
		Match  ArtifactSearchMatch `json:"match"`
		Query  string              `json:"query"`
		Plan   json.RawMessage     `json:"plan"`
	}
	report := struct {
		SQL          map[ArtifactSearchMatch]string `json:"sql"`
		Measurements []measurement                  `json:"measurements"`
	}{SQL: statements}
	for _, format := range []Format{FormatRaw, FormatMaven} {
		id := raw.ID
		if format == FormatMaven {
			id = maven.ID
		}
		for _, match := range []ArtifactSearchMatch{ArtifactSearchPrefix, ArtifactSearchContains} {
			for _, keyword := range []string{"needle", "artifact", "absent-keyword", "x", "ph", "/", "a:b", "1.0", "alpha"} {
				queryCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
				var plan []byte
				err := store.db.QueryRowContext(queryCtx, "EXPLAIN (ANALYZE, BUFFERS, VERBOSE, FORMAT JSON) "+statements[match], id, format, ArtifactSearchByCoordinate, searchTextPattern(keyword, []ArtifactSearchMatch{match}), "", 0, "", 51).Scan(&plan)
				cancel()
				if err != nil {
					t.Fatalf("%s %s %q: %v", format, match, keyword, err)
				}
				var parsed []struct {
					Execution float64 `json:"Execution Time"`
					Planning  float64 `json:"Planning Time"`
				}
				if err = json.Unmarshal(plan, &parsed); err != nil || len(parsed) != 1 {
					t.Fatalf("invalid plan: %s", plan)
				}
				t.Logf("%s %s %q execution=%.3fms planning=%.3fms", format, match, keyword, parsed[0].Execution, parsed[0].Planning)
				report.Measurements = append(report.Measurements, measurement{format, match, keyword, plan})
			}
		}
	}
	if output := os.Getenv("GATEWAY_SEARCH_PLAN_OUTPUT"); output != "" {
		data, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(output, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	// A blocked SQL read must stop promptly on incoming request cancellation.
	store.db.SetMaxOpenConns(2)
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, `LOCK TABLE native_raw_assets IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	queryCtx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err = store.SearchArtifactProjection(queryCtx, raw.ID, FormatRaw, ArtifactSearchQuery{Mode: ArtifactSearchByCoordinate, Value: "alpha", Match: ArtifactSearchContains}, 51, ArtifactSearchPosition{})
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(started) > time.Second {
		t.Fatalf("SQL cancellation elapsed=%v err=%v", time.Since(started), err)
	}
}
