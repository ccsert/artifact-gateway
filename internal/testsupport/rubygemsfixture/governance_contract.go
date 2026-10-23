package rubygemsfixture

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/ccsert/artifact-gateway/internal/repository"
	d "github.com/ccsert/artifact-gateway/internal/rubygemspublication"
	"github.com/google/uuid"
)

func GovernanceContract(t *testing.T, store d.Store, repo repository.HostedRepository) {
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
	c.Action = "yank"
	c.ExpectedVersion = "3"
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
