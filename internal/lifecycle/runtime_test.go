package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

type runtimeTestStore struct {
	claim    func(context.Context, repository.LifecycleJobKind, repository.Format, int) ([]repository.LifecycleJob, error)
	update   func(context.Context, string, string, int, int, string) error
	renew    func(context.Context, string, string) error
	complete func(context.Context, string, string) error
	fail     func(context.Context, string, string, string) error
}

func (s *runtimeTestStore) ClaimLifecycleJobsByKindAndFormat(ctx context.Context, kind repository.LifecycleJobKind, format repository.Format, limit int) ([]repository.LifecycleJob, error) {
	if s.claim != nil {
		return s.claim(ctx, kind, format, limit)
	}
	return nil, nil
}

func (s *runtimeTestStore) UpdateLifecycleJobProgress(ctx context.Context, id, leaseToken string, current, total int, message string) error {
	if s.update != nil {
		return s.update(ctx, id, leaseToken, current, total, message)
	}
	return nil
}

func (s *runtimeTestStore) RenewLifecycleJobLease(ctx context.Context, id, leaseToken string) error {
	if s.renew != nil {
		return s.renew(ctx, id, leaseToken)
	}
	return nil
}

func (s *runtimeTestStore) CompleteLifecycleJob(ctx context.Context, id, leaseToken string) error {
	if s.complete != nil {
		return s.complete(ctx, id, leaseToken)
	}
	return nil
}

func (s *runtimeTestStore) FailLifecycleJob(ctx context.Context, id, leaseToken, message string) error {
	if s.fail != nil {
		return s.fail(ctx, id, leaseToken, message)
	}
	return nil
}

type runtimeTestMetrics struct {
	mu       sync.Mutex
	events   []string
	inFlight int64
}

func (m *runtimeTestMetrics) RecordBackgroundOperation(operation string, format repository.Format, outcome string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append(m.events, fmt.Sprintf("%s/%s/%s", operation, format, outcome))
}

func (m *runtimeTestMetrics) AddBackgroundOperationInFlight(_ string, _ repository.Format, delta int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.inFlight += delta
}

func runtimeTestJob(id string) repository.LifecycleJob {
	return repository.LifecycleJob{ID: id, RepositoryID: "repo", LeaseToken: "lease-" + id, ProgressTotal: 1}
}

func TestRuntimeRunJobsRequiresConfigurationAndDefaultsLimit(t *testing.T) {
	ctx := context.Background()
	if err := (Runtime{}).RunJobs(ctx, 1, func(context.Context, repository.LifecycleJob) error { return nil }); err == nil {
		t.Fatal("RunJobs() error = nil without store")
	}
	store := &runtimeTestStore{}
	if err := (Runtime{Store: store}).RunJobs(ctx, 1, nil); err == nil {
		t.Fatal("RunJobs() error = nil without handler")
	}
	store.claim = func(_ context.Context, _ repository.LifecycleJobKind, _ repository.Format, limit int) ([]repository.LifecycleJob, error) {
		if limit != 100 {
			t.Fatalf("claim limit=%d want=100", limit)
		}
		return nil, nil
	}
	if err := (Runtime{Store: store, Kind: repository.LifecycleJobScan, Formats: []repository.Format{repository.FormatRaw}}).RunJobs(ctx, 0, func(context.Context, repository.LifecycleJob) error { return nil }); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeRunJobsCompletesClaimedJobWithProgressAndMetrics(t *testing.T) {
	ctx := context.Background()
	job := runtimeTestJob("completed")
	metrics := &runtimeTestMetrics{}
	var completed bool
	var claims []repository.Format
	store := &runtimeTestStore{
		claim: func(_ context.Context, kind repository.LifecycleJobKind, format repository.Format, limit int) ([]repository.LifecycleJob, error) {
			claims = append(claims, format)
			if kind != repository.LifecycleJobScan || limit != 1 {
				t.Fatalf("claim kind=%q limit=%d", kind, limit)
			}
			return []repository.LifecycleJob{job}, nil
		},
		update: func(_ context.Context, id, token string, current, total int, message string) error {
			if id != job.ID || token != job.LeaseToken || current != 0 || total != 1 || message != "scanning artifact" {
				t.Fatalf("progress id=%q token=%q current=%d total=%d message=%q", id, token, current, total, message)
			}
			return nil
		},
		complete: func(_ context.Context, id, token string) error {
			completed = id == job.ID && token == job.LeaseToken
			return nil
		},
	}
	runtime := Runtime{Store: store, Kind: repository.LifecycleJobScan, Formats: []repository.Format{repository.FormatRaw, repository.FormatOCI}, Name: "artifact scan", Operation: "scan", Metrics: metrics, LeaseProgressMessage: "scanning artifact"}
	if err := runtime.RunJobs(ctx, 1, func(_ context.Context, received repository.LifecycleJob) error {
		if received.ID != job.ID {
			t.Fatalf("handler job=%#v", received)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !completed || len(claims) != 1 || claims[0] != repository.FormatRaw {
		t.Fatalf("completed=%t claims=%v", completed, claims)
	}
	if fmt.Sprint(metrics.events) != "[scan/raw/started scan/raw/completed]" || metrics.inFlight != 0 {
		t.Fatalf("metrics events=%v inFlight=%d", metrics.events, metrics.inFlight)
	}
}

func TestRuntimeRunJobsContinuesAfterClaimAndHandlerFailures(t *testing.T) {
	claimFailure := errors.New("claim unavailable")
	handlerFailure := errors.New("task unavailable")
	job := runtimeTestJob("failed")
	metrics := &runtimeTestMetrics{}
	var failedMessage string
	store := &runtimeTestStore{
		claim: func(_ context.Context, _ repository.LifecycleJobKind, format repository.Format, _ int) ([]repository.LifecycleJob, error) {
			switch format {
			case repository.FormatRaw, repository.FormatMaven:
				return nil, claimFailure
			case repository.FormatOCI:
				return []repository.LifecycleJob{job}, nil
			default:
				return nil, nil
			}
		},
		fail: func(_ context.Context, _, _, message string) error {
			failedMessage = message
			return nil
		},
	}
	runtime := Runtime{Store: store, Kind: repository.LifecycleJobScan, Formats: []repository.Format{repository.FormatRaw, repository.FormatOCI, repository.FormatMaven}, Name: "artifact scan", Operation: "scan", Metrics: metrics}
	err := runtime.RunJobs(context.Background(), 3, func(context.Context, repository.LifecycleJob) error { return handlerFailure })
	if !errors.Is(err, claimFailure) || failedMessage != handlerFailure.Error() || fmt.Sprint(metrics.events) != "[scan/oci/started scan/oci/failed]" || metrics.inFlight != 0 {
		t.Fatalf("RunJobs() error=%v failedMessage=%q metrics=%v inFlight=%d", err, failedMessage, metrics.events, metrics.inFlight)
	}
}

func TestRuntimeRunJobsFailsWhenInitialLeaseProgressCannotBeRecorded(t *testing.T) {
	progressFailure := errors.New("lease lost")
	job := runtimeTestJob("progress")
	var failedMessage string
	store := &runtimeTestStore{
		claim: func(context.Context, repository.LifecycleJobKind, repository.Format, int) ([]repository.LifecycleJob, error) {
			return []repository.LifecycleJob{job}, nil
		},
		update: func(context.Context, string, string, int, int, string) error { return progressFailure },
		fail: func(_ context.Context, _, _, message string) error {
			failedMessage = message
			return nil
		},
	}
	runtime := Runtime{Store: store, Kind: repository.LifecycleJobScan, Formats: []repository.Format{repository.FormatRaw}, Name: "artifact scan", LeaseProgressMessage: "scanning artifact"}
	err := runtime.RunJobs(context.Background(), 1, func(context.Context, repository.LifecycleJob) error {
		t.Fatal("handler ran after initial lease progress failed")
		return nil
	})
	if !errors.Is(err, progressFailure) || failedMessage != "artifact scan lease renewal failed: lease lost" {
		t.Fatalf("RunJobs() error=%v failedMessage=%q", err, failedMessage)
	}
}

func TestRuntimeRunJobsReturnsCompletionFailure(t *testing.T) {
	completionFailure := errors.New("complete unavailable")
	store := &runtimeTestStore{
		claim: func(context.Context, repository.LifecycleJobKind, repository.Format, int) ([]repository.LifecycleJob, error) {
			return []repository.LifecycleJob{runtimeTestJob("complete-failure")}, nil
		},
		complete: func(context.Context, string, string) error { return completionFailure },
	}
	runtime := Runtime{Store: store, Kind: repository.LifecycleJobScan, Formats: []repository.Format{repository.FormatRaw}, Name: "artifact scan"}
	if err := runtime.RunJobs(context.Background(), 1, func(context.Context, repository.LifecycleJob) error { return nil }); !errors.Is(err, completionFailure) {
		t.Fatalf("RunJobs() error=%v", err)
	}
}

func TestRuntimeLeaseHeartbeatRenewsUntilHandlerCompletes(t *testing.T) {
	job := runtimeTestJob("renewed")
	renewed := make(chan struct{})
	var once sync.Once
	store := &runtimeTestStore{
		claim: func(context.Context, repository.LifecycleJobKind, repository.Format, int) ([]repository.LifecycleJob, error) {
			return []repository.LifecycleJob{job}, nil
		},
		renew: func(context.Context, string, string) error {
			once.Do(func() { close(renewed) })
			return nil
		},
	}
	runtime := Runtime{Store: store, Kind: repository.LifecycleJobScan, Formats: []repository.Format{repository.FormatRaw}, Name: "artifact scan", LeaseRefreshInterval: time.Millisecond}
	if err := runtime.RunJobs(context.Background(), 1, func(context.Context, repository.LifecycleJob) error {
		select {
		case <-renewed:
			return nil
		case <-time.After(time.Second):
			return errors.New("lease was not renewed")
		}
	}); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeLeaseHeartbeatFailureCancelsHandler(t *testing.T) {
	leaseFailure := errors.New("renew unavailable")
	job := runtimeTestJob("lease-failure")
	var failedMessage string
	store := &runtimeTestStore{
		claim: func(context.Context, repository.LifecycleJobKind, repository.Format, int) ([]repository.LifecycleJob, error) {
			return []repository.LifecycleJob{job}, nil
		},
		renew: func(context.Context, string, string) error { return leaseFailure },
		fail: func(_ context.Context, _, _, message string) error {
			failedMessage = message
			return nil
		},
	}
	runtime := Runtime{Store: store, Kind: repository.LifecycleJobScan, Formats: []repository.Format{repository.FormatRaw}, Name: "artifact scan", LeaseRefreshInterval: time.Millisecond}
	err := runtime.RunJobs(context.Background(), 1, func(ctx context.Context, _ repository.LifecycleJob) error {
		<-ctx.Done()
		return nil
	})
	if !errors.Is(err, leaseFailure) || failedMessage != "artifact scan lease renewal failed: renew unavailable" {
		t.Fatalf("RunJobs() error=%v failedMessage=%q", err, failedMessage)
	}
}

func TestRuntimeLeaseHeartbeatFailureOverridesHandlerCancellation(t *testing.T) {
	leaseFailure := errors.New("renew unavailable")
	job := runtimeTestJob("lease-handler-failure")
	store := &runtimeTestStore{
		claim: func(context.Context, repository.LifecycleJobKind, repository.Format, int) ([]repository.LifecycleJob, error) {
			return []repository.LifecycleJob{job}, nil
		},
		renew: func(context.Context, string, string) error { return leaseFailure },
	}
	runtime := Runtime{Store: store, Kind: repository.LifecycleJobScan, Formats: []repository.Format{repository.FormatRaw}, Name: "artifact scan", LeaseRefreshInterval: time.Millisecond}
	err := runtime.RunJobs(context.Background(), 1, func(ctx context.Context, _ repository.LifecycleJob) error {
		<-ctx.Done()
		return errors.New("handler cancelled")
	})
	if !errors.Is(err, leaseFailure) || err.Error() != "artifact scan lease renewal failed: renew unavailable" {
		t.Fatalf("RunJobs() error=%v", err)
	}
}

func TestRuntimeTreatsRenewalCancellationAsCleanShutdown(t *testing.T) {
	job := runtimeTestJob("renew-cancelled")
	renewStarted := make(chan struct{})
	store := &runtimeTestStore{
		claim: func(context.Context, repository.LifecycleJobKind, repository.Format, int) ([]repository.LifecycleJob, error) {
			return []repository.LifecycleJob{job}, nil
		},
		renew: func(ctx context.Context, _, _ string) error {
			close(renewStarted)
			<-ctx.Done()
			return ctx.Err()
		},
	}
	runtime := Runtime{Store: store, Kind: repository.LifecycleJobScan, Formats: []repository.Format{repository.FormatRaw}, Name: "artifact scan", LeaseRefreshInterval: time.Millisecond}
	if err := runtime.RunJobs(context.Background(), 1, func(context.Context, repository.LifecycleJob) error {
		select {
		case <-renewStarted:
			return nil
		case <-time.After(time.Second):
			return errors.New("lease renewal did not start")
		}
	}); err != nil {
		t.Fatal(err)
	}
}

type startOrderStore struct {
	listened      chan struct{}
	wake          chan struct{}
	claimObserved chan bool
	claims        atomic.Int64
}

func (s *startOrderStore) Listen(context.Context, string) <-chan struct{} {
	close(s.listened)
	return s.wake
}

func (s *startOrderStore) ClaimLifecycleJobsByKindAndFormat(context.Context, repository.LifecycleJobKind, repository.Format, int) ([]repository.LifecycleJob, error) {
	s.claims.Add(1)
	listened := false
	select {
	case <-s.listened:
		listened = true
	default:
	}
	select {
	case s.claimObserved <- listened:
	default:
	}
	return nil, nil
}

func (s *startOrderStore) UpdateLifecycleJobProgress(context.Context, string, string, int, int, string) error {
	return nil
}

func (s *startOrderStore) RenewLifecycleJobLease(context.Context, string, string) error {
	return nil
}

func (s *startOrderStore) CompleteLifecycleJob(context.Context, string, string) error {
	return nil
}

func (s *startOrderStore) FailLifecycleJob(context.Context, string, string, string) error {
	return nil
}

func TestRuntimeSubscribesBeforeInitialClaim(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	store := &startOrderStore{
		listened:      make(chan struct{}),
		wake:          make(chan struct{}),
		claimObserved: make(chan bool, 1),
	}
	runtime := Runtime{
		Store:     store,
		Kind:      repository.LifecycleJobScan,
		Formats:   []repository.Format{repository.FormatRaw},
		Name:      "artifact scan",
		Operation: "scan",
	}
	runtime.Start(ctx, time.Hour, 1, func(context.Context, repository.LifecycleJob) error { return nil })
	select {
	case listened := <-store.claimObserved:
		if !listened {
			t.Fatal("initial claim ran before lifecycle notification subscription")
		}
	case <-time.After(time.Second):
		t.Fatal("initial claim did not run")
	}
}

func TestRuntimeStopsListeningAfterNotificationChannelCloses(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	wake := make(chan struct{})
	close(wake)
	store := &startOrderStore{
		listened:      make(chan struct{}),
		wake:          wake,
		claimObserved: make(chan bool, 1),
	}
	runtime := Runtime{
		Store:     store,
		Kind:      repository.LifecycleJobScan,
		Formats:   []repository.Format{repository.FormatRaw},
		Name:      "artifact scan",
		Operation: "scan",
	}
	runtime.Start(ctx, time.Hour, 1, func(context.Context, repository.LifecycleJob) error { return nil })
	select {
	case <-store.claimObserved:
	case <-time.After(time.Second):
		t.Fatal("initial claim did not run")
	}
	time.Sleep(20 * time.Millisecond)
	if claims := store.claims.Load(); claims > 2 {
		t.Fatalf("closed notification channel triggered a busy loop: claims=%d", claims)
	}
}

func TestRuntimeRunsAfterLifecycleNotification(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	store := &startOrderStore{
		listened:      make(chan struct{}),
		wake:          make(chan struct{}, 1),
		claimObserved: make(chan bool, 2),
	}
	runtime := Runtime{Store: store, Kind: repository.LifecycleJobScan, Formats: []repository.Format{repository.FormatRaw}, Name: "artifact scan"}
	runtime.Start(ctx, time.Hour, 1, func(context.Context, repository.LifecycleJob) error { return nil })
	select {
	case <-store.claimObserved:
	case <-time.After(time.Second):
		t.Fatal("initial claim did not run")
	}
	store.wake <- struct{}{}
	select {
	case <-store.claimObserved:
	case <-time.After(time.Second):
		t.Fatal("notification did not trigger a claim")
	}
	cancel()
	time.Sleep(time.Millisecond)
}

func TestRuntimePollsWithoutNotificationAdapter(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var claims atomic.Int64
	claimed := make(chan struct{}, 2)
	store := &runtimeTestStore{
		claim: func(context.Context, repository.LifecycleJobKind, repository.Format, int) ([]repository.LifecycleJob, error) {
			claims.Add(1)
			select {
			case claimed <- struct{}{}:
			default:
			}
			return nil, nil
		},
	}
	runtime := Runtime{Store: store, Kind: repository.LifecycleJobScan, Formats: []repository.Format{repository.FormatRaw}, Name: "artifact scan"}
	runtime.Start(ctx, time.Millisecond, 1, func(context.Context, repository.LifecycleJob) error { return nil })
	for claims.Load() < 2 {
		select {
		case <-claimed:
		case <-time.After(time.Second):
			t.Fatal("polling did not trigger a second claim")
		}
	}
	cancel()
	time.Sleep(time.Millisecond)
}

func TestRuntimeStartRejectsInvalidConfigurationWithoutClaimingJobs(t *testing.T) {
	var claims atomic.Int64
	store := &runtimeTestStore{
		claim: func(context.Context, repository.LifecycleJobKind, repository.Format, int) ([]repository.LifecycleJob, error) {
			claims.Add(1)
			return nil, nil
		},
	}
	runtime := Runtime{Store: store, Kind: repository.LifecycleJobScan, Formats: []repository.Format{repository.FormatRaw}}
	runtime.Start(context.Background(), time.Hour, 1, nil)
	runtime.Start(context.Background(), 0, 1, func(context.Context, repository.LifecycleJob) error { return nil })
	time.Sleep(time.Millisecond)
	if got := claims.Load(); got != 0 {
		t.Fatalf("invalid runtime configuration claimed %d job batches", got)
	}
}
