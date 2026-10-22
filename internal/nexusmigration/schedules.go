package nexusmigration

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ScheduleBinding is copied from a completed batch. Editing a source, mapping or
// policy requires a new successful batch and a new plan, never a silent rebind.
type ScheduleBinding struct {
	TemplateBatchID string    `json:"templateBatchId"`
	SourceID        string    `json:"sourceId"`
	SourceVersion   string    `json:"sourceVersion"`
	Mappings        []Mapping `json:"mappings"`
	PolicyVersion   string    `json:"policyVersion"`
	IntervalSeconds int       `json:"intervalSeconds"`
}

type ScheduleCreate struct {
	TemplateBatchID  string            `json:"templateBatchId"`
	IntervalSeconds  int               `json:"intervalSeconds"`
	CapacityEvidence *CapacityEvidence `json:"capacityEvidence,omitempty"`
}

type Schedule struct {
	ScheduleBinding
	ID                 string            `json:"id"`
	Version            string            `json:"version"`
	State              string            `json:"state"`
	NextRunAt          *time.Time        `json:"nextRunAt"`
	LastSuccessfulAt   *time.Time        `json:"lastSuccessfulAt"`
	ConsumedObservedAt *time.Time        `json:"consumedObservedAt"`
	CapacityEvidence   *CapacityEvidence `json:"capacityEvidence"`
	ActiveBatchID      string            `json:"activeBatchId"`
	LastBatchID        string            `json:"lastBatchId"`
	LastError          string            `json:"lastError"`
	DispatchAttempts   int               `json:"dispatchAttempts"`
	CreatedAt          time.Time         `json:"createdAt"`
	UpdatedAt          time.Time         `json:"updatedAt"`
	Actor              string            `json:"-"`
	LeaseToken         string            `json:"-"`
	LeaseEpoch         int64             `json:"-"`
}

// ScheduleStore owns only migration plans and atomically creates their existing
// migration batches. It is deliberately separate from repository.GatewayStore.
type ScheduleStore interface {
	ScheduleTemplate(context.Context, string) (Batch, error)
	ReplayScheduleCreate(context.Context, string, string, string) (Schedule, bool, error)
	ReplayScheduleControl(context.Context, string, string, string, string, string) (Schedule, bool, error)
	CreateSchedule(context.Context, Schedule, string, string) (Schedule, error)
	GetSchedule(context.Context, string) (Schedule, error)
	ListSchedules(context.Context, int, string) ([]Schedule, string, error)
	ControlSchedule(context.Context, string, string, string, string, string, string, *CapacityEvidence, time.Time) (Schedule, error)
	ReconcileSchedules(context.Context, int) error
	ClaimDueSchedule(context.Context, time.Time) (Schedule, error)
	DispatchSchedule(context.Context, Schedule, Batch, time.Time) error
	FailSchedule(context.Context, Schedule, string, string, time.Time) error
}

type ScheduleService struct {
	store   ScheduleStore
	batches *Service
	now     func() time.Time
}

func NewSchedules(store ScheduleStore, batches *Service) *ScheduleService {
	now := time.Now
	if batches != nil {
		now = batches.options.Now
	}
	return &ScheduleService{store: store, batches: batches, now: now}
}

func (s *ScheduleService) Available() bool {
	return s != nil && s.store != nil && s.batches.Available()
}

func (s *ScheduleService) validateBinding(ctx context.Context, p Schedule) error {
	if p.PolicyVersion != PolicyVersion {
		return Failed("policy_version_changed")
	}
	if err := s.batches.sourceUnchanged(ctx, Batch{StartInput: StartInput{SourceID: p.SourceID, SourceVersion: p.SourceVersion}}); err != nil {
		return err
	}
	return s.batches.publication.ValidateTargets(ctx, p.Mappings)
}

func (s *ScheduleService) Create(ctx context.Context, in ScheduleCreate, actor, key string) (Schedule, error) {
	if !s.Available() {
		return Schedule{}, Failed("migration_execution_disabled")
	}
	if !validKey(actor) || !validKey(key) || in.IntervalSeconds < 60 || in.IntervalSeconds > 30*24*60*60 {
		return Schedule{}, Failed("invalid_request")
	}
	if _, err := uuid.Parse(in.TemplateBatchID); err != nil {
		return Schedule{}, Failed("invalid_request")
	}
	hash := digest(in)
	if p, found, err := s.store.ReplayScheduleCreate(ctx, actor, key, hash); err != nil || found {
		return p, err
	}
	b, err := s.store.ScheduleTemplate(ctx, in.TemplateBatchID)
	if err != nil {
		return Schedule{}, err
	}
	if b.State != "completed" || !b.ScanComplete || b.LastError != "" {
		return Schedule{}, Failed("successful_template_required")
	}
	now := s.now().UTC()
	p := Schedule{ScheduleBinding: ScheduleBinding{TemplateBatchID: b.ID, SourceID: b.SourceID, SourceVersion: b.SourceVersion, Mappings: b.Mappings, PolicyVersion: b.PolicyVersion, IntervalSeconds: in.IntervalSeconds}, ID: uuid.NewString(), Actor: actor, State: "waiting_capacity", NextRunAt: &now, LastBatchID: b.ID, LastSuccessfulAt: &b.UpdatedAt, ConsumedObservedAt: &b.CapacityEvidence.ObservedAt, LastError: "capacity_evidence_refresh_required"}
	if err = s.validateBinding(ctx, p); err != nil {
		return Schedule{}, err
	}
	if in.CapacityEvidence != nil {
		if err = s.validateObservation(p, *in.CapacityEvidence); err != nil {
			return Schedule{}, err
		}
		p.State, p.LastError, p.CapacityEvidence = "active", "", in.CapacityEvidence
	}
	return s.store.CreateSchedule(ctx, p, key, hash)
}

func (s *ScheduleService) validateObservation(p Schedule, e CapacityEvidence) error {
	if err := s.batches.ValidateEvidence(e); err != nil {
		return err
	}
	if p.ConsumedObservedAt != nil && !e.ObservedAt.After(*p.ConsumedObservedAt) || p.CapacityEvidence != nil && !e.ObservedAt.After(p.CapacityEvidence.ObservedAt) {
		return Failed("capacity_evidence_refresh_required")
	}
	return nil
}

func (s *ScheduleService) Get(ctx context.Context, id string) (Schedule, error) {
	return s.store.GetSchedule(ctx, id)
}

func (s *ScheduleService) List(ctx context.Context, limit int, cursor string) ([]Schedule, string, error) {
	if !validPage(limit, cursor) {
		return nil, "", Failed("invalid_request")
	}
	return s.store.ListSchedules(ctx, limit, cursor)
}

func (s *ScheduleService) Control(ctx context.Context, id, expected, actor, key, action string, evidence *CapacityEvidence) (Schedule, error) {
	if !s.Available() {
		return Schedule{}, Failed("migration_execution_disabled")
	}
	if !validKey(actor) || !validKey(key) || expected == "" || action != "pause" && action != "resume" && action != "stop" && action != "capacity" {
		return Schedule{}, Failed("invalid_request")
	}
	hash := digest(struct {
		Action   string
		Evidence *CapacityEvidence
	}{action, evidence})
	if p, found, err := s.store.ReplayScheduleControl(ctx, id, actor, key, action, hash); err != nil || found {
		return p, err
	}
	p, err := s.store.GetSchedule(ctx, id)
	if err != nil {
		return p, err
	}
	if p.Version != expected {
		return p, ErrVersionConflict
	}
	if action == "resume" || action == "capacity" {
		if evidence == nil {
			return p, Failed("capacity_unknown")
		}
		if err = s.validateObservation(p, *evidence); err != nil {
			return p, err
		}
		if err = s.validateBinding(ctx, p); err != nil {
			return p, err
		}
	}
	return s.store.ControlSchedule(ctx, id, expected, actor, key, action, hash, evidence, s.now().UTC())
}

// RunDue only dispatches durable batches. Missed occurrences coalesce, and one
// observation cannot authorize several occurrences even before it expires.
func (s *ScheduleService) RunDue(ctx context.Context, limit int) error {
	if !s.Available() || limit < 1 || limit > 100 {
		return Failed("invalid_request")
	}
	if err := s.store.ReconcileSchedules(ctx, limit); err != nil {
		return err
	}
	for range limit {
		now := s.now().UTC()
		p, err := s.store.ClaimDueSchedule(ctx, now)
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		err = s.validateBinding(ctx, p)
		if err == nil {
			if p.CapacityEvidence == nil {
				err = Failed("capacity_evidence_refresh_required")
			} else {
				err = s.batches.ValidateEvidence(*p.CapacityEvidence)
				if err == nil && p.ConsumedObservedAt != nil && !p.CapacityEvidence.ObservedAt.After(*p.ConsumedObservedAt) {
					err = Failed("capacity_evidence_refresh_required")
				}
			}
		}
		if err == nil {
			b := Batch{ID: uuid.NewString(), Actor: p.Actor, StartInput: StartInput{SourceID: p.SourceID, SourceVersion: p.SourceVersion, Mappings: p.Mappings, PolicyVersion: p.PolicyVersion, CapacityEvidence: *p.CapacityEvidence}}
			err = s.store.DispatchSchedule(ctx, p, b, now)
		}
		if errors.Is(err, ErrLeaseLost) {
			continue
		}
		if err != nil {
			state, message := "retry", "scheduler_dispatch_failed"
			var failure *Failure
			if errors.As(err, &failure) {
				state, message = "blocked", failure.Code
				if strings.HasPrefix(message, "capacity_") {
					state = "waiting_capacity"
				}
			}
			if err = s.store.FailSchedule(ctx, p, state, message, now); err != nil && !errors.Is(err, ErrLeaseLost) {
				return err
			}
		}
	}
	return nil
}

func (s *ScheduleService) StartScheduler(ctx context.Context, interval time.Duration) <-chan struct{} {
	done := make(chan struct{})
	if interval <= 0 || !s.Available() {
		close(done)
		return done
	}
	go func() {
		defer close(done)
		_ = s.RunDue(ctx, 100)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_ = s.RunDue(ctx, 100)
			}
		}
	}()
	return done
}
