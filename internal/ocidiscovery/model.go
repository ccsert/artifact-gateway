// Package ocidiscovery owns immutable discovery snapshots and subject source
// bindings. It has no transport, HTTP management, or aggregate-store dependency.
package ocidiscovery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"
)

var (
	ErrNotFound = errors.New("oci_discovery_not_found")
	ErrChanged  = errors.New("oci_discovery_source_changed")
	ErrInvalid  = errors.New("oci_discovery_invalid")
	ErrLimit    = errors.New("oci_discovery_limit")
)

const (
	MaxNames     = 10000
	MaxSources   = 128
	MaxPages     = 128
	MaxReferrers = 256
	MaxPayload   = 8 << 20
	SnapshotTTL  = 15 * time.Minute
)

// Guard captures an actual persisted configuration version, never a secret or
// a caller-provided source URL. Stores check guards in the publication/read tx.
type Guard struct {
	Kind    string `json:"kind"`
	ID      string `json:"id"`
	Version string `json:"version"`
}

type Coverage struct {
	Namespace string `json:"namespace"`
	State     string `json:"state"`
	Reason    string `json:"reason,omitempty"`
}

type CatalogEntry struct {
	Name    string   `json:"name"`
	Sources []string `json:"sources"`
}

type Descriptor struct {
	MediaType    string            `json:"mediaType"`
	Digest       string            `json:"digest"`
	Size         int64             `json:"size"`
	ArtifactType string            `json:"artifactType,omitempty"`
	Annotations  map[string]string `json:"annotations,omitempty"`
}

// Snapshot is immutable and principal-bound; cursors only identify this row and
// a lexical position. No cache-name enumeration can create a complete snapshot.
type Snapshot struct {
	ID           string         `json:"id"`
	Owner        string         `json:"owner"`
	Actor        string         `json:"actor"`
	Kind         string         `json:"kind"`
	Guards       []Guard        `json:"guards"`
	Entries      []CatalogEntry `json:"entries,omitempty"`
	Coverage     []Coverage     `json:"coverage"`
	Scope        string         `json:"scope,omitempty"`
	Image        string         `json:"image,omitempty"`
	Subject      string         `json:"subject,omitempty"`
	ArtifactType string         `json:"artifactType,omitempty"`
	Referrers    []Descriptor   `json:"referrers,omitempty"`
	ExpiresAt    time.Time      `json:"expiresAt"`
}

type Object struct {
	Digest    string `json:"digest"`
	MediaType string `json:"mediaType"`
	Size      int64  `json:"size"`
	CacheKey  string `json:"cacheKey"`
	Resource  string `json:"resource"`
}

// Binding fixes the authority of a successful discovery. Reordering a Group
// cannot replace SourceID; removal, source mutation, or permission loss denies
// the read instead of selecting a lower-priority source.
type Binding struct {
	Scope         string       `json:"scope"`
	Owner         string       `json:"owner"`
	Image         string       `json:"image"`
	Subject       string       `json:"subject"`
	SourceID      string       `json:"sourceId"`
	SourceVersion string       `json:"sourceVersion"`
	Actor         string       `json:"actor"`
	Anonymous     bool         `json:"anonymous,omitempty"`
	Guards        []Guard      `json:"guards"`
	Objects       []Object     `json:"objects"`
	Referrers     []Descriptor `json:"referrers"`
	FetchedAt     time.Time    `json:"fetchedAt"`
}

// Store is deliberately outside NativeOCIStore and GatewayStore. Production
// and test composition supply it explicitly; nil never selects a memory store.
type Store interface {
	SaveOCIDiscoverySnapshot(context.Context, Snapshot) error
	GetOCIDiscoverySnapshot(context.Context, string, string) (Snapshot, error)
	PublishOCISubjectBinding(context.Context, Binding) error
	GetOCISubjectBinding(context.Context, string, string, string, string) (Binding, error)
	GetOCIBoundObject(context.Context, string, string, string, string) (Binding, Object, error)
}

// OwnerForActor partitions authenticated authority by the stable subject,
// independently of credential kind or role claims. The namespace is distinct
// from unauthenticated access, even when an actor is literally named anonymous.
func OwnerForActor(actor string) string {
	return ownerDigest("authenticated\x00" + actor)
}

func OwnerForAnonymous() string { return ownerDigest("unauthenticated") }

func ownerDigest(identity string) string {
	digest := sha256.Sum256([]byte(identity))
	return hex.EncodeToString(digest[:])
}

func CloneSnapshot(value Snapshot) Snapshot { return clone(value) }
func CloneBinding(value Binding) Binding    { return clone(value) }

func clone[T any](value T) T {
	data, _ := json.Marshal(value)
	var clone T
	_ = json.Unmarshal(data, &clone)
	return clone
}

func Payload(value any) ([]byte, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, ErrInvalid
	}
	if len(data) > MaxPayload {
		return nil, ErrLimit
	}
	return data, nil
}
