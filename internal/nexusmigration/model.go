// Package nexusmigration owns durable, append-only Nexus release/Raw migration.
package nexusmigration

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/ccsert/artifact-gateway/internal/capacityplan"
	"github.com/ccsert/artifact-gateway/internal/nexussource"
)

var (
	ErrNotFound            = errors.New("migration batch not found")
	ErrVersionConflict     = errors.New("migration batch version conflict")
	ErrIdempotencyConflict = errors.New("migration idempotency conflict")
	ErrLeaseLost           = errors.New("migration lease lost")
	ErrStopped             = errors.New("migration stop requested")
)

type Failure struct{ Code string }

func (e *Failure) Error() string { return e.Code }
func Failed(code string) error   { return &Failure{Code: code} }

const PolicyVersion = "release-raw-v1"
const MaxAttempts = 3

type Mapping struct {
	SourceRepository   string `json:"sourceRepository"`
	TargetRepositoryID string `json:"targetRepositoryId"`
}

// CapacityEvidence is an operator observation, not a measurement by Gateway.
type CapacityEvidence struct {
	TargetID      string    `json:"targetId"`
	PoolID        string    `json:"poolId"`
	ObservedAt    time.Time `json:"observedAt"`
	ValidUntil    time.Time `json:"validUntil"`
	FreeBytes     *int64    `json:"freeBytes"`
	DownloadBytes *int64    `json:"downloadBytes"`
	UploadBytes   *int64    `json:"uploadBytes"`
	BackupBytes   *int64    `json:"backupBytes"`
	RestoreBytes  *int64    `json:"restoreBytes"`
	HeadroomBytes *int64    `json:"headroomBytes"`
}
type StartInput struct {
	SourceID         string           `json:"sourceId"`
	SourceVersion    string           `json:"sourceVersion"`
	Mappings         []Mapping        `json:"mappings"`
	PolicyVersion    string           `json:"policyVersion"`
	CapacityEvidence CapacityEvidence `json:"capacityEvidence"`
}
type Counts struct {
	Pending       int64 `json:"pending"`
	Uploaded      int64 `json:"uploaded"`
	Verified      int64 `json:"verified"`
	Committed     int64 `json:"committed"`
	Skipped       int64 `json:"skipped"`
	Conflict      int64 `json:"conflict"`
	Failed        int64 `json:"failed"`
	Unsupported   int64 `json:"unsupported"`
	SourceMissing int64 `json:"sourceMissing"`
	SourceDeleted int64 `json:"sourceDeleted"`
}
type ScanFailure struct {
	Stage              string    `json:"stage"`
	SourceRepositories []string  `json:"sourceRepositories"`
	SourceRepository   string    `json:"sourceRepository"`
	SourcePath         string    `json:"sourcePath"`
	Identity           string    `json:"identity"`
	Code               string    `json:"code"`
	OccurredAt         time.Time `json:"occurredAt"`
}
type Batch struct {
	StartInput
	ID                      string       `json:"id"`
	Version                 string       `json:"version"`
	State                   string       `json:"state"`
	ManifestSHA256          string       `json:"manifestSha256"`
	ScanComplete            bool         `json:"scanComplete"`
	MetadataDiscovered      int64        `json:"metadataDiscovered"`
	SourceVerificationBytes int64        `json:"sourceVerificationBytes"`
	EntryCount              int64        `json:"entryCount"`
	TotalBytes              *int64       `json:"totalBytes"`
	TransferredBytes        int64        `json:"transferredBytes"`
	VerifiedBytes           int64        `json:"verifiedBytes"`
	VisibleIdentities       int64        `json:"visibleIdentities"`
	Counts                  Counts       `json:"counts"`
	LastError               string       `json:"lastError"`
	LastScanFailure         *ScanFailure `json:"lastScanFailure,omitempty"`
	CreatedAt               time.Time    `json:"createdAt"`
	UpdatedAt               time.Time    `json:"updatedAt"`
	Actor                   string       `json:"-"`
	LeaseToken              string       `json:"-"`
	LeaseEpoch              int64        `json:"-"`
	PauseRequested          bool         `json:"-"`
	CancelRequested         bool         `json:"-"`
}
type Asset struct {
	SourcePath string `json:"sourcePath"`
	TargetPath string `json:"targetPath"`
	Size       int64  `json:"size"`
	SHA256     string `json:"sha256"`
	Validator  string `json:"validator"`
	Uploaded   bool   `json:"uploaded"`
	Verified   bool   `json:"verified"`
	SHA1       string `json:"-"`
	MD5        string `json:"-"`
}
type Entry struct {
	ID                 string    `json:"id"`
	BatchID            string    `json:"batchId"`
	SourceRepository   string    `json:"sourceRepository"`
	TargetRepositoryID string    `json:"targetRepositoryId"`
	Format             string    `json:"format"`
	Identity           string    `json:"identity"`
	State              string    `json:"state"`
	Attempts           int       `json:"attempts"`
	Assets             []Asset   `json:"assets"`
	TransferredBytes   int64     `json:"transferredBytes"`
	VerifiedBytes      int64     `json:"verifiedBytes"`
	VisibleIdentities  int64     `json:"visibleIdentities"`
	LastError          string    `json:"lastError"`
	UpdatedAt          time.Time `json:"updatedAt"`
	SessionID          string    `json:"-"`
	Actor              string    `json:"-"`
}
type Capabilities struct {
	TargetID               string `json:"targetId"`
	PoolID                 string `json:"poolId"`
	ExecutionEnabled       bool   `json:"executionEnabled"`
	CapacityEvidenceSource string `json:"capacityEvidenceSource"`
	PolicyVersion          string `json:"policyVersion"`
	MaxAttempts            int    `json:"maxAttempts"`
}
type Source interface {
	Get(context.Context, string) (nexussource.Source, error)
	Discover(context.Context, string, nexussource.DiscoveryInput) (nexussource.Discovery, error)
	OpenAsset(context.Context, string, string, string, AssetRequest) (*Content, error)
}

// Source byte ports use Nexus-owned types through aliases, avoiding an import cycle.
type AssetRequest = nexussource.AssetRequest
type Content = nexussource.Content

type Readback string

const (
	Absent    Readback = "absent"
	Identical Readback = "identical"
	Conflict  Readback = "conflict"
)

// Publication is implemented at the native format boundary. Every Readback and
// Verify hashes complete bytes; Publish only changes native metadata references.
type Publication interface {
	ValidateTargets(context.Context, []Mapping) error
	Capacity(context.Context, Batch, []Entry) (capacityplan.Report, error)
	Lock(context.Context, Entry) (context.Context, func(), error)
	Readback(context.Context, Entry) (Readback, error)
	Begin(context.Context, Entry) (string, error)
	Stage(context.Context, Entry, Asset, io.ReadSeeker) error
	Verify(context.Context, Entry) error
	Publish(context.Context, Entry) error
}

// Store is dedicated to migration. It never extends repository.GatewayStore.
type Store interface {
	ReplayStart(context.Context, string, string, string) (Batch, bool, error)
	ReplayControl(context.Context, string, string, string, string, string) (Batch, bool, error)
	Create(context.Context, Batch, string, string) (Batch, error)
	Get(context.Context, string) (Batch, error)
	List(context.Context, int, string) ([]Batch, string, error)
	Entries(context.Context, string, int, string) ([]Entry, string, error)
	PreviousEntries(context.Context, Batch) ([]Entry, error)
	Control(context.Context, string, string, string, string, string, string, *CapacityEvidence) (Batch, error)
	Claim(context.Context, []string) (Batch, error)
	Renew(context.Context, string, string) error
	RecordScanProgress(context.Context, string, string, int64, int64) error
	RecordScanFailure(context.Context, string, string, ScanFailure) error
	Seal(context.Context, Batch, []Entry, string, int64, int64) error
	BeginEntry(context.Context, Entry, string) error
	Checkpoint(context.Context, Entry, string) error
	AddTransferred(context.Context, string, string, string, int64) error
	PublicationContext(context.Context, string, string) context.Context
	Finish(context.Context, string, string, string, string) error
}
