package rubygemsfixture

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	protocol "github.com/ccsert/artifact-gateway/internal/protocol/rubygems"
	"github.com/ccsert/artifact-gateway/internal/repository"
	dist "github.com/ccsert/artifact-gateway/internal/rubygemsdistribution"
	domain "github.com/ccsert/artifact-gateway/internal/rubygemspublication"
	"github.com/google/uuid"
)

type ReplicationStore interface {
	domain.Store
	dist.Store
}

func replicationPublication(t *testing.T, s domain.Store, repo, name, platform string) domain.Publication {
	t.Helper()
	body := Build(t, YAML(name, "1.0.0", platform, "unchanged metadata", Dependency("leaf", "runtime")), "lib/owned.rb")
	in, err := protocol.Inspect(bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(body)
	session, _, err := s.Reserve(t.Context(), domain.Session{ID: uuid.NewString(), RepositoryID: repo, Name: name, Version: "1.0.0", Platform: platform, Digest: "sha256:" + hex.EncodeToString(sum[:]), Size: int64(len(body)), Actor: "operator", MetadataJSON: in.MetadataJSON, MetadataYAML: in.MetadataYAML, ExpiresAt: time.Now().UTC().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	p, _, err := s.Commit(t.Context(), session.ID, "operator")
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func replicationStage(t *testing.T, s ReplicationStore, j dist.Job, index int, p domain.Publication, check dist.Check) domain.Session {
	t.Helper()
	session, _, err := s.Reserve(t.Context(), domain.Session{ID: uuid.NewString(), RepositoryID: j.TargetRepositoryID, Name: p.Name, Version: p.Version, Platform: p.Platform, Digest: p.Digest, Size: p.Size, Actor: j.Actor, MetadataJSON: p.MetadataJSON, MetadataYAML: p.MetadataYAML, ExpiresAt: time.Now().UTC().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.BindRubyGemsReplicationSession(t.Context(), j.Lease, index, session.ID, check); err != nil {
		t.Fatal(err)
	}
	return session
}
func ReplicationStoreContract(t *testing.T, s ReplicationStore, source, target repository.HostedRepository) {
	t.Helper()
	ctx := t.Context()
	check := func(ctx context.Context, a dist.Authority) error {
		if a.Subject.Actor != "operator" || !a.Active || a.Source.ID != source.ID || a.Target.ID != target.ID {
			return dist.ErrAccessDenied
		}
		return ctx.Err()
	}
	job := func(pubs ...domain.Publication) dist.Job {
		t.Helper()
		p := dist.Plan{SourceRepositoryID: source.ID, TargetRepositoryID: target.ID}
		for _, v := range pubs {
			p.Items = append(p.Items, dist.Identity{Name: v.Name, Version: v.Version, Platform: v.Platform, Digest: v.Digest, Size: v.Size, SourceGovernanceVersion: v.Governance.Version})
		}
		j, err := dist.NewJob(p, dist.Subject{Actor: "operator", Kind: "static_admin"}, uuid.NewString())
		if err != nil {
			t.Fatal(err)
		}
		return j
	}
	deny := func(context.Context, dist.Authority) error { return dist.ErrAccessDenied }
	p := replicationPublication(t, s, source.ID, "distribution", "ruby")
	in := job(p)
	if _, _, err := s.CreateRubyGemsReplication(ctx, in, nil); !errors.Is(err, dist.ErrAccessDenied) {
		t.Fatalf("missing authority %v", err)
	}
	j, replay, err := s.CreateRubyGemsReplication(ctx, in, check)
	if err != nil || replay || j.State != "queued" {
		t.Fatalf("create %+v %t %v", j, replay, err)
	}
	same, replay, err := s.CreateRubyGemsReplication(ctx, in, check)
	if err != nil || !replay || same.Version != j.Version {
		t.Fatalf("replay %+v %v", same, err)
	}
	changed := dist.Clone(in)
	changed.Items[0].Size++
	plan := dist.Plan{SourceRepositoryID: source.ID, TargetRepositoryID: target.ID, Items: []dist.Identity{changed.Items[0].Identity}}
	_, changed.PlanDigest, _ = dist.CanonicalPlan(plan)
	if _, _, err = s.CreateRubyGemsReplication(ctx, changed, check); !errors.Is(err, dist.ErrConflict) {
		t.Fatalf("changed plan %v", err)
	}
	if _, _, err = s.CreateRubyGemsReplication(ctx, in, deny); !errors.Is(err, dist.ErrAccessDenied) {
		t.Fatalf("replay authority %v", err)
	}
	var wg sync.WaitGroup
	claims := make(chan dist.Job, 8)
	failures := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, e := s.ClaimRubyGemsReplication(ctx, uuid.NewString(), time.Minute)
			if e == nil {
				claims <- c
			} else if !errors.Is(e, dist.ErrNotFound) {
				failures <- e
			}
		}()
	}
	wg.Wait()
	close(failures)
	for e := range failures {
		t.Fatal(e)
	}
	if len(claims) != 1 {
		t.Fatalf("owners %d", len(claims))
	}
	claim := <-claims
	session := replicationStage(t, s, claim, 0, p, check)
	stale := claim.Lease
	stale.Token = uuid.NewString()
	if _, err = s.CompleteRubyGemsReplicationItem(ctx, stale, 0, dist.Commit{SessionID: session.ID}, check); !errors.Is(err, dist.ErrLeaseLost) {
		t.Fatalf("stale complete %v", err)
	}
	if _, err = s.CompleteRubyGemsReplicationItem(ctx, claim.Lease, 0, dist.Commit{SessionID: session.ID}, deny); !errors.Is(err, dist.ErrAccessDenied) {
		t.Fatalf("denied complete %v", err)
	}
	if _, err = s.Get(ctx, target.ID, p.FullName()); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("denied publication visible %v", err)
	}
	complete, err := s.CompleteRubyGemsReplicationItem(ctx, claim.Lease, 0, dist.Commit{SessionID: session.ID}, check)
	if err != nil || complete.Items[0].State != "succeeded" || complete.Items[0].Result != "published" {
		t.Fatalf("complete %+v %v", complete, err)
	}
	got, err := s.Get(ctx, target.ID, p.FullName())
	if err != nil || got.ObjectKey == p.ObjectKey || got.Digest != p.Digest || !bytes.Equal(got.MetadataYAML, p.MetadataYAML) {
		t.Fatalf("target %+v %v", got, err)
	}
	if _, err = s.CompleteRubyGemsReplicationItem(ctx, claim.Lease, 0, dist.Commit{SessionID: session.ID}, check); err != nil {
		t.Fatal(err)
	}
	done, err := s.FinishRubyGemsReplication(ctx, claim.Lease)
	if err != nil || done.State != "succeeded" || done.Items[0].Attempts != 1 {
		t.Fatalf("finish %+v %v", done, err)
	}
	if _, err = s.RenewRubyGemsReplication(ctx, claim.Lease, time.Minute); !errors.Is(err, dist.ErrLeaseLost) {
		t.Fatalf("terminal renewed %v", err)
	}
	// Partial failure preserves a completed platform variant and retries only the
	// selected failed item with CAS and an idempotent retry key.
	ruby := replicationPublication(t, s, source.ID, "variants", "ruby")
	java := replicationPublication(t, s, source.ID, "variants", "java")
	in = job(ruby, java)
	if _, _, err = s.CreateRubyGemsReplication(ctx, in, check); err != nil {
		t.Fatal(err)
	}
	claim, err = s.ClaimRubyGemsReplication(ctx, "partial", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	first := java
	if claim.Items[0].Platform == "ruby" {
		first = ruby
	}
	session = replicationStage(t, s, claim, 0, first, check)
	if _, err = s.CompleteRubyGemsReplicationItem(ctx, claim.Lease, 0, dist.Commit{SessionID: session.ID}, check); err != nil {
		t.Fatal(err)
	}
	if err = s.FailRubyGemsReplicationItem(ctx, claim.Lease, 1, "transfer_failed"); err != nil {
		t.Fatal(err)
	}
	done, err = s.FinishRubyGemsReplication(ctx, claim.Lease)
	if err != nil || done.State != "failed" {
		t.Fatalf("partial %+v %v", done, err)
	}
	if _, _, err = s.RetryRubyGemsReplication(ctx, done.ID, "1", []int{1}, "stale", check); !errors.Is(err, dist.ErrConflict) {
		t.Fatalf("stale retry %v", err)
	}
	if _, _, err = s.RetryRubyGemsReplication(ctx, done.ID, done.Version, []int{0}, "succeeded", check); !errors.Is(err, dist.ErrConflict) {
		t.Fatalf("successful retry %v", err)
	}
	retried, replay, err := s.RetryRubyGemsReplication(ctx, done.ID, done.Version, []int{1}, "retry", check)
	if err != nil || replay || retried.Items[0].TargetSessionID != session.ID {
		t.Fatalf("retry %+v %v", retried, err)
	}
	if _, replay, err = s.RetryRubyGemsReplication(ctx, done.ID, done.Version, []int{1}, "retry", check); err != nil || !replay {
		t.Fatalf("retry replay %t %v", replay, err)
	}
	claim, err = s.ClaimRubyGemsReplication(ctx, "retry", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	second := ruby
	if claim.Items[1].Platform == "java" {
		second = java
	}
	session = replicationStage(t, s, claim, 1, second, check)
	if _, err = s.CompleteRubyGemsReplicationItem(ctx, claim.Lease, 1, dist.Commit{SessionID: session.ID}, check); err != nil {
		t.Fatal(err)
	}
	if done, err = s.FinishRubyGemsReplication(ctx, claim.Lease); err != nil || done.State != "succeeded" || done.Items[0].Attempts != 1 || done.Items[1].Attempts != 2 {
		t.Fatalf("retry finish %+v %v", done, err)
	}
	// A hidden target is retained as an immutable conflict, irrespective of read policy.
	for _, action := range []string{"yank", "quarantine"} {
		name := "blocked-" + action
		p := replicationPublication(t, s, source.ID, name, "ruby")
		targetPub := replicationPublication(t, s, target.ID, name, "ruby")
		_, err = s.TransitionGovernance(ctx, domain.GovernanceChange{Identity: repository.RubyGemsGovernanceIdentity{Actor: "operator", Kind: "static_admin"}, RepositoryID: target.ID, Name: name, Version: p.Version, Platform: p.Platform, ExpectedDigest: targetPub.Digest, ExpectedVersion: "1", Actor: "operator", Action: action, Reason: "owned target restriction"}, func(ctx context.Context, _ repository.RubyGemsGovernanceAuthority) error { return ctx.Err() })
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err = s.CreateRubyGemsReplication(ctx, job(p), check); !errors.Is(err, dist.ErrTargetBlocked) {
			t.Fatalf("hidden target %s %v", action, err)
		}
	}
	// Source governance changing after checkpoint invalidates the exact plan.
	for _, action := range []string{"yank", "quarantine"} {
		p := replicationPublication(t, s, source.ID, "source-"+action, "ruby")
		in = job(p)
		if _, _, err = s.CreateRubyGemsReplication(ctx, in, check); err != nil {
			t.Fatal(err)
		}
		claim, err = s.ClaimRubyGemsReplication(ctx, "governed", time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		session = replicationStage(t, s, claim, 0, p, check)
		_, err = s.TransitionGovernance(ctx, domain.GovernanceChange{Identity: repository.RubyGemsGovernanceIdentity{Actor: "operator", Kind: "static_admin"}, RepositoryID: source.ID, Name: p.Name, Version: p.Version, Platform: p.Platform, ExpectedDigest: p.Digest, ExpectedVersion: "1", Actor: "operator", Action: action, Reason: "owned source restriction"}, func(ctx context.Context, _ repository.RubyGemsGovernanceAuthority) error { return ctx.Err() })
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.CompleteRubyGemsReplicationItem(ctx, claim.Lease, 0, dist.Commit{SessionID: session.ID}, check); !errors.Is(err, dist.ErrSourceChanged) {
			t.Fatalf("changed source %s %v", action, err)
		}
		if _, err = s.Get(ctx, target.ID, p.FullName()); !errors.Is(err, repository.ErrNotFound) {
			t.Fatalf("changed source published %v", err)
		}
		if err = s.FailRubyGemsReplicationItem(ctx, claim.Lease, 0, "source_changed"); err != nil {
			t.Fatal(err)
		}
		if _, err = s.FinishRubyGemsReplication(ctx, claim.Lease); err != nil {
			t.Fatal(err)
		}
	}
	// Lease expiry allows one new owner; the old owner cannot publish or checkpoint.
	p = replicationPublication(t, s, source.ID, "restart", "ruby")
	in = job(p)
	if _, _, err = s.CreateRubyGemsReplication(ctx, in, check); err != nil {
		t.Fatal(err)
	}
	claim, err = s.ClaimRubyGemsReplication(ctx, "old", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	session = replicationStage(t, s, claim, 0, p, check)
	timer := time.NewTimer(time.Until(claim.Lease.Until) + 20*time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	case <-timer.C:
	}
	renewed, err := s.ClaimRubyGemsReplication(ctx, "new", time.Minute)
	if err != nil || renewed.Lease.Fence <= claim.Lease.Fence || renewed.Items[0].TargetSessionID != session.ID {
		t.Fatalf("handoff %+v %v", renewed, err)
	}
	if _, err = s.CompleteRubyGemsReplicationItem(ctx, claim.Lease, 0, dist.Commit{SessionID: session.ID}, check); !errors.Is(err, dist.ErrLeaseLost) {
		t.Fatalf("old owner %v", err)
	}
	if _, err = s.BindRubyGemsReplicationSession(ctx, renewed.Lease, 0, session.ID, check); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CompleteRubyGemsReplicationItem(ctx, renewed.Lease, 0, dist.Commit{SessionID: session.ID}, check); err != nil {
		t.Fatal(err)
	}
	if _, err = s.FinishRubyGemsReplication(ctx, renewed.Lease); err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err = s.GetRubyGemsReplication(canceled, in.ID); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel ignored %v", err)
	}
	if _, _, err = s.RetryRubyGemsReplication(ctx, in.ID, "1", []int{0}, strings.Repeat("x", 201), check); !errors.Is(err, dist.ErrInvalid) {
		t.Fatalf("unbounded key %v", err)
	}
}
