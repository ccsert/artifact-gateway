package format

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/ccsert/artifact-gateway/internal/scanning"
)

// These are independently queried capabilities, not a monolithic interface.
// They contain domain records only; HTTP DTOs stay at the management boundary.
type Profile interface {
	Profile() repository.FormatProfile
}
type Scanner interface {
	ResolveArtifactScan(context.Context, string, repository.ArtifactScanPayload) (scanning.Artifact, error)
}

type RetentionCandidate struct {
	Coordinate, Digest, CursorID, VersionType string
	CreatedAt                                 time.Time
	Reasons                                   []string
	AgeDays                                   int
}
type Retention interface {
	PlanRetention(context.Context, string, repository.RepositoryRetentionPolicy, time.Time) ([]RetentionCandidate, error)
	Tombstone(context.Context, string, string) error
}
type Governance interface {
	ValidCoordinate(string) bool
	Tombstone(context.Context, string, string) error
	Restore(context.Context, string, string) error
}
type Promotion interface {
	EnqueuePromotion(context.Context, string, string, string, string, string) (repository.LifecycleJob, bool, error)
}
type Replication interface {
	ReplicationCheckpoints(context.Context, string, string, string) ([]repository.ReplicationCheckpoint, error)
}

type ArtifactSummary struct {
	Coordinate, Digest, ContentType string
	Size                            int64
	CreatedAt                       time.Time
}
type Browse interface {
	ValidBrowseParent(repository.ArtifactBrowseParent) bool
	BrowseName(string) string
	ValidPrefix(string) bool
	ValidKeyword(string, repository.ArtifactSearchMatch) bool
	SearchQuery(repository.ArtifactSearchQuery) repository.ArtifactSearchQuery
	Search(context.Context, string, repository.ArtifactSearchQuery, int, string) ([]ArtifactSummary, error)
}
type Usage interface{ UsageKey(string) string }
type CacheMaintenance interface{ CollectCache(context.Context) error }
type Background interface {
	StartScheduler(context.Context, time.Duration)
	StartReclaimWorker(context.Context, time.Duration)
	StartPromotionWorker(context.Context, time.Duration)
	StartReplicationWorker(context.Context, time.Duration)
}

// NativeAsset is a verified immutable publication unit. Migration-specific
// manifests, lease state and error envelopes stay in their owning domain.
type NativeAsset struct {
	RepositoryID, Path, ObjectKey, Digest string
	Size                                  int64
}
type ImmutablePublication interface {
	ValidatePublication(string, []NativeAsset) error
	LockPublication(context.Context, []NativeAsset) (context.Context, func(), error)
	StageVerifiedPublication(context.Context, NativeAsset, io.Reader) error
	PublishPublication(context.Context, NativeAsset) error
	ReadPublication(context.Context, string, string) (NativeAsset, error)
}

var ErrPublicationIdentity = errors.New("invalid native publication identity")
var ErrPublicationPath = errors.New("unsupported native publication path")
var ErrPublicationStage = errors.New("native publication staging failed")
var ErrPublicationUpload = errors.New("native publication upload failed")
