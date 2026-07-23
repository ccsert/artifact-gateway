//go:build integration

package app

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

type referenceAfterClaimStore struct {
	repository.NativeMavenStore
	db           *sql.DB
	repositoryID string
}

type blockingDeleteObjectStore struct {
	*MemoryOCIObjectStore
	entered chan struct{}
	release chan struct{}
}

func (s blockingDeleteObjectStore) Delete(ctx context.Context, key string) error {
	s.entered <- struct{}{}
	<-s.release
	return s.MemoryOCIObjectStore.Delete(ctx, key)
}

type blockingMavenCommitStore struct {
	*repository.PostgresStore
	entered chan struct{}
	release chan struct{}
}

type failMavenTombstoneStore struct{ repository.NativeMavenStore }

func (s failMavenTombstoneStore) DeleteClaimedMavenObjectIntent(context.Context, string) error {
	return errors.New("tombstone write interrupted")
}

func (s failMavenTombstoneStore) ReleaseClaimedMavenObjectIntent(context.Context, string) error {
	return errors.New("claim release interrupted")
}

func (s blockingMavenCommitStore) CommitMavenPublishSession(ctx context.Context, id string, assets []repository.MavenAsset) (repository.MavenArtifact, error) {
	s.entered <- struct{}{}
	<-s.release
	return s.PostgresStore.CommitMavenPublishSession(ctx, id, assets)
}

func (s referenceAfterClaimStore) ClaimExpiredMavenObjectIntents(ctx context.Context, before time.Time, limit int) ([]repository.MavenObjectIntent, error) {
	intents, err := s.NativeMavenStore.ClaimExpiredMavenObjectIntents(ctx, before, limit)
	if err != nil {
		return nil, err
	}
	for _, intent := range intents {
		if _, err := s.db.ExecContext(ctx, `INSERT INTO native_maven_object_references (object_key,repository_id) VALUES ($1,$2)`, intent.ObjectKey, s.repositoryID); err != nil {
			return nil, err
		}
	}
	return intents, nil
}

func TestPostgresHTTPIntegration(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is required for PostgreSQL integration tests")
	}
	store, err := repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())
	hostedRequest := httptest.NewRequest(http.MethodPost, "/api/v2/repositories", strings.NewReader(`{"name":"releases","format":"maven"}`))
	authorize(hostedRequest, "admin-secret")
	hostedRequest.Header.Set("Idempotency-Key", "integration-releases")
	hostedCreated := httptest.NewRecorder()
	handler.ServeHTTP(hostedCreated, hostedRequest)
	if hostedCreated.Code != http.StatusCreated {
		t.Fatalf("create Hosted repository = %d %s", hostedCreated.Code, hostedCreated.Body.String())
	}
	var hosted repository.HostedRepository
	if err := json.NewDecoder(hostedCreated.Body).Decode(&hosted); err != nil {
		t.Fatal(err)
	}
	if hosted.State != repository.RepositoryActive || hosted.Version != "1" {
		t.Fatalf("created Hosted repository = %#v", hosted)
	}
	replayRequest := httptest.NewRequest(http.MethodPost, "/api/v2/repositories", strings.NewReader(`{"name":"releases","format":"maven"}`))
	authorize(replayRequest, "admin-secret")
	replayRequest.Header.Set("Idempotency-Key", "integration-releases")
	replayed := httptest.NewRecorder()
	handler.ServeHTTP(replayed, replayRequest)
	var replay repository.HostedRepository
	if replayed.Code != http.StatusCreated || json.NewDecoder(replayed.Body).Decode(&replay) != nil || replay.ID != hosted.ID {
		t.Fatalf("replay Hosted repository = %d %s", replayed.Code, replayed.Body.String())
	}
	hostedDisabled := integrationRequest(handler, http.MethodDelete, "/api/v2/repositories/"+hosted.ID, "", "admin-secret")
	if hostedDisabled.Code != http.StatusAccepted {
		t.Fatalf("disable Hosted repository = %d %s", hostedDisabled.Code, hostedDisabled.Body.String())
	}

	group := `{"name":"engineering","members":[{"name":"proxy","type":"proxy","endpoint":"test://available","position":1},{"name":"hosted","type":"hosted","endpoint":"test://unavailable","position":0}]}`
	created := integrationRequest(handler, http.MethodPost, "/api/v1/oci/groups", group, "admin-secret")
	if created.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", created.Code, created.Body.String())
	}
	got := integrationRequest(handler, http.MethodGet, "/api/v1/oci/groups/engineering", "", "admin-secret")
	if got.Code != http.StatusOK {
		t.Fatalf("get = %d %s", got.Code, got.Body.String())
	}
	var stored repository.Group
	if err := json.NewDecoder(got.Body).Decode(&stored); err != nil {
		t.Fatal(err)
	}
	if len(stored.Members) != 2 || stored.Members[0].Name != "hosted" || stored.Members[1].Type != repository.MemberProxy {
		t.Fatalf("stored group = %#v", stored)
	}
	resolved := integrationRequest(handler, http.MethodGet, "/api/v1/oci/groups/engineering/resolve?repository=team/app", "", "resolver-secret")
	if resolved.Code != http.StatusOK || !strings.Contains(resolved.Body.String(), `"name":"proxy"`) {
		t.Fatalf("resolve = %d %s", resolved.Code, resolved.Body.String())
	}

	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var hostedState string
	if err := db.QueryRowContext(context.Background(), `SELECT state FROM hosted_repositories WHERE id=$1`, hosted.ID).Scan(&hostedState); err != nil {
		t.Fatal(err)
	}
	if hostedState != string(repository.RepositoryDeleting) {
		t.Fatalf("Hosted repository state = %q", hostedState)
	}
	nativeRead := httptest.NewRequest(http.MethodGet, "/maven/releases/com/example/library/1.0/library-1.0.pom", nil)
	authorize(nativeRead, "resolver-secret")
	nativeReadResponse := httptest.NewRecorder()
	handler.ServeHTTP(nativeReadResponse, nativeRead)
	if nativeReadResponse.Code != http.StatusForbidden {
		t.Fatalf("disabled Native Hosted read = %d %s", nativeReadResponse.Code, nativeReadResponse.Body.String())
	}
	var actor, outcome, member string
	if err := db.QueryRowContext(context.Background(), `SELECT actor, outcome, member_name FROM resolver_audit_log WHERE group_name=$1`, "engineering").Scan(&actor, &outcome, &member); err != nil {
		t.Fatal(err)
	}
	if actor != "build-agent" || outcome != string(repository.AuditResolved) || member != "proxy" {
		t.Fatalf("audit = actor=%q outcome=%q member=%q", actor, outcome, member)
	}
	disabled := integrationRequest(handler, http.MethodPost, "/api/v1/oci/groups/engineering/disable", "", "admin-secret")
	if disabled.Code != http.StatusNoContent {
		t.Fatalf("disable = %d %s", disabled.Code, disabled.Body.String())
	}
	blocked := integrationRequest(handler, http.MethodGet, "/api/v1/oci/groups/engineering/resolve?repository=team/app", "", "resolver-secret")
	if blocked.Code != http.StatusConflict || !strings.Contains(blocked.Body.String(), `group_disabled`) {
		t.Fatalf("disabled resolve = %d %s", blocked.Code, blocked.Body.String())
	}

	mavenUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("artifact")) }))
	defer mavenUpstream.Close()
	mavenGroup := fmt.Sprintf(`{"name":"maven-engineering","members":[{"name":"hosted","type":"hosted","endpoint":"%s","position":0},{"name":"proxy","type":"proxy","endpoint":"%s","position":1}]}`, mavenUpstream.URL, mavenUpstream.URL)
	createdMaven := integrationRequest(handler, http.MethodPost, "/api/v1/maven/groups", mavenGroup, "admin-secret")
	if createdMaven.Code != http.StatusCreated {
		t.Fatalf("create Maven group = %d %s", createdMaven.Code, createdMaven.Body.String())
	}
	mavenRequest := httptest.NewRequest(http.MethodGet, "/maven/maven-engineering/com/example/library/1.0/library-1.0.pom", nil)
	mavenRequest.SetBasicAuth("integration", "resolver-secret")
	mavenResponse := httptest.NewRecorder()
	handler.ServeHTTP(mavenResponse, mavenRequest)
	if mavenResponse.Code != http.StatusOK || mavenResponse.Header().Get("X-Artifact-Gateway-Conflict") != "internal-preferred" || mavenResponse.Body.String() != "artifact" {
		t.Fatalf("Maven response = %d headers=%v body=%q", mavenResponse.Code, mavenResponse.Header(), mavenResponse.Body.String())
	}
	var conflictCount, resolvedCount int
	if err := db.QueryRowContext(context.Background(), `SELECT count(*) FROM resolver_audit_log WHERE group_name=$1 AND outcome=$2`, "maven-engineering", repository.AuditInternalPreferred).Scan(&conflictCount); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(context.Background(), `SELECT count(*) FROM resolver_audit_log WHERE group_name=$1 AND outcome=$2`, "maven-engineering", repository.AuditResolved).Scan(&resolvedCount); err != nil {
		t.Fatal(err)
	}
	if conflictCount != 1 || resolvedCount != 1 {
		t.Fatalf("Maven audit counts = conflict:%d resolved:%d", conflictCount, resolvedCount)
	}
}

func TestPostgresMavenCollectorClaimSkipsCommitLockedSession(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is required for PostgreSQL integration tests")
	}
	store, err := repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "claim-fence-" + uuid.NewString(), Format: repository.FormatMaven})
	if err != nil {
		t.Fatal(err)
	}
	session := repository.MavenPublishSession{ID: uuid.NewString(), RepositoryID: repo.ID, Coordinate: "org.example:widget:1.0.0", Publisher: "alice", PomObject: "widget-1.0.0.pom", State: "open", ExpiresAt: time.Now().Add(-time.Hour), Objects: []repository.MavenDeclaredObject{{Name: "widget-1.0.0.pom", Digest: "sha256:claim-fence", Size: 1}}}
	if _, err = store.CreateMavenPublishSession(ctx, session); err != nil {
		t.Fatal(err)
	}
	key := "native/maven/sha256/claim-fence-" + uuid.NewString()
	if err = store.MarkMavenPublishObject(ctx, session.ID, session.Objects[0].Name, key); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SELECT 1 FROM native_maven_publish_sessions WHERE id=$1 FOR UPDATE`, session.ID); err != nil {
		t.Fatal(err)
	}
	claimed, err := store.ClaimExpiredMavenObjectIntents(ctx, time.Now().Add(time.Hour), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(claimed) != 0 {
		t.Fatalf("collector claimed an intent behind commit session lock: %#v", claimed)
	}
}

func TestPostgresMavenMaintenanceRetainsObjectReferencedAfterClaim(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is required for PostgreSQL integration tests")
	}
	store, err := repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "claim-reference-" + uuid.NewString(), Format: repository.FormatMaven})
	if err != nil {
		t.Fatal(err)
	}
	session := repository.MavenPublishSession{ID: uuid.NewString(), RepositoryID: repo.ID, Coordinate: "org.example:claim-reference:1.0.0", Publisher: "collector", PomObject: "claim-reference-1.0.0.pom", State: "open", ExpiresAt: time.Now().Add(-25 * time.Hour), Objects: []repository.MavenDeclaredObject{{Name: "claim-reference-1.0.0.pom", Digest: "sha256:claim-reference", Size: 1}}}
	if _, err = store.CreateMavenPublishSession(ctx, session); err != nil {
		t.Fatal(err)
	}
	key := "native/maven/sha256/claim-reference-" + uuid.NewString()
	if err = store.MarkMavenPublishObject(ctx, session.ID, session.Objects[0].Name, key); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.ExecContext(ctx, `UPDATE native_maven_object_intents SET created_at=now()-interval '25 hours' WHERE object_key=$1`, key); err != nil {
		t.Fatal(err)
	}
	objects := NewMemoryOCIObjectStore()
	if err = objects.Put(ctx, key, []byte("staged")); err != nil {
		t.Fatal(err)
	}
	maintenance := NativeMavenMaintenance{Store: referenceAfterClaimStore{NativeMavenStore: store, db: db, repositoryID: repo.ID}, Objects: objects, Now: func() time.Time { return time.Now() }}
	if err = maintenance.Collect(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = objects.Get(ctx, key); err != nil {
		t.Fatalf("referenced object was deleted: %v", err)
	}
	var intents, references int
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM native_maven_object_intents WHERE object_key=$1`, key).Scan(&intents); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM native_maven_object_references WHERE object_key=$1`, key).Scan(&references); err != nil || intents != 1 || references != 1 {
		t.Fatalf("retained intent/reference intents=%d references=%d err=%v", intents, references, err)
	}
}

func TestPostgresMavenCollectorRecoversClaimAfterTombstoneWriteFailure(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is required for PostgreSQL integration tests")
	}
	store, err := repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "collector-recovery-" + uuid.NewString(), Format: repository.FormatMaven})
	if err != nil {
		t.Fatal(err)
	}
	session := repository.MavenPublishSession{ID: uuid.NewString(), RepositoryID: repo.ID, Coordinate: "org.example:collector-recovery:1.0.0", Publisher: "collector", PomObject: "collector-recovery-1.0.0.pom", State: "open", ExpiresAt: time.Now().Add(-25 * time.Hour), Objects: []repository.MavenDeclaredObject{{Name: "collector-recovery-1.0.0.pom", Digest: "sha256:collector-recovery", Size: 1}}}
	if _, err = store.CreateMavenPublishSession(ctx, session); err != nil {
		t.Fatal(err)
	}
	key := "native/maven/sha256/collector-recovery-" + uuid.NewString()
	if err = store.MarkMavenPublishObject(ctx, session.ID, session.Objects[0].Name, key); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.ExecContext(ctx, `UPDATE native_maven_object_intents SET created_at=now()-interval '25 hours' WHERE object_key=$1`, key); err != nil {
		t.Fatal(err)
	}
	objects := NewMemoryOCIObjectStore()
	if err = objects.Put(ctx, key, []byte("staged")); err != nil {
		t.Fatal(err)
	}
	failed := NativeMavenMaintenance{Store: failMavenTombstoneStore{NativeMavenStore: store}, Objects: objects, Now: func() time.Time { return time.Now() }}
	if err = failed.Collect(ctx); err == nil {
		t.Fatal("collector must report the interrupted tombstone write")
	}
	if _, err = objects.Get(ctx, key); err == nil {
		t.Fatal("first collector must have deleted the staged object")
	}
	var claimedAt, deletedAt sql.NullTime
	if err = db.QueryRowContext(ctx, `SELECT claimed_at, deleted_at FROM native_maven_object_intents WHERE object_key=$1`, key).Scan(&claimedAt, &deletedAt); err != nil || !claimedAt.Valid || deletedAt.Valid {
		t.Fatalf("interrupted state claimed=%v deleted=%v err=%v", claimedAt.Valid, deletedAt.Valid, err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE native_maven_object_intents SET claimed_at=now()-interval '6 minutes' WHERE object_key=$1`, key); err != nil {
		t.Fatal(err)
	}
	if err = (NativeMavenMaintenance{Store: store, Objects: objects, Now: func() time.Time { return time.Now() }}).Collect(ctx); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRowContext(ctx, `SELECT claimed_at, deleted_at FROM native_maven_object_intents WHERE object_key=$1`, key).Scan(&claimedAt, &deletedAt); err != nil || !claimedAt.Valid || !deletedAt.Valid {
		t.Fatalf("recovered tombstone claimed=%v deleted=%v err=%v", claimedAt.Valid, deletedAt.Valid, err)
	}
	retry := repository.MavenPublishSession{ID: uuid.NewString(), RepositoryID: repo.ID, Coordinate: "org.example:collector-retry:1.0.0", Publisher: "retry", PomObject: "collector-recovery-1.0.0.pom", State: "open", ExpiresAt: time.Now().Add(time.Hour), Objects: session.Objects}
	if _, err = store.CreateMavenPublishSession(ctx, retry); err != nil {
		t.Fatal(err)
	}
	if err = store.MarkMavenPublishObject(ctx, retry.ID, retry.PomObject, key); err != nil {
		t.Fatalf("tombstoned object cannot be re-staged: %v", err)
	}
	if err = objects.Put(ctx, key, []byte("restaged")); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRowContext(ctx, `SELECT claimed_at, deleted_at FROM native_maven_object_intents WHERE object_key=$1`, key).Scan(&claimedAt, &deletedAt); err != nil || claimedAt.Valid || deletedAt.Valid {
		t.Fatalf("restaged intent claimed=%v deleted=%v err=%v", claimedAt.Valid, deletedAt.Valid, err)
	}
}

func TestPostgresMavenCommitCannotReferenceObjectDuringBlockedDeletion(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is required for PostgreSQL integration tests")
	}
	store, err := repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "delete-fence-" + uuid.NewString(), Format: repository.FormatMaven})
	if err != nil {
		t.Fatal(err)
	}
	pom := []byte("<project><groupId>org.example</groupId><artifactId>delete-fence</artifactId><version>1.0.0</version></project>")
	sum := sha256.Sum256(pom)
	digest := "sha256:" + fmt.Sprintf("%x", sum)
	declared := repository.MavenDeclaredObject{Name: "delete-fence-1.0.0.pom", Digest: digest, Size: int64(len(pom))}
	stale := repository.MavenPublishSession{ID: uuid.NewString(), RepositoryID: repo.ID, Coordinate: "org.example:delete-fence:1.0.0", Publisher: "stale", PomObject: declared.Name, State: "open", ExpiresAt: time.Now().Add(-25 * time.Hour), Objects: []repository.MavenDeclaredObject{declared}}
	active := repository.MavenPublishSession{ID: uuid.NewString(), RepositoryID: repo.ID, Coordinate: "org.example:delete-fence:1.0.0", Publisher: "active", PomObject: declared.Name, State: "open", ExpiresAt: time.Now().Add(time.Hour), Objects: []repository.MavenDeclaredObject{declared}}
	for _, session := range []repository.MavenPublishSession{stale, active} {
		if _, err = store.CreateMavenPublishSession(ctx, session); err != nil {
			t.Fatal(err)
		}
	}
	key := "native/maven/sha256/" + strings.TrimPrefix(digest, "sha256:")
	if err = store.MarkMavenPublishObject(ctx, stale.ID, declared.Name, key); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.ExecContext(ctx, `UPDATE native_maven_object_intents SET created_at=now()-interval '25 hours' WHERE object_key=$1`, key); err != nil {
		t.Fatal(err)
	}
	if err = store.MarkMavenPublishObject(ctx, active.ID, declared.Name, key); err != nil {
		t.Fatal(err)
	}
	objects := blockingDeleteObjectStore{MemoryOCIObjectStore: NewMemoryOCIObjectStore(), entered: make(chan struct{}, 1), release: make(chan struct{})}
	if err = objects.Put(ctx, key, pom); err != nil {
		t.Fatal(err)
	}
	commitStore := blockingMavenCommitStore{PostgresStore: store, entered: make(chan struct{}, 1), release: make(chan struct{})}
	handler := newNativeMavenHandler(commitStore, objects, testAuthenticator())
	commitResult := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		r := httptest.NewRequest(http.MethodPost, "/api/v2/publish-sessions/"+active.ID+":commit", nil)
		r.Header.Set("Authorization", "Bearer admin-secret")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		commitResult <- w
	}()
	<-commitStore.entered
	maintenanceResult := make(chan error, 1)
	go func() {
		maintenanceResult <- NativeMavenMaintenance{Store: store, Objects: objects, Now: func() time.Time { return time.Now() }}.Collect(ctx)
	}()
	<-objects.entered
	close(commitStore.release)
	commit := <-commitResult
	if commit.Code != http.StatusUnprocessableEntity {
		t.Fatalf("commit during deletion=%d %s", commit.Code, commit.Body.String())
	}
	close(objects.release)
	if err = <-maintenanceResult; err != nil {
		t.Fatal(err)
	}
	if _, err = objects.Get(ctx, key); err == nil {
		t.Fatal("deleted object remained after a fenced commit")
	}
	var references, assets int
	var deletedAt sql.NullTime
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM native_maven_object_references WHERE object_key=$1`, key).Scan(&references); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM native_maven_assets WHERE repository_id=$1`, repo.ID).Scan(&assets); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRowContext(ctx, `SELECT deleted_at FROM native_maven_object_intents WHERE object_key=$1`, key).Scan(&deletedAt); err != nil || !deletedAt.Valid || references != 0 || assets != 0 {
		t.Fatalf("fenced deletion tombstone=%v references=%d assets=%d err=%v", deletedAt.Valid, references, assets, err)
	}
}

func TestPostgresNativeMavenPublishLifecycleIntegration(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is required for PostgreSQL integration tests")
	}
	store, err := repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "maven-lifecycle-" + uuid.NewString(), Format: repository.FormatMaven})
	if err != nil {
		t.Fatal(err)
	}

	// Concurrent first use must yield one session and one replay, then an expired
	// idempotency key can create a fresh session after the original is closed.
	newSession := func(id string) repository.MavenPublishSession {
		return repository.MavenPublishSession{ID: id, RepositoryID: repo.ID, Coordinate: "org.example:concurrent:1.0.0", Publisher: "admin", PomObject: "concurrent-1.0.0.pom", State: "open", ExpiresAt: time.Now().Add(time.Hour), Objects: []repository.MavenDeclaredObject{{Name: "concurrent-1.0.0.pom", Digest: "sha256:concurrent", Size: 1}}}
	}
	type createResult struct {
		session  repository.MavenPublishSession
		replayed bool
		err      error
	}
	results := make(chan createResult, 2)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			session, replayed, createErr := store.CreateMavenPublishSessionIdempotently(ctx, newSession(uuid.NewString()), "admin", "repositories/"+repo.ID+"/publish-sessions", "concurrent-key", "same-payload")
			results <- createResult{session, replayed, createErr}
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	var created []createResult
	for result := range results {
		if result.err != nil {
			t.Fatalf("concurrent session create: %v", result.err)
		}
		created = append(created, result)
	}
	if len(created) != 2 || created[0].session.ID != created[1].session.ID || created[0].replayed == created[1].replayed {
		t.Fatalf("concurrent results=%#v", created)
	}
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.ExecContext(ctx, `UPDATE native_maven_publish_sessions SET state='expired' WHERE id=$1`, created[0].session.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE native_maven_publish_idempotency SET expires_at=now()-interval '1 second' WHERE actor='admin' AND target=$1 AND key='concurrent-key'`, "repositories/"+repo.ID+"/publish-sessions"); err != nil {
		t.Fatal(err)
	}
	fresh, replayed, err := store.CreateMavenPublishSessionIdempotently(ctx, newSession(uuid.NewString()), "admin", "repositories/"+repo.ID+"/publish-sessions", "concurrent-key", "same-payload")
	if err != nil || replayed || fresh.ID == created[0].session.ID {
		t.Fatalf("expired idempotency retry session=%#v replayed=%t err=%v", fresh, replayed, err)
	}

	// Two publishers can stage the same coordinate, but the commit transaction
	// must make exactly one coordinate and its asset path visible.
	commitCoordinate := "org.example:commit-race:1.0.0"
	commitPath := "org/example/commit-race/1.0.0/commit-race-1.0.0.pom"
	commitResults := make(chan error, 2)
	commitStart := make(chan struct{})
	for i := range 2 {
		session := repository.MavenPublishSession{ID: uuid.NewString(), RepositoryID: repo.ID, Coordinate: commitCoordinate, Publisher: fmt.Sprintf("publisher-%d", i), PomObject: "commit-race-1.0.0.pom", State: "open", ExpiresAt: time.Now().Add(time.Hour), Objects: []repository.MavenDeclaredObject{{Name: "commit-race-1.0.0.pom", Digest: fmt.Sprintf("sha256:commit-race-%d", i), Size: 1}}}
		if _, err = store.CreateMavenPublishSession(ctx, session); err != nil {
			t.Fatal(err)
		}
		key := "native/maven/sha256/commit-race-" + uuid.NewString()
		if err = store.MarkMavenPublishObject(ctx, session.ID, session.Objects[0].Name, key); err != nil {
			t.Fatal(err)
		}
		go func(session repository.MavenPublishSession, key string) {
			<-commitStart
			_, commitErr := store.CommitMavenPublishSession(ctx, session.ID, []repository.MavenAsset{{RepositoryID: repo.ID, Path: commitPath, ObjectKey: key, Digest: session.Objects[0].Digest, Size: 1}})
			commitResults <- commitErr
		}(session, key)
	}
	close(commitStart)
	var committed, rejected int
	for range 2 {
		if commitErr := <-commitResults; commitErr == nil {
			committed++
		} else if commitErr == repository.ErrNameExists {
			rejected++
		} else {
			t.Fatalf("concurrent commit error=%v", commitErr)
		}
	}
	if committed != 1 || rejected != 1 {
		t.Fatalf("concurrent commit outcomes committed=%d rejected=%d", committed, rejected)
	}

	// A bad staged checksum must create neither upload metadata nor bytes. A
	// later retry can replace a lost staged object and commit its references.
	objects := NewMemoryOCIObjectStore()
	handler := newNativeMavenHandler(store, objects, testAuthenticator())
	pom := []byte("<project><groupId>org.example</groupId><artifactId>recovery</artifactId><version>1.0.0</version></project>")
	jar := []byte("recovery jar")
	digest := func(body []byte) string { sum := sha256.Sum256(body); return "sha256:" + fmt.Sprintf("%x", sum) }
	session := repository.MavenPublishSession{ID: uuid.NewString(), RepositoryID: repo.ID, Coordinate: "org.example:recovery:1.0.0", Publisher: "admin", PomObject: "recovery-1.0.0.pom", State: "open", ExpiresAt: time.Now().Add(time.Hour), Objects: []repository.MavenDeclaredObject{{Name: "recovery-1.0.0.pom", Digest: digest(pom), Size: int64(len(pom))}, {Name: "recovery-1.0.0.jar", Digest: digest(jar), Size: int64(len(jar))}}}
	if _, err = store.CreateMavenPublishSession(ctx, session); err != nil {
		t.Fatal(err)
	}
	upload := func(name string, body []byte) int {
		r := httptest.NewRequest(http.MethodPut, "/api/v2/publish-sessions/"+session.ID+"/objects/"+name, strings.NewReader(string(body)))
		r.Header.Set("Authorization", "Bearer admin-secret")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w.Code
	}
	if status := upload("recovery-1.0.0.jar", []byte("wrong bytes")); status != http.StatusUnprocessableEntity {
		t.Fatalf("bad checksum status=%d", status)
	}
	var uploads int
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM native_maven_publish_uploads WHERE session_id=$1`, session.ID).Scan(&uploads); err != nil || uploads != 0 {
		t.Fatalf("bad checksum uploads=%d err=%v", uploads, err)
	}
	if status := upload("recovery-1.0.0.pom", pom); status != http.StatusNoContent {
		t.Fatalf("stage pom=%d", status)
	}
	if status := upload("recovery-1.0.0.jar", jar); status != http.StatusNoContent {
		t.Fatalf("stage jar=%d", status)
	}
	jarKey := "native/maven/sha256/" + strings.TrimPrefix(digest(jar), "sha256:")
	if err = objects.Delete(ctx, jarKey); err != nil {
		t.Fatal(err)
	}
	commit := func() int {
		r := httptest.NewRequest(http.MethodPost, "/api/v2/publish-sessions/"+session.ID+":commit", nil)
		r.Header.Set("Authorization", "Bearer admin-secret")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w.Code
	}
	if status := commit(); status != http.StatusUnprocessableEntity {
		t.Fatalf("lost staged object commit=%d", status)
	}
	if status := upload("recovery-1.0.0.jar", jar); status != http.StatusNoContent {
		t.Fatalf("recovery stage jar=%d", status)
	}
	if status := commit(); status != http.StatusOK {
		t.Fatalf("recovered commit=%d", status)
	}
	var assets, references int
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM native_maven_assets WHERE repository_id=$1`, repo.ID).Scan(&assets); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM native_maven_object_references WHERE repository_id=$1`, repo.ID).Scan(&references); err != nil || assets == 0 || references != assets {
		t.Fatalf("asset/reference counts assets=%d references=%d err=%v", assets, references, err)
	}

	// After the 24-hour grace period, claims skip a row locked by a commit and
	// retain an already claimed intent when a reference appears before deletion.
	stale := repository.MavenPublishSession{ID: uuid.NewString(), RepositoryID: repo.ID, Coordinate: "org.example:stale:1.0.0", Publisher: "collector", PomObject: "stale-1.0.0.pom", State: "open", ExpiresAt: time.Now().Add(-25 * time.Hour), Objects: []repository.MavenDeclaredObject{{Name: "stale-1.0.0.pom", Digest: "sha256:stale", Size: 1}}}
	if _, err = store.CreateMavenPublishSession(ctx, stale); err != nil {
		t.Fatal(err)
	}
	staleKey := "native/maven/sha256/stale-" + uuid.NewString()
	if err = store.MarkMavenPublishObject(ctx, stale.ID, stale.Objects[0].Name, staleKey); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE native_maven_object_intents SET created_at=now()-interval '25 hours' WHERE object_key=$1`, staleKey); err != nil {
		t.Fatal(err)
	}
	lock, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = lock.ExecContext(ctx, `SELECT 1 FROM native_maven_publish_sessions WHERE id=$1 FOR UPDATE`, stale.ID); err != nil {
		lock.Rollback()
		t.Fatal(err)
	}
	claimed, err := store.ClaimExpiredMavenObjectIntents(ctx, time.Now().Add(-24*time.Hour), 10)
	if err != nil || len(claimed) != 0 {
		lock.Rollback()
		t.Fatalf("locked stale claim=%#v err=%v", claimed, err)
	}
	if err = lock.Rollback(); err != nil {
		t.Fatal(err)
	}
	claimed, err = store.ClaimExpiredMavenObjectIntents(ctx, time.Now().Add(-24*time.Hour), 10)
	if err != nil || len(claimed) != 1 || claimed[0].ObjectKey != staleKey {
		t.Fatalf("grace-period claim=%#v err=%v", claimed, err)
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO native_maven_object_references (object_key,repository_id) VALUES ($1,$2)`, staleKey, repo.ID); err != nil {
		t.Fatal(err)
	}
	if err = store.DeleteClaimedMavenObjectIntent(ctx, staleKey); err != repository.ErrNotFound {
		t.Fatalf("reference recheck delete=%v", err)
	}
	var retained int
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM native_maven_object_intents WHERE object_key=$1`, staleKey).Scan(&retained); err != nil || retained != 1 {
		t.Fatalf("referenced intent retained=%d err=%v", retained, err)
	}
}

func TestPostgresRawAuditFieldsIntegration(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is required for PostgreSQL integration tests")
	}
	store, err := repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	if _, err := store.CreateRawGroup(context.Background(), repository.Group{Name: "raw-audit", CacheQuotaBytes: 1 << 30, Members: []repository.Member{{Name: "hosted", Type: repository.MemberHosted, Endpoint: "https://gitea.example:8443"}}}); err != nil {
		t.Fatal(err)
	}
	handler := RawHandler{
		Store:         store,
		Authenticator: testAuthenticator(),
		Client:        &rawFixtureClient{responses: map[string]int{"hosted": http.StatusOK}, body: []byte("artifact")},
		Cache:         NewRawCache(NewMemoryOCIObjectStore(), time.Hour, time.Hour, nil),
	}
	request := httptest.NewRequest(http.MethodGet, "/raw/raw-audit/release/app.txt", nil)
	authorize(request, "resolver-secret")
	request.Header.Set("X-Request-ID", "postgres-raw-request")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("Raw response = %d %s", response.Code, response.Body.String())
	}

	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var format, resource, representation, memberType, upstreamHost, operation, disposition, requestID, traceID string
	var status int
	var bytes int64
	err = db.QueryRowContext(context.Background(), `SELECT format, resource, representation, member_type, upstream_host, operation, http_status, cache_disposition, bytes, request_id, trace_id
		FROM resolver_audit_log WHERE group_name=$1 ORDER BY id DESC LIMIT 1`, "raw-audit").Scan(&format, &resource, &representation, &memberType, &upstreamHost, &operation, &status, &disposition, &bytes, &requestID, &traceID)
	if err != nil {
		t.Fatal(err)
	}
	if format != "raw" || resource != "release/app.txt" || representation != "body" || memberType != "hosted" || upstreamHost != "gitea.example" || operation != "get" || status != http.StatusOK || disposition != "miss" || bytes != 8 || requestID != "postgres-raw-request" || len(traceID) != 32 {
		t.Fatalf("Raw audit fields = format=%q resource=%q representation=%q member_type=%q upstream_host=%q operation=%q status=%d disposition=%q bytes=%d request_id=%q trace_id=%q", format, resource, representation, memberType, upstreamHost, operation, status, disposition, bytes, requestID, traceID)
	}
	audits, err := store.ListAudits(context.Background(), repository.AuditQuery{GroupName: "raw-audit"})
	if err != nil || len(audits) != 1 {
		t.Fatalf("ListAudits err=%v audits=%#v", err, audits)
	}
	if audit := audits[0]; audit.Format != "raw" || audit.Resource != "release/app.txt" || audit.Representation != "body" || audit.MemberType != "hosted" || audit.UpstreamHost != "gitea.example" || audit.Operation != "get" || audit.Status != http.StatusOK || audit.CacheDisposition != "miss" || audit.Bytes != 8 || audit.RequestID != "postgres-raw-request" || len(audit.TraceID) != 32 {
		t.Fatalf("ListAudits Raw fields=%#v", audit)
	}
}

func TestPostgresLegacyAuditsRemainQueryable(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is required for PostgreSQL integration tests")
	}
	store, err := repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.ExecContext(context.Background(), `INSERT INTO resolver_audit_log (group_name, repository, member_name, outcome, actor) VALUES ($1,$2,$3,$4,$5)`, "legacy-audit", "legacy-repository", "legacy-member", repository.AuditResolved, "legacy-actor"); err != nil {
		t.Fatal(err)
	}
	audits, err := store.ListAudits(context.Background(), repository.AuditQuery{GroupName: "legacy-audit"})
	if err != nil || len(audits) != 1 {
		t.Fatalf("ListAudits err=%v audits=%#v", err, audits)
	}
	if audit := audits[0]; audit.Actor != "legacy-actor" || audit.Outcome != repository.AuditResolved || audit.Format != "" || audit.Resource != "" || audit.Representation != "" || audit.MemberType != "" || audit.UpstreamHost != "" || audit.Operation != "" || audit.Status != 0 || audit.CacheDisposition != "" || audit.Bytes != 0 {
		t.Fatalf("legacy audit=%#v", audit)
	}
}

func TestPostgresAnonymousMigrationPreservesLegacyOCIAndMavenRows(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is required for PostgreSQL integration tests")
	}
	store, err := repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	for _, statement := range []string{
		`DELETE FROM resolver_audit_log WHERE group_name IN ('legacy-policy-oci', 'legacy-policy-maven')`,
		`DELETE FROM oci_groups WHERE name='legacy-policy-oci'`,
		`DELETE FROM maven_groups WHERE name='legacy-policy-maven'`,
	} {
		if _, err := db.ExecContext(context.Background(), statement); err != nil {
			t.Fatal(err)
		}
	}

	// These inserts and queries deliberately use the pre-policy column lists.
	// They model a populated database and the previous application binary.
	for _, statement := range []string{
		`INSERT INTO oci_groups (name, enabled) VALUES ('legacy-policy-oci', true)`,
		`INSERT INTO oci_group_members (group_name, name, member_type, endpoint, position) VALUES ('legacy-policy-oci', 'hosted', 'hosted', 'https://oci.example', 0)`,
		`INSERT INTO maven_groups (name, enabled) VALUES ('legacy-policy-maven', true)`,
		`INSERT INTO maven_group_members (group_name, name, member_type, endpoint, position) VALUES ('legacy-policy-maven', 'hosted', 'hosted', 'https://maven.example', 0)`,
		`INSERT INTO resolver_audit_log (group_name, repository, member_name, outcome, actor) VALUES ('legacy-policy-oci', 'team/app', 'hosted', 'resolved', 'legacy-reader')`,
	} {
		if _, err := db.ExecContext(context.Background(), statement); err != nil {
			t.Fatal(err)
		}
	}
	for _, query := range []string{
		`SELECT name, enabled, created_at FROM oci_groups WHERE name='legacy-policy-oci'`,
		`SELECT name, enabled, created_at FROM maven_groups WHERE name='legacy-policy-maven'`,
		`SELECT group_name, repository, member_name, outcome, actor, occurred_at FROM resolver_audit_log WHERE actor='legacy-reader'`,
	} {
		if err := db.QueryRowContext(context.Background(), query).Err(); err != nil {
			t.Fatalf("legacy query failed: %s: %v", query, err)
		}
	}
	oci, err := store.GetGroup(context.Background(), "legacy-policy-oci")
	if err != nil || oci.Anonymous || len(oci.Members) != 1 || oci.Members[0].Anonymous {
		t.Fatalf("OCI compatibility group=%#v err=%v", oci, err)
	}
	maven, err := store.GetMavenGroup(context.Background(), "legacy-policy-maven")
	if err != nil || maven.Anonymous || len(maven.Members) != 1 || maven.Members[0].Anonymous {
		t.Fatalf("Maven compatibility group=%#v err=%v", maven, err)
	}
	audits, err := store.ListAudits(context.Background(), repository.AuditQuery{GroupName: "legacy-policy-oci"})
	if err != nil || len(audits) != 1 || audits[0].Actor != "legacy-reader" || audits[0].Outcome != repository.AuditResolved {
		t.Fatalf("legacy audit compatibility audits=%#v err=%v", audits, err)
	}
}

func integrationRequest(handler http.Handler, method, target, body, token string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	authorize(request, token)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
