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
	databaseURL, endpoint := os.Getenv("TEST_DATABASE_URL"), os.Getenv("TEST_RUSTFS_ENDPOINT")
	accessKey, secretKey := os.Getenv("TEST_RUSTFS_ACCESS_KEY"), os.Getenv("TEST_RUSTFS_SECRET_KEY")
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
	objects, err := objectstore.NewRustFSStore(endpoint, accessKey, secretKey, "apt-snapshot-"+strings.ReplaceAll(uuid.NewString(), "-", "")[:20])
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
	input := PublishSnapshotInput{
		ID: uuid.NewString(), RepositoryID: repo.ID, Suite: "stable", Sequence: 1,
		SessionIDs: []string{session.ID}, Actor: "release-operator",
		CreatedAt: time.Date(2026, time.August, 13, 8, 30, 0, 0, time.UTC),
	}
	type publicationResult struct {
		snapshot repository.APTRepositorySnapshot
		err      error
	}
	start := make(chan struct{})
	results := make(chan publicationResult, 2)
	for _, publisher := range []*Publisher{NewPublisher(storeA, objects, deterministicAPTSigner{}), NewPublisher(storeB, objects, deterministicAPTSigner{})} {
		go func() {
			<-start
			snapshot, publishErr := publisher.Publish(ctx, input)
			results <- publicationResult{snapshot: snapshot, err: publishErr}
		}()
	}
	close(start)
	first, second := <-results, <-results
	if first.err != nil || second.err != nil || first.snapshot.ID != input.ID || second.snapshot.ID != input.ID {
		t.Fatalf("concurrent exact replay first=%#v second=%#v", first, second)
	}
	snapshot := first.snapshot
	visible, err := storeB.GetVisibleAPTRepositorySnapshot(ctx, repo.ID, "stable")
	if err != nil || visible.ID != snapshot.ID || visible.ReleaseDigest != snapshot.ReleaseDigest || visible.SignatureAlgorithm != "fixture-sha256" {
		t.Fatalf("cross-instance snapshot=%#v err=%v", visible, err)
	}
	audits, err := storeB.ListAudits(ctx, repository.AuditQuery{Repository: repo.Name, Operation: "apt.repository_snapshot.publish", Limit: 10})
	if err != nil || len(audits) != 1 || audits[0].AuthorizationReason != "signed_snapshot_visible" ||
		audits[0].Evidence["signerIdentity"] != "apt-release@example.test" ||
		audits[0].Evidence["keyFingerprint"] != strings.Repeat("a", 40) ||
		audits[0].Evidence["signatureAlgorithm"] != "fixture-sha256" {
		t.Fatalf("cross-instance immutable signing audit=%#v err=%v", audits, err)
	}
	assets, err := storeB.ListVisibleAPTSnapshotAssets(ctx, repo.ID, "stable")
	if err != nil || len(assets) < 8 {
		t.Fatalf("cross-instance assets=%#v err=%v", assets, err)
	}
	search, err := storeB.SearchArtifactProjection(ctx, repo.ID, repository.FormatAPT, repository.ArtifactSearchQuery{
		Mode: repository.ArtifactSearchByCoordinate, Value: "pool/main/w/widget/",
	}, 10, repository.ArtifactSearchPosition{})
	if err != nil || len(search) != 1 || search[0].Coordinate != "pool/main/w/widget/widget_1.0-1_amd64.deb" || search[0].Digest == "" {
		t.Fatalf("cross-instance search projection=%#v err=%v", search, err)
	}
	capacity, err := storeB.GetRepositoryCapacity(ctx, repo.ID)
	if err != nil || capacity.UsedBytes <= int64(len(deb)) || capacity.ObjectCount <= 1 {
		t.Fatalf("cross-instance capacity=%#v packageSize=%d err=%v", capacity, len(deb), err)
	}
	changed := input
	changed.Suite = "testing"
	changed.SessionIDs = []string{"missing-session"}
	if _, err = NewPublisher(storeB, objects, deterministicAPTSigner{}).Publish(ctx, changed); !errors.Is(err, repository.ErrIdempotencyConflict) {
		t.Fatalf("cross-instance changed replay error=%v", err)
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

	quotaDeb := testDebianPackage(t, "Package: gadget\nVersion: 1.0-1\nArchitecture: amd64\nMaintainer: Gateway Team <gateway@example.test>\nDescription: quota fixture\n")
	quotaSum := sha256.Sum256(quotaDeb)
	quotaSession, _, err := manager.CreateSession(ctx, CreateSessionInput{
		RepositoryID: repo.ID, Suite: "testing", Component: "main", Publisher: "release-ci",
		ObjectName: "gadget_1.0-1_amd64.deb", DeclaredDigest: "sha256:" + hex.EncodeToString(quotaSum[:]),
		DeclaredSize: int64(len(quotaDeb)), ExpectedIdentity: "gadget@1.0-1#amd64", IdempotencyKey: uuid.NewString(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = manager.UploadPackageAs(ctx, quotaSession.ID, quotaSession.ObjectName, bytes.NewReader(quotaDeb), int64(len(quotaDeb)), "release-operator"); err != nil {
		t.Fatal(err)
	}
	quotaInput := PublishSnapshotInput{
		ID: uuid.NewString(), RepositoryID: repo.ID, Suite: "testing", Sequence: 1,
		SessionIDs: []string{quotaSession.ID}, Actor: "release-operator", CreatedAt: time.Now().UTC(),
	}
	quotaPublisher := NewPublisher(storeB, objects, deterministicAPTSigner{})
	quotaPackages, _, err := quotaPublisher.loadPackages(ctx, quotaInput)
	if err != nil {
		t.Fatal(err)
	}
	quotaBundle, err := buildSnapshotBundle(repository.APTRepositorySnapshot{ID: quotaInput.ID, RepositoryID: repo.ID, Suite: "testing", Sequence: 1}, quotaPackages)
	if err != nil {
		t.Fatal(err)
	}
	quotaObjects := make(map[string]int64)
	for _, asset := range quotaBundle.assets {
		if strings.HasPrefix(asset.Path, "dists/") {
			quotaObjects[asset.ObjectKey] = asset.Size
		}
	}
	var testingGenerated int64
	for _, size := range quotaObjects {
		testingGenerated += size
	}
	stableGenerated := capacity.UsedBytes - int64(len(deb))
	quota := int64(len(deb)+len(quotaDeb)) + max(stableGenerated, testingGenerated)
	if _, err = storeA.ReplaceRepositoryCapacityQuota(ctx, repo.ID, quota); err != nil {
		t.Fatal(err)
	}
	if _, err = quotaPublisher.Publish(ctx, quotaInput); !errors.Is(err, repository.ErrQuotaExceeded) {
		t.Fatalf("cross-suite PostgreSQL quota error=%v quota=%d stable=%d testing=%d", err, quota, stableGenerated, testingGenerated)
	}
	if _, err = storeA.ReplaceRepositoryCapacityQuota(ctx, repo.ID, 0); err != nil {
		t.Fatal(err)
	}

	expiredDeb := testDebianPackage(t, "Package: expiring\nVersion: 1.0-1\nArchitecture: amd64\nDescription: signer failure fixture\n")
	expiredSum := sha256.Sum256(expiredDeb)
	expiredSession, _, err := manager.CreateSession(ctx, CreateSessionInput{
		RepositoryID: repo.ID, Suite: "unstable", Component: "main", Publisher: "release-ci",
		ObjectName: "expiring_1.0-1_amd64.deb", DeclaredDigest: "sha256:" + hex.EncodeToString(expiredSum[:]),
		DeclaredSize: int64(len(expiredDeb)), ExpectedIdentity: "expiring@1.0-1#amd64", IdempotencyKey: uuid.NewString(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = manager.UploadPackageAs(ctx, expiredSession.ID, expiredSession.ObjectName, bytes.NewReader(expiredDeb), int64(len(expiredDeb)), "release-operator"); err != nil {
		t.Fatal(err)
	}
	expiredInput := PublishSnapshotInput{
		ID: uuid.NewString(), RepositoryID: repo.ID, Suite: "unstable", Sequence: 1,
		SessionIDs: []string{expiredSession.ID}, Actor: "release-operator", CreatedAt: time.Now().UTC().Add(-2 * time.Hour),
	}
	if _, err = NewPublisher(storeA, objects, deterministicAPTSigner{err: errors.New("signer offline")}).Publish(ctx, expiredInput); !errors.Is(err, ErrSignerUnavailable) {
		t.Fatalf("signer failure error=%v", err)
	}
	if err = storeB.ExpireAPTRepositorySnapshots(ctx, time.Now().UTC().Add(-time.Hour), 10); err != nil {
		t.Fatal(err)
	}
	expired, intents, err := storeA.GetAPTRepositorySnapshot(ctx, expiredInput.ID)
	if err != nil || expired.State != repository.APTRepositorySnapshotFailed || len(intents) != 1 {
		t.Fatalf("expired no-intent snapshot=%#v membership=%#v err=%v", expired, intents, err)
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
		ID: uuid.NewString(), RepositoryID: repo.ID, Suite: "testing", Sequence: 2,
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
	databaseURL, endpoint := os.Getenv("TEST_DATABASE_URL"), os.Getenv("TEST_RUSTFS_ENDPOINT")
	accessKey, secretKey := os.Getenv("TEST_RUSTFS_ACCESS_KEY"), os.Getenv("TEST_RUSTFS_SECRET_KEY")
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
	objects, err := objectstore.NewRustFSStore(endpoint, accessKey, secretKey, "apt-failed-snapshot-"+strings.ReplaceAll(uuid.NewString(), "-", "")[:14])
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
	maintenance := Maintenance{Store: store, Objects: objects, Now: func() time.Time { return time.Now().UTC().Add(2 * time.Hour) }}
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
