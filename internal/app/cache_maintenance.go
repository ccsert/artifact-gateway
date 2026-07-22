package app

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// CacheMaintenance reports cache capacity and runs OCI's retention collector
// outside request handling. Maven cache entries expire on reads; OCI also has
// digest-addressed objects that must be collected after their grace period.
type CacheMaintenance struct {
	store OCIObjectStore
	oci   *OCICache
	raw   *RawCache
	conan *ConanCache

	mu     sync.RWMutex
	status CacheMaintenanceStatus
}

type CacheMaintenanceStatus struct {
	ObjectCount       int64     `json:"object_count"`
	Bytes             int64     `json:"bytes"`
	PendingCandidates int64     `json:"pending_candidates"`
	LastStartedAt     time.Time `json:"last_started_at,omitempty"`
	LastCompletedAt   time.Time `json:"last_completed_at,omitempty"`
	LastError         string    `json:"last_error,omitempty"`
	SuccessfulRuns    uint64    `json:"successful_runs"`
	FailedRuns        uint64    `json:"failed_runs"`
}

func NewCacheMaintenance(store OCIObjectStore, oci *OCICache) *CacheMaintenance {
	return &CacheMaintenance{store: store, oci: oci}
}
func NewCacheMaintenanceWithRaw(store OCIObjectStore, oci *OCICache, raw *RawCache) *CacheMaintenance {
	return &CacheMaintenance{store: store, oci: oci, raw: raw}
}
func (m *CacheMaintenance) WithConan(conan *ConanCache) *CacheMaintenance { m.conan = conan; return m }

func (m *CacheMaintenance) Status(ctx context.Context) (CacheMaintenanceStatus, error) {
	status := m.snapshot()
	objects, err := m.store.List(ctx, "oci/objects/")
	if err != nil {
		return CacheMaintenanceStatus{}, fmt.Errorf("list OCI cache objects: %w", err)
	}
	var bytes int64
	for _, object := range objects {
		info, statErr := m.store.Stat(ctx, object)
		if statErr != nil {
			return CacheMaintenanceStatus{}, fmt.Errorf("stat OCI cache object: %w", statErr)
		}
		bytes += info.Size
	}
	status.ObjectCount, status.Bytes = int64(len(objects)), bytes
	candidates, err := m.store.List(ctx, "oci/gc/")
	if err != nil {
		return CacheMaintenanceStatus{}, fmt.Errorf("list OCI cleanup candidates: %w", err)
	}
	status.PendingCandidates = int64(len(candidates))
	return status, nil
}

func (m *CacheMaintenance) Run(ctx context.Context) error {
	m.mu.Lock()
	m.status.LastStartedAt = time.Now().UTC()
	m.status.LastError = ""
	m.mu.Unlock()

	err := m.oci.CollectGarbage(ctx)
	if err == nil && m.raw != nil {
		err = m.raw.CollectGarbage(ctx)
	}
	if err == nil && m.conan != nil {
		err = m.conan.CollectGarbage(ctx)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.status.LastCompletedAt = time.Now().UTC()
	if err != nil {
		m.status.FailedRuns++
		m.status.LastError = err.Error()
		return err
	}
	m.status.SuccessfulRuns++
	return nil
}

func (m *CacheMaintenance) Start(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		return
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_ = m.Run(ctx)
			}
		}
	}()
}

func (m *CacheMaintenance) snapshot() CacheMaintenanceStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.status
}
