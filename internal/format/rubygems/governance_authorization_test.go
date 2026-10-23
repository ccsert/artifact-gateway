package rubygems

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/ccsert/artifact-gateway/internal/authorization"
	"github.com/ccsert/artifact-gateway/internal/objectstore"
	"github.com/ccsert/artifact-gateway/internal/repository"
	domain "github.com/ccsert/artifact-gateway/internal/rubygemspublication"
	"github.com/google/uuid"
)

type governanceLockGate struct {
	domain.Store
	ready, resume chan struct{}
}

func (g governanceLockGate) Lock(ctx context.Context, repo, identity string) (context.Context, func(), error) {
	ctx, release, err := g.Store.Lock(ctx, repo, identity)
	if err != nil {
		return ctx, release, err
	}
	close(g.ready)
	select {
	case <-g.resume:
		return ctx, release, nil
	case <-ctx.Done():
		release()
		return ctx, func() {}, ctx.Err()
	}
}

type governanceReadGate struct {
	objectstore.Store
	ready, resume chan struct{}
}

func (g governanceReadGate) Open(ctx context.Context, key string) (io.ReadCloser, int64, error) {
	r, n, err := g.Store.Open(ctx, key)
	if err != nil {
		return r, n, err
	}
	close(g.ready)
	select {
	case <-g.resume:
		return r, n, nil
	case <-ctx.Done():
		_ = r.Close()
		return nil, 0, ctx.Err()
	}
}

func governanceAuthority(t *testing.T, f fixture, change string) (Options, string, func()) {
	t.Helper()
	o := options(f.repos, f.store, f.objects, f.repos)
	o.Authenticator.APIKeys = f.repos
	o.Authenticator.ServiceAccounts = f.repos
	o.Authenticator.Users = f.repos
	token := "owned-current-credential"
	key := repository.APIKey{ID: uuid.NewString(), Name: "owned-current", SecretHash: authorization.HashAPIKey(token)}
	actor := "api-key:" + key.ID
	var account repository.ServiceAccount
	var user repository.User
	var err error
	switch change {
	case "account-disabled":
		account, err = f.repos.CreateServiceAccount(t.Context(), repository.ServiceAccount{ID: uuid.NewString(), Name: "owned-service"})
		if err == nil {
			key.ServiceAccountID = account.ID
			_, err = f.repos.CreateServiceAccountCredential(t.Context(), key)
			actor = "service-account:" + account.ID
		}
	case "user-disabled", "password-reset", "role-none":
		user, err = f.repos.CreateUser(t.Context(), repository.User{ID: uuid.NewString(), Name: "owned-user", Role: "member"})
		actor = "user:" + user.Name
		token = o.Authenticator.IssueUserSession(user.ID, user.SessionVersion)
	default:
		_, err = f.repos.CreateAPIKey(t.Context(), key)
	}
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.repos.ReplaceRepositoryGrants(t.Context(), f.repo.ID, []repository.RepositoryGrant{{Principal: actor, Scopes: []string{"repositories:admin"}, ResourcePrefix: "owned"}}, "1"); err != nil {
		t.Fatal(err)
	}
	o.Authorizer.Legacy = o.Authenticator
	revoke := func() {
		var err error
		switch change {
		case "grant-revoked":
			_, err = f.repos.ReplaceRepositoryGrants(t.Context(), f.repo.ID, []repository.RepositoryGrant{}, "2")
		case "key-revoked":
			_, err = f.repos.RevokeAPIKey(t.Context(), key.ID)
		case "key-expired":
			expiry := time.Now().Add(-time.Second)
			key.ExpiresAt = &expiry
			_, err = f.repos.CreateAPIKey(t.Context(), key)
		case "account-disabled":
			state := repository.ServiceAccountDisabled
			_, err = f.repos.UpdateServiceAccount(t.Context(), repository.ServiceAccountUpdate{ID: account.ID, State: &state}, account.Version)
		case "user-disabled":
			state := repository.UserDisabled
			_, err = f.repos.UpdateUser(t.Context(), repository.UserUpdate{ID: user.ID, State: &state}, user.Version)
		case "password-reset":
			_, err = f.repos.UpdateUserPassword(t.Context(), user.ID, "owned-unused-hash", user.Version, true)
		case "role-none":
			role := "none"
			_, err = f.repos.UpdateUser(t.Context(), repository.UserUpdate{ID: user.ID, Role: &role}, user.Version)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	return o, token, revoke
}

func TestRubyGemsGovernanceRequiresCurrentAuthorityAfterLockAndVerification(t *testing.T) {
	for _, boundary := range []string{"yank-lock", "restore-lock", "restore-bytes"} {
		for _, change := range []string{"key-revoked", "key-expired", "account-disabled", "user-disabled", "password-reset", "role-none", "grant-revoked"} {
			t.Run(boundary+"/"+change, func(t *testing.T) {
				f := setup(t)
				if w := push(f, gem(t, "owned", "1.0.0", "ruby", "fixture")); w.Code != 201 {
					t.Fatal(w.Code)
				}
				p, err := f.store.GetRetained(t.Context(), f.repo.ID, "owned", "1.0.0", "ruby")
				if err != nil {
					t.Fatal(err)
				}
				restore := boundary != "yank-lock"
				if restore {
					if w := yankRequest(f.module, f.repo.Name, "owned", "1.0.0", "", "owned-admin"); w.Code != 200 {
						t.Fatal(w.Code)
					}
				}
				o, token, revoke := governanceAuthority(t, f, change)
				ready, resume := make(chan struct{}), make(chan struct{})
				if boundary == "restore-bytes" {
					o.Objects = governanceReadGate{f.objects, ready, resume}
				} else {
					o.Store = governanceLockGate{f.store, ready, resume}
				}
				m, err := New(o)
				if err != nil {
					t.Fatal(err)
				}
				result := make(chan int, 1)
				go func() {
					if !restore {
						result <- yankRequest(m, f.repo.Name, "owned", "1.0.0", "", token).Code
						return
					}
					body, _ := json.Marshal(map[string]string{"expectedDigest": p.Digest, "expectedGovernanceVersion": "2", "action": "restore", "reason": "Owned current authority check"})
					result <- request(m.Preview(), "POST", governancePath(f.repo.ID, "owned", "1.0.0", "ruby"), body, "Bearer "+token).Code
				}()
				select {
				case <-ready:
				case code := <-result:
					t.Fatalf("did not reach authority boundary: %d", code)
				case <-time.After(5 * time.Second):
					t.Fatal("boundary timeout")
				}
				revoke()
				close(resume)
				select {
				case code := <-result:
					if code != http.StatusUnauthorized && code != http.StatusForbidden {
						t.Fatalf("stale authority committed %d", code)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("request not reaped")
				}
				got, err := f.store.GetRetained(t.Context(), f.repo.ID, "owned", "1.0.0", "ruby")
				if err != nil || got.Governance.Yanked != restore {
					t.Fatal(got, err)
				}
				op := "rubygems.yank"
				version := "1"
				if restore {
					op = "rubygems.restore"
					version = "2"
				}
				if got.Governance.Version != version {
					t.Fatal(got)
				}
				audits, err := f.repos.ListAudits(t.Context(), repository.AuditQuery{Operation: op, Limit: 10})
				if err != nil || len(audits) != 0 {
					t.Fatal(audits, err)
				}
			})
		}
	}
}

func TestRubyGemsGovernanceUsesRepositoryAdminAndExactName(t *testing.T) {
	f := setup(t)
	for _, name := range []string{"owned", "other"} {
		if w := push(f, gem(t, name, "1.0.0", "ruby", "fixture")); w.Code != 201 {
			t.Fatal(w.Code)
		}
	}
	o, token, _ := governanceAuthority(t, f, "grant-revoked")
	m, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	if w := request(m.Preview(), "GET", governancePath(f.repo.ID, "owned", "1.0.0", "ruby"), nil, "Bearer "+token); w.Code != 200 {
		t.Fatalf("repository-only admin key rejected %d", w.Code)
	}
	if w := request(m.Preview(), "GET", governancePath(f.repo.ID, "other", "1.0.0", "ruby"), nil, "Bearer "+token); w.Code != 403 {
		t.Fatalf("name grant widened %d", w.Code)
	}
	if w := yankRequest(m, f.repo.Name, "other", "1.0.0", "", token); w.Code != 403 {
		t.Fatal(w.Code)
	}
	if w := request(m.Preview(), "GET", governancePath(f.repo.ID, "owned", "1.0.0", "ruby"), nil, "Bearer owned-reader"); w.Code != 403 {
		t.Fatal(w.Code)
	}
}
