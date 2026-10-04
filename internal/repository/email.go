package repository

import (
	"context"
	"errors"
	"time"
)

var ErrEmailTargetDisabled = errors.New("email target is disabled")
var ErrEmailRateLimited = errors.New("email test rate limit exceeded")
var ErrEmailQueueFull = errors.New("email queue is full")
var ErrEmailIdempotencyConflict = errors.New("email idempotency conflict")
var ErrEmailInvalidState = errors.New("email delivery cannot be replayed")

const EmailMaxAttempts = 8
const EmailLease = 30 * time.Second

type EmailTarget struct {
	ID, Name, Locale, RecipientCiphertext, Version string
	Enabled                                        bool
	CreatedAt, UpdatedAt                           time.Time
}
type EmailDelivery struct {
	ID, EventID, TargetID, TargetVersion, RequestKey, Scenario, Locale, TemplateVersion, RecipientCiphertext, From, ConsoleOrigin, State, Version, ErrorCode, LeaseOwner, LeaseToken string
	Attempts                                                                                                                                                                         int
	PossibleDuplicate                                                                                                                                                                bool
	CreatedAt, UpdatedAt, NextAttemptAt, AcceptedAt, LeaseExpiresAt                                                                                                                  time.Time
}
type EmailTestRequest struct{ ID, EventID, RequestKey, TargetID, TargetVersion, Scenario, TemplateVersion, From, ConsoleOrigin string }
type EmailAttemptResult struct {
	Code                      string
	Permanent, OutcomeUnknown bool
}
type EmailTargetStore interface {
	CreateEmailTarget(context.Context, EmailTarget) (EmailTarget, error)
	ListEmailTargets(context.Context) ([]EmailTarget, error)
	GetEmailTarget(context.Context, string) (EmailTarget, error)
	UpdateEmailTarget(context.Context, EmailTarget, string) (EmailTarget, error)
}
type EmailStore interface {
	EmailTargetStore
	EnqueueEmailTest(context.Context, EmailTestRequest) (EmailDelivery, error)
	ListEmailDeliveries(context.Context, int) ([]EmailDelivery, error)
	GetEmailDelivery(context.Context, string) (EmailDelivery, error)
	ClaimEmailDelivery(context.Context, string) (EmailDelivery, error)
	FinishEmailDelivery(context.Context, string, string, EmailAttemptResult) error
	ReplayEmailDelivery(context.Context, string, string) (EmailDelivery, error)
}

func emailSameRequest(v EmailDelivery, r EmailTestRequest) bool {
	return v.TargetID == r.TargetID && v.TargetVersion == r.TargetVersion && v.Scenario == r.Scenario && v.TemplateVersion == r.TemplateVersion
}
func emailRetryDelay(attempts int) time.Duration {
	if attempts < 1 {
		attempts = 1
	}
	if attempts > 10 {
		return time.Hour
	}
	delay := time.Duration(1<<uint(attempts-1)) * 5 * time.Second
	if delay > time.Hour {
		return time.Hour
	}
	return delay
}
func emailSafeErrorCode(code string) string {
	switch code {
	case "", "email_disabled", "invalid_message", "relay_resolution_failed", "relay_address_denied", "relay_connect_failed", "tls_configuration_failed", "tls_verification_failed", "tls_required", "authentication_configuration_failed", "authentication_failed", "smtp_permanent_rejection", "smtp_temporary_rejection", "outcome_unknown", "smtp_transport_failed", "target_changed", "target_disabled", "encryption_key_unavailable", "attempts_exhausted":
		return code
	default:
		return "invalid_message"
	}
}
