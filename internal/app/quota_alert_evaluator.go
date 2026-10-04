package app

import (
	"context"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/artifact-gateway/artifact-gateway/internal/secrets"
	"time"
)

// QuotaAlertEvaluator bounds database work; SMTP runs separately in the existing worker.
type QuotaAlertEvaluator struct {
	Store repository.RepositoryQuotaAlertStore
	Mail  repository.QuotaAlertMailConfig
}

func (e QuotaAlertEvaluator) Run(ctx context.Context) (int, error) {
	if e.Store == nil {
		return 0, nil
	}
	budget, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	mail := e.Mail
	if mail.Enabled {
		if _, err := secrets.Seal("email-readiness", "synthetic-probe"); err != nil {
			mail.Enabled = false
			mail.UnavailableCode = "encryption_key_unavailable"
		}
	}
	for n := 0; n < 100; n++ {
		worked, err := e.Store.EvaluateNextRepositoryQuotaAlert(budget, mail, 15*time.Second)
		if err != nil {
			return n, err
		}
		if !worked {
			return n, nil
		}
	}
	return 100, nil
}
func (e QuotaAlertEvaluator) Start(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 15 * time.Second
	}
	go func() {
		timer := time.NewTimer(0)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
				if ctx.Err() != nil {
					return
				}
				_, _ = e.Run(ctx)
				// Due time is based on the completed database evaluation. Starting
				// the next wait here avoids buffered/early ticks skipping a sample.
				timer.Reset(interval)
			}
		}
	}()
}
