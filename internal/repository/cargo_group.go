package repository

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sort"
	"strings"

	"github.com/artifact-gateway/artifact-gateway/internal/protocol/cargo"
)

var ErrCargoGroupConflict = errors.New("cargo group version conflicts with another member")

// CargoGroupVersion binds the index row and download checksum to one member.
// Once visible, its source may not change because Cargo locks the registry
// source rather than the individual member repository.
type CargoGroupVersion struct {
	GroupID            string
	SourceRepositoryID string
	Name               string
	Version            string
	Checksum           string
	IndexRow           []byte
}

type CargoGroupStore interface {
	ReconcileCargoGroupIndex(context.Context, string, string, []CargoGroupVersion) ([]CargoGroupVersion, error)
	GetCargoGroupVersion(context.Context, string, string, string) (CargoGroupVersion, error)
}

func cargoGroupPublicationCompatible(owner CargoGroupVersion, publication CargoPublication) bool {
	candidate := CargoGroupVersion{GroupID: owner.GroupID, SourceRepositoryID: publication.RepositoryID,
		Name: publication.Name, Version: publication.Version, Checksum: strings.TrimPrefix(publication.Digest, "sha256:"),
		IndexRow: publication.IndexRow}
	_, oldHash, oldErr := cargoGroupIdentity(owner, owner.GroupID, owner.Name)
	_, newHash, newErr := cargoGroupIdentity(candidate, owner.GroupID, owner.Name)
	return oldErr == nil && newErr == nil && owner.Checksum == candidate.Checksum && oldHash == newHash
}

func cargoGroupIdentity(value CargoGroupVersion, groupID, name string) (string, string, error) {
	identity, err := cargo.NormalizeIdentity(value.Name, value.Version)
	requested, requestedErr := cargo.NormalizeIdentity(name, value.Version)
	if err != nil || requestedErr != nil || value.GroupID != groupID || value.SourceRepositoryID == "" ||
		identity.CollisionKey != requested.CollisionKey || len(value.IndexRow) == 0 {
		return "", "", ErrInvalidCargoIdentity
	}
	rows, err := cargoProxyIndexRows(value.Name, value.IndexRow)
	row, found := rows[identity.VersionKey]
	if err != nil || len(rows) != 1 || !found || row.Checksum != value.Checksum {
		return "", "", ErrInvalidCargoIdentity
	}
	sum := sha256.Sum256(row.Immutable)
	return identity.VersionKey, hex.EncodeToString(sum[:]), nil
}

func reconcileCargoGroupVersions(groupID, name string, candidates, existing []CargoGroupVersion, memberIDs map[string]bool) ([]CargoGroupVersion, error) {
	prior := make(map[string]CargoGroupVersion, len(existing))
	for _, value := range existing {
		key, _, err := cargoGroupIdentity(value, groupID, name)
		if err != nil {
			return nil, err
		}
		prior[key] = value
	}
	options := make(map[string][]CargoGroupVersion)
	order := make([]string, 0)
	for _, value := range candidates {
		key, _, err := cargoGroupIdentity(value, groupID, name)
		if err != nil || !memberIDs[value.SourceRepositoryID] {
			return nil, ErrInvalidCargoIdentity
		}
		if _, seen := options[key]; !seen {
			order = append(order, key)
		}
		options[key] = append(options[key], value)
	}
	if len(options) == 0 && len(prior) == 0 {
		return nil, ErrNotFound
	}
	for key := range prior {
		if len(options[key]) == 0 {
			return nil, ErrUpstreamChanged
		}
	}
	sort.Strings(order)
	chosen := make([]CargoGroupVersion, 0, len(order))
	for _, key := range order {
		choices := options[key]
		_, firstHash, _ := cargoGroupIdentity(choices[0], groupID, name)
		for _, other := range choices[1:] {
			_, hash, _ := cargoGroupIdentity(other, groupID, name)
			if hash != firstHash || other.Checksum != choices[0].Checksum {
				return nil, ErrCargoGroupConflict
			}
		}
		selected := choices[0]
		if old, found := prior[key]; found {
			matched := false
			for _, option := range choices {
				if option.SourceRepositoryID == old.SourceRepositoryID {
					selected, matched = option, true
					break
				}
			}
			if !matched {
				return nil, ErrUpstreamChanged
			}
			_, oldHash, _ := cargoGroupIdentity(old, groupID, name)
			if firstHash != oldHash || selected.Checksum != old.Checksum {
				return nil, ErrUpstreamChanged
			}
		}
		selected.IndexRow = bytes.Clone(selected.IndexRow)
		chosen = append(chosen, selected)
	}
	return chosen, nil
}
