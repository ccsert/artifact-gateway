package repository

import (
	"bytes"
	"encoding/json"

	"github.com/artifact-gateway/artifact-gateway/internal/protocol/cargo"
)

type cargoGroupKnownIndex struct {
	RepositoryID string
	Body         []byte
}

type cargoGroupFingerprint struct{ checksum, immutable string }

type cargoGroupPreflight struct {
	groupID string
	seen    map[string]cargoGroupFingerprint
}

func newCargoGroupPreflight(groupID string) *cargoGroupPreflight {
	return &cargoGroupPreflight{groupID: groupID, seen: make(map[string]cargoGroupFingerprint)}
}

func (p *cargoGroupPreflight) addVersion(value CargoGroupVersion) error {
	versionKey, immutable, err := cargoGroupIdentity(value, p.groupID, value.Name)
	if err != nil {
		return err
	}
	identity, err := cargo.NormalizeIdentity(value.Name, value.Version)
	if err != nil {
		return ErrInvalidCargoIdentity
	}
	key := identity.CollisionKey + "\x00" + versionKey
	current := cargoGroupFingerprint{checksum: value.Checksum, immutable: immutable}
	if prior, found := p.seen[key]; found && prior != current {
		return ErrCargoGroupConflict
	}
	p.seen[key] = current
	return nil
}

func (p *cargoGroupPreflight) addIndex(index cargoGroupKnownIndex) error {
	for _, row := range bytes.Split(bytes.TrimSuffix(index.Body, []byte("\n")), []byte("\n")) {
		if len(row) == 0 {
			continue
		}
		var identity struct {
			Name     string `json:"name"`
			Version  string `json:"vers"`
			Checksum string `json:"cksum"`
		}
		if json.Unmarshal(row, &identity) != nil {
			return ErrInvalidCargoIdentity
		}
		if err := p.addVersion(CargoGroupVersion{GroupID: p.groupID, SourceRepositoryID: index.RepositoryID,
			Name: identity.Name, Version: identity.Version, Checksum: identity.Checksum, IndexRow: row}); err != nil {
			return err
		}
	}
	return nil
}

// validateCargoGroupKnownVersions checks every coordinate that is already
// visible in a Hosted publication, a Proxy index cache, or this Group's owner
// ledger. Sparse upstreams cannot enumerate unrequested crates; their unknown
// coordinates are still checked by the normal first-read reconciliation.
func validateCargoGroupKnownVersions(groupID string, owners []CargoGroupVersion, indexes []cargoGroupKnownIndex) error {
	preflight := newCargoGroupPreflight(groupID)
	for _, owner := range owners {
		if err := preflight.addVersion(owner); err != nil {
			return err
		}
	}
	for _, index := range indexes {
		if err := preflight.addIndex(index); err != nil {
			return err
		}
	}
	return nil
}
