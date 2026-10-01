//go:build integration

package repository

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestPostgresCargoGroupOwnerSurvivesConnectionsAndReorder(t *testing.T) {
	connection := os.Getenv("TEST_DATABASE_URL")
	if connection == "" {
		t.Skip("TEST_DATABASE_URL is required")
	}
	first, err := NewPostgresStore(connection)
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewPostgresStore(connection)
	if err != nil {
		_ = first.Close()
		t.Fatal(err)
	}
	ctx := context.Background()
	repos := make([]HostedRepository, 2)
	for i := range repos {
		repos[i], err = first.CreateHostedRepository(ctx, HostedRepository{ID: uuid.NewString(), Name: "cargo-member-" + uuid.NewString()[:8], Format: FormatCargo})
		if err != nil {
			t.Fatal(err)
		}
	}
	group, _, err := first.CreateHostedGroupIdempotently(ctx, HostedGroup{ID: uuid.NewString(), Name: "cargo-group-" + uuid.NewString()[:8],
		Format: FormatCargo, Members: []GroupMember{{RepositoryID: repos[0].ID, Position: 0}, {RepositoryID: repos[1].ID, Position: 1}}}, "test", uuid.NewString(), "payload")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = first.db.ExecContext(context.Background(), `DELETE FROM hosted_groups WHERE id=$1`, group.ID)
		for _, repo := range repos {
			_, _ = first.db.ExecContext(context.Background(), `DELETE FROM hosted_repositories WHERE id=$1`, repo.ID)
		}
		_ = first.Close()
		_ = second.Close()
	})
	checksum := strings.Repeat("a", 64)
	makeVersion := func(repo HostedRepository) CargoGroupVersion {
		return CargoGroupVersion{GroupID: group.ID, SourceRepositoryID: repo.ID, Name: "demo", Version: "1.0.0",
			Checksum: checksum, IndexRow: []byte(`{"name":"demo","vers":"1.0.0","cksum":"` + checksum + `","yanked":false}`)}
	}
	a, b := makeVersion(repos[0]), makeVersion(repos[1])
	selected, err := first.ReconcileCargoGroupIndex(ctx, group.ID, "demo", []CargoGroupVersion{a, b})
	if err != nil || len(selected) != 1 || selected[0].SourceRepositoryID != a.SourceRepositoryID {
		t.Fatalf("initial=%+v err=%v", selected, err)
	}
	selected, err = second.ReconcileCargoGroupIndex(ctx, group.ID, "demo", []CargoGroupVersion{b, a})
	if err != nil || selected[0].SourceRepositoryID != a.SourceRepositoryID {
		t.Fatalf("reordered=%+v err=%v", selected, err)
	}
	owner, err := second.GetCargoGroupVersion(ctx, group.ID, "demo", "1.0.0")
	if err != nil || owner.SourceRepositoryID != a.SourceRepositoryID {
		t.Fatalf("cross-connection owner=%+v err=%v", owner, err)
	}
	if _, err := second.ReconcileCargoGroupIndex(ctx, group.ID, "demo", []CargoGroupVersion{b}); !errors.Is(err, ErrUpstreamChanged) {
		t.Fatalf("removed owner: %v", err)
	}
	owner, err = first.GetCargoGroupVersion(ctx, group.ID, "demo", "1.0.0")
	if err != nil || owner.SourceRepositoryID != a.SourceRepositoryID {
		t.Fatalf("rollback owner=%+v err=%v", owner, err)
	}
	claim := CargoIdentityClaim{RepositoryID: repos[1].ID, Name: "demo", Version: "1.0.0",
		Digest: "sha256:" + strings.Repeat("b", 64), MetadataDigest: "sha256:" + strings.Repeat("c", 64)}
	reservation, _, err := first.ReserveCargoIdentity(ctx, claim)
	if err != nil {
		t.Fatal(err)
	}
	publication := cargoTestPublication(t, claim, reservation, 10)
	if _, _, err := second.CommitCargoPublication(ctx, publication); !errors.Is(err, ErrCargoGroupConflict) {
		t.Fatalf("conflicting Hosted publication=%v", err)
	}
}

func TestPostgresCargoGroupPreflightsKnownHostedCoordinates(t *testing.T) {
	connection := os.Getenv("TEST_DATABASE_URL")
	if connection == "" {
		t.Skip("TEST_DATABASE_URL is required")
	}
	store, err := NewPostgresStore(connection)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	ctx := context.Background()
	ids := []string{uuid.NewString(), uuid.NewString()}
	for i, id := range ids {
		if _, err := store.CreateHostedRepository(ctx, HostedRepository{ID: id, Name: "cargo-preflight-" + uuid.NewString()[:8], Format: FormatCargo}); err != nil {
			t.Fatal(err)
		}
		claim := cargoTestClaim(id, "1.0.0", []string{"a", "b"}[i])
		reservation, _, err := store.ReserveCargoIdentity(ctx, claim)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := store.CommitCargoPublication(ctx, cargoTestPublication(t, claim, reservation, 10)); err != nil {
			t.Fatal(err)
		}
	}
	group := HostedGroup{ID: uuid.NewString(), Name: "cargo-preflight-" + uuid.NewString()[:8], Format: FormatCargo,
		Members: []GroupMember{{RepositoryID: ids[0], Position: 0}, {RepositoryID: ids[1], Position: 1}}}
	defer func() {
		_, _ = store.db.ExecContext(context.Background(), `DELETE FROM hosted_groups WHERE id=$1`, group.ID)
		for _, id := range ids {
			_, _ = store.db.ExecContext(context.Background(), `DELETE FROM hosted_repositories WHERE id=$1`, id)
		}
	}()
	if _, _, err := store.CreateHostedGroupIdempotently(ctx, group, "test", uuid.NewString(), "payload"); !errors.Is(err, ErrCargoGroupConflict) {
		t.Fatalf("existing Hosted collision accepted: %v", err)
	}
	if _, err := store.GetHostedGroup(ctx, group.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("rejected group became visible: %v", err)
	}
	group.Members = group.Members[:1]
	created, _, err := store.CreateHostedGroupIdempotently(ctx, group, "test", uuid.NewString(), "payload")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReplaceHostedGroupMembers(ctx, created.ID,
		[]GroupMember{{RepositoryID: ids[0], Position: 0}, {RepositoryID: ids[1], Position: 1}}, created.Version); !errors.Is(err, ErrCargoGroupConflict) {
		t.Fatalf("conflicting Hosted member admitted: %v", err)
	}
	proxyID := uuid.NewString()
	if _, err := store.CreateHostedRepository(ctx, HostedRepository{ID: proxyID, Name: "cargo-proxy-preflight-" + uuid.NewString()[:8],
		Format: FormatCargo, Type: RepositoryTypeProxy, Endpoint: "https://index.example"}); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = store.db.ExecContext(context.Background(), `DELETE FROM hosted_repositories WHERE id=$1`, proxyID)
	}()
	index := CargoProxyIndex{RepositoryID: proxyID, Name: "demo-crate", Status: 200,
		Body:      []byte(`{"name":"demo-crate","vers":"1.0.0","cksum":"` + strings.Repeat("b", 64) + `","yanked":false}` + "\n"),
		FetchedAt: time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(time.Hour)}
	if err := store.PutCargoProxyIndex(ctx, index); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReplaceHostedGroupMembers(ctx, created.ID,
		[]GroupMember{{RepositoryID: ids[0], Position: 0}, {RepositoryID: proxyID, Position: 1}}, created.Version); !errors.Is(err, ErrCargoGroupConflict) {
		t.Fatalf("conflicting cached Proxy member admitted: %v", err)
	}
	current, err := store.GetHostedGroup(ctx, created.ID)
	if err != nil || current.Version != created.Version || len(current.Members) != 1 {
		t.Fatalf("failed preflight mutated group: %+v err=%v", current, err)
	}
	freshIDs := []string{uuid.NewString(), uuid.NewString()}
	for _, id := range freshIDs {
		if _, err := store.CreateHostedRepository(ctx, HostedRepository{ID: id, Name: "cargo-unread-" + uuid.NewString()[:8], Format: FormatCargo}); err != nil {
			t.Fatal(err)
		}
	}
	unread := HostedGroup{ID: uuid.NewString(), Name: "cargo-unread-" + uuid.NewString()[:8], Format: FormatCargo,
		Members: []GroupMember{{RepositoryID: freshIDs[0], Position: 0}, {RepositoryID: freshIDs[1], Position: 1}}}
	defer func() {
		_, _ = store.db.ExecContext(context.Background(), `DELETE FROM hosted_groups WHERE id=$1`, unread.ID)
		for _, id := range freshIDs {
			_, _ = store.db.ExecContext(context.Background(), `DELETE FROM hosted_repositories WHERE id=$1`, id)
		}
	}()
	if _, _, err := store.CreateHostedGroupIdempotently(ctx, unread, "test", uuid.NewString(), "payload"); err != nil {
		t.Fatal(err)
	}
	for i, id := range freshIDs {
		claim := cargoTestClaim(id, "1.0.0", []string{"a", "b"}[i])
		reservation, _, err := store.ReserveCargoIdentity(ctx, claim)
		if err != nil {
			t.Fatal(err)
		}
		_, _, err = store.CommitCargoPublication(ctx, cargoTestPublication(t, claim, reservation, 10))
		if i == 0 && err != nil || i == 1 && !errors.Is(err, ErrCargoGroupConflict) {
			t.Fatalf("publish member %d: %v", i, err)
		}
	}
	if _, err := store.GetCargoPublication(ctx, freshIDs[1], "demo-crate", "1.0.0"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("conflicting version became visible: %v", err)
	}
}
