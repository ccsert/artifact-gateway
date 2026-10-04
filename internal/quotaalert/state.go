// Package quotaalert owns the finite repository logical quota lifecycle.
package quotaalert

import (
	"errors"
	"math/big"
	"time"
)

type Policy struct {
	WarningBasisPoints       int `json:"warningBasisPoints"`
	CriticalBasisPoints      int `json:"criticalBasisPoints"`
	RecoveryBelowBasisPoints int `json:"recoveryBelowBasisPoints"`
	WarningForSeconds        int `json:"warningForSeconds"`
	CriticalForSeconds       int `json:"criticalForSeconds"`
	RecoveryForSeconds       int `json:"recoveryForSeconds"`
	MaxSampleAgeSeconds      int `json:"maxSampleAgeSeconds"`
}

func (p Policy) Validate() error {
	if p.RecoveryBelowBasisPoints <= 0 || p.RecoveryBelowBasisPoints >= p.WarningBasisPoints || p.WarningBasisPoints >= p.CriticalBasisPoints || p.CriticalBasisPoints > 10000 {
		return errors.New("quota thresholds must be explicitly ordered")
	}
	for _, seconds := range []int{p.WarningForSeconds, p.CriticalForSeconds, p.RecoveryForSeconds} {
		if seconds < 1 || seconds > 86400 {
			return errors.New("quota duration must be explicit and bounded")
		}
	}
	if p.MaxSampleAgeSeconds < 30 || p.MaxSampleAgeSeconds > 3600 {
		return errors.New("quota freshness must be explicit and bounded")
	}
	return nil
}

type State struct {
	Severity      string    `json:"severity"`
	DataState     string    `json:"dataState"`
	UsedBytes     int64     `json:"usedBytes"`
	QuotaBytes    int64     `json:"quotaBytes"`
	LastSampleAt  time.Time `json:"lastSampleAt"`
	WarningSince  time.Time `json:"warningSince"`
	CriticalSince time.Time `json:"criticalSince"`
	RecoverySince time.Time `json:"recoverySince"`
}

type Observation struct {
	DataState             string
	UsedBytes, QuotaBytes int64
	SampleAt              time.Time
}

func Advance(p Policy, s State, o Observation, now time.Time) (State, string) {
	if s.Severity == "" {
		s.Severity = "normal"
	}
	if o.DataState != "available" {
		return Reset(s, o.DataState), ""
	}
	if o.QuotaBytes <= 0 {
		return Reset(s, "not_configured"), ""
	}
	if o.UsedBytes < 0 || o.SampleAt.IsZero() || o.SampleAt.After(now) {
		return Reset(s, "unknown"), ""
	}
	if now.Sub(o.SampleAt) > time.Duration(p.MaxSampleAgeSeconds)*time.Second {
		return Reset(s, "stale"), ""
	}
	if !s.LastSampleAt.IsZero() {
		if o.SampleAt.Before(s.LastSampleAt) {
			return Reset(s, "unknown"), ""
		}
		if o.SampleAt.Equal(s.LastSampleAt) {
			return s, ""
		}
		// Scheduler samples every 15 seconds. More than two periods is a gap,
		// even if a looser UI freshness limit still considers the last value fresh.
		if o.SampleAt.Sub(s.LastSampleAt) > 30*time.Second {
			s = Reset(s, "available")
		}
	}
	s.DataState = o.DataState
	if s.QuotaBytes != 0 && s.QuotaBytes != o.QuotaBytes {
		s = Reset(s, "available")
	}
	s.UsedBytes, s.QuotaBytes, s.LastSampleAt = o.UsedBytes, o.QuotaBytes, o.SampleAt
	s.WarningSince = continuousSince(s.WarningSince, ratioCompare(o.UsedBytes, o.QuotaBytes, p.WarningBasisPoints) >= 0, o.SampleAt)
	s.CriticalSince = continuousSince(s.CriticalSince, ratioCompare(o.UsedBytes, o.QuotaBytes, p.CriticalBasisPoints) >= 0, o.SampleAt)
	s.RecoverySince = continuousSince(s.RecoverySince, s.Severity != "normal" && ratioCompare(o.UsedBytes, o.QuotaBytes, p.RecoveryBelowBasisPoints) < 0, o.SampleAt)
	if s.Severity != "critical" && matured(s.CriticalSince, o.SampleAt, p.CriticalForSeconds) {
		s.Severity = "critical"
		return s, "critical"
	}
	if s.Severity == "normal" && matured(s.WarningSince, o.SampleAt, p.WarningForSeconds) {
		s.Severity = "warning"
		return s, "warning"
	}
	if s.Severity != "normal" && matured(s.RecoverySince, o.SampleAt, p.RecoveryForSeconds) {
		s.Severity = "normal"
		return Reset(s, "available"), "resolved"
	}
	return s, ""
}

func continuousSince(since time.Time, condition bool, at time.Time) time.Time {
	if !condition {
		return time.Time{}
	}
	if since.IsZero() {
		return at
	}
	return since
}
func matured(since, at time.Time, seconds int) bool {
	return !since.IsZero() && at.Sub(since) >= time.Duration(seconds)*time.Second
}
func ratioCompare(used, quota int64, basisPoints int) int {
	var left, right big.Int
	left.Mul(big.NewInt(used), big.NewInt(10000))
	right.Mul(big.NewInt(quota), big.NewInt(int64(basisPoints)))
	return left.Cmp(&right)
}

// Reset discards continuous evidence without declaring recovery or losing the
// last valid observation. Configuration changes use the same boundary.
func Reset(s State, dataState string) State {
	if s.Severity == "" {
		s.Severity = "normal"
	}
	s.DataState = dataState
	s.WarningSince, s.CriticalSince, s.RecoverySince = time.Time{}, time.Time{}, time.Time{}
	return s
}
