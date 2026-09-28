package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"

	"github.com/artifact-gateway/artifact-gateway/internal/protocol/cargo"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

// ReserveCargoPublicationIdentity is the non-public C0 entry point. It parses
// the complete client request before a persistent name/version claim is made.
// No object intent or sparse-index row is written by this function.
func ReserveCargoPublicationIdentity(ctx context.Context, store repository.CargoIdentityStore, repositoryID string, body io.ReaderAt, size int64) (repository.CargoIdentityReservation, bool, error) {
	if store == nil {
		return repository.CargoIdentityReservation{}, false, errors.New("cargo identity store is unavailable")
	}
	envelope, err := cargo.ParsePublishEnvelope(ctx, body, size)
	if err != nil {
		return repository.CargoIdentityReservation{}, false, err
	}
	crateReader := io.NewSectionReader(body, envelope.CrateOffset, envelope.CrateSize)
	crate, err := cargo.ParseCrate(ctx, crateReader, envelope.CrateSize)
	if err != nil {
		return repository.CargoIdentityReservation{}, false, err
	}
	if err = cargo.CrossCheckPublishIdentity(envelope.Metadata, crate); err != nil {
		return repository.CargoIdentityReservation{}, false, err
	}
	crateDigest, err := digestCargoSection(ctx, body, envelope.CrateOffset, envelope.CrateSize)
	if err != nil {
		return repository.CargoIdentityReservation{}, false, err
	}
	metadataDigest, err := digestCargoSection(ctx, body, 4, envelope.CrateOffset-8)
	if err != nil {
		return repository.CargoIdentityReservation{}, false, err
	}
	return store.ReserveCargoIdentity(ctx, repository.CargoIdentityClaim{
		RepositoryID:   repositoryID,
		Name:           crate.Name,
		Version:        crate.Version,
		Digest:         crateDigest,
		MetadataDigest: metadataDigest,
	})
}

func digestCargoSection(ctx context.Context, body io.ReaderAt, offset, size int64) (string, error) {
	checksum := sha256.New()
	section := io.NewSectionReader(body, offset, size)
	var buffer [32 * 1024]byte
	var hashed int64
	for hashed < size {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		n, readErr := section.Read(buffer[:])
		if n > 0 {
			_, _ = checksum.Write(buffer[:n])
			hashed += int64(n)
		}
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return "", readErr
		}
		if n == 0 {
			return "", io.ErrUnexpectedEOF
		}
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return "sha256:" + hex.EncodeToString(checksum.Sum(nil)), nil
}
