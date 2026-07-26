package repository

import (
	"context"
	"errors"
	"time"
)

var (
	ErrNotFound            = errors.New("group not found")
	ErrDisabled            = errors.New("group is disabled")
	ErrNameExists          = errors.New("group name already exists")
	ErrIdempotencyConflict = errors.New("idempotency key conflicts with request")
	ErrVersionConflict     = errors.New("resource version conflicts with current state")
	ErrQuotaExceeded       = errors.New("repository capacity quota exceeded")
)

type HostedRepositoryStore interface {
	CreateHostedRepository(context.Context, HostedRepository) (HostedRepository, error)
	CreateHostedRepositoryIdempotently(context.Context, HostedRepository, string, string, string) (HostedRepository, bool, error)
	ListHostedRepositories(context.Context, int, string) ([]HostedRepository, string, error)
	GetHostedRepository(context.Context, string) (HostedRepository, error)
	GetHostedRepositoryByName(context.Context, string) (HostedRepository, error)
	DisableHostedRepository(context.Context, string) (HostedRepository, error)
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

type RepositoryRetentionPolicyStore interface {
	GetRepositoryRetentionPolicy(context.Context, string) (RepositoryRetentionPolicy, error)
	ReplaceRepositoryRetentionPolicy(context.Context, string, RepositoryRetentionPolicy, string) (RepositoryRetentionPolicy, error)
}

type RepositoryCapacityStore interface {
	GetRepositoryCapacity(context.Context, string) (RepositoryCapacity, error)
	ReplaceRepositoryCapacityQuota(context.Context, string, int64) (RepositoryCapacity, error)
}

// BackgroundOperationMetrics accepts only bounded operation dimensions. It is
// deliberately independent of repository IDs and artifact coordinates.
type BackgroundOperationMetrics interface {
	RecordBackgroundOperation(string, Format, string)
	AddBackgroundOperationInFlight(string, Format, int64)
}

type ArtifactTombstoneStore interface {
	GetArtifactTombstone(context.Context, string, Format, string) (ArtifactTombstone, error)
	ListArtifactTombstones(context.Context, string, Format, string, int, string) ([]ArtifactTombstone, error)
}

type LifecycleJobStore interface {
	EnqueueLifecycleJob(context.Context, LifecycleJob) (LifecycleJob, bool, error)
	ListLifecycleJobs(context.Context, string, int) ([]LifecycleJob, error)
	ClaimLifecycleJobs(context.Context, int) ([]LifecycleJob, error)
	ClaimLifecycleJobsByKind(context.Context, LifecycleJobKind, int) ([]LifecycleJob, error)
	ClaimLifecycleJobsByKindAndFormat(context.Context, LifecycleJobKind, Format, int) ([]LifecycleJob, error)
	CompleteLifecycleJob(context.Context, string) error
	FailLifecycleJob(context.Context, string, string) error
}

type ReplicationStore interface {
	CreateReplicationPlan(context.Context, ReplicationPlan, []ReplicationCheckpoint) (ReplicationPlan, bool, error)
	ClaimReplicationPlans(context.Context, int) ([]ReplicationPlan, error)
	ClaimReplicationPlansByFormat(context.Context, Format, int) ([]ReplicationPlan, error)
	ListReplicationPlans(context.Context, string, int) ([]ReplicationPlan, error)
	ListReplicationCheckpoints(context.Context, string) ([]ReplicationCheckpoint, error)
	UpdateReplicationCheckpoint(context.Context, ReplicationCheckpoint) error
	CompleteReplicationPlan(context.Context, string) error
	FailReplicationPlan(context.Context, string, string) error
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
	SearchMavenArtifacts(context.Context, string, string, int, string) ([]MavenArtifact, error)
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
	GetOCIManifest(context.Context, string, string, string) (OCIManifest, error)
	ListOCIReferrers(context.Context, string, string, string, int, string) ([]OCIManifest, error)
	ListOCIManifestNames(context.Context, string, int, string) ([]string, error)
	SearchOCIManifestNames(context.Context, string, string, int, string) ([]string, error)
	ListOCITags(context.Context, string, string, int, string) ([]string, error)
	DeleteOCIManifest(context.Context, string, string, string) error
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
	ListUnreferencedRawObjects(context.Context, time.Time, int) ([]RawObject, error)
	RawObjectIsUnreferenced(context.Context, string) (bool, error)
	MarkRawObjectCollected(context.Context, string) error
}

type Store interface {
	CreateGroup(context.Context, Group) (Group, error)
	GetGroup(context.Context, string) (Group, error)
	DisableGroup(context.Context, string) error
	RecordAudit(context.Context, AuditRecord) error
	ListAudits(context.Context, AuditQuery) ([]AuditRecord, error)
}

type AuditStore interface {
	ListAudits(context.Context, AuditQuery) ([]AuditRecord, error)
}

// MavenStore keeps Maven Group configuration separate from OCI Groups.
type MavenStore interface {
	CreateMavenGroup(context.Context, Group) (Group, error)
	GetMavenGroup(context.Context, string) (Group, error)
	DisableMavenGroup(context.Context, string) error
	RecordAudit(context.Context, AuditRecord) error
}

type RawStore interface {
	CreateRawGroup(context.Context, Group) (Group, error)
	GetRawGroup(context.Context, string) (Group, error)
	DisableRawGroup(context.Context, string) error
	RecordAudit(context.Context, AuditRecord) error
}

// ConanStore deliberately uses a separate Group namespace from OCI because a
// Conan Group is a distinct format and authorization boundary.
type ConanStore interface {
	CreateConanGroup(context.Context, Group) (Group, error)
	GetConanGroup(context.Context, string) (Group, error)
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
	GetConanRecipeRevision(context.Context, string, string, string) (ConanRecipeRevision, error)
	GetConanPackageRevision(context.Context, string, string, string, string, string) (ConanPackageRevision, error)
	SearchConanReferences(context.Context, string, string, int, string) ([]string, error)
	ListConanRecipeRevisions(context.Context, string, string) ([]ConanRecipeRevision, error)
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
