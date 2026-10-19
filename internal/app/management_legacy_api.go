package app

import (
	migrationapi "github.com/ccsert/artifact-gateway/internal/admin/nexusmigration"
	sourceapi "github.com/ccsert/artifact-gateway/internal/admin/nexussource"
	"github.com/ccsert/artifact-gateway/internal/aptpublication"
	"github.com/ccsert/artifact-gateway/internal/consoletheme"
	"github.com/ccsert/artifact-gateway/internal/operationalog"
	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/ccsert/artifact-gateway/internal/scanning"
)

// generatedRepositoryAPIAdapter temporarily owns the domains not yet extracted
// under #280. Migrated operations live only on their domain handler.
type generatedRepositoryAPIAdapter struct {
	*sourceapi.Adapter
	*migrationapi.BatchAdapter
	managementScope
	store                  repository.HostedRepositoryStore
	sessions               nativeMavenHandler
	conanPublish           nativeConanPublishHandler
	aptPublication         *aptpublication.Manager
	aptSnapshotPublisher   *aptpublication.Publisher
	aptSnapshotImporter    *aptpublication.SnapshotArchiveImporter
	aptSnapshotExporter    aptpublication.SnapshotArchiveExporter
	aptPublications        repository.NativeAPTPublicationStore
	groups                 repository.HostedGroupStore
	grants                 repository.RepositoryGrantStore
	templates              repository.AuthorizationTemplateStore
	authorizationRoles     repository.AuthorizationRoleStore
	retentionPolicies      repository.RepositoryRetentionPolicyStore
	securityPolicies       repository.RepositorySecurityPolicyStore
	quarantineReadPolicies repository.RepositoryQuarantineReadPolicyStore
	capacities             repository.RepositoryCapacityStore
	tombstones             repository.ArtifactTombstoneStore
	lifecycleJobs          repository.LifecycleJobStore
	auditRetention         repository.AuditRetentionStore
	anonymousAccess        repository.AnonymousAccessPolicyStore
	siteSettings           repository.SiteSettingsStore
	consoleThemePackages   repository.ConsoleThemeStore
	consoleThemes          *consoletheme.Registry
	oidcRuntime            *OIDCRuntime
	replication            repository.ReplicationStore
	oci                    repository.NativeOCIStore
	cargo                  repository.NativeCargoStore
	cargoProxy             repository.CargoProxyStore
	conan                  repository.NativeConanStore
	apiKeys                repository.APIKeyStore
	serviceAccounts        repository.ServiceAccountStore
	users                  userManagementStore
	audit                  repository.Store
	maintenance            *CacheMaintenance
	proxyCache             proxyCacheBrowseHandler
	mavenProxy             mavenProxyOperationsHandler
	searchProjection       repository.ArtifactSearchStore
	intelligence           repository.ArtifactIntelligenceStore
	quarantine             repository.ArtifactQuarantineStore
	runtimeNodes           repository.RuntimeNodeStore
	scheduledTasks         repository.ScheduledTaskStore
	emailTargets           repository.EmailStore
	quotaAlerts            repository.RepositoryQuotaAlertStore
	webhooks               repository.WebhookStore
	queueStats             repository.BackgroundOperationQueueStore
	browse                 repository.ArtifactBrowseStore
	diagnostics            Dependencies
	objects                ObjectStores
	logBuffer              *operationalog.Buffer
	artifactScanner        scanning.Scanner
	artifactScanFormats    []repository.Format
}

type userManagementStore interface {
	repository.UserStore
	repository.UserIdentityStore
	repository.UserSessionStore
}
