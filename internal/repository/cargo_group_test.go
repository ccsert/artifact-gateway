package repository

import (
	"context"
	"errors"
	"strings"
	"testing"
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
