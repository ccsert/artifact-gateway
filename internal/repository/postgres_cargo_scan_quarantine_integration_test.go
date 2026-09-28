//go:build integration

package repository

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
)

func TestPostgresCargoScanIdentityAndQuarantinePersistence(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is required")
	}
	store, err := NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	repo, err := store.CreateHostedRepository(ctx, HostedRepository{ID: uuid.NewString(), Name: "cargo-govern-" + uuid.NewString()[:8], Format: FormatCargo})
	if err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = store.db.ExecContext(context.Background(), `DELETE FROM hosted_repositories WHERE id=$1`, repo.ID)
		_ = store.Close()
	})
	claim := cargoTestClaim(repo.ID, "1.0.0", "a")
	reservation, _, err := store.ReserveCargoIdentity(ctx, claim)
	if err != nil {
		t.Fatal(err)
	}
	publication := cargoTestPublication(t, claim, reservation, 7)
	if _, _, err = store.CommitCargoPublication(ctx, publication); err != nil {
		t.Fatal(err)
	}
	other, err := NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = other.Close() }()
	coordinate := publication.Name + "@" + publication.Version
	identities, err := other.ListArtifactIdentities(ctx, repo.ID, FormatCargo, ArtifactIdentityScan, "", 10)
	if err != nil || len(identities) != 1 || identities[0].Coordinate != coordinate || identities[0].Digest != publication.Digest {
		t.Fatalf("identities=%+v err=%v", identities, err)
	}
	candidates, err := other.ListArtifactScanCandidates(ctx, repo.ID, FormatCargo, 10)
	if err != nil || len(candidates) != 1 || candidates[0].Coordinate != coordinate {
		t.Fatalf("scan candidates=%+v err=%v", candidates, err)
	}
	quarantine, err := store.ReplaceArtifactQuarantine(ctx, ArtifactQuarantine{RepositoryID: repo.ID,
		Format: FormatCargo, Coordinate: coordinate, Digest: publication.Digest,
		State: ArtifactQuarantineStateQuarantined, Reason: "scan review", UpdatedBy: "operator"}, "0")
	if err != nil {
		t.Fatal(err)
	}
	policy, err := store.ReplaceRepositoryQuarantineReadPolicy(ctx, repo.ID,
		RepositoryQuarantineReadPolicy{Version: "1", Enabled: true}, "1")
	if err != nil || !policy.Enabled {
		t.Fatalf("policy=%+v err=%v", policy, err)
	}
	blocked, err := QuarantinedArtifactReadBlocked(ctx, other, other, repo.ID, FormatCargo, coordinate, publication.Digest)
	if err != nil || !blocked {
		t.Fatalf("cross-instance blocked=%t err=%v", blocked, err)
	}
	quarantine.State, quarantine.Reason, quarantine.UpdatedBy = ArtifactQuarantineStateReleased, "reviewed", "operator"
	if _, err := store.ReplaceArtifactQuarantine(ctx, quarantine, quarantine.Version); err != nil {
		t.Fatal(err)
	}
	blocked, err = QuarantinedArtifactReadBlocked(ctx, other, other, repo.ID, FormatCargo, coordinate, publication.Digest)
	if err != nil || blocked {
		t.Fatalf("cross-instance release blocked=%t err=%v", blocked, err)
	}
}
