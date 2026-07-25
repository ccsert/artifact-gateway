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

type ArtifactTombstoneStore interface {
	GetArtifactTombstone(context.Context, string, Format, string) (ArtifactTombstone, error)
}

type LifecycleJobStore interface {
	EnqueueLifecycleJob(context.Context, LifecycleJob) (LifecycleJob, bool, error)
	ClaimLifecycleJobs(context.Context, int) ([]LifecycleJob, error)
	ClaimLifecycleJobsByKind(context.Context, LifecycleJobKind, int) ([]LifecycleJob, error)
	CompleteLifecycleJob(context.Context, string) error
	FailLifecycleJob(context.Context, string, string) error
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
	GetMavenAsset(context.Context, string, string) (MavenAsset, error)
	ListMavenArtifacts(context.Context, string) ([]MavenArtifact, error)
	GetMavenArtifact(context.Context, string, string) (MavenArtifact, error)
	TombstoneMavenArtifact(context.Context, string, string) (MavenArtifact, error)
	ClaimExpiredMavenObjectIntents(context.Context, time.Time, int) ([]MavenObjectIntent, error)
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
	ListOCITags(context.Context, string, string, int, string) ([]string, error)
	DeleteOCIManifest(context.Context, string, string, string) error
}

type NativeRawStore interface {
	LockRawObject(context.Context, string) (func(), error)
	StageRawObject(context.Context, RawObject) error
	PutRawAsset(context.Context, RawAsset) (RawAsset, error)
	GetRawAsset(context.Context, string, string) (RawAsset, error)
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
