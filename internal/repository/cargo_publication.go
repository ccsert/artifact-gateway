package repository

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/protocol/cargo"
)

// CargoPublication is a committed Hosted version. Reservations and uploaded
// objects are deliberately absent from sparse-index and download reads.
type CargoPublication struct {
	CargoIdentityClaim
	ObjectKey   string
	Size        int64
	IndexRow    []byte
	Publisher   string
	PublishedAt time.Time
	CreatedAt   time.Time
}

type NativeCargoStore interface {
	CargoIdentityStore
	CommitCargoPublication(context.Context, CargoPublication) (CargoPublication, bool, error)
	GetCargoPublication(context.Context, string, string, string) (CargoPublication, error)
	ListCargoPublications(context.Context, string, string) ([]CargoPublication, error)
	CargoObjectHasReference(context.Context, string) (bool, error)
	LockCargoObject(context.Context, string) (func(), error)
}

func normalizeCargoPublication(in CargoPublication) (CargoPublication, CargoIdentityReservation, error) {
	reservation, err := normalizeCargoIdentityClaim(in.CargoIdentityClaim)
	if err != nil || in.Size <= 0 || in.Publisher == "" || in.PublishedAt.IsZero() ||
		in.ObjectKey != "native/cargo/sha256/"+strings.TrimPrefix(in.Digest, "sha256:") || len(in.IndexRow) == 0 {
		return CargoPublication{}, CargoIdentityReservation{}, ErrInvalidCargoIdentity
	}
	in.PublishedAt = in.PublishedAt.UTC().Truncate(time.Second)
	var entry cargo.IndexEntry
	decoder := json.NewDecoder(bytes.NewReader(in.IndexRow))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&entry) != nil || decoder.Decode(&struct{}{}) != io.EOF ||
		entry.Name != in.Name || entry.Version != in.Version || entry.Checksum != strings.TrimPrefix(in.Digest, "sha256:") ||
		entry.PublishedAt != in.PublishedAt.Format(time.RFC3339) || entry.Yanked {
		return CargoPublication{}, CargoIdentityReservation{}, ErrInvalidCargoIdentity
	}
	in.IndexRow, err = json.Marshal(entry)
	if err != nil {
		return CargoPublication{}, CargoIdentityReservation{}, err
	}
	return in, reservation, nil
}

func cargoPublicationMatches(existing, incoming CargoPublication) bool {
	return existing.RepositoryID == incoming.RepositoryID && existing.Name == incoming.Name &&
		existing.Version == incoming.Version && existing.Digest == incoming.Digest &&
		existing.MetadataDigest == incoming.MetadataDigest && existing.ObjectKey == incoming.ObjectKey &&
		existing.Size == incoming.Size && existing.Publisher == incoming.Publisher &&
		existing.PublishedAt.Equal(incoming.PublishedAt) && bytes.Equal(existing.IndexRow, incoming.IndexRow)
}

func cargoReservationMatches(reservation CargoIdentityReservation, publication CargoPublication) bool {
	return reservation.Name == publication.Name && reservation.Version == publication.Version &&
		reservation.Digest == publication.Digest && reservation.MetadataDigest == publication.MetadataDigest &&
		reservation.CreatedAt.UTC().Truncate(time.Second).Equal(publication.PublishedAt)
}

var ErrCargoPublicationConflict = errors.New("cargo version is already published with different content")
