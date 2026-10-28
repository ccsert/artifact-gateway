package rubygemspublication_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/ccsert/artifact-gateway/internal/authorization"
	"github.com/ccsert/artifact-gateway/internal/repository"
	dist "github.com/ccsert/artifact-gateway/internal/rubygemsdistribution"
	domain "github.com/ccsert/artifact-gateway/internal/rubygemspublication"
	f "github.com/ccsert/artifact-gateway/internal/testsupport/rubygemsfixture"
	"github.com/google/uuid"
)

type distributionAudit func(context.Context, repository.AuditRecord) error

func (a distributionAudit) RecordAudit(c context.Context, r repository.AuditRecord) error {
	return a(c, r)
}
func TestRubyGemsMemoryReplicationAuditAtomicityCancellationAndExpiry(t *testing.T) {
	for _, mode := range []string{"audit-failure", "cancel", "expiry", "waiting-cancel"} {
		t.Run(mode, func(t *testing.T) {
			repos := repository.NewMemoryStore()
			var source, target repository.HostedRepository
			for _, r := range []*repository.HostedRepository{&source, &target} {
				v, err := repos.CreateHostedRepository(t.Context(), repository.HostedRepository{ID: uuid.NewString(), Name: "atomic-" + uuid.NewString(), Format: repository.FormatRubyGems, Type: repository.RepositoryTypeHosted})
				if err != nil {
					t.Fatal(err)
				}
				*r = v
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			expiry := time.Now().UTC().Add(time.Second)
			started, resume := make(chan struct{}), make(chan struct{})
			var once sync.Once
			audit := distributionAudit(func(c context.Context, r repository.AuditRecord) error {
				if err := repos.RecordAudit(c, r); err != nil {
					return err
				}
				if r.Operation == "rubygems.replication.item" && r.Status == 200 {
					switch mode {
					case "audit-failure":
						return errors.New("owned audit failure")
					case "cancel":
						cancel()
					case "expiry":
						time.Sleep(time.Until(expiry) + 20*time.Millisecond)
					case "waiting-cancel":
						once.Do(func() { close(started) })
						<-resume
					}
				}
				return nil
			})
			store := domain.NewMemory(repos, audit)
			in := f.Session(t, source.ID)
			session, _, err := store.Reserve(ctx, in)
			if err != nil {
				t.Fatal(err)
			}
			p, _, err := store.Commit(ctx, session.ID, in.Actor)
			if err != nil {
				t.Fatal(err)
			}
			subject := dist.Subject{Actor: "operator", Kind: "static_admin"}
			if mode == "expiry" {
				key, err := repos.CreateAPIKey(ctx, repository.APIKey{ID: uuid.NewString(), Name: "owned", SecretHash: authorization.HashAPIKey(uuid.NewString()), Roles: []string{}, ExpiresAt: &expiry})
				if err != nil {
					t.Fatal(err)
				}
				subject = dist.Subject{Actor: "api-key:" + key.ID, Kind: "api_key"}
			}
			job, err := dist.NewJob(dist.Plan{SourceRepositoryID: source.ID, TargetRepositoryID: target.ID, Items: []dist.Identity{{Name: p.Name, Version: p.Version, Platform: p.Platform, Digest: p.Digest, Size: p.Size, SourceGovernanceVersion: p.Governance.Version}}}, subject, uuid.NewString())
			if err != nil {
				t.Fatal(err)
			}
			check := func(c context.Context, _ dist.Authority) error { return c.Err() }
			if _, _, err = store.CreateRubyGemsReplication(ctx, job, check); err != nil {
				t.Fatal(err)
			}
			claim, err := store.ClaimRubyGemsReplication(ctx, "owned", time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			in.ID = uuid.NewString()
			in.RepositoryID = target.ID
			in.Actor = job.Actor
			session, _, err = store.Reserve(ctx, in)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = store.BindRubyGemsReplicationSession(ctx, claim.Lease, 0, session.ID, check); err != nil {
				t.Fatal(err)
			}
			complete := func() error {
				_, e := store.CompleteRubyGemsReplicationItem(ctx, claim.Lease, 0, dist.Commit{SessionID: session.ID}, check)
				return e
			}
			if mode == "waiting-cancel" {
				result := make(chan error, 1)
				go func() { result <- complete() }()
				select {
				case <-started:
				case <-time.After(time.Second):
					t.Fatal("audit hook not reached")
				}
				waiting, stop := context.WithCancel(t.Context())
				stop()
				if _, err = store.GetRubyGemsReplication(waiting, job.ID); !errors.Is(err, context.Canceled) {
					t.Fatalf("domain waiter did not cancel %v", err)
				}
				committed := false
				if err = repos.ChangeRubyGemsDistribution(waiting, source.ID, target.ID, nil, func(context.Context, repository.RubyGemsGovernanceAuthority, repository.RubyGemsGovernanceAuthority) error {
					return nil
				}, func(repository.RubyGemsGovernanceAuthority, repository.RubyGemsGovernanceAuthority) error { return nil }, func() { committed = true }); !errors.Is(err, context.Canceled) || committed {
					t.Fatalf("repository waiter published %v %v", committed, err)
				}
				close(resume)
				if err = <-result; err != nil {
					t.Fatal(err)
				}
				return
			}
			err = complete()
			if err == nil {
				t.Fatal("failed transaction committed")
			}
			if mode == "cancel" && !errors.Is(err, context.Canceled) || mode == "expiry" && !errors.Is(err, dist.ErrAccessDenied) {
				t.Fatalf("wrong final rejection %v", err)
			}
			if _, err = store.Get(t.Context(), target.ID, p.FullName()); !errors.Is(err, repository.ErrNotFound) {
				t.Fatal("failed transaction visible", err)
			}
			job, err = store.GetRubyGemsReplication(t.Context(), job.ID)
			if err != nil || job.Items[0].State != "running" {
				t.Fatalf("checkpoint escaped %+v %v", job, err)
			}
			for _, op := range []string{"rubygems.publish", "rubygems.replication.item"} {
				audits, err := repos.ListAudits(t.Context(), repository.AuditQuery{Repository: target.Name, Operation: op, Limit: 100})
				if err != nil || len(audits) != 0 {
					t.Fatalf("audit escaped rollback %s %+v %v", op, audits, err)
				}
			}
		})
	}
}

func TestRubyGemsMemoryReplicationRejectsPublicationExpiryAfterDelegatedAudit(t *testing.T) {
	repos := repository.NewMemoryStore()
	var source, target repository.HostedRepository
	for _, r := range []*repository.HostedRepository{&source, &target} {
		v, err := repos.CreateHostedRepository(t.Context(), repository.HostedRepository{ID: uuid.NewString(), Name: "session-expiry-" + uuid.NewString(), Format: repository.FormatRubyGems, Type: repository.RepositoryTypeHosted})
		if err != nil {
			t.Fatal(err)
		}
		*r = v
	}
	var until time.Time
	entered := false
	auditor := distributionAudit(func(ctx context.Context, record repository.AuditRecord) error {
		if err := repos.RecordAudit(ctx, record); err != nil {
			return err
		}
		if record.Operation == "rubygems.replication.item" && record.Status == 200 {
			entered = true
			timer := time.NewTimer(time.Until(until) + 30*time.Millisecond)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-timer.C:
			}
		}
		return nil
	})
	store := domain.NewMemory(repos, auditor)
	f.ReplicationPublicationExpiryContract(t, store, repos, source, target, func(expiry time.Time, complete func() error) error {
		until = expiry
		err := complete()
		if !entered {
			t.Fatal("delegated audit was never entered")
		}
		return err
	})
}
