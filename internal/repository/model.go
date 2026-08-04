package repository

import (
	"strings"
	"time"
)

type Format string

const (
	FormatRaw   Format = "raw"
	FormatOCI   Format = "oci"
	FormatMaven Format = "maven"
	// FormatConan is a managed authorization target with native Hosted, Proxy,
	// and Group protocol routes.
	FormatConan Format = "conan"
)

type RepositoryState string

const (
	RepositoryActive   RepositoryState = "active"
	RepositoryDeleting RepositoryState = "deleting"
)

type RepositoryType string

const (
	RepositoryTypeHosted RepositoryType = "hosted"
	RepositoryTypeProxy  RepositoryType = "proxy"
)

type HostedRepository struct {
	ID            string          `json:"id"`
	Name          string          `json:"name"`
	Format        Format          `json:"format"`
	Type          RepositoryType  `json:"type"`
	Endpoint      string          `json:"endpoint,omitempty"`
	AllowedHosts  []string        `json:"allowedHosts,omitempty"`
	AnonymousRead bool            `json:"anonymousRead"`
	State         RepositoryState `json:"state"`
	Version       string          `json:"version"`
	CreatedAt     time.Time       `json:"-"`
}

func normalizeHostedRepository(repo HostedRepository) HostedRepository {
	if repo.Type == "" {
		repo.Type = RepositoryTypeHosted
	}
	if repo.AllowedHosts == nil {
		repo.AllowedHosts = []string{}
	}
	return repo
}

// AnonymousAccessPolicy controls whether any unauthenticated protocol read may
// be admitted. Repository and Group anonymous settings remain required gates.
type AnonymousAccessPolicy struct {
	Enabled bool   `json:"enabled"`
	Version string `json:"version"`
}

// RepositoryCapacity is logical usage attributed to one Hosted repository.
// Shared content-addressed bytes are counted once for each visible repository
// reference so quotas remain meaningful after promotion.
type RepositoryCapacity struct {
	RepositoryID       string `json:"repositoryId"`
	Format             Format `json:"format"`
	UsedBytes          int64  `json:"usedBytes"`
	ObjectCount        int64  `json:"objectCount"`
	QuotaBytes         int64  `json:"quotaBytes"` // zero means no configured quota
	PrimaryBytes       int64  `json:"primaryBytes,omitempty"`
	SidecarBytes       int64  `json:"sidecarBytes,omitempty"`
	NegativeCount      int64  `json:"negativeCount,omitempty"`
	ExpiredObjectCount int64  `json:"expiredObjectCount,omitempty"`
	ReclaimableBytes   int64  `json:"reclaimableBytes,omitempty"`
}

type HostedGroup struct {
	ID            string        `json:"id"`
	Name          string        `json:"name"`
	Format        Format        `json:"format"`
	AnonymousRead bool          `json:"anonymousRead"`
	Members       []GroupMember `json:"members"`
	Version       string        `json:"version"`
}

type GroupMember struct {
	RepositoryID string `json:"repositoryId"`
	Position     int    `json:"position"`
}

type RepositoryGrant struct {
	Principal      string   `json:"principal"`
	Scopes         []string `json:"scopes"`
	ResourcePrefix string   `json:"resourcePrefix,omitempty"`
}

type RepositoryGrantSet struct {
	Version string
	Grants  []RepositoryGrant
}

type RepositoryRetentionPolicy struct {
	Version            string   `json:"version"`
	Enabled            bool     `json:"enabled"`
	KeepDays           int      `json:"keepDays"`
	SnapshotKeepDays   int      `json:"snapshotKeepDays"`
	MinimumVersions    int      `json:"minimumVersions"`
	MaximumVersions    int      `json:"maximumVersions"`
	CoordinatePatterns []string `json:"coordinatePatterns"`
	ProtectedPatterns  []string `json:"protectedPatterns"`
}

type RawAsset struct {
	RepositoryID, Path, Digest, ObjectKey, ContentType string
	Size                                               int64
	UpdatedAt                                          time.Time
}
type RawObject struct {
	RepositoryID, Digest, ObjectKey string
	Size                            int64
	CreatedAt, CollectedAt          time.Time
}
type RawUpload struct {
	ID, RepositoryID, Path, ObjectKey, State string
	Offset                                   int64
	ExpiresAt                                time.Time
}

type OCIUpload struct {
	ID, RepositoryID, Name, ObjectKey, State string
	Offset                                   int64
	ExpiresAt                                time.Time
	CollectedAt                              time.Time
}

type OCIBlob struct {
	Digest, ObjectKey string
	Size              int64
}
type OCIObjectIntent struct {
	RepositoryID, ObjectKey, Digest   string
	Size                              int64
	CreatedAt, ClaimedAt, CollectedAt time.Time
}

type OCIManifest struct {
	RepositoryID, Name, Digest, ObjectKey, MediaType string
	SubjectDigest, ArtifactType                      string
	Size                                             int64
	CreatedAt                                        time.Time
	Tags                                             []string
}

// ArtifactTombstone keeps a deleted artifact's identity until its byte objects
// are safely reclaimed by the lifecycle collector.
type ArtifactTombstone struct {
	RepositoryID string
	Format       Format
	Coordinate   string
	Digest       string
	TombstonedAt time.Time
}

type LifecycleJobKind string

const (
	LifecycleJobRetention   LifecycleJobKind = "retention"
	LifecycleJobPromotion   LifecycleJobKind = "promotion"
	LifecycleJobReplication LifecycleJobKind = "replication"
	LifecycleJobReclaim     LifecycleJobKind = "reclaim"
)

type LifecycleJobState string

const (
	LifecycleJobPending   LifecycleJobState = "pending"
	LifecycleJobRunning   LifecycleJobState = "running"
	LifecycleJobCompleted LifecycleJobState = "completed"
	LifecycleJobFailed    LifecycleJobState = "failed"
)

type LifecycleJob struct {
	ID             string
	RepositoryID   string
	Kind           LifecycleJobKind
	IdempotencyKey string
	Payload        []byte
	State          LifecycleJobState
	CreatedAt      time.Time
	StartedAt      time.Time
	CompletedAt    time.Time
	LastError      string
}

type ReplicationPlan struct {
	ID, SourceRepositoryID, TargetRepositoryID string
	Format                                     Format
	IdempotencyKey, State, LastError           string
	CreatedAt, StartedAt, CompletedAt          time.Time
}

type ReplicationCheckpoint struct {
	PlanID, SourceObjectKey, ObjectKey, Digest, State, LastError string
	Size, ByteOffset                                             int64
	Attempts                                                     int
	VerifiedAt, UpdatedAt                                        time.Time
}

type MavenDeclaredObject struct {
	Name, Digest string
	Size         int64
}

// User is a local account that authenticates with a username and password and
// carries a coarse role (reader/writer/admin). SecretHash is a bcrypt hash and
// is never returned by management responses.
type User struct {
	ID         string
	Name       string
	SecretHash string
	Role       string
	State      string
	CreatedAt  time.Time
	UpdatedAt  time.Time
	Version    string
}

const (
	UserActive   = "active"
	UserDisabled = "disabled"
)

type MavenPublishSession struct {
	ID, RepositoryID, Coordinate, Publisher, PomObject, State string
	Objects                                                   []MavenDeclaredObject
	ExpiresAt                                                 time.Time
}
type MavenAsset struct {
	RepositoryID, Path, ObjectKey, Digest string
	Size                                  int64
}
type MavenArtifact struct {
	ID           string `json:"id"`
	RepositoryID string `json:"repositoryId"`
	Coordinate   string `json:"coordinate"`
	Digest       string `json:"digest"`
	State        string `json:"state"`
	Publisher    string `json:"publisher,omitempty"`
	// BuildNumber is 0 for immutable releases and the 1-based publish sequence
	// for SNAPSHOT coordinates, which keep one row per timestamped build.
	BuildNumber int       `json:"buildNumber,omitempty"`
	CreatedAt   time.Time `json:"createdAt"`
}

// IsMavenSnapshotCoordinate reports whether the coordinate's version segment
// ends in -SNAPSHOT, making the coordinate eligible for multi-build publishes.
func IsMavenSnapshotCoordinate(coordinate string) bool {
	parts := strings.Split(coordinate, ":")
	return len(parts) >= 3 && strings.HasSuffix(parts[2], "-SNAPSHOT")
}

// MavenReplication is the immutable source snapshot and target-owned assets
// that are published after every replication checkpoint is verified.
type MavenReplication struct {
	ID, SourceRepositoryID, TargetRepositoryID, Coordinate, Digest string
	Assets                                                         []MavenReplicationAsset
}
type MavenReplicationAsset struct {
	Path, SourceObjectKey, ObjectKey, Digest string
	Size                                     int64
}

// OCIReplicationPublication atomically rechecks the source manifest and makes
// its copied manifest and referenced blobs visible in the target Repository.
type OCIReplicationPublication struct {
	SourceRepositoryID, TargetRepositoryID, SourceObjectKey string
	Manifest                                                OCIManifest
	BlobDigests                                             []string
}

// MavenPromotion snapshots a visible coordinate for immutable promotion into a
// second Maven Hosted repository.
type MavenPromotion struct {
	ID, SourceRepositoryID, TargetRepositoryID, Coordinate, Digest string
}
type MavenObjectIntent struct{ RepositoryID, ObjectKey, ClaimToken string }

type ConanObjectIntent struct {
	RepositoryID, ObjectKey, Digest   string
	Size                              int64
	CreatedAt, ClaimedAt, CollectedAt time.Time
}

type ConanAsset struct {
	RepositoryID, Reference, RecipeRevision, PackageID, PackageRevision string
	Path, ObjectKey, Digest                                             string
	Size                                                                int64
}

type ConanRecipeRevision struct {
	RepositoryID, Reference, Revision, Digest, State string
	CreatedAt                                        time.Time
}

// ConanReference is a visible recipe reference together with the publisher of
// its most recent committed publish session. Publisher is empty when no
// publish session was recorded (replicated, promoted, or pre-session data).
type ConanReference struct {
	Reference, Publisher string
}

type ConanPackageRevision struct {
	RepositoryID, Reference, RecipeRevision, PackageID, Revision, Digest, State string
	CreatedAt                                                                   time.Time
}

// ConanReplicationPublication is the complete target-owned recipe subtree.
// Publishing it is all-or-nothing so retries never expose a partial revision.
type ConanReplicationPublication struct {
	SourceRepositoryID string
	Recipe             ConanRecipeRevision
	Packages           []ConanPackageRevision
	SourceAssets       []ConanAsset
	TargetAssets       []ConanAsset
}

// ConanPromotion snapshots a visible recipe revision together with every
// visible package revision beneath it into another Hosted repository.
type ConanPromotion struct {
	SourceRepositoryID, TargetRepositoryID, Reference, Revision, Digest string
}

// ConanPublishSession keeps uploads unaddressable until a complete recipe or
// package revision is atomically promoted to visible metadata.
type ConanPublishSession struct {
	ID, RepositoryID, Publisher, Kind string
	Reference, RecipeRevision         string
	PackageID, PackageRevision        string
	State                             string
	Objects                           []MavenDeclaredObject
	ExpiresAt                         time.Time
}

type MemberType string

const (
	MemberHosted MemberType = "hosted"
	MemberProxy  MemberType = "proxy"
)

type Member struct {
	Name         string     `json:"name"`
	Type         MemberType `json:"type"`
	Endpoint     string     `json:"endpoint"`
	Position     int        `json:"position"`
	Anonymous    bool       `json:"anonymous"`
	AllowedHosts []string   `json:"allowedHosts,omitempty"`
	RepositoryID string     `json:"repositoryId,omitempty"`
}

type Group struct {
	Name            string    `json:"name"`
	Enabled         bool      `json:"enabled"`
	Anonymous       bool      `json:"anonymous"`
	CacheQuotaBytes int64     `json:"cacheQuotaBytes,omitempty"`
	Members         []Member  `json:"members"`
	CreatedAt       time.Time `json:"createdAt"`
}

type AuditOutcome string

const (
	AuditResolved          AuditOutcome = "resolved"
	AuditInternalPreferred AuditOutcome = "internal_preferred"
	AuditNotFound          AuditOutcome = "not_found"
	AuditGroupDisabled     AuditOutcome = "group_disabled"
	AuditStorageError      AuditOutcome = "storage_error"
	AuditUpstreamError     AuditOutcome = "upstream_error"
	AuditAccessDenied      AuditOutcome = "access_denied"
	AuditProxyDenied       AuditOutcome = "proxy_denied"
)

type AuditRecord struct {
	GroupName                                                                               string
	Repository                                                                              string
	MemberName                                                                              string
	Outcome                                                                                 AuditOutcome
	Actor                                                                                   string
	OccurredAt                                                                              time.Time
	Format, Resource, Representation, MemberType, UpstreamHost, Operation, CacheDisposition string
	AuthorizationSource, AuthorizationReason                                                string
	RequestID, TraceID                                                                      string
	Status                                                                                  int
	Bytes                                                                                   int64
}

type AuditQuery struct {
	GroupName  string
	Repository string
	Outcome    string
	Format     string
	Operation  string
	Actor      string
	Limit      int
}

// AuditRetentionPolicy governs global resolver audit cleanup. It is disabled by
// default so enabling deletion is always an explicit administrative action.
type AuditRetentionPolicy struct {
	Version  string `json:"version"`
	Enabled  bool   `json:"enabled"`
	KeepDays int    `json:"keepDays"`
}

type AuditCleanupJob struct {
	ID             string
	IdempotencyKey string
	PolicyVersion  string
	CutoffAt       time.Time
	BatchSize      int
	Deleted        int
	State          LifecycleJobState
	CreatedAt      time.Time
	StartedAt      time.Time
	CompletedAt    time.Time
	LastError      string
}
