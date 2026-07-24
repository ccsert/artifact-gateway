//go:build integration

package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	claimed, err := store.ClaimExpiredMavenObjectIntents(context.Background(), time.Now().Add(time.Hour), 1)
	if err != nil || len(claimed) != 1 || claimed[0].ObjectKey != artifactKey {
		t.Fatalf("claim tombstoned Maven artifact object = %#v err=%v", claimed, err)
	}
	if err = store.DeleteClaimedMavenObjectIntent(context.Background(), claimed[0].ObjectKey, claimed[0].ClaimToken); err != nil {
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

func TestPostgresNativeOCIStateTransitions(t *testing.T) {
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
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "oci-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:20], Format: repository.FormatOCI})
	if err != nil {
		t.Fatal(err)
	}
	digest := "sha256:" + strings.Repeat("a", 64)
	upload, err := store.CreateOCIUpload(ctx, repository.OCIUpload{ID: uuid.NewString(), RepositoryID: repo.ID, Name: "widget", ObjectKey: "native/oci/uploads/" + uuid.NewString(), State: "open", ExpiresAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if upload, err = store.UpdateOCIUpload(ctx, upload.ID, 12); err != nil || upload.Offset != 12 {
		t.Fatalf("update upload=%#v err=%v", upload, err)
	}
	blob, err := store.CompleteOCIUpload(ctx, upload.ID, repository.OCIBlob{Digest: digest, ObjectKey: "native/oci/blobs/sha256/" + strings.Repeat("a", 64), Size: 12})
	if err != nil || blob.Digest != digest {
		t.Fatalf("complete blob=%#v err=%v", blob, err)
	}
	if _, err = store.MountOCIBlob(ctx, repo.ID, digest); err != nil {
		t.Fatalf("idempotent mount: %v", err)
	}
	manifestDigest := "sha256:" + strings.Repeat("b", 64)
	manifest, err := store.PutOCIManifest(ctx, repository.OCIManifest{RepositoryID: repo.ID, Name: "widget", Digest: manifestDigest, ObjectKey: "native/oci/manifests/" + uuid.NewString(), MediaType: "application/vnd.oci.image.manifest.v1+json", Size: 42}, "latest")
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := store.GetOCIManifest(ctx, repo.ID, "widget", "latest")
	if err != nil || resolved.Digest != manifest.Digest {
		t.Fatalf("tag resolution=%#v err=%v", resolved, err)
	}
	if err = store.DeleteOCIManifest(ctx, repo.ID, "widget", manifest.Digest); err != nil {
		t.Fatalf("delete manifest: %v", err)
	}
	if _, err = store.GetOCIManifest(ctx, repo.ID, "widget", "latest"); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("deleted tag lookup=%v", err)
	}
}

func TestNativeOCIHostedHTTPAcrossPostgresAndMinIOGatewayInstances(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	s3Endpoint := os.Getenv("TEST_S3_ENDPOINT")
	accessKey := os.Getenv("TEST_S3_ACCESS_KEY")
	secretKey := os.Getenv("TEST_S3_SECRET_KEY")
	if databaseURL == "" || s3Endpoint == "" || accessKey == "" || secretKey == "" {
		t.Skip("PostgreSQL and S3 integration environment is required")
	}
	storeA, err := repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer storeA.Close()
	storeB, err := repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer storeB.Close()
	bucket := "native-oci-http-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:20]
	objectsA, err := NewS3OCIObjectStore(s3Endpoint, accessKey, secretKey, bucket)
	if err != nil {
		t.Fatal(err)
	}
	if err = objectsA.EnsureBucket(context.Background()); err != nil {
		t.Fatal(err)
	}
	objectsB, err := NewS3OCIObjectStore(s3Endpoint, accessKey, secretKey, bucket)
	if err != nil {
		t.Fatal(err)
	}
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:18]
	source, err := storeA.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "oci-src-" + suffix, Format: repository.FormatOCI})
	if err != nil {
		t.Fatal(err)
	}
	target, err := storeA.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "oci-dst-" + suffix, Format: repository.FormatOCI})
	if err != nil {
		t.Fatal(err)
	}
	serverA := httptest.NewServer(NewGatewayHandler(Dependencies{NativeOCIObjectStore: objectsA}, storeA, TestAdapter{}, testAuthenticator()))
	defer serverA.Close()
	serverB := httptest.NewServer(NewGatewayHandler(Dependencies{NativeOCIObjectStore: objectsB}, storeB, TestAdapter{}, testAuthenticator()))
	defer serverB.Close()
	client := serverA.Client()
	request := func(method, address string, body []byte) *http.Response {
		t.Helper()
		r, err := http.NewRequest(method, address, bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		r.Header.Set("Authorization", "Bearer resolver-secret")
		response, err := client.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		return response
	}
	body := []byte("cross-instance OCI blob")
	sum := sha256.Sum256(body)
	digest := "sha256:" + fmt.Sprintf("%x", sum[:])
	start := request(http.MethodPost, serverA.URL+"/v2/"+source.Name+"/app/blobs/uploads/", nil)
	if start.StatusCode != http.StatusAccepted {
		t.Fatalf("start upload=%d", start.StatusCode)
	}
	location := start.Header.Get("Location")
	_ = start.Body.Close()
	if location == "" {
		t.Fatal("upload location is missing")
	}
	complete := request(http.MethodPut, serverA.URL+location+"?digest="+digest, body)
	if complete.StatusCode != http.StatusCreated {
		t.Fatalf("complete upload=%d", complete.StatusCode)
	}
	_ = complete.Body.Close()
	mount := request(http.MethodPost, serverB.URL+"/v2/"+target.Name+"/app/blobs/uploads/?mount="+digest+"&from="+source.Name+"/app", nil)
	if mount.StatusCode != http.StatusCreated {
		t.Fatalf("cross-instance mount=%d", mount.StatusCode)
	}
	_ = mount.Body.Close()
	manifest := []byte(`{"schemaVersion":2,"config":{"digest":"` + digest + `","size":` + fmt.Sprint(len(body)) + `}}`)
	publish := request(http.MethodPut, serverB.URL+"/v2/"+target.Name+"/app/manifests/latest", manifest)
	if publish.StatusCode != http.StatusCreated {
		t.Fatalf("cross-instance manifest publish=%d", publish.StatusCode)
	}
	_ = publish.Body.Close()
	read := request(http.MethodGet, serverA.URL+"/v2/"+target.Name+"/app/manifests/latest", nil)
	defer read.Body.Close()
	got, err := io.ReadAll(read.Body)
	if err != nil {
		t.Fatal(err)
	}
	if read.StatusCode != http.StatusOK || !bytes.Equal(got, manifest) {
		t.Fatalf("cross-instance manifest read=%d body=%q", read.StatusCode, got)
	}
}

func TestPostgresNativeRawStateTransitions(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is required for PostgreSQL integration tests")
	}
	store, err := repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "raw-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:20], Format: repository.FormatRaw})
	if err != nil {
		t.Fatal(err)
	}
	digest := "sha256:" + strings.Repeat("c", 64)
	objectKey := "native/raw/sha256/" + strings.Repeat("c", 64)
	if err := store.StageRawObject(context.Background(), repository.RawObject{Digest: digest, ObjectKey: objectKey, Size: 12}); err != nil {
		t.Fatalf("stage raw object: %v", err)
	}
	asset, err := store.PutRawAsset(context.Background(), repository.RawAsset{RepositoryID: repo.ID, Path: "releases/app.txt", Digest: digest, ObjectKey: objectKey, Size: 12, ContentType: "text/plain"})
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := store.GetRawAsset(context.Background(), repo.ID, asset.Path)
	if err != nil || loaded.Digest != digest || loaded.ContentType != "text/plain" {
		t.Fatalf("load=%#v err=%v", loaded, err)
	}
	if err = store.DeleteRawAsset(context.Background(), repo.ID, asset.Path); err != nil {
		t.Fatal(err)
	}
	if _, err = store.GetRawAsset(context.Background(), repo.ID, asset.Path); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("deleted asset lookup=%v", err)
	}
	objects, err := store.ListUnreferencedRawObjects(context.Background(), time.Now().Add(time.Hour), 10)
	if err != nil || len(objects) != 1 || objects[0].Digest != digest {
		t.Fatalf("unreferenced raw objects=%#v err=%v", objects, err)
	}
	if referenced, err := store.RawObjectIsUnreferenced(context.Background(), digest); err != nil || !referenced {
		t.Fatalf("raw object unreferenced=%t err=%v", referenced, err)
	}
	if err = store.MarkRawObjectCollected(context.Background(), digest); err != nil {
		t.Fatalf("mark raw object collected: %v", err)
	}
	objects, err = store.ListUnreferencedRawObjects(context.Background(), time.Now().Add(time.Hour), 10)
	if err != nil || len(objects) != 0 {
		t.Fatalf("collected raw objects=%#v err=%v", objects, err)
	}
}

func TestPostgresNativeOCIUploadLockSerializesConnections(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is required for PostgreSQL integration tests")
	}
	first, err := repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	release, err := first.LockOCIUpload(context.Background(), "cross-instance-upload")
	if err != nil {
		t.Fatal(err)
	}
	acquired := make(chan func(), 1)
	errs := make(chan error, 1)
	go func() {
		unlock, lockErr := second.LockOCIUpload(context.Background(), "cross-instance-upload")
		if lockErr != nil {
			errs <- lockErr
			return
		}
		acquired <- unlock
	}()
	select {
	case <-acquired:
		t.Fatal("second connection acquired upload lock before release")
	case err := <-errs:
		t.Fatal(err)
	case <-time.After(100 * time.Millisecond):
	}
	release()
	select {
	case unlock := <-acquired:
		unlock()
	case err := <-errs:
		t.Fatal(err)
	case <-time.After(time.Second):
		t.Fatal("second connection did not acquire upload lock")
	}
}

func TestPostgresNativeOCIObjectLockSerializesConnections(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is required for PostgreSQL integration tests")
	}
	first, err := repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	key := "native/oci/manifests/cross-instance-" + uuid.NewString()
	release, err := first.LockOCIObject(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	acquired := make(chan func(), 1)
	errs := make(chan error, 1)
	go func() {
		unlock, lockErr := second.LockOCIObject(context.Background(), key)
		if lockErr != nil {
			errs <- lockErr
			return
		}
		acquired <- unlock
	}()
	select {
	case <-acquired:
		t.Fatal("second connection acquired object lock before release")
	case err := <-errs:
		t.Fatal(err)
	case <-time.After(100 * time.Millisecond):
	}
	release()
	select {
	case unlock := <-acquired:
		unlock()
	case err := <-errs:
		t.Fatal(err)
	case <-time.After(time.Second):
		t.Fatal("second connection did not acquire object lock")
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

func TestPostgresMavenCollectorClaimTokenFencesOverlappingCollectors(t *testing.T) {
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
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "claim-token-" + uuid.NewString(), Format: repository.FormatMaven})
	if err != nil {
		t.Fatal(err)
	}
	declared := repository.MavenDeclaredObject{Name: "claim-token-1.0.0.pom", Digest: "sha256:claim-token", Size: 1}
	stale := repository.MavenPublishSession{ID: uuid.NewString(), RepositoryID: repo.ID, Coordinate: "org.example:claim-token:1.0.0", Publisher: "stale", PomObject: declared.Name, State: "open", ExpiresAt: time.Now().Add(-25 * time.Hour), Objects: []repository.MavenDeclaredObject{declared}}
	active := repository.MavenPublishSession{ID: uuid.NewString(), RepositoryID: repo.ID, Coordinate: "org.example:claim-token:1.0.0", Publisher: "active", PomObject: declared.Name, State: "open", ExpiresAt: time.Now().Add(time.Hour), Objects: []repository.MavenDeclaredObject{declared}}
	for _, session := range []repository.MavenPublishSession{stale, active} {
		if _, err = store.CreateMavenPublishSession(ctx, session); err != nil {
			t.Fatal(err)
		}
	}
	key := "native/maven/sha256/claim-token-" + uuid.NewString()
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
	first, err := store.ClaimExpiredMavenObjectIntents(ctx, time.Now().Add(-24*time.Hour), 1)
	if err != nil || len(first) != 1 {
		t.Fatalf("first claim=%#v err=%v", first, err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE native_maven_object_intents SET claimed_at=now()-interval '6 minutes' WHERE object_key=$1`, key); err != nil {
		t.Fatal(err)
	}
	second, err := store.ClaimExpiredMavenObjectIntents(ctx, time.Now().Add(-24*time.Hour), 1)
	if err != nil || len(second) != 1 || second[0].ClaimToken == first[0].ClaimToken {
		t.Fatalf("second claim=%#v first=%#v err=%v", second, first, err)
	}
	if err = store.ReleaseClaimedMavenObjectIntent(ctx, key, first[0].ClaimToken); err != repository.ErrNotFound {
		t.Fatalf("stale claimant released current lease: %v", err)
	}
	var currentToken string
	if err = db.QueryRowContext(ctx, `SELECT claimed_token FROM native_maven_object_intents WHERE object_key=$1`, key).Scan(&currentToken); err != nil || currentToken != second[0].ClaimToken {
		t.Fatalf("current token=%q want=%q err=%v", currentToken, second[0].ClaimToken, err)
	}
	_, err = store.CommitMavenPublishSession(ctx, active.ID, []repository.MavenAsset{{RepositoryID: repo.ID, Path: "org/example/claim-token/1.0.0/claim-token-1.0.0.pom", ObjectKey: key, Digest: declared.Digest, Size: 1}})
	if err != repository.ErrDisabled {
		t.Fatalf("promotion bypassed current deletion fence: %v", err)
	}
	if err = store.DeleteClaimedMavenObjectIntent(ctx, key, second[0].ClaimToken); err != nil {
		t.Fatal(err)
	}
	var references int
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM native_maven_object_references WHERE object_key=$1`, key).Scan(&references); err != nil || references != 0 {
		t.Fatalf("fenced overlap references=%d err=%v", references, err)
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
	if err = store.DeleteClaimedMavenObjectIntent(ctx, staleKey, claimed[0].ClaimToken); err != repository.ErrNotFound {
		t.Fatalf("reference recheck delete=%v", err)
	}
	var retained int
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM native_maven_object_intents WHERE object_key=$1`, staleKey).Scan(&retained); err != nil || retained != 1 {
		t.Fatalf("referenced intent retained=%d err=%v", retained, err)
	}
}

func TestPostgresNativeMavenPromotionFailureLeavesS3ObjectsUnpublished(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	s3Endpoint := os.Getenv("TEST_S3_ENDPOINT")
	s3AccessKey := os.Getenv("TEST_S3_ACCESS_KEY")
	s3SecretKey := os.Getenv("TEST_S3_SECRET_KEY")
	if databaseURL == "" || s3Endpoint == "" || s3AccessKey == "" || s3SecretKey == "" {
		t.Skip("PostgreSQL and S3 integration environment is required")
	}
	ctx := context.Background()
	store, err := repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	objects, err := NewS3OCIObjectStore(s3Endpoint, s3AccessKey, s3SecretKey, "native-maven-promotion-"+strings.ReplaceAll(uuid.NewString(), "-", "")[:20])
	if err != nil {
		t.Fatal(err)
	}
	if err = objects.EnsureBucket(ctx); err != nil {
		t.Fatal(err)
	}
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "promotion-retry-" + uuid.NewString(), Format: repository.FormatMaven})
	if err != nil {
		t.Fatal(err)
	}
	handler := newNativeMavenHandler(store, objects, Authenticator{ResolverToken: "resolver-secret", RepositoryWriters: map[string][]string{"maven": {repo.Name}}})
	request := func(method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.SetBasicAuth("maven", "resolver-secret")
		if method == http.MethodPost {
			r.Header.Set("Idempotency-Key", "postgres-promotion-retry")
			r.Header.Set("Content-Type", "application/json")
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	const version = "1.0.0"
	pom := "<project><groupId>org.example</groupId><artifactId>promotion-retry</artifactId><version>1.0.0</version></project>"
	jar := "postgres-backed jar"
	for name, body := range map[string]string{"promotion-retry-1.0.0.pom": pom, "promotion-retry-1.0.0.jar": jar} {
		if response := request(http.MethodPut, "/repository/maven/"+repo.Name+"/org/example/promotion-retry/"+version+"/"+name, body); response.Code != http.StatusCreated {
			t.Fatalf("stage %s = %d %s", name, response.Code, response.Body.String())
		}
	}
	jarSum := sha256.Sum256([]byte(jar))
	jarKey := "native/maven/sha256/" + fmt.Sprintf("%x", jarSum[:])
	if _, err = objects.Stat(ctx, jarKey); err != nil {
		t.Fatalf("S3 object must exist before PostgreSQL promotion: %v", err)
	}

	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	failpoint := "native_maven_promotion_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	trigger := failpoint + "_trigger"
	if _, err = db.ExecContext(ctx, fmt.Sprintf(`CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected native Maven promotion failure'; END; $$`, failpoint)); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, fmt.Sprintf(`CREATE TRIGGER %s BEFORE INSERT ON native_maven_assets FOR EACH ROW EXECUTE FUNCTION %s()`, trigger, failpoint)); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = db.ExecContext(ctx, fmt.Sprintf(`DROP TRIGGER IF EXISTS %s ON native_maven_assets`, trigger))
		_, _ = db.ExecContext(ctx, fmt.Sprintf(`DROP FUNCTION IF EXISTS %s()`, failpoint))
	}()

	commitPath := "/repository/maven/" + repo.Name + "/coordinates/org.example:promotion-retry:" + version + ":commit"
	commitBody := `{"expectedAssetNames":["promotion-retry-1.0.0.pom","promotion-retry-1.0.0.jar"]}`
	if response := request(http.MethodPost, commitPath, commitBody); response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("injected PostgreSQL promotion failure = %d %s", response.Code, response.Body.String())
	}
	for _, path := range []string{
		"org/example/promotion-retry/1.0.0/promotion-retry-1.0.0.pom",
		"org/example/promotion-retry/1.0.0/promotion-retry-1.0.0.jar",
		"org/example/promotion-retry/1.0.0/promotion-retry-1.0.0.jar.sha256",
		"org/example/promotion-retry/maven-metadata.xml",
	} {
		if response := request(http.MethodGet, "/repository/maven/"+repo.Name+"/"+path, ""); response.Code != http.StatusNotFound {
			t.Fatalf("failed promotion read %s = %d, want 404", path, response.Code)
		}
	}
	var assets, references int
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM native_maven_assets WHERE repository_id=$1`, repo.ID).Scan(&assets); err != nil || assets != 0 {
		t.Fatalf("rolled back PostgreSQL assets=%d err=%v", assets, err)
	}
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM native_maven_object_references WHERE repository_id=$1`, repo.ID).Scan(&references); err != nil || references != 0 {
		t.Fatalf("rolled back PostgreSQL references=%d err=%v", references, err)
	}
	if _, err = db.ExecContext(ctx, fmt.Sprintf(`DROP TRIGGER %s ON native_maven_assets`, trigger)); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, fmt.Sprintf(`DROP FUNCTION %s()`, failpoint)); err != nil {
		t.Fatal(err)
	}
	if response := request(http.MethodPost, commitPath, commitBody); response.Code != http.StatusOK {
		t.Fatalf("commit retry after PostgreSQL recovery = %d %s", response.Code, response.Body.String())
	}
	for _, path := range []string{
		"org/example/promotion-retry/1.0.0/promotion-retry-1.0.0.pom",
		"org/example/promotion-retry/1.0.0/promotion-retry-1.0.0.jar",
		"org/example/promotion-retry/1.0.0/promotion-retry-1.0.0.jar.sha256",
		"org/example/promotion-retry/maven-metadata.xml",
	} {
		if response := request(http.MethodGet, "/repository/maven/"+repo.Name+"/"+path, ""); response.Code != http.StatusOK {
			t.Fatalf("retried promotion read %s = %d, want 200", path, response.Code)
		}
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
	if _, err := store.CreateRawGroup(context.Background(), repository.Group{Name: "raw-audit", CacheQuotaBytes: 1 << 30, Members: []repository.Member{{Name: "hosted", Type: repository.MemberHosted, Endpoint: "https://legacy.example:8443"}}}); err != nil {
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
	if format != "raw" || resource != "release/app.txt" || representation != "body" || memberType != "hosted" || upstreamHost != "legacy.example" || operation != "get" || status != http.StatusOK || disposition != "miss" || bytes != 8 || requestID != "postgres-raw-request" || len(traceID) != 32 {
		t.Fatalf("Raw audit fields = format=%q resource=%q representation=%q member_type=%q upstream_host=%q operation=%q status=%d disposition=%q bytes=%d request_id=%q trace_id=%q", format, resource, representation, memberType, upstreamHost, operation, status, disposition, bytes, requestID, traceID)
	}
	audits, err := store.ListAudits(context.Background(), repository.AuditQuery{GroupName: "raw-audit"})
	if err != nil || len(audits) != 1 {
		t.Fatalf("ListAudits err=%v audits=%#v", err, audits)
	}
	if audit := audits[0]; audit.Format != "raw" || audit.Resource != "release/app.txt" || audit.Representation != "body" || audit.MemberType != "hosted" || audit.UpstreamHost != "legacy.example" || audit.Operation != "get" || audit.Status != http.StatusOK || audit.CacheDisposition != "miss" || audit.Bytes != 8 || audit.RequestID != "postgres-raw-request" || len(audit.TraceID) != 32 {
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

func TestPostgresMavenRetentionTombstonesExpiredExcessVersions(t *testing.T) {
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
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "retention-pg-" + uuid.NewString(), Format: repository.FormatMaven})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.ReplaceRepositoryRetentionPolicy(ctx, repo.ID, repository.RepositoryRetentionPolicy{KeepDays: 1, MinimumVersions: 1}, "1"); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	ids := []string{uuid.NewString(), uuid.NewString(), uuid.NewString()}
	for index, id := range ids {
		createdAt := time.Now().UTC().Add(-time.Duration(72-index*24) * time.Hour)
		coordinate := fmt.Sprintf("org.example:retained:%d.0.0", index+1)
		if _, err = db.ExecContext(ctx, `INSERT INTO native_maven_artifacts (id,repository_id,coordinate,digest,state,created_at) VALUES ($1,$2,$3,$4,'visible',$5)`, id, repo.ID, coordinate, "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", createdAt); err != nil {
			t.Fatal(err)
		}
	}
	if err = (NativeMavenRetention{Store: store, Now: func() time.Time { return time.Now().UTC() }}).Collect(ctx); err != nil {
		t.Fatal(err)
	}
	visible, err := store.ListMavenArtifacts(ctx, repo.ID)
	if err != nil || len(visible) != 1 || visible[0].ID != ids[2] {
		t.Fatalf("visible artifacts=%#v err=%v", visible, err)
	}
	for _, id := range ids[:2] {
		artifact, getErr := store.GetMavenArtifact(ctx, repo.ID, id)
		if getErr != nil || artifact.State != "deleted" {
			t.Fatalf("artifact=%#v err=%v", artifact, getErr)
		}
	}
}

func integrationRequest(handler http.Handler, method, target, body, token string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	authorize(request, token)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
