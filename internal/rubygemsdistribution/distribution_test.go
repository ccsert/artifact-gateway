package rubygemsdistribution_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	dist "github.com/ccsert/artifact-gateway/internal/rubygemsdistribution"
	"github.com/google/uuid"
)

func plan() dist.Plan {
	return dist.Plan{SourceRepositoryID: uuid.NewString(), TargetRepositoryID: uuid.NewString(), Items: []dist.Identity{{Name: "Acme", Version: "1.0.0", Platform: "ruby", Digest: "sha256:" + strings.Repeat("a", 64), Size: 1, SourceGovernanceVersion: "1"}, {Name: "Acme", Version: "1.0.0", Platform: "java", Digest: "sha256:" + strings.Repeat("b", 64), Size: 1, SourceGovernanceVersion: "1"}}}
}
func TestRubyGemsReplicationCanonicalIdentityBoundsAndSubjects(t *testing.T) {
	p := plan()
	a, d, err := dist.CanonicalPlan(p)
	if err != nil || a.Items[0].Platform != "java" {
		t.Fatalf("canonical %+v %v", a, err)
	}
	reverse := p
	reverse.Items = []dist.Identity{p.Items[1], p.Items[0]}
	_, other, err := dist.CanonicalPlan(reverse)
	if err != nil || other != d || p.Items[0].Platform != "ruby" {
		t.Fatal("order changed identity or caller")
	}
	for _, field := range []string{"name", "version", "platform", "digest", "size", "governance", "duplicate", "canonical-alias", "same-repository", "unbounded", "empty"} {
		bad := p
		bad.Items = append([]dist.Identity(nil), p.Items...)
		switch field {
		case "name":
			bad.Items[0].Name = "../escape"
		case "version":
			bad.Items[0].Version = "latest"
		case "platform":
			bad.Items[0].Platform = ""
		case "digest":
			bad.Items[0].Digest = "sha256:" + strings.Repeat("A", 64)
		case "size":
			bad.Items[0].Size = 0
		case "governance":
			bad.Items[0].SourceGovernanceVersion = "01"
		case "duplicate":
			bad.Items = append(bad.Items, bad.Items[0])
		case "canonical-alias":
			v := bad.Items[0]
			v.Version = "1.0"
			bad.Items = append(bad.Items, v)
		case "same-repository":
			bad.TargetRepositoryID = bad.SourceRepositoryID
		case "unbounded":
			bad.Items = make([]dist.Identity, 101)
		case "empty":
			bad.Items = nil
		}
		if _, _, err = dist.CanonicalPlan(bad); !errors.Is(err, dist.ErrInvalid) {
			t.Fatalf("accepted %s", field)
		}
	}
	subject := dist.Subject{Actor: "operator", Kind: "static_admin"}
	j, err := dist.NewJob(p, subject, "key")
	if err != nil || !dist.ValidNewJob(j) {
		t.Fatalf("new %+v %v", j, err)
	}
	same, _ := dist.NewJob(reverse, subject, "key")
	if same.ID != j.ID || same.PlanDigest != j.PlanDigest {
		t.Fatal("idempotency differs")
	}
	bad := dist.Clone(j)
	bad.Items[0].State = "succeeded"
	if dist.ValidNewJob(bad) {
		t.Fatal("client checkpoint accepted")
	}
	bad = dist.Clone(j)
	bad.Items[0], bad.Items[1] = bad.Items[1], bad.Items[0]
	if dist.ValidNewJob(bad) {
		t.Fatal("noncanonical job accepted")
	}
	for _, s := range []dist.Subject{{Actor: "operator", Kind: "unknown"}, {Actor: "service-account:" + uuid.NewString(), Kind: "service_account_credential"}, {Actor: "user:a", Kind: "local_session"}, {Actor: "operator", Kind: "static_admin", SessionVersion: 1}} {
		if _, err = dist.NewJob(p, s, "key"); !errors.Is(err, dist.ErrInvalid) {
			t.Fatal("invalid subject accepted")
		}
	}
	for _, s := range []dist.Subject{{Actor: "api-key:" + uuid.NewString(), Kind: "api_key"}, {Actor: "service-account:" + uuid.NewString(), Kind: "service_account_credential", CredentialID: uuid.NewString()}, {Actor: "user:a", Kind: "local_session", UserID: uuid.NewString(), SessionID: uuid.NewString(), SessionVersion: 2, ValidUntil: time.Now().UTC().Add(time.Hour)}} {
		if !dist.ValidSubject(s) || !dist.SameSubject(s, s) {
			t.Fatalf("valid subject %+v", s)
		}
	}
	for _, indexes := range [][]int{nil, {-1}, {100}, {1, 1}} {
		if _, err = dist.RetryDigest("1", indexes); !errors.Is(err, dist.ErrInvalid) {
			t.Fatal("invalid retry accepted")
		}
	}
	if _, err = dist.RetryDigest("01", []int{0}); !errors.Is(err, dist.ErrInvalid) {
		t.Fatal("noncanonical version")
	}
}

type workerStore struct {
	dist.Store
	job                                    dist.Job
	claimErr, renewErr, failErr, finishErr error
	code                                   string
}

func (s *workerStore) ClaimRubyGemsReplication(context.Context, string, time.Duration) (dist.Job, error) {
	return s.job, s.claimErr
}
func (s *workerStore) RenewRubyGemsReplication(_ context.Context, l dist.Lease, _ time.Duration) (dist.Lease, error) {
	return l, s.renewErr
}
func (s *workerStore) FailRubyGemsReplicationItem(_ context.Context, _ dist.Lease, _ int, code string) error {
	s.code = code
	return s.failErr
}
func (s *workerStore) FinishRubyGemsReplication(context.Context, dist.Lease) (dist.Job, error) {
	return s.job, s.finishErr
}

type transfer func(context.Context, dist.Job, dist.Item) error

func (f transfer) ReplicateRubyGemsItem(c context.Context, j dist.Job, i dist.Item) error {
	return f(c, j, i)
}
func TestRubyGemsReplicationWorkerPreservesFailuresAndLeaseLoss(t *testing.T) {
	for _, e := range []error{nil, dist.ErrAccessDenied, dist.ErrSourceChanged, dist.ErrTargetBlocked, dist.ErrCapacity, dist.ErrConflict, dist.ErrLeaseLost, errors.New("owned transfer failure")} {
		s := &workerStore{job: dist.Job{Items: []dist.Item{{State: "succeeded"}, {Index: 1, State: "pending"}}}}
		w := dist.Worker{Store: s, Transfer: transfer(func(context.Context, dist.Job, dist.Item) error { return e }), Owner: "worker"}
		ran, err := w.RunOnce(t.Context())
		if !ran {
			t.Fatal("no work")
		}
		if errors.Is(e, dist.ErrLeaseLost) {
			if !errors.Is(err, e) || s.code != "" {
				t.Fatal("lease loss was checkpointed")
			}
		} else if err != nil || e != nil && s.code != dist.FailureCode(e) {
			t.Fatalf("worker %s %v", s.code, err)
		}
	}
	for _, stage := range []string{"claim", "renew", "fail", "finish"} {
		e := errors.New(stage)
		s := &workerStore{job: dist.Job{Items: []dist.Item{{State: "pending"}}}}
		switch stage {
		case "claim":
			s.claimErr = e
		case "renew":
			s.renewErr = e
		case "fail":
			s.failErr = e
		case "finish":
			s.finishErr = e
		}
		w := dist.Worker{Store: s, Transfer: transfer(func(context.Context, dist.Job, dist.Item) error {
			if stage == "fail" {
				return dist.ErrSourceChanged
			}
			return nil
		}), Owner: "worker"}
		if _, err := w.RunOnce(t.Context()); !errors.Is(err, e) {
			t.Fatalf("lost %s %v", stage, err)
		}
	}
	if _, err := (dist.Worker{}).RunOnce(t.Context()); !errors.Is(err, dist.ErrInvalid) {
		t.Fatal(err)
	}
	s := &workerStore{claimErr: dist.ErrNotFound}
	w := dist.Worker{Store: s, Transfer: transfer(func(context.Context, dist.Job, dist.Item) error { return nil }), Owner: "worker"}
	if ran, err := w.RunOnce(t.Context()); ran || err != nil {
		t.Fatal("empty queue")
	}
	w.LeaseDuration = time.Millisecond
	if _, err := w.RunOnce(t.Context()); !errors.Is(err, dist.ErrInvalid) {
		t.Fatal("invalid duration")
	}
	for _, code := range []string{"access_denied", "source_changed", "target_conflict", "identity_conflict", "quota_exceeded", "transfer_failed"} {
		if !dist.ValidFailureCode(code) || dist.FailureStatus(code) < 400 {
			t.Fatal(code)
		}
	}
	if dist.ValidFailureCode("raw internal error") {
		t.Fatal("unsafe code")
	}
}
