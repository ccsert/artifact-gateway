package repository

import (
	"context"
	"errors"
	"github.com/artifact-gateway/artifact-gateway/internal/quotaalert"
	"time"
)

type RepositoryQuotaAlertRule struct {
	ID, RepositoryID, TargetID, TargetVersion, Version, StateVersion, ActiveEpisodeID, LastEventID string
	Enabled, Deleted                                                                               bool
	Policy                                                                                         quotaalert.Policy
	State                                                                                          quotaalert.State
	Sequence                                                                                       int64
	CreatedAt, UpdatedAt, EvaluatedAt                                                              time.Time
}
type RepositoryQuotaAlertStore interface {
	EvaluateNextRepositoryQuotaAlert(context.Context, QuotaAlertMailConfig, time.Duration) (bool, error)
	ListRepositoryQuotaAlertEvents(context.Context, string) ([]RepositoryQuotaAlertEvent, error)
	ListRepositoryQuotaAlertRules(context.Context) ([]RepositoryQuotaAlertRule, error)
	CreateRepositoryQuotaAlertRule(context.Context, RepositoryQuotaAlertRule) (RepositoryQuotaAlertRule, error)
	GetRepositoryQuotaAlertRule(context.Context, string) (RepositoryQuotaAlertRule, error)
	UpdateRepositoryQuotaAlertRule(context.Context, RepositoryQuotaAlertRule, string) (RepositoryQuotaAlertRule, error)
	DeleteRepositoryQuotaAlertRule(context.Context, string, string) (RepositoryQuotaAlertRule, error)
}

type QuotaAlertMailConfig struct {
	Enabled             bool
	From, ConsoleOrigin string
	UnavailableCode     string
}

type RepositoryQuotaAlertEvent struct {
	Snapshot                                                                                quotaalert.Event
	TargetID, TargetVersion, DeliveryID, DeliveryState, DeliveryErrorCode, NotificationCode string
}

var ErrQuotaAlertConflict = errors.New("repository already has a quota alert rule")
var ErrQuotaAlertLimit = errors.New("quota alert rule limit reached")
var ErrQuotaAlertDeleted = errors.New("quota alert rule is deleted")
var ErrQuotaAlertScopeImmutable = errors.New("quota alert repository scope is immutable")

func quotaSameConfig(a, b RepositoryQuotaAlertRule) bool {
	return a.RepositoryID == b.RepositoryID && a.TargetID == b.TargetID && a.TargetVersion == b.TargetVersion && a.Enabled == b.Enabled && a.Policy == b.Policy
}

func quotaRuleAt(v RepositoryQuotaAlertRule, now time.Time) RepositoryQuotaAlertRule {
	if v.State.DataState == "available" && !v.State.LastSampleAt.IsZero() && now.Sub(v.State.LastSampleAt) > time.Duration(v.Policy.MaxSampleAgeSeconds)*time.Second {
		v.State = quotaalert.Reset(v.State, "stale")
	}
	return v
}
