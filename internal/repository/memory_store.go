package repository

import (
	"sync"
	"time"
)

type MemoryStore struct {
	mu                 sync.RWMutex
	groups             map[string]Group
	mavenGroups        map[string]Group
	rawGroups          map[string]Group
	conanGroups        map[string]Group
	Audits             []AuditRecord
	hostedRepositories map[string]HostedRepository
	hostedGroups       map[string]HostedGroup
	repositoryGrants   map[string]RepositoryGrantSet
	retentionPolicies  map[string]RepositoryRetentionPolicy
	capacityQuotas     map[string]int64
	idempotencyRecords map[string]idempotencyRecord
	mavenSessions      map[string]MavenPublishSession
	mavenUploads       map[string]map[string]string
	mavenAssets        map[string]MavenAsset
	mavenArtifacts     map[string]MavenArtifact
	mavenSessionKeys   map[string]idempotencyRecord
	mavenCommitKeys    map[string]mavenCommitRecord
	mavenObjectIntents map[string]mavenObjectIntent
	mavenObjectRefs    map[string]bool
	conanObjects       map[string]ConanObjectIntent
	conanSessions      map[string]ConanPublishSession
	conanUploads       map[string]map[string]string
	conanAssets        map[string]ConanAsset
	conanRecipes       map[string]ConanRecipeRevision
	conanPackages      map[string]ConanPackageRevision
	ociUploads         map[string]OCIUpload
	ociBlobs           map[string]OCIBlob
	ociRepositoryBlobs map[string]map[string]bool
	ociManifests       map[string]OCIManifest
	ociTags            map[string]string
	ociUploadLocks     map[string]*sync.Mutex
	ociObjectLocks     map[string]*sync.Mutex
	rawAssets          map[string]RawAsset
	rawObjects         map[string]RawObject
	rawObjectLocks     map[string]*sync.Mutex
	rawUploads         map[string]RawUpload
	rawUploadLocks     map[string]*sync.Mutex
	ociObjectIntents   map[string]OCIObjectIntent
	artifactTombstones map[string]ArtifactTombstone
	lifecycleJobs      map[string]LifecycleJob
	replicationPlans   map[string]ReplicationPlan
	replicationKeys    map[string]string
	replicationChecks  map[string]map[string]ReplicationCheckpoint
	apiKeys            map[string]APIKey
}

type mavenObjectIntent struct {
	createdAt, claimedAt, deletedAt time.Time
	claimToken                      string
}

type idempotencyRecord struct {
	payload, repositoryID string
	expiresAt             time.Time
}

type mavenCommitRecord struct {
	key, payload string
	expiresAt    time.Time
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{groups: make(map[string]Group), mavenGroups: make(map[string]Group), rawGroups: make(map[string]Group), conanGroups: make(map[string]Group), hostedRepositories: make(map[string]HostedRepository), hostedGroups: make(map[string]HostedGroup), repositoryGrants: make(map[string]RepositoryGrantSet), retentionPolicies: make(map[string]RepositoryRetentionPolicy), capacityQuotas: make(map[string]int64), idempotencyRecords: make(map[string]idempotencyRecord), mavenSessions: make(map[string]MavenPublishSession), mavenUploads: make(map[string]map[string]string), mavenAssets: make(map[string]MavenAsset), mavenArtifacts: make(map[string]MavenArtifact), mavenSessionKeys: make(map[string]idempotencyRecord), mavenCommitKeys: make(map[string]mavenCommitRecord), mavenObjectIntents: make(map[string]mavenObjectIntent), mavenObjectRefs: make(map[string]bool), conanObjects: make(map[string]ConanObjectIntent), conanSessions: make(map[string]ConanPublishSession), conanUploads: make(map[string]map[string]string), conanAssets: make(map[string]ConanAsset), conanRecipes: make(map[string]ConanRecipeRevision), conanPackages: make(map[string]ConanPackageRevision), ociUploads: make(map[string]OCIUpload), ociBlobs: make(map[string]OCIBlob), ociRepositoryBlobs: make(map[string]map[string]bool), ociManifests: make(map[string]OCIManifest), ociTags: make(map[string]string), ociUploadLocks: make(map[string]*sync.Mutex), ociObjectLocks: make(map[string]*sync.Mutex), rawAssets: make(map[string]RawAsset), rawObjects: make(map[string]RawObject), rawObjectLocks: make(map[string]*sync.Mutex), rawUploads: make(map[string]RawUpload), rawUploadLocks: make(map[string]*sync.Mutex), ociObjectIntents: make(map[string]OCIObjectIntent), artifactTombstones: make(map[string]ArtifactTombstone), lifecycleJobs: make(map[string]LifecycleJob), replicationPlans: make(map[string]ReplicationPlan), replicationKeys: make(map[string]string), replicationChecks: make(map[string]map[string]ReplicationCheckpoint), apiKeys: make(map[string]APIKey)}
}
