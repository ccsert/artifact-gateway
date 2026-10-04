package app

import (
	"context"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"testing"
	"time"
)

type quotaRoutingCapture struct {
	repository.RepositoryQuotaAlertStore
	mail  repository.QuotaAlertMailConfig
	calls int
}

func (s *quotaRoutingCapture) EvaluateNextRepositoryQuotaAlert(_ context.Context, cfg repository.QuotaAlertMailConfig, interval time.Duration) (bool, error) {
	s.mail = cfg
	s.calls++
	return false, nil
}
func TestQuotaEvaluatorMissingEncryptionMarksRoutingUnavailable(t *testing.T) {
	t.Setenv("GATEWAY_SETTINGS_ENCRYPTION_KEY", "")
	capture := &quotaRoutingCapture{}
	_, err := (QuotaAlertEvaluator{Store: capture, Mail: repository.QuotaAlertMailConfig{Enabled: true, From: "synthetic@example.test"}}).Run(context.Background())
	if err != nil || capture.calls != 1 || capture.mail.Enabled || capture.mail.UnavailableCode != "encryption_key_unavailable" {
		t.Fatalf("missing encryption permitted new queued mail: %#v %v", capture, err)
	}
}

type quotaPollTimingStore struct {
	repository.RepositoryQuotaAlertStore
	started  chan struct{}
	release  chan struct{}
	finished chan time.Time
	next     chan time.Time
	calls    int
}

func (s *quotaPollTimingStore) EvaluateNextRepositoryQuotaAlert(ctx context.Context, _ repository.QuotaAlertMailConfig, _ time.Duration) (bool, error) {
	s.calls++
	if s.calls == 1 {
		close(s.started)
		select {
		case <-s.release:
		case <-ctx.Done():
			return false, ctx.Err()
		}
		s.finished <- time.Now()
	} else {
		s.next <- time.Now()
	}
	return false, nil
}
func TestQuotaPollingWaitsAfterCompletedEvaluation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := &quotaPollTimingStore{started: make(chan struct{}), release: make(chan struct{}), finished: make(chan time.Time, 1), next: make(chan time.Time, 10)}
	const interval = 100 * time.Millisecond
	(QuotaAlertEvaluator{Store: s}).Start(ctx, interval)
	select {
	case <-s.started:
	case <-time.After(time.Second):
		t.Fatal("scheduler did not start")
	}
	// A delayed evaluation must not consume a buffered tick and immediately repoll.
	time.Sleep(150 * time.Millisecond)
	close(s.release)
	finished := <-s.finished
	select {
	case next := <-s.next:
		if next.Sub(finished) < interval {
			t.Fatalf("poll interval measured before evaluation finished: %v", next.Sub(finished))
		}
	case <-time.After(time.Second):
		t.Fatal("scheduler did not continue")
	}
}
