//go:build integration

package app

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

func TestPostgresContainsArtifactSearchHTTP(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is required")
	}
	store, err := repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()
	actor := "contains-reader-" + uuid.NewString()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "contains-" + uuid.NewString(), Format: repository.FormatRaw})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReplaceRepositoryGrants(ctx, repo.ID, []repository.RepositoryGrant{{Principal: actor, Scopes: []string{"repositories:read"}}}, "1"); err != nil {
		t.Fatal(err)
	}
	digest := fmt.Sprintf("sha256:%x", sha256.Sum256([]byte("contains-http-"+repo.ID)))
	for _, path := range []string{"a/alpha.bin", "b/Alpha_100%25.bin", "c/%E4%B8%AD%E6%96%87.txt"} {
		if _, err := store.PutRawAsset(ctx, repository.RawAsset{RepositoryID: repo.ID, Path: path, Digest: digest, ObjectKey: "contains/" + repo.ID, Size: 7}); err != nil {
			t.Fatal(err)
		}
	}
	authenticator := testAuthenticator()
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, authenticator)
	for _, endpoint := range []string{"/api/v2/artifact-search", "/api/v2/repositories/" + repo.ID + "/artifact-search"} {
		t.Run(endpoint, func(t *testing.T) {
			query := "Alpha"
			values := url.Values{"q": {query}, "match": {"contains"}, "pageSize": {"1"}}
			request := httptest.NewRequest(http.MethodGet, endpoint+"?"+values.Encode(), nil)
			authorize(request, authenticator.IssueToken(actor))
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			var page struct {
				Items []struct {
					Coordinate string `json:"coordinate"`
				} `json:"items"`
				Next string `json:"nextPageToken"`
			}
			if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &page) != nil || len(page.Items) != 1 || page.Items[0].Coordinate != "a/alpha.bin" || page.Next == "" {
				t.Fatalf("contains PostgreSQL search=%d %s", response.Code, response.Body.String())
			}
			values.Set("pageToken", page.Next)
			page.Next = ""
			request = httptest.NewRequest(http.MethodGet, endpoint+"?"+values.Encode(), nil)
			authorize(request, authenticator.IssueToken(actor))
			response = httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &page) != nil || len(page.Items) != 1 || page.Items[0].Coordinate != "b/Alpha_100%25.bin" || page.Next != "" {
				t.Fatalf("contains next=%d %s", response.Code, response.Body.String())
			}
			values.Set("match", "prefix")
			request = httptest.NewRequest(http.MethodGet, endpoint+"?"+values.Encode(), nil)
			authorize(request, authenticator.IssueToken(actor))
			response = httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != 400 {
				t.Fatalf("cross-mode cursor=%d %s", response.Code, response.Body.String())
			}
		})
	}
}

type countedSearchStore struct {
	GatewayStore
	probes int
}

func (s *countedSearchStore) SearchArtifactProjection(ctx context.Context, id string, format repository.Format, query repository.ArtifactSearchQuery, limit int, after repository.ArtifactSearchPosition) ([]repository.ArtifactSearchItem, error) {
	s.probes++
	return s.GatewayStore.(repository.ArtifactSearchStore).SearchArtifactProjection(ctx, id, format, query, limit, after)
}

func TestPostgresContainsGlobalSearchProbeBudget(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is required")
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
	ctx := context.Background()
	actor := "probe-reader-" + uuid.NewString()
	// Fixed workload and budget: three readable repositories, 20k paths each,
	// page sizes 1/50/200, <=5s HTTP, <=6 projection probes including lookahead.
	for i := 0; i < 3; i++ {
		repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "probe-" + uuid.NewString(), Format: repository.FormatRaw})
		if err != nil {
			t.Fatal(err)
		}
		digest := fmt.Sprintf("sha256:%x", sha256.Sum256([]byte("contains-probe-"+actor)))
		if _, err = store.PutRawAsset(ctx, repository.RawAsset{RepositoryID: repo.ID, Path: "needle/alpha.bin", Digest: digest, ObjectKey: "probe/" + repo.ID, Size: 7}); err != nil {
			t.Fatal(err)
		}
		if _, err = db.ExecContext(ctx, `INSERT INTO native_raw_assets(repository_id,path,digest,content_type) SELECT $1,'releases/artifact-'||lpad(i::text,6,'0')||'/a:b/1.0.bin',$2,'application/octet-stream' FROM generate_series(1,20000) i`, repo.ID, digest); err != nil {
			t.Fatal(err)
		}
		if _, err = store.ReplaceRepositoryGrants(ctx, repo.ID, []repository.RepositoryGrant{{Principal: actor, Scopes: []string{"repositories:read"}}}, "1"); err != nil {
			t.Fatal(err)
		}
	}
	counted := &countedSearchStore{GatewayStore: store}
	auth := testAuthenticator()
	handler := NewGatewayHandler(Dependencies{}, counted, TestAdapter{}, auth)
	for _, q := range []string{"needle", "artifact", "absent-keyword", "x", "ph", "/", "a:b", "1.0"} {
		for _, size := range []string{"1", "50", "200"} {
			counted.probes = 0
			values := url.Values{"q": {q}, "match": {"contains"}, "format": {"raw"}, "pageSize": {size}}
			req := httptest.NewRequest("GET", "/api/v2/artifact-search?"+values.Encode(), nil)
			authorize(req, auth.IssueToken(actor))
			res := httptest.NewRecorder()
			started := time.Now()
			handler.ServeHTTP(res, req)
			elapsed := time.Since(started)
			if res.Code != 200 || elapsed > 5*time.Second || counted.probes > 6 {
				t.Fatalf("q=%q size=%s probes=%d elapsed=%s status=%d body=%s", q, size, counted.probes, elapsed, res.Code, res.Body.String())
			}
			t.Logf("q=%q pageSize=%s projectionProbes=%d HTTP=%s", q, size, counted.probes, elapsed)
		}
	}
}

// Seed only metadata in the explicitly isolated integration database; the HTTP
// assertions exercise the same format handlers used by the Console.
func TestPostgresContainsSearchAcrossFormats(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is required")
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
	ctx := context.Background()
	actor := "contains-cross-" + uuid.NewString()
	digest := fmt.Sprintf("sha256:%x", sha256.Sum256([]byte("contains-cross-"+actor)))
	for _, format := range []repository.Format{repository.FormatMaven, repository.FormatOCI, repository.FormatConan, repository.FormatNPM, repository.FormatPyPI, repository.FormatGo, repository.FormatAPT, repository.FormatCargo} {
		t.Run(string(format), func(t *testing.T) {
			repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "contains-" + string(format) + "-" + uuid.NewString(), Format: format})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = store.ReplaceRepositoryGrants(ctx, repo.ID, []repository.RepositoryGrant{{Principal: actor, Scopes: []string{"repositories:read"}}}, "1"); err != nil {
				t.Fatal(err)
			}
			exec := func(query string, args ...any) {
				t.Helper()
				if _, err := db.ExecContext(ctx, query, args...); err != nil {
					t.Fatal(err)
				}
			}
			coordinate := ""
			switch format {
			case repository.FormatMaven:
				coordinate = "org.example:alpha:1.0"
				exec(`INSERT INTO native_maven_artifacts(id,repository_id,coordinate,digest,state) VALUES($1,$2,$3,$4,'visible')`, uuid.NewString(), repo.ID, coordinate, digest)
			case repository.FormatOCI:
				coordinate = "team/alpha"
				if _, err = store.PutOCIManifest(ctx, repository.OCIManifest{RepositoryID: repo.ID, Name: coordinate, Digest: digest, ObjectKey: "contains/" + repo.ID, MediaType: "application/vnd.oci.image.manifest.v1+json", Size: 7}, "latest"); err != nil {
					t.Fatal(err)
				}
			case repository.FormatConan:
				coordinate = "pkg/1.0/alpha/stable"
				exec(`INSERT INTO native_conan_recipe_revisions(repository_id,reference,revision,digest,state) VALUES($1,$2,'revision',$3,'visible')`, repo.ID, coordinate, digest)
			case repository.FormatNPM:
				coordinate = "@scope/alpha"
				exec(`INSERT INTO native_npm_packages(repository_id,name,dist_tags) VALUES($1,$2,'{"latest":"1.0.0"}')`, repo.ID, coordinate)
				for i, version := range []string{"1.0.0", "2.0.0"} {
					exec(`INSERT INTO native_npm_versions(repository_id,package_name,version,digest,integrity,shasum,tarball_name,object_key,size,manifest,created_at,cached_at) VALUES($1,$2,$3,$4,'sha512-YQ==',$5,$6,$6,7,'{}',$7,$7)`, repo.ID, coordinate, version, digest, strings.Repeat("b", 40), fmt.Sprintf("alpha-%s.tgz", version), fmt.Sprintf("2026-01-0%dT00:00:00Z", i+1))
				}
			case repository.FormatPyPI:
				coordinate = "my-alpha"
				exec(`INSERT INTO native_pypi_files(repository_id,project,version,filename,digest) VALUES($1,$2,'1.0.0','alpha.tar.gz',$3)`, repo.ID, coordinate, digest)
			case repository.FormatGo:
				coordinate = "example.test/alpha"
				exec(`INSERT INTO native_go_versions(repository_id,module_path,version) VALUES($1,$2,'v1.0.0')`, repo.ID, coordinate)
			case repository.FormatAPT:
				coordinate = "pool/main/a/alpha.deb"
				exec(`INSERT INTO native_apt_assets(repository_id,path,digest,object_key,size,source_url) VALUES($1,$2,$3,$4,7,'https://upstream.example.test/alpha.deb')`, repo.ID, coordinate, digest, "contains/"+repo.ID)
			case repository.FormatCargo:
				coordinate = "my-alpha-crate"
				exec(`INSERT INTO native_cargo_names(repository_id,collision_key,name,normalized_name) VALUES($1,$2,$2,$2)`, repo.ID, coordinate)
				exec(`INSERT INTO native_cargo_identity_reservations(repository_id,collision_key,version_key,version,digest,metadata_digest) VALUES($1,$2,'1.0.0','1.0.0',$3,$3)`, repo.ID, coordinate, digest)
				exec(`INSERT INTO native_cargo_publications(repository_id,collision_key,version_key,object_key,size,index_row,publisher,published_at) VALUES($1,$2,'1.0.0',$3,7,'{}','fixture',now())`, repo.ID, coordinate, "native/cargo/sha256/"+strings.Repeat("a", 64))
			}
			authenticator := testAuthenticator()
			handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, authenticator)
			for _, endpoint := range []string{"/api/v2/artifact-search", "/api/v2/repositories/" + repo.ID + "/artifact-search"} {
				values := url.Values{"q": {"ALPHA"}, "match": {"contains"}, "format": {string(format)}}
				request := httptest.NewRequest("GET", endpoint+"?"+values.Encode(), nil)
				authorize(request, authenticator.IssueToken(actor))
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, request)
				var page struct {
					Items []struct {
						Coordinate   string `json:"coordinate"`
						Version      string `json:"version"`
						VersionCount int    `json:"versionCount"`
					} `json:"items"`
				}
				if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &page) != nil || len(page.Items) != 1 || page.Items[0].Coordinate != coordinate {
					t.Fatalf("%s contains=%d %s", endpoint, response.Code, response.Body.String())
				}
				if format == repository.FormatNPM && strings.Contains(endpoint, "/repositories/") && (page.Items[0].Version != "1.0.0" || page.Items[0].VersionCount != 2) {
					t.Fatalf("npm latest/tag or version count changed: %+v", page.Items)
				}
			}
		})
	}
}

func TestPostgresContainsSearchLiteralPermissionAndLegacyCursorBoundaries(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is required")
	}
	store, err := repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	verifyContainsSearchBoundaries(t, store)
}

func TestPostgresPyPISearchExplicitMatchIsLiteralAndOmissionPreservesNormalization(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is required")
	}
	store, err := repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	verifyPyPILiteralMatchAndLegacyNormalization(t, store)
}

func TestPostgresContainsSearchMetadataLockBudget(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is required")
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
	ctx := context.Background()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "metadata-budget-" + uuid.NewString(), Format: repository.FormatRaw})
	if err != nil {
		t.Fatal(err)
	}
	// Lock private table copies through search_path so other integration packages
	// can use their public metadata concurrently without acquiring this fixture's lock.
	schema := "contains_budget_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = db.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := db.ExecContext(context.Background(), "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Errorf("remove budget fixture schema: %v", err)
		}
	})
	for _, table := range []string{"hosted_repositories", "api_keys"} {
		if _, err = db.ExecContext(ctx, "CREATE TABLE "+schema+"."+table+" (LIKE public."+table+" INCLUDING ALL)"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = db.ExecContext(ctx, "INSERT INTO "+schema+".hosted_repositories SELECT * FROM public.hosted_repositories WHERE id=$1", repo.ID); err != nil {
		t.Fatal(err)
	}
	scopedURL, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	values := scopedURL.Query()
	values.Set("search_path", schema+",public")
	scopedURL.RawQuery = values.Encode()
	scopedStore, err := repository.NewPostgresStore(scopedURL.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = scopedStore.Close() })
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, "LOCK TABLE "+schema+".hosted_repositories IN ACCESS EXCLUSIVE MODE"); err != nil {
		t.Fatal(err)
	}
	auth := testAuthenticator()
	auth.APIKeys = scopedStore
	handler := NewGatewayHandler(Dependencies{}, scopedStore, TestAdapter{}, auth)
	for _, anonymous := range []bool{false, true} {
		requestCtx, cancel := context.WithTimeout(ctx, 7*time.Second)
		req := httptest.NewRequest("GET", "/api/v2/repositories/"+repo.ID+"/artifact-search?q=alpha&match=contains", nil).WithContext(requestCtx)
		if !anonymous {
			authorize(req, "admin-secret")
		}
		res := httptest.NewRecorder()
		started := time.Now()
		handler.ServeHTTP(res, req)
		elapsed := time.Since(started)
		cancel()
		if res.Code == 200 || elapsed < 4500*time.Millisecond || elapsed > 6500*time.Millisecond || strings.Contains(res.Body.String(), `"items"`) {
			t.Fatalf("anonymous=%t metadata lock elapsed=%s status=%d body=%s", anonymous, elapsed, res.Code, res.Body.String())
		}
		t.Logf("anonymous=%t complete blocked request=%s status=%d", anonymous, elapsed, res.Code)
	}
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	tx, err = db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, "LOCK TABLE "+schema+".api_keys IN ACCESS EXCLUSIVE MODE"); err != nil {
		t.Fatal(err)
	}
	requestCtx, cancel := context.WithTimeout(ctx, 7*time.Second)
	defer cancel()
	req := httptest.NewRequest("GET", "/api/v2/artifact-search?q=alpha&match=contains", nil).WithContext(requestCtx)
	authorize(req, "blocked-credential-fixture")
	res := httptest.NewRecorder()
	started := time.Now()
	handler.ServeHTTP(res, req)
	elapsed := time.Since(started)
	if res.Code != 401 || elapsed < 4500*time.Millisecond || elapsed > 6500*time.Millisecond {
		t.Fatalf("credential lock elapsed=%s status=%d", elapsed, res.Code)
	}
	t.Logf("credential read complete blocked request=%s status=%d", elapsed, res.Code)
}
