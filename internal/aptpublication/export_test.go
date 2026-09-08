package aptpublication

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/objectstore"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

func TestSnapshotArchiveExporterProducesPortableDigestVerifiedArchive(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := repository.NewMemoryStore()
	repo := createAPTHostedRepository(t, ctx, store)
	objects := objectstore.NewMemoryStore()
	session := stageAPTPackage(t, ctx, store, objects, repo.ID, "export-session", "widget", "1:2.0-3")
	snapshot, err := NewPublisher(store, objects, deterministicAPTSigner{}).Publish(ctx, PublishSnapshotInput{
		ID: "00000000-0000-4000-8000-000000000041", RepositoryID: repo.ID, Suite: "stable", Sequence: 1,
		SessionIDs: []string{session.ID}, Actor: "release-operator",
		CreatedAt: time.Date(2026, time.August, 18, 10, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	manifest, err := (SnapshotArchiveExporter{Store: store, Objects: objects}).Export(ctx, snapshot.ID, &output)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.SchemaVersion != "artifact-gateway.dev/apt-snapshot-export/v1" || manifest.Snapshot.ID != snapshot.ID ||
		manifest.Snapshot.RepositoryID != repo.ID || manifest.Snapshot.ReleaseDigest != snapshot.ReleaseDigest ||
		len(manifest.Packages) != 1 || manifest.Packages[0].CanonicalIdentity != "widget@1:2.0-3#amd64" || len(manifest.Assets) < 8 {
		t.Fatalf("manifest=%#v", manifest)
	}

	prepared, err := (SnapshotArchiveExporter{Store: store, Objects: objects}).Prepare(ctx, snapshot.ID)
	if err != nil {
		t.Fatal(err)
	}
	var replay bytes.Buffer
	if err = prepared.WriteTo(ctx, &replay); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(output.Bytes(), replay.Bytes()) || prepared.Size() != int64(output.Len()) {
		t.Fatal("archive replay or declared stream length changed")
	}
	entries := make(map[string][]byte)
	entryOrder := make([]string, 0)
	reader := tar.NewReader(bytes.NewReader(output.Bytes()))
	for {
		header, nextErr := reader.Next()
		if nextErr == io.EOF {
			break
		}
		if nextErr != nil {
			t.Fatal(nextErr)
		}
		body, readErr := io.ReadAll(reader)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if _, exists := entries[header.Name]; exists {
			t.Fatalf("duplicate archive entry %q", header.Name)
		}
		entries[header.Name] = body
		entryOrder = append(entryOrder, header.Name)
	}
	if len(entryOrder) < 2 || entryOrder[0] != "manifest.json" || !slices.IsSorted(entryOrder[1:]) {
		t.Fatalf("entry order=%v", entryOrder)
	}
	var archived SnapshotArchiveManifest
	if err = json.Unmarshal(entries["manifest.json"], &archived); err != nil {
		t.Fatal(err)
	}
	if archived.SchemaVersion != manifest.SchemaVersion || archived.Snapshot.ID != manifest.Snapshot.ID || len(archived.Assets) != len(manifest.Assets) {
		t.Fatalf("archived manifest=%#v", archived)
	}
	for _, asset := range archived.Assets {
		if strings.Contains(asset.Object, repo.ID) || !strings.HasPrefix(asset.Object, "objects/sha256/") {
			t.Fatalf("non-portable object reference %q", asset.Object)
		}
		body, ok := entries[asset.Object]
		if !ok {
			t.Fatalf("missing object %q", asset.Object)
		}
		sum := sha256.Sum256(body)
		if got := "sha256:" + hex.EncodeToString(sum[:]); got != asset.Digest || int64(len(body)) != asset.Size {
			t.Fatalf("asset %q digest=%q size=%d", asset.Path, got, len(body))
		}
	}
}

func TestSnapshotArchiveBoundsCoverMaximumPublishedSnapshot(t *testing.T) {
	t.Parallel()
	const generatedReleaseAssets = 3
	const assetsPerPackageAndUniqueIndexScope = 5
	if maxSnapshotArchiveAssets < maxSnapshotArchivePackages*assetsPerPackageAndUniqueIndexScope+generatedReleaseAssets {
		t.Fatalf("archive asset limit %d cannot represent a valid %d-package snapshot", maxSnapshotArchiveAssets, maxSnapshotArchivePackages)
	}
	if maxSnapshotArchiveManifestBytes < 80<<20 {
		t.Fatalf("archive manifest limit %d cannot represent bounded package and path metadata", maxSnapshotArchiveManifestBytes)
	}
}

func TestSnapshotArchiveExporterPreservesRetiredSnapshotAssets(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := repository.NewMemoryStore()
	repo := createAPTHostedRepository(t, ctx, store)
	objects := objectstore.NewMemoryStore()
	firstSession := stageAPTPackage(t, ctx, store, objects, repo.ID, "retired-export-one", "widget", "1.0-1")
	first, err := NewPublisher(store, objects, deterministicAPTSigner{}).Publish(ctx, PublishSnapshotInput{
		ID: "00000000-0000-4000-8000-000000000042", RepositoryID: repo.ID, Suite: "stable", Sequence: 1,
		SessionIDs: []string{firstSession.ID}, Actor: "release-operator",
		CreatedAt: time.Date(2026, time.August, 18, 10, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	secondSession := stageAPTPackage(t, ctx, store, objects, repo.ID, "retired-export-two", "widget", "2.0-1")
	if _, err = NewPublisher(store, objects, deterministicAPTSigner{}).Publish(ctx, PublishSnapshotInput{
		ID: "00000000-0000-4000-8000-000000000043", RepositoryID: repo.ID, Suite: "stable", Sequence: 2,
		SessionIDs: []string{secondSession.ID}, Actor: "release-operator",
		CreatedAt: time.Date(2026, time.August, 18, 10, 1, 0, 0, time.UTC),
	}); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	manifest, err := (SnapshotArchiveExporter{Store: store, Objects: objects}).Export(ctx, first.ID, &output)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Snapshot.State != repository.APTRepositorySnapshotRetired || len(manifest.Packages) != 1 ||
		manifest.Packages[0].CanonicalIdentity != "widget@1.0-1#amd64" {
		t.Fatalf("retired manifest=%#v", manifest)
	}
	for _, asset := range manifest.Assets {
		if strings.Contains(asset.Path, "2.0-1") {
			t.Fatalf("retired export leaked current asset %q", asset.Path)
		}
	}
}

type aptSnapshotArchiveMutatingStore struct {
	objectstore.Store
	target string
	opens  int
}

func (s *aptSnapshotArchiveMutatingStore) Open(ctx context.Context, key string) (io.ReadCloser, int64, error) {
	reader, size, err := s.Store.Open(ctx, key)
	if err != nil || key != s.target {
		return reader, size, err
	}
	s.opens++
	if s.opens == 1 {
		return reader, size, nil
	}
	body, readErr := io.ReadAll(reader)
	closeErr := reader.Close()
	if readErr != nil {
		return nil, 0, readErr
	}
	if closeErr != nil {
		return nil, 0, closeErr
	}
	body[0] ^= 0xff
	return io.NopCloser(bytes.NewReader(body)), int64(len(body)), nil
}

func TestPreparedSnapshotArchiveRejectsObjectMutationWhileStreaming(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := repository.NewMemoryStore()
	repo := createAPTHostedRepository(t, ctx, store)
	objects := objectstore.NewMemoryStore()
	session := stageAPTPackage(t, ctx, store, objects, repo.ID, "mutating-export", "widget", "2.1-1")
	snapshot, err := NewPublisher(store, objects, deterministicAPTSigner{}).Publish(ctx, PublishSnapshotInput{
		ID: "00000000-0000-4000-8000-000000000065", RepositoryID: repo.ID, Suite: "stable", Sequence: 1,
		SessionIDs: []string{session.ID}, Actor: "release-operator", CreatedAt: time.Date(2026, time.August, 18, 10, 2, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	revision, err := store.GetAPTPackageRevisionForSession(ctx, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	mutating := &aptSnapshotArchiveMutatingStore{Store: objects, target: revision.ObjectKey}
	prepared, err := (SnapshotArchiveExporter{Store: store, Objects: mutating}).Prepare(ctx, snapshot.ID)
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err = prepared.WriteTo(ctx, &output); !errors.Is(err, ErrSnapshotArchiveCorrupt) {
		t.Fatalf("mutated streaming export error=%v", err)
	}
}

func TestSnapshotArchiveVerifierRejectsTamperedAndIncompleteArchives(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := repository.NewMemoryStore()
	repo := createAPTHostedRepository(t, ctx, store)
	objects := objectstore.NewMemoryStore()
	session := stageAPTPackage(t, ctx, store, objects, repo.ID, "verify-export", "widget", "3.0-1")
	snapshot, err := NewPublisher(store, objects, deterministicAPTSigner{}).Publish(ctx, PublishSnapshotInput{
		ID: "00000000-0000-4000-8000-000000000044", RepositoryID: repo.ID, Suite: "stable", Sequence: 1,
		SessionIDs: []string{session.ID}, Actor: "release-operator",
		CreatedAt: time.Date(2026, time.August, 18, 11, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if _, err = (SnapshotArchiveExporter{Store: store, Objects: objects}).Export(ctx, snapshot.ID, &output); err != nil {
		t.Fatal(err)
	}

	verified, err := VerifySnapshotArchive(ctx, bytes.NewReader(output.Bytes()))
	if err != nil || verified.Snapshot.ID != snapshot.ID {
		t.Fatalf("verified=%#v err=%v", verified, err)
	}

	tampered := rewriteSnapshotArchive(t, output.Bytes(), func(name string, body []byte) ([]byte, bool) {
		if !strings.HasPrefix(name, "objects/sha256/") {
			return body, true
		}
		body = append([]byte(nil), body...)
		body[0] ^= 0xff
		return body, false
	})
	if _, err = VerifySnapshotArchive(ctx, bytes.NewReader(tampered)); !errors.Is(err, ErrSnapshotArchiveCorrupt) {
		t.Fatalf("tampered archive error=%v", err)
	}

	incomplete := rewriteSnapshotArchive(t, output.Bytes(), func(name string, body []byte) ([]byte, bool) {
		return body, !strings.HasPrefix(name, "objects/sha256/")
	})
	if _, err = VerifySnapshotArchive(ctx, bytes.NewReader(incomplete)); !errors.Is(err, ErrSnapshotArchiveCorrupt) {
		t.Fatalf("incomplete archive error=%v", err)
	}

	brokenClosure := rewriteSnapshotArchive(t, output.Bytes(), func(name string, body []byte) ([]byte, bool) {
		if name != "manifest.json" {
			return body, true
		}
		var manifest SnapshotArchiveManifest
		if err := json.Unmarshal(body, &manifest); err != nil {
			t.Fatal(err)
		}
		for index, asset := range manifest.Assets {
			if strings.Contains(asset.Path, "/by-hash/SHA256/") {
				manifest.Assets = append(manifest.Assets[:index], manifest.Assets[index+1:]...)
				break
			}
		}
		updated, err := json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		return updated, true
	})
	if _, err = VerifySnapshotArchive(ctx, bytes.NewReader(brokenClosure)); !errors.Is(err, ErrSnapshotArchiveCorrupt) {
		t.Fatalf("broken Release closure error=%v", err)
	}

}

func rewriteSnapshotArchive(t *testing.T, input []byte, mutate func(string, []byte) ([]byte, bool)) []byte {
	t.Helper()
	reader := tar.NewReader(bytes.NewReader(input))
	var output bytes.Buffer
	writer := tar.NewWriter(&output)
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(reader)
		if err != nil {
			t.Fatal(err)
		}
		body, keep := mutate(header.Name, body)
		if !keep {
			continue
		}
		copyHeader := *header
		copyHeader.Size = int64(len(body))
		if err = writer.WriteHeader(&copyHeader); err != nil {
			t.Fatal(err)
		}
		if _, err = writer.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}
