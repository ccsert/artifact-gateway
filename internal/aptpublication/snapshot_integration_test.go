//go:build integration

package aptpublication

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/objectstore"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

func TestPostgresRustFSPublisherExposesOneCompleteSignedSnapshotAcrossInstances(t *testing.T) {
	databaseURL, endpoint := os.Getenv("TEST_DATABASE_URL"), os.Getenv("TEST_S3_ENDPOINT")
	accessKey, secretKey := os.Getenv("TEST_S3_ACCESS_KEY"), os.Getenv("TEST_S3_SECRET_KEY")
	if databaseURL == "" || endpoint == "" || accessKey == "" || secretKey == "" {
		t.Skip("PostgreSQL and RustFS integration environment is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	storeA, err := repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = storeA.Close() }()
	storeB, err := repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = storeB.Close() }()
	objects, err := objectstore.NewS3Store(endpoint, accessKey, secretKey, "apt-snapshot-"+strings.ReplaceAll(uuid.NewString(), "-", "")[:20])
	if err != nil {
		t.Fatal(err)
	}
	if err = objects.EnsureBucket(ctx); err != nil {
		t.Fatal(err)
	}
	repo, err := storeA.CreateHostedRepository(ctx, repository.HostedRepository{
		ID: uuid.NewString(), Name: "apt-snapshot-" + uuid.NewString(), Format: repository.FormatAPT, Type: repository.RepositoryTypeHosted,
	})
	if err != nil {
		t.Fatal(err)
	}
	deb := testDebianPackage(t, "Package: widget\nVersion: 1.0-1\nArchitecture: amd64\nMaintainer: Gateway Team <gateway@example.test>\nDescription: integration fixture\n")
	sum := sha256.Sum256(deb)
	manager := NewManager(storeA, objects)
	session, _, err := manager.CreateSession(ctx, CreateSessionInput{
		RepositoryID: repo.ID, Suite: "stable", Component: "main", Publisher: "release-ci",
		ObjectName: "widget_1.0-1_amd64.deb", DeclaredDigest: "sha256:" + hex.EncodeToString(sum[:]),
		DeclaredSize: int64(len(deb)), ExpectedIdentity: "widget@1.0-1#amd64", IdempotencyKey: uuid.NewString(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = manager.UploadPackageAs(ctx, session.ID, session.ObjectName, bytes.NewReader(deb), int64(len(deb)), "release-operator"); err != nil {
		t.Fatal(err)
	}
	snapshot, err := NewPublisher(storeA, objects, deterministicAPTSigner{}).Publish(ctx, PublishSnapshotInput{
		ID: uuid.NewString(), RepositoryID: repo.ID, Suite: "stable", Sequence: 1,
		SessionIDs: []string{session.ID}, Actor: "release-operator",
		CreatedAt: time.Date(2026, time.August, 13, 8, 30, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	visible, err := storeB.GetVisibleAPTRepositorySnapshot(ctx, repo.ID, "stable")
	if err != nil || visible.ID != snapshot.ID || visible.ReleaseDigest != snapshot.ReleaseDigest || visible.SignatureAlgorithm != "fixture-sha256" {
		t.Fatalf("cross-instance snapshot=%#v err=%v", visible, err)
	}
	assets, err := storeB.ListVisibleAPTSnapshotAssets(ctx, repo.ID, "stable")
	if err != nil || len(assets) < 8 {
		t.Fatalf("cross-instance assets=%#v err=%v", assets, err)
	}
	for _, path := range []string{
		"dists/stable/InRelease", "dists/stable/Release", "dists/stable/Release.gpg",
		"dists/stable/main/binary-amd64/Packages", "dists/stable/main/binary-amd64/Packages.gz",
		"pool/main/w/widget/widget_1.0-1_amd64.deb",
	} {
		asset, assetErr := storeB.GetVisibleAPTSnapshotAsset(ctx, repo.ID, path)
		if assetErr != nil || asset.SnapshotID != snapshot.ID {
			t.Fatalf("asset %q=%#v err=%v", path, asset, assetErr)
		}
		body, objectErr := objects.Get(ctx, asset.ObjectKey)
		if objectErr != nil || int64(len(body)) != asset.Size || digestBytes(body) != asset.Digest {
			t.Fatalf("object %q size=%d digest=%q err=%v", path, len(body), digestBytes(body), objectErr)
		}
	}

	conflictingDeb := testDebianPackage(t, "Package: widget\nVersion: 2.0-1\nArchitecture: amd64\nMaintainer: Gateway Team <gateway@example.test>\nDescription: conflicting pool path\n")
	conflictingSum := sha256.Sum256(conflictingDeb)
	conflictingSession, _, err := manager.CreateSession(ctx, CreateSessionInput{
		RepositoryID: repo.ID, Suite: "testing", Component: "main", Publisher: "release-ci",
		ObjectName: session.ObjectName, DeclaredDigest: "sha256:" + hex.EncodeToString(conflictingSum[:]),
		DeclaredSize: int64(len(conflictingDeb)), ExpectedIdentity: "widget@2.0-1#amd64", IdempotencyKey: uuid.NewString(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = manager.UploadPackageAs(ctx, conflictingSession.ID, conflictingSession.ObjectName, bytes.NewReader(conflictingDeb), int64(len(conflictingDeb)), "release-operator"); err != nil {
		t.Fatal(err)
	}
	if _, err = NewPublisher(storeA, objects, deterministicAPTSigner{}).Publish(ctx, PublishSnapshotInput{
		ID: uuid.NewString(), RepositoryID: repo.ID, Suite: "testing", Sequence: 1,
		SessionIDs: []string{conflictingSession.ID}, Actor: "release-operator",
		CreatedAt: time.Date(2026, time.August, 13, 8, 31, 0, 0, time.UTC),
	}); !errors.Is(err, repository.ErrAPTPackageConflict) {
		t.Fatalf("cross-suite pool path rebinding error=%v", err)
	}
	poolPath := "pool/main/w/widget/" + session.ObjectName
	poolAsset, err := storeB.GetVisibleAPTSnapshotAsset(ctx, repo.ID, poolPath)
	if err != nil || poolAsset.SnapshotID != snapshot.ID || poolAsset.Digest != "sha256:"+hex.EncodeToString(sum[:]) {
		t.Fatalf("pool path changed after conflicting suite: asset=%#v err=%v", poolAsset, err)
	}
}

func TestPostgresRustFSFailedSnapshotObjectsAreDurablyReclaimed(t *testing.T) {
	databaseURL, endpoint := os.Getenv("TEST_DATABASE_URL"), os.Getenv("TEST_S3_ENDPOINT")
	accessKey, secretKey := os.Getenv("TEST_S3_ACCESS_KEY"), os.Getenv("TEST_S3_SECRET_KEY")
	if databaseURL == "" || endpoint == "" || accessKey == "" || secretKey == "" {
		t.Skip("PostgreSQL and RustFS integration environment is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	store, err := repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	objects, err := objectstore.NewS3Store(endpoint, accessKey, secretKey, "apt-failed-snapshot-"+strings.ReplaceAll(uuid.NewString(), "-", "")[:14])
	if err != nil {
		t.Fatal(err)
	}
	if err = objects.EnsureBucket(ctx); err != nil {
		t.Fatal(err)
	}
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "apt-failed-" + uuid.NewString(), Format: repository.FormatAPT, Type: repository.RepositoryTypeHosted})
	if err != nil {
		t.Fatal(err)
	}
	deb := testDebianPackage(t, "Package: orphan\nVersion: 1.0-1\nArchitecture: amd64\nDescription: failed snapshot fixture\n")
	sum := sha256.Sum256(deb)
	manager := NewManager(store, objects)
	session, _, err := manager.CreateSession(ctx, CreateSessionInput{
		RepositoryID: repo.ID, Suite: "stable", Component: "main", Publisher: "ci", ObjectName: "orphan_1.0-1_amd64.deb",
		DeclaredDigest: "sha256:" + hex.EncodeToString(sum[:]), DeclaredSize: int64(len(deb)), IdempotencyKey: uuid.NewString(),
	})
	if err != nil {
		t.Fatal(err)
	}
	revision, err := manager.UploadPackageAs(ctx, session.ID, session.ObjectName, bytes.NewReader(deb), int64(len(deb)), "release-operator")
	if err != nil {
		t.Fatal(err)
	}
	failing := &failingSnapshotObjectStore{Store: objects, remainingWrites: 1}
	if _, err = NewPublisher(store, failing, deterministicAPTSigner{}).Publish(ctx, PublishSnapshotInput{
		ID: uuid.NewString(), RepositoryID: repo.ID, Suite: "stable", Sequence: 1,
		SessionIDs: []string{session.ID}, Actor: "release-operator", CreatedAt: time.Now().UTC(),
	}); err == nil {
		t.Fatal("snapshot unexpectedly published after RustFS write failure")
	}
	keysBefore, err := objects.List(ctx, "native/apt/sha256/")
	if err != nil || len(keysBefore) != 2 {
		t.Fatalf("partial snapshot objects=%v err=%v", keysBefore, err)
	}
	maintenance := Maintenance{Store: store, Objects: objects}
	if err = maintenance.Schedule(ctx); err != nil {
		t.Fatal(err)
	}
	if err = maintenance.RunReclaimJobs(ctx, 100); err != nil {
		t.Fatal(err)
	}
	keysAfter, err := objects.List(ctx, "native/apt/sha256/")
	if err != nil || len(keysAfter) != 1 || keysAfter[0] != revision.ObjectKey {
		t.Fatalf("objects after durable reclaim=%v want=%q err=%v", keysAfter, revision.ObjectKey, err)
	}
}
