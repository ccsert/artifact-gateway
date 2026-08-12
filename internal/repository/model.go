package repository

import (
	"errors"
	"strings"
	"time"
)

type Format string

const (
	FormatRaw   Format = "raw"
	FormatOCI   Format = "oci"
	FormatMaven Format = "maven"
	FormatNPM   Format = "npm"
	FormatPyPI  Format = "pypi"
	FormatGo    Format = "go"
	// FormatAPT is a protocol-only Debian repository proxy. APT publication
	// requires trusted Release metadata and signing, so Hosted is intentionally
	// not admitted until that workflow is implemented.
	FormatAPT Format = "apt"
	// FormatConan is a managed authorization target with native Hosted, Proxy,
	// and Group protocol routes.
	FormatConan Format = "conan"
)

type RepositoryState string

const (
	RepositoryActive   RepositoryState = "active"
	RepositoryDeleting RepositoryState = "deleting"
	RepositoryDeleted  RepositoryState = "deleted"
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
	EgressProxy   *EgressProxy    `json:"egressProxy,omitempty"`
	AnonymousRead bool            `json:"anonymousRead"`
	State         RepositoryState `json:"state"`
	Version       string          `json:"version"`
	CreatedAt     time.Time       `json:"-"`
}

// EgressProxyMode selects how a Proxy Repository reaches its upstream.
type EgressProxyMode string

const (
	EgressProxyModeDirect      EgressProxyMode = "direct"
	EgressProxyModeEnvironment EgressProxyMode = "environment"
	EgressProxyModeCustom      EgressProxyMode = "custom"
)

// EgressProxyProtocol is the network protocol of a custom egress proxy.
type EgressProxyProtocol string

const (
	EgressProxyProtocolHTTP   EgressProxyProtocol = "http"
	EgressProxyProtocolSOCKS5 EgressProxyProtocol = "socks5"
)

// EgressProxy is the per-Proxy-Repository egress network proxy configuration.
// Password holds AES-256-GCM ciphertext (base64), never plaintext; it is
// redacted from all management API responses.
type EgressProxy struct {
	Mode      EgressProxyMode     `json:"mode"`
	Protocol  EgressProxyProtocol `json:"protocol,omitempty"`
	Host      string              `json:"host,omitempty"`
	Port      int                 `json:"port,omitempty"`
	Username  string              `json:"username,omitempty"`
	Password  string              `json:"password,omitempty"`
	RemoteDNS bool                `json:"remoteDns,omitempty"`
	NoProxy   []string            `json:"noProxy,omitempty"`
	// CredentialsConfigured is a response-only marker computed at encode time;
	// it is never persisted.
	CredentialsConfigured bool `json:"credentialsConfigured,omitempty"`
}

// Validate enforces the egress proxy invariants documented in
// docs/proxy-egress-design.md.
func (p *EgressProxy) Validate() error {
	if p == nil {
		return nil
	}
	switch p.Mode {
	case EgressProxyModeDirect, EgressProxyModeEnvironment:
		if p.Protocol != "" || p.Host != "" || p.Port != 0 || p.Username != "" || p.Password != "" || p.RemoteDNS || len(p.NoProxy) > 0 {
			return errors.New("egress proxy custom fields require custom mode")
		}
		return nil
	case EgressProxyModeCustom:
		if p.Protocol != EgressProxyProtocolHTTP && p.Protocol != EgressProxyProtocolSOCKS5 {
			return errors.New("egress proxy protocol must be http or socks5")
		}
		if p.Host == "" || strings.ContainsAny(p.Host, "/@") || strings.Contains(p.Host, "://") {
			return errors.New("egress proxy host must be a bare hostname or IP")
		}
		if p.Port < 1 || p.Port > 65535 {
			return errors.New("egress proxy port must be between 1 and 65535")
		}
		if p.RemoteDNS && p.Protocol != EgressProxyProtocolSOCKS5 {
			return errors.New("remoteDns only applies to the socks5 protocol")
		}
		for _, entry := range p.NoProxy {
			if strings.TrimSpace(entry) == "" || strings.Contains(entry, "://") {
				return errors.New("egress proxy noProxy entries must be host suffixes or CIDR ranges")
			}
		}
		return nil
	default:
		return errors.New("egress proxy mode must be direct, environment, or custom")
	}
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

type RepositoryCapacityRecord struct {
	Repository HostedRepository
	Capacity   RepositoryCapacity
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

type RepositoryGrantRecord struct {
	RepositoryID   string
	RepositoryName string
	Format         Format
	Grant          RepositoryGrant
}

// AuthorizationTemplate is a reusable, repository-scoped set of grant rules.
// The rules are validated against the target repository format when applied.
type AuthorizationTemplate struct {
	ID          string
	Name        string
	Description string
	Grants      []RepositoryGrant
	Version     string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// AuthorizationRole is a reusable repository permission set. Grants and
// templates copy its scopes when edited so later role changes never mutate
// already-persisted authorization decisions.
type AuthorizationRole struct {
	ID          string
	Name        string
	Description string
	Scopes      []string
	Version     string
	CreatedAt   time.Time
	UpdatedAt   time.Time
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

// RepositorySecurityPolicy defines the admission rules applied when an
// immutable artifact is promoted into this repository. It intentionally does
// not affect ordinary reads or writes in the source repository.
type RepositorySecurityPolicy struct {
	Version                  string   `json:"version"`
	Enabled                  bool     `json:"enabled"`
	AutoScanOnPublish        bool     `json:"autoScanOnPublish"`
	RequireSignature         bool     `json:"requireSignature"`
	RequireVerifiedSignature bool     `json:"requireVerifiedSignature"`
	RequireSBOM              bool     `json:"requireSbom"`
	RequireProvenance        bool     `json:"requireProvenance"`
	RequireVulnerabilityScan bool     `json:"requireVulnerabilityScan"`
	MaxAllowedSeverity       string   `json:"maxAllowedSeverity"`
	FailOnScanError          bool     `json:"failOnScanError"`
	AllowedLicenses          []string `json:"allowedLicenses"`
}

// RepositoryQuarantineReadPolicy controls protocol-read enforcement for
// quarantined artifacts in one Hosted repository. It is independent from
// promotion admission and defaults disabled for compatibility.
type RepositoryQuarantineReadPolicy struct {
	Version string `json:"version"`
	Enabled bool   `json:"enabled"`
}

// SecurityPolicyEvaluation is the stable result returned by dry-run checks
// and used by promotion admission. Reasons are machine-readable codes.
type SecurityPolicyEvaluation struct {
	Allowed             bool     `json:"allowed"`
	Enforced            bool     `json:"enforced"`
	PolicyVersion       string   `json:"policyVersion"`
	IntelligencePresent bool     `json:"intelligencePresent"`
	Reasons             []string `json:"reasons"`
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

// NPMPackage is the registry packument identity projected from immutable
// versions and mutable distribution tags.
type NPMPackage struct {
	RepositoryID      string
	Name              string
	DistTags          map[string]string
	Versions          []NPMVersion
	SourceEndpoint    string
	UpstreamETag      string
	UpstreamModified  string
	MetadataExpiresAt time.Time
	NegativeExpiresAt time.Time
	Negative          bool
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// NPMVersion is one immutable package publication. Manifest retains the
// protocol-native version document; dist URLs and integrity fields are
// overwritten from trusted server metadata when the packument is served.
type NPMVersion struct {
	RepositoryID    string
	PackageName     string
	Version         string
	Digest          string
	Integrity       string
	Shasum          string
	TarballName     string
	UpstreamTarball string
	ObjectKey       string
	Size            int64
	Manifest        []byte
	Publisher       string
	State           string
	CachedAt        time.Time
	DeletedAt       time.Time
	CollectedAt     time.Time
	CreatedAt       time.Time
}

type NPMObject struct {
	RepositoryID string
	ObjectKey    string
	Digest       string
	Size         int64
	DeletedAt    time.Time
}

// PyPIFile is one immutable wheel or source distribution exposed through the
// PEP 503 Simple Repository API.
type PyPIFile struct {
	RepositoryID   string
	Project        string
	Version        string
	Filename       string
	FileType       string
	PythonVersion  string
	RequiresPython string
	Digest         string
	ObjectKey      string
	Size           int64
	Publisher      string
	SourceURL      string
	State          string
	CachedAt       time.Time
	DeletedAt      time.Time
	CollectedAt    time.Time
	CreatedAt      time.Time
}

type PyPIProjectSummary struct {
	RepositoryID string
	Project      string
	Latest       PyPIFile
	VersionCount int
	FileCount    int
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type PyPIObject struct {
	RepositoryID string
	ObjectKey    string
	Digest       string
	Size         int64
	DeletedAt    time.Time
}

// GoModuleVersion is one immutable module version known by a Go Proxy
// repository. Asset bytes are stored independently so list and @latest
// metadata remain useful before a client downloads the complete module.
type GoModuleVersion struct {
	RepositoryID string
	Module       string
	Version      string
	PublishedAt  time.Time
	Publisher    string
	CachedAt     time.Time
	CreatedAt    time.Time
}

// GoModuleAsset is one protocol representation for a module version. Kind is
// one of info, mod, or zip.
type GoModuleAsset struct {
	RepositoryID string
	Module       string
	Version      string
	Kind         string
	Digest       string
	ObjectKey    string
	Size         int64
	SourceURL    string
	CachedAt     time.Time
	CreatedAt    time.Time
}

// APTAsset is an immutable byte representation cached from a Debian
// repository. The path is the protocol identity (for example
// dists/bookworm/InRelease or pool/main/h/hello/hello_2.10_amd64.deb).
type APTAsset struct {
	RepositoryID     string
	Path             string
	Digest           string
	ObjectKey        string
	Size             int64
	ContentType      string
	SourceURL        string
	UpstreamETag     string
	UpstreamModified string
	CachedAt         time.Time
	CreatedAt        time.Time
}

// APTAssetMutable reports whether an upstream path may legitimately change in
// place. Content-addressed by-hash metadata and pool package objects are
// immutable even though they live below otherwise mutable repository trees.
func APTAssetMutable(path string) bool {
	return strings.HasPrefix(path, "dists/") && !strings.Contains(path, "/by-hash/")
}

type NPMPackageSummary struct {
	RepositoryID string
	Name         string
	Latest       NPMVersion
	VersionCount int
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type OCIUpload struct {
	ID, RepositoryID, Name, ObjectKey, State string
	Offset                                   int64
	ExpiresAt                                time.Time
	CollectedAt                              time.Time
}

// RuntimeNode is the durable inventory record for one Gateway process session.
// Instance IDs are deployment-owned labels; SessionID fences restarts and
// exposes accidental duplicate instance IDs without collapsing their records.
type RuntimeNode struct {
	InstanceID    string
	SessionID     string
	Roles         []string
	WorkerFormats []string
	WorkerKinds   []string
	StartedAt     time.Time
	LastSeenAt    time.Time
	StoppedAt     time.Time
}

func (n RuntimeNode) Validate() error {
	if n.InstanceID == "" || n.SessionID == "" || n.LastSeenAt.IsZero() || n.StartedAt.IsZero() {
		return ErrInvalidRuntimeNode
	}
	return nil
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

// ArtifactQuarantineState is repository-local governance state for one
// immutable artifact identity. It is deliberately independent from the
// visible/tombstoned lifecycle state.
type ArtifactQuarantineState string

const (
	ArtifactQuarantineStateQuarantined ArtifactQuarantineState = "quarantined"
	ArtifactQuarantineStateReleased    ArtifactQuarantineState = "released"
)

// ArtifactQuarantine records the latest explicit quarantine decision for one
// canonical repository, format, coordinate, and digest identity.
type ArtifactQuarantine struct {
	RepositoryID  string
	Format        Format
	Coordinate    string
	Digest        string
	State         ArtifactQuarantineState
	Reason        string
	UpdatedBy     string
	Version       string
	QuarantinedAt time.Time
	ReleasedAt    time.Time
	UpdatedAt     time.Time
}

// ArtifactIntelligence is format-neutral security and build metadata attached
// to one immutable artifact identity. Scanners and CI systems produce the
// evidence represented here; the Gateway stores and serves the summaries.
type ArtifactIntelligence struct {
	RepositoryID  string
	Format        Format
	Coordinate    string
	Digest        string
	Signatures    []ArtifactSignature
	SBOMs         []ArtifactSBOM
	Provenance    *ArtifactProvenance
	Licenses      []ArtifactLicense
	Vulnerability *ArtifactVulnerabilitySummary
	Version       string
	CreatedAt     time.Time
	UpdatedAt     time.Time
	UpdatedBy     string
}

// ArtifactIntelligenceSummary is the bounded, list-friendly projection of
// security metadata. Full evidence remains available from the detail endpoint.
type ArtifactIntelligenceSummary struct {
	SignatureCount      int    `json:"signatureCount"`
	SBOMCount           int    `json:"sbomCount"`
	LicenseCount        int    `json:"licenseCount"`
	VulnerabilityStatus string `json:"vulnerabilityStatus,omitempty"`
	Critical            int    `json:"critical,omitempty"`
	High                int    `json:"high,omitempty"`
	Medium              int    `json:"medium,omitempty"`
	Low                 int    `json:"low,omitempty"`
	Unknown             int    `json:"unknown,omitempty"`
}

type ArtifactSignature struct {
	KeyID      string    `json:"keyId"`
	Algorithm  string    `json:"algorithm"`
	Identity   string    `json:"identity"`
	Signature  string    `json:"signature"`
	Verified   bool      `json:"verified"`
	VerifiedAt time.Time `json:"verifiedAt,omitempty"`
}

type ArtifactSBOM struct {
	MediaType string `json:"mediaType"`
	Digest    string `json:"digest"`
	URL       string `json:"url,omitempty"`
	Size      int64  `json:"size,omitempty"`
}

type ArtifactProvenance struct {
	Builder          string    `json:"builder"`
	BuildType        string    `json:"buildType"`
	SourceRepository string    `json:"sourceRepository"`
	SourceCommit     string    `json:"sourceCommit"`
	BuildID          string    `json:"buildId"`
	StartedAt        time.Time `json:"startedAt,omitempty"`
	FinishedAt       time.Time `json:"finishedAt,omitempty"`
}

type ArtifactLicense struct {
	SPDXID string `json:"spdxId"`
	Name   string `json:"name"`
	Source string `json:"source,omitempty"`
}

type ArtifactVulnerabilitySummary struct {
	Scanner   string                         `json:"scanner"`
	ScannedAt time.Time                      `json:"scannedAt,omitempty"`
	Status    string                         `json:"status"`
	Critical  int                            `json:"critical"`
	High      int                            `json:"high"`
	Medium    int                            `json:"medium"`
	Low       int                            `json:"low"`
	Unknown   int                            `json:"unknown"`
	Findings  []ArtifactVulnerabilityFinding `json:"findings,omitempty"`
}

// ArtifactVulnerabilityFinding identifies one affected component reported by
// the scanner. Summary counts remain available for bounded list projections;
// findings carry the evidence required to investigate and remediate a result.
type ArtifactVulnerabilityFinding struct {
	ID           string                        `json:"id"`
	Source       string                        `json:"source,omitempty"`
	Severity     ArtifactVulnerabilitySeverity `json:"severity"`
	Component    string                        `json:"component"`
	Version      string                        `json:"version,omitempty"`
	FixedVersion string                        `json:"fixedVersion,omitempty"`
	Location     string                        `json:"location,omitempty"`
	Title        string                        `json:"title,omitempty"`
	Description  string                        `json:"description,omitempty"`
	URL          string                        `json:"url,omitempty"`
	CVSSScore    *float64                      `json:"cvssScore,omitempty"`
	CVSSVector   string                        `json:"cvssVector,omitempty"`
}

type LifecycleJobKind string

const (
	LifecycleJobRetention    LifecycleJobKind = "retention"
	LifecycleJobPromotion    LifecycleJobKind = "promotion"
	LifecycleJobReplication  LifecycleJobKind = "replication"
	LifecycleJobReclaim      LifecycleJobKind = "reclaim"
	LifecycleJobIntelligence LifecycleJobKind = "intelligence"
	LifecycleJobScan         LifecycleJobKind = "scan"
)

// ArtifactScanPayload identifies one immutable artifact for a scanner worker.
// Asset bytes are resolved from repository metadata at execution time so a
// queued request cannot smuggle arbitrary object-store keys into a scanner.
type ArtifactScanPayload struct {
	Format     Format `json:"format"`
	Coordinate string `json:"coordinate"`
	Digest     string `json:"digest"`
}

// ArtifactScanCandidate is one visible immutable artifact identity that can be
// reconciled against the durable lifecycle queue.
type ArtifactScanCandidate struct {
	Coordinate  string
	Digest      string
	PublishedAt time.Time
}

// ScheduledTaskKind is intentionally a closed set. Scheduled tasks may only
// dispatch operations implemented by the gateway; they never execute user
// supplied commands or SQL.
type ScheduledTaskKind string

const (
	ScheduledTaskRepositoryRetention ScheduledTaskKind = "repository-retention"
	ScheduledTaskAuditRetention      ScheduledTaskKind = "audit-retention"
)

type ScheduledTaskState string

const (
	ScheduledTaskSubmitted ScheduledTaskState = "submitted"
	ScheduledTaskFailed    ScheduledTaskState = "failed"
)

type ScheduledTask struct {
	ID              string
	Name            string
	Description     string
	Kind            ScheduledTaskKind
	RepositoryID    string
	IntervalSeconds int
	Enabled         bool
	NextRunAt       time.Time
	LastRunAt       time.Time
	LastRunID       string
	LastRunState    ScheduledTaskState
	LastError       string
	Version         string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type ScheduledTaskRun struct {
	ID          string
	TaskID      string
	Trigger     string
	State       ScheduledTaskState
	ScheduledAt time.Time
	CreatedAt   time.Time
	CompletedAt time.Time
	TargetKind  string
	TargetID    string
	LastError   string
}

type ScheduledTaskClaim struct {
	Task ScheduledTask
	Run  ScheduledTaskRun
}

type LifecycleJobState string

const (
	LifecycleJobPending   LifecycleJobState = "pending"
	LifecycleJobRunning   LifecycleJobState = "running"
	LifecycleJobRetrying  LifecycleJobState = "retrying"
	LifecycleJobCompleted LifecycleJobState = "completed"
	LifecycleJobFailed    LifecycleJobState = "failed"
	LifecycleJobCancelled LifecycleJobState = "cancelled"
)

const DefaultLifecycleJobMaxAttempts = 3

type LifecycleJob struct {
	ID              string
	RepositoryID    string
	Kind            LifecycleJobKind
	IdempotencyKey  string
	Payload         []byte
	State           LifecycleJobState
	CreatedAt       time.Time
	StartedAt       time.Time
	CompletedAt     time.Time
	NextAttemptAt   time.Time
	LeaseExpiresAt  time.Time
	LeaseToken      string
	Attempts        int
	MaxAttempts     int
	ProgressCurrent int
	ProgressTotal   int
	ProgressMessage string
	LastError       string
}

type RepositoryLifecycleJob struct {
	RepositoryName string
	Job            LifecycleJob
}

type ReplicationPlan struct {
	ID, SourceRepositoryID, TargetRepositoryID string
	Format                                     Format
	Coordinate, Digest, IdempotencyKey, State  string
	LastError                                  string
	CreatedAt, StartedAt, CompletedAt          time.Time
	NextAttemptAt, LeaseExpiresAt              time.Time
	LeaseToken                                 string
	Attempts, MaxAttempts                      int
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
	ID                  string
	Name                string
	DisplayName         string
	Email               string
	Description         string
	SecretHash          string
	Role                string
	State               string
	LastLoginAt         *time.Time
	PasswordChangedAt   *time.Time
	FailedLoginAttempts int
	LockedUntil         *time.Time
	MustChangePassword  bool
	SessionVersion      int64
	CreatedAt           time.Time
	UpdatedAt           time.Time
	Version             string
}

// UserSession is server-side metadata for a signed browser or local login
// token. ID is embedded in the signed token; token material is never stored.
type UserSession struct {
	ID        string
	UserID    string
	Kind      string
	IPAddress string
	UserAgent string
	CreatedAt time.Time
	ExpiresAt time.Time
	RevokedAt *time.Time
}

const (
	UserSessionLocal = "local_session"
	UserSessionOIDC  = "oidc"
)

// UserIdentity is an external credential linked to a local account. Issuer
// and Subject together form the provider-owned immutable identity key.
type UserIdentity struct {
	ID            string
	UserID        string
	Kind          string
	Issuer        string
	Subject       string
	Email         string
	DisplayName   string
	EmailVerified bool
	LastLoginAt   *time.Time
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

const UserIdentityOIDC = "oidc"

// OIDCIdentityProvision describes verified claims and the explicit
// provisioning policy observed during an OIDC sign-in.
type OIDCIdentityProvision struct {
	Issuer            string
	Subject           string
	Email             string
	DisplayName       string
	PreferredUsername string
	EmailVerified     bool
	Role              string
	Provision         bool
	MatchEmail        bool
	DefaultRole       string
	OccurredAt        time.Time
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

// MavenArtifactCursor preserves a SNAPSHOT build's position without encoding
// domain fields into an opaque string inside the storage boundary.
type MavenArtifactCursor struct {
	Coordinate  string
	BuildNumber int
}

// MavenArtifactAfterCoordinate resumes after every build of one coordinate.
func MavenArtifactAfterCoordinate(coordinate string) MavenArtifactCursor {
	return MavenArtifactCursor{Coordinate: coordinate, BuildNumber: int(^uint(0) >> 1)}
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
	Name         string       `json:"name"`
	Type         MemberType   `json:"type"`
	Endpoint     string       `json:"endpoint"`
	Position     int          `json:"position"`
	Anonymous    bool         `json:"anonymous"`
	AllowedHosts []string     `json:"allowedHosts,omitempty"`
	EgressProxy  *EgressProxy `json:"egressProxy,omitempty"`
	RepositoryID string       `json:"repositoryId,omitempty"`
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
	// ID is an internal stable tie-breaker for cursor pagination. It is never
	// serialized as part of the public audit representation.
	ID                                                                                      int64 `json:"-"`
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
	From       time.Time
	To         time.Time
	Before     AuditCursor
}

// AuditCursor identifies the last item in a descending audit page.
type AuditCursor struct {
	OccurredAt time.Time
	ID         int64
}

// AuditPage is the storage-level result for cursor-paginated audit queries.
// The API layer signs Next before returning it to callers.
type AuditPage struct {
	Items []AuditRecord
	Next  *AuditCursor
}

// AuditRetentionPolicy governs global resolver audit cleanup. It is disabled by
// default so enabling deletion is always an explicit administrative action.
type AuditRetentionPolicy struct {
	Version  string `json:"version"`
	Enabled  bool   `json:"enabled"`
	KeepDays int    `json:"keepDays"`
}

// OIDCSettings is the persisted singleton configuration for API bearer and
// browser OIDC authentication. ClientSecret contains ciphertext at the
// repository boundary and must never be serialized by an API response.
type OIDCSettings struct {
	Version             string    `json:"version"`
	Enabled             bool      `json:"enabled"`
	Issuer              string    `json:"issuer"`
	Audience            string    `json:"audience"`
	JWKSURL             string    `json:"jwksUrl,omitempty"`
	ClientID            string    `json:"clientId"`
	ClientSecret        string    `json:"-"`
	RedirectURL         string    `json:"redirectUrl"`
	Scopes              []string  `json:"scopes"`
	AdminSubjects       []string  `json:"adminSubjects"`
	ReaderRoles         []string  `json:"readerRoles"`
	WriterRoles         []string  `json:"writerRoles"`
	AdminRoles          []string  `json:"adminRoles"`
	ProvisioningMode    string    `json:"provisioningMode"`
	EmailLinkingEnabled bool      `json:"emailLinkingEnabled"`
	JITDefaultRole      string    `json:"jitDefaultRole"`
	UpdatedAt           time.Time `json:"updatedAt"`
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

// WebhookEventType is a stable consumer-facing operational event name.
type WebhookEventType string

const (
	WebhookEventArtifactQuarantined WebhookEventType = "artifact.quarantined"
	WebhookEventArtifactReleased    WebhookEventType = "artifact.released"
)

type WebhookSubscription struct {
	ID               string
	Name             string
	EndpointURL      string
	SecretCiphertext string `json:"-"`
	EventTypes       []WebhookEventType
	Enabled          bool
	Version          string
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

type WebhookEvent struct {
	ID         string
	Type       WebhookEventType
	OccurredAt time.Time
	Data       []byte
}

type WebhookDeliveryState string

const (
	WebhookDeliveryPending    WebhookDeliveryState = "pending"
	WebhookDeliveryDelivering WebhookDeliveryState = "delivering"
	WebhookDeliveryRetrying   WebhookDeliveryState = "retrying"
	WebhookDeliverySucceeded  WebhookDeliveryState = "succeeded"
	WebhookDeliveryDead       WebhookDeliveryState = "dead"
)

type WebhookDelivery struct {
	ID             string
	EventID        string
	EventType      WebhookEventType
	SubscriptionID string
	State          WebhookDeliveryState
	Attempts       int
	NextAttemptAt  time.Time
	LeaseOwner     string
	LeaseToken     string
	LeaseExpiresAt time.Time
	LastStatus     int
	LastError      string
	CreatedAt      time.Time
	UpdatedAt      time.Time
	DeliveredAt    time.Time
}

type WebhookDeliveryClaim struct {
	Delivery     WebhookDelivery
	Event        WebhookEvent
	Subscription WebhookSubscription
}

type WebhookDeliveryQuery struct {
	SubscriptionID string
	State          WebhookDeliveryState
	Limit          int
}

type ArtifactQuarantineWebhookData struct {
	RepositoryID string                  `json:"repositoryId"`
	Format       Format                  `json:"format"`
	Coordinate   string                  `json:"coordinate"`
	Digest       string                  `json:"digest"`
	State        ArtifactQuarantineState `json:"state"`
	Reason       string                  `json:"reason"`
	Actor        string                  `json:"actor"`
	Version      string                  `json:"version"`
}
