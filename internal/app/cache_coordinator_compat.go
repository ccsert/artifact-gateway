package app

import cache "github.com/artifact-gateway/artifact-gateway/internal/cache"

// Compatibility aliases retain the app composition surface while cache
// coordination is owned by its protocol-independent module.
type OCICacheCoordinator = cache.Coordinator
type PostgresCacheCoordinator = cache.PostgresCoordinator
type CacheQuota = cache.Quota

var NewPostgresCacheCoordinator = cache.NewPostgresCoordinator
var NewPostgresCacheCoordinatorWithPools = cache.NewPostgresCoordinatorWithPools
var NewCacheQuota = cache.NewQuota
var ErrCacheQuotaExceeded = cache.ErrQuotaExceeded
