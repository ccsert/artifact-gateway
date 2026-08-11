package repository

import (
	"context"
	"reflect"
	"testing"
	"time"
)

type recordingDistributionAdmissionStore struct {
	events []string
}

func (s *recordingDistributionAdmissionStore) LockArtifactScanIdentity(_ context.Context, _ string, _ Format, _ string, digest string) (func(), error) {
	s.events = append(s.events, "lock:"+digest)
	return func() { s.events = append(s.events, "unlock:"+digest) }, nil
}

func (s *recordingDistributionAdmissionStore) GetArtifactQuarantine(_ context.Context, _ string, _ Format, _ string, digest string) (ArtifactQuarantine, error) {
	s.events = append(s.events, "check:"+digest)
	return ArtifactQuarantine{}, ErrNotFound
}

func (*recordingDistributionAdmissionStore) ReplaceArtifactQuarantine(_ context.Context, value ArtifactQuarantine, _ string) (ArtifactQuarantine, error) {
	return value, nil
}

func TestLockArtifactDistributionAdmissionForDigestsOrdersAndDeduplicatesLocks(t *testing.T) {
	ctx := context.Background()
	store := &recordingDistributionAdmissionStore{}
	digestA := "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	digestB := "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

	release, err := LockArtifactDistributionAdmissionForDigests(ctx, store, "source", FormatRaw, "widget.bin", []string{digestB, digestA, digestB})
	if err != nil {
		t.Fatal(err)
	}
	release()
	release()

	want := []string{
		"lock:" + digestA,
		"lock:" + digestB,
		"check:" + digestA,
		"check:" + digestB,
		"unlock:" + digestB,
		"unlock:" + digestA,
	}
	if !reflect.DeepEqual(store.events, want) {
		t.Fatalf("admission events=%v want=%v", store.events, want)
	}
}

func TestPyPIDistributionAdmissionUsesOneCoordinateLock(t *testing.T) {
	ctx := context.Background()
	store := &recordingDistributionAdmissionStore{}
	digestA := "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	digestB := "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

	release, err := LockArtifactDistributionAdmissionForDigests(ctx, store, "source", FormatPyPI, "widget@1.0.0", []string{digestB, digestA})
	if err != nil {
		t.Fatal(err)
	}
	release()

	want := []string{
		"lock:" + artifactDistributionUnitDigest,
		"check:" + digestA,
		"check:" + digestB,
		"unlock:" + artifactDistributionUnitDigest,
	}
	if !reflect.DeepEqual(store.events, want) {
		t.Fatalf("admission events=%v want=%v", store.events, want)
	}
}

func TestDistributionCoordinateContextReusesNestedTargetLock(t *testing.T) {
	ctx := context.Background()
	store := &recordingDistributionAdmissionStore{}
	coordinate := "widget@1.0.0"

	lockedCtx, release, err := LockArtifactDistributionCoordinates(ctx, store, []ArtifactDistributionCoordinate{
		{RepositoryID: "source", Format: FormatPyPI, Coordinate: coordinate},
		{RepositoryID: "target", Format: FormatPyPI, Coordinate: coordinate},
	})
	if err != nil {
		t.Fatal(err)
	}
	nestedRelease, err := lockArtifactDistributionCoordinates(lockedCtx, store, "target", FormatPyPI, []string{coordinate})
	if err != nil {
		t.Fatal(err)
	}
	nestedRelease()
	release()
	afterRelease, err := lockArtifactDistributionCoordinates(lockedCtx, store, "target", FormatPyPI, []string{coordinate})
	if err != nil {
		t.Fatal(err)
	}
	afterRelease()

	locks, unlocks := 0, 0
	for _, event := range store.events {
		switch event {
		case "lock:" + artifactDistributionUnitDigest:
			locks++
		case "unlock:" + artifactDistributionUnitDigest:
			unlocks++
		}
	}
	if locks != 3 || unlocks != 3 {
		t.Fatalf("nested coordinate locks=%d unlocks=%d events=%v", locks, unlocks, store.events)
	}
}

func TestNestedDistributionCoordinateContextDoesNotOutliveOuterLease(t *testing.T) {
	ctx := context.Background()
	store := &recordingDistributionAdmissionStore{}
	coordinate := ArtifactDistributionCoordinate{RepositoryID: "target", Format: FormatPyPI, Coordinate: "widget@1.0.0"}

	outerCtx, releaseOuter, err := LockArtifactDistributionCoordinates(ctx, store, []ArtifactDistributionCoordinate{coordinate})
	if err != nil {
		t.Fatal(err)
	}
	innerCtx, releaseInner, err := LockArtifactDistributionCoordinates(outerCtx, store, []ArtifactDistributionCoordinate{coordinate})
	if err != nil {
		t.Fatal(err)
	}
	releaseOuter()
	afterCtx, releaseAfter, err := LockArtifactDistributionCoordinates(innerCtx, store, []ArtifactDistributionCoordinate{coordinate})
	if err != nil {
		t.Fatal(err)
	}
	releaseAfter()
	releaseInner()
	_ = afterCtx

	locks, unlocks := 0, 0
	for _, event := range store.events {
		switch event {
		case "lock:" + artifactDistributionUnitDigest:
			locks++
		case "unlock:" + artifactDistributionUnitDigest:
			unlocks++
		}
	}
	if locks != 2 || unlocks != 2 {
		t.Fatalf("locks=%d unlocks=%d events=%v", locks, unlocks, store.events)
	}
}

func TestPyPIFilePublicationWaitsForDistributionCoordinateLock(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	coordinate := "widget@1.0.0"
	release, err := LockArtifactDistributionUnit(ctx, store, "source", FormatPyPI, coordinate, nil)
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		close(started)
		_, publishErr := store.PublishPyPIFile(ctx, PyPIFile{
			RepositoryID: "source", Project: "widget", Version: "1.0.0", Filename: "widget-1.0.0.tar.gz",
			Digest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", ObjectKey: "native/pypi/widget", Size: 1,
		})
		done <- publishErr
	}()
	<-started
	select {
	case publishErr := <-done:
		release()
		t.Fatalf("PyPI file publication bypassed coordinate lock: %v", publishErr)
	case <-time.After(50 * time.Millisecond):
	}
	release()
	select {
	case publishErr := <-done:
		if publishErr != nil {
			t.Fatal(publishErr)
		}
	case <-time.After(time.Second):
		t.Fatal("PyPI file publication did not resume after coordinate release")
	}
}
