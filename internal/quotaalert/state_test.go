package quotaalert

import (
	"reflect"
	"testing"
	"time"
)

func testPolicy() Policy {
	return Policy{WarningBasisPoints: 8500, CriticalBasisPoints: 9500, RecoveryBelowBasisPoints: 8000, WarningForSeconds: 300, CriticalForSeconds: 120, RecoveryForSeconds: 180, MaxSampleAgeSeconds: 60}
}

func TestWarningEscalationAndHysteresisRecovery(t *testing.T) {
	p := testPolicy()
	p.WarningForSeconds = 30
	p.CriticalForSeconds = 30
	p.RecoveryForSeconds = 30
	start := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	var state State
	var events []string
	samples := []struct {
		seconds  int
		used     int64
		severity string
	}{
		{0, 880, "normal"}, {15, 880, "normal"}, {30, 880, "warning"}, {45, 880, "warning"},
		{60, 980, "warning"}, {75, 980, "warning"}, {90, 980, "critical"}, {105, 880, "critical"},
		{120, 760, "critical"}, {135, 790, "critical"}, {150, 810, "critical"},
		{165, 790, "critical"}, {180, 790, "critical"}, {195, 790, "normal"}, {210, 790, "normal"},
	}
	for _, sample := range samples {
		at := start.Add(time.Duration(sample.seconds) * time.Second)
		var event string
		state, event = Advance(p, state, Observation{DataState: "available", UsedBytes: sample.used, QuotaBytes: 1000, SampleAt: at}, at)
		if state.Severity != sample.severity {
			t.Fatalf("%ds severity=%q wanted=%q", sample.seconds, state.Severity, sample.severity)
		}
		if event != "" {
			events = append(events, event)
		}
	}
	if !reflect.DeepEqual(events, []string{"warning", "critical", "resolved"}) {
		t.Fatalf("events=%v", events)
	}
}

func TestDirectCriticalCanFireBeforeWarningDuration(t *testing.T) {
	start := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	var state State
	var events []string
	for seconds := 0; seconds <= 135; seconds += 15 {
		at := start.Add(time.Duration(seconds) * time.Second)
		var event string
		state, event = Advance(testPolicy(), state, Observation{DataState: "available", UsedBytes: 980, QuotaBytes: 1000, SampleAt: at}, at)
		if event != "" {
			events = append(events, event)
		}
	}
	if state.Severity != "critical" || !reflect.DeepEqual(events, []string{"critical"}) {
		t.Fatalf("state=%#v events=%v", state, events)
	}
}

func TestLargeQuotaComparesExactBytesWithoutRoundingOrOverflow(t *testing.T) {
	p := testPolicy()
	p.WarningForSeconds = 1
	at := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	o := Observation{DataState: "available", UsedBytes: 7_649_999_999_999_999_999, QuotaBytes: 9_000_000_000_000_000_000, SampleAt: at}
	s, event := Advance(p, State{}, o, at)
	if event != "" || s.Severity != "normal" {
		t.Fatalf("below threshold initial sample: %#v %q", s, event)
	}
	o.SampleAt = at.Add(15 * time.Second)
	s, event = Advance(p, s, o, o.SampleAt)
	if event != "" || s.Severity != "normal" {
		t.Fatalf("rounded one byte below boundary into warning: %#v %q", s, event)
	}
}

func TestPolicyRequiresExplicitOrderedThresholdsAndDurations(t *testing.T) {
	if err := testPolicy().Validate(); err != nil {
		t.Fatal(err)
	}
	for _, edit := range []func(*Policy){
		func(p *Policy) { p.WarningBasisPoints = 0 }, func(p *Policy) { p.CriticalBasisPoints = 10001 },
		func(p *Policy) { p.RecoveryBelowBasisPoints = p.WarningBasisPoints }, func(p *Policy) { p.CriticalBasisPoints = p.WarningBasisPoints },
		func(p *Policy) { p.WarningForSeconds = 0 }, func(p *Policy) { p.CriticalForSeconds = -1 }, func(p *Policy) { p.RecoveryForSeconds = 86401 },
		func(p *Policy) { p.MaxSampleAgeSeconds = 29 }, func(p *Policy) { p.MaxSampleAgeSeconds = 3601 },
	} {
		p := testPolicy()
		edit(&p)
		if p.Validate() == nil {
			t.Fatalf("invalid policy accepted: %#v", p)
		}
	}
}

func TestWarningRequiresContinuousQuotaEvidenceAndEmitsOnce(t *testing.T) {
	start := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	var state State
	var events []string
	for seconds := 0; seconds <= 360; seconds += 15 {
		now := start.Add(time.Duration(seconds) * time.Second)
		var transition string
		state, transition = Advance(testPolicy(), state, Observation{DataState: "available", UsedBytes: 880, QuotaBytes: 1000, SampleAt: now}, now)
		if transition != "" {
			events = append(events, transition)
		}
		if seconds < 300 && (state.Severity != "normal" || transition != "") {
			t.Fatalf("early alert at %d: %#v %q", seconds, state, transition)
		}
	}
	if state.Severity != "warning" || len(events) != 1 || events[0] != "warning" {
		t.Fatalf("continuous warning: state=%#v events=%v", state, events)
	}
}

func TestInvalidEvidencePreservesActiveWarningWithoutRecovery(t *testing.T) {
	start := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	now := start.Add(15 * time.Second)
	cases := []struct {
		name, quality string
		o             Observation
	}{
		{"read failure", "unknown", Observation{DataState: "unknown", SampleAt: now}},
		{"unlimited", "not_configured", Observation{DataState: "available", QuotaBytes: 0, SampleAt: now}},
		{"stale", "stale", Observation{DataState: "available", UsedBytes: 1, QuotaBytes: 1000, SampleAt: now.Add(-61 * time.Second)}},
		{"future", "unknown", Observation{DataState: "available", UsedBytes: 1, QuotaBytes: 1000, SampleAt: now.Add(time.Second)}},
		{"invalid bytes", "unknown", Observation{DataState: "available", UsedBytes: -1, QuotaBytes: 1000, SampleAt: now}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			before := State{Severity: "warning", DataState: "available", UsedBytes: 880, QuotaBytes: 1000, LastSampleAt: start, WarningSince: start, CriticalSince: start, RecoverySince: start.Add(-time.Hour)}
			after, event := Advance(testPolicy(), before, c.o, now)
			if event != "" || after.Severity != "warning" || after.DataState != c.quality || after.UsedBytes != 880 || after.QuotaBytes != 1000 || !after.LastSampleAt.Equal(start) || !after.WarningSince.IsZero() || !after.CriticalSince.IsZero() || !after.RecoverySince.IsZero() {
				t.Fatalf("invalid evidence changed alert/retained candidates: %#v %q", after, event)
			}
		})
	}
}

func TestRestartGapCannotCompleteOldPendingDuration(t *testing.T) {
	start := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	state, _ := Advance(testPolicy(), State{}, Observation{DataState: "available", UsedBytes: 880, QuotaBytes: 1000, SampleAt: start}, start)
	now := start.Add(301 * time.Second)
	state, event := Advance(testPolicy(), state, Observation{DataState: "available", UsedBytes: 880, QuotaBytes: 1000, SampleAt: now}, now)
	if event != "" || state.Severity != "normal" || !state.WarningSince.Equal(now) {
		t.Fatalf("restart fabricated uninterrupted evidence: %#v %q", state, event)
	}
}

func TestQuotaChangeRestartsRecoveryEvidenceWithoutResolving(t *testing.T) {
	p := testPolicy()
	p.RecoveryForSeconds = 30
	start := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	s := State{Severity: "critical", DataState: "available", UsedBytes: 790, QuotaBytes: 1000, LastSampleAt: start.Add(15 * time.Second), RecoverySince: start}
	at := start.Add(30 * time.Second)
	s, event := Advance(p, s, Observation{DataState: "available", UsedBytes: 790, QuotaBytes: 2000, SampleAt: at}, at)
	if event != "" || s.Severity != "critical" || !s.RecoverySince.Equal(at) {
		t.Fatalf("quota edit fabricated prior recovery evidence: %#v %q", s, event)
	}
}
