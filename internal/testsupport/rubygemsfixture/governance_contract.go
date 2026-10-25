package rubygemsfixture

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	protocol "github.com/ccsert/artifact-gateway/internal/protocol/rubygems"
	"sync"
	"testing"
	"time"

	"github.com/ccsert/artifact-gateway/internal/repository"
	d "github.com/ccsert/artifact-gateway/internal/rubygemspublication"
	"github.com/google/uuid"
)

func GovernanceContract(t *testing.T, store d.Store, repo repository.HostedRepository) {
	defer QuarantinePlatformContract(t, store, repo)
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	check := func(ctx context.Context, _ repository.RubyGemsGovernanceAuthority) error { return ctx.Err() }
	in := Session(t, repo.ID)
	s, _, err := store.Reserve(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	p, _, err := store.Commit(ctx, s.ID, in.Actor)
	if err != nil || p.Governance.Version != "1" {
		t.Fatalf("initial state %+v %v", p, err)
	}
	c := d.GovernanceChange{Identity: repository.RubyGemsGovernanceIdentity{Actor: in.Actor, Kind: "static_admin"}, RepositoryID: repo.ID, Name: p.Name, Version: "1.0", Platform: p.Platform, ExpectedDigest: p.Digest, ExpectedVersion: "1", Action: "yank", Actor: in.Actor, Reason: "Owned contract withdrawal"}
	if _, err := store.TransitionGovernance(ctx, c, nil); !errors.Is(err, d.ErrAccessDenied) {
		t.Fatalf("nil authority check accepted %v", err)
	}
	if _, err := store.TransitionGovernance(ctx, c, func(context.Context, repository.RubyGemsGovernanceAuthority) error { return d.ErrAccessDenied }); !errors.Is(err, d.ErrAccessDenied) {
		t.Fatalf("denied authority check accepted %v", err)
	}
	yanked, err := store.TransitionGovernance(ctx, c, check)
	if err != nil || !yanked.Governance.Yanked || yanked.Governance.Version != "2" {
		t.Fatalf("yank %+v %v", yanked, err)
	}
	if _, err = store.Get(ctx, repo.ID, p.FullName()); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("yanked download %v", err)
	}
	rows, err := store.List(ctx, repo.ID, 100)
	if err != nil || len(rows) != 0 {
		t.Fatalf("yanked index %+v %v", rows, err)
	}
	retained, err := store.GetRetained(ctx, repo.ID, p.Name, p.Version, p.Platform)
	if err != nil || retained.ObjectKey != p.ObjectKey || retained.Digest != p.Digest {
		t.Fatalf("retained closure %+v %v", retained, err)
	}
	if _, err = store.TransitionGovernance(ctx, c, check); !errors.Is(err, repository.ErrVersionConflict) {
		t.Fatalf("stale CAS %v", err)
	}
	c.ExpectedVersion = "2"
	replay, err := store.TransitionGovernance(ctx, c, check)
	if err != nil || replay.Governance != yanked.Governance {
		t.Fatalf("no-op changed state %+v %v", replay, err)
	}
	in.ID = uuid.NewString()
	if _, _, err = store.Reserve(ctx, in); !errors.Is(err, d.ErrConflict) {
		t.Fatalf("repush revived identity %v", err)
	}
	if _, _, err = store.Commit(ctx, p.SessionID, in.Actor); !errors.Is(err, d.ErrConflict) {
		t.Fatalf("commit replay bypassed yank %v", err)
	}
	capacity, err := store.Capacity(ctx, repo.ID)
	if err != nil || capacity.UsedBytes != p.Size || capacity.ObjectCount != 1 {
		t.Fatalf("retained quota %+v %v", capacity, err)
	}
	if _, err = store.SetQuota(ctx, repo.ID, in.Actor, p.Size-1); !errors.Is(err, repository.ErrQuotaExceeded) {
		t.Fatalf("retained quota reduction %v", err)
	}
	expired, err := store.Expire(ctx, time.Now().Add(24*time.Hour), 100)
	if err != nil || len(expired) != 0 {
		t.Fatalf("collector selected retained gem %+v %v", expired, err)
	}
	c.Action = "restore"
	restored, err := store.TransitionGovernance(ctx, c, check)
	if err != nil || restored.Governance.Yanked || restored.Governance.Version != "3" {
		t.Fatalf("restore %+v %v", restored, err)
	}
	if got, err := store.Get(ctx, repo.ID, p.FullName()); err != nil || got.Digest != p.Digest {
		t.Fatalf("restored original %+v %v", got, err)
	}
	// Each action changes only its own overlay; canonical identity and retained
	// capacity remain stable throughout all four yank/quarantine combinations.
	c.ExpectedVersion = "3"
	for _, step := range []struct {
		action, version     string
		yanked, quarantined bool
	}{
		{"quarantine", "4", false, true}, {"yank", "5", true, true},
		{"release", "6", true, false}, {"quarantine", "7", true, true},
		{"restore", "8", false, true}, {"release", "9", false, false},
	} {
		c.Action = step.action
		got, err := store.TransitionGovernance(ctx, c, check)
		if err != nil || got.Governance.Yanked != step.yanked || got.Governance.Quarantined != step.quarantined || got.Governance.Version != step.version || got.Digest != p.Digest || got.ObjectKey != p.ObjectKey {
			t.Fatalf("independent %s: %+v %v", step.action, got, err)
		}
		c.ExpectedVersion = step.version
		same, err := store.TransitionGovernance(ctx, c, check)
		if err != nil || same.Governance != got.Governance {
			t.Fatalf("no-op %s %+v %v", step.action, same, err)
		}
		if _, err = store.TransitionGovernance(ctx, c, nil); !errors.Is(err, d.ErrAccessDenied) {
			t.Fatalf("no-op bypassed authority %v", err)
		}
		if step.quarantined {
			if _, _, err = store.Reserve(ctx, in); !errors.Is(err, d.ErrConflict) {
				t.Fatalf("push bypassed quarantine: %v", err)
			}
			if _, _, err = store.Commit(ctx, p.SessionID, in.Actor); !errors.Is(err, d.ErrConflict) {
				t.Fatalf("commit bypassed quarantine: %v", err)
			}
		}
	}
	policy, err := store.GetReadPolicy(ctx, repo.ID)
	if err != nil || policy.Version != "1" || policy.Enabled {
		t.Fatalf("default policy %+v %v", policy, err)
	}
	pc := d.PolicyChange{RepositoryID: repo.ID, Identity: c.Identity, Actor: c.Actor, ExpectedVersion: "1", Enabled: true}
	if _, err = store.ReplaceReadPolicy(ctx, pc, nil); !errors.Is(err, d.ErrAccessDenied) {
		t.Fatalf("policy bypassed authority: %v", err)
	}
	policy, err = store.ReplaceReadPolicy(ctx, pc, check)
	if err != nil || policy.Version != "2" || !policy.Enabled {
		t.Fatalf("policy %+v %v", policy, err)
	}
	if _, err = store.ReplaceReadPolicy(ctx, pc, check); !errors.Is(err, repository.ErrVersionConflict) {
		t.Fatalf("policy stale CAS %v", err)
	}
	pc.ExpectedVersion = "2"
	if same, err := store.ReplaceReadPolicy(ctx, pc, check); err != nil || same != policy {
		t.Fatalf("policy no-op %+v %v", same, err)
	}
	if _, err = store.ReplaceReadPolicy(ctx, pc, nil); !errors.Is(err, d.ErrAccessDenied) {
		t.Fatalf("policy no-op bypassed authority %v", err)
	}
	for _, enabled := range []bool{false, true} {
		pc.Enabled = enabled
		checks := 0
		_, err = store.ReplaceReadPolicy(ctx, pc, func(context.Context, repository.RubyGemsGovernanceAuthority) error {
			checks++
			if checks > 1 {
				return d.ErrAccessDenied
			}
			return nil
		})
		if !errors.Is(err, d.ErrAccessDenied) {
			t.Fatalf("policy final authority (enabled=%t): checks=%d, error=%v", enabled, checks, err)
		}
		if got, err := store.GetReadPolicy(ctx, repo.ID); err != nil || got != policy {
			t.Fatalf("policy final authority rollback: %+v, %v", got, err)
		}
	}
	c.Action = "yank"
	c.ExpectedVersion = "9"
	var wg sync.WaitGroup
	errorsCh := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := store.TransitionGovernance(ctx, c, check); errorsCh <- err }()
	}
	wg.Wait()
	close(errorsCh)
	success := 0
	for err := range errorsCh {
		if err == nil {
			success++
		} else if !errors.Is(err, repository.ErrVersionConflict) {
			t.Fatal(err)
		}
	}
	if success != 1 {
		t.Fatalf("concurrent CAS accepted %d writers", success)
	}
}

func QuarantinePlatformContract(t *testing.T, store d.Store, repo repository.HostedRepository) {
	t.Helper()
	ctx := t.Context()
	original := Session(t, repo.ID)
	original.Name = "variant"
	original.MetadataYAML = bytes.Replace(original.MetadataYAML, []byte("name: contract"), []byte("name: variant"), 1)
	for _, platform := range []string{"ruby", "java"} {
		in := original
		in.ID, in.Platform = uuid.NewString(), platform
		in.MetadataYAML = bytes.Replace(in.MetadataYAML, []byte("platform: ruby"), []byte("platform: "+platform), 1)
		meta, err := protocol.ParseMetadata(in.MetadataYAML)
		if err != nil {
			t.Fatal(err)
		}
		in.MetadataJSON, _ = json.Marshal(meta)
		session, _, err := store.Reserve(ctx, in)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err = store.Commit(ctx, session.ID, in.Actor); err != nil {
			t.Fatal(err)
		}
	}
	check := func(ctx context.Context, _ repository.RubyGemsGovernanceAuthority) error { return ctx.Err() }
	c := d.GovernanceChange{RepositoryID: repo.ID, Name: "variant", Version: "1.0", Platform: "ruby", ExpectedDigest: original.Digest, ExpectedVersion: "1", Action: "quarantine", Actor: original.Actor, Reason: "Owned platform quarantine", Identity: repository.RubyGemsGovernanceIdentity{Actor: original.Actor, Kind: "static_admin"}}
	got, err := store.TransitionGovernance(ctx, c, check)
	if err != nil || !got.Governance.Quarantined || got.Governance.Yanked {
		t.Fatal(got, err)
	}
	java, err := store.GetRetained(ctx, repo.ID, "variant", "1.0.0", "java")
	if err != nil || java.Governance.Quarantined || java.Governance.Version != "1" {
		t.Fatal("quarantine widened platform", java, err)
	}
}
