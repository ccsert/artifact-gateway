package repository

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestMemoryCargoGroupOwnerConflictAndMemberChange(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	for _, name := range []string{"first", "second"} {
		if _, err := store.CreateHostedRepository(ctx, HostedRepository{ID: name, Name: name, Format: FormatCargo}); err != nil {
			t.Fatal(err)
		}
	}
	group, _, err := store.CreateHostedGroupIdempotently(ctx, HostedGroup{ID: "cargo-group", Name: "cargo-group", Format: FormatCargo,
		Members: []GroupMember{{RepositoryID: "first", Position: 0}, {RepositoryID: "second", Position: 1}}}, "tester", "create", "payload")
	if err != nil {
		t.Fatal(err)
	}
	makeVersion := func(source, checksum string, yanked bool) CargoGroupVersion {
		row := `{"name":"demo","vers":"1.0.0","deps":[],"cksum":"` + checksum + `","yanked":false}`
		if yanked {
			row = strings.Replace(row, `"yanked":false`, `"yanked":true`, 1)
		}
		return CargoGroupVersion{GroupID: group.ID, SourceRepositoryID: source, Name: "demo", Version: "1.0.0",
			Checksum: checksum, IndexRow: []byte(row)}
	}
	a := strings.Repeat("a", 64)
	b := strings.Repeat("b", 64)
	first := makeVersion("first", a, false)
	second := makeVersion("second", a, false)
	selected, err := store.ReconcileCargoGroupIndex(ctx, group.ID, "demo", []CargoGroupVersion{first, second})
	if err != nil || len(selected) != 1 || selected[0].SourceRepositoryID != "first" {
		t.Fatalf("initial=%+v err=%v", selected, err)
	}
	selected, err = store.ReconcileCargoGroupIndex(ctx, group.ID, "demo", []CargoGroupVersion{second, first})
	if err != nil || selected[0].SourceRepositoryID != "first" {
		t.Fatalf("reorder=%+v err=%v", selected, err)
	}
	if _, err := store.ReconcileCargoGroupIndex(ctx, group.ID, "demo", []CargoGroupVersion{first, makeVersion("second", b, false)}); !errors.Is(err, ErrCargoGroupConflict) {
		t.Fatalf("conflicting member=%v", err)
	}
	if _, err := store.ReconcileCargoGroupIndex(ctx, group.ID, "demo", []CargoGroupVersion{second}); !errors.Is(err, ErrUpstreamChanged) {
		t.Fatalf("owner disappearance=%v", err)
	}
	selected, err = store.ReconcileCargoGroupIndex(ctx, group.ID, "demo", []CargoGroupVersion{makeVersion("first", a, true), second})
	if err != nil || !strings.Contains(string(selected[0].IndexRow), `"yanked":true`) {
		t.Fatalf("yank=%+v err=%v", selected, err)
	}
	owner, err := store.GetCargoGroupVersion(ctx, group.ID, "demo", "1.0.0")
	if err != nil || owner.SourceRepositoryID != "first" || owner.Checksum != a {
		t.Fatalf("owner=%+v err=%v", owner, err)
	}
}

func TestMemoryCargoGroupRejectsConflictingHostedPublish(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	for _, name := range []string{"first", "second"} {
		if _, err := store.CreateHostedRepository(ctx, HostedRepository{ID: name, Name: name, Format: FormatCargo}); err != nil {
			t.Fatal(err)
		}
	}
	group, _, err := store.CreateHostedGroupIdempotently(ctx, HostedGroup{ID: "cargo-publication-group", Name: "cargo-publication-group",
		Format: FormatCargo, Members: []GroupMember{{RepositoryID: "first", Position: 0}, {RepositoryID: "second", Position: 1}}}, "test", "publish", "payload")
	if err != nil {
		t.Fatal(err)
	}
	checksum := strings.Repeat("a", 64)
	owner := CargoGroupVersion{GroupID: group.ID, SourceRepositoryID: "second", Name: "demo-crate", Version: "1.0.0",
		Checksum: checksum, IndexRow: []byte(`{"name":"demo-crate","vers":"1.0.0","cksum":"` + checksum + `","yanked":false}`)}
	if _, err := store.ReconcileCargoGroupIndex(ctx, group.ID, "demo-crate", []CargoGroupVersion{owner}); err != nil {
		t.Fatal(err)
	}
	claim := cargoTestClaim("first", "1.0.0", "b")
	reservation, _, err := store.ReserveCargoIdentity(ctx, claim)
	if err != nil {
		t.Fatal(err)
	}
	publication := cargoTestPublication(t, claim, reservation, 10)
	if _, _, err := store.CommitCargoPublication(ctx, publication); !errors.Is(err, ErrCargoGroupConflict) {
		t.Fatalf("conflicting publish=%v", err)
	}
	if _, err := store.GetCargoPublication(ctx, "first", "demo-crate", "1.0.0"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("conflicting publication visible: %v", err)
	}
}

func TestMemoryCargoGroupPreflightsKnownMemberCoordinates(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	for _, id := range []string{"first", "second", "proxy"} {
		repo := HostedRepository{ID: id, Name: id, Format: FormatCargo}
		if id == "proxy" {
			repo.Type, repo.Endpoint = RepositoryTypeProxy, "https://index.example"
		}
		if _, err := store.CreateHostedRepository(ctx, repo); err != nil {
			t.Fatal(err)
		}
	}
	publish := func(id, digest string) {
		t.Helper()
		claim := cargoTestClaim(id, "1.0.0", digest)
		reservation, _, err := store.ReserveCargoIdentity(ctx, claim)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := store.CommitCargoPublication(ctx, cargoTestPublication(t, claim, reservation, 10)); err != nil {
			t.Fatal(err)
		}
	}
	publish("first", "a")
	publish("second", "b")
	group := HostedGroup{ID: "preflight", Name: "preflight", Format: FormatCargo,
		Members: []GroupMember{{RepositoryID: "first", Position: 0}, {RepositoryID: "second", Position: 1}}}
	if _, _, err := store.CreateHostedGroupIdempotently(ctx, group, "test", "conflict", "payload"); !errors.Is(err, ErrCargoGroupConflict) {
		t.Fatalf("existing Hosted collision accepted: %v", err)
	}
	if _, err := store.GetHostedGroup(ctx, group.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("rejected group became visible: %v", err)
	}
	group.Members = group.Members[:1]
	created, _, err := store.CreateHostedGroupIdempotently(ctx, group, "test", "safe", "payload")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReplaceHostedGroupMembers(ctx, created.ID,
		[]GroupMember{{RepositoryID: "first", Position: 0}, {RepositoryID: "second", Position: 1}}, created.Version); !errors.Is(err, ErrCargoGroupConflict) {
		t.Fatalf("conflicting Hosted member admitted: %v", err)
	}
	index := CargoProxyIndex{RepositoryID: "proxy", Name: "demo-crate", Status: 200,
		Body:      []byte(`{"name":"demo-crate","vers":"1.0.0","cksum":"` + strings.Repeat("b", 64) + `","yanked":false}` + "\n"),
		FetchedAt: time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(time.Hour)}
	if err := store.PutCargoProxyIndex(ctx, index); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReplaceHostedGroupMembers(ctx, created.ID,
		[]GroupMember{{RepositoryID: "first", Position: 0}, {RepositoryID: "proxy", Position: 1}}, created.Version); !errors.Is(err, ErrCargoGroupConflict) {
		t.Fatalf("conflicting cached Proxy member admitted: %v", err)
	}
	current, err := store.GetHostedGroup(ctx, created.ID)
	if err != nil || current.Version != created.Version || len(current.Members) != 1 {
		t.Fatalf("failed preflight mutated group: %+v err=%v", current, err)
	}
}

func TestMemoryCargoGroupRejectsConflictingPublishBeforeFirstRead(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	for _, id := range []string{"first", "second"} {
		if _, err := store.CreateHostedRepository(ctx, HostedRepository{ID: id, Name: id, Format: FormatCargo}); err != nil {
			t.Fatal(err)
		}
	}
	group := HostedGroup{ID: "unread-group", Name: "unread-group", Format: FormatCargo,
		Members: []GroupMember{{RepositoryID: "first", Position: 0}, {RepositoryID: "second", Position: 1}}}
	if _, _, err := store.CreateHostedGroupIdempotently(ctx, group, "test", "unread", "payload"); err != nil {
		t.Fatal(err)
	}
	for _, fixture := range []struct {
		repo, digest string
		wantConflict bool
	}{{"first", "a", false}, {"second", "b", true}} {
		claim := cargoTestClaim(fixture.repo, "1.0.0", fixture.digest)
		reservation, _, err := store.ReserveCargoIdentity(ctx, claim)
		if err != nil {
			t.Fatal(err)
		}
		_, _, err = store.CommitCargoPublication(ctx, cargoTestPublication(t, claim, reservation, 10))
		if fixture.wantConflict && !errors.Is(err, ErrCargoGroupConflict) || !fixture.wantConflict && err != nil {
			t.Fatalf("publish %s: %v", fixture.repo, err)
		}
	}
	if _, err := store.GetCargoPublication(ctx, "second", "demo-crate", "1.0.0"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("conflicting version became visible: %v", err)
	}
}
