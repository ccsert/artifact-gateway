// Package httperrorrate observes bounded, process-local HTTP counter windows.
package httperrorrate

import (
	"sync"
	"time"
)

const (
	Window                 = 5 * time.Minute
	SampleInterval         = 15 * time.Second
	MaxSampleAge           = 30 * time.Second
	MinimumRequests uint64 = 20
	MaxSafeCount    uint64 = 1<<53 - 1
	// Ten fixed non-probe request classes and the five HTTP status families.
	ClassCount  = 10
	StatusCount = 5
	maxSamples  = int(Window/SampleInterval) + 2
)

// Counters contains cumulative counts; individual cells must never decrease
// within a session. Keeping cells separate detects resets hidden by other traffic.
type Counters [ClassCount][StatusCount]uint64

type Snapshot struct {
	InstanceID, SessionID            string
	CheckedAt                        time.Time
	State, Reason                    string
	SampleAt, WindowStart, WindowEnd *time.Time
	CoverageSeconds                  *float64
	Requests, Errors                 *uint64
	Ratio                            *float64
}

type sample struct {
	at       time.Time
	counters Counters
}

// Observer retains at most 22 samples. It performs no I/O and starts no goroutines.
type Observer struct {
	mu                    sync.Mutex
	instanceID, sessionID string
	samples               []sample
	warmupReason          string
}

func New(instanceID, sessionID string) *Observer {
	return &Observer{instanceID: instanceID, sessionID: sessionID, warmupReason: "warming_up"}
}

// Record accepts at most one sample per interval. A reset starts a new baseline;
// no delta ever crosses a session change, backwards clock, gap or counter reset.
func (o *Observer) Record(at time.Time, sessionID string, counters Counters) {
	o.mu.Lock()
	defer o.mu.Unlock()
	reset := func(reason string) { o.samples = nil; o.warmupReason = reason }
	if o.instanceID == "" || sessionID == "" || at.IsZero() {
		reset("source_unavailable")
		o.sessionID = sessionID
		return
	}
	if sessionID != o.sessionID {
		reset("session_changed")
		o.sessionID = sessionID
	}
	if len(o.samples) > 0 {
		last := o.samples[len(o.samples)-1]
		switch {
		case at.Before(last.at):
			reset("clock_invalid")
		case decreased(last.counters, counters):
			reset("counter_reset")
		case at.Sub(last.at) > MaxSampleAge:
			reset("sampling_gap")
		case at.Sub(last.at) < SampleInterval:
			return
		}
	}
	o.samples = append(o.samples, sample{at: at, counters: counters})
	if len(o.samples) > maxSamples {
		copy(o.samples, o.samples[len(o.samples)-maxSamples:])
		o.samples = o.samples[:maxSamples]
	}
}

func decreased(before, after Counters) bool {
	for c := range before {
		for s := range before[c] {
			if after[c][s] < before[c][s] {
				return true
			}
		}
	}
	return false
}

// Snapshot does not sample. Safe counts use the actual reported boundaries,
// including a partial warmup window or a stale last sample. Ratio is present only
// for a fresh, complete window with at least MinimumRequests requests.
func (o *Observer) Snapshot(now time.Time) Snapshot {
	o.mu.Lock()
	defer o.mu.Unlock()
	r := Snapshot{InstanceID: o.instanceID, SessionID: o.sessionID, CheckedAt: now, State: "unknown", Reason: "source_unavailable"}
	if len(o.samples) == 0 {
		return r
	}
	last := o.samples[len(o.samples)-1]
	r.SampleAt = ptr(last.at)
	if now.Before(last.at) || now.IsZero() {
		r.Reason = "clock_invalid"
		return r
	}
	first := o.samples[0]
	complete := false
	cutoff := last.at.Add(-Window)
	for _, s := range o.samples {
		if s.at.After(cutoff) {
			break
		}
		first = s
		complete = true
	}
	coverage := last.at.Sub(first.at).Seconds()
	r.WindowStart, r.WindowEnd, r.CoverageSeconds = ptr(first.at), ptr(last.at), &coverage
	r.Reason = o.warmupReason
	if coverage > 0 {
		var total, errors uint64
		for c := range last.counters {
			for s := range last.counters[c] {
				delta := last.counters[c][s] - first.counters[c][s]
				if delta > MaxSafeCount-total {
					r.Reason = "count_overflow"
					return r
				}
				total += delta
				if s == StatusCount-1 {
					errors += delta
				}
			}
		}
		r.Requests, r.Errors = &total, &errors
	}
	if now.Sub(last.at) > MaxSampleAge {
		r.State, r.Reason = "stale", "sample_stale"
		return r
	}
	if !complete {
		return r
	}
	switch {
	case *r.Requests == 0:
		r.Reason = "no_traffic"
	case *r.Requests < MinimumRequests:
		r.Reason = "low_sample"
	default:
		r.State, r.Reason = "available", ""
		r.Ratio = ptr(float64(*r.Errors) / float64(*r.Requests))
	}
	return r
}

func ptr[T any](v T) *T { return &v }
