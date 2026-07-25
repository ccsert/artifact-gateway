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
type S3OCIObjectStore = objectstore.S3Store
type CachedOCIContent = ociprotocol.CachedContent
type OCICache = ociprotocol.Cache

var NewMemoryOCIObjectStore = objectstore.NewMemoryStore
var NewS3OCIObjectStore = objectstore.NewS3Store
var NewOCICache = ociprotocol.NewCache
var NewDefaultOCICache = ociprotocol.NewDefaultCache

var errOCICacheMiss = ociprotocol.ErrCacheMiss
var errOCICacheNegative = ociprotocol.ErrNegativeCache
