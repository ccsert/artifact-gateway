// Package helmdistribution owns the durable, opt-in Hosted replication job.
// It has no SQL, HTTP or format implementation dependency.
package helmdistribution

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Masterminds/semver/v3"
	"github.com/google/uuid"
)

const MaxItems = 100

var (
	ErrInvalid       = errors.New("invalid Helm replication plan")
	ErrConflict      = errors.New("Helm replication version or idempotency conflict")
	ErrNotFound      = errors.New("Helm replication not found")
	ErrLeaseLost     = errors.New("Helm replication lease lost")
	ErrAccessDenied  = errors.New("Helm replication access denied")
	ErrSourceChanged = errors.New("Helm replication source changed or governed")
	ErrTargetBlocked = errors.New("Helm replication target identity governed or conflicting")
	ErrCapacity      = errors.New("Helm replication target capacity exceeded")
)
var coordinateID = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

type Identity struct {
	Name                    string `json:"name"`
	Version                 string `json:"version"`
	Digest                  string `json:"digest"`
	Size                    int64  `json:"size"`
	ProvenanceDigest        string `json:"provenanceDigest"`
	ProvenanceSize          int64  `json:"provenanceSize"`
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
type Authority struct {
	Actor, Kind, Role          string
	Active, MustChangePassword bool
	ValidUntil                 time.Time
	Source, Target             RepositoryAuthority
}
type Check func(context.Context, Authority) error

type Commit struct{ SessionID string }
type Store interface {
	CreateHelmReplication(context.Context, Job, Check) (Job, bool, error)
	GetHelmReplication(context.Context, string) (Job, error)
	RetryHelmReplication(context.Context, string, string, []int, string, Check) (Job, bool, error)
	ClaimHelmReplication(context.Context, string, time.Duration) (Job, error)
	RenewHelmReplication(context.Context, Lease, time.Duration) (Lease, error)
	AuthorizeHelmReplicationItem(context.Context, Lease, int, Check) error
	BindHelmReplicationSession(context.Context, Lease, int, string, Check) (Job, error)
	CompleteHelmReplicationItem(context.Context, Lease, int, Commit, Check) (Job, error)
	FailHelmReplicationItem(context.Context, Lease, int, string) error
	FinishHelmReplication(context.Context, Lease) (Job, error)
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
		if in.Items[i].Name == in.Items[j].Name {
			return in.Items[i].Version < in.Items[j].Version
		}
		return in.Items[i].Name < in.Items[j].Name
	})
	for i, v := range in.Items {
		d, e := hex.DecodeString(strings.TrimPrefix(v.Digest, "sha256:"))
		_, versionErr := semver.StrictNewVersion(v.Version)
		g, ge := strconv.ParseInt(v.SourceGovernanceVersion, 10, 64)
		if !coordinateID.MatchString(v.Name) || versionErr != nil || len(v.Version) > 256 || !strings.HasPrefix(v.Digest, "sha256:") || e != nil || len(d) != 32 || strings.ToLower(v.Digest) != v.Digest || v.Size < 1 || v.Size > 64<<20 || !validProvenance(v.ProvenanceDigest, v.ProvenanceSize) || ge != nil || g < 1 || strconv.FormatInt(g, 10) != v.SourceGovernanceVersion {
			return Plan{}, "", ErrInvalid
		}
		if i > 0 && in.Items[i-1].Name == v.Name && in.Items[i-1].Version == v.Version {
			return Plan{}, "", ErrInvalid
		}
	}
	body, _ := json.Marshal(in)
	sum := sha256.Sum256(body)
	return in, "sha256:" + hex.EncodeToString(sum[:]), nil
}
func NewJob(plan Plan, actor, kind, credential, key string) (Job, error) {
	p, d, err := CanonicalPlan(plan)
	if err != nil || actor == "" || len(actor) > 512 || strings.ContainsAny(actor, "\x00\r\n") || key == "" || len(key) > 200 || strings.ContainsAny(key, "\x00\r\n") {
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
	j := Job{ID: uuid.NewSHA1(uuid.NameSpaceURL, []byte("helm-replication\x00"+actor+"\x00"+kind+"\x00"+key)).String(), PlanDigest: d, SourceRepositoryID: p.SourceRepositoryID, TargetRepositoryID: p.TargetRepositoryID, Actor: actor, ActorKind: kind, CredentialID: credential}
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
	_, err = NewJob(p, j.Actor, j.ActorKind, j.CredentialID, "validation")
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

func validProvenance(digest string, size int64) bool {
	if size == 0 {
		return digest == ""
	}
	d, err := hex.DecodeString(strings.TrimPrefix(digest, "sha256:"))
	return size > 0 && size <= 1<<20 && strings.HasPrefix(digest, "sha256:") && strings.ToLower(digest) == digest && err == nil && len(d) == 32
}
