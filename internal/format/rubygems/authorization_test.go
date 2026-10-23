package rubygems

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/ccsert/artifact-gateway/internal/authorization"
	"github.com/ccsert/artifact-gateway/internal/objectstore"
	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

type blockingObjects struct {
	objectstore.Store
	ready, release chan struct{}
}

func (b *blockingObjects) PutVerifiedReader(ctx context.Context, key string, r io.Reader, size int64, digest string) error {
	if e := b.Store.PutVerifiedReader(ctx, key, r, size, digest); e != nil {
		return e
	}
	close(b.ready)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-b.release:
		return nil
	}
}
func TestRubyGemsPublicationRechecksRevocationGrantAccountAndExpiryBeforeCommit(t *testing.T) {
	for _, change := range []string{"grant-revoked", "key-revoked", "account-disabled", "key-expired"} {
		t.Run(change, func(t *testing.T) {
			f := setup(t)
			token := "owned-publisher"
			key := repository.APIKey{ID: uuid.NewString(), Name: "owned-key", SecretHash: authorization.HashAPIKey(token), Roles: []string{"member"}}
			actor := "api-key:" + key.ID
			var account repository.ServiceAccount
			if change == "account-disabled" {
				var e error
				account, e = f.repos.CreateServiceAccount(t.Context(), repository.ServiceAccount{ID: uuid.NewString(), Name: "owned-uploader"})
				if e != nil {
					t.Fatal(e)
				}
				key.ServiceAccountID = account.ID
				actor = "service-account:" + account.ID
				if _, e = f.repos.CreateServiceAccountCredential(t.Context(), key); e != nil {
					t.Fatal(e)
				}
			} else {
				if _, e := f.repos.CreateAPIKey(t.Context(), key); e != nil {
					t.Fatal(e)
				}
			}
			if _, e := f.repos.ReplaceRepositoryGrants(t.Context(), f.repo.ID, []repository.RepositoryGrant{{Principal: actor, Scopes: []string{"repositories:write"}, ResourcePrefix: "owned"}}, "1"); e != nil {
				t.Fatal(e)
			}
			blocked := &blockingObjects{Store: f.objects, ready: make(chan struct{}), release: make(chan struct{})}
			o := options(f.repos, f.store, blocked, f.repos)
			o.Authenticator.APIKeys = f.repos
			o.Authenticator.ServiceAccounts = f.repos
			o.Authorizer.Legacy = o.Authenticator
			module, e := New(o)
			if e != nil {
				t.Fatal(e)
			}
			body := gem(t, "owned", "1.0.0", "ruby", "fixture")
			result := make(chan int, 1)
			go func() { result <- request(module, "POST", "/rubygems/owned-gems/api/v1/gems", body, token).Code }()
			select {
			case <-blocked.ready:
			case <-time.After(5 * time.Second):
				t.Fatal("upload did not reach owned deterministic object boundary")
			}
			switch change {
			case "grant-revoked":
				_, e = f.repos.ReplaceRepositoryGrants(t.Context(), f.repo.ID, []repository.RepositoryGrant{}, "2")
			case "key-revoked":
				_, e = f.repos.RevokeAPIKey(t.Context(), key.ID)
			case "account-disabled":
				state := repository.ServiceAccountDisabled
				_, e = f.repos.UpdateServiceAccount(t.Context(), repository.ServiceAccountUpdate{ID: account.ID, State: &state}, account.Version)
			case "key-expired":
				expired := time.Now().Add(-time.Second)
				key.ExpiresAt = &expired
				_, e = f.repos.CreateAPIKey(t.Context(), key)
			}
			close(blocked.release)
			if e != nil {
				t.Fatal(e)
			}
			code := <-result
			if code != 401 && code != 403 {
				t.Fatalf("revoked publisher committed: %d", code)
			}
			rows, e := f.store.List(t.Context(), f.repo.ID, 10)
			if e != nil || len(rows) != 0 {
				t.Fatalf("revoked upload visible %+v %v", rows, e)
			}
			audits, e := f.repos.ListAudits(t.Context(), repository.AuditQuery{Operation: "rubygems.publish", Limit: 10})
			if e != nil || len(audits) != 0 {
				t.Fatalf("revoked upload success audit %+v %v", audits, e)
			}
		})
	}
}
