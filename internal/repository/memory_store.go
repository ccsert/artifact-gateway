package repository

import (
	"sync"
	"time"
)

type MemoryStore struct {
	mu                     sync.RWMutex
	groups                 map[string]Group
	mavenGroups            map[string]Group
	rawGroups              map[string]Group
	conanGroups            map[string]Group
	Audits                 []AuditRecord
	auditRetentionPolicy   AuditRetentionPolicy
	anonymousAccessPolicy  AnonymousAccessPolicy
	oidcSettings           *OIDCSettings
	auditCleanupJobs       map[string]AuditCleanupJob
	hostedRepositories     map[string]HostedRepository
	hostedGroups           map[string]HostedGroup
	repositoryGrants       map[string]RepositoryGrantSet
	authorizationTemplates map[string]AuthorizationTemplate
	retentionPolicies      map[string]RepositoryRetentionPolicy
	securityPolicies       map[string]RepositorySecurityPolicy
	capacityQuotas         map[string]int64
	idempotencyRecords     map[string]idempotencyRecord
	mavenSessions          map[string]MavenPublishSession
	mavenUploads           map[string]map[string]string
	mavenAssets            map[string]MavenAsset
	mavenArtifacts         map[string]MavenArtifact
	mavenSessionKeys       map[string]idempotencyRecord
	mavenCommitKeys        map[string]mavenCommitRecord
	mavenObjectIntents     map[string]mavenObjectIntent
	mavenObjectRefs        map[string]bool
	conanObjects           map[string]ConanObjectIntent
	conanSessions          map[string]ConanPublishSession
	conanUploads           map[string]map[string]string
	conanAssets            map[string]ConanAsset
	conanRecipes           map[string]ConanRecipeRevision
	conanPackages          map[string]ConanPackageRevision
	ociUploads             map[string]OCIUpload
	ociBlobs               map[string]OCIBlob
	ociRepositoryBlobs     map[string]map[string]bool
	ociManifests           map[string]OCIManifest
	ociDeletedManifests    map[string]ociDeletedManifest
	ociTags                map[string]string
	ociUploadLocks         map[string]*sync.Mutex
	ociObjectLocks         map[string]*sync.Mutex
	rawAssets              map[string]RawAsset
	rawObjects             map[string]RawObject
	rawAssetTombstones     map[string]RawAsset
	rawObjectLocks         map[string]*sync.Mutex
	rawUploads             map[string]RawUpload
	rawUploadLocks         map[string]*sync.Mutex
	npmPackages            map[string]NPMPackage
	npmVersions            map[string]NPMVersion
	npmProxyLocks          map[string]*sync.Mutex
	npmObjectLocks         map[string]*sync.Mutex
	pypiFiles              map[string]PyPIFile
	pypiObjectLocks        map[string]*sync.Mutex
	goVersions             map[string]GoModuleVersion
	goAssets               map[string]GoModuleAsset
	goObjectLocks          map[string]*sync.Mutex
	ociObjectIntents       map[string]OCIObjectIntent
	artifactTombstones     map[string]ArtifactTombstone
	artifactIntelligence   map[string]ArtifactIntelligence
	lifecycleJobs          map[string]LifecycleJob
	lifecycleCreatedAt     time.Time
	replicationPlans       map[string]ReplicationPlan
	replicationKeys        map[string]string
	replicationChecks      map[string]map[string]ReplicationCheckpoint
	apiKeys                map[string]APIKey
	users                  map[string]User
	userIdentities         map[string]UserIdentity
	runtimeNodes           map[string]RuntimeNode
	scheduledTasks         map[string]ScheduledTask
	scheduledTaskRuns      map[string]ScheduledTaskRun
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
	return &MemoryStore{groups: make(map[string]Group), mavenGroups: make(map[string]Group), rawGroups: make(map[string]Group), conanGroups: make(map[string]Group), hostedRepositories: make(map[string]HostedRepository), hostedGroups: make(map[string]HostedGroup), repositoryGrants: make(map[string]RepositoryGrantSet), authorizationTemplates: make(map[string]AuthorizationTemplate), retentionPolicies: make(map[string]RepositoryRetentionPolicy), securityPolicies: make(map[string]RepositorySecurityPolicy), capacityQuotas: make(map[string]int64), idempotencyRecords: make(map[string]idempotencyRecord), mavenSessions: make(map[string]MavenPublishSession), mavenUploads: make(map[string]map[string]string), mavenAssets: make(map[string]MavenAsset), mavenArtifacts: make(map[string]MavenArtifact), mavenSessionKeys: make(map[string]idempotencyRecord), mavenCommitKeys: make(map[string]mavenCommitRecord), mavenObjectIntents: make(map[string]mavenObjectIntent), mavenObjectRefs: make(map[string]bool), conanObjects: make(map[string]ConanObjectIntent), conanSessions: make(map[string]ConanPublishSession), conanUploads: make(map[string]map[string]string), conanAssets: make(map[string]ConanAsset), conanRecipes: make(map[string]ConanRecipeRevision), conanPackages: make(map[string]ConanPackageRevision), ociUploads: make(map[string]OCIUpload), ociBlobs: make(map[string]OCIBlob), ociRepositoryBlobs: make(map[string]map[string]bool), ociManifests: make(map[string]OCIManifest), ociDeletedManifests: make(map[string]ociDeletedManifest), ociTags: make(map[string]string), ociUploadLocks: make(map[string]*sync.Mutex), ociObjectLocks: make(map[string]*sync.Mutex), rawAssets: make(map[string]RawAsset), rawObjects: make(map[string]RawObject), rawAssetTombstones: make(map[string]RawAsset), rawObjectLocks: make(map[string]*sync.Mutex), rawUploads: make(map[string]RawUpload), rawUploadLocks: make(map[string]*sync.Mutex), npmPackages: make(map[string]NPMPackage), npmVersions: make(map[string]NPMVersion), npmProxyLocks: make(map[string]*sync.Mutex), npmObjectLocks: make(map[string]*sync.Mutex), pypiFiles: make(map[string]PyPIFile), pypiObjectLocks: make(map[string]*sync.Mutex), goVersions: make(map[string]GoModuleVersion), goAssets: make(map[string]GoModuleAsset), goObjectLocks: make(map[string]*sync.Mutex), ociObjectIntents: make(map[string]OCIObjectIntent), artifactTombstones: make(map[string]ArtifactTombstone), artifactIntelligence: make(map[string]ArtifactIntelligence), lifecycleJobs: make(map[string]LifecycleJob), auditCleanupJobs: make(map[string]AuditCleanupJob), replicationPlans: make(map[string]ReplicationPlan), replicationKeys: make(map[string]string), replicationChecks: make(map[string]map[string]ReplicationCheckpoint), apiKeys: make(map[string]APIKey), users: make(map[string]User), userIdentities: make(map[string]UserIdentity), runtimeNodes: make(map[string]RuntimeNode), scheduledTasks: make(map[string]ScheduledTask), scheduledTaskRuns: make(map[string]ScheduledTaskRun)}
}

func (s *MemoryStore) lockMemoryObject(locks map[string]*sync.Mutex, key string) (func(), error) {
	s.mu.Lock()
	lock := locks[key]
	if lock == nil {
		lock = &sync.Mutex{}
		locks[key] = lock
	}
	s.mu.Unlock()
	lock.Lock()
	return lock.Unlock, nil
}
