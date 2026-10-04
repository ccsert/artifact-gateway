package repository

import (
	"github.com/artifact-gateway/artifact-gateway/internal/quotaalert"
	"github.com/google/uuid"
	"time"
)

func quotaTransition(rule *RepositoryQuotaAlertRule, o quotaalert.Observation, name string, now time.Time) *quotaalert.Event {
	before := rule.State
	state, scenario := quotaalert.Advance(rule.Policy, before, o, now)
	rule.State = state
	rule.EvaluatedAt = now
	rule.StateVersion = nextHostedGroupVersion(rule.StateVersion)
	if scenario == "" {
		return nil
	}
	if rule.ActiveEpisodeID == "" {
		rule.ActiveEpisodeID = uuid.NewString()
	}
	since := state.WarningSince
	if scenario == "critical" {
		since = state.CriticalSince
	}
	if scenario == "resolved" {
		since = before.RecoverySince
	}
	rule.Sequence++
	event := quotaalert.Event{ID: uuid.NewString(), RuleID: rule.ID, RuleVersion: rule.Version, RepositoryID: rule.RepositoryID, RepositoryName: name, EpisodeID: rule.ActiveEpisodeID, PreviousEventID: rule.LastEventID, Sequence: rule.Sequence, Scenario: scenario, UsedBytes: state.UsedBytes, QuotaBytes: state.QuotaBytes, Policy: rule.Policy, OccurredAt: now, SampleAt: state.LastSampleAt, EvidenceSince: since}
	rule.LastEventID = event.ID
	if scenario == "resolved" {
		rule.ActiveEpisodeID = ""
	}
	return &event
}

func quotaRouteCode(cfg QuotaAlertMailConfig, target EmailTarget, rule RepositoryQuotaAlertRule) string {
	switch {
	case cfg.UnavailableCode == "encryption_key_unavailable":
		return cfg.UnavailableCode
	case !cfg.Enabled:
		return "email_disabled"
	case target.ID == "":
		return "target_unavailable"
	case !target.Enabled:
		return "target_disabled"
	case target.Version != rule.TargetVersion:
		return "target_changed"
	default:
		return "queued"
	}
}

func quotaDelivery(e quotaalert.Event, target EmailTarget, cfg QuotaAlertMailConfig, full bool) EmailDelivery {
	d := EmailDelivery{ID: uuid.NewString(), EventID: e.ID, RequestKey: e.ID, Kind: "repository_quota", QuotaRuleID: e.RuleID, EpisodeID: e.EpisodeID, EventSequence: e.Sequence, QuotaEvent: e, TargetID: target.ID, TargetVersion: target.Version, Scenario: e.Scenario, Locale: target.Locale, TemplateVersion: quotaalert.TemplateVersion, RecipientCiphertext: target.RecipientCiphertext, From: cfg.From, ConsoleOrigin: cfg.ConsoleOrigin, State: "pending", Version: "1", CreatedAt: e.OccurredAt, UpdatedAt: e.OccurredAt, NextAttemptAt: e.OccurredAt}
	if full {
		d.State = "dead"
		d.ErrorCode = "queue_full"
	}
	return d
}
