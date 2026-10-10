import type {
  GetAnonymousAccessPolicyResponses,
  GetDiagnosticsResponses,
  GetOidcSettingsResponses,
  GetOverviewStatisticsResponses,
  GetRepositoryCapabilitiesResponses,
  GetRepositoryCapacityResponses,
  GetRepositoryEffectiveAccessResponses,
  GetRepositoryResponses,
  ListApiKeysResponses,
  ListAuditPageResponses,
  ListAuditsResponses,
  ListFormatProfilesResponses,
  ListGroupsResponses,
  ListMavenCoordinatesResponses,
  ListRepositoriesResponses,
  ListRepositoryCapacitiesResponses,
  ListRepositoryGrantsResponses,
  ListRuntimeNodesResponses,
  ListScheduledTasksResponses,
  ListServiceAccountCredentialsResponses,
  ListServiceAccountsResponses,
  ListUsersResponses,
  SearchArtifactsResponses,
} from "../../src/client";

/**
 * Deterministic data for the visual baseline. Every payload is checked
 * against the generated client types, so a contract change breaks the build
 * here instead of silently drifting from what the Gateway really returns.
 */

export const FIXED_NOW = "2026-10-01T09:30:00Z";

type Repository = GetRepositoryResponses[200];

function repository(
  id: string,
  name: string,
  format: Repository["format"],
  type: "hosted" | "proxy",
  extra: Partial<Repository> = {},
): Repository {
  return {
    id,
    name,
    format,
    type,
    anonymousRead: false,
    mavenStrictPublication: false,
    state: "active",
    version: "3",
    ...extra,
  };
}

export const repositories = [
  repository("repo-maven-releases", "maven-releases", "maven", "hosted", {
    anonymousRead: true,
  }),
  repository("repo-maven-central", "maven-central", "maven", "proxy", {
    endpoint: "https://repo.maven.apache.org/maven2",
    allowedHosts: ["repo.maven.apache.org"],
  }),
  repository("repo-npm-hosted", "npm-hosted", "npm", "hosted"),
  repository("repo-oci-hosted", "containers", "oci", "hosted"),
  repository("repo-pypi-proxy", "pypi-proxy", "pypi", "proxy", {
    endpoint: "https://pypi.org",
    allowedHosts: ["pypi.org", "files.pythonhosted.org"],
  }),
  repository("repo-raw", "release-files", "raw", "hosted", {
    anonymousRead: true,
  }),
  repository("repo-go-proxy", "go-proxy", "go", "proxy", {
    endpoint: "https://proxy.golang.org",
    allowedHosts: ["proxy.golang.org"],
  }),
  repository("repo-cargo-hosted", "cargo-hosted", "cargo", "hosted"),
] satisfies Repository[];

export const repositoryPage = {
  items: repositories,
} satisfies ListRepositoriesResponses[200];

export const groupPage = {
  items: [
    {
      id: "group-maven-public",
      name: "maven-public",
      format: "maven",
      anonymousRead: true,
      members: [
        { repositoryId: "repo-maven-releases", position: 0 },
        { repositoryId: "repo-maven-central", position: 1 },
      ],
      version: "2",
    },
  ],
} satisfies ListGroupsResponses[200];

function counts(oneDay: number) {
  return { oneDay, sevenDays: oneDay * 6, thirtyDays: oneDay * 24 };
}

export const overviewStatistics = {
  generatedAt: FIXED_NOW,
  totals: {
    requests: counts(18_420),
    denied: counts(37),
    // Sums of the per-repository rows below.
    objectCount: 6000,
    usedBytes: 36 * 1_073_741_824,
  },
  repositories: repositories.map((item, index) => ({
    repositoryId: item.id,
    name: item.name,
    format: item.format,
    requests: counts(4200 - index * 480),
    denied: counts(index % 3 === 0 ? 9 - index : 0),
    objectCount: 1240 - index * 140,
    usedBytes: (8 - index) * 1_073_741_824,
  })),
} satisfies GetOverviewStatisticsResponses[200];

export const auditPage = {
  items: [
    {
      occurredAt: "2026-10-01T09:12:44Z",
      actor: "user:alice",
      operation: "repository.update",
      outcome: "success",
      repository: "maven-releases",
      format: "maven",
      status: 200,
      requestId: "req-7f3a9c2e",
    },
    {
      occurredAt: "2026-10-01T08:47:10Z",
      actor: "service-account:ci-publisher",
      operation: "publish",
      outcome: "success",
      repository: "npm-hosted",
      format: "npm",
      resource: "@acme/web-ui/-/web-ui-2.4.1.tgz",
      status: 201,
      bytes: 482_113,
      requestId: "req-1b84d0aa",
    },
    {
      occurredAt: "2026-10-01T08:03:59Z",
      actor: "user:bob",
      operation: "read",
      outcome: "denied",
      repository: "containers",
      format: "oci",
      resource: "platform/api",
      status: 403,
      authorizationReason: "no grant for repositories:read",
      requestId: "req-c02e51f9",
    },
  ],
} satisfies ListAuditPageResponses[200];

export const recentAudits = auditPage.items satisfies ListAuditsResponses[200];

export const repositoryCapacities = repositories.map((item, index) => ({
  repositoryId: item.id,
  format: item.format,
  usedBytes: (8 - index) * 1_073_741_824,
  objectCount: 1240 - index * 140,
  quotaBytes: index % 2 === 0 ? 20 * 1_073_741_824 : 0,
})) satisfies ListRepositoryCapacitiesResponses[200];

export const formatProfiles = {
  items: (
    [
      "oci",
      "maven",
      "conan",
      "raw",
      "npm",
      "pypi",
      "go",
      "cargo",
      "apt",
    ] as const
  ).map((format) => ({
    format,
    repositoryTypes: format === "apt" ? ["proxy"] : ["hosted", "proxy"],
    groupSupported: true,
    anonymousRead: true,
    hostedOperations:
      format === "apt"
        ? []
        : [
            "read",
            "publish",
            "browse",
            "delete",
            "restore",
            "retain",
            "reclaim",
            "promote",
            "replicate",
          ],
    proxyOperations: ["npm", "go", "cargo", "apt"].includes(format)
      ? ["read", "browse"]
      : ["read", "browse", "reclaim"],
  })),
} satisfies ListFormatProfilesResponses[200];

export const artifactSearch = {
  items: [
    {
      repositoryId: "repo-maven-releases",
      repositoryName: "maven-releases",
      format: "maven",
      matchKind: "coordinate",
      coordinate: "com.acme:demo-service",
      version: "1.8.2",
      versionCount: 14,
      size: 18_204_331,
      createdAt: "2026-09-28T14:02:11Z",
      publisher: "service-account:ci-publisher",
    },
    {
      repositoryId: "repo-npm-hosted",
      repositoryName: "npm-hosted",
      format: "npm",
      matchKind: "coordinate",
      coordinate: "@acme/demo-ui",
      version: "2.4.1",
      versionCount: 31,
      size: 482_113,
      createdAt: "2026-10-01T08:47:10Z",
      publisher: "service-account:ci-publisher",
    },
  ],
  searchedRepositories: 8,
} satisfies SearchArtifactsResponses[200];

export const scheduledTasks = [
  {
    id: "task-retention",
    name: "nightly-retention",
    description: "Apply retention to maven-releases",
    kind: "repository-retention",
    repositoryId: "repo-maven-releases",
    intervalMinutes: 1440,
    enabled: true,
    nextRunAt: "2026-10-02T02:00:00Z",
    lastRunAt: "2026-10-01T02:00:00Z",
    lastRunState: "submitted",
    version: "4",
    createdAt: "2026-08-01T00:00:00Z",
    updatedAt: "2026-09-20T00:00:00Z",
  },
] satisfies ListScheduledTasksResponses[200];

export const runtimeNodes = {
  items: [
    {
      instanceId: "gateway-0",
      sessionId: "session-a",
      version: "0.8.0",
      revision: "b2db161",
      roles: ["standalone"],
      workerFormats: [],
      workerKinds: [],
      startedAt: "2026-09-30T00:00:00Z",
      lastSeenAt: FIXED_NOW,
      status: "online",
    },
  ],
  health: { status: "healthy", online: 1, stale: 0, offline: 0, issues: [] },
  currentSessionId: "session-a",
  releaseSource: "not_configured",
} satisfies ListRuntimeNodesResponses[200];

export const diagnostics = {
  generatedAt: FIXED_NOW,
  build: {
    version: "0.8.0",
    revision: "b2db161",
    goVersion: "go1.26.9",
    modified: false,
  },
  runtime: {
    instanceId: "gateway-0",
    roles: ["standalone"],
    workerFormats: [],
    workerKinds: [],
  },
  dependencies: [],
  queues: [],
  nodes: { status: "healthy", online: 1, stale: 0, offline: 0, issues: [] },
} satisfies GetDiagnosticsResponses[200];

export const anonymousAccessPolicy = {
  enabled: true,
  version: "1",
} satisfies GetAnonymousAccessPolicyResponses[200];

export const apiKeys = {
  items: [
    {
      id: "key-ci",
      name: "legacy-ci",
      roles: ["member"],
      createdAt: "2026-06-01T00:00:00Z",
      expiresAt: "2026-12-31T00:00:00Z",
      lastUsedAt: "2026-09-30T22:10:00Z",
    },
  ],
} satisfies ListApiKeysResponses[200];

export const serviceAccountCredentials = {
  items: [
    {
      id: "cred-2026q4",
      serviceAccountId: "sa-ci",
      name: "2026-q4",
      createdAt: "2026-09-15T00:00:00Z",
      expiresAt: "2026-12-31T00:00:00Z",
      lastUsedAt: "2026-10-01T08:47:10Z",
    },
  ],
} satisfies ListServiceAccountCredentialsResponses[200];

export const repositoryGrants = [
  {
    repositoryId: "repo-maven-releases",
    repositoryName: "maven-releases",
    format: "maven",
    principal: "service-account:ci-publisher",
    scopes: ["repositories:read", "repositories:write"],
  },
  {
    repositoryId: "repo-npm-hosted",
    repositoryName: "npm-hosted",
    format: "npm",
    principal: "user:alice",
    scopes: ["repositories:admin"],
  },
] satisfies ListRepositoryGrantsResponses[200];

export const serviceAccounts = {
  items: [
    {
      id: "sa-ci",
      name: "ci-publisher",
      description: "Publishes release builds from CI",
      state: "active",
      createdAt: "2026-07-01T00:00:00Z",
      updatedAt: "2026-09-01T00:00:00Z",
      version: "2",
    },
  ],
} satisfies ListServiceAccountsResponses[200];

function user(
  id: string,
  name: string,
  displayName: string,
  role: "member" | "admin",
  lastLoginAt?: string,
) {
  return {
    id,
    name,
    displayName,
    email: `${name}@example.com`,
    description: "",
    role,
    state: "active" as const,
    lastLoginAt,
    localPasswordEnabled: true,
    failedLoginAttempts: 0,
    mustChangePassword: false,
    createdAt: "2026-05-01T00:00:00Z",
    version: "1",
  };
}

export const users = {
  items: [
    user("user-alice", "alice", "Alice Chen", "admin", "2026-10-01T08:00:00Z"),
    user("user-bob", "bob", "Bob Li", "member", "2026-09-29T11:20:00Z"),
  ],
  total: 2,
  offset: 0,
  limit: 50,
} satisfies ListUsersResponses[200];

export const oidcSettings = {
  version: "1",
  source: "database",
  enabled: false,
  issuer: "",
  audience: "",
  clientId: "",
  clientSecretConfigured: false,
  redirectUrl: "",
  scopes: ["openid", "profile", "email"],
  adminSubjects: [],
  memberRoles: [],
  adminRoles: [],
  provisioningMode: "disabled",
  emailLinkingEnabled: false,
  jitDefaultRole: "none",
} satisfies GetOidcSettingsResponses[200];

/** Not in the management contract; Login reads it with plain fetch. */
export const oidcLoginConfig = { enabled: false };

export const publicRepositories = {
  enabled: true,
  items: repositories
    .filter((item) => item.anonymousRead)
    .map(({ id, name, format, type }) => ({ id, name, format, type })),
};

/** Detail-page answers for maven-releases, seen by the platform admin. */
export const mavenReleases = repositories[0];

export const mavenReleasesCapabilities = {
  format: "maven",
  type: "hosted",
  operations: ["read", "publish", "browse", "delete", "retain", "promote"],
  artifactScanning: true,
  publicationScanning: true,
} satisfies GetRepositoryCapabilitiesResponses[200];

const allow = { allowed: true, source: "platform", reason: "administrator" };

export const mavenReleasesAccess = {
  actor: "mock-admin",
  identity: {
    actor: "mock-admin",
    kind: "local_session",
    role: "admin",
    administrator: true,
  },
  resource: "",
  simulated: false,
  repository: {
    id: mavenReleases.id,
    name: mavenReleases.name,
    format: "maven",
    type: "hosted",
    state: "active",
  },
  anonymousRead: { allowed: true, source: "repository", reason: "enabled" },
  permissions: { read: allow, write: allow, admin: allow, intelligence: allow },
} satisfies GetRepositoryEffectiveAccessResponses[200];

export const mavenReleasesCapacity = {
  repositoryId: mavenReleases.id,
  format: "maven",
  usedBytes: 8 * 1_073_741_824,
  objectCount: 1240,
  quotaBytes: 20 * 1_073_741_824,
} satisfies GetRepositoryCapacityResponses[200];

export const mavenReleasesCoordinates = {
  items: [
    ["com.acme:demo-service:1.8.2", "9f86d081884c7d65", "2026-09-28T14:02:11Z"],
    ["com.acme:billing-core:3.2.0", "3c0a1d8e5b7f2a91", "2026-09-27T09:41:03Z"],
    ["com.acme:auth-client:0.9.7", "e4b2c91f07ad6630", "2026-09-25T17:20:44Z"],
    [
      "com.acme.platform:bom:2026.10.0",
      "71fd2e09a3c4b850",
      "2026-09-24T08:05:12Z",
    ],
  ].map(([coordinate, digest, createdAt]) => ({
    coordinate,
    digest: `sha256:${digest.repeat(4)}`,
    createdAt,
    publisher: "service-account:ci-publisher",
  })),
} satisfies ListMavenCoordinatesResponses[200];
