package localcapacity_test

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/localcapacity"
)

func TestUnconfiguredObserverDoesNotProbeDefaultLocations(t *testing.T) {
	var calls atomic.Int32
	o := observer(t, nil, localcapacity.Options{Probe: func(string) (localcapacity.Filesystem, error) {
		calls.Add(1)
		return localcapacity.Filesystem{}, errors.New("unexpected probe")
	}})
	s := o.Snapshot(context.Background())
	if calls.Load() != 0 || len(s.Mounts) != 3 || s.Source != "statfs" || s.Scope != "observer_mount_namespace" || s.Unit != "bytes" {
		t.Fatalf("unconfigured observation=%#v calls=%d", s, calls.Load())
	}
	for _, m := range s.Mounts {
		if m.Status != localcapacity.NotConfigured || m.Reason != localcapacity.ReasonNotConfigured || m.SampleAt != nil || m.TotalBytes != nil || m.AvailableBytes != nil {
			t.Fatalf("unconfigured mount=%#v", m)
		}
	}
}

func TestObservationUsesAvailableBlocksAndIdentifiesOnlyKnownSharedFilesystem(t *testing.T) {
	paths := map[localcapacity.Alias]string{localcapacity.Temporary: "/synthetic/private-temp", localcapacity.Logs: "/synthetic/private-logs", localcapacity.Backups: "/synthetic/private-backups"}
	var calls atomic.Int32
	o := observer(t, paths, localcapacity.Options{Probe: func(path string) (localcapacity.Filesystem, error) {
		calls.Add(1)
		key := "private-device-shared"
		if path == "/synthetic/private-backups" {
			key = "private-device-other"
		}
		return localcapacity.Filesystem{Kind: localcapacity.Supported, Key: key, BlockSize: 4096, Blocks: 100, FreeBlocks: 30, AvailableBlocks: 20}, nil
	}})
	// The observer must retain only the explicit startup configuration.
	paths[localcapacity.Temporary] = "/unrequested/location"
	s := o.Snapshot(context.Background())
	if calls.Load() != 3 || len(s.Mounts) != 3 {
		t.Fatalf("observation=%#v calls=%d", s, calls.Load())
	}
	for _, m := range s.Mounts {
		if m.Status != localcapacity.Available || m.Reason != "" || m.SampleAt == nil || m.TotalBytes == nil || *m.TotalBytes != 409600 || m.AvailableBytes == nil || *m.AvailableBytes != 81920 {
			t.Fatalf("available mount=%#v", m)
		}
	}
	if len(s.Mounts[0].SharedWith) != 1 || s.Mounts[0].SharedWith[0] != localcapacity.Logs || len(s.Mounts[1].SharedWith) != 1 || s.Mounts[1].SharedWith[0] != localcapacity.Temporary || len(s.Mounts[2].SharedWith) != 0 {
		t.Fatalf("shared filesystem projection=%#v", s.Mounts)
	}
	data, err := json.Marshal(s)
	if err != nil || strings.Contains(string(data), "private-") || strings.Contains(string(data), "/synthetic") || strings.Contains(string(data), "device") {
		t.Fatalf("private coordinates escaped: %s error=%v", data, err)
	}
}

func TestUnknownObservationsOmitValuesAndPrivateErrors(t *testing.T) {
	valid := localcapacity.Filesystem{Kind: localcapacity.Supported, Key: "private-fs", BlockSize: 4096, Blocks: 100, FreeBlocks: 30, AvailableBlocks: 20}
	tests := []struct {
		name   string
		fs     localcapacity.Filesystem
		err    error
		reason localcapacity.Reason
	}{
		{"remote", localcapacity.Filesystem{Kind: localcapacity.Remote, BlockSize: 4096, Blocks: 100}, nil, localcapacity.ReasonRemoteFilesystem},
		{"unsupported filesystem", localcapacity.Filesystem{Kind: localcapacity.Unsupported}, nil, localcapacity.ReasonUnsupportedFilesystem},
		{"unreadable", valid, errors.New("s3://private-secret@private-host/path"), localcapacity.ReasonReadFailed},
		{"unsupported platform", valid, localcapacity.ErrUnsupportedPlatform, localcapacity.ReasonUnsupportedPlatform},
		{"unknown identity", localcapacity.Filesystem{Kind: localcapacity.Supported, BlockSize: 4096, Blocks: 100}, nil, localcapacity.ReasonFilesystemIdentityUnknown},
		{"zero unit", localcapacity.Filesystem{Kind: localcapacity.Supported, Key: "private-fs", Blocks: 100}, nil, localcapacity.ReasonInvalidMeasurement},
		{"no filesystem geometry", localcapacity.Filesystem{Kind: localcapacity.Supported, Key: "private-fs", BlockSize: 4096}, nil, localcapacity.ReasonInvalidMeasurement},
		{"inconsistent counts", localcapacity.Filesystem{Kind: localcapacity.Supported, Key: "private-fs", BlockSize: 1, Blocks: 5, FreeBlocks: 6}, nil, localcapacity.ReasonInvalidMeasurement},
		{"available exceeds free", localcapacity.Filesystem{Kind: localcapacity.Supported, Key: "private-fs", BlockSize: 1, Blocks: 5, FreeBlocks: 2, AvailableBlocks: 3}, nil, localcapacity.ReasonInvalidMeasurement},
		{"overflow", localcapacity.Filesystem{Kind: localcapacity.Supported, Key: "private-fs", BlockSize: 4096, Blocks: math.MaxUint64}, nil, localcapacity.ReasonUnitOverflow},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := observer(t, map[localcapacity.Alias]string{localcapacity.Temporary: "/synthetic/private-secret"}, localcapacity.Options{Probe: func(string) (localcapacity.Filesystem, error) { return tt.fs, tt.err }})
			s := o.Snapshot(context.Background())
			m := s.Mounts[0]
			if m.Status != localcapacity.Unknown || m.Reason != tt.reason || m.TotalBytes != nil || m.AvailableBytes != nil || m.SampleAt != nil || len(m.SharedWith) != 0 {
				t.Fatalf("unknown mount=%#v", m)
			}
			data, _ := json.Marshal(s)
			if strings.Contains(string(data), "private-") || strings.Contains(string(data), "s3://") {
				t.Fatalf("private error escaped: %s", data)
			}
		})
	}
}

func TestFullFilesystemReportsKnownZeroAvailableBytes(t *testing.T) {
	o := observer(t, map[localcapacity.Alias]string{localcapacity.Temporary: "/synthetic/full"}, localcapacity.Options{Probe: func(string) (localcapacity.Filesystem, error) {
		return localcapacity.Filesystem{Kind: localcapacity.Supported, Key: "fs", BlockSize: 4096, Blocks: 100}, nil
	}})
	m := o.Snapshot(context.Background()).Mounts[0]
	if m.Status != localcapacity.Available || m.AvailableBytes == nil || *m.AvailableBytes != 0 || m.TotalBytes == nil || *m.TotalBytes != 409600 {
		t.Fatalf("full filesystem=%#v", m)
	}
}

func TestCancelledObservationDoesNotStartFilesystemProbe(t *testing.T) {
	var calls atomic.Int32
	o := observer(t, map[localcapacity.Alias]string{localcapacity.Temporary: "/synthetic/temp"}, localcapacity.Options{Probe: func(string) (localcapacity.Filesystem, error) { calls.Add(1); return localcapacity.Filesystem{}, nil }})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	m := o.Snapshot(ctx).Mounts[0]
	if calls.Load() != 0 || m.Status != localcapacity.Unknown || m.Reason != localcapacity.ReasonCancelled {
		t.Fatalf("cancelled mount=%#v calls=%d", m, calls.Load())
	}
}

func TestBlockedProbesRemainBoundedAcrossConcurrentRequests(t *testing.T) {
	var calls atomic.Int32
	started := make(chan struct{}, 3)
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	o := observer(t, map[localcapacity.Alias]string{localcapacity.Temporary: "/synthetic/temp", localcapacity.Logs: "/synthetic/logs", localcapacity.Backups: "/synthetic/backups"}, localcapacity.Options{Timeout: 100 * time.Millisecond, Probe: func(string) (localcapacity.Filesystem, error) {
		calls.Add(1)
		started <- struct{}{}
		<-release
		return localcapacity.Filesystem{Kind: localcapacity.Supported, Key: "fs", BlockSize: 1, Blocks: 100}, nil
	}})
	first := make(chan localcapacity.Snapshot, 1)
	go func() { first <- o.Snapshot(context.Background()) }()
	for range 3 {
		select {
		case <-started:
		case <-time.After(2 * time.Second):
			t.Fatal("configured probes did not start")
		}
	}
	var wg sync.WaitGroup
	results := make(chan localcapacity.Snapshot, 32)
	for range 32 {
		wg.Add(1)
		go func() { defer wg.Done(); results <- o.Snapshot(context.Background()) }()
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(results); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("blocked provider exceeded the observation response bound")
	}
	if calls.Load() != 3 {
		t.Fatalf("blocked provider spawned extra probes: %d", calls.Load())
	}
	for s := range results {
		for _, m := range s.Mounts {
			if m.Status != localcapacity.Unknown || m.Reason != localcapacity.ReasonTimeout || m.TotalBytes != nil || m.AvailableBytes != nil {
				t.Fatalf("blocked observation=%#v", m)
			}
		}
	}
	select {
	case <-first:
	case <-time.After(2 * time.Second):
		t.Fatal("initial observation remained blocked")
	}
}

func TestExpiredGoodSampleIsStaleWhenRefreshCannotFinish(t *testing.T) {
	base := time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC)
	var current atomic.Int64
	current.Store(base.UnixNano())
	var calls atomic.Int32
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	o := observer(t, map[localcapacity.Alias]string{localcapacity.Temporary: "/synthetic/temp"}, localcapacity.Options{Timeout: 50 * time.Millisecond, Now: func() time.Time { return time.Unix(0, current.Load()) }, Probe: func(string) (localcapacity.Filesystem, error) {
		if calls.Add(1) > 1 {
			<-release
		}
		return localcapacity.Filesystem{Kind: localcapacity.Supported, Key: "fs", BlockSize: 4096, Blocks: 100}, nil
	}})
	if first := o.Snapshot(context.Background()).Mounts[0]; first.Status != localcapacity.Available {
		t.Fatalf("initial sample=%#v", first)
	}
	current.Store(base.Add(2 * time.Minute).UnixNano())
	m := o.Snapshot(context.Background()).Mounts[0]
	if m.Status != localcapacity.Stale || m.Reason != localcapacity.ReasonTimeout || m.SampleAt == nil || !m.SampleAt.Equal(base) || m.TotalBytes != nil || m.AvailableBytes != nil {
		t.Fatalf("expired sample became a fresh value: %#v", m)
	}
}

func TestBlockedMountDoesNotEraseSuccessfulIndependentObservation(t *testing.T) {
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	o := observer(t, map[localcapacity.Alias]string{localcapacity.Temporary: "/synthetic/blocked", localcapacity.Logs: "/synthetic/logs"}, localcapacity.Options{Timeout: 100 * time.Millisecond, Probe: func(path string) (localcapacity.Filesystem, error) {
		if path == "/synthetic/blocked" {
			<-release
		}
		return localcapacity.Filesystem{Kind: localcapacity.Supported, Key: "fs", BlockSize: 4096, Blocks: 100, FreeBlocks: 20, AvailableBlocks: 10}, nil
	}})
	s := o.Snapshot(context.Background())
	if s.Mounts[0].Status != localcapacity.Unknown || s.Mounts[0].Reason != localcapacity.ReasonTimeout || s.Mounts[1].Status != localcapacity.Available || s.Mounts[1].AvailableBytes == nil || *s.Mounts[1].AvailableBytes != 40960 {
		t.Fatalf("one timeout corrupted another source: %#v", s.Mounts)
	}
}

func TestFailedRefreshDoesNotReusePreviousCapacity(t *testing.T) {
	base := time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC)
	var current atomic.Int64
	current.Store(base.UnixNano())
	var calls atomic.Int32
	o := observer(t, map[localcapacity.Alias]string{localcapacity.Temporary: "/synthetic/temp"}, localcapacity.Options{Now: func() time.Time { return time.Unix(0, current.Load()) }, Probe: func(string) (localcapacity.Filesystem, error) {
		if calls.Add(1) > 1 {
			return localcapacity.Filesystem{}, errors.New("private-path: unavailable")
		}
		return localcapacity.Filesystem{Kind: localcapacity.Supported, Key: "fs", BlockSize: 4096, Blocks: 100}, nil
	}})
	if first := o.Snapshot(context.Background()).Mounts[0]; first.Status != localcapacity.Available {
		t.Fatalf("initial sample=%#v", first)
	}
	current.Store(base.Add(20 * time.Second).UnixNano())
	m := o.Snapshot(context.Background()).Mounts[0]
	if m.Status != localcapacity.Unknown || m.Reason != localcapacity.ReasonReadFailed || m.AvailableBytes != nil || m.TotalBytes != nil || m.SampleAt != nil {
		t.Fatalf("failed refresh reused old capacity: %#v", m)
	}
}

func TestConfigurationErrorsDoNotExposePathsOrArbitraryAliases(t *testing.T) {
	for _, paths := range []map[localcapacity.Alias]string{{localcapacity.Temporary: "relative-private-path"}, {localcapacity.Temporary: "s3://private-secret@bucket/path"}, {"private-label": "/private-secret"}, {localcapacity.Temporary: "/private-secret/../other"}} {
		_, err := localcapacity.New(paths, localcapacity.Options{})
		if !errors.Is(err, localcapacity.ErrConfiguration) || strings.Contains(err.Error(), "private") || strings.Contains(err.Error(), "s3://") {
			t.Fatalf("unsafe configuration error: %v", err)
		}
	}
}

func observer(t *testing.T, paths map[localcapacity.Alias]string, options localcapacity.Options) *localcapacity.Observer {
	t.Helper()
	o, err := localcapacity.New(paths, options)
	if err != nil {
		t.Fatal(err)
	}
	return o
}
