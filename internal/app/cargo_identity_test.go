package app

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"testing"

	"github.com/artifact-gateway/artifact-gateway/internal/protocol/cargo"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

func TestReserveCargoPublicationIdentityValidatesBeforeWriting(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	if _, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: "repo", Name: "cargo-c0", Format: repository.FormatRaw}); err != nil {
		t.Fatal(err)
	}
	body, crate := cargoC0PublishFixture(t, "demo", "1.0.0", "demo")
	first, replay, err := ReserveCargoPublicationIdentity(ctx, store, "repo", bytes.NewReader(body), int64(len(body)))
	checksum := sha256.Sum256(crate)
	if err != nil || replay || first.Name != "demo" || first.Digest != "sha256:"+hex.EncodeToString(checksum[:]) {
		t.Fatalf("first=%+v replay=%t err=%v", first, replay, err)
	}
	if _, replay, err := ReserveCargoPublicationIdentity(ctx, store, "repo", bytes.NewReader(body), int64(len(body))); err != nil || !replay {
		t.Fatalf("exact retry replay=%t err=%v", replay, err)
	}
	changedMetadata, sameCrate := cargoC0PublishFixture(t, "demo", "1.0.0", "demo", "different-author")
	if !bytes.Equal(crate, sameCrate) {
		t.Fatal("metadata-only fixture changed crate bytes")
	}
	if _, _, err := ReserveCargoPublicationIdentity(ctx, store, "repo", bytes.NewReader(changedMetadata), int64(len(changedMetadata))); !errors.Is(err, repository.ErrCargoIdentityConflict) {
		t.Fatalf("metadata-only change was treated as exact retry: %v", err)
	}

	invalidCases := []struct {
		name string
		body []byte
	}{
		{name: "trailing bytes", body: append(append([]byte(nil), body...), 'x')},
		{name: "truncated archive", body: body[:len(body)-1]},
	}
	corruptArchive := append([]byte(nil), body...)
	corruptArchive[len(corruptArchive)-1] ^= 0xff
	invalidCases = append(invalidCases, struct {
		name string
		body []byte
	}{name: "corrupt archive", body: corruptArchive})
	mismatch, _ := cargoC0PublishFixture(t, "different", "1.0.0", "demo")
	invalidCases = append(invalidCases, struct {
		name string
		body []byte
	}{name: "manifest mismatch", body: mismatch})
	for _, testCase := range invalidCases {
		t.Run(testCase.name, func(t *testing.T) {
			invalidStore := repository.NewMemoryStore()
			if _, err := invalidStore.CreateHostedRepository(ctx, repository.HostedRepository{ID: "repo", Name: "cargo-c0", Format: repository.FormatRaw}); err != nil {
				t.Fatal(err)
			}
			if _, _, err := ReserveCargoPublicationIdentity(ctx, invalidStore, "repo", bytes.NewReader(testCase.body), int64(len(testCase.body))); err == nil {
				t.Fatal("invalid publish created a reservation")
			}
			if _, err := invalidStore.GetCargoIdentityReservation(ctx, "repo", "demo", "1.0.0"); !errors.Is(err, repository.ErrNotFound) {
				t.Fatalf("invalid publish left identity state: %v", err)
			}
		})
	}
}

func cargoC0PublishFixture(t *testing.T, metadataName, version, crateName string, authors ...string) ([]byte, []byte) {
	t.Helper()
	var crate bytes.Buffer
	gzipWriter := gzip.NewWriter(&crate)
	tarWriter := tar.NewWriter(gzipWriter)
	manifest := []byte("[package]\nname = \"" + crateName + "\"\nversion = \"" + version + "\"\n")
	header := &tar.Header{Name: crateName + "-" + version + "/Cargo.toml", Mode: 0o644, Size: int64(len(manifest)), Typeflag: tar.TypeReg}
	if err := tarWriter.WriteHeader(header); err != nil {
		t.Fatal(err)
	}
	if _, err := tarWriter.Write(manifest); err != nil {
		t.Fatal(err)
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	metadata, err := json.Marshal(cargo.PublishMetadata{
		Name: metadataName, Version: version,
		Dependencies: []cargo.PublishDependency{}, Features: map[string][]string{}, Authors: authors,
	})
	if err != nil {
		t.Fatal(err)
	}
	var body bytes.Buffer
	if err := binary.Write(&body, binary.LittleEndian, uint32(len(metadata))); err != nil {
		t.Fatal(err)
	}
	if _, err := body.Write(metadata); err != nil {
		t.Fatal(err)
	}
	if err := binary.Write(&body, binary.LittleEndian, uint32(crate.Len())); err != nil {
		t.Fatal(err)
	}
	if _, err := body.Write(crate.Bytes()); err != nil {
		t.Fatal(err)
	}
	return body.Bytes(), crate.Bytes()
}
