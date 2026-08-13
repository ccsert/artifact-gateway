package repository

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestAPTSnapshotPublicationValidationBindsReleaseAndByHashClosure(t *testing.T) {
	t.Parallel()
	digest := func(body []byte) string {
		sum := sha256.Sum256(body)
		return "sha256:" + hex.EncodeToString(sum[:])
	}
	snapshot := APTRepositorySnapshot{
		ID: "snapshot", RepositoryID: "repo", Suite: "stable", Sequence: 1, State: APTRepositorySnapshotVisible,
		SignerIdentity: "signer", KeyFingerprint: strings.Repeat("a", 40), SignatureAlgorithm: "fixture",
	}
	indexBody := []byte("Package: widget\n")
	indexDigest := digest(indexBody)
	release := []byte(fmt.Sprintf("Suite: stable\nAcquire-By-Hash: yes\nSHA256:\n %s %16d main/binary-amd64/Packages\n", strings.TrimPrefix(indexDigest, "sha256:"), len(indexBody)))
	snapshot.ReleaseDigest = digest(release)
	snapshot.InReleaseDigest = digest([]byte("inrelease"))
	asset := func(path, body, contentType string) APTSnapshotAsset {
		d := digest([]byte(body))
		return APTSnapshotAsset{SnapshotID: snapshot.ID, RepositoryID: snapshot.RepositoryID, Path: path, Digest: d, ObjectKey: "native/apt/sha256/" + strings.TrimPrefix(d, "sha256:"), Size: int64(len(body)), ContentType: contentType}
	}
	assets := []APTSnapshotAsset{
		asset("dists/stable/Release", string(release), "text/plain"),
		asset("dists/stable/InRelease", "inrelease", "application/pgp-signature"),
		asset("dists/stable/Release.gpg", "signature", "application/pgp-signature"),
		asset("dists/stable/main/binary-amd64/Packages", string(indexBody), "text/plain"),
		asset("dists/stable/main/binary-amd64/by-hash/SHA256/"+strings.TrimPrefix(indexDigest, "sha256:"), string(indexBody), "text/plain"),
		asset("pool/main/w/widget/widget_1.0-1_amd64.deb", "package", "application/vnd.debian.binary-package"),
	}
	if !validAPTSnapshotPublication(snapshot, assets, release) {
		t.Fatal("coherent snapshot publication was rejected")
	}
	badByHash := append([]APTSnapshotAsset(nil), assets...)
	badByHash[4].Path = strings.TrimSuffix(badByHash[4].Path, strings.TrimPrefix(indexDigest, "sha256:")) + strings.Repeat("b", 64)
	if validAPTSnapshotPublication(snapshot, badByHash, release) {
		t.Fatal("mismatched by-hash suffix was accepted")
	}
	badRelease := []byte(strings.Replace(string(release), strings.TrimPrefix(indexDigest, "sha256:"), strings.Repeat("c", 64), 1))
	badSnapshot := snapshot
	badSnapshot.ReleaseDigest = digest(badRelease)
	badAssets := append([]APTSnapshotAsset(nil), assets...)
	badAssets[0] = asset("dists/stable/Release", string(badRelease), "text/plain")
	if validAPTSnapshotPublication(badSnapshot, badAssets, badRelease) {
		t.Fatal("Release checksum closure mismatch was accepted")
	}
}

func TestMemoryAPTPublicationSessionReservesQuotaAndIsIdempotent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := NewMemoryStore()
	repo, err := store.CreateHostedRepository(ctx, HostedRepository{ID: "apt-hosted", Name: "apt-hosted", Format: FormatAPT, Type: RepositoryTypeHosted})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.ReplaceRepositoryCapacityQuota(ctx, repo.ID, 12); err != nil {
		t.Fatal(err)
	}
	session := APTPublicationSession{
		ID: "apt-session", RepositoryID: repo.ID, Suite: "stable", Component: "main",
		Publisher: "ci", ObjectName: "widget_1.0_amd64.deb",
		DeclaredDigest: "sha256:" + strings.Repeat("a", 64), DeclaredSize: 8,
		State: APTPublicationSessionOpen, ExpiresAt: time.Now().Add(time.Hour),
	}
	created, replayed, err := store.CreateAPTPublicationSessionIdempotently(ctx, session, "ci", "repositories/apt-hosted/apt-publication-sessions", "release-1", "payload-a")
	if err != nil || replayed || created.ID != session.ID {
		t.Fatalf("created=%#v replayed=%t err=%v", created, replayed, err)
	}
	replay, replayed, err := store.CreateAPTPublicationSessionIdempotently(ctx, APTPublicationSession{ID: "discarded"}, "ci", "repositories/apt-hosted/apt-publication-sessions", "release-1", "payload-a")
	if err != nil || !replayed || replay.ID != session.ID {
		t.Fatalf("replay=%#v replayed=%t err=%v", replay, replayed, err)
	}
	if _, _, err = store.CreateAPTPublicationSessionIdempotently(ctx, session, "ci", "repositories/apt-hosted/apt-publication-sessions", "release-1", "payload-b"); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("conflicting replay error=%v", err)
	}

	second := session
	second.ID = "apt-session-two"
	second.DeclaredDigest = "sha256:" + strings.Repeat("b", 64)
	if _, _, err = store.CreateAPTPublicationSessionIdempotently(ctx, second, "ci", "repositories/apt-hosted/apt-publication-sessions", "release-2", "payload-c"); !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("second reservation error=%v", err)
	}
}

func TestMemoryAPTStagedRevisionAndBuildingSnapshotRemainInvisible(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := NewMemoryStore()
	repo, err := store.CreateHostedRepository(ctx, HostedRepository{ID: "apt-hosted", Name: "apt-hosted", Format: FormatAPT, Type: RepositoryTypeHosted})
	if err != nil {
		t.Fatal(err)
	}
	session := APTPublicationSession{
		ID: "apt-session", RepositoryID: repo.ID, Suite: "stable", Component: "main",
		Publisher: "ci", ObjectName: "widget_1.0-1_amd64.deb", ExpectedIdentity: "widget@1.0-1#amd64",
		DeclaredDigest: "sha256:" + strings.Repeat("a", 64), DeclaredSize: 8,
		State: APTPublicationSessionOpen, ExpiresAt: time.Now().Add(time.Hour),
	}
	if _, _, err = store.CreateAPTPublicationSessionIdempotently(ctx, session, "ci", "apt", "one", "one"); err != nil {
		t.Fatal(err)
	}
	objectKey := "native/apt/sha256/" + strings.Repeat("a", 64)
	if err = store.BeginAPTPackageUpload(ctx, session.ID, objectKey); err != nil {
		t.Fatal(err)
	}
	revision, err := store.CompleteAPTPackageUpload(ctx, session.ID, APTPackageRevision{
		ID: "apt-revision", RepositoryID: repo.ID, Package: "widget", Version: "1.0-1", Architecture: "amd64",
		CanonicalIdentity: "widget@1.0-1#amd64", Digest: session.DeclaredDigest,
		ObjectKey: objectKey, Size: session.DeclaredSize, ObjectName: session.ObjectName, Publisher: "ci",
	})
	if err != nil {
		t.Fatal(err)
	}
	if revision.CanonicalIdentity != session.ExpectedIdentity {
		t.Fatalf("revision=%#v", revision)
	}
	stored, err := store.GetAPTPublicationSession(ctx, session.ID)
	if err != nil || stored.State != APTPublicationSessionStaged || stored.PackageRevisionID != revision.ID {
		t.Fatalf("stored session=%#v err=%v", stored, err)
	}
	if _, err = store.GetAPTAsset(ctx, repo.ID, "pool/main/w/widget/widget_1.0-1_amd64.deb"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("staged revision leaked through protocol asset lookup: %v", err)
	}

	snapshot, err := store.CreateAPTRepositorySnapshot(ctx, APTRepositorySnapshot{
		ID: "snapshot-one", RepositoryID: repo.ID, Suite: "stable", Sequence: 1, State: APTRepositorySnapshotBuilding,
	}, []APTSnapshotPackage{{PublicationSessionID: session.ID, PackageRevisionID: revision.ID, Component: "main", Architecture: "amd64"}})
	if err != nil || snapshot.State != APTRepositorySnapshotBuilding {
		t.Fatalf("snapshot=%#v err=%v", snapshot, err)
	}
	if _, err = store.GetVisibleAPTRepositorySnapshot(ctx, repo.ID, "stable"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("building snapshot became visible: %v", err)
	}
	replayed, err := store.CreateAPTRepositorySnapshot(ctx, APTRepositorySnapshot{
		ID: snapshot.ID, RepositoryID: repo.ID, Suite: "stable", Sequence: 1, State: APTRepositorySnapshotBuilding,
	}, []APTSnapshotPackage{{PublicationSessionID: session.ID, PackageRevisionID: revision.ID, Component: "main", Architecture: "amd64"}})
	if err != nil || replayed.ID != snapshot.ID || replayed.CreatedAt != snapshot.CreatedAt {
		t.Fatalf("exact snapshot replay=%#v err=%v", replayed, err)
	}
	if _, err = store.CreateAPTRepositorySnapshot(ctx, APTRepositorySnapshot{
		ID: snapshot.ID, RepositoryID: repo.ID, Suite: "stable", Sequence: 2, State: APTRepositorySnapshotBuilding,
	}, []APTSnapshotPackage{{PublicationSessionID: session.ID, PackageRevisionID: revision.ID, Component: "main", Architecture: "amd64"}}); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("conflicting snapshot replay error=%v", err)
	}
}

func TestAPTSnapshotMembershipRejectsDuplicateSessionsAndPackageMembership(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		items []APTSnapshotPackage
	}{
		{
			name: "same publication session",
			items: []APTSnapshotPackage{
				{PublicationSessionID: "session-one", PackageRevisionID: "revision-one", Component: "main", Architecture: "amd64"},
				{PublicationSessionID: "session-one", PackageRevisionID: "revision-two", Component: "main", Architecture: "arm64"},
			},
		},
		{
			name: "same package revision component and architecture through different sessions",
			items: []APTSnapshotPackage{
				{PublicationSessionID: "session-one", PackageRevisionID: "revision-one", Component: "main", Architecture: "amd64"},
				{PublicationSessionID: "session-two", PackageRevisionID: "revision-one", Component: "main", Architecture: "amd64"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := validateAPTSnapshotMembership(tt.items); !errors.Is(err, ErrNameExists) {
				t.Fatalf("membership error=%v", err)
			}
		})
	}
}

func TestAPTRevisionValidationMatchesDebianIdentityGrammar(t *testing.T) {
	t.Parallel()
	base := APTPackageRevision{
		ID: "revision", RepositoryID: "repository", Package: "libc6", Version: "2:2.39-0ubuntu8.4", Architecture: "amd64",
		CanonicalIdentity: "libc6@2:2.39-0ubuntu8.4#amd64", Digest: "sha256:" + strings.Repeat("d", 64),
		ObjectKey: "native/apt/sha256/" + strings.Repeat("d", 64), Size: 8, ObjectName: "libc6.deb", Publisher: "ci",
	}
	if !validAPTPackageRevision(base) {
		t.Fatal("valid Debian identity was rejected")
	}
	invalid := base
	invalid.Package = "Bad_Name"
	invalid.CanonicalIdentity = invalid.Package + "@" + invalid.Version + "#" + invalid.Architecture
	if validAPTPackageRevision(invalid) {
		t.Fatal("invalid Debian package name was accepted")
	}
	invalid = base
	invalid.Version = "foo"
	invalid.CanonicalIdentity = invalid.Package + "@" + invalid.Version + "#" + invalid.Architecture
	if validAPTPackageRevision(invalid) {
		t.Fatal("Debian version without a numeric upstream prefix was accepted")
	}
}

func TestMemoryAPTUploadRecoveryDoesNotDeleteReferencedObjects(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := NewMemoryStore()
	repo, err := store.CreateHostedRepository(ctx, HostedRepository{ID: "apt-hosted", Name: "apt-hosted", Format: FormatAPT, Type: RepositoryTypeHosted})
	if err != nil {
		t.Fatal(err)
	}
	expired := APTPublicationSession{
		ID: "expired", RepositoryID: repo.ID, Suite: "stable", Component: "main", Publisher: "ci",
		ObjectName: "orphan.deb", DeclaredDigest: "sha256:" + strings.Repeat("c", 64), DeclaredSize: 4,
		State: APTPublicationSessionOpen, ExpiresAt: time.Now().Add(time.Minute),
	}
	if _, _, err = store.CreateAPTPublicationSessionIdempotently(ctx, expired, "ci", "apt", "expired", "expired"); err != nil {
		t.Fatal(err)
	}
	objectKey := "native/apt/sha256/" + strings.Repeat("c", 64)
	if err = store.BeginAPTPackageUpload(ctx, expired.ID, objectKey); err != nil {
		t.Fatal(err)
	}
	abandoned, err := store.ExpireAPTPublicationSessions(ctx, time.Now().Add(2*time.Minute), 10)
	if err != nil || len(abandoned) != 1 || abandoned[0].ObjectKey != objectKey {
		t.Fatalf("abandoned=%#v err=%v", abandoned, err)
	}
	recovered, err := store.GetAPTPublicationSession(ctx, expired.ID)
	if err != nil || recovered.State != APTPublicationSessionAborted {
		t.Fatalf("recovered=%#v err=%v", recovered, err)
	}
	referenced, err := store.APTObjectHasPackageReference(ctx, objectKey)
	if err != nil || referenced {
		t.Fatalf("referenced=%t err=%v", referenced, err)
	}
}

func TestMemoryAPTAbandonedSnapshotIntentsBecomeReclaimable(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := time.Date(2026, time.August, 13, 12, 0, 0, 0, time.UTC)
	store := NewMemoryStore()
	digest := "sha256:" + strings.Repeat("e", 64)
	objectKey := "native/apt/sha256/" + strings.Repeat("e", 64)
	store.aptSnapshots["abandoned-snapshot"] = APTRepositorySnapshot{
		ID: "abandoned-snapshot", RepositoryID: "apt-hosted", Suite: "stable",
		Sequence: 1, State: APTRepositorySnapshotBuilding, CreatedAt: now.Add(-2 * time.Hour),
	}
	store.aptSnapshotObjects["abandoned-snapshot"] = map[string]APTSnapshotObjectIntent{
		objectKey: {
			SnapshotID: "abandoned-snapshot", RepositoryID: "apt-hosted", ObjectKey: objectKey,
			Digest: digest, Size: 8, CreatedAt: now.Add(-2 * time.Hour),
		},
	}

	if err := store.ExpireAPTRepositorySnapshots(ctx, now.Add(-time.Hour), 10); err != nil {
		t.Fatal(err)
	}
	if state := store.aptSnapshots["abandoned-snapshot"].State; state != APTRepositorySnapshotFailed {
		t.Fatalf("snapshot state=%q", state)
	}
	intents, err := store.ListUnscheduledAPTSnapshotObjects(ctx, 10)
	if err != nil || len(intents) != 1 || intents[0].ObjectKey != objectKey {
		t.Fatalf("reclaimable intents=%#v err=%v", intents, err)
	}
}
