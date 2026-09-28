package repository

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/protocol/cargo"
)

// CargoProxyIndex stores the upstream sparse response without rewriting its
// version rows. Status is 200, 404, 410, or 451; only a 200 has a body.
type CargoProxyIndex struct {
	RepositoryID string
	Name         string
	Body         []byte
	Status       int
	ETag         string
	Modified     string
	FetchedAt    time.Time
	ExpiresAt    time.Time
}

type CargoProxyConfig struct {
	RepositoryID     string
	DownloadTemplate string
	SearchAPI        string
	FetchedAt        time.Time
	ExpiresAt        time.Time
}

type CargoProxyCrate struct {
	RepositoryID string
	Name         string
	Version      string
	Checksum     string
	ObjectKey    string
	Size         int64
	CachedAt     time.Time
}

type CargoProxyStore interface {
	GetCargoProxyConfig(context.Context, string) (CargoProxyConfig, error)
	PutCargoProxyConfig(context.Context, CargoProxyConfig) error
	GetCargoProxyIndex(context.Context, string, string) (CargoProxyIndex, error)
	PutCargoProxyIndex(context.Context, CargoProxyIndex) error
	GetCargoProxyCrate(context.Context, string, string, string) (CargoProxyCrate, error)
	PutCargoProxyCrate(context.Context, CargoProxyCrate) error
}

type cargoProxyRow struct {
	VersionKey string
	Checksum   string
	Immutable  []byte
}

func cargoProxyIndexRows(name string, body []byte) (map[string]cargoProxyRow, error) {
	if len(body) == 0 || len(body) > 16<<20 {
		return nil, ErrInvalidCargoIdentity
	}
	requested, err := cargo.NormalizeIdentity(name, "0.0.0")
	if err != nil {
		return nil, ErrInvalidCargoIdentity
	}
	rows := make(map[string]cargoProxyRow)
	for _, line := range bytes.Split(bytes.TrimSuffix(body, []byte("\n")), []byte("\n")) {
		if len(line) == 0 || len(line) > 1<<20 {
			return nil, ErrInvalidCargoIdentity
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(line, &fields); err != nil || len(fields) == 0 {
			return nil, ErrInvalidCargoIdentity
		}
		var rowName, version, checksum string
		yanked := fields["yanked"]
		if json.Unmarshal(fields["name"], &rowName) != nil || json.Unmarshal(fields["vers"], &version) != nil ||
			json.Unmarshal(fields["cksum"], &checksum) != nil || (!bytes.Equal(yanked, []byte("true")) && !bytes.Equal(yanked, []byte("false"))) ||
			len(checksum) != 64 || strings.ToLower(checksum) != checksum {
			return nil, ErrInvalidCargoIdentity
		}
		if _, err := hex.DecodeString(checksum); err != nil {
			return nil, ErrInvalidCargoIdentity
		}
		identity, err := cargo.NormalizeIdentity(rowName, version)
		if err != nil || identity.CollisionKey != requested.CollisionKey {
			return nil, ErrInvalidCargoIdentity
		}
		if _, exists := rows[identity.VersionKey]; exists {
			return nil, ErrInvalidCargoIdentity
		}
		delete(fields, "yanked")
		immutable, err := json.Marshal(fields)
		if err != nil {
			return nil, err
		}
		rows[identity.VersionKey] = cargoProxyRow{VersionKey: identity.VersionKey, Checksum: checksum, Immutable: immutable}
	}
	return rows, nil
}

func validateCargoProxyIndex(incoming CargoProxyIndex, previous *CargoProxyIndex) error {
	if incoming.RepositoryID == "" || incoming.Name == "" || incoming.FetchedAt.IsZero() ||
		!incoming.ExpiresAt.After(incoming.FetchedAt) {
		return ErrInvalidCargoIdentity
	}
	if incoming.Status != 200 {
		if incoming.Status != 404 && incoming.Status != 410 && incoming.Status != 451 || len(incoming.Body) != 0 {
			return ErrInvalidCargoIdentity
		}
		if previous != nil && previous.Status == 200 {
			return ErrUpstreamChanged
		}
		return nil
	}
	nextRows, err := cargoProxyIndexRows(incoming.Name, incoming.Body)
	if err != nil {
		return err
	}
	if previous == nil || previous.Status != 200 {
		return nil
	}
	priorRows, err := cargoProxyIndexRows(previous.Name, previous.Body)
	if err != nil {
		return err
	}
	for key, previousRow := range priorRows {
		nextRow, exists := nextRows[key]
		if !exists || !bytes.Equal(previousRow.Immutable, nextRow.Immutable) {
			return ErrUpstreamChanged
		}
	}
	return nil
}

func cargoProxyChecksum(index CargoProxyIndex, version string) (string, error) {
	if index.Status != 200 {
		return "", ErrNotFound
	}
	identity, err := cargo.NormalizeIdentity(index.Name, version)
	if err != nil {
		return "", ErrInvalidCargoIdentity
	}
	rows, err := cargoProxyIndexRows(index.Name, index.Body)
	if err != nil {
		return "", err
	}
	row, found := rows[identity.VersionKey]
	if !found {
		return "", ErrNotFound
	}
	return row.Checksum, nil
}

// CargoProxyIndexChecksum returns the archive digest bound to a cached version.
func CargoProxyIndexChecksum(index CargoProxyIndex, version string) (string, error) {
	return cargoProxyChecksum(index, version)
}

func validateCargoProxyCrate(crate CargoProxyCrate, index CargoProxyIndex) error {
	if crate.RepositoryID == "" || crate.ObjectKey != "native/cargo-proxy/sha256/"+crate.Checksum ||
		crate.Size <= 0 || crate.CachedAt.IsZero() {
		return ErrInvalidCargoIdentity
	}
	identity, err := cargo.NormalizeIdentity(crate.Name, crate.Version)
	if err != nil {
		return ErrInvalidCargoIdentity
	}
	indexIdentity, err := cargo.NormalizeIdentity(index.Name, crate.Version)
	if err != nil || identity.CollisionKey != indexIdentity.CollisionKey {
		return ErrInvalidCargoIdentity
	}
	want, err := cargoProxyChecksum(index, crate.Version)
	if errors.Is(err, ErrNotFound) {
		return ErrUpstreamChanged
	}
	if err != nil {
		return err
	}
	if crate.Checksum != want {
		return ErrUpstreamChanged
	}
	return nil
}
