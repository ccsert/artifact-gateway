package rubygemspublication_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ccsert/artifact-gateway/internal/repository"
	d "github.com/ccsert/artifact-gateway/internal/rubygemspublication"
	"github.com/google/uuid"
)

type readPolicyAuditor func(context.Context, repository.AuditRecord) error

func (audit readPolicyAuditor) RecordAudit(ctx context.Context, record repository.AuditRecord) error {
	return audit(ctx, record)
}

func TestRubyGemsMemoryReadPolicyRechecksAuthorityAfterAudit(t *testing.T) {
	for _, mode := range []string{"canceled", "identity-expired", "credential-expired", "denied"} {
		t.Run(mode, func(t *testing.T) {
			repos := repository.NewMemoryStore()
			repo, err := repos.CreateHostedRepository(t.Context(), repository.HostedRepository{
				ID: uuid.NewString(), Name: "ruby-policy-" + uuid.NewString(),
				Format: repository.FormatRubyGems, Type: repository.RepositoryTypeHosted,
			})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			identity := repository.RubyGemsGovernanceIdentity{Actor: "owned-admin", Kind: "static_admin"}
			wantErr := d.ErrAccessDenied
			denied := false
			afterAudit := func() { denied = true }
			switch mode {
			case "canceled":
				afterAudit, wantErr = cancel, context.Canceled
			case "identity-expired", "credential-expired":
				expiry := time.Now().Add(250 * time.Millisecond)
				identity.ValidUntil = expiry
				if mode == "credential-expired" {
					key, err := repos.CreateAPIKey(t.Context(), repository.APIKey{
						ID: uuid.NewString(), Name: "owned-policy-key", Roles: []string{"admin"}, ExpiresAt: &expiry,
					})
					if err != nil {
						t.Fatal(err)
					}
					identity = repository.RubyGemsGovernanceIdentity{Actor: "api-key:" + key.ID, Kind: "api_key", ValidUntil: expiry.Add(time.Hour)}
				}
				afterAudit = func() { time.Sleep(time.Until(expiry)) }
			}
			auditCalls := 0
			store := d.NewMemory(repos, readPolicyAuditor(func(ctx context.Context, record repository.AuditRecord) error {
				auditCalls++
				// Delegate to the same store so the audit is staged under its mutex.
				if err := repos.RecordAudit(ctx, record); err != nil {
					return err
				}
				afterAudit()
				return nil
			}))
			_, err = store.ReplaceReadPolicy(ctx, d.PolicyChange{
				RepositoryID: repo.ID, Identity: identity, Actor: identity.Actor, ExpectedVersion: "1", Enabled: true,
			}, func(context.Context, repository.RubyGemsGovernanceAuthority) error {
				if denied {
					return d.ErrAccessDenied
				}
				return nil
			})
			if auditCalls != 1 {
				t.Fatalf("audit boundary was not reached: calls = %d, error = %v", auditCalls, err)
			}
			if !errors.Is(err, wantErr) {
				t.Errorf("policy change = %v; want %v", err, wantErr)
			}
			policy, err := store.GetReadPolicy(t.Context(), repo.ID)
			if err != nil || policy != repository.DefaultRepositoryQuarantineReadPolicy() {
				t.Errorf("rejected policy committed: %+v, %v", policy, err)
			}
			audits, err := repos.ListAudits(t.Context(), repository.AuditQuery{Operation: "rubygems.quarantine_read_policy.configure"})
			if err != nil || len(audits) != 0 {
				t.Errorf("rejected policy audit committed: %+v, %v", audits, err)
			}
		})
	}
}
