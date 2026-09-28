//go:build integration

package repository

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

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
