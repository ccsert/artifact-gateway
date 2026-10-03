// Package localcapacity observes only explicitly selected filesystems in the
// current process's mount namespace. It does not measure an object store,
// calculate Repository quotas or discover backend physical storage pools.
package localcapacity

import (
	"context"
	"errors"
	"math"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Alias string

const (
	Temporary Alias = "temporary"
	Logs      Alias = "logs"
	Backups   Alias = "backups"
)

var aliases = [...]Alias{Temporary, Logs, Backups}

type Status string

const (
	Available     Status = "available"
	Unknown       Status = "unknown"
	NotConfigured Status = "not_configured"
	Stale         Status = "stale"
)

type Reason string

const (
	ReasonNotConfigured             Reason = "not_configured"
	ReasonRemoteFilesystem          Reason = "remote_filesystem"
	ReasonUnsupportedFilesystem     Reason = "unsupported_filesystem"
	ReasonUnsupportedPlatform       Reason = "unsupported_platform"
	ReasonReadFailed                Reason = "read_failed"
	ReasonFilesystemIdentityUnknown Reason = "filesystem_identity_unknown"
	ReasonInvalidMeasurement        Reason = "invalid_measurement"
	ReasonUnitOverflow              Reason = "unit_overflow"
	ReasonTimeout                   Reason = "timeout"
	ReasonCancelled                 Reason = "cancelled"
)

var ErrConfiguration = errors.New("invalid local capacity observation configuration")
var ErrUnsupportedPlatform = errors.New("local capacity observation unsupported on this platform")

type Kind string

const (
	Supported   Kind = "supported"
	Remote      Kind = "remote"
	Unsupported Kind = "unsupported"
)

// Filesystem is private provider evidence, never a diagnostics response.
// Key identifies a filesystem within this observer's namespace only.
type Filesystem struct {
	Kind                                           Kind
	Key                                            string
	BlockSize, Blocks, FreeBlocks, AvailableBlocks uint64
}

type Options struct {
	Probe func(string) (Filesystem, error)
	Now   func() time.Time
	// Timeout is a response bound, not a claim that the OS syscall is killable.
	// The fixed production default and maximum are one second.
	Timeout time.Duration
}

type MountObservation struct {
	Alias          Alias
	Status         Status
	Reason         Reason
	SampleAt       *time.Time
	TotalBytes     *int64
	AvailableBytes *int64
	SharedWith     []Alias
}

type Snapshot struct {
	CheckedAt                                                        time.Time
	Source, Scope, Unit                                              string
	RefreshIntervalSeconds, MaxSampleAgeSeconds, TimeoutMilliseconds int
	Mounts                                                           []MountObservation
}

type observation struct {
	at               time.Time
	completedAt      time.Time
	status           Status
	reason           Reason
	key              string
	total, available int64
}

type slot struct {
	path     string
	done     chan struct{}
	last     *observation
	lastGood *observation
}

// Observer owns at most one outstanding probe per configured alias. Request
// cancellation cannot accumulate goroutines for an uninterruptible syscall.
// Configuration is copied once; no request can introduce another path.
type Observer struct {
	mu      sync.Mutex
	slots   [len(aliases)]slot
	probe   func(string) (Filesystem, error)
	now     func() time.Time
	timeout time.Duration
}

const refreshInterval = 15 * time.Second
const maxSampleAge = time.Minute

func New(paths map[Alias]string, options Options) (*Observer, error) {
	if err := validatePaths(paths); err != nil {
		return nil, err
	}
	if options.Timeout == 0 {
		options.Timeout = time.Second
	}
	if options.Timeout < time.Millisecond || options.Timeout > time.Second {
		return nil, ErrConfiguration
	}
	if options.Probe == nil {
		options.Probe = nativeProbe
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	o := &Observer{probe: options.Probe, now: options.Now, timeout: options.Timeout}
	for i, alias := range aliases {
		o.slots[i].path = paths[alias]
	}
	return o, nil
}

func validatePaths(paths map[Alias]string) error {
	if len(paths) > len(aliases) {
		return ErrConfiguration
	}
	for alias, path := range paths {
		if alias != Temporary && alias != Logs && alias != Backups {
			return ErrConfiguration
		}
		if len(path) > 4096 || strings.ContainsRune(path, 0) || !filepath.IsAbs(path) || filepath.Clean(path) != path {
			return ErrConfiguration
		}
	}
	return nil
}

func (o *Observer) Snapshot(ctx context.Context) Snapshot {
	// Omitted dependencies report explicit not_configured observations and never
	// infer paths. Configured runtimes retain one observer for the process lifetime.
	if o == nil {
		o = &Observer{now: time.Now, timeout: time.Second}
	}
	requestContext := ctx
	ctx, cancel := context.WithTimeout(ctx, o.timeout)
	defer cancel()
	deadline, _ := ctx.Deadline()
	var pending [len(aliases)]chan struct{}
	var values [len(aliases)]*observation
	var configured [len(aliases)]bool
	o.mu.Lock()
	for i := range o.slots {
		s := &o.slots[i]
		configured[i] = s.path != ""
		if !configured[i] || ctx.Err() != nil {
			continue
		}
		now := o.now().UTC()
		if s.last != nil && !s.last.at.After(now) && now.Sub(s.last.at) < refreshInterval {
			values[i] = s.last
			continue
		}
		if s.done == nil {
			s.done = make(chan struct{})
			go o.sample(i, s.path, s.done)
		}
		pending[i] = s.done
	}
	o.mu.Unlock()
	for i, done := range pending {
		if done == nil {
			continue
		}
		select {
		case <-done:
			o.mu.Lock()
			values[i] = o.slots[i].last
			o.mu.Unlock()
		case <-ctx.Done():
			// Another alias may have completed while this request was waiting
			// for a blocked one. Retain only work completed within the bound.
			select {
			case <-done:
				o.mu.Lock()
				values[i] = o.slots[i].last
				o.mu.Unlock()
			default:
			}
		}
	}
	now := o.now().UTC()
	result := Snapshot{CheckedAt: now, Source: "statfs", Scope: "observer_mount_namespace", Unit: "bytes", Mounts: make([]MountObservation, len(aliases))}
	result.RefreshIntervalSeconds = int(refreshInterval / time.Second)
	result.MaxSampleAgeSeconds = int(maxSampleAge / time.Second)
	result.TimeoutMilliseconds = int(o.timeout / time.Millisecond)
	var keys [len(aliases)]string
	for i, alias := range aliases {
		m := MountObservation{Alias: alias, Status: NotConfigured, Reason: ReasonNotConfigured, SharedWith: []Alias{}}
		if !configured[i] {
			result.Mounts[i] = m
			continue
		}
		m.Status, m.Reason = Unknown, ReasonTimeout
		if requestContext.Err() != nil || values[i] == nil || values[i].completedAt.After(deadline) {
			if errors.Is(requestContext.Err(), context.Canceled) {
				m.Reason = ReasonCancelled
			} else {
				o.mu.Lock()
				lastGood := o.slots[i].lastGood
				o.mu.Unlock()
				if lastGood != nil && !lastGood.at.After(now) && now.Sub(lastGood.at) > maxSampleAge {
					m.Status, m.SampleAt = Stale, timePointer(lastGood.at)
				}
			}
		} else if v := values[i]; v != nil {
			m.Status, m.Reason = v.status, v.reason
			if v.at.IsZero() || v.at.After(now) {
				m.Status, m.Reason = Unknown, ReasonInvalidMeasurement
			} else if v.status == Available {
				m.SampleAt = timePointer(v.at)
				if now.Sub(v.at) > maxSampleAge {
					m.Status, m.Reason = Stale, ReasonTimeout
				} else {
					m.TotalBytes, m.AvailableBytes = intPointer(v.total), intPointer(v.available)
					keys[i] = v.key
				}
			}
		}
		result.Mounts[i] = m
	}
	for i := range keys {
		if keys[i] == "" {
			continue
		}
		for j := range keys {
			if i != j && keys[i] == keys[j] {
				result.Mounts[i].SharedWith = append(result.Mounts[i].SharedWith, aliases[j])
			}
		}
	}
	return result
}

func (o *Observer) sample(index int, path string, done chan struct{}) {
	fs, err := o.probe(path)
	v := normalize(fs, err)
	v.at = o.now().UTC()
	v.completedAt = time.Now()
	o.mu.Lock()
	s := &o.slots[index]
	s.last = &v
	if v.status == Available {
		s.lastGood = &v
	}
	s.done = nil
	close(done)
	o.mu.Unlock()
}

func normalize(fs Filesystem, err error) observation {
	v := observation{status: Unknown, reason: ReasonReadFailed}
	if err != nil {
		if errors.Is(err, ErrUnsupportedPlatform) {
			v.reason = ReasonUnsupportedPlatform
		}
		return v
	}
	switch fs.Kind {
	case Remote:
		v.reason = ReasonRemoteFilesystem
		return v
	case Supported:
	case Unsupported:
		v.reason = ReasonUnsupportedFilesystem
		return v
	default:
		v.reason = ReasonUnsupportedFilesystem
		return v
	}
	if fs.Key == "" {
		v.reason = ReasonFilesystemIdentityUnknown
		return v
	}
	if fs.BlockSize == 0 || fs.Blocks == 0 || fs.AvailableBlocks > fs.FreeBlocks || fs.FreeBlocks > fs.Blocks {
		v.reason = ReasonInvalidMeasurement
		return v
	}
	if fs.BlockSize > math.MaxInt64 || fs.Blocks > math.MaxInt64/fs.BlockSize {
		v.reason = ReasonUnitOverflow
		return v
	}
	v.status, v.reason, v.key = Available, "", fs.Key
	v.total = int64(fs.Blocks * fs.BlockSize)
	v.available = int64(fs.AvailableBlocks * fs.BlockSize)
	return v
}

func timePointer(value time.Time) *time.Time { return &value }
func intPointer(value int64) *int64          { return &value }
