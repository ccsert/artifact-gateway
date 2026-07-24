package app

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"
)

// ErrCacheQuotaExceeded means the upstream response is valid but must not be
// retained because its logical repository has exhausted its cache budget.
var ErrCacheQuotaExceeded = errors.New("repository cache quota exceeded")

// CacheQuota coordinates admission across OCI and Maven indexes. Cache objects
// are digest-addressed and shared, so usage is intentionally logical index
// usage: one repository cannot consume another repository's quota.
type CacheQuota struct {
	store       OCIObjectStore
	limits      map[string]int64
	mu          sync.Mutex
	coordinator OCICacheCoordinator
}

type cacheQuotaIndex struct {
	Repository string    `json:"repository"`
	Size       int64     `json:"size"`
	Negative   bool      `json:"negative"`
	ExpiresAt  time.Time `json:"expires_at"`
}

// UnmarshalJSON retains quota accounting for Raw indexes written before the
// index schema used snake_case field names.
func (i *cacheQuotaIndex) UnmarshalJSON(data []byte) error {
	type encodedCacheQuotaIndex cacheQuotaIndex
	var decoded encodedCacheQuotaIndex
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	if decoded.ExpiresAt.IsZero() {
		var legacy struct {
			Repository string
			Size       int64
			Negative   bool
			ExpiresAt  time.Time
		}
		if err := json.Unmarshal(data, &legacy); err != nil {
			return err
		}
		decoded = encodedCacheQuotaIndex(legacy)
	}
	*i = cacheQuotaIndex(decoded)
	return nil
}

func NewCacheQuota(store OCIObjectStore, limits map[string]int64) *CacheQuota {
	return &CacheQuota{store: store, limits: limits}
}

func (q *CacheQuota) WithCoordinator(coordinator OCICacheCoordinator) *CacheQuota {
	q.coordinator = coordinator
	return q
}

func (q *CacheQuota) Admit(ctx context.Context, repository, replacingKey string, size int64, publish func() error) error {
	if q == nil || repository == "" {
		return publish()
	}
	limit, configured := q.limits[repository]
	if !configured {
		return publish()
	}
	return q.admit(ctx, repository, replacingKey, size, limit, publish)
}

// AdmitWithLimit admits an entry against a durable group-level limit. It is
// used by Raw because its cache budget belongs to the group configuration.
func (q *CacheQuota) AdmitWithLimit(ctx context.Context, repository, replacingKey string, size, limit int64, publish func() error) error {
	if q == nil || repository == "" || limit <= 0 {
		return publish()
	}
	return q.admitRaw(ctx, repository, replacingKey, size, limit, publish)
}

func (q *CacheQuota) admit(ctx context.Context, repository, replacingKey string, size, limit int64, publish func() error) error {
	return q.withAdmissionLock(ctx, repository, func() error {
		used, err := q.usedLocked(ctx, repository, replacingKey, []string{"oci/index/", "maven/index/", "raw/index/"})
		if err != nil {
			return err
		}
		if size > limit-used {
			return ErrCacheQuotaExceeded
		}
		return publish()
	})
}

func (q *CacheQuota) admitRaw(ctx context.Context, repository, replacingKey string, size, limit int64, publish func() error) error {
	return q.withAdmissionLock(ctx, repository, func() error {
		used, err := q.usedLocked(ctx, repository, replacingKey, []string{"raw/index/"})
		if err != nil {
			return err
		}
		if size > limit-used {
			return ErrCacheQuotaExceeded
		}
		return publish()
	})
}

func (q *CacheQuota) withAdmissionLock(ctx context.Context, repository string, work func() error) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.coordinator == nil {
		return work()
	}
	for {
		owner, acquired, err := q.coordinator.Acquire(ctx, "cache-quota:"+repository, rawDistributedLockLease)
		if err != nil {
			return err
		}
		if acquired {
			defer func() {
				releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
				defer cancel()
				_ = q.coordinator.Release(releaseCtx, "cache-quota:"+repository, owner)
			}()
			return work()
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(20 * time.Millisecond):
		}
	}
}

func (q *CacheQuota) AdmitConanWithLimit(ctx context.Context, repository, replacingKey string, size, limit int64, publish func() error) error {
	if q == nil || repository == "" || limit <= 0 {
		return publish()
	}
	return q.withAdmissionLock(ctx, repository, func() error {
		used, err := q.usedLocked(ctx, repository, replacingKey, []string{"conan/index/"})
		if err != nil {
			return err
		}
		if size > limit-used {
			return ErrCacheQuotaExceeded
		}
		return publish()
	})
}

func (q *CacheQuota) usedLocked(ctx context.Context, repository, skipKey string, prefixes []string) (int64, error) {
	var used int64
	for _, prefix := range prefixes {
		keys, err := q.store.List(ctx, prefix)
		if err != nil {
			return 0, err
		}
		for _, key := range keys {
			if key == skipKey {
				continue
			}
			encoded, err := q.store.Get(ctx, key)
			if err != nil {
				continue
			}
			var index cacheQuotaIndex
			if json.Unmarshal(encoded, &index) == nil && !index.Negative && index.Repository == repository && index.Size > 0 && time.Now().UTC().Before(index.ExpiresAt) {
				used += index.Size
			}
		}
	}
	return used, nil
}
