// Package capacityplan evaluates a fixed, normalized inventory against caller-
// supplied evidence. It performs no discovery, allocation, or storage writes.
package capacityplan

import (
	"encoding/hex"
	"math"
	"regexp"
	"sort"
	"strings"
	"time"
)

type Status string

const (
	Sufficient   Status = "sufficient"
	Insufficient Status = "insufficient"
	Unknown      Status = "unknown"
)

type Plan struct {
	SchemaVersion int         `json:"schemaVersion"`
	InventoryID   string      `json:"inventoryId"`
	Complete      bool        `json:"complete"`
	TargetID      string      `json:"targetId"`
	References    []Reference `json:"references"`
	Snapshot      Snapshot    `json:"snapshot"`
	Peak          PeakBudget  `json:"peak"`
}

// LogicalKey names one unit counted by the repository capacity contract.
// ObjectKey names its planned physical storage allocation, not just its digest.
type Reference struct {
	RepositoryID string `json:"repositoryId"`
	LogicalKey   string `json:"logicalKey"`
	ObjectKey    string `json:"objectKey"`
	Digest       string `json:"digest"`
	Size         *int64 `json:"size"`
}

// Snapshot binds all observations to one target and a caller-chosen validity
// interval. It does not establish a transaction or reserve future capacity.
type Snapshot struct {
	TargetID      string               `json:"targetId"`
	ObservedAt    time.Time            `json:"observedAt"`
	ValidUntil    time.Time            `json:"validUntil"`
	StoragePoolID string               `json:"storagePoolId"`
	FreeBytes     *int64               `json:"freeBytes"`
	Repositories  []RepositoryCapacity `json:"repositories"`
	Objects       []Presence           `json:"objects"`
	References    []Membership         `json:"references"`
}

// UsedBytes and QuotaBytes retain the existing repository API's logical units;
// quota zero explicitly disables that quota. Nil means unavailable, never zero.
type RepositoryCapacity struct {
	RepositoryID string `json:"repositoryId"`
	UsedBytes    *int64 `json:"usedBytes"`
	QuotaBytes   *int64 `json:"quotaBytes"`
}

// States are absent, verified (full bytes and identity), or unknown. A present
// object with only ETag/size evidence cannot use the verified state.
type Presence struct {
	Key    string `json:"key"`
	State  string `json:"state"`
	Digest string `json:"digest,omitempty"`
	Size   *int64 `json:"size,omitempty"`
}

type Membership struct {
	RepositoryID string `json:"repositoryId"`
	Key          string `json:"key"`
	State        string `json:"state"`
	Digest       string `json:"digest,omitempty"`
	Size         *int64 `json:"size,omitempty"`
}

// The first slice supports one physical pool. All peak components must describe
// simultaneous byte budgets in that pool; explicit zero is accepted.
type PeakBudget struct {
	StoragePoolID string `json:"storagePoolId"`
	DownloadBytes *int64 `json:"downloadBytes"`
	UploadBytes   *int64 `json:"uploadBytes"`
	BackupBytes   *int64 `json:"backupBytes"`
	RestoreBytes  *int64 `json:"restoreBytes"`
	HeadroomBytes *int64 `json:"headroomBytes"`
}

type Report struct {
	SchemaVersion int                `json:"schemaVersion"`
	InputSHA256   string             `json:"inputSha256,omitempty"`
	InventoryID   string             `json:"inventoryId"`
	CheckedAt     time.Time          `json:"checkedAt"`
	Status        Status             `json:"status"`
	Reasons       []string           `json:"reasons"`
	Repositories  []RepositoryResult `json:"repositories"`
	Storage       StorageResult      `json:"storage"`
}

type RepositoryResult struct {
	RepositoryID               string   `json:"repositoryId"`
	Status                     Status   `json:"status"`
	UsedBytes                  *int64   `json:"usedBytes"`
	QuotaBytes                 *int64   `json:"quotaBytes"`
	AdditionalBytes            *int64   `json:"additionalBytes"`
	ProjectedBytes             *int64   `json:"projectedBytes"`
	KnownMinimumProjectedBytes *int64   `json:"knownMinimumProjectedBytes"`
	Reasons                    []string `json:"reasons"`
}

type StorageResult struct {
	Status                Status     `json:"status"`
	FreeBytes             *int64     `json:"freeBytes"`
	AdditionalBytes       *int64     `json:"additionalBytes"`
	RequiredFreeBytes     *int64     `json:"requiredFreeBytes"`
	KnownMinimumFreeBytes *int64     `json:"knownMinimumFreeBytes"`
	Peak                  PeakBudget `json:"peak"`
	Reasons               []string   `json:"reasons"`
}

type logicalIdentity struct{ repository, key string }

type repositoryGrowth struct {
	total, minimum, verified *int64
	reasons                  []string
}

var identityPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)

// Evaluate returns sufficient only when every relevant quantity is known and
// fits this evidence snapshot. Insufficient takes precedence over unknown when
// an independent, fully known constraint is already exceeded.
func Evaluate(plan Plan, now time.Time) Report {
	report := Report{SchemaVersion: 1, CheckedAt: now.UTC(), Status: Unknown, Reasons: []string{}, Repositories: []RepositoryResult{}, Storage: StorageResult{Status: Unknown, Reasons: []string{}}}
	if identityPattern.MatchString(plan.InventoryID) {
		report.InventoryID = plan.InventoryID
	}
	if reason := validatePlan(plan, now); reason != "" {
		report.Reasons = append(report.Reasons, reason)
		return report
	}
	logical, physical, reason := groupReferences(plan.References)
	if reason != "" {
		report.Reasons = append(report.Reasons, reason)
		return report
	}
	objects, memberships, capacities := indexSnapshot(plan.Snapshot)
	growth := make(map[string]repositoryGrowth)
	for key, ref := range logical {
		g, ok := growth[key.repository]
		if !ok {
			g = repositoryGrowth{total: value(0), minimum: value(0), verified: value(0)}
		}
		evidence := memberships[key]
		delta, why := presenceDelta(evidence, ref)
		if evidence != nil && evidence.State == "verified" && objects[ref.ObjectKey] != nil && objects[ref.ObjectKey].State == "absent" {
			delta, why = nil, "reference_object_missing"
		}
		g.total = add(g.total, delta)
		g.minimum = addMinimum(g.minimum, delta)
		if delta != nil && evidence != nil && evidence.State == "verified" {
			g.verified = add(g.verified, ref.Size)
		}
		if why != "" {
			g.reasons = append(g.reasons, why)
		}
		growth[key.repository] = g
	}
	ids := make([]string, 0, len(growth))
	for id := range growth {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		report.Repositories = append(report.Repositories, repositoryResult(id, capacities[id], growth[id]))
	}
	report.Storage = storageResult(plan, physical, objects)
	report.Status = report.Storage.Status
	for _, result := range report.Repositories {
		report.Status = combine(report.Status, result.Status)
	}
	return report
}

func validatePlan(plan Plan, now time.Time) string {
	if plan.SchemaVersion != 1 {
		return "unsupported_schema_version"
	}
	if !plan.Complete || plan.References == nil || !identityPattern.MatchString(plan.InventoryID) {
		return "inventory_incomplete"
	}
	if !identityPattern.MatchString(plan.TargetID) || plan.Snapshot.TargetID != plan.TargetID {
		return "target_identity_unknown"
	}
	if plan.Snapshot.ObservedAt.IsZero() || plan.Snapshot.ObservedAt.After(now) || !plan.Snapshot.ValidUntil.After(now) || !plan.Snapshot.ValidUntil.After(plan.Snapshot.ObservedAt) {
		return "snapshot_outside_validity_interval"
	}
	if !identityPattern.MatchString(plan.Peak.StoragePoolID) || plan.Peak.StoragePoolID != plan.Snapshot.StoragePoolID {
		return "storage_pool_identity_unknown"
	}
	return ""
}

func groupReferences(refs []Reference) (map[logicalIdentity]Reference, map[string]Reference, string) {
	logical := make(map[logicalIdentity]Reference)
	physical := make(map[string]Reference)
	for _, ref := range refs {
		if !identityPattern.MatchString(ref.RepositoryID) || !validKey(ref.LogicalKey) || !validKey(ref.ObjectKey) || !validDigest(ref.Digest) {
			return nil, nil, "reference_identity_invalid"
		}
		key := logicalIdentity{ref.RepositoryID, ref.LogicalKey}
		if old, ok := logical[key]; ok && (old.ObjectKey != ref.ObjectKey || !sameBytes(old, ref)) {
			return nil, nil, "logical_identity_conflict"
		}
		if old, ok := physical[ref.ObjectKey]; ok && !sameBytes(old, ref) {
			return nil, nil, "physical_identity_conflict"
		}
		logical[key], physical[ref.ObjectKey] = ref, ref
	}
	return logical, physical, ""
}

func indexSnapshot(snapshot Snapshot) (map[string]*Presence, map[logicalIdentity]*Presence, map[string]*RepositoryCapacity) {
	objects := make(map[string]*Presence)
	for _, object := range snapshot.Objects {
		if _, ok := objects[object.Key]; ok {
			objects[object.Key] = nil // conflicting or duplicate observations are ambiguous
		} else {
			objects[object.Key] = &object
		}
	}
	memberships := make(map[logicalIdentity]*Presence)
	for _, membership := range snapshot.References {
		key := logicalIdentity{membership.RepositoryID, membership.Key}
		if _, ok := memberships[key]; ok {
			memberships[key] = nil
		} else {
			memberships[key] = &Presence{Key: membership.Key, State: membership.State, Digest: membership.Digest, Size: membership.Size}
		}
	}
	capacities := make(map[string]*RepositoryCapacity)
	for _, capacity := range snapshot.Repositories {
		if _, ok := capacities[capacity.RepositoryID]; ok {
			capacities[capacity.RepositoryID] = nil
		} else {
			capacities[capacity.RepositoryID] = &capacity
		}
	}
	return objects, memberships, capacities
}

func presenceDelta(evidence *Presence, ref Reference) (*int64, string) {
	if nonnegative(ref.Size) == nil {
		return nil, "object_size_unknown"
	}
	if evidence == nil {
		return nil, "presence_unknown"
	}
	switch evidence.State {
	case "absent":
		if evidence.Digest != "" || evidence.Size != nil {
			return nil, "absence_evidence_invalid"
		}
		return value(*ref.Size), ""
	case "verified":
		if evidence.Digest != ref.Digest || nonnegative(evidence.Size) == nil || *evidence.Size != *ref.Size {
			return nil, "verified_bytes_conflict"
		}
		return value(0), ""
	default:
		return nil, "presence_unknown"
	}
}

func repositoryResult(id string, capacity *RepositoryCapacity, growth repositoryGrowth) RepositoryResult {
	r := RepositoryResult{RepositoryID: id, Status: Unknown, AdditionalBytes: growth.total, Reasons: uniqueReasons(growth.reasons)}
	if capacity != nil {
		r.UsedBytes, r.QuotaBytes = nonnegative(capacity.UsedBytes), nonnegative(capacity.QuotaBytes)
	}
	if r.UsedBytes != nil && (growth.verified == nil || *growth.verified > *r.UsedBytes) {
		r.Reasons = append(r.Reasons, "verified_membership_exceeds_usage")
		return r
	}
	r.ProjectedBytes = add(r.UsedBytes, growth.total)
	knownUsage := r.UsedBytes
	if knownUsage == nil {
		// Verified memberships establish a lower bound even when the complete
		// repository usage is unavailable; unknown usage cannot erase new bytes.
		knownUsage = growth.verified
	}
	r.KnownMinimumProjectedBytes = add(knownUsage, growth.minimum)
	if r.UsedBytes == nil || r.QuotaBytes == nil {
		r.Reasons = append(r.Reasons, "capacity_unknown")
	}
	if growth.total == nil || (r.UsedBytes != nil && r.ProjectedBytes == nil) {
		r.Reasons = append(r.Reasons, "logical_growth_unknown_or_overflow")
	}
	if r.QuotaBytes != nil && *r.QuotaBytes > 0 && (r.KnownMinimumProjectedBytes == nil || *r.KnownMinimumProjectedBytes > *r.QuotaBytes) {
		r.Status = Insufficient
		r.Reasons = append(r.Reasons, "repository_quota_exceeded")
	} else if r.UsedBytes != nil && r.QuotaBytes != nil && r.ProjectedBytes != nil {
		r.Status = Sufficient
	}
	return r
}

func storageResult(plan Plan, physical map[string]Reference, evidence map[string]*Presence) StorageResult {
	r := StorageResult{Status: Unknown, FreeBytes: nonnegative(plan.Snapshot.FreeBytes), AdditionalBytes: value(0), KnownMinimumFreeBytes: value(0), Reasons: []string{}, Peak: PeakBudget{
		StoragePoolID: plan.Peak.StoragePoolID, DownloadBytes: nonnegative(plan.Peak.DownloadBytes), UploadBytes: nonnegative(plan.Peak.UploadBytes),
		BackupBytes: nonnegative(plan.Peak.BackupBytes), RestoreBytes: nonnegative(plan.Peak.RestoreBytes), HeadroomBytes: nonnegative(plan.Peak.HeadroomBytes),
	}}
	for key, ref := range physical {
		delta, reason := presenceDelta(evidence[key], ref)
		r.AdditionalBytes = add(r.AdditionalBytes, delta)
		r.KnownMinimumFreeBytes = addMinimum(r.KnownMinimumFreeBytes, delta)
		if reason != "" {
			r.Reasons = append(r.Reasons, reason)
		}
	}
	r.RequiredFreeBytes = r.AdditionalBytes
	for _, component := range []*int64{plan.Peak.DownloadBytes, plan.Peak.UploadBytes, plan.Peak.BackupBytes, plan.Peak.RestoreBytes, plan.Peak.HeadroomBytes} {
		r.RequiredFreeBytes = add(r.RequiredFreeBytes, nonnegative(component))
		r.KnownMinimumFreeBytes = addMinimum(r.KnownMinimumFreeBytes, nonnegative(component))
	}
	if r.FreeBytes == nil {
		r.Reasons = append(r.Reasons, "free_space_unknown")
	}
	if r.AdditionalBytes == nil || r.RequiredFreeBytes == nil {
		r.Reasons = append(r.Reasons, "physical_growth_or_peak_unknown_or_overflow")
	}
	if r.FreeBytes != nil && (r.KnownMinimumFreeBytes == nil || *r.KnownMinimumFreeBytes > *r.FreeBytes) {
		r.Status = Insufficient
		r.Reasons = append(r.Reasons, "storage_peak_exceeded")
	} else if r.FreeBytes != nil && r.RequiredFreeBytes != nil {
		r.Status = Sufficient
	}
	r.Reasons = uniqueReasons(r.Reasons)
	return r
}

func add(a, b *int64) *int64 {
	if a == nil || b == nil || *a < 0 || *b < 0 || *b > math.MaxInt64-*a {
		return nil
	}
	return value(*a + *b)
}

// Unknown nonnegative terms cannot reduce a minimum. Nil total here means the
// known minimum itself exceeded int64, which exceeds every representable limit.
func addMinimum(total, component *int64) *int64 {
	if component == nil {
		return total
	}
	return add(total, component)
}

func nonnegative(n *int64) *int64 {
	if n == nil || *n < 0 {
		return nil
	}
	return value(*n)
}

func value(n int64) *int64 { return &n }

func sameBytes(a, b Reference) bool {
	return a.Digest == b.Digest && a.Size != nil && b.Size != nil && *a.Size == *b.Size
}

func validKey(key string) bool {
	return key != "" && len(key) <= 4096 && !strings.ContainsAny(key, "\x00\r\n")
}

func validDigest(digest string) bool {
	if !strings.HasPrefix(digest, "sha256:") || len(digest) != 71 {
		return false
	}
	_, err := hex.DecodeString(digest[7:])
	return err == nil
}

func combine(a, b Status) Status {
	if a == Insufficient || b == Insufficient {
		return Insufficient
	}
	if a == Unknown || b == Unknown {
		return Unknown
	}
	return Sufficient
}

func uniqueReasons(reasons []string) []string {
	seen := make(map[string]bool)
	result := []string{}
	for _, reason := range reasons {
		if !seen[reason] {
			seen[reason] = true
			result = append(result, reason)
		}
	}
	sort.Strings(result)
	return result
}
