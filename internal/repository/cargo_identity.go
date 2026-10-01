package repository

import (
	"context"
	"regexp"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/protocol/cargo"
)

// CargoIdentityStore reserves a crate/version before any public index or
// object intent exists. It is an internal C0 boundary, not a format profile.
type CargoIdentityStore interface {
	// The bool reports an identical existing reservation, not a completed publication.
	ReserveCargoIdentity(context.Context, CargoIdentityClaim) (CargoIdentityReservation, bool, error)
	GetCargoIdentityReservation(context.Context, string, string, string) (CargoIdentityReservation, error)
}

type CargoIdentityClaim struct {
	RepositoryID   string
	Name           string
	Version        string
	Digest         string
	MetadataDigest string
}

type CargoIdentityReservation struct {
	CargoIdentityClaim
	NormalizedName string
	CollisionKey   string
	VersionKey     string
	CreatedAt      time.Time
}

var cargoIdentityDigestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

func normalizeCargoIdentityClaim(claim CargoIdentityClaim) (CargoIdentityReservation, error) {
	if claim.RepositoryID == "" || !cargoIdentityDigestPattern.MatchString(claim.Digest) || !cargoIdentityDigestPattern.MatchString(claim.MetadataDigest) {
		return CargoIdentityReservation{}, ErrInvalidCargoIdentity
	}
	normalized, err := cargo.NormalizeIdentity(claim.Name, claim.Version)
	if err != nil {
		return CargoIdentityReservation{}, ErrInvalidCargoIdentity
	}
	return CargoIdentityReservation{
		CargoIdentityClaim: claim,
		NormalizedName:     normalized.NormalizedName,
		CollisionKey:       normalized.CollisionKey,
		VersionKey:         normalized.VersionKey,
	}, nil
}

func cargoNameReservationKey(repositoryID, collisionKey string) string {
	return repositoryID + "\x00" + collisionKey
}

func cargoVersionReservationKey(repositoryID, collisionKey, versionKey string) string {
	return cargoNameReservationKey(repositoryID, collisionKey) + "\x00" + versionKey
}
