package nuget

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ccsert/artifact-gateway/internal/authorization"
	dist "github.com/ccsert/artifact-gateway/internal/nugetdistribution"
	"github.com/ccsert/artifact-gateway/internal/objectstore"
	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

type replicationAuthorityStore interface {
	dist.Store
	repository.NativeNuGetStore
	repository.HostedRepositoryStore
	repository.RepositoryGrantStore
	repository.APIKeyStore
}

func replicationAuthorityHTTP(t *testing.T, module *Module) func(*testing.T, string, string, []byte, map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/v2/previews/nuget/") {
			module.Preview().ServeHTTP(w, r)
		} else {
			module.ServeHTTP(w, r)
		}
	}))
	t.Cleanup(server.Close)
	client := server.Client()
	client.Timeout = 10 * time.Second
	return func(t *testing.T, method, path string, body []byte, headers map[string]string) *httptest.ResponseRecorder {
		t.Helper()
		r, err := http.NewRequestWithContext(t.Context(), method, server.URL+path, bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		for k, v := range headers {
			r.Header.Set(k, v)
		}
		response, err := client.Do(r)
		if err != nil {
			t.Fatal("owned replication HTTP request failed")
		}
		defer func() { _ = response.Body.Close() }()
		w := httptest.NewRecorder()
		w.WriteHeader(response.StatusCode)
		if _, err = io.Copy(w, response.Body); err != nil {
			t.Fatal(err)
		}
		return w
	}
}

func TestNuGetReplicationRepositoryOnlyKeyAuthority(t *testing.T) {
	f := newReplicationFixture(t)
	testNuGetReplicationRepositoryOnlyKeyAuthority(t, f.module, f.store, f.objects, f.repo)
}

// Run the same public request and worker boundary against both current-snapshot
// implementations. No caller role or grant is injected into the durable job.
func testNuGetReplicationRepositoryOnlyKeyAuthority(t *testing.T, module *Module, store replicationAuthorityStore, objects objectstore.Store, source repository.HostedRepository) {
	t.Helper()
	module.distribution = store
	module.auth.APIKeys = store
	send := replicationAuthorityHTTP(t, module)
	body := packageFixture(t, "App", "1.0.0", "")
	payload, contentType := multipartPackage(t, body)
	w := send(t, "PUT", "/nuget/"+source.Name+"/api/v2/package/", payload, map[string]string{"X-NuGet-ApiKey": "nuget-test-admin", "Content-Type": contentType})
	if w.Code != 201 {
		t.Fatal(w.Body.String())
	}
	publication, err := store.GetNuGetPublication(t.Context(), source.ID, "app", "1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"missing-source-grant", "missing-target-grant", "missing-key", "revoked-key", "expired-key", "revoke-key-during-transfer", "expire-key-during-transfer", "revoke-source-during-transfer", "revoke-target-during-transfer", "success"} {
		t.Run(mode, func(t *testing.T) {
			target, err := store.CreateHostedRepository(t.Context(), repository.HostedRepository{ID: uuid.NewString(), Name: "scoped-target-" + uuid.NewString(), Format: repository.FormatNuGet, Type: repository.RepositoryTypeHosted})
			if err != nil {
				t.Fatal(err)
			}
			token := "repository-only-" + uuid.NewString()
			expires := time.Now().UTC().Add(time.Hour)
			switch mode {
			case "expired-key":
				expires = time.Now().UTC().Add(-time.Minute)
			case "expire-key-during-transfer":
				expires = time.Now().UTC().Add(2 * time.Second)
			}
			key := repository.APIKey{ID: uuid.NewString(), Name: "scoped-replication", SecretHash: authorization.HashAPIKey(token), Roles: []string{}, ExpiresAt: &expires}
			if mode != "missing-key" {
				if key, err = store.CreateAPIKey(t.Context(), key); err != nil || len(key.Roles) != 0 {
					t.Fatalf("repository-only key roles=%v err=%v", key.Roles, err)
				}
			}
			actor := "api-key:" + key.ID
			for _, repo := range []repository.HostedRepository{source, target} {
				scopes := []string{"repositories:read"}
				if repo.ID == target.ID {
					scopes = []string{"repositories:write"}
				}
				if mode == "missing-source-grant" && repo.ID == source.ID || mode == "missing-target-grant" && repo.ID == target.ID {
					scopes = nil
				}
				if len(scopes) > 0 {
					if _, err = store.UpsertRepositoryGrant(t.Context(), repo.ID, repository.RepositoryGrant{Principal: actor, Scopes: scopes, ResourcePrefix: "app"}); err != nil {
						t.Fatal(err)
					}
				}
			}
			if mode == "revoked-key" {
				if _, err = store.RevokeAPIKey(t.Context(), key.ID); err != nil {
					t.Fatal(err)
				}
			}
			plan := dist.Plan{SourceRepositoryID: source.ID, TargetRepositoryID: target.ID, Items: []dist.Identity{{PackageID: "app", Version: "1.0.0", Digest: publication.Digest, Size: publication.Size, SourceGovernanceVersion: publication.Governance.Version}}}
			encoded, _ := json.Marshal(plan)
			w := send(t, "POST", "/api/v2/previews/nuget/replication-jobs", encoded, map[string]string{"Authorization": "Bearer " + token, "Idempotency-Key": uuid.NewString()})
			want := 202
			switch mode {
			case "missing-source-grant", "missing-target-grant":
				want = 403
			case "missing-key", "revoked-key", "expired-key":
				want = 401
			}
			if w.Code != want {
				t.Fatalf("create=%d want=%d %s", w.Code, want, w.Body.String())
			}
			if want != 202 {
				// Explicit grants and a plausible actor string cannot substitute for
				// the missing/revoked/expired credential in either locked snapshot.
				forged, err := dist.NewJob(plan, actor, "api_key", "", uuid.NewString())
				if err != nil {
					t.Fatal(err)
				}
				if _, _, err = store.CreateNuGetReplication(t.Context(), forged, module.replicationCheck(forged)); !errors.Is(err, dist.ErrAccessDenied) {
					t.Fatalf("current authority accepted rejected HTTP caller=%v", err)
				}
				return
			}
			principal, ok := module.auth.AuthenticateContext(t.Context(), "Bearer "+token)
			if !ok || principal.Role != "" || principal.Admin || principal.Actor != actor {
				t.Fatal("repository-only key gained a global role")
			}
			var job dist.Job
			if err = json.Unmarshal(w.Body.Bytes(), &job); err != nil {
				t.Fatal(err)
			}
			module.objects = &replicationHookObjects{Store: objects, hook: func() {
				switch mode {
				case "revoke-key-during-transfer":
					_, err = store.RevokeAPIKey(t.Context(), key.ID)
				case "expire-key-during-transfer":
					time.Sleep(time.Until(expires) + time.Millisecond)
				case "revoke-source-during-transfer", "revoke-target-during-transfer":
					id := source.ID
					if strings.HasPrefix(mode, "revoke-target") {
						id = target.ID
					}
					_, err = store.DeleteRepositoryGrant(t.Context(), id, actor, "app")
				}
				if err != nil {
					t.Fatal(err)
				}
			}}
			defer func() { module.objects = objects }()
			worker, err := module.ReplicationWorker(uuid.NewString())
			if err != nil {
				t.Fatal(err)
			}
			if ran, err := worker.RunOnce(t.Context()); !ran || err != nil {
				t.Fatalf("worker=%t err=%v", ran, err)
			}
			job, err = store.GetNuGetReplication(t.Context(), job.ID)
			if err != nil {
				t.Fatal(err)
			}
			if mode != "success" {
				if job.State != "failed" || job.Items[0].Code != "access_denied" {
					t.Fatalf("revocation/expiry bypassed=%+v", job)
				}
				if _, err = store.GetNuGetPublication(t.Context(), target.ID, "app", "1.0.0"); !errors.Is(err, repository.ErrNotFound) {
					t.Fatalf("unauthorized target visible=%v", err)
				}
				return
			}
			copied, err := store.GetNuGetPublication(t.Context(), target.ID, "app", "1.0.0")
			if err != nil || job.State != "succeeded" || copied.ObjectKey == publication.ObjectKey || copied.SessionID == publication.SessionID || !strings.Contains(copied.ObjectKey, target.ID) {
				t.Fatalf("target ownership=%+v state=%s err=%v", copied, job.State, err)
			}
			if err = objects.Delete(t.Context(), publication.ObjectKey); err != nil {
				t.Fatal(err)
			}
			w = send(t, "GET", "/nuget/"+target.Name+"/v3/flat/app/1.0.0/app.1.0.0.nupkg", nil, map[string]string{"Authorization": "Bearer " + token})
			if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), body) {
				t.Fatalf("target-only original bytes=%d", w.Code)
			}
		})
	}
}

func TestNuGetReplicationBlockedCurrentUserAuthority(t *testing.T) {
	f := newReplicationFixture(t)
	testNuGetReplicationBlockedCurrentUserAuthority(t, f.module, f.store, f.objects, f.repo)
}

func testNuGetReplicationBlockedCurrentUserAuthority(t *testing.T, module *Module, store interface {
	replicationAuthorityStore
	repository.UserStore
}, objects objectstore.Store, source repository.HostedRepository) {
	t.Helper()
	module.distribution, module.auth.Users = store, store
	send := replicationAuthorityHTTP(t, module)
	body := packageFixture(t, "App", "1.0.0", "")
	payload, contentType := multipartPackage(t, body)
	if w := send(t, "PUT", "/nuget/"+source.Name+"/api/v2/package/", payload, map[string]string{"X-NuGet-ApiKey": "nuget-test-admin", "Content-Type": contentType}); w.Code != 201 {
		t.Fatal(w.Body.String())
	}
	publication, err := store.GetNuGetPublication(t.Context(), source.ID, "app", "1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"empty-role", "pending", "disabled", "password-reset", "deleted"} {
		t.Run(mode, func(t *testing.T) {
			role := "member"
			if mode == "empty-role" {
				role = ""
			}
			user, err := store.CreateUser(t.Context(), repository.User{ID: uuid.NewString(), Name: "replicator-" + uuid.NewString(), Role: role})
			if err != nil {
				// PostgreSQL disallows an empty persisted role at its schema
				// boundary. Memory still exercises the malformed snapshot guard.
				var checkViolation interface{ SQLState() string }
				if mode == "empty-role" && errors.As(err, &checkViolation) && checkViolation.SQLState() == "23514" {
					t.Log("empty persisted user role rejected by database CHECK")
					return
				}
				t.Fatal(err)
			}
			target, err := store.CreateHostedRepository(t.Context(), repository.HostedRepository{ID: uuid.NewString(), Name: "user-target-" + uuid.NewString(), Format: repository.FormatNuGet, Type: repository.RepositoryTypeHosted})
			if err != nil {
				t.Fatal(err)
			}
			for _, repo := range []repository.HostedRepository{source, target} {
				scopes := []string{"repositories:read"}
				if repo.ID == target.ID {
					scopes = []string{"repositories:write"}
				}
				if _, err = store.UpsertRepositoryGrant(t.Context(), repo.ID, repository.RepositoryGrant{Principal: "user:" + user.Name, Scopes: scopes, ResourcePrefix: "app"}); err != nil {
					t.Fatal(err)
				}
			}
			plan := dist.Plan{SourceRepositoryID: source.ID, TargetRepositoryID: target.ID, Items: []dist.Identity{{PackageID: "app", Version: "1.0.0", Digest: publication.Digest, Size: publication.Size, SourceGovernanceVersion: publication.Governance.Version}}}
			encoded, _ := json.Marshal(plan)
			token := module.auth.IssueUserSession(user.ID, user.SessionVersion)
			w := send(t, "POST", "/api/v2/previews/nuget/replication-jobs", encoded, map[string]string{"Authorization": "Bearer " + token, "Idempotency-Key": uuid.NewString()})
			if mode == "empty-role" {
				if w.Code != 403 {
					t.Fatalf("empty-role user HTTP create=%d", w.Code)
				}
				for _, kind := range []string{"local_session", "oidc"} {
					forged, err := dist.NewJob(plan, "user:"+user.Name, kind, "", uuid.NewString())
					if err != nil {
						t.Fatal(err)
					}
					if _, _, err = store.CreateNuGetReplication(t.Context(), forged, module.replicationCheck(forged)); !errors.Is(err, dist.ErrAccessDenied) {
						t.Fatalf("empty-role user snapshot=%v", err)
					}
				}
				return
			}
			var job dist.Job
			if w.Code != 202 || json.Unmarshal(w.Body.Bytes(), &job) != nil {
				t.Fatalf("active user create=%d %s", w.Code, w.Body.String())
			}
			module.objects = &replicationHookObjects{Store: objects, hook: func() {
				switch mode {
				case "pending":
					role := "none"
					_, err = store.UpdateUser(t.Context(), repository.UserUpdate{ID: user.ID, Role: &role}, user.Version)
				case "disabled":
					state := repository.UserDisabled
					_, err = store.UpdateUser(t.Context(), repository.UserUpdate{ID: user.ID, State: &state}, user.Version)
				case "password-reset":
					_, err = store.UpdateUserPassword(t.Context(), user.ID, "reset-password-hash", user.Version, true)
				case "deleted":
					err = store.DeleteUser(t.Context(), user.ID)
				}
				if err != nil {
					t.Fatal(err)
				}
			}}
			defer func() { module.objects = objects }()
			worker, err := module.ReplicationWorker(uuid.NewString())
			if err != nil {
				t.Fatal(err)
			}
			if ran, err := worker.RunOnce(t.Context()); !ran || err != nil {
				t.Fatalf("worker=%t err=%v", ran, err)
			}
			job, err = store.GetNuGetReplication(t.Context(), job.ID)
			if err != nil || job.State != "failed" || job.Items[0].Code != "access_denied" {
				t.Fatalf("blocked user committed=%+v err=%v", job, err)
			}
			if _, err = store.GetNuGetPublication(t.Context(), target.ID, "app", "1.0.0"); !errors.Is(err, repository.ErrNotFound) {
				t.Fatalf("blocked user target visible=%v", err)
			}
			w = send(t, "POST", "/api/v2/previews/nuget/replication-jobs", encoded, map[string]string{"Authorization": "Bearer " + token, "Idempotency-Key": uuid.NewString()})
			if w.Code != 401 && w.Code != 403 {
				t.Fatalf("blocked user retained HTTP access=%d", w.Code)
			}
		})
	}
}
