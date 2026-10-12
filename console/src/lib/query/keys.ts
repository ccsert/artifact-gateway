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
  scanJobs: (id: string) =>
    [...repositoryKeys.detail(id), "scan-jobs"] as const,
  replicationPlans: (id: string) =>
    [...repositoryKeys.detail(id), "replication-plans"] as const,
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

export const formatKeys = {
  profiles: ["formats", "profiles"] as const,
};

export const publicKeys = {
  all: ["public"] as const,
  catalog: () => [...publicKeys.all, "catalog"] as const,
  artifacts: (repositoryId: string, query: string) =>
    [...publicKeys.all, "artifacts", repositoryId, query] as const,
  artifactLookup: (repositoryId: string, coordinate: string) =>
    [...publicKeys.all, "artifact-lookup", repositoryId, coordinate] as const,
};
