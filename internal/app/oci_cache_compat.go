package app

import (
	"github.com/artifact-gateway/artifact-gateway/internal/objectstore"
	ociprotocol "github.com/artifact-gateway/artifact-gateway/internal/protocol/oci"
)

// Compatibility aliases retain the app composition surface while OCI cache
// behavior is owned by the protocol module.
type OCIObjectStore = objectstore.Store
type OCIObjectInfo = objectstore.Info
type MemoryOCIObjectStore = objectstore.MemoryStore
type RustFSOCIObjectStore = objectstore.RustFSStore
type CachedOCIContent = ociprotocol.CachedContent
type OCICache = ociprotocol.Cache

var NewMemoryOCIObjectStore = objectstore.NewMemoryStore
var NewRustFSOCIObjectStore = objectstore.NewRustFSStore
var NewOCICache = ociprotocol.NewCache
var NewDefaultOCICache = ociprotocol.NewDefaultCache

var errOCICacheMiss = ociprotocol.ErrCacheMiss
var errOCICacheNegative = ociprotocol.ErrNegativeCache
