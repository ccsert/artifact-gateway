package app

import (
	"context"
	"errors"
	"github.com/artifact-gateway/artifact-gateway/internal/emailnotification"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/artifact-gateway/artifact-gateway/internal/secrets"
	"time"
)

// EmailNotificationWorker processes one delivery at a time. PostgreSQL fences
// claims across worker sessions; SMTP acceptance itself cannot be exactly-once.
type EmailNotificationWorker struct {
	Store      repository.EmailStore
	Sender     emailnotification.Sender
	InstanceID string
}

func (w EmailNotificationWorker) Run(ctx context.Context) (bool, error) {
	if !w.Sender.Config.Enabled || w.Store == nil {
		return false, nil
	}
	claimStarted := time.Now()
	v, err := w.Store.ClaimEmailDelivery(ctx, w.InstanceID)
	if errors.Is(err, repository.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	attemptCtx, cancel := context.WithDeadline(ctx, claimStarted.Add(repository.EmailLease-5*time.Second))
	defer cancel()
	target, err := w.Store.GetEmailTarget(attemptCtx, v.TargetID)
	if err != nil {
		return true, err
	}
	result := repository.EmailAttemptResult{}
	switch {
	case !target.Enabled:
		result = repository.EmailAttemptResult{Code: "target_disabled", Permanent: true}
	case target.Version != v.TargetVersion:
		result = repository.EmailAttemptResult{Code: "target_changed", Permanent: true}
	default:
		recipient, e := secrets.Open("email-target:"+v.TargetID, v.RecipientCiphertext)
		if e != nil {
			result.Code = "encryption_key_unavailable"
		} else {
			sender := w.Sender
			sender.Config.From = v.From
			sender.Config.ConsoleOrigin = v.ConsoleOrigin
			message, e := emailnotification.MIME(sender.Config, recipient, v.EventID, v.Scenario, v.Locale, v.TemplateVersion, v.CreatedAt)
			if v.Kind == "repository_quota" {
				if v.EventID != v.QuotaEvent.ID || v.QuotaRuleID != v.QuotaEvent.RuleID || v.EventSequence != v.QuotaEvent.Sequence || v.EpisodeID != v.QuotaEvent.EpisodeID || v.Scenario != v.QuotaEvent.Scenario {
					e = emailnotification.ErrInvalidMessage
				} else {
					message, e = emailnotification.MIMEQuota(sender.Config, recipient, v.QuotaEvent, v.Locale, v.TemplateVersion)
				}
			} else if v.Kind != "" && v.Kind != "test" {
				e = emailnotification.ErrInvalidMessage
			}
			if e != nil {
				result = repository.EmailAttemptResult{Code: "invalid_message", Permanent: true}
			} else {
				sent := sender.Send(attemptCtx, recipient, v.EventID, message)
				result = repository.EmailAttemptResult{Code: sent.Code, Permanent: sent.Permanent, OutcomeUnknown: sent.OutcomeUnknown}
			}
		}
	}
	return true, w.Store.FinishEmailDelivery(ctx, v.ID, v.LeaseToken, result)
}
func (w EmailNotificationWorker) Start(ctx context.Context, interval time.Duration) {
	if !w.Sender.Config.Enabled {
		return
	}
	if interval <= 0 {
		interval = 5 * time.Second
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			if ctx.Err() != nil {
				return
			}
			_, _ = w.Run(ctx)
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}
