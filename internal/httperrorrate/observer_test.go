package httperrorrate_test

import (
	"sync"
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/httperrorrate"
)

var epoch = time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)

func window(total, errors uint64) (*httperrorrate.Observer, time.Time) {
	o := httperrorrate.New("node-a", "session-a")
	var counts httperrorrate.Counters
	counts[0][1] = 100 // Lifetime traffic before observation is not included.
	o.Record(epoch, "session-a", counts)
	for i := 1; i <= 20; i++ {
		if i == 20 {
			counts[0][1] += total - errors
			counts[0][4] += errors
		}
		o.Record(epoch.Add(time.Duration(i)*15*time.Second), "session-a", counts)
	}
	return o, epoch.Add(5 * time.Minute)
}

func TestMinimumSampleAndActualWindow(t *testing.T) {
	for _, tc := range []struct {
		total, errors uint64
		state, reason string
	}{
		{0, 0, "unknown", "no_traffic"}, {19, 2, "unknown", "low_sample"},
		{20, 0, "available", ""}, {20, 2, "available", ""},
	} {
		o, now := window(tc.total, tc.errors)
		s := o.Snapshot(now)
		if s.State != tc.state || s.Reason != tc.reason || s.Requests == nil || *s.Requests != tc.total || s.Errors == nil || *s.Errors != tc.errors || *s.CoverageSeconds != 300 {
			t.Fatalf("snapshot: %#v", s)
		}
		if tc.state == "available" {
			if s.Ratio == nil || *s.Ratio != float64(tc.errors)/float64(tc.total) {
				t.Fatal("wrong ratio")
			}
		} else if s.Ratio != nil {
			t.Fatal("insufficient data has a ratio")
		}
		if !s.WindowStart.Equal(epoch) || !s.WindowEnd.Equal(now) {
			t.Fatal("wrong boundaries")
		}
	}
}

func TestFreshnessAndFutureClock(t *testing.T) {
	o, now := window(20, 5)
	if o.Snapshot(now.Add(30*time.Second)).Ratio == nil {
		t.Fatal("freshness boundary")
	}
	stale := o.Snapshot(now.Add(30*time.Second + time.Nanosecond))
	if stale.State != "stale" || stale.Ratio != nil || *stale.Requests != 20 || *stale.Errors != 5 || !stale.SampleAt.Equal(now) {
		t.Fatalf("stale: %#v", stale)
	}
	future := o.Snapshot(now.Add(-time.Nanosecond))
	if future.Reason != "clock_invalid" || future.Requests != nil || future.Ratio != nil {
		t.Fatal("future sample trusted")
	}
}

func TestWarmupResetAndGapNeverCrossBaseline(t *testing.T) {
	for _, reason := range []string{"session_changed", "counter_reset", "sampling_gap", "clock_invalid"} {
		t.Run(reason, func(t *testing.T) {
			o, now := window(20, 2)
			var counts httperrorrate.Counters
			counts[0][1] = 118
			counts[0][4] = 2
			at, session := now.Add(15*time.Second), "session-a"
			switch reason {
			case "session_changed":
				session = "session-b"
				counts[0][1] = 1000
			case "counter_reset":
				counts[0][4] = 1
				counts[1][1] = 1000 // An individual reset masked in the aggregate.
			case "sampling_gap":
				at = now.Add(30*time.Second + time.Nanosecond)
			case "clock_invalid":
				at = now.Add(-time.Second)
			}
			o.Record(at, session, counts)
			s := o.Snapshot(at)
			if s.Reason != reason || s.Ratio != nil || s.Requests != nil {
				t.Fatalf("reset: %#v", s)
			}
			counts[1][3] += 25 // 4xx belongs only to the denominator.
			o.Record(at.Add(15*time.Second), session, counts)
			s = o.Snapshot(at.Add(15 * time.Second))
			if *s.Requests != 25 || *s.Errors != 0 || s.Ratio != nil || *s.CoverageSeconds != 15 {
				t.Fatalf("partial reset window: %#v", s)
			}
		})
	}
}

func TestBoundedRepeatedSamplingAndNonAlignedWindow(t *testing.T) {
	o := httperrorrate.New("node", "session")
	var counts httperrorrate.Counters
	for i := 0; i <= 40; i++ {
		counts[0][1] = uint64(i) * 20
		at := epoch.Add(time.Duration(i) * 16 * time.Second)
		o.Record(at, "session", counts)
		for range 100 {
			o.Record(at, "session", counts)
			o.Snapshot(at)
		}
	}
	s := o.Snapshot(epoch.Add(640 * time.Second))
	if s.Ratio == nil || *s.CoverageSeconds != 304 || *s.Requests != 380 {
		t.Fatalf("actual window: %#v", s)
	}
}

func TestSafeIntegerBoundaryAndMissingIdentity(t *testing.T) {
	for _, n := range []uint64{httperrorrate.MaxSafeCount, httperrorrate.MaxSafeCount + 1} {
		o, now := window(n, 1)
		s := o.Snapshot(now)
		if n == httperrorrate.MaxSafeCount {
			if s.Ratio == nil || *s.Requests != n {
				t.Fatal("safe count rejected")
			}
		} else if s.Reason != "count_overflow" || s.Requests != nil || s.Ratio != nil {
			t.Fatal("unsafe count exposed")
		}
	}
	o := httperrorrate.New("", "session")
	o.Record(epoch, "session", httperrorrate.Counters{})
	if s := o.Snapshot(epoch); s.Reason != "source_unavailable" || s.SampleAt != nil {
		t.Fatal("missing identity trusted")
	}
}

func TestConcurrentRecordAndSnapshot(t *testing.T) {
	o := httperrorrate.New("node", "session")
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 500 {
				o.Record(epoch, "session", httperrorrate.Counters{})
				s := o.Snapshot(epoch)
				if s.Ratio != nil {
					t.Error("invented ratio")
				}
			}
		})
	}
	wg.Wait()
}
