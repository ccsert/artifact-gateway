package app

import rawprotocol "github.com/artifact-gateway/artifact-gateway/internal/protocol/raw"

// Raw cache ownership is in the protocol package. These aliases preserve the
// app composition surface while routes are migrated.
type RawContent = rawprotocol.CachedContent
type RawCache = rawprotocol.Cache

var NewRawCache = rawprotocol.NewCache
var NewDefaultRawCache = rawprotocol.NewDefaultCache

var errRawCacheMiss = rawprotocol.ErrCacheMiss
var errRawCacheNegative = rawprotocol.ErrNegativeCache
