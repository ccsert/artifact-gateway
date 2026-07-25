package app

import mavenprotocol "github.com/artifact-gateway/artifact-gateway/internal/protocol/maven"

// Compatibility aliases retain the app composition surface while Maven cache
// behavior is owned by the protocol module.
type CachedMavenContent = mavenprotocol.CachedContent
type MavenCache = mavenprotocol.Cache

var NewMavenCache = mavenprotocol.NewCache
var NewDefaultMavenCache = mavenprotocol.NewDefaultCache

var errMavenCacheMiss = mavenprotocol.ErrCacheMiss
var errMavenCacheNegative = mavenprotocol.ErrNegativeCache
