/**
 * Query keys, one factory per resource. Keys nest from broad to narrow so a
 * mutation invalidates exactly the reads it can have changed:
 *
 *   invalidateQueries({ queryKey: repositoryKeys.all })        every repository read
 *   invalidateQueries({ queryKey: repositoryKeys.detail(id) }) one repository
 */
export const repositoryKeys = {
  all: ["repositories"] as const,
  list: () => [...repositoryKeys.all, "list"] as const,
  capacities: () => [...repositoryKeys.all, "capacities"] as const,
  detail: (id: string) => [...repositoryKeys.all, "detail", id] as const,
  capabilities: (id: string) =>
    [...repositoryKeys.detail(id), "capabilities"] as const,
  effectiveAccess: (id: string) =>
    [...repositoryKeys.detail(id), "effective-access"] as const,
  capacity: (id: string) => [...repositoryKeys.detail(id), "capacity"] as const,
  retentionPolicy: (id: string) =>
    [...repositoryKeys.detail(id), "retention-policy"] as const,
  securityPolicy: (id: string) =>
    [...repositoryKeys.detail(id), "security-policy"] as const,
  quarantineReadPolicy: (id: string) =>
    [...repositoryKeys.detail(id), "quarantine-read-policy"] as const,
  aptSigningState: (id: string) =>
    [...repositoryKeys.detail(id), "apt-signing-state"] as const,
  proxyHealth: (id: string) =>
    [...repositoryKeys.detail(id), "proxy-health"] as const,
  lifecycleJobs: (id: string) =>
    [...repositoryKeys.detail(id), "lifecycle-jobs"] as const,
  tombstones: (id: string) =>
    [...repositoryKeys.detail(id), "tombstones"] as const,
  scanJobs: (id: string) =>
    [...repositoryKeys.detail(id), "scan-jobs"] as const,
  replicationPlans: (id: string) =>
    [...repositoryKeys.detail(id), "replication-plans"] as const,
  replicationPlan: (id: string, planId: string) =>
    [...repositoryKeys.replicationPlans(id), planId] as const,
  aptLifecycle: (id: string, suite: string) =>
    [...repositoryKeys.detail(id), "apt-lifecycle", suite] as const,
};

export const identityKeys = {
  all: ["identity"] as const,
  apiKeys: () => [...identityKeys.all, "api-keys"] as const,
  serviceAccounts: () => [...identityKeys.all, "service-accounts"] as const,
  credentials: (serviceAccountId: string) =>
    [
      ...identityKeys.serviceAccounts(),
      serviceAccountId,
      "credentials",
    ] as const,
  users: () => [...identityKeys.all, "users"] as const,
  userSessions: (userId: string) =>
    [...identityKeys.all, "user-sessions", userId] as const,
  userIdentities: (userId: string) =>
    [...identityKeys.all, "user-identities", userId] as const,
  oidcIssuer: () => [...identityKeys.all, "oidc-issuer"] as const,
};

export const accessKeys = {
  all: ["access"] as const,
  grants: () => [...accessKeys.all, "grants"] as const,
  anonymousPolicy: () => [...accessKeys.all, "anonymous-policy"] as const,
  templates: () => [...accessKeys.all, "templates"] as const,
  roles: () => [...accessKeys.all, "roles"] as const,
  repositoryGrants: (repositoryId: string) =>
    [...accessKeys.all, "repository-grants", repositoryId] as const,
  /** Single 200-item pages used to choose principals and repositories. */
  directory: (source: "users" | "service-accounts" | "repositories") =>
    [...accessKeys.all, "directory", source] as const,
  /** One submitted effective-access evaluation. */
  evaluation: (
    repositoryId: string,
    query: Record<string, string | undefined>,
  ) => [...accessKeys.all, "evaluation", repositoryId, query] as const,
};

export const operationKeys = {
  all: ["operations"] as const,
  jobs: () => [...operationKeys.all, "jobs"] as const,
  scheduledTasks: () => [...operationKeys.all, "scheduled-tasks"] as const,
  taskRuns: (taskId: string) =>
    [...operationKeys.scheduledTasks(), taskId, "runs"] as const,
  retentionRepositories: () =>
    [...operationKeys.all, "retention-repositories"] as const,
  webhookSubscriptions: () =>
    [...operationKeys.all, "webhook-subscriptions"] as const,
  webhookDeliveries: (state: string) =>
    [...operationKeys.all, "webhook-deliveries", state] as const,
};

export const systemKeys = {
  all: ["system"] as const,
  diagnostics: () => [...systemKeys.all, "diagnostics"] as const,
  runtimeNodes: () => [...systemKeys.all, "runtime-nodes"] as const,
};

export const groupKeys = {
  all: ["groups"] as const,
  list: () => [...groupKeys.all, "list"] as const,
  members: (id: string) => [...groupKeys.all, "members", id] as const,
  capacity: (id: string) => [...groupKeys.all, "capacity", id] as const,
  browse: (id: string) => [...groupKeys.all, "browse", id] as const,
  resolution: (id: string) => [...groupKeys.all, "resolution", id] as const,
};

export const auditKeys = {
  all: ["audits"] as const,
  pages: (filters: object) => [...auditKeys.all, "pages", filters] as const,
  retentionPolicy: () => [...auditKeys.all, "retention-policy"] as const,
  retentionJobs: () => [...auditKeys.all, "retention-jobs"] as const,
};

export const settingsKeys = {
  all: ["settings"] as const,
  oidc: () => [...settingsKeys.all, "oidc"] as const,
};

export const dashboardKeys = {
  all: ["dashboard"] as const,
  groups: () => [...dashboardKeys.all, "groups"] as const,
  audits: () => [...dashboardKeys.all, "audits"] as const,
  statistics: () => [...dashboardKeys.all, "statistics"] as const,
};

export const siteKeys = {
  settings: () => ["site", "settings"] as const,
};

export const searchKeys = {
  all: ["search"] as const,
  artifacts: (match: string, q: string) =>
    [...searchKeys.all, "artifacts", match, q] as const,
};

export const formatKeys = {
  profiles: ["formats", "profiles"] as const,
};

export const publicKeys = {
  all: ["public"] as const,
  catalog: () => [...publicKeys.all, "catalog"] as const,
  artifacts: (repositoryId: string, query: string, match = "contains") =>
    [...publicKeys.all, "artifacts", repositoryId, query, match] as const,
  artifactLookup: (repositoryId: string, coordinate: string) =>
    [...publicKeys.all, "artifact-lookup", repositoryId, coordinate] as const,
};

/**
 * Reads of a format's own client protocol (npm packument, PyPI Simple JSON,
 * Go module proxy, Cargo sparse index), keyed by repository name and package.
 */
export const protocolKeys = {
  all: ["protocol"] as const,
  npmPackument: (repoName: string, packageName: string) =>
    [...protocolKeys.all, "npm", repoName, packageName] as const,
  pypiProject: (repoName: string, project: string) =>
    [...protocolKeys.all, "pypi", repoName, project] as const,
  goVersions: (repoName: string, modulePath: string) =>
    [...protocolKeys.all, "go", repoName, modulePath, "list"] as const,
  goInfo: (repoName: string, modulePath: string, version: string) =>
    [...protocolKeys.all, "go", repoName, modulePath, version, "info"] as const,
  goDigest: (repoName: string, modulePath: string, version: string) =>
    [
      ...protocolKeys.all,
      "go",
      repoName,
      modulePath,
      version,
      "digest",
    ] as const,
  cargoIndex: (repoName: string, crateName: string) =>
    [...protocolKeys.all, "cargo", repoName, crateName] as const,
};

/** Quota alert and email notification snapshots, one per panel or rule. */
export const quotaKeys = {
  all: ["quota"] as const,
  snapshot: (key: string) => [...quotaKeys.all, "snapshot", key] as const,
  /** Repository pages a rule editor loads after the panel's first page. */
  editorRepositories: (startToken: string) =>
    [...quotaKeys.all, "editor-repositories", startToken] as const,
  emailTarget: (targetId: string) =>
    [...quotaKeys.all, "email-target", targetId] as const,
};
