package repository

import (
	"context"
	"sort"
	"time"
)

// EmailQueueDiagnosticsStore is a separate read port, not a worker capability.
type EmailQueueDiagnosticsStore interface {
	EmailQueueDiagnostics(context.Context) (EmailQueueDiagnostics, error)
}

// EmailQueueDiagnostics contains only aggregate, non-identifying evidence.
type EmailQueueDiagnostics struct {
	SampledAt                   time.Time                `json:"sampledAt"`
	Scope                       string                   `json:"scope"`
	Clock                       string                   `json:"clock"`
	Counts                      EmailQueueCounts         `json:"counts"`
	OldestExecutableWaitSeconds *float64                 `json:"oldestExecutableWaitSeconds"`
	LastSMTPAcceptedAt          *time.Time               `json:"lastSmtpAcceptedAt"`
	FailureClasses              []EmailQueueFailureClass `json:"failureClasses"`
	WorkerHealth                string                   `json:"workerHealth"`
	EvidenceLimitations         []string                 `json:"evidenceLimitations"`
}
type EmailQueueCounts struct {
	Total                int64 `json:"total"`
	Pending              int64 `json:"pending"`
	Retrying             int64 `json:"retrying"`
	Delivering           int64 `json:"delivering"`
	Accepted             int64 `json:"accepted"`
	Dead                 int64 `json:"dead"`
	ExecutableCandidates int64 `json:"executableCandidates"`
	FutureBackoff        int64 `json:"futureBackoff"`
	ValidLeases          int64 `json:"validLeases"`
	ExpiredLeases        int64 `json:"expiredLeases"`
	PredecessorBlocked   int64 `json:"predecessorBlocked"`
	AwaitingCancellation int64 `json:"awaitingCancellation"`
	AwaitingExhaustion   int64 `json:"awaitingExhaustion"`
}
type EmailQueueFailureClass struct {
	Category string `json:"category"`
	Count    int64  `json:"count"`
}

func newEmailQueueDiagnostics(clock string, now time.Time) EmailQueueDiagnostics {
	return EmailQueueDiagnostics{SampledAt: now, Scope: "whole_retained_queue", Clock: clock, WorkerHealth: "unknown", FailureClasses: []EmailQueueFailureClass{}, EvidenceLimitations: []string{
		"Retained rows only; no role heartbeat or expected worker configuration is persisted.",
		"Candidates project claim cleanup and ignore row locks; concurrent commits can change claim results.",
		"Wait age starts at the candidate's own due time or lease expiry, not predecessor clearance.",
		"SMTP accepted is a recorded positive final DATA reply, not inbox delivery or current worker activity; missing evidence is null.",
		"State totals partition the queue; diagnostic counters overlap and must not be summed. Future backoff is expected.",
	}}
}

// Unknown stored codes are never echoed or misrepresented as a known failure.
func emailFailureClass(code string) string {
	switch code {
	case "":
		return ""
	case "email_disabled", "invalid_message", "relay_resolution_failed", "relay_address_denied", "tls_configuration_failed", "tls_verification_failed", "tls_required", "authentication_configuration_failed", "authentication_failed", "encryption_key_unavailable":
		return "configuration"
	case "relay_connect_failed", "smtp_transport_failed":
		return "transport"
	case "smtp_temporary_rejection":
		return "temporary_rejection"
	case "smtp_permanent_rejection":
		return "permanent_rejection"
	case "outcome_unknown":
		return "outcome_unknown"
	case "target_changed", "target_disabled", "rule_changed", "rule_disabled", "rule_deleted":
		return "cancelled"
	case "attempts_exhausted":
		return "attempts_exhausted"
	default:
		return "unknown"
	}
}
func emailActive(v EmailDelivery) bool {
	return v.State == "pending" || v.State == "retrying" || v.State == "delivering"
}
func emailDue(v EmailDelivery, now time.Time) bool {
	if v.State == "delivering" {
		return !v.LeaseExpiresAt.IsZero() && !v.LeaseExpiresAt.After(now)
	}
	return (v.State == "pending" || v.State == "retrying") && !v.NextAttemptAt.After(now)
}
func emailAwaitingCancellation(v EmailDelivery, now time.Time) bool {
	return v.AutomaticCancellationCode != "" && (v.State == "pending" || v.State == "retrying" || (v.State == "delivering" && emailDue(v, now)))
}
func (s *MemoryStore) EmailQueueDiagnostics(ctx context.Context) (EmailQueueDiagnostics, error) {
	if err := ctx.Err(); err != nil {
		return EmailQueueDiagnostics{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return EmailQueueDiagnostics{}, err
	}
	now := time.Now().UTC()
	out := newEmailQueueDiagnostics("memory_store", now)
	failures := map[string]int64{}
	predecessors := map[string]int64{}
	for _, v := range s.emailDeliveries {
		if v.QuotaRuleID == "" || !emailActive(v) || emailAwaitingCancellation(v, now) || (v.Attempts >= EmailMaxAttempts && emailDue(v, now)) {
			continue
		}
		if seq, ok := predecessors[v.QuotaRuleID]; !ok || v.EventSequence < seq {
			predecessors[v.QuotaRuleID] = v.EventSequence
		}
	}
	for _, v := range s.emailDeliveries {
		c := &out.Counts
		c.Total++
		switch v.State {
		case "pending":
			c.Pending++
		case "retrying":
			c.Retrying++
		case "delivering":
			c.Delivering++
		case "accepted":
			c.Accepted++
		case "dead":
			c.Dead++
		}
		if (v.State == "pending" || v.State == "retrying") && v.NextAttemptAt.After(now) {
			c.FutureBackoff++
		}
		if v.State == "delivering" {
			if v.LeaseExpiresAt.After(now) {
				c.ValidLeases++
			} else if !v.LeaseExpiresAt.IsZero() {
				c.ExpiredLeases++
			}
		}
		if emailAwaitingCancellation(v, now) {
			c.AwaitingCancellation++
		}
		if v.Attempts >= EmailMaxAttempts && emailDue(v, now) {
			c.AwaitingExhaustion++
		}
		if cat := emailFailureClass(v.ErrorCode); cat != "" {
			failures[cat]++
		}
		if !v.AcceptedAt.IsZero() && (out.LastSMTPAcceptedAt == nil || v.AcceptedAt.After(*out.LastSMTPAcceptedAt)) {
			at := v.AcceptedAt
			out.LastSMTPAcceptedAt = &at
		}
		if v.Attempts >= EmailMaxAttempts || v.AutomaticCancellationCode != "" || !emailDue(v, now) {
			continue
		}
		seq, hasPrior := predecessors[v.QuotaRuleID]
		blocked := v.Kind != "test" && v.Kind != "" && hasPrior && seq < v.EventSequence
		if blocked {
			c.PredecessorBlocked++
			continue
		}
		c.ExecutableCandidates++
		due := v.NextAttemptAt
		if v.State == "delivering" {
			due = v.LeaseExpiresAt
		}
		age := now.Sub(due).Seconds()
		if age < 0 {
			age = 0
		}
		if out.OldestExecutableWaitSeconds == nil || age > *out.OldestExecutableWaitSeconds {
			out.OldestExecutableWaitSeconds = &age
		}
	}
	for cat, count := range failures {
		out.FailureClasses = append(out.FailureClasses, EmailQueueFailureClass{cat, count})
	}
	sort.Slice(out.FailureClasses, func(i, j int) bool { return out.FailureClasses[i].Category < out.FailureClasses[j].Category })
	return out, nil
}
