package app

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	adminopenapi "github.com/artifact-gateway/artifact-gateway/internal/admin/openapi"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestRepositoryRetentionPolicyManagementUsesVersioning(t *testing.T) {
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "retention-target", Format: repository.FormatMaven})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())
	get := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/"+repo.ID+"/retention-policy", nil)
	authorize(get, "admin-secret")
	defaultPolicy := httptest.NewRecorder()
	handler.ServeHTTP(defaultPolicy, get)
	if defaultPolicy.Code != http.StatusOK || !strings.Contains(defaultPolicy.Body.String(), `"version":"1"`) || !strings.Contains(defaultPolicy.Body.String(), `"enabled":false`) || !strings.Contains(defaultPolicy.Body.String(), `"snapshotKeepDays":30`) {
		t.Fatalf("default policy=%d body=%s", defaultPolicy.Code, defaultPolicy.Body.String())
	}
	replace := httptest.NewRequest(http.MethodPut, "/api/v2/repositories/"+repo.ID+"/retention-policy", strings.NewReader(`{"version":"1","keepDays":14,"minimumVersions":3}`))
	authorize(replace, "admin-secret")
	replace.Header.Set("If-Match", "1")
	replaced := httptest.NewRecorder()
	handler.ServeHTTP(replaced, replace)
	if replaced.Code != http.StatusOK || !strings.Contains(replaced.Body.String(), `"version":"2"`) || !strings.Contains(replaced.Body.String(), `"minimumVersions":3`) {
		t.Fatalf("replace=%d body=%s", replaced.Code, replaced.Body.String())
	}
	stale := httptest.NewRequest(http.MethodPut, "/api/v2/repositories/"+repo.ID+"/retention-policy", strings.NewReader(`{"version":"1","keepDays":7,"minimumVersions":1}`))
	authorize(stale, "admin-secret")
	stale.Header.Set("If-Match", "1")
	staleResult := httptest.NewRecorder()
	handler.ServeHTTP(staleResult, stale)
	if staleResult.Code != http.StatusPreconditionFailed {
		t.Fatalf("stale=%d body=%s", staleResult.Code, staleResult.Body.String())
	}
	invalid := httptest.NewRequest(http.MethodPut, "/api/v2/repositories/"+repo.ID+"/retention-policy", strings.NewReader(`{"version":"2","keepDays":0,"minimumVersions":1}`))
	authorize(invalid, "admin-secret")
	invalid.Header.Set("If-Match", "2")
	invalidResult := httptest.NewRecorder()
	handler.ServeHTTP(invalidResult, invalid)
	if invalidResult.Code != http.StatusBadRequest {
		t.Fatalf("invalid=%d body=%s", invalidResult.Code, invalidResult.Body.String())
	}
	invalidMaximum := httptest.NewRequest(http.MethodPut, "/api/v2/repositories/"+repo.ID+"/retention-policy", strings.NewReader(`{"version":"2","keepDays":14,"minimumVersions":3,"maximumVersions":2}`))
	authorize(invalidMaximum, "admin-secret")
	invalidMaximum.Header.Set("If-Match", "2")
	invalidMaximumResult := httptest.NewRecorder()
	handler.ServeHTTP(invalidMaximumResult, invalidMaximum)
	if invalidMaximumResult.Code != http.StatusBadRequest || !strings.Contains(invalidMaximumResult.Body.String(), "maximumVersions") {
		t.Fatalf("invalid maximum=%d body=%s", invalidMaximumResult.Code, invalidMaximumResult.Body.String())
	}
	invalidPattern := httptest.NewRequest(http.MethodPut, "/api/v2/repositories/"+repo.ID+"/retention-policy", strings.NewReader(`{"version":"2","keepDays":14,"minimumVersions":1,"coordinatePatterns":["["]}`))
	authorize(invalidPattern, "admin-secret")
	invalidPattern.Header.Set("If-Match", "2")
	invalidPatternResult := httptest.NewRecorder()
	handler.ServeHTTP(invalidPatternResult, invalidPattern)
	if invalidPatternResult.Code != http.StatusBadRequest || !strings.Contains(invalidPatternResult.Body.String(), "coordinatePatterns") {
		t.Fatalf("invalid pattern=%d body=%s", invalidPatternResult.Code, invalidPatternResult.Body.String())
	}
}

func TestRepositoryCapacityManagementUsesScopedGrantsAndAuditsConfiguration(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "capacity-target", Format: repository.FormatRaw})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.PutRawAsset(ctx, repository.RawAsset{RepositoryID: repo.ID, Path: "widget", Digest: "sha256:widget", ObjectKey: "native/raw/widget", Size: 4}); err != nil {
		t.Fatal(err)
	}
	if _, err = store.ReplaceRepositoryGrants(ctx, repo.ID, []repository.RepositoryGrant{
		{Principal: "reader", Scopes: []string{"repositories:read"}},
		{Principal: "manager", Scopes: []string{"repositories:admin"}},
	}, "1"); err != nil {
		t.Fatal(err)
	}
	authenticator := testAuthenticator()
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, authenticator)
	request := func(method, actor, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "/api/v2/repositories/"+repo.ID+"/capacity", strings.NewReader(body))
		authorize(r, authenticator.IssueToken(actor))
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	if response := request(http.MethodGet, "reader", ""); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"usedBytes":4`) || !strings.Contains(response.Body.String(), `"quotaBytes":0`) {
		t.Fatalf("reader capacity=%d body=%s", response.Code, response.Body.String())
	}
	if response := request(http.MethodPut, "reader", `{"quotaBytes":10}`); response.Code != http.StatusForbidden {
		t.Fatalf("reader configure=%d body=%s", response.Code, response.Body.String())
	}
	if response := request(http.MethodPut, "manager", `{"quotaBytes":-1}`); response.Code != http.StatusBadRequest {
		t.Fatalf("invalid configure=%d body=%s", response.Code, response.Body.String())
	}
	if response := request(http.MethodPut, "manager", `{"quotaBytes":10}`); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"quotaBytes":10`) {
		t.Fatalf("configure=%d body=%s", response.Code, response.Body.String())
	}
	if len(store.Audits) == 0 {
		t.Fatal("expected capacity configuration audit")
	}
	audit := store.Audits[len(store.Audits)-1]
	if audit.Outcome != repository.AuditResolved || audit.Actor != "manager" || audit.Operation != "capacity.configure" || audit.Resource != "repositories/"+repo.ID+"/capacity" {
		t.Fatalf("audit=%#v", audit)
	}
}

func TestRepositoryRetentionDryRunIsAdminOnlyAcrossFormatsAndDoesNotMutateArtifacts(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	maven, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "retention-dry-run", Format: repository.FormatMaven})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "retention-dry-run-raw", Format: repository.FormatRaw})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.ReplaceRepositoryGrants(ctx, maven.ID, []repository.RepositoryGrant{{Principal: "retention-reader", Scopes: []string{"repositories:read"}}}, "1"); err != nil {
		t.Fatal(err)
	}
	session := repository.MavenPublishSession{ID: uuid.NewString(), RepositoryID: maven.ID, Coordinate: "org.example:dry-run:1.0.0", Publisher: "test", State: "open", ExpiresAt: time.Now().Add(time.Hour), Objects: []repository.MavenDeclaredObject{{Name: "dry-run-1.0.0.jar", Digest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Size: 1}}}
	if _, err = store.CreateMavenPublishSession(ctx, session); err != nil {
		t.Fatal(err)
	}
	if err = store.MarkMavenPublishObject(ctx, session.ID, session.Objects[0].Name, "native/maven/dry-run/"+session.ID); err != nil {
		t.Fatal(err)
	}
	artifact, err := store.CommitMavenPublishSession(ctx, session.ID, []repository.MavenAsset{{RepositoryID: maven.ID, Path: "org/example/dry-run/1.0.0/dry-run-1.0.0.jar", ObjectKey: "native/maven/dry-run/" + session.ID, Digest: session.Objects[0].Digest, Size: 1}})
	if err != nil {
		t.Fatal(err)
	}
	authenticator := testAuthenticator()
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, authenticator)
	request := func(repositoryID, token string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/api/v2/repositories/"+repositoryID+"/retention:dry-run", nil)
		authorize(req, token)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		return response
	}
	if response := request(maven.ID, "admin-secret"); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"policyVersion":"1"`) || !strings.Contains(response.Body.String(), `"candidates":[]`) {
		t.Fatalf("dry run=%d body=%s", response.Code, response.Body.String())
	}
	visible, err := store.GetMavenArtifact(ctx, maven.ID, artifact.ID)
	if err != nil || visible.State != "visible" {
		t.Fatalf("dry run mutated artifact=%#v err=%v", visible, err)
	}
	if response := request(raw.ID, "admin-secret"); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"candidates":[]`) {
		t.Fatalf("raw dry run=%d body=%s", response.Code, response.Body.String())
	}
	if response := request(maven.ID, authenticator.IssueToken("retention-reader")); response.Code != http.StatusForbidden {
		t.Fatalf("reader dry run=%d body=%s", response.Code, response.Body.String())
	}
}

func TestRepositoryRetentionDryRunPaginatesAndBindsPolicyVersion(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "retention-page", Format: repository.FormatMaven})
	if err != nil {
		t.Fatal(err)
	}
	otherRepo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "retention-page-other", Format: repository.FormatMaven})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.ReplaceRepositoryRetentionPolicy(ctx, repo.ID, repository.RepositoryRetentionPolicy{Enabled: true, KeepDays: 36500, SnapshotKeepDays: 36500, MinimumVersions: 1, MaximumVersions: 1}, "1"); err != nil {
		t.Fatal(err)
	}
	for index, version := range []string{"1.0.0", "1.1.0", "1.2.0"} {
		sessionID := uuid.NewString()
		coordinate := "org.example:pageable:" + version
		digest := "sha256:" + fmt.Sprintf("%064x", index+1)
		name := "pageable-" + version + ".jar"
		objectKey := "native/maven/pageable/" + sessionID
		session := repository.MavenPublishSession{ID: sessionID, RepositoryID: repo.ID, Coordinate: coordinate, Publisher: "test", State: "open", ExpiresAt: time.Now().Add(time.Hour), Objects: []repository.MavenDeclaredObject{{Name: name, Digest: digest, Size: 1}}}
		if _, err = store.CreateMavenPublishSession(ctx, session); err != nil {
			t.Fatal(err)
		}
		if err = store.MarkMavenPublishObject(ctx, sessionID, name, objectKey); err != nil {
			t.Fatal(err)
		}
		if _, err = store.CommitMavenPublishSession(ctx, sessionID, []repository.MavenAsset{{RepositoryID: repo.ID, Path: "org/example/pageable/" + version + "/" + name, ObjectKey: objectKey, Digest: digest, Size: 1}}); err != nil {
			t.Fatal(err)
		}
	}
	authenticator := testAuthenticator()
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, authenticator)
	request := func(target string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, target, nil)
		authorize(req, "admin-secret")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		return response
	}
	first := request("/api/v2/repositories/" + repo.ID + "/retention:dry-run?pageSize=1")
	if first.Code != http.StatusOK {
		t.Fatalf("first page=%d body=%s", first.Code, first.Body.String())
	}
	if !strings.Contains(first.Body.String(), `"summary":{"oldestCandidateAt":`) || !strings.Contains(first.Body.String(), `"maximumVersions":2`) || !strings.Contains(first.Body.String(), `"release":2`) {
		t.Fatalf("first page summary=%s", first.Body.String())
	}
	var firstPage adminopenapi.RetentionDryRun
	if err = json.NewDecoder(first.Body).Decode(&firstPage); err != nil {
		t.Fatal(err)
	}
	if firstPage.TotalCandidates != 2 || len(firstPage.Candidates) != 1 || firstPage.NextPageToken == nil {
		t.Fatalf("first page=%#v", firstPage)
	}
	second := request("/api/v2/repositories/" + repo.ID + "/retention:dry-run?pageSize=1&pageToken=" + url.QueryEscape(*firstPage.NextPageToken))
	if second.Code != http.StatusOK {
		t.Fatalf("second page=%d body=%s", second.Code, second.Body.String())
	}
	var secondPage adminopenapi.RetentionDryRun
	if err = json.NewDecoder(second.Body).Decode(&secondPage); err != nil {
		t.Fatal(err)
	}
	if len(secondPage.Candidates) != 1 || secondPage.Candidates[0].Coordinate == firstPage.Candidates[0].Coordinate || secondPage.NextPageToken != nil {
		t.Fatalf("second page=%#v", secondPage)
	}
	exported := request("/api/v2/repositories/" + repo.ID + "/retention:dry-run?output=csv&pageSize=1")
	if exported.Code != http.StatusOK || exported.Header().Get("Content-Type") != "text/csv; charset=utf-8" {
		t.Fatalf("export=%d content-type=%q body=%s", exported.Code, exported.Header().Get("Content-Type"), exported.Body.String())
	}
	lines := strings.Split(strings.TrimSpace(exported.Body.String()), "\n")
	if len(lines) != 3 || lines[0] != "format,coordinate,digest,createdAt,ageDays,versionType,reasons" || !strings.Contains(lines[1]+lines[2], "maximum_versions") {
		t.Fatalf("export lines=%#v", lines)
	}
	foreign := request("/api/v2/repositories/" + otherRepo.ID + "/retention:dry-run?pageSize=1&pageToken=" + url.QueryEscape(*firstPage.NextPageToken))
	if foreign.Code != http.StatusBadRequest || !strings.Contains(foreign.Body.String(), "invalid_page_token") {
		t.Fatalf("foreign repository page=%d body=%s", foreign.Code, foreign.Body.String())
	}
	expiredPayload, err := json.Marshal(retentionDryRunPageCursor{Endpoint: "retention-dry-run", RepositoryID: repo.ID, PolicyVersion: firstPage.PolicyVersion, Coordinate: firstPage.Candidates[0].Coordinate, ArtifactID: "expired-artifact", ExpiresAt: time.Now().UTC().Add(-time.Minute).Unix()})
	if err != nil {
		t.Fatal(err)
	}
	expiredMAC := hmac.New(sha256.New, []byte(authenticator.AdminToken))
	_, _ = expiredMAC.Write(expiredPayload)
	expiredToken := base64.RawURLEncoding.EncodeToString(append(expiredPayload, expiredMAC.Sum(nil)...))
	expired := request("/api/v2/repositories/" + repo.ID + "/retention:dry-run?pageSize=1&pageToken=" + url.QueryEscape(expiredToken))
	if expired.Code != http.StatusBadRequest || !strings.Contains(expired.Body.String(), "invalid_page_token") {
		t.Fatalf("expired page=%d body=%s", expired.Code, expired.Body.String())
	}
	updated, err := store.ReplaceRepositoryRetentionPolicy(ctx, repo.ID, repository.RepositoryRetentionPolicy{Enabled: true, KeepDays: 36500, SnapshotKeepDays: 36500, MinimumVersions: 1, MaximumVersions: 1}, secondPage.PolicyVersion)
	if err != nil {
		t.Fatal(err)
	}
	stale := request("/api/v2/repositories/" + repo.ID + "/retention:dry-run?pageSize=1&pageToken=" + url.QueryEscape(*firstPage.NextPageToken))
	if stale.Code != http.StatusBadRequest || !strings.Contains(stale.Body.String(), "invalid_page_token") {
		t.Fatalf("stale page=%d body=%s policy=%#v", stale.Code, stale.Body.String(), updated)
	}
}

func TestRepositoryRetentionExecutionEnqueuesIdempotentCrossFormatJobs(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	maven, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "retention-execute", Format: repository.FormatMaven})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "retention-execute-raw", Format: repository.FormatRaw})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.ReplaceRepositoryRetentionPolicy(ctx, maven.ID, repository.RepositoryRetentionPolicy{Enabled: true, KeepDays: 30, MinimumVersions: 1}, "1"); err != nil {
		t.Fatal(err)
	}
	if _, err = store.ReplaceRepositoryRetentionPolicy(ctx, raw.ID, repository.RepositoryRetentionPolicy{Enabled: true, KeepDays: 30, MinimumVersions: 1}, "1"); err != nil {
		t.Fatal(err)
	}
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())
	request := func(repo string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/v2/repositories/"+repo+"/retention:execute", nil)
		authorize(req, "admin-secret")
		req.Header.Set("Idempotency-Key", "retention-run")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		return response
	}
	first := request(maven.ID)
	if first.Code != http.StatusAccepted || !strings.Contains(first.Body.String(), `"kind":"retention"`) || !strings.Contains(first.Body.String(), `"state":"pending"`) {
		t.Fatalf("execute=%d body=%s", first.Code, first.Body.String())
	}
	second := request(maven.ID)
	if second.Code != http.StatusAccepted || second.Body.String() != first.Body.String() {
		t.Fatalf("replay=%d body=%s", second.Code, second.Body.String())
	}
	rawResponse := request(raw.ID)
	if rawResponse.Code != http.StatusAccepted || !strings.Contains(rawResponse.Body.String(), `"kind":"retention"`) {
		t.Fatalf("raw execute=%d body=%s", rawResponse.Code, rawResponse.Body.String())
	}
	if err = (NativeRepositoryRetention{Store: store}).RunJobs(ctx, 10); err != nil {
		t.Fatal(err)
	}
	jobs, err := store.ListLifecycleJobs(ctx, maven.ID, 10)
	if err != nil || len(jobs) != 1 || jobs[0].State != repository.LifecycleJobCompleted {
		t.Fatalf("jobs=%#v err=%v", jobs, err)
	}
	rawJobs, err := store.ListLifecycleJobs(ctx, raw.ID, 10)
	if err != nil || len(rawJobs) != 1 || rawJobs[0].State != repository.LifecycleJobCompleted {
		t.Fatalf("raw jobs=%#v err=%v", rawJobs, err)
	}
}

func TestRepositoryRetentionExecutionChecksDryRunPolicyVersion(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "retention-if-match", Format: repository.FormatMaven})
	if err != nil {
		t.Fatal(err)
	}
	policy, err := store.ReplaceRepositoryRetentionPolicy(ctx, repo.ID, repository.RepositoryRetentionPolicy{Enabled: true, KeepDays: 30, MinimumVersions: 1}, "1")
	if err != nil {
		t.Fatal(err)
	}
	authenticator := testAuthenticator()
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, authenticator)
	request := func(key, version string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/v2/repositories/"+repo.ID+"/retention:execute", nil)
		authorize(req, "admin-secret")
		req.Header.Set("Idempotency-Key", key)
		req.Header.Set("If-Match", version)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		return response
	}
	if stale := request("retention-stale", "1"); stale.Code != http.StatusPreconditionFailed || !strings.Contains(stale.Body.String(), "version_conflict") {
		t.Fatalf("stale execute=%d body=%s", stale.Code, stale.Body.String())
	}
	if current := request("retention-current", policy.Version); current.Code != http.StatusAccepted {
		t.Fatalf("current execute=%d body=%s", current.Code, current.Body.String())
	}
	if _, err = store.ReplaceRepositoryRetentionPolicy(ctx, repo.ID, repository.RepositoryRetentionPolicy{Enabled: false, KeepDays: 30, MinimumVersions: 1}, policy.Version); err != nil {
		t.Fatal(err)
	}
	if disabled := request("retention-disabled", policy.Version); disabled.Code != http.StatusPreconditionFailed {
		// The version was incremented by the disable update, so the stale If-Match
		// is expected to fail before the disabled-policy guard.
		t.Fatalf("disabled execute=%d body=%s", disabled.Code, disabled.Body.String())
	}
	currentPolicy, err := store.GetRepositoryRetentionPolicy(ctx, repo.ID)
	if err != nil {
		t.Fatal(err)
	}
	disabled := request("retention-disabled-current", currentPolicy.Version)
	if disabled.Code != http.StatusConflict || !strings.Contains(disabled.Body.String(), "retention_disabled") {
		t.Fatalf("disabled current execute=%d body=%s", disabled.Code, disabled.Body.String())
	}
}

func TestRepositoryNPMRetentionAndRestoreLifecycle(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "npm-retention", Format: repository.FormatNPM})
	if err != nil {
		t.Fatal(err)
	}
	publishNPMGroupTestVersion(t, store, repo, "widget", "1.0.0")
	publishNPMGroupTestVersion(t, store, repo, "widget", "2.0.0")
	policy, err := store.ReplaceRepositoryRetentionPolicy(ctx, repo.ID, repository.RepositoryRetentionPolicy{
		Enabled: true, KeepDays: 36500, MinimumVersions: 1, MaximumVersions: 1,
	}, "1")
	if err != nil {
		t.Fatal(err)
	}
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())
	request := func(method, target, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, target, strings.NewReader(body))
		authorize(req, "admin-secret")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		return response
	}
	dryRun := request(http.MethodPost, "/api/v2/repositories/"+repo.ID+"/retention:dry-run", "")
	if dryRun.Code != http.StatusOK || !strings.Contains(dryRun.Body.String(), `"coordinate":"widget@1.0.0"`) {
		t.Fatalf("npm retention dry-run=%d body=%s", dryRun.Code, dryRun.Body.String())
	}
	executeRequest := httptest.NewRequest(http.MethodPost, "/api/v2/repositories/"+repo.ID+"/retention:execute", nil)
	authorize(executeRequest, "admin-secret")
	executeRequest.Header.Set("Idempotency-Key", "npm-retention-run")
	executeRequest.Header.Set("If-Match", policy.Version)
	execute := httptest.NewRecorder()
	handler.ServeHTTP(execute, executeRequest)
	if execute.Code != http.StatusAccepted {
		t.Fatalf("npm retention execute=%d body=%s", execute.Code, execute.Body.String())
	}
	if err = (NativeRepositoryRetention{Store: store}).RunJobs(ctx, 10); err != nil {
		t.Fatal(err)
	}
	pkg, err := store.GetNPMPackage(ctx, repo.ID, "widget")
	if err != nil || len(pkg.Versions) != 1 || pkg.Versions[0].Version != "2.0.0" {
		t.Fatalf("retained npm package=%#v err=%v", pkg, err)
	}
	if _, err = store.GetArtifactTombstone(ctx, repo.ID, repository.FormatNPM, "widget@1.0.0"); err != nil {
		t.Fatalf("npm tombstone: %v", err)
	}
	restore := request(http.MethodPost, "/api/v2/repositories/"+repo.ID+"/restore", `{"coordinate":"widget@1.0.0"}`)
	if restore.Code != http.StatusNoContent {
		t.Fatalf("npm restore=%d body=%s", restore.Code, restore.Body.String())
	}
	pkg, err = store.GetNPMPackage(ctx, repo.ID, "widget")
	if err != nil || len(pkg.Versions) != 2 {
		t.Fatalf("restored npm package=%#v err=%v", pkg, err)
	}
}

func TestRepositoryNPMArtifactCanBeTombstonedThroughManagementAPI(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "npm-delete", Format: repository.FormatNPM})
	if err != nil {
		t.Fatal(err)
	}
	publishNPMGroupTestVersion(t, store, repo, "widget", "1.0.0")
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())
	request := httptest.NewRequest(http.MethodPost, "/api/v2/repositories/"+repo.ID+"/tombstones", strings.NewReader(`{"coordinate":"widget@1.0.0"}`))
	authorize(request, "admin-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("npm tombstone=%d body=%s", response.Code, response.Body.String())
	}
	if _, err = store.GetNPMPackage(ctx, repo.ID, "widget"); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("deleted npm package remained visible: %v", err)
	}
}

func TestRepositoryRestoreRestoresConanTombstoneAndRejectsCollectedObjects(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	conan, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "restore-conan", Format: repository.FormatConan})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "restore-raw", Format: repository.FormatRaw})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.ReplaceRepositoryGrants(ctx, conan.ID, []repository.RepositoryGrant{{Principal: "restore-reader", Scopes: []string{"repositories:read"}}}, "1"); err != nil {
		t.Fatal(err)
	}
	publish := func(reference, revision, key string) repository.ConanRecipeRevision {
		t.Helper()
		digest := "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		if err := store.StageConanObject(ctx, repository.ConanObjectIntent{RepositoryID: conan.ID, ObjectKey: key, Digest: digest, Size: 1}); err != nil {
			t.Fatal(err)
		}
		item, err := store.PutConanRecipeRevision(ctx, repository.ConanRecipeRevision{RepositoryID: conan.ID, Reference: reference, Revision: revision, Digest: digest}, []repository.ConanAsset{{RepositoryID: conan.ID, Reference: reference, RecipeRevision: revision, Path: "conanfile.py", ObjectKey: key, Digest: digest, Size: 1}})
		if err != nil {
			t.Fatal(err)
		}
		return item
	}
	first := publish("pkg/1.0/user/stable", "rrev", "native/conan/restore/first")
	if _, err = store.TombstoneConanRecipeRevision(ctx, conan.ID, first.Reference, first.Revision); err != nil {
		t.Fatal(err)
	}
	authenticator := testAuthenticator()
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, authenticator)
	request := func(repositoryID, token, coordinate string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/api/v2/repositories/"+repositoryID+"/restore", strings.NewReader(`{"coordinate":"`+coordinate+`"}`))
		authorize(req, token)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		return response
	}
	coordinate := first.Reference + "#" + first.Revision
	if response := request(conan.ID, "admin-secret", coordinate); response.Code != http.StatusNoContent {
		t.Fatalf("restore=%d body=%s", response.Code, response.Body.String())
	}
	restored, err := store.GetConanRecipeRevision(ctx, conan.ID, first.Reference, first.Revision)
	if err != nil || restored.State != "visible" {
		t.Fatalf("restored=%#v err=%v", restored, err)
	}
	if _, err = store.GetArtifactTombstone(ctx, conan.ID, repository.FormatConan, coordinate); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("restore kept tombstone: %v", err)
	}
	second := publish("pkg/2.0/user/stable", "rrev", "native/conan/restore/second")
	if _, err = store.TombstoneConanRecipeRevision(ctx, conan.ID, second.Reference, second.Revision); err != nil {
		t.Fatal(err)
	}
	if err = store.MarkConanObjectCollected(ctx, "native/conan/restore/second"); err != nil {
		t.Fatal(err)
	}
	if response := request(conan.ID, "admin-secret", second.Reference+"#"+second.Revision); response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "restore_unavailable") {
		t.Fatalf("restore collected=%d body=%s", response.Code, response.Body.String())
	}
	if response := request(raw.ID, "admin-secret", coordinate); response.Code != http.StatusNotFound || !strings.Contains(response.Body.String(), "tombstone not found") {
		t.Fatalf("restore missing raw tombstone=%d body=%s", response.Code, response.Body.String())
	}
	if response := request(conan.ID, authenticator.IssueToken("restore-reader"), coordinate); response.Code != http.StatusForbidden {
		t.Fatalf("restore reader=%d body=%s", response.Code, response.Body.String())
	}
	if response := request(conan.ID, "admin-secret", "not-a-coordinate"); response.Code != http.StatusBadRequest {
		t.Fatalf("invalid restore=%d body=%s", response.Code, response.Body.String())
	}
}

func TestRepositoryRestoreRestoresMavenTombstone(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "restore-maven", Format: repository.FormatMaven})
	if err != nil {
		t.Fatal(err)
	}
	coordinate := "org.example:widget:1.0.0"
	session := repository.MavenPublishSession{ID: uuid.NewString(), RepositoryID: repo.ID, Coordinate: coordinate, Publisher: "admin", PomObject: "widget-1.0.0.jar", State: "open", ExpiresAt: time.Now().Add(time.Hour), Objects: []repository.MavenDeclaredObject{{Name: "widget-1.0.0.jar", Digest: "sha256:restore", Size: 1}}}
	if _, err = store.CreateMavenPublishSession(ctx, session); err != nil {
		t.Fatal(err)
	}
	key := "native/maven/restore/" + session.ID
	if err = store.MarkMavenPublishObject(ctx, session.ID, session.PomObject, key); err != nil {
		t.Fatal(err)
	}
	artifact, err := store.CommitMavenPublishSession(ctx, session.ID, []repository.MavenAsset{{RepositoryID: repo.ID, Path: "org/example/widget/1.0.0/widget-1.0.0.jar", ObjectKey: key, Digest: session.Objects[0].Digest, Size: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.TombstoneMavenArtifact(ctx, repo.ID, artifact.ID); err != nil {
		t.Fatal(err)
	}
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())
	request := httptest.NewRequest(http.MethodPost, "/api/v2/repositories/"+repo.ID+"/restore", strings.NewReader(`{"coordinate":"`+coordinate+`"}`))
	authorize(request, "admin-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("restore=%d body=%s", response.Code, response.Body.String())
	}
	if _, err = store.GetMavenAsset(ctx, repo.ID, "org/example/widget/1.0.0/widget-1.0.0.jar"); err != nil {
		t.Fatalf("restored Maven asset unavailable: %v", err)
	}
}

func TestRepositoryRestoreRestoresOCIManifestAndTags(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "restore-oci", Format: repository.FormatOCI})
	if err != nil {
		t.Fatal(err)
	}
	digest := "sha256:" + strings.Repeat("a", 64)
	objectKey := "native/oci/manifests/restore"
	if err = store.StageOCIObjectIntent(ctx, repository.OCIObjectIntent{RepositoryID: repo.ID, ObjectKey: objectKey, Digest: digest, Size: 42}); err != nil {
		t.Fatal(err)
	}
	manifest, err := store.PutOCIManifest(ctx, repository.OCIManifest{RepositoryID: repo.ID, Name: "team/widget", Digest: digest, ObjectKey: objectKey, MediaType: "application/vnd.oci.image.manifest.v1+json", Size: 42}, "1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if err = store.DeleteOCIManifest(ctx, repo.ID, manifest.Name, manifest.Digest); err != nil {
		t.Fatal(err)
	}
	coordinate := manifest.Name + "@" + manifest.Digest
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())
	request := httptest.NewRequest(http.MethodPost, "/api/v2/repositories/"+repo.ID+"/restore", strings.NewReader(`{"coordinate":"`+coordinate+`"}`))
	authorize(request, "admin-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("restore=%d body=%s", response.Code, response.Body.String())
	}
	if restored, getErr := store.GetOCIManifest(ctx, repo.ID, manifest.Name, manifest.Digest); getErr != nil || restored.ObjectKey != objectKey {
		t.Fatalf("restored manifest=%#v err=%v", restored, getErr)
	}
	if restored, getErr := store.GetOCIManifest(ctx, repo.ID, manifest.Name, "1.0.0"); getErr != nil || restored.Digest != digest {
		t.Fatalf("restored tag=%#v err=%v", restored, getErr)
	}
	if _, getErr := store.GetArtifactTombstone(ctx, repo.ID, repository.FormatOCI, coordinate); !errors.Is(getErr, repository.ErrNotFound) {
		t.Fatalf("restore kept tombstone: %v", getErr)
	}
}

func TestRepositoryLifecycleJobStatusManagement(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "job-status-target", Format: repository.FormatConan})
	if err != nil {
		t.Fatal(err)
	}
	failedID := uuid.NewString()
	if _, _, err = store.EnqueueLifecycleJob(ctx, repository.LifecycleJob{ID: failedID, RepositoryID: repo.ID, Kind: repository.LifecycleJobReclaim, IdempotencyKey: "reclaim-object", Payload: []byte(`{"format":"conan","objectKey":"secret-object-key"}`)}); err != nil {
		t.Fatal(err)
	}
	claimed, err := store.ClaimLifecycleJobs(ctx, 1)
	if err != nil || len(claimed) != 1 || claimed[0].ID != failedID {
		t.Fatalf("claimed=%#v err=%v", claimed, err)
	}
	if err = store.FailLifecycleJob(ctx, failedID, claimed[0].LeaseToken, "object store unavailable"); err != nil {
		t.Fatal(err)
	}
	if _, _, err = store.EnqueueLifecycleJob(ctx, repository.LifecycleJob{ID: uuid.NewString(), RepositoryID: repo.ID, Kind: repository.LifecycleJobRetention, IdempotencyKey: "retention-run", Payload: []byte(`{"format":"conan"}`)}); err != nil {
		t.Fatal(err)
	}
	sourceRepositoryID := uuid.NewString()
	intelligencePayload, err := json.Marshal(repository.ArtifactIntelligenceCopyPayload{
		Format:             repository.FormatConan,
		SourceRepositoryID: sourceRepositoryID,
		Coordinate:         "pkg/1.0#rrev",
		Digest:             "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = store.EnqueueLifecycleJob(ctx, repository.LifecycleJob{ID: uuid.NewString(), RepositoryID: repo.ID, Kind: repository.LifecycleJobIntelligence, IdempotencyKey: "intelligence-copy", Payload: intelligencePayload}); err != nil {
		t.Fatal(err)
	}
	if _, err = store.ReplaceRepositoryGrants(ctx, repo.ID, []repository.RepositoryGrant{{Principal: "job-reader", Scopes: []string{"repositories:read"}}}, "1"); err != nil {
		t.Fatal(err)
	}
	authenticator := testAuthenticator()
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, authenticator)
	request := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/"+repo.ID+"/lifecycle-jobs", nil)
	authorize(request, "admin-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"state":"retrying"`) || !strings.Contains(response.Body.String(), `"state":"pending"`) || !strings.Contains(response.Body.String(), `"lastError":"object store unavailable"`) || !strings.Contains(response.Body.String(), `"coordinate":"pkg/1.0#rrev"`) || !strings.Contains(response.Body.String(), `"sourceRepositoryId":"`+sourceRepositoryID+`"`) || strings.Contains(response.Body.String(), "secret-object-key") || strings.Contains(response.Body.String(), "idempotencyKey") {
		t.Fatalf("jobs=%d body=%s", response.Code, response.Body.String())
	}
	denied := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/"+repo.ID+"/lifecycle-jobs", nil)
	authorize(denied, authenticator.IssueToken("job-reader"))
	deniedResponse := httptest.NewRecorder()
	handler.ServeHTTP(deniedResponse, denied)
	if deniedResponse.Code != http.StatusForbidden {
		t.Fatalf("reader status=%d body=%s", deniedResponse.Code, deniedResponse.Body.String())
	}
}

func TestRepositoryLifecycleJobControlsAreAdminOnlyAndAudited(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "job-controls", Format: repository.FormatRaw})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.ReplaceRepositoryGrants(ctx, repo.ID, []repository.RepositoryGrant{{Principal: "reader", Scopes: []string{"repositories:read"}}}, "1"); err != nil {
		t.Fatal(err)
	}
	enqueue := func(id string, maxAttempts int) {
		t.Helper()
		if _, _, enqueueErr := store.EnqueueLifecycleJob(ctx, repository.LifecycleJob{ID: id, RepositoryID: repo.ID, Kind: repository.LifecycleJobReclaim, IdempotencyKey: id, Payload: []byte(`{"format":"raw"}`), MaxAttempts: maxAttempts}); enqueueErr != nil {
			t.Fatal(enqueueErr)
		}
	}
	cancelID, runID, failedID, pendingID := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	enqueue(cancelID, 3)
	enqueue(runID, 3)
	claimed, err := store.ClaimLifecycleJobs(ctx, 1)
	if err != nil || len(claimed) != 1 || claimed[0].ID != cancelID {
		t.Fatalf("first claim=%#v err=%v", claimed, err)
	}
	if err = store.FailLifecycleJob(ctx, cancelID, claimed[0].LeaseToken, "temporary"); err != nil {
		t.Fatal(err)
	}
	if _, err = store.RunLifecycleJobNow(ctx, repo.ID, cancelID); err != nil {
		t.Fatal(err)
	}
	claimed, err = store.ClaimLifecycleJobs(ctx, 1)
	if err != nil || len(claimed) != 1 || claimed[0].ID != cancelID {
		t.Fatalf("second claim=%#v err=%v", claimed, err)
	}
	if err = store.CompleteLifecycleJob(ctx, cancelID, claimed[0].LeaseToken); err != nil {
		t.Fatal(err)
	}
	claimed, err = store.ClaimLifecycleJobs(ctx, 1)
	if err != nil || len(claimed) != 1 || claimed[0].ID != runID {
		t.Fatalf("run claim=%#v err=%v", claimed, err)
	}
	if err = store.FailLifecycleJob(ctx, runID, claimed[0].LeaseToken, "temporary"); err != nil {
		t.Fatal(err)
	}
	enqueue(failedID, 1)
	claimed, err = store.ClaimLifecycleJobs(ctx, 1)
	if err != nil || len(claimed) != 1 || claimed[0].ID != failedID {
		t.Fatalf("failed claim=%#v err=%v", claimed, err)
	}
	if err = store.FailLifecycleJob(ctx, failedID, claimed[0].LeaseToken, "permanent"); err != nil {
		t.Fatal(err)
	}
	enqueue(pendingID, 3)

	authenticator := testAuthenticator()
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, authenticator)
	request := func(id, action, token string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/api/v2/repositories/"+repo.ID+"/lifecycle-jobs/"+id+"/"+action, nil)
		authorize(req, token)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		return response
	}
	if response := request(pendingID, "cancel", "admin-secret"); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"state":"cancelled"`) {
		t.Fatalf("cancel=%d body=%s", response.Code, response.Body.String())
	}
	if response := request(runID, "cancel", authenticator.IssueToken("reader")); response.Code != http.StatusForbidden {
		t.Fatalf("reader cancel=%d body=%s", response.Code, response.Body.String())
	}
	if response := request(runID, "run", "admin-secret"); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"state":"pending"`) {
		t.Fatalf("run now=%d body=%s", response.Code, response.Body.String())
	}
	if response := request(failedID, "retry", "admin-secret"); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"state":"pending"`) || !strings.Contains(response.Body.String(), `"attempts":0`) {
		t.Fatalf("retry=%d body=%s", response.Code, response.Body.String())
	}
	if response := request(cancelID, "retry", "admin-secret"); response.Code != http.StatusConflict {
		t.Fatalf("completed retry=%d body=%s", response.Code, response.Body.String())
	}
	operations := map[string]bool{}
	for _, audit := range store.Audits {
		operations[audit.Operation] = true
	}
	for _, operation := range []string{"lifecycle.cancel", "lifecycle.run_now", "lifecycle.retry"} {
		if !operations[operation] {
			t.Fatalf("missing audit %q in %#v", operation, store.Audits)
		}
	}
}

func TestRepositoryTombstoneInspectionUsesBoundPagination(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "tombstone-target", Format: repository.FormatOCI})
	if err != nil {
		t.Fatal(err)
	}
	for i, name := range []string{"team/alpha", "team/beta"} {
		digest := fmt.Sprintf("sha256:%064x", i+1)
		if _, err = store.PutOCIManifest(ctx, repository.OCIManifest{RepositoryID: repo.ID, Name: name, Digest: digest, ObjectKey: fmt.Sprintf("oci/%d", i), MediaType: "application/json", Size: 1}, "latest"); err != nil {
			t.Fatal(err)
		}
		if err = store.DeleteOCIManifest(ctx, repo.ID, name, digest); err != nil {
			t.Fatal(err)
		}
	}
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())
	request := func(query string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/"+repo.ID+"/tombstones?"+query, nil)
		authorize(req, "admin-secret")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		return response
	}
	first := request("q=team%2F&pageSize=1")
	var page struct {
		Items []struct {
			Coordinate string `json:"coordinate"`
		}
		NextPageToken string `json:"nextPageToken"`
	}
	if first.Code != http.StatusOK || json.NewDecoder(first.Body).Decode(&page) != nil || len(page.Items) != 1 || page.Items[0].Coordinate[:10] != "team/alpha" || page.NextPageToken == "" {
		t.Fatalf("first=%d body=%s page=%#v", first.Code, first.Body.String(), page)
	}
	next := request("q=team%2F&pageSize=1&pageToken=" + url.QueryEscape(page.NextPageToken))
	if next.Code != http.StatusOK || !strings.Contains(next.Body.String(), "team/beta") {
		t.Fatalf("next=%d body=%s", next.Code, next.Body.String())
	}
	changed := request("q=other%2F&pageToken=" + url.QueryEscape(page.NextPageToken))
	if changed.Code != http.StatusBadRequest || !strings.Contains(changed.Body.String(), "invalid_page_token") {
		t.Fatalf("changed=%d body=%s", changed.Code, changed.Body.String())
	}
}

func TestMavenArtifactDetailAndTombstoneManagement(t *testing.T) {
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "artifact-target", Format: repository.FormatMaven})
	if err != nil {
		t.Fatal(err)
	}
	const key = "native/maven/sha256/artifact-target"
	session := repository.MavenPublishSession{ID: uuid.NewString(), RepositoryID: repo.ID, Coordinate: "org.example:widget:1.0.0", State: "open", ExpiresAt: time.Now().Add(time.Hour), Objects: []repository.MavenDeclaredObject{{Name: "widget-1.0.0.jar", Digest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Size: 3}}}
	if _, err = store.CreateMavenPublishSession(context.Background(), session); err != nil {
		t.Fatal(err)
	}
	if err = store.MarkMavenPublishObject(context.Background(), session.ID, session.Objects[0].Name, key); err != nil {
		t.Fatal(err)
	}
	artifact, err := store.CommitMavenPublishSession(context.Background(), session.ID, []repository.MavenAsset{{RepositoryID: repo.ID, Path: "org/example/widget/1.0.0/widget-1.0.0.jar", ObjectKey: key, Digest: session.Objects[0].Digest, Size: 3}})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())
	detail := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/"+repo.ID+"/artifacts/"+artifact.ID, nil)
	authorize(detail, "admin-secret")
	detailed := httptest.NewRecorder()
	handler.ServeHTTP(detailed, detail)
	if detailed.Code != http.StatusOK || !strings.Contains(detailed.Body.String(), `"state":"visible"`) {
		t.Fatalf("detail=%d body=%s", detailed.Code, detailed.Body.String())
	}
	deleteRequest := httptest.NewRequest(http.MethodDelete, "/api/v2/repositories/"+repo.ID+"/artifacts/"+artifact.ID, nil)
	authorize(deleteRequest, "admin-secret")
	deleted := httptest.NewRecorder()
	handler.ServeHTTP(deleted, deleteRequest)
	if deleted.Code != http.StatusAccepted || !strings.Contains(deleted.Body.String(), `"state":"pending"`) {
		t.Fatalf("delete=%d body=%s", deleted.Code, deleted.Body.String())
	}
	repeated := httptest.NewRecorder()
	repeatRequest := httptest.NewRequest(http.MethodDelete, "/api/v2/repositories/"+repo.ID+"/artifacts/"+artifact.ID, nil)
	authorize(repeatRequest, "admin-secret")
	handler.ServeHTTP(repeated, repeatRequest)
	if repeated.Code != http.StatusAccepted {
		t.Fatalf("repeat delete=%d body=%s", repeated.Code, repeated.Body.String())
	}
	list := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/"+repo.ID+"/artifacts", nil)
	authorize(list, "admin-secret")
	listed := httptest.NewRecorder()
	handler.ServeHTTP(listed, list)
	if listed.Code != http.StatusOK || strings.Contains(listed.Body.String(), artifact.ID) {
		t.Fatalf("list=%d body=%s", listed.Code, listed.Body.String())
	}
	protocolRead := httptest.NewRequest(http.MethodGet, "/repository/maven/artifact-target/org/example/widget/1.0.0/widget-1.0.0.jar", nil)
	protocolRead.SetBasicAuth("maven", "resolver-secret")
	protocolResponse := httptest.NewRecorder()
	handler.ServeHTTP(protocolResponse, protocolRead)
	if protocolResponse.Code != http.StatusNotFound {
		t.Fatalf("protocol read=%d body=%s", protocolResponse.Code, protocolResponse.Body.String())
	}
	detailAfterDelete := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/"+repo.ID+"/artifacts/"+artifact.ID, nil)
	authorize(detailAfterDelete, "admin-secret")
	detailedAfterDelete := httptest.NewRecorder()
	handler.ServeHTTP(detailedAfterDelete, detailAfterDelete)
	if detailedAfterDelete.Code != http.StatusOK || !strings.Contains(detailedAfterDelete.Body.String(), `"state":"deleted"`) {
		t.Fatalf("detail after delete=%d body=%s", detailedAfterDelete.Code, detailedAfterDelete.Body.String())
	}
	tombstone, err := store.GetArtifactTombstone(context.Background(), repo.ID, repository.FormatMaven, artifact.Coordinate)
	if err != nil || tombstone.Digest != artifact.Digest || tombstone.TombstonedAt.IsZero() {
		t.Fatalf("tombstone=%#v err=%v", tombstone, err)
	}
}

func TestConanRevisionManagementListsAndTombstonesSelectedRevisions(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	enableAnonymousAccess(t, store)
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "conan-revisions", Format: repository.FormatConan, AnonymousRead: true})
	if err != nil {
		t.Fatal(err)
	}
	const reference = "widget/1.0/user/stable"
	const recipeRevision = "recipe-r1"
	const packageID = "package-a"
	const packageRevision = "package-r1"
	const digest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if err = store.StageConanObject(ctx, repository.ConanObjectIntent{RepositoryID: repo.ID, ObjectKey: "native/conan/recipe", Digest: digest, Size: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err = store.PutConanRecipeRevision(ctx, repository.ConanRecipeRevision{RepositoryID: repo.ID, Reference: reference, Revision: recipeRevision, Digest: digest}, []repository.ConanAsset{{RepositoryID: repo.ID, Reference: reference, RecipeRevision: recipeRevision, Path: "conanfile.py", ObjectKey: "native/conan/recipe", Digest: digest, Size: 1}}); err != nil {
		t.Fatal(err)
	}
	if err = store.StageConanObject(ctx, repository.ConanObjectIntent{RepositoryID: repo.ID, ObjectKey: "native/conan/package", Digest: digest, Size: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err = store.PutConanPackageRevision(ctx, repository.ConanPackageRevision{RepositoryID: repo.ID, Reference: reference, RecipeRevision: recipeRevision, PackageID: packageID, Revision: packageRevision, Digest: digest}, []repository.ConanAsset{{RepositoryID: repo.ID, Reference: reference, RecipeRevision: recipeRevision, PackageID: packageID, PackageRevision: packageRevision, Path: "package.tgz", ObjectKey: "native/conan/package", Digest: digest, Size: 1}}); err != nil {
		t.Fatal(err)
	}
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())
	request := func(method, path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, nil)
		authorize(req, "admin-secret")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		return response
	}
	recipeList := request(http.MethodGet, "/api/v2/repositories/"+repo.ID+"/conan/recipe-revisions?reference="+url.QueryEscape(reference))
	if recipeList.Code != http.StatusOK || !strings.Contains(recipeList.Body.String(), `"revision":"recipe-r1"`) {
		t.Fatalf("recipe list=%d body=%s", recipeList.Code, recipeList.Body.String())
	}
	recipeListWithTrailingSlash := request(http.MethodGet, "/api/v2/repositories/"+repo.ID+"/conan/recipe-revisions?reference="+url.QueryEscape(reference+"/"))
	if recipeListWithTrailingSlash.Code != http.StatusOK || !strings.Contains(recipeListWithTrailingSlash.Body.String(), `"revision":"recipe-r1"`) {
		t.Fatalf("recipe list with trailing slash=%d body=%s", recipeListWithTrailingSlash.Code, recipeListWithTrailingSlash.Body.String())
	}
	anonymousRecipeList := httptest.NewRecorder()
	handler.ServeHTTP(anonymousRecipeList, httptest.NewRequest(http.MethodGet, "/api/v2/repositories/"+repo.ID+"/conan/recipe-revisions?reference="+url.QueryEscape(reference), nil))
	if anonymousRecipeList.Code != http.StatusOK {
		t.Fatalf("anonymous recipe list=%d body=%s", anonymousRecipeList.Code, anonymousRecipeList.Body.String())
	}
	packageIDs := request(http.MethodGet, "/api/v2/repositories/"+repo.ID+"/conan/package-ids?reference="+url.QueryEscape(reference)+"&recipeRevision="+recipeRevision)
	if packageIDs.Code != http.StatusOK || !strings.Contains(packageIDs.Body.String(), packageID) {
		t.Fatalf("package ids=%d body=%s", packageIDs.Code, packageIDs.Body.String())
	}
	emptyPackageIDs := request(http.MethodGet, "/api/v2/repositories/"+repo.ID+"/conan/package-ids?reference="+url.QueryEscape(reference)+"&recipeRevision=missing")
	if emptyPackageIDs.Code != http.StatusOK || !strings.Contains(emptyPackageIDs.Body.String(), `"items":[]`) {
		t.Fatalf("empty package ids=%d body=%s", emptyPackageIDs.Code, emptyPackageIDs.Body.String())
	}
	packageList := request(http.MethodGet, "/api/v2/repositories/"+repo.ID+"/conan/package-revisions?reference="+url.QueryEscape(reference)+"&recipeRevision="+recipeRevision+"&packageId="+packageID)
	if packageList.Code != http.StatusOK || !strings.Contains(packageList.Body.String(), `"revision":"package-r1"`) {
		t.Fatalf("package list=%d body=%s", packageList.Code, packageList.Body.String())
	}
	packageDelete := request(http.MethodDelete, "/api/v2/repositories/"+repo.ID+"/conan/package-revisions/"+packageRevision+"?reference="+url.QueryEscape(reference)+"&recipeRevision="+recipeRevision+"&packageId="+packageID)
	if packageDelete.Code != http.StatusNoContent {
		t.Fatalf("package delete=%d body=%s", packageDelete.Code, packageDelete.Body.String())
	}
	packageItem, err := store.GetConanPackageRevision(ctx, repo.ID, reference, recipeRevision, packageID, packageRevision)
	if err != nil || packageItem.State != "deleted" {
		t.Fatalf("package=%#v err=%v", packageItem, err)
	}
	recipe, err := store.GetConanRecipeRevision(ctx, repo.ID, reference, recipeRevision)
	if err != nil || recipe.State != "visible" {
		t.Fatalf("recipe=%#v err=%v", recipe, err)
	}
	proxy, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "conan-revisions-proxy", Format: repository.FormatConan, Type: repository.RepositoryTypeProxy, Endpoint: "https://conan.example", AllowedHosts: []string{"conan.example"}})
	if err != nil {
		t.Fatal(err)
	}
	proxyDelete := request(http.MethodDelete, "/api/v2/repositories/"+proxy.ID+"/conan/package-revisions/"+packageRevision+"?reference="+url.QueryEscape(reference)+"&recipeRevision="+recipeRevision+"&packageId="+packageID)
	if proxyDelete.Code != http.StatusBadRequest {
		t.Fatalf("proxy delete=%d body=%s", proxyDelete.Code, proxyDelete.Body.String())
	}
}
