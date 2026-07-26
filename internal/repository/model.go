package repository

import "time"

type Format string

const (
	FormatRaw   Format = "raw"
	FormatOCI   Format = "oci"
	FormatMaven Format = "maven"
	// FormatConan is a managed authorization target for Conan Group members.
	// Conan remains read-through only; this format has no native artifact route.
	FormatConan Format = "conan"
)

type RepositoryState string

const (
	RepositoryActive   RepositoryState = "active"
	RepositoryDeleting RepositoryState = "deleting"
)

type HostedRepository struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Format    Format          `json:"format"`
	State     RepositoryState `json:"state"`
	Version   string          `json:"version"`
	CreatedAt time.Time       `json:"-"`
}

type HostedGroup struct {
	ID      string        `json:"id"`
	Name    string        `json:"name"`
	Format  Format        `json:"format"`
	Members []GroupMember `json:"members"`
	Version string        `json:"version"`
}

type GroupMember struct {
	RepositoryID string `json:"repositoryId"`
	Position     int    `json:"position"`
}

type RepositoryGrant struct {
	Principal string   `json:"principal"`
	Scopes    []string `json:"scopes"`
}

type RepositoryGrantSet struct {
	Version string
	Grants  []RepositoryGrant
}

type RepositoryRetentionPolicy struct {
	Version         string `json:"version"`
	KeepDays        int    `json:"keepDays"`
	MinimumVersions int    `json:"minimumVersions"`
}

type RawAsset struct {
	RepositoryID, Path, Digest, ObjectKey, ContentType string
	Size                                               int64
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
	PlanID, ObjectKey, Digest, State, LastError string
	Size, ByteOffset                            int64
	Attempts                                    int
	VerifiedAt, UpdatedAt                       time.Time
}

type MavenDeclaredObject struct {
	Name, Digest string
	Size         int64
}
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
	ID           string    `json:"id"`
	RepositoryID string    `json:"repositoryId"`
	Coordinate   string    `json:"coordinate"`
	Digest       string    `json:"digest"`
	State        string    `json:"state"`
	CreatedAt    time.Time `json:"createdAt"`
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

type ConanPackageRevision struct {
	RepositoryID, Reference, RecipeRevision, PackageID, Revision, Digest, State string
	CreatedAt                                                                   time.Time
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
	Limit      int
}
