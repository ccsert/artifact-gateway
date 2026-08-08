//go:build integration

package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

type referenceAfterClaimStore struct {
	repository.NativeMavenStore
	repository.LifecycleJobStore
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

type failMavenTombstoneStore struct {
	repository.NativeMavenStore
	repository.LifecycleJobStore
}

func (s failMavenTombstoneStore) DeleteClaimedMavenObjectIntent(context.Context, string, string) error {
	return errors.New("tombstone write interrupted")
}

func (s failMavenTombstoneStore) ReleaseClaimedMavenObjectIntent(context.Context, string, string) error {
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
	groupCreate := httptest.NewRequest(http.MethodPost, "/api/v2/groups", strings.NewReader(`{"name":"release-group","format":"maven","members":[{"repositoryId":"`+hosted.ID+`","position":0}]}`))
	authorize(groupCreate, "admin-secret")
	groupCreate.Header.Set("Idempotency-Key", "integration-release-group")
	groupCreated := httptest.NewRecorder()
	handler.ServeHTTP(groupCreated, groupCreate)
	if groupCreated.Code != http.StatusCreated {
		t.Fatalf("create Hosted group = %d %s", groupCreated.Code, groupCreated.Body.String())
	}
	var hostedGroup repository.HostedGroup
	if err := json.NewDecoder(groupCreated.Body).Decode(&hostedGroup); err != nil || hostedGroup.Version != "1" {
		t.Fatalf("created Hosted group = %#v err=%v", hostedGroup, err)
	}
	groupReplace := httptest.NewRequest(http.MethodPut, "/api/v2/groups/"+hostedGroup.ID+"/members", strings.NewReader(`[{"repositoryId":"`+hosted.ID+`","position":0}]`))
	authorize(groupReplace, "admin-secret")
	groupReplace.Header.Set("If-Match", "1")
	groupReplaced := httptest.NewRecorder()
	handler.ServeHTTP(groupReplaced, groupReplace)
	if groupReplaced.Code != http.StatusOK || !strings.Contains(groupReplaced.Body.String(), `"version":"2"`) {
		t.Fatalf("replace Hosted group = %d %s", groupReplaced.Code, groupReplaced.Body.String())
	}
	grantReplace := httptest.NewRequest(http.MethodPut, "/api/v2/repositories/"+hosted.ID+"/grants", strings.NewReader(`[{"principal":"integration-reader","scopes":["repositories:read"]}]`))
	authorize(grantReplace, "admin-secret")
	grantReplace.Header.Set("If-Match", "1")
	grantsReplaced := httptest.NewRecorder()
	handler.ServeHTTP(grantsReplaced, grantReplace)
	if grantsReplaced.Code != http.StatusOK || grantsReplaced.Header().Get("ETag") != "2" {
		t.Fatalf("replace repository grants = %d etag=%q body=%s", grantsReplaced.Code, grantsReplaced.Header().Get("ETag"), grantsReplaced.Body.String())
	}
	grantList := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/"+hosted.ID+"/grants", nil)
	authorize(grantList, "admin-secret")
	listedGrants := httptest.NewRecorder()
	handler.ServeHTTP(listedGrants, grantList)
	if listedGrants.Code != http.StatusOK || listedGrants.Header().Get("ETag") != "2" || !strings.Contains(listedGrants.Body.String(), "integration-reader") {
		t.Fatalf("list repository grants = %d etag=%q body=%s", listedGrants.Code, listedGrants.Header().Get("ETag"), listedGrants.Body.String())
	}
	retentionReplace := httptest.NewRequest(http.MethodPut, "/api/v2/repositories/"+hosted.ID+"/retention-policy", strings.NewReader(`{"version":"1","keepDays":21,"minimumVersions":2}`))
	authorize(retentionReplace, "admin-secret")
	retentionReplace.Header.Set("If-Match", "1")
	retentionReplaced := httptest.NewRecorder()
	handler.ServeHTTP(retentionReplaced, retentionReplace)
	if retentionReplaced.Code != http.StatusOK || !strings.Contains(retentionReplaced.Body.String(), `"version":"2"`) {
		t.Fatalf("replace repository retention policy = %d body=%s", retentionReplaced.Code, retentionReplaced.Body.String())
	}
	capacityReplace := httptest.NewRequest(http.MethodPut, "/api/v2/repositories/"+hosted.ID+"/capacity", strings.NewReader(`{"quotaBytes":1024}`))
	authorize(capacityReplace, "admin-secret")
	capacityReplaced := httptest.NewRecorder()
	handler.ServeHTTP(capacityReplaced, capacityReplace)
	if capacityReplaced.Code != http.StatusOK || !strings.Contains(capacityReplaced.Body.String(), `"quotaBytes":1024`) {
		t.Fatalf("replace repository capacity = %d body=%s", capacityReplaced.Code, capacityReplaced.Body.String())
	}
	capacityGet := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/"+hosted.ID+"/capacity", nil)
	authorize(capacityGet, "admin-secret")
	capacityResult := httptest.NewRecorder()
	handler.ServeHTTP(capacityResult, capacityGet)
	if capacityResult.Code != http.StatusOK || !strings.Contains(capacityResult.Body.String(), `"format":"maven"`) || !strings.Contains(capacityResult.Body.String(), `"quotaBytes":1024`) {
		t.Fatalf("get repository capacity = %d body=%s", capacityResult.Code, capacityResult.Body.String())
	}
	artifactSession := repository.MavenPublishSession{ID: uuid.NewString(), RepositoryID: hosted.ID, Coordinate: "org.example:integration:1.0.0", State: "open", ExpiresAt: time.Now().Add(time.Hour), Objects: []repository.MavenDeclaredObject{{Name: "integration-1.0.0.jar", Digest: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", Size: 3}}}
	if _, err = store.CreateMavenPublishSession(context.Background(), artifactSession); err != nil {
		t.Fatal(err)
	}
	artifactKey := "native/maven/sha256/integration-" + artifactSession.ID
	if err = store.MarkMavenPublishObject(context.Background(), artifactSession.ID, "integration-1.0.0.jar", artifactKey); err != nil {
		t.Fatal(err)
	}
	artifact, err := store.CommitMavenPublishSession(context.Background(), artifactSession.ID, []repository.MavenAsset{{RepositoryID: hosted.ID, Path: "org/example/integration/1.0.0/integration-1.0.0.jar", ObjectKey: artifactKey, Digest: artifactSession.Objects[0].Digest, Size: 3}})
	if err != nil {
		t.Fatal(err)
	}
	artifactDelete := httptest.NewRequest(http.MethodDelete, "/api/v2/repositories/"+hosted.ID+"/artifacts/"+artifact.ID, nil)
	authorize(artifactDelete, "admin-secret")
	artifactDeleted := httptest.NewRecorder()
	handler.ServeHTTP(artifactDeleted, artifactDelete)
	if artifactDeleted.Code != http.StatusAccepted {
		t.Fatalf("delete Maven artifact = %d %s", artifactDeleted.Code, artifactDeleted.Body.String())
	}
	tombstoned, err := store.GetMavenArtifact(context.Background(), hosted.ID, artifact.ID)
	if err != nil || tombstoned.State != "deleted" {
		t.Fatalf("tombstone Maven artifact = %#v err=%v", tombstoned, err)
	}
	claimed, err := store.ClaimExpiredMavenObjectIntents(context.Background(), time.Now().Add(time.Hour), 100)
	if err != nil {
		t.Fatalf("claim tombstoned Maven artifact object: %v", err)
	}
	var artifactClaim *repository.MavenObjectIntent
	for index := range claimed {
		if claimed[index].ObjectKey == artifactKey {
			artifactClaim = &claimed[index]
			break
		}
	}
	if artifactClaim == nil {
		t.Fatalf("claim tombstoned Maven artifact object %q = %#v", artifactKey, claimed)
	}
	if err = store.DeleteClaimedMavenObjectIntent(context.Background(), artifactClaim.ObjectKey, artifactClaim.ClaimToken); err != nil {
		t.Fatal(err)
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

func TestPostgresAuditRetentionCleanupDrainsBatchesAndIsDurable(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is required for PostgreSQL integration tests")
	}
	store, err := repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	ctx := context.Background()
	old := time.Now().UTC().AddDate(0, 0, -10)
	for _, actor := range []string{"audit-retention-old-one", "audit-retention-old-two"} {
		if err := store.RecordAudit(ctx, repository.AuditRecord{Actor: actor, OccurredAt: old}); err != nil {
			t.Fatal(err)
		}
	}
	p, err := store.GetAuditRetentionPolicy(ctx)
	if err != nil {
		t.Fatal(err)
	}
	p, err = store.ReplaceAuditRetentionPolicy(ctx, repository.AuditRetentionPolicy{Enabled: true, KeepDays: 1}, p.Version)
	if err != nil {
		t.Fatal(err)
	}
	job, _, err := (AuditRetentionWorker{Store: store}).Enqueue(ctx, "postgres-audit-retention-"+uuid.NewString(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := (AuditRetentionWorker{Store: store}).RunJobs(ctx, 1); err != nil {
		t.Fatal(err)
	}
	jobs, err := store.ListAuditCleanupJobs(ctx, 10)
	if err != nil || len(jobs) == 0 || jobs[0].ID != job.ID || jobs[0].State != repository.LifecycleJobCompleted || jobs[0].Deleted != 2 {
		t.Fatalf("jobs=%#v err=%v", jobs, err)
	}
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var remaining int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM resolver_audit_log WHERE actor IN ('audit-retention-old-one','audit-retention-old-two')`).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 0 {
		t.Fatalf("drained deletion remaining=%d", remaining)
	}
}

func TestPostgresAuditRetentionCleanupReclaimsExpiredRunningJob(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is required for PostgreSQL integration tests")
	}
	store, err := repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	ctx := context.Background()
	policy, err := store.GetAuditRetentionPolicy(ctx)
	if err != nil {
		t.Fatal(err)
	}
	policy, err = store.ReplaceAuditRetentionPolicy(ctx, repository.AuditRetentionPolicy{Enabled: true, KeepDays: 1}, policy.Version)
	if err != nil {
		t.Fatal(err)
	}
	job, _, err := store.EnqueueAuditCleanupJob(ctx, repository.AuditCleanupJob{ID: uuid.NewString(), IdempotencyKey: "postgres-audit-reclaim-" + uuid.NewString(), PolicyVersion: policy.Version, CutoffAt: time.Now().UTC(), BatchSize: 1})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := store.ClaimAuditCleanupJobs(ctx, 1)
	if err != nil || len(claimed) != 1 || claimed[0].ID != job.ID {
		t.Fatalf("initial claim=%#v err=%v", claimed, err)
	}
	claimed, err = store.ClaimAuditCleanupJobs(ctx, 1)
	if err != nil || len(claimed) != 0 {
		t.Fatalf("fresh running claim=%#v err=%v", claimed, err)
	}
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.ExecContext(ctx, `UPDATE audit_cleanup_jobs SET started_at=now() - interval '16 minutes' WHERE id::text=$1`, job.ID); err != nil {
		t.Fatal(err)
	}
	claimed, err = store.ClaimAuditCleanupJobs(ctx, 1)
	if err != nil || len(claimed) != 1 || claimed[0].ID != job.ID || claimed[0].State != repository.LifecycleJobRunning {
		t.Fatalf("expired claim=%#v err=%v", claimed, err)
	}
}

func TestPostgresLifecycleJobStatusManagementHTTP(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is required for PostgreSQL integration tests")
	}
	ctx := context.Background()
	store, err := repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "job-api-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:20], Format: repository.FormatOCI})
	if err != nil {
		t.Fatal(err)
	}
	job := repository.LifecycleJob{ID: uuid.NewString(), RepositoryID: repo.ID, Kind: repository.LifecycleJobReclaim, IdempotencyKey: "reclaim-" + uuid.NewString(), Payload: []byte(`{"format":"oci","objectKey":"private-object-key"}`)}
	if _, _, err = store.EnqueueLifecycleJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())
	request := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/"+repo.ID+"/lifecycle-jobs", nil)
	authorize(request, "admin-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || strings.Contains(response.Body.String(), "private-object-key") {
		t.Fatalf("lifecycle jobs=%d body=%s", response.Code, response.Body.String())
	}
	var jobs []struct {
		ID        string `json:"id"`
		Kind      string `json:"kind"`
		State     string `json:"state"`
		CreatedAt string `json:"createdAt"`
	}
	if err := json.NewDecoder(response.Body).Decode(&jobs); err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 || jobs[0].ID != job.ID || jobs[0].Kind != "reclaim" || jobs[0].State != "pending" || jobs[0].CreatedAt == "" {
		t.Fatalf("jobs=%#v", jobs)
	}
}

func TestPostgresTombstoneInspectionHTTP(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is required for PostgreSQL integration tests")
	}
	ctx := context.Background()
	store, err := repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "tombstone-api-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:20], Format: repository.FormatOCI})
	if err != nil {
		t.Fatal(err)
	}
	digest := "sha256:" + strings.Repeat("a", 64)
	if _, err = store.PutOCIManifest(ctx, repository.OCIManifest{RepositoryID: repo.ID, Name: "team/widget", Digest: digest, ObjectKey: "oci/" + uuid.NewString(), MediaType: "application/json", Size: 1}, "latest"); err != nil {
		t.Fatal(err)
	}
	if err = store.DeleteOCIManifest(ctx, repo.ID, "team/widget", digest); err != nil {
		t.Fatal(err)
	}
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())
	request := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/"+repo.ID+"/tombstones?q=team%2F", nil)
	authorize(request, "admin-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "team/widget@"+digest) {
		t.Fatalf("tombstones=%d body=%s", response.Code, response.Body.String())
	}
}

func TestPostgresConanGroupPreservesManagedRepositoryBinding(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is required")
	}
	store, err := repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "conan-binding-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:20], Format: repository.FormatConan})
	if err != nil {
		t.Fatal(err)
	}
	groupName := "conan-binding-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:20]
	if _, err := store.CreateConanGroup(context.Background(), repository.Group{Name: groupName, Members: []repository.Member{{Name: "remote", Type: repository.MemberProxy, Endpoint: "https://conan.example", AllowedHosts: []string{"conan.example"}, RepositoryID: repo.ID}}}); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.GetConanGroup(context.Background(), groupName)
	if err != nil || len(loaded.Members) != 1 || loaded.Members[0].RepositoryID != repo.ID {
		t.Fatalf("loaded=%#v err=%v", loaded, err)
	}
}

func TestPostgresLegacyGroupsPreserveManagedRepositoryBindings(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is required")
	}
	store, err := repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()

	for _, tc := range []struct {
		name   string
		format repository.Format
		create func(repository.Group) error
		load   func(string) (repository.Group, error)
	}{
		{name: "oci", format: repository.FormatOCI, create: func(group repository.Group) error {
			_, err := store.CreateGroup(context.Background(), group)
			return err
		}, load: func(name string) (repository.Group, error) { return store.GetGroup(context.Background(), name) }},
		{name: "maven", format: repository.FormatMaven, create: func(group repository.Group) error {
			_, err := store.CreateMavenGroup(context.Background(), group)
			return err
		}, load: func(name string) (repository.Group, error) { return store.GetMavenGroup(context.Background(), name) }},
		{name: "raw", format: repository.FormatRaw, create: func(group repository.Group) error {
			_, err := store.CreateRawGroup(context.Background(), group)
			return err
		}, load: func(name string) (repository.Group, error) { return store.GetRawGroup(context.Background(), name) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:20]
			repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: tc.name + "-binding-" + suffix, Format: tc.format})
			if err != nil {
				t.Fatal(err)
			}
			groupName := tc.name + "-group-" + suffix
			if err := tc.create(repository.Group{Name: groupName, CacheQuotaBytes: 1 << 20, Members: []repository.Member{{Name: "bound", Type: repository.MemberHosted, Endpoint: "https://" + tc.name + ".example", RepositoryID: repo.ID}}}); err != nil {
				t.Fatal(err)
			}
			loaded, err := tc.load(groupName)
			if err != nil || len(loaded.Members) != 1 || loaded.Members[0].RepositoryID != repo.ID {
				t.Fatalf("loaded=%#v err=%v", loaded, err)
			}
		})
	}
}

func TestPostgresAuditRetainsRepositoryGrantDecision(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is required")
	}
	store, err := repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	group := "grant-audit-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:20]
	if err := store.RecordAudit(context.Background(), repository.AuditRecord{
		GroupName: group, Repository: group, Actor: "reader", Outcome: repository.AuditAccessDenied, OccurredAt: time.Now().UTC(),
		Format: "management", Resource: "repositories/id", Operation: "write", Status: http.StatusForbidden, CacheDisposition: "bypass",
		AuthorizationSource: "repository_grants", AuthorizationReason: "scope_not_granted",
	}); err != nil {
		t.Fatal(err)
	}
	audits, err := store.ListAudits(context.Background(), repository.AuditQuery{GroupName: group})
	if err != nil || len(audits) != 1 || audits[0].AuthorizationSource != "repository_grants" || audits[0].AuthorizationReason != "scope_not_granted" {
		t.Fatalf("audits=%#v err=%v", audits, err)
	}
}

func TestPostgresAuditPageUsesIDToBreakTimestampTies(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is required")
	}
	store, err := repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	group := "cursor-audit-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:20]
	occurredAt := time.Now().UTC().Truncate(time.Microsecond)
	for _, actor := range []string{"first", "second", "third"} {
		if err := store.RecordAudit(context.Background(), repository.AuditRecord{
			GroupName: group, Repository: group, Actor: actor, Outcome: repository.AuditResolved, OccurredAt: occurredAt,
		}); err != nil {
			t.Fatal(err)
		}
	}
	first, err := store.ListAuditPage(context.Background(), repository.AuditQuery{GroupName: group, Limit: 2})
	if err != nil || len(first.Items) != 2 || first.Items[0].Actor != "third" || first.Items[1].Actor != "second" || first.Next == nil {
		t.Fatalf("first page=%#v err=%v", first, err)
	}
	second, err := store.ListAuditPage(context.Background(), repository.AuditQuery{GroupName: group, Limit: 2, Before: *first.Next})
	if err != nil || len(second.Items) != 1 || second.Items[0].Actor != "first" || second.Next != nil {
		t.Fatalf("second page=%#v err=%v", second, err)
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

func TestPostgresMavenRetentionDryRunReturnsCandidatesWithoutTombstoning(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is required for PostgreSQL integration tests")
	}
	ctx := context.Background()
	store, err := repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "retention-dry-run-pg-" + uuid.NewString(), Format: repository.FormatMaven})
	if err != nil {
		t.Fatal(err)
	}
	policy, err := store.ReplaceRepositoryRetentionPolicy(ctx, repo.ID, repository.RepositoryRetentionPolicy{Enabled: true, KeepDays: 1, SnapshotKeepDays: 1, MinimumVersions: 1}, "1")
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	now := time.Now().UTC()
	coordinates := []string{"org.example:dry-run:1.0.0", "org.example:dry-run:1.1.0", "org.example:dry-run:1.2.0"}
	for index, coordinate := range coordinates {
		createdAt := now.Add(-time.Duration(72-index*30) * time.Hour)
		if _, err = db.ExecContext(ctx, `INSERT INTO native_maven_artifacts (id,repository_id,coordinate,digest,state,created_at) VALUES ($1,$2,$3,$4,'visible',$5)`, uuid.NewString(), repo.ID, coordinate, "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", createdAt); err != nil {
			t.Fatal(err)
		}
	}
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())
	response := integrationRequest(handler, http.MethodPost, "/api/v2/repositories/"+repo.ID+"/retention:dry-run", "", "admin-secret")
	if response.Code != http.StatusOK {
		t.Fatalf("dry run=%d body=%s", response.Code, response.Body.String())
	}
	var result struct {
		PolicyVersion string `json:"policyVersion"`
		Candidates    []struct {
			Coordinate string `json:"coordinate"`
			Digest     string `json:"digest"`
		} `json:"candidates"`
	}
	if err = json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if result.PolicyVersion != policy.Version || len(result.Candidates) != 2 || result.Candidates[0].Coordinate != coordinates[0] || result.Candidates[1].Coordinate != coordinates[1] {
		t.Fatalf("dry run result=%#v", result)
	}
	visible, err := store.ListMavenArtifacts(ctx, repo.ID)
	if err != nil || len(visible) != 3 {
		t.Fatalf("dry run changed artifact visibility=%#v err=%v", visible, err)
	}
}

func integrationRequest(handler http.Handler, method, target, body, token string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	authorize(request, token)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
