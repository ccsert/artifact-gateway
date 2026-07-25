package cache

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/objectstore"
)

// ErrQuotaExceeded means a valid upstream response cannot be retained because
// its logical repository has exhausted its cache budget.
var ErrQuotaExceeded = errors.New("repository cache quota exceeded")

// Quota coordinates admission across cache indexes. Digest-addressed objects
// are shared, so usage is logical index usage rather than physical bytes.
type Quota struct {
	store       objectstore.Store
	limits      map[string]int64
	mu          sync.Mutex
	coordinator Coordinator
}

type quotaIndex struct {
	Repository string    `json:"repository"`
	Size       int64     `json:"size"`
	Negative   bool      `json:"negative"`
	ExpiresAt  time.Time `json:"expires_at"`
}

func (i *quotaIndex) UnmarshalJSON(data []byte) error {
	type encodedQuotaIndex quotaIndex
	var decoded encodedQuotaIndex
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
		decoded = encodedQuotaIndex(legacy)
	}
	*i = quotaIndex(decoded)
	return nil
}

func NewQuota(store objectstore.Store, limits map[string]int64) *Quota {
	return &Quota{store: store, limits: limits}
}

func (q *Quota) WithCoordinator(coordinator Coordinator) *Quota {
	q.coordinator = coordinator
	return q
}

func (q *Quota) Admit(ctx context.Context, repository, replacingKey string, size int64, publish func() error) error {
	if q == nil || repository == "" {
		return publish()
	}
	limit, configured := q.limits[repository]
	if !configured {
		return publish()
	}
	return q.admit(ctx, repository, replacingKey, size, limit, []string{"oci/index/", "maven/index/", "raw/index/"}, publish)
}

func (q *Quota) AdmitWithLimit(ctx context.Context, repository, replacingKey string, size, limit int64, publish func() error) error {
	if q == nil || repository == "" || limit <= 0 {
		return publish()
	}
	return q.admit(ctx, repository, replacingKey, size, limit, []string{"raw/index/"}, publish)
}

func (q *Quota) AdmitConanWithLimit(ctx context.Context, repository, replacingKey string, size, limit int64, publish func() error) error {
	if q == nil || repository == "" || limit <= 0 {
		return publish()
	}
	return q.admit(ctx, repository, replacingKey, size, limit, []string{"conan/index/"}, publish)
}

func (q *Quota) admit(ctx context.Context, repository, replacingKey string, size, limit int64, prefixes []string, publish func() error) error {
	return q.withAdmissionLock(ctx, repository, func() error {
		used, err := q.usedLocked(ctx, repository, replacingKey, prefixes)
		if err != nil {
			return err
		}
		if size > limit-used {
			return ErrQuotaExceeded
		}
		return publish()
	})
}

func (q *Quota) withAdmissionLock(ctx context.Context, repository string, work func() error) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.coordinator == nil {
		return work()
	}
	for {
		owner, acquired, err := q.coordinator.Acquire(ctx, "cache-quota:"+repository, 35*time.Second)
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

func (q *Quota) usedLocked(ctx context.Context, repository, skipKey string, prefixes []string) (int64, error) {
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
			var index quotaIndex
			if json.Unmarshal(encoded, &index) == nil && !index.Negative && index.Repository == repository && index.Size > 0 && time.Now().UTC().Before(index.ExpiresAt) {
				used += index.Size
			}
		}
	}
	return used, nil
}
