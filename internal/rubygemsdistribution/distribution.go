// Package rubygemsdistribution owns the durable, opt-in Hosted replication job.
// It has no SQL, HTTP or format implementation dependency.
package rubygemsdistribution

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"

	protocol "github.com/ccsert/artifact-gateway/internal/protocol/rubygems"
	"github.com/google/uuid"
)

const MaxItems = 100

var (
	ErrInvalid       = errors.New("invalid RubyGems replication plan")
	ErrConflict      = errors.New("rubygems replication version or idempotency conflict")
	ErrNotFound      = errors.New("rubygems replication not found")
	ErrLeaseLost     = errors.New("rubygems replication lease lost")
	ErrAccessDenied  = errors.New("rubygems replication access denied")
	ErrSourceChanged = errors.New("rubygems replication source changed or governed")
	ErrTargetBlocked = errors.New("rubygems replication target identity governed or conflicting")
	ErrCapacity      = errors.New("rubygems replication target capacity exceeded")
)

type Identity struct {
	Name                    string `json:"name"`
	Version                 string `json:"version"`
	Digest                  string `json:"digest"`
	Size                    int64  `json:"size"`
	Platform                string `json:"platform"`
	SourceGovernanceVersion string `json:"sourceGovernanceVersion"`
}
type Plan struct {
	SourceRepositoryID string     `json:"sourceRepositoryId"`
	TargetRepositoryID string     `json:"targetRepositoryId"`
	Items              []Identity `json:"items"`
}
type Item struct {
	Index int `json:"index"`
	Identity
	State           string     `json:"state"`
	Attempts        int        `json:"attempts"`
	Code            string     `json:"code,omitempty"`
	TargetSessionID string     `json:"targetSessionId,omitempty"`
	Result          string     `json:"result,omitempty"`
	CompletedAt     *time.Time `json:"completedAt,omitempty"`
}
type Job struct {
	ID                 string    `json:"id"`
	PlanDigest         string    `json:"planDigest"`
	SourceRepositoryID string    `json:"sourceRepositoryId"`
	TargetRepositoryID string    `json:"targetRepositoryId"`
	Actor              string    `json:"actor"`
	ActorKind          string    `json:"-"`
	CredentialID       string    `json:"-"`
	UserID, SessionID  string    `json:"-"`
	SessionVersion     int64     `json:"-"`
	ValidUntil         time.Time `json:"-"`
	Version            string    `json:"version"`
	State              string    `json:"state"`
	Items              []Item    `json:"items"`
	CreatedAt          time.Time `json:"createdAt"`
	UpdatedAt          time.Time `json:"updatedAt"`
	Lease              Lease     `json:"-"`
}
type Lease struct {
	JobID, Owner, Token string
	Fence               int64
	Until               time.Time
}
type Grant struct {
	Principal      string
	Scopes         []string
	ResourcePrefix string
}
type RepositoryAuthority struct {
	ID, Name, Version string
	Grants            []Grant
}

// Authority is read under the publication transaction's subject and grant-set
// locks, not copied from the submitting HTTP principal. No secret is included.
type Subject struct {
	Actor, Kind, CredentialID, UserID, SessionID string
	SessionVersion                               int64
	ValidUntil                                   time.Time
}

func (j Job) Subject() Subject {
	return Subject{j.Actor, j.ActorKind, j.CredentialID, j.UserID, j.SessionID, j.SessionVersion, j.ValidUntil}
}

type Authority struct {
	Subject                    Subject
	Role                       string
	Active, MustChangePassword bool
	ValidUntil                 time.Time
	Source, Target             RepositoryAuthority
}
type Check func(context.Context, Authority) error

type Commit struct{ SessionID string }
type Store interface {
	CreateRubyGemsReplication(context.Context, Job, Check) (Job, bool, error)
	GetRubyGemsReplication(context.Context, string) (Job, error)
	RetryRubyGemsReplication(context.Context, string, string, []int, string, Check) (Job, bool, error)
	ClaimRubyGemsReplication(context.Context, string, time.Duration) (Job, error)
	RenewRubyGemsReplication(context.Context, Lease, time.Duration) (Lease, error)
	AuthorizeRubyGemsReplicationItem(context.Context, Lease, int, Check) error
	BindRubyGemsReplicationSession(context.Context, Lease, int, string, Check) (Job, error)
	CompleteRubyGemsReplicationItem(context.Context, Lease, int, Commit, Check) (Job, error)
	FailRubyGemsReplicationItem(context.Context, Lease, int, string) error
	FinishRubyGemsReplication(context.Context, Lease) (Job, error)
}

func CanonicalPlan(in Plan) (Plan, string, error) {
	source, sourceErr := uuid.Parse(in.SourceRepositoryID)
	target, targetErr := uuid.Parse(in.TargetRepositoryID)
	if sourceErr != nil || targetErr != nil || source == target || len(in.Items) < 1 || len(in.Items) > MaxItems {
		return Plan{}, "", ErrInvalid
	}
	in.SourceRepositoryID = source.String()
	in.TargetRepositoryID = target.String()
	in.Items = append([]Identity(nil), in.Items...)
	sort.Slice(in.Items, func(i, j int) bool {
		a, b := in.Items[i], in.Items[j]
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		if protocol.CanonicalVersion(a.Version) != protocol.CanonicalVersion(b.Version) {
			return protocol.CanonicalVersion(a.Version) < protocol.CanonicalVersion(b.Version)
		}
		return a.Platform < b.Platform
	})
	for i, v := range in.Items {
		d, e := hex.DecodeString(strings.TrimPrefix(v.Digest, "sha256:"))
		g, ge := strconv.ParseInt(v.SourceGovernanceVersion, 10, 64)
		if !protocol.ValidName(v.Name) || !protocol.ValidVersion(v.Version) || !protocol.ValidPlatform(v.Platform) || !strings.HasPrefix(v.Digest, "sha256:") || e != nil || len(d) != 32 || strings.ToLower(v.Digest) != v.Digest || v.Size < 1 || v.Size > protocol.MaxGemBytes || ge != nil || g < 1 || strconv.FormatInt(g, 10) != v.SourceGovernanceVersion {
			return Plan{}, "", ErrInvalid
		}
		if i > 0 && in.Items[i-1].Name == v.Name && protocol.CanonicalVersion(in.Items[i-1].Version) == protocol.CanonicalVersion(v.Version) && in.Items[i-1].Platform == v.Platform {
			return Plan{}, "", ErrInvalid
		}
	}
	body, _ := json.Marshal(in)
	sum := sha256.Sum256(body)
	return in, "sha256:" + hex.EncodeToString(sum[:]), nil
}
func NewJob(plan Plan, subject Subject, key string) (Job, error) {
	actor, kind, credential := subject.Actor, subject.Kind, subject.CredentialID
	p, d, err := CanonicalPlan(plan)
	if err != nil || !ValidSubject(subject) || actor == "" || len(actor) > 512 || strings.ContainsAny(actor, "\x00\r\n") || key == "" || len(key) > 200 || strings.ContainsAny(key, "\x00\r\n") {
		return Job{}, ErrInvalid
	}
	switch kind {
	case "static_admin", "static_resolver", "local_session", "api_key", "service_account_credential", "oidc":
	default:
		return Job{}, ErrInvalid
	}
	if kind == "service_account_credential" {
		if _, err := uuid.Parse(credential); err != nil {
			return Job{}, ErrInvalid
		}
	} else if credential != "" {
		return Job{}, ErrInvalid
	}
	j := Job{ID: uuid.NewSHA1(uuid.NameSpaceURL, []byte("rubygems-replication\x00"+actor+"\x00"+kind+"\x00"+key)).String(), PlanDigest: d, SourceRepositoryID: p.SourceRepositoryID, TargetRepositoryID: p.TargetRepositoryID, Actor: actor, ActorKind: kind, CredentialID: credential, UserID: subject.UserID, SessionID: subject.SessionID, SessionVersion: subject.SessionVersion, ValidUntil: subject.ValidUntil}
	for i, v := range p.Items {
		j.Items = append(j.Items, Item{Index: i, Identity: v, State: "pending"})
	}
	return j, nil
}
func ValidNewJob(j Job) bool {
	if _, err := uuid.Parse(j.ID); err != nil || j.Version != "" || j.State != "" || !j.CreatedAt.IsZero() || !j.UpdatedAt.IsZero() || j.Lease != (Lease{}) {
		return false
	}
	p := Plan{SourceRepositoryID: j.SourceRepositoryID, TargetRepositoryID: j.TargetRepositoryID}
	for i, v := range j.Items {
		if v.Index != i || v.State != "pending" || v.Attempts != 0 || v.Code != "" || v.TargetSessionID != "" || v.CompletedAt != nil || v.Result != "" {
			return false
		}
		p.Items = append(p.Items, v.Identity)
	}
	normalized, d, err := CanonicalPlan(p)
	if err != nil || d != j.PlanDigest {
		return false
	}
	for i := range p.Items {
		if normalized.Items[i] != p.Items[i] {
			return false
		}
	}
	_, err = NewJob(p, j.Subject(), "validation")
	return err == nil
}
func Clone(j Job) Job {
	j.Items = append([]Item(nil), j.Items...)
	for i := range j.Items {
		if j.Items[i].CompletedAt != nil {
			v := *j.Items[i].CompletedAt
			j.Items[i].CompletedAt = &v
		}
	}
	return j
}
func NextVersion(v string) string {
	n, _ := strconv.ParseInt(v, 10, 64)
	return strconv.FormatInt(n+1, 10)
}
func ValidLease(lease Lease) bool {
	return lease.JobID != "" && lease.Owner != "" && lease.Token != "" && lease.Fence > 0 && lease.Until.After(time.Now())
}
func ValidDuration(d time.Duration) bool { return d >= time.Second && d <= 5*time.Minute }
func RetryDigest(version string, indexes []int) (string, error) {
	numeric, versionErr := strconv.ParseInt(version, 10, 64)
	if versionErr != nil || numeric < 1 || strconv.FormatInt(numeric, 10) != version || len(indexes) < 1 || len(indexes) > MaxItems {
		return "", ErrInvalid
	}
	indexes = append([]int(nil), indexes...)
	sort.Ints(indexes)
	for i, x := range indexes {
		if x < 0 || x >= MaxItems || i > 0 && indexes[i-1] == x {
			return "", ErrInvalid
		}
	}
	b, _ := json.Marshal(struct {
		Version string
		Indexes []int
	}{version, indexes})
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:]), nil
}

// ValidSubject validates only identity references captured after authentication;
// current authority and original credential expiry are re-read at every commit.
func ValidSubject(s Subject) bool {
	if s.Actor == "" || len(s.Actor) > 256 || strings.ContainsAny(s.Actor, "\x00\r\n") || s.SessionVersion < 0 {
		return false
	}
	switch s.Kind {
	case "static_admin", "static_resolver":
		return s.CredentialID == "" && s.UserID == "" && s.SessionID == "" && s.SessionVersion == 0 && s.ValidUntil.IsZero()
	case "api_key":
		_, e := uuid.Parse(strings.TrimPrefix(s.Actor, "api-key:"))
		return strings.HasPrefix(s.Actor, "api-key:") && e == nil && s.CredentialID == "" && s.UserID == "" && s.SessionID == "" && s.SessionVersion == 0
	case "service_account_credential":
		_, e := uuid.Parse(strings.TrimPrefix(s.Actor, "service-account:"))
		_, ce := uuid.Parse(s.CredentialID)
		return strings.HasPrefix(s.Actor, "service-account:") && e == nil && ce == nil && s.UserID == "" && s.SessionID == "" && s.SessionVersion == 0
	case "local_session", "oidc":
		_, e := uuid.Parse(s.UserID)
		if !strings.HasPrefix(s.Actor, "user:") || e != nil || s.CredentialID != "" || s.ValidUntil.IsZero() {
			return false
		}
		if s.SessionID != "" {
			_, e = uuid.Parse(s.SessionID)
		}
		return e == nil
	}
	return false
}

func SameSubject(a, b Subject) bool {
	return a.Actor == b.Actor && a.Kind == b.Kind && a.CredentialID == b.CredentialID && a.UserID == b.UserID && a.SessionID == b.SessionID && a.SessionVersion == b.SessionVersion && a.ValidUntil.Equal(b.ValidUntil)
}
func CheckAuthority(ctx context.Context, check Check, a Authority) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if check == nil || !a.Active || !a.ValidUntil.IsZero() && !a.ValidUntil.After(time.Now()) {
		return ErrAccessDenied
	}
	return check(ctx, a)
}
func LeaseMatches(j Job, l Lease) bool {
	return j.State == "running" && l.JobID == j.ID && j.Lease.Owner == l.Owner && j.Lease.Token == l.Token && j.Lease.Fence == l.Fence && j.Lease.Until.After(time.Now())
}
func RetryJob(j Job, expected string, indexes []int) (Job, error) {
	if j.Version != expected || j.State != "failed" {
		return j, ErrConflict
	}
	for _, i := range indexes {
		if i < 0 || i >= len(j.Items) || j.Items[i].State != "failed" {
			return j, ErrConflict
		}
	}
	j = Clone(j)
	for _, i := range indexes {
		j.Items[i].State = "pending"
		j.Items[i].Code = ""
		j.Items[i].Result = ""
		j.Items[i].CompletedAt = nil
	}
	j.State = "queued"
	j.Version = NextVersion(j.Version)
	j.UpdatedAt = time.Now().UTC()
	j.Lease.Owner = ""
	j.Lease.Token = ""
	j.Lease.Until = time.Time{}
	return j, nil
}
