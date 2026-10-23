package nugetdistribution

import (
	"context"
	"errors"
	"time"
)

// Transfer returns only after bytes are verified and the fenced publication
// transaction has committed. A lost reply is recovered by the item checkpoint.
type Transfer interface {
	ReplicateNuGetItem(context.Context, Job, Item) error
}
type Worker struct {
	Store         Store
	Transfer      Transfer
	Owner         string
	LeaseDuration time.Duration
}

func (w Worker) RunOnce(ctx context.Context) (bool, error) {
	if w.Store == nil || w.Transfer == nil || w.Owner == "" {
		return false, ErrInvalid
	}
	duration := w.LeaseDuration
	if duration == 0 {
		duration = 2 * time.Minute
	}
	if !ValidDuration(duration) {
		return false, ErrInvalid
	}
	job, err := w.Store.ClaimNuGetReplication(ctx, w.Owner, duration)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	for _, item := range job.Items {
		if item.State != "pending" && item.State != "running" {
			continue
		}
		// Bounded packages are retried in full. No lease heartbeat can outlive this
		// foreground call; expiry prevents the old owner from publishing.
		lease, e := w.Store.RenewNuGetReplication(ctx, job.Lease, duration)
		if e != nil {
			return true, e
		}
		job.Lease = lease
		if e = w.Transfer.ReplicateNuGetItem(ctx, job, item); e != nil {
			if errors.Is(e, ErrLeaseLost) || ctx.Err() != nil {
				return true, e
			}
			if failErr := w.Store.FailNuGetReplicationItem(ctx, job.Lease, item.Index, FailureCode(e)); failErr != nil {
				return true, failErr
			}
		}
	}
	_, err = w.Store.FinishNuGetReplication(ctx, job.Lease)
	return true, err
}
func FailureCode(err error) string {
	switch {
	case errors.Is(err, ErrAccessDenied):
		return "access_denied"
	case errors.Is(err, ErrSourceChanged):
		return "source_changed"
	case errors.Is(err, ErrTargetBlocked):
		return "target_conflict"
	case errors.Is(err, ErrCapacity):
		return "quota_exceeded"
	case errors.Is(err, ErrConflict):
		return "identity_conflict"
	default:
		return "transfer_failed"
	}
}
func (w Worker) Start(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 5 * time.Second
	}
	go func() {
		timer := time.NewTicker(interval)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
				_, _ = w.RunOnce(ctx)
			}
		}
	}()
}

func ValidFailureCode(code string) bool {
	switch code {
	case "access_denied", "source_changed", "target_conflict", "identity_conflict", "quota_exceeded", "transfer_failed":
		return true
	}
	return false
}
func FailureStatus(code string) int {
	switch code {
	case "access_denied":
		return 403
	case "transfer_failed":
		return 503
	default:
		return 409
	}
}
