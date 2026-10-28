package rubygems

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ccsert/artifact-gateway/internal/authorization"
	"github.com/ccsert/artifact-gateway/internal/objectstore"
	"github.com/ccsert/artifact-gateway/internal/repository"
	dist "github.com/ccsert/artifact-gateway/internal/rubygemsdistribution"
	domain "github.com/ccsert/artifact-gateway/internal/rubygemspublication"
	"github.com/google/uuid"
)

type distributionStore interface {
	domain.Store
	dist.Store
}
type distributionRepos interface {
	repository.HostedRepositoryStore
	repository.RepositoryGrantStore
	repository.APIKeyStore
	repository.UserStore
	repository.UserSessionStore
	repository.ServiceAccountStore
	repository.AuditStore
	repository.AnonymousAccessPolicyStore
}
type distributionFixture struct {
	module         *Module
	store          distributionStore
	repos          distributionRepos
	objects        objectstore.Store
	source, target repository.HostedRepository
}

func distributionMemory(t *testing.T) distributionFixture {
	t.Helper()
	f := setup(t)
	target, err := f.repos.CreateHostedRepository(t.Context(), repository.HostedRepository{ID: uuid.NewString(), Name: "replication-target-" + uuid.NewString(), Format: repository.FormatRubyGems, Type: repository.RepositoryTypeHosted})
	if err != nil {
		t.Fatal(err)
	}
	f.module.distribution = f.store
	f.module.auth.APIKeys = f.repos
	f.module.auth.Users = f.repos
	f.module.auth.UserSessions = f.repos
	f.module.auth.ServiceAccounts = f.repos
	return distributionFixture{f.module, f.store, f.repos, f.objects, f.repo, target}
}
func replicationRequest(handler http.Handler, method, path string, body []byte, token, key string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, bytes.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("Content-Type", "application/json")
	if key != "" {
		r.Header.Set("Idempotency-Key", key)
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	return w
}
func distributionPublish(t *testing.T, f distributionFixture, repo repository.HostedRepository, name, platform string) (domain.Publication, []byte) {
	t.Helper()
	body := gem(t, name, "1.0.0", platform, "same original archive")
	w := request(f.module, "POST", "/rubygems/"+repo.Name+"/api/v1/gems", body, "owned-admin")
	if w.Code != 201 {
		t.Fatalf("publish %d %s", w.Code, w.Body.String())
	}
	p, err := f.store.GetRetained(t.Context(), repo.ID, name, "1.0.0", platform)
	if err != nil {
		t.Fatal(err)
	}
	return p, body
}
func distributionPlan(f distributionFixture, p ...domain.Publication) dist.Plan {
	out := dist.Plan{SourceRepositoryID: f.source.ID, TargetRepositoryID: f.target.ID}
	for _, v := range p {
		out.Items = append(out.Items, dist.Identity{Name: v.Name, Version: v.Version, Platform: v.Platform, Digest: v.Digest, Size: v.Size, SourceGovernanceVersion: v.Governance.Version})
	}
	return out
}
func distributionCreate(t *testing.T, f distributionFixture, p dist.Plan, token, key string) dist.Job {
	t.Helper()
	body, _ := json.Marshal(p)
	w := replicationRequest(f.module.Preview(), "POST", "/api/v2/previews/rubygems/replication-jobs", body, token, key)
	var j dist.Job
	if w.Code != 202 || json.Unmarshal(w.Body.Bytes(), &j) != nil {
		t.Fatalf("create %d %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "objectKey") || strings.Contains(w.Body.String(), "credentialId") || strings.Contains(w.Body.String(), "leaseToken") {
		t.Fatal("private references leaked")
	}
	return j
}
func distributionRun(t *testing.T, f distributionFixture, id string) dist.Job {
	t.Helper()
	w, err := f.module.ReplicationWorker("owned-worker")
	if err != nil {
		t.Fatal(err)
	}
	if ran, err := w.RunOnce(t.Context()); !ran || err != nil {
		t.Fatalf("worker %t %v", ran, err)
	}
	j, err := f.store.GetRubyGemsReplication(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	return j
}
func testDistributionComplete(t *testing.T, f distributionFixture) {
	t.Helper()
	ruby, body := distributionPublish(t, f, f.source, "owned_variant", "ruby")
	java, javaBody := distributionPublish(t, f, f.source, "owned_variant", "java")
	plan := distributionPlan(f, ruby, java)
	key := uuid.NewString()
	j := distributionCreate(t, f, plan, "owned-admin", key)
	j = distributionRun(t, f, j.ID)
	if j.State != "succeeded" {
		t.Fatalf("job %+v", j)
	}
	replay := distributionCreate(t, f, plan, "owned-admin", key)
	if replay.ID != j.ID || replay.Version != j.Version {
		t.Fatal("replay changed durable result")
	}
	for _, v := range []struct {
		p    domain.Publication
		body []byte
	}{{ruby, body}, {java, javaBody}} {
		got, err := f.store.GetRetained(t.Context(), f.target.ID, v.p.Name, v.p.Version, v.p.Platform)
		if err != nil || got.ObjectKey == v.p.ObjectKey || !bytes.Equal(got.MetadataYAML, v.p.MetadataYAML) || !bytes.Equal(got.MetadataJSON, v.p.MetadataJSON) {
			t.Fatalf("metadata/independent object %+v %v", got, err)
		}
		w := request(f.module, "GET", "/rubygems/"+f.target.Name+"/gems/"+got.FullName()+".gem", nil, "owned-admin")
		if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), v.body) {
			t.Fatal("target bytes changed", w.Code)
		}
	}
	identityPath := "/api/v2/previews/rubygems/repositories/" + f.source.ID + "/packages/owned_variant/1.0/ruby"
	w := request(f.module.Preview(), "GET", identityPath, nil, "Bearer owned-admin")
	var identity dist.Identity
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &identity) != nil || identity.Version != "1.0.0" || identity.Platform != "ruby" || identity.Digest != ruby.Digest {
		t.Fatalf("server identity %d %s", w.Code, w.Body.String())
	}
	// Complete-byte verification applies even to an already-present target.
	target, err := f.store.GetRetained(t.Context(), f.target.ID, ruby.Name, ruby.Version, ruby.Platform)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.objects.Put(t.Context(), target.ObjectKey, []byte("owned corrupt bytes")); err != nil {
		t.Fatal(err)
	}
	bad := distributionCreate(t, f, distributionPlan(f, ruby), "owned-admin", uuid.NewString())
	bad = distributionRun(t, f, bad.ID)
	if bad.State != "failed" || bad.Items[0].Code != "transfer_failed" {
		t.Fatalf("corrupt target accepted %+v", bad)
	}
	if w = request(f.module, "GET", "/rubygems/"+f.target.Name+"/gems/"+ruby.FullName()+".gem", nil, "owned-admin"); w.Code != 500 {
		t.Fatalf("corrupt bytes served %d", w.Code)
	}
}
func TestRubyGemsReplicationOriginalBytesMetadataAndPlatform(t *testing.T) {
	testDistributionComplete(t, distributionMemory(t))
}

type distributionReadHook struct {
	objectstore.Store
	target string
	once   sync.Once
	apply  func()
}

func (s *distributionReadHook) Open(ctx context.Context, key string) (io.ReadCloser, int64, error) {
	r, n, err := s.Store.Open(ctx, key)
	if err == nil && strings.HasPrefix(key, "native/rubygems/"+s.target+"/") {
		s.once.Do(s.apply)
	}
	return r, n, err
}
func testDistributionGovernanceAndAuthority(t *testing.T, f distributionFixture) {
	t.Helper()
	for _, mode := range []string{"source-yank", "source-quarantine", "source-roundtrip", "target-yank", "target-quarantine", "source-grant", "target-grant", "key-revoke", "session-revoke", "password-reset", "user-disable", "key-expire", "service-disabled", "credential-revoke", "session-record-revoke"} {
		t.Run(mode, func(t *testing.T) {
			name := "owned_" + strings.ReplaceAll(mode, "-", "_")
			p, _ := distributionPublish(t, f, f.source, name, "ruby")
			token := "owned-key-" + uuid.NewString()
			key := repository.APIKey{ID: uuid.NewString(), Name: "replication-" + uuid.NewString(), SecretHash: authorization.HashAPIKey(token), Roles: []string{}}
			actor := "api-key:" + key.ID
			var user repository.User
			var account repository.ServiceAccount
			var sessionID string
			var err error
			if strings.HasPrefix(mode, "session-") || mode == "password-reset" || mode == "user-disable" {
				user, err = f.repos.CreateUser(t.Context(), repository.User{ID: uuid.NewString(), Name: "owned-" + uuid.NewString(), Role: "member"})
				if err != nil {
					t.Fatal(err)
				}
				actor = "user:" + user.Name
				token = f.module.auth.IssueUserSession(user.ID, user.SessionVersion)
				if mode == "session-record-revoke" {
					sessionID = uuid.NewString()
					if _, err = f.repos.CreateUserSession(t.Context(), repository.UserSession{ID: sessionID, UserID: user.ID, Kind: repository.UserSessionLocal, ExpiresAt: time.Now().UTC().Add(time.Hour)}); err != nil {
						t.Fatal(err)
					}
					token = f.module.auth.IssueUserSessionWithID(user.ID, sessionID, user.SessionVersion)
				}
			} else {
				if mode == "key-expire" {
					expiry := time.Now().UTC().Add(2 * time.Second)
					key.ExpiresAt = &expiry
				}
				if mode == "service-disabled" || mode == "credential-revoke" {
					account, err = f.repos.CreateServiceAccount(t.Context(), repository.ServiceAccount{ID: uuid.NewString(), Name: "owned-" + uuid.NewString()})
					if err != nil {
						t.Fatal(err)
					}
					key.ServiceAccountID = account.ID
					actor = "service-account:" + account.ID
					_, err = f.repos.CreateServiceAccountCredential(t.Context(), key)
				} else {
					_, err = f.repos.CreateAPIKey(t.Context(), key)
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			for _, r := range []repository.HostedRepository{f.source, f.target} {
				g, err := f.repos.GetRepositoryGrants(t.Context(), r.ID)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = f.repos.ReplaceRepositoryGrants(t.Context(), r.ID, []repository.RepositoryGrant{{Principal: actor, ResourcePrefix: name, Scopes: []string{"repositories:read", "repositories:write"}}}, g.Version); err != nil {
					t.Fatal(err)
				}
			}
			if strings.HasPrefix(mode, "target-") && mode != "target-grant" {
				distributionPublish(t, f, f.target, name, "ruby")
			}
			identityPath := "/api/v2/previews/rubygems/repositories/" + f.source.ID + "/packages/" + name + "/1.0.0/ruby"
			if w := request(f.module.Preview(), "GET", identityPath, nil, "Bearer "+token); w.Code != 200 {
				t.Fatalf("source read identity %d %s", w.Code, w.Body.String())
			}
			j := distributionCreate(t, f, distributionPlan(f, p), token, uuid.NewString())
			f.module.objects = &distributionReadHook{Store: f.objects, target: f.target.ID, apply: func() {
				switch mode {
				case "source-yank", "source-quarantine", "source-roundtrip", "target-yank", "target-quarantine":
					repo := f.source
					if strings.HasPrefix(mode, "target-") {
						repo = f.target
					}
					action := "yank"
					if strings.Contains(mode, "quarantine") {
						action = "quarantine"
					}
					c := domain.GovernanceChange{Identity: repository.RubyGemsGovernanceIdentity{Actor: "operator", Kind: "static_admin"}, RepositoryID: repo.ID, Name: p.Name, Version: p.Version, Platform: p.Platform, ExpectedDigest: p.Digest, ExpectedVersion: "1", Actor: "operator", Action: action, Reason: "owned concurrent governance"}
					check := func(ctx context.Context, _ repository.RubyGemsGovernanceAuthority) error { return ctx.Err() }
					g, e := f.store.TransitionGovernance(t.Context(), c, check)
					if e != nil {
						t.Fatal(e)
					}
					if mode == "source-roundtrip" {
						c.ExpectedVersion, c.Action = g.Governance.Version, "restore"
						if _, e = f.store.TransitionGovernance(t.Context(), c, check); e != nil {
							t.Fatal(e)
						}
					}
				case "source-grant", "target-grant":
					repo := f.source
					if mode == "target-grant" {
						repo = f.target
					}
					g, e := f.repos.GetRepositoryGrants(t.Context(), repo.ID)
					if e != nil {
						t.Fatal(e)
					}
					if _, e = f.repos.ReplaceRepositoryGrants(t.Context(), repo.ID, nil, g.Version); e != nil {
						t.Fatal(e)
					}
				case "key-revoke":
					if _, e := f.repos.RevokeAPIKey(t.Context(), key.ID); e != nil {
						t.Fatal(e)
					}
				case "session-revoke", "password-reset":
					if _, e := f.repos.RevokeUserSessions(t.Context(), user.ID, user.Version); e != nil {
						t.Fatal(e)
					}
				case "user-disable":
					state := "disabled"
					if _, e := f.repos.UpdateUser(t.Context(), repository.UserUpdate{ID: user.ID, State: &state}, user.Version); e != nil {
						t.Fatal(e)
					}
				case "service-disabled":
					state := repository.ServiceAccountDisabled
					if _, e := f.repos.UpdateServiceAccount(t.Context(), repository.ServiceAccountUpdate{ID: account.ID, State: &state}, account.Version); e != nil {
						t.Fatal(e)
					}
				case "credential-revoke":
					if _, e := f.repos.RevokeServiceAccountCredential(t.Context(), account.ID, key.ID); e != nil {
						t.Fatal(e)
					}
				case "session-record-revoke":
					if _, e := f.repos.RevokeUserSession(t.Context(), user.ID, sessionID); e != nil {
						t.Fatal(e)
					}
				case "key-expire":
					time.Sleep(time.Until(*key.ExpiresAt) + 20*time.Millisecond)
				}
			}}
			j = distributionRun(t, f, j.ID)
			f.module.objects = f.objects
			want := "access_denied"
			if strings.HasPrefix(mode, "source-") && mode != "source-grant" {
				want = "source_changed"
			}
			if strings.HasPrefix(mode, "target-") && mode != "target-grant" {
				want = "target_conflict"
			}
			if j.State != "failed" || j.Items[0].Code != want {
				t.Fatalf("not closed %+v want %s", j, want)
			}
			retained, e := f.store.GetRetained(t.Context(), f.target.ID, p.Name, p.Version, p.Platform)
			if strings.HasPrefix(mode, "target-") && mode != "target-grant" {
				if e != nil || mode == "target-yank" && !retained.Governance.Yanked || mode == "target-quarantine" && !retained.Governance.Quarantined {
					t.Fatalf("target revived %+v %v", retained, e)
				}
			} else if !errors.Is(e, repository.ErrNotFound) {
				t.Fatalf("denied item visible %+v %v", retained, e)
			}
		})
	}
}
func TestRubyGemsReplicationRechecksGovernanceGrantsAndOriginalCredential(t *testing.T) {
	testDistributionGovernanceAndAuthority(t, distributionMemory(t))
}

func TestRubyGemsReplicationRejectsInvalidPlansQuotaAndForgedTransfers(t *testing.T) {
	f := distributionMemory(t)
	p, body := distributionPublish(t, f, f.source, "owned_guards", "ruby")
	plan := distributionPlan(f, p)
	encoded, _ := json.Marshal(plan)
	for _, c := range []struct {
		body   []byte
		key    string
		status int
	}{{encoded, "", 400}, {[]byte(`{"unexpected":true}`), "owned", 400}, {[]byte(`{"sourceRepositoryId":"bad"}`), "owned", 400}} {
		w := replicationRequest(f.module.Preview(), "POST", "/api/v2/previews/rubygems/replication-jobs", c.body, "owned-admin", c.key)
		if w.Code != c.status {
			t.Fatalf("invalid request %d %s", w.Code, w.Body.String())
		}
	}
	if _, err := f.store.SetQuota(t.Context(), f.target.ID, "operator", p.Size-1); err != nil {
		t.Fatal(err)
	}
	w := replicationRequest(f.module.Preview(), "POST", "/api/v2/previews/rubygems/replication-jobs", encoded, "owned-admin", uuid.NewString())
	if w.Code != 409 {
		t.Fatal("quota accepted", w.Code)
	}
	if _, err := f.store.SetQuota(t.Context(), f.target.ID, "operator", 0); err != nil {
		t.Fatal(err)
	}
	j := distributionCreate(t, f, plan, "owned-admin", uuid.NewString())
	claim, err := f.store.ClaimRubyGemsReplication(t.Context(), "owned", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	forged := dist.Clone(claim)
	forged.Actor = "forged"
	if err = f.module.ReplicateRubyGemsItem(t.Context(), forged, forged.Items[0]); !errors.Is(err, dist.ErrInvalid) {
		t.Fatalf("forged actor %v", err)
	}
	forged = dist.Clone(claim)
	forged.Items[0].Digest = "sha256:" + strings.Repeat("f", 64)
	_, forged.PlanDigest, _ = dist.CanonicalPlan(dist.Plan{SourceRepositoryID: forged.SourceRepositoryID, TargetRepositoryID: forged.TargetRepositoryID, Items: []dist.Identity{forged.Items[0].Identity}})
	if err = f.module.ReplicateRubyGemsItem(t.Context(), forged, forged.Items[0]); !errors.Is(err, dist.ErrInvalid) {
		t.Fatalf("forged plan %v", err)
	}
	if err = f.objects.Put(t.Context(), p.ObjectKey, append([]byte("bad"), body...)); err != nil {
		t.Fatal(err)
	}
	if err = f.module.ReplicateRubyGemsItem(t.Context(), claim, claim.Items[0]); err == nil {
		t.Fatal("corrupt source transferred")
	}
	if _, err = f.store.Get(t.Context(), f.target.ID, p.FullName()); !errors.Is(err, repository.ErrNotFound) {
		t.Fatal("corrupt source visible")
	}
	if err = f.store.FailRubyGemsReplicationItem(t.Context(), claim.Lease, 0, "transfer_failed"); err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.FinishRubyGemsReplication(t.Context(), claim.Lease); err != nil {
		t.Fatal(err)
	}
	f.module.distribution = nil
	w = replicationRequest(f.module.Preview(), "GET", "/api/v2/previews/rubygems/replication-jobs/"+j.ID, nil, "owned-admin", "")
	if w.Code != 503 {
		t.Fatal("missing explicit port", w.Code)
	}
}
