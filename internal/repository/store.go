package repository

import (
	"context"
	"errors"
	"time"
)

var (
	ErrNotFound                      = errors.New("group not found")
	ErrDisabled                      = errors.New("group is disabled")
	ErrNameExists                    = errors.New("group name already exists")
	ErrIdempotencyConflict           = errors.New("idempotency key conflicts with request")
	ErrVersionConflict               = errors.New("resource version conflicts with current state")
	ErrLastActiveAdmin               = errors.New("last active administrator cannot be removed")
	ErrQuotaExceeded                 = errors.New("repository capacity quota exceeded")
	ErrUpstreamChanged               = errors.New("upstream immutable artifact metadata changed")
	ErrInvalidRuntimeNode            = errors.New("runtime node identity is invalid")
	ErrTemplateNameExists            = errors.New("authorization template name already exists")
	ErrAuthorizationRoleNameExists   = errors.New("authorization role name already exists")
	ErrInvalidArtifactIntelligence   = errors.New("artifact intelligence is invalid")
	ErrInvalidArtifactQuarantine     = errors.New("artifact quarantine is invalid")
	ErrArtifactIntelligenceConflict  = errors.New("target artifact intelligence conflicts with source")
	ErrArtifactIntelligenceDeferred  = errors.New("artifact intelligence copy was deferred")
	ErrIdentityExists                = errors.New("user identity already exists")
	ErrIdentityAmbiguous             = errors.New("user identity matches multiple accounts")
	ErrInvalidUserSession            = errors.New("user session is invalid")
	ErrWebhookSubscriptionNameExists = errors.New("webhook subscription name already exists")
	ErrInvalidWebhookDeliveryState   = errors.New("webhook delivery state is invalid")
	ErrAPTPackageConflict            = errors.New("APT package identity already has different immutable bytes")
)

type HostedRepositoryStore interface {
	CreateHostedRepository(context.Context, HostedRepository) (HostedRepository, error)
	CreateHostedRepositoryIdempotently(context.Context, HostedRepository, string, string, string) (HostedRepository, bool, error)
	ListHostedRepositories(context.Context, int, string) ([]HostedRepository, string, error)
	GetHostedRepository(context.Context, string) (HostedRepository, error)
	GetHostedRepositoryByName(context.Context, string) (HostedRepository, error)
	DisableHostedRepository(context.Context, string) (HostedRepository, error)
	FinalizeHostedRepositoryDeletion(context.Context, string) (HostedRepository, error)
	UpdateHostedRepository(context.Context, HostedRepository, string) (HostedRepository, error)
}

type AnonymousAccessPolicyStore interface {
	GetAnonymousAccessPolicy(context.Context) (AnonymousAccessPolicy, error)
	ReplaceAnonymousAccessPolicy(context.Context, AnonymousAccessPolicy, string) (AnonymousAccessPolicy, error)
}

type OIDCSettingsStore interface {
	GetOIDCSettings(context.Context) (OIDCSettings, error)
	ReplaceOIDCSettings(context.Context, OIDCSettings, string) (OIDCSettings, error)
}

type WebhookStore interface {
	CreateWebhookSubscription(context.Context, WebhookSubscription) (WebhookSubscription, error)
	ListWebhookSubscriptions(context.Context) ([]WebhookSubscription, error)
	GetWebhookSubscription(context.Context, string) (WebhookSubscription, error)
	UpdateWebhookSubscription(context.Context, WebhookSubscription, string) (WebhookSubscription, error)
	EnqueueWebhookEvent(context.Context, WebhookEvent) (WebhookEvent, error)
	ListWebhookDeliveries(context.Context, WebhookDeliveryQuery) ([]WebhookDelivery, error)
	GetWebhookDelivery(context.Context, string) (WebhookDelivery, error)
	ClaimWebhookDeliveries(context.Context, string, time.Time, time.Duration, int) ([]WebhookDeliveryClaim, error)
	CompleteWebhookDelivery(context.Context, string, string, int, time.Time) error
	FailWebhookDelivery(context.Context, string, string, time.Time, time.Time, int, string, bool) error
	ReplayWebhookDelivery(context.Context, string, time.Time) (WebhookDelivery, error)
}

// UserIdentityStore links provider credentials to a local account. The
// provider subject is the stable external identity; email and display name
// are informational claims refreshed at successful sign-in.
type UserIdentityStore interface {
	ListUserIdentities(context.Context, string) ([]UserIdentity, error)
	CreateUserIdentity(context.Context, UserIdentity) (UserIdentity, error)
	DeleteUserIdentity(context.Context, string, string) error
	GetUserByOIDCIdentity(context.Context, string, string) (User, UserIdentity, error)
	ResolveOIDCIdentity(context.Context, OIDCIdentityProvision) (User, UserIdentity, bool, error)
}

// UserSessionStore persists only signed-token identifiers and bounded client
// metadata. Implementations must never persist the bearer token itself.
type UserSessionStore interface {
	CreateUserSession(context.Context, UserSession) (UserSession, error)
	GetUserSession(context.Context, string, string) (UserSession, error)
	ListUserSessions(context.Context, string, bool) ([]UserSession, error)
	RevokeUserSession(context.Context, string, string) (UserSession, error)
	RevokeAllUserSessionRecords(context.Context, string, time.Time) error
	PruneExpiredUserSessions(context.Context, time.Time, int) (int, error)
}

type HostedGroupStore interface {
	CreateHostedGroupIdempotently(context.Context, HostedGroup, string, string, string) (HostedGroup, bool, error)
	ListHostedGroups(context.Context, int, string) ([]HostedGroup, string, error)
	GetHostedGroup(context.Context, string) (HostedGroup, error)
	ReplaceHostedGroup(context.Context, HostedGroup, string) (HostedGroup, error)
	ReplaceHostedGroupMembers(context.Context, string, []GroupMember, string) (HostedGroup, error)
	DeleteHostedGroup(context.Context, string) error
}

type RepositoryGrantStore interface {
	GetRepositoryGrants(context.Context, string) (RepositoryGrantSet, error)
	ReplaceRepositoryGrants(context.Context, string, []RepositoryGrant, string) (RepositoryGrantSet, error)
}

type RepositoryGrantRecordStore interface {
	ListRepositoryGrantRecords(context.Context) ([]RepositoryGrantRecord, error)
}

type AuthorizationTemplateStore interface {
	CreateAuthorizationTemplate(context.Context, AuthorizationTemplate) (AuthorizationTemplate, error)
	ListAuthorizationTemplates(context.Context) ([]AuthorizationTemplate, error)
	GetAuthorizationTemplate(context.Context, string) (AuthorizationTemplate, error)
	UpdateAuthorizationTemplate(context.Context, AuthorizationTemplate, string) (AuthorizationTemplate, error)
	DeleteAuthorizationTemplate(context.Context, string) error
	ApplyAuthorizationTemplate(context.Context, string, string, string) (RepositoryGrantSet, error)
}

type AuthorizationRoleStore interface {
	CreateAuthorizationRole(context.Context, AuthorizationRole) (AuthorizationRole, error)
	ListAuthorizationRoles(context.Context) ([]AuthorizationRole, error)
	GetAuthorizationRole(context.Context, string) (AuthorizationRole, error)
	UpdateAuthorizationRole(context.Context, AuthorizationRole, string) (AuthorizationRole, error)
	DeleteAuthorizationRole(context.Context, string) error
}

type RepositoryRetentionPolicyStore interface {
	GetRepositoryRetentionPolicy(context.Context, string) (RepositoryRetentionPolicy, error)
	ReplaceRepositoryRetentionPolicy(context.Context, string, RepositoryRetentionPolicy, string) (RepositoryRetentionPolicy, error)
}

type RepositorySecurityPolicyStore interface {
	GetRepositorySecurityPolicy(context.Context, string) (RepositorySecurityPolicy, error)
	ReplaceRepositorySecurityPolicy(context.Context, string, RepositorySecurityPolicy, string) (RepositorySecurityPolicy, error)
}

type RepositoryQuarantineReadPolicyStore interface {
	GetRepositoryQuarantineReadPolicy(context.Context, string) (RepositoryQuarantineReadPolicy, error)
	ReplaceRepositoryQuarantineReadPolicy(context.Context, string, RepositoryQuarantineReadPolicy, string) (RepositoryQuarantineReadPolicy, error)
}

type RepositoryCapacityStore interface {
	GetRepositoryCapacity(context.Context, string) (RepositoryCapacity, error)
	ReplaceRepositoryCapacityQuota(context.Context, string, int64) (RepositoryCapacity, error)
}

type RepositoryCapacityRecordStore interface {
	ListRepositoryCapacityRecords(context.Context) ([]RepositoryCapacityRecord, error)
}

// BackgroundOperationMetrics accepts only bounded operation dimensions. It is
// deliberately independent of repository IDs and artifact coordinates.
type BackgroundOperationMetrics interface {
	RecordBackgroundOperation(string, Format, string)
	AddBackgroundOperationInFlight(string, Format, int64)
}

type BackgroundOperationQueueStore interface {
	BackgroundOperationQueueStats(context.Context) ([]BackgroundOperationQueueStat, error)
}

type ArtifactTombstoneStore interface {
	GetArtifactTombstone(context.Context, string, Format, string) (ArtifactTombstone, error)
	ListArtifactTombstones(context.Context, string, Format, string, int, string) ([]ArtifactTombstone, error)
}

type ArtifactIntelligenceStore interface {
	GetArtifactIntelligence(context.Context, string, Format, string, string) (ArtifactIntelligence, error)
	ReplaceArtifactIntelligence(context.Context, ArtifactIntelligence, string) (ArtifactIntelligence, error)
}

type ArtifactQuarantineStore interface {
	GetArtifactQuarantine(context.Context, string, Format, string, string) (ArtifactQuarantine, error)
	ReplaceArtifactQuarantine(context.Context, ArtifactQuarantine, string) (ArtifactQuarantine, error)
}

type LifecycleJobStore interface {
	EnqueueLifecycleJob(context.Context, LifecycleJob) (LifecycleJob, bool, error)
	ListLifecycleJobs(context.Context, string, int) ([]LifecycleJob, error)
	GetLifecycleJob(context.Context, string, string) (LifecycleJob, error)
	GetLatestArtifactScanJob(context.Context, string, Format, string, string) (LifecycleJob, error)
	ClaimLifecycleJobs(context.Context, int) ([]LifecycleJob, error)
	ClaimLifecycleJobsByKind(context.Context, LifecycleJobKind, int) ([]LifecycleJob, error)
	ClaimLifecycleJobsByKindAndFormat(context.Context, LifecycleJobKind, Format, int) ([]LifecycleJob, error)
	RecoverExpiredLifecycleJobs(context.Context, time.Time) (int, error)
	RunLifecycleJobNow(context.Context, string, string) (LifecycleJob, error)
	RetryLifecycleJob(context.Context, string, string) (LifecycleJob, error)
	RequeueFailedLifecycleJobs(context.Context, string, LifecycleJobKind, int) ([]LifecycleJob, error)
	CancelLifecycleJob(context.Context, string, string) (LifecycleJob, error)
	UpdateLifecycleJobProgress(context.Context, string, string, int, int, string) error
	RenewLifecycleJobLease(context.Context, string, string) error
	CompleteLifecycleJob(context.Context, string, string) error
	FailLifecycleJob(context.Context, string, string, string) error
}

// ArtifactScanIdentityLockStore serializes enqueue/reconcile decisions for one
// immutable artifact identity across concurrent Gateway nodes.
type ArtifactScanIdentityLockStore interface {
	LockArtifactScanIdentity(context.Context, string, Format, string, string) (func(), error)
}

type ArtifactDistributionLockIdentity struct {
	RepositoryID string
	Format       Format
	Coordinate   string
	Digest       string
}

// ArtifactDistributionIdentityLockStore acquires several immutable-identity
// locks on one backend session. PostgreSQL implementations use this deeper
// interface so a multi-file admission occupies one dedicated lock connection
// instead of one primary-pool connection per digest.
type ArtifactDistributionIdentityLockStore interface {
	LockArtifactDistributionIdentities(context.Context, []ArtifactDistributionLockIdentity) (func(), error)
}

// ArtifactObjectKeysLockStore acquires all format object locks on one backend
// session. PostgreSQL uses it for multi-file publication so a single worker
// cannot exhaust the primary connection pool while coordinating many objects.
type ArtifactObjectKeysLockStore interface {
	LockArtifactObjectKeys(context.Context, Format, []string) (context.Context, func(), error)
}

type ArtifactScanCandidateStore interface {
	ListArtifactScanCandidates(context.Context, string, Format, int) ([]ArtifactScanCandidate, error)
}

type ArtifactIdentityStore interface {
	ListArtifactIdentities(context.Context, string, Format, ArtifactIdentityPurpose, string, int) ([]ArtifactIdentity, error)
}

type RepositoryLifecycleJobStore interface {
	ListAllLifecycleJobs(context.Context, int) ([]RepositoryLifecycleJob, error)
}

type ScheduledTaskStore interface {
	CreateScheduledTask(context.Context, ScheduledTask) (ScheduledTask, error)
	ListScheduledTasks(context.Context) ([]ScheduledTask, error)
	GetScheduledTask(context.Context, string) (ScheduledTask, error)
	UpdateScheduledTask(context.Context, ScheduledTask, string) (ScheduledTask, error)
	DeleteScheduledTask(context.Context, string) error
	ClaimDueScheduledTasks(context.Context, time.Time, int) ([]ScheduledTaskClaim, error)
	CreateScheduledTaskRun(context.Context, string, string, time.Time) (ScheduledTaskRun, error)
	ListScheduledTaskRuns(context.Context, string, int) ([]ScheduledTaskRun, error)
	UpdateScheduledTaskRun(context.Context, ScheduledTaskRun) error
}

type ReplicationStore interface {
	CreateReplicationPlan(context.Context, ReplicationPlan, []ReplicationCheckpoint) (ReplicationPlan, bool, error)
	ClaimReplicationPlans(context.Context, int) ([]ReplicationPlan, error)
	ClaimReplicationPlansByFormat(context.Context, Format, int) ([]ReplicationPlan, error)
	RecoverExpiredReplicationPlans(context.Context, time.Time) (int, error)
	ListReplicationPlans(context.Context, string, int) ([]ReplicationPlan, error)
	GetReplicationPlan(context.Context, string, string) (ReplicationPlan, error)
	ListReplicationCheckpoints(context.Context, string) ([]ReplicationCheckpoint, error)
	UpdateReplicationCheckpointWithLease(context.Context, ReplicationCheckpoint, string) error
	CompleteReplicationPlanWithLease(context.Context, string, string) error
	FailReplicationPlanWithLease(context.Context, string, string, string) error
	ParkReplicationPlanWithLease(context.Context, string, string, string) error
	CancelReplicationPlan(context.Context, string, string) error
}

type NativeMavenStore interface {
	CreateMavenPublishSession(context.Context, MavenPublishSession) (MavenPublishSession, error)
	FindOpenMavenPublishSession(context.Context, string, string, string) (MavenPublishSession, error)
	FindMavenPublishSession(context.Context, string, string, string) (MavenPublishSession, error)
	FindAnyMavenPublishSession(context.Context, string, string) (MavenPublishSession, error)
	AppendMavenPublishObject(context.Context, string, MavenDeclaredObject) error
	SetMavenPublishPom(context.Context, string, string) error
	CreateMavenPublishSessionIdempotently(context.Context, MavenPublishSession, string, string, string, string) (MavenPublishSession, bool, error)
	GetMavenPublishSession(context.Context, string) (MavenPublishSession, error)
	MarkMavenPublishObject(context.Context, string, string, string) error
	CommitMavenPublishSession(context.Context, string, []MavenAsset) (MavenArtifact, error)
	CommitMavenPublishSessionIdempotently(context.Context, string, string, string, []MavenAsset) (MavenArtifact, bool, error)
	GetMavenAsset(context.Context, string, string) (MavenAsset, error)
	ListMavenAssets(context.Context, string, string) ([]MavenAsset, error)
	ListMavenArtifacts(context.Context, string) ([]MavenArtifact, error)
	SearchMavenArtifacts(context.Context, string, string, int, MavenArtifactCursor) ([]MavenArtifact, error)
	GetMavenArtifact(context.Context, string, string) (MavenArtifact, error)
	GetMavenArtifactByCoordinate(context.Context, string, string) (MavenArtifact, error)
	TombstoneMavenArtifact(context.Context, string, string) (MavenArtifact, error)
	RestoreMavenArtifact(context.Context, string, string) (MavenArtifact, error)
	PromoteMavenArtifact(context.Context, MavenPromotion) (MavenArtifact, error)
	PublishReplicatedMavenArtifact(context.Context, MavenReplication) (MavenArtifact, error)
	ClaimExpiredMavenObjectIntents(context.Context, time.Time, int) ([]MavenObjectIntent, error)
	MavenObjectIntentClaimIsActive(context.Context, string, string) (bool, error)
	MavenObjectIntentHasReference(context.Context, string) (bool, error)
	DeleteClaimedMavenObjectIntent(context.Context, string, string) error
	ReleaseClaimedMavenObjectIntent(context.Context, string, string) error
}

// NativeOCIStore owns the registry metadata. Blob bytes are deliberately not
// represented here: they live in the object store and are only made visible

type NativeOCIStore interface {
	CreateOCIUpload(context.Context, OCIUpload) (OCIUpload, error)
	LockOCIUpload(context.Context, string) (func(), error)
	LockOCIObject(context.Context, string) (func(), error)
	GetOCIUpload(context.Context, string) (OCIUpload, error)
	UpdateOCIUpload(context.Context, string, int64) (OCIUpload, error)
	CancelOCIUpload(context.Context, string) (OCIUpload, error)
	StageOCIObjectIntent(context.Context, OCIObjectIntent) error
	CompleteOCIUpload(context.Context, string, OCIBlob) (OCIBlob, error)
	ExpireOCIUploads(context.Context, time.Time, int) ([]OCIUpload, error)
	ListUncollectedOCIUploads(context.Context, int) ([]OCIUpload, error)
	MarkOCIUploadCollected(context.Context, string) error
	ListUnclaimedOCIObjectIntents(context.Context, time.Time, int) ([]OCIObjectIntent, error)
	OCIObjectIntentIsUnclaimed(context.Context, string) (bool, error)
	MarkOCIObjectIntentCollected(context.Context, string) error
	MountOCIBlob(context.Context, string, string) (OCIBlob, error)
	MountOCIBlobFrom(context.Context, string, string, string) (OCIBlob, error)
	GetOCIBlob(context.Context, string, string) (OCIBlob, error)
	PutOCIManifest(context.Context, OCIManifest, string) (OCIManifest, error)
	PublishReplicatedOCIManifest(context.Context, OCIReplicationPublication) (OCIManifest, error)
	GetOCIManifest(context.Context, string, string, string) (OCIManifest, error)
	ListOCIReferrers(context.Context, string, string, string, int, string) ([]OCIManifest, error)
	ListOCIManifests(context.Context, string, string, int, string) ([]OCIManifest, error)
	ListOCIManifestNames(context.Context, string, int, string) ([]string, error)
	SearchOCIManifestNames(context.Context, string, string, int, string) ([]string, error)
	ListOCITags(context.Context, string, string, int, string) ([]string, error)
	DeleteOCITag(context.Context, string, string, string) error
	DeleteOCIManifest(context.Context, string, string, string) error
	RestoreOCIManifest(context.Context, string, string, string) (OCIManifest, error)
}

type NativeRawStore interface {
	CreateRawUpload(context.Context, RawUpload) (RawUpload, error)
	LockRawUpload(context.Context, string) (func(), error)
	GetRawUpload(context.Context, string) (RawUpload, error)
	UpdateRawUpload(context.Context, string, int64) (RawUpload, error)
	CancelRawUpload(context.Context, string) (RawUpload, error)
	CompleteRawUpload(context.Context, string, RawAsset) (RawAsset, error)
	LockRawObject(context.Context, string) (func(), error)
	StageRawObject(context.Context, RawObject) error
	PutRawAsset(context.Context, RawAsset) (RawAsset, error)
	GetRawAsset(context.Context, string, string) (RawAsset, error)
	ListRawAssets(context.Context, string, string, int, string) ([]RawAsset, error)
	DeleteRawAsset(context.Context, string, string) error
	RestoreRawAsset(context.Context, string, string) (RawAsset, error)
	ListUnreferencedRawObjects(context.Context, time.Time, int) ([]RawObject, error)
	RawObjectIsUnreferenced(context.Context, string) (bool, error)
	MarkRawObjectCollected(context.Context, string) error
}

type NativeNPMStore interface {
	PublishNPMVersion(context.Context, NPMVersion, map[string]string) (NPMVersion, error)
	SyncNPMProxyPackage(context.Context, NPMPackage) (NPMPackage, error)
	StoreNPMProxyNegative(context.Context, NPMPackage) error
	CacheNPMProxyTarball(context.Context, NPMVersion) (NPMVersion, error)
	LockNPMProxy(context.Context, string) (func(), error)
	LockNPMObject(context.Context, string) (func(), error)
	GetNPMPackage(context.Context, string, string) (NPMPackage, error)
	ListNPMVersions(context.Context, string, string) ([]NPMVersion, error)
	GetNPMVersion(context.Context, string, string, string) (NPMVersion, error)
	TombstoneNPMVersion(context.Context, string, string, string) (NPMVersion, error)
	RestoreNPMVersion(context.Context, string, string, string) (NPMVersion, error)
	ListReclaimableNPMObjects(context.Context, time.Time, int) ([]NPMObject, error)
	NPMObjectHasVisibleReference(context.Context, string) (bool, error)
	MarkNPMObjectCollected(context.Context, string) error
	GetNPMVersionByTarball(context.Context, string, string, string) (NPMVersion, error)
	SearchNPMPackages(context.Context, string, string, int, string) ([]NPMPackageSummary, error)
}

type NativePyPIStore interface {
	PublishPyPIFile(context.Context, PyPIFile) (PyPIFile, error)
	PublishPyPIVersion(context.Context, []PyPIFile) ([]PyPIFile, error)
	SyncPyPIProxyFiles(context.Context, string, string, []PyPIFile) error
	CachePyPIProxyFile(context.Context, PyPIFile) (PyPIFile, error)
	LockPyPIObject(context.Context, string) (func(), error)
	GetPyPIFile(context.Context, string, string) (PyPIFile, error)
	ListPyPIProjectFiles(context.Context, string, string) ([]PyPIFile, error)
	ListPyPIProjects(context.Context, string, string, int, string) ([]PyPIProjectSummary, error)
	TombstonePyPIVersion(context.Context, string, string, string) ([]PyPIFile, error)
	RestorePyPIVersion(context.Context, string, string, string) ([]PyPIFile, error)
	ListReclaimablePyPIObjects(context.Context, time.Time, int) ([]PyPIObject, error)
	PyPIObjectHasVisibleReference(context.Context, string) (bool, error)
	MarkPyPIObjectCollected(context.Context, string) error
}

type NativeGoStore interface {
	SyncGoProxyVersions(context.Context, string, string, []GoModuleVersion) error
	PutGoModuleVersion(context.Context, GoModuleVersion) (GoModuleVersion, error)
	ListGoModuleVersions(context.Context, string, string) ([]GoModuleVersion, error)
	GetGoModuleVersion(context.Context, string, string, string) (GoModuleVersion, error)
	CacheGoModuleAsset(context.Context, GoModuleAsset) (GoModuleAsset, error)
	GetGoModuleAsset(context.Context, string, string, string, string) (GoModuleAsset, error)
	LockGoObject(context.Context, string) (func(), error)
}

// NativeAPTStore owns immutable APT Proxy cache metadata and the shared object
// coordination boundary. Pre-visibility Hosted staging lives in
// NativeAPTPublicationStore; no staged record is an APT protocol asset.
type NativeAPTStore interface {
	GetAPTAsset(context.Context, string, string) (APTAsset, error)
	CacheAPTAsset(context.Context, APTAsset) (APTAsset, error)
	LockAPTObject(context.Context, string) (func(), error)
	ListAPTAssets(context.Context, string, string, int, string) ([]APTAsset, error)
}

// NativeAPTPublicationStore persists APT Hosted staging and immutable signed
// snapshots. A staged package is deliberately absent from NativeAPTStore
// protocol assets; only one atomically visible snapshot may make it readable.
type NativeAPTPublicationStore interface {
	CreateAPTPublicationSessionWithAuditIdempotently(context.Context, APTPublicationSession, string, string, string, string, AuditRecord) (APTPublicationSession, bool, error)
	GetAPTPublicationSession(context.Context, string) (APTPublicationSession, error)
	BeginAPTPackageUpload(context.Context, string, string) error
	CompleteAPTPackageUploadWithAudit(context.Context, string, APTPackageRevision, AuditRecord) (APTPackageRevision, error)
	GetAPTPackageRevisionForSession(context.Context, string) (APTPackageRevision, error)
	ExpireAPTPublicationSessions(context.Context, time.Time, int) ([]APTAbandonedUpload, error)
	ListUncollectedAPTPublicationObjects(context.Context, int) ([]APTAbandonedUpload, error)
	ListUnscheduledAPTPublicationObjects(context.Context, int) ([]APTAbandonedUpload, error)
	MarkAPTPublicationObjectScheduled(context.Context, string, string) error
	MarkAPTPublicationObjectCollected(context.Context, string, string) error
	APTObjectHasPackageReference(context.Context, string) (bool, error)
	CreateAPTRepositorySnapshot(context.Context, APTRepositorySnapshot, []APTSnapshotPackage) (APTRepositorySnapshot, error)
	CreateAPTSnapshotObjectIntents(context.Context, string, []APTSnapshotObjectIntent) error
	PublishAPTRepositorySnapshotWithAudit(context.Context, APTRepositorySnapshot, []APTSnapshotAsset, []byte, AuditRecord) (APTRepositorySnapshot, error)
	FailAPTRepositorySnapshot(context.Context, string) error
	ExpireAPTRepositorySnapshots(context.Context, time.Time, int) error
	ListUnscheduledAPTSnapshotObjects(context.Context, int) ([]APTSnapshotObjectIntent, error)
	MarkAPTSnapshotObjectScheduled(context.Context, string, string) error
	MarkAPTSnapshotObjectCollected(context.Context, string, string) error
	APTObjectHasDurableReference(context.Context, string) (bool, error)
	GetVisibleAPTRepositorySnapshot(context.Context, string, string) (APTRepositorySnapshot, error)
	GetVisibleAPTSnapshotAsset(context.Context, string, string) (APTSnapshotAsset, error)
	ListVisibleAPTSnapshotAssets(context.Context, string, string) ([]APTSnapshotAsset, error)
}

type Store interface {
	CreateGroup(context.Context, Group) (Group, error)
	GetGroup(context.Context, string) (Group, error)
	ListGroups(context.Context) ([]Group, error)
	DisableGroup(context.Context, string) error
	RecordAudit(context.Context, AuditRecord) error
	ListAudits(context.Context, AuditQuery) ([]AuditRecord, error)
}

type RuntimeNodeStore interface {
	UpsertRuntimeNodeHeartbeat(context.Context, RuntimeNode) error
	MarkRuntimeNodeStopped(context.Context, string, string, time.Time) error
	ListRuntimeNodes(context.Context) ([]RuntimeNode, error)
	PruneRuntimeNodes(context.Context, time.Time) (int64, error)
}

type AuditStore interface {
	ListAudits(context.Context, AuditQuery) ([]AuditRecord, error)
}

// AuditPageStore is implemented by stores that support stable cursor paging.
// It is optional so legacy Store implementations can continue serving the
// original array-shaped audit endpoint.
type AuditPageStore interface {
	ListAuditPage(context.Context, AuditQuery) (AuditPage, error)
}

type AuditRetentionStore interface {
	GetAuditRetentionPolicy(context.Context) (AuditRetentionPolicy, error)
	ReplaceAuditRetentionPolicy(context.Context, AuditRetentionPolicy, string) (AuditRetentionPolicy, error)
	EnqueueAuditCleanupJob(context.Context, AuditCleanupJob) (AuditCleanupJob, bool, error)
	ListAuditCleanupJobs(context.Context, int) ([]AuditCleanupJob, error)
	ClaimAuditCleanupJobs(context.Context, int) ([]AuditCleanupJob, error)
	CompleteAuditCleanupJob(context.Context, string, int) error
	FailAuditCleanupJob(context.Context, string, string) error
	DeleteAuditsBefore(context.Context, time.Time, int) (int, error)
}

// MavenStore keeps Maven Group configuration separate from OCI Groups.
type MavenStore interface {
	CreateMavenGroup(context.Context, Group) (Group, error)
	GetMavenGroup(context.Context, string) (Group, error)
	ListMavenGroups(context.Context) ([]Group, error)
	DisableMavenGroup(context.Context, string) error
	RecordAudit(context.Context, AuditRecord) error
}

type RawStore interface {
	CreateRawGroup(context.Context, Group) (Group, error)
	GetRawGroup(context.Context, string) (Group, error)
	ListRawGroups(context.Context) ([]Group, error)
	DisableRawGroup(context.Context, string) error
	RecordAudit(context.Context, AuditRecord) error
}

// ConanStore deliberately uses a separate Group namespace from OCI because a
// Conan Group is a distinct format and authorization boundary.
type ConanStore interface {
	CreateConanGroup(context.Context, Group) (Group, error)
	GetConanGroup(context.Context, string) (Group, error)
	ListConanGroups(context.Context) ([]Group, error)
	DisableConanGroup(context.Context, string) error
	RecordAudit(context.Context, AuditRecord) error
}

type NativeConanStore interface {
	CreateConanPublishSession(context.Context, ConanPublishSession) (ConanPublishSession, error)
	GetConanPublishSession(context.Context, string) (ConanPublishSession, error)
	MarkConanPublishObject(context.Context, string, string, string) error
	ListConanPublishUploads(context.Context, string) (map[string]string, error)
	CommitConanPublishSession(context.Context, string) error
	ListReclaimableConanObjects(context.Context, time.Time, int) ([]ConanObjectIntent, error)
	ConanObjectHasVisibleReference(context.Context, string) (bool, error)
	MarkConanObjectCollected(context.Context, string) error
	StageConanObject(context.Context, ConanObjectIntent) error
	PutConanRecipeRevision(context.Context, ConanRecipeRevision, []ConanAsset) (ConanRecipeRevision, error)
	PutConanPackageRevision(context.Context, ConanPackageRevision, []ConanAsset) (ConanPackageRevision, error)
	PublishReplicatedConanRevision(context.Context, ConanReplicationPublication) (ConanRecipeRevision, error)
	GetConanRecipeRevision(context.Context, string, string, string) (ConanRecipeRevision, error)
	GetConanPackageRevision(context.Context, string, string, string, string, string) (ConanPackageRevision, error)
	SearchConanReferences(context.Context, string, string, int, string) ([]ConanReference, error)
	ListConanRecipeRevisions(context.Context, string, string) ([]ConanRecipeRevision, error)
	SearchConanRecipeRevisions(context.Context, string, string, string, int, string) ([]ConanRecipeRevision, error)
	ListConanPackageRevisions(context.Context, string, string, string, string) ([]ConanPackageRevision, error)
	ListConanPackageIDs(context.Context, string, string, string) ([]string, error)
	ListConanRecipeAssets(context.Context, string, string, string) ([]ConanAsset, error)
	ListConanPackageAssets(context.Context, string, string, string, string, string) ([]ConanAsset, error)
	GetConanRecipeAsset(context.Context, string, string, string, string) (ConanAsset, error)
	GetConanPackageAsset(context.Context, string, string, string, string, string, string) (ConanAsset, error)
	TombstoneConanRecipeRevision(context.Context, string, string, string) (ConanRecipeRevision, error)
	TombstoneConanPackageRevision(context.Context, string, string, string, string, string) (ConanPackageRevision, error)
	RestoreConanRecipeRevision(context.Context, string, string, string) (ConanRecipeRevision, error)
	RestoreConanPackageRevision(context.Context, string, string, string, string, string) (ConanPackageRevision, error)
	PromoteConanRecipeRevision(context.Context, ConanPromotion) (ConanRecipeRevision, error)
}
