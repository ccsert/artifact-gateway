import type { Page } from "@playwright/test";

const identity = {
  actor: "ops-admin",
  kind: "local_session",
  role: "admin",
  administrator: true,
};

const themes = [
  {
    schemaVersion: 1,
    id: "gateway-dark",
    name: "Gateway Dark",
    description: "dark",
    mode: "dark",
    token: {
      colorPrimary: "#06B6D4",
      colorSuccess: "#34D399",
      colorWarning: "#FBBF24",
      colorError: "#FB7185",
      colorInfo: "#06B6D4",
      colorTextBase: "#E4E4E7",
      colorBgBase: "#08090B",
    },
  },
  {
    schemaVersion: 1,
    id: "gateway-light",
    name: "Gateway Light",
    description: "light",
    mode: "light",
    token: {
      colorPrimary: "#0891B2",
      colorSuccess: "#047857",
      colorWarning: "#A16207",
      colorError: "#BE123C",
      colorInfo: "#0891B2",
      colorTextBase: "#27272A",
      colorBgBase: "#F6F7F9",
    },
  },
];

const formatProfiles = [
  "oci",
  "raw",
  "maven",
  "conan",
  "npm",
  "pypi",
  "go",
  "apt",
].map((format) => ({
  format,
  repositoryTypes: ["hosted", "proxy"],
  groupSupported: true,
  anonymousRead: true,
  hostedOperations: ["read", "publish", "browse"],
  proxyOperations: ["read", "browse"],
}));

const repositories = [
  {
    id: "rep-oci-001",
    name: "docker-release",
    format: "oci",
    type: "hosted",
    endpoint: "/v2/docker-release",
    anonymousRead: true,
    mavenStrictPublication: false,
    state: "active",
    version: "1",
  },
  {
    id: "rep-mvn-002",
    name: "maven-internal",
    format: "maven",
    type: "hosted",
    endpoint: "/maven/maven-internal",
    anonymousRead: false,
    mavenStrictPublication: true,
    state: "active",
    version: "1",
  },
  {
    id: "rep-npm-003",
    name: "npm-mirror",
    format: "npm",
    type: "proxy",
    endpoint: "/npm/npm-mirror",
    anonymousRead: true,
    mavenStrictPublication: false,
    state: "active",
    version: "1",
    allowedHosts: ["registry.npmjs.org"],
  },
  {
    id: "rep-pypi-04",
    name: "pypi-mirror",
    format: "pypi",
    type: "proxy",
    endpoint: "/pypi/pypi-mirror",
    anonymousRead: true,
    mavenStrictPublication: false,
    state: "active",
    version: "1",
    allowedHosts: ["pypi.org"],
  },
  {
    id: "rep-go-005",
    name: "go-modules",
    format: "go",
    type: "hosted",
    endpoint: "/go/go-modules",
    anonymousRead: false,
    mavenStrictPublication: false,
    state: "active",
    version: "1",
  },
  {
    id: "rep-raw-006",
    name: "dist-raw",
    format: "raw",
    type: "hosted",
    endpoint: "/raw/dist-raw",
    anonymousRead: false,
    mavenStrictPublication: false,
    state: "active",
    version: "1",
  },
  {
    id: "rep-conan-07",
    name: "conan-center",
    format: "conan",
    type: "hosted",
    endpoint: "/conan/conan-center",
    anonymousRead: false,
    mavenStrictPublication: false,
    state: "deleting",
    version: "1",
  },
  {
    id: "rep-apt-008",
    name: "debian-apt",
    format: "apt",
    type: "proxy",
    endpoint: "/apt/debian-apt",
    anonymousRead: true,
    mavenStrictPublication: false,
    state: "active",
    version: "1",
    allowedHosts: ["deb.debian.org"],
  },
];

const capacities = repositories.map((repo, i) => ({
  repositoryId: repo.id,
  format: repo.format,
  usedBytes: [
    182_536_110_088, 24_118_882_304, 8_452_112, 51_539_607_552, 2_104_857_600,
    96_636_764_160, 1_073_741_824, 41_264_895_180,
  ][i],
  objectCount: [14_204, 8_912, 12_041, 96_233, 4_211, 3_120, 12, 44_120][i],
  quotaBytes: 0,
}));

const groups = [
  {
    id: "grp-001",
    name: "release-group",
    format: "maven",
    anonymousRead: true,
    members: [
      { repositoryId: "rep-mvn-002", position: 1 },
      { repositoryId: "rep-npm-003", position: 2 },
    ],
    version: "1",
  },
  {
    id: "grp-002",
    name: "docker-group",
    format: "oci",
    anonymousRead: false,
    members: [{ repositoryId: "rep-oci-001", position: 1 }],
    version: "1",
  },
];

const audits = [
  {
    occurredAt: "2026-08-29T09:12:04Z",
    operation: "get",
    outcome: "resolved",
    status: 200,
    actor: "ops-admin",
    resource: "docker-release/nginx:1.27",
    repository: "docker-release",
    requestId: "req-9f31c2ab",
  },
  {
    occurredAt: "2026-08-29T09:04:41Z",
    operation: "put",
    outcome: "resolved",
    status: 201,
    actor: "service:ci-runner",
    resource: "maven-internal/com/example/core:1.4.2",
    repository: "maven-internal",
    requestId: "req-77ad10e4",
  },
  {
    occurredAt: "2026-08-29T08:55:19Z",
    operation: "get",
    outcome: "failed",
    status: 403,
    actor: "anonymous",
    resource: "go-modules/golang.org/x/sys",
    repository: "go-modules",
    requestId: "req-2b8e5590",
  },
  {
    occurredAt: "2026-08-29T08:41:00Z",
    operation: "delete",
    outcome: "resolved",
    status: 202,
    actor: "ops-admin",
    resource: "conan-center/boost/1.85.0",
    repository: "conan-center",
    requestId: "req-51c0d3f7",
  },
  {
    occurredAt: "2026-08-29T08:12:33Z",
    operation: "get",
    outcome: "resolved",
    status: 200,
    actor: "anonymous",
    resource: "npm-mirror/react",
    repository: "npm-mirror",
    requestId: "req-c4d90a12",
  },
  {
    occurredAt: "2026-08-28T23:59:58Z",
    operation: "put",
    outcome: "failed",
    status: 500,
    actor: "service:release-bot",
    resource: "raw/dist-raw/installer-0.4.1.tar.gz",
    repository: "dist-raw",
    requestId: "req-00aa71bc",
  },
];

const users = [
  {
    id: "usr-001",
    name: "ops-admin",
    displayName: "Platform Admin",
    email: "admin@example.com",
    description: "Platform administrator",
    role: "admin",
    state: "active",
    lastLoginAt: "2026-08-29T09:00:00Z",
    passwordChangedAt: "2026-07-01T00:00:00Z",
    localPasswordEnabled: true,
    failedLoginAttempts: 0,
    mustChangePassword: false,
    createdAt: "2026-01-10T00:00:00Z",
    version: "3",
  },
  {
    id: "usr-002",
    name: "dev-lee",
    displayName: "Dev Lee",
    email: "lee@example.com",
    description: "Release engineer",
    role: "writer",
    state: "active",
    lastLoginAt: "2026-08-28T18:22:00Z",
    localPasswordEnabled: true,
    failedLoginAttempts: 0,
    mustChangePassword: false,
    createdAt: "2026-02-14T00:00:00Z",
    version: "1",
  },
  {
    id: "usr-003",
    name: "auditor",
    displayName: "Read Only Auditor",
    email: "audit@example.com",
    description: "",
    role: "reader",
    state: "disabled",
    localPasswordEnabled: false,
    failedLoginAttempts: 2,
    lockedUntil: "2026-08-29T12:00:00Z",
    mustChangePassword: false,
    createdAt: "2026-03-02T00:00:00Z",
    version: "2",
  },
];

const serviceAccounts = [
  {
    id: "svc-001",
    name: "ci-runner",
    description: "CI publish credentials",
    state: "active",
    createdAt: "2026-01-20T00:00:00Z",
    updatedAt: "2026-08-01T00:00:00Z",
    version: "1",
  },
  {
    id: "svc-002",
    name: "scanner-sync",
    description: "Scanner intelligence sync",
    state: "disabled",
    createdAt: "2026-03-11T00:00:00Z",
    updatedAt: "2026-07-15T00:00:00Z",
    version: "1",
  },
];

const apiKeys = [
  {
    id: "key-001",
    name: "release-key",
    roles: ["writer"],
    createdAt: "2026-05-01T00:00:00Z",
    expiresAt: "2026-11-01T00:00:00Z",
    lastUsedAt: "2026-08-29T08:30:00Z",
  },
  {
    id: "key-002",
    name: "readonly-key",
    roles: ["reader"],
    createdAt: "2026-06-15T00:00:00Z",
    expiresAt: "2027-06-15T00:00:00Z",
  },
  {
    id: "key-003",
    name: "legacy-key",
    roles: ["reader"],
    createdAt: "2025-12-01T00:00:00Z",
    expiresAt: "2026-02-01T00:00:00Z",
    revokedAt: "2026-07-01T00:00:00Z",
  },
];

const runtimeNodes = {
  items: [
    {
      instanceId: "gw-api-0",
      sessionId: "sess-a1b2",
      roles: ["api", "coordinator"],
      workerFormats: [],
      workerKinds: [],
      startedAt: "2026-08-27T06:00:00Z",
      lastSeenAt: "2026-08-29T09:14:00Z",
      status: "online",
    },
    {
      instanceId: "gw-worker-0",
      sessionId: "sess-c3d4",
      roles: ["worker"],
      workerFormats: ["oci", "maven"],
      workerKinds: ["retention", "replication"],
      startedAt: "2026-08-27T06:00:00Z",
      lastSeenAt: "2026-08-29T09:13:52Z",
      status: "online",
    },
    {
      instanceId: "gw-worker-1",
      sessionId: "sess-e5f6",
      roles: ["worker"],
      workerFormats: ["npm"],
      workerKinds: ["intelligence"],
      startedAt: "2026-08-25T06:00:00Z",
      lastSeenAt: "2026-08-29T07:44:00Z",
      status: "stale",
    },
  ],
  health: {
    status: "degraded",
    online: 2,
    stale: 1,
    offline: 0,
    issues: [
      {
        code: "node_stale",
        severity: "warning",
        message: "gw-worker-1 has not reported since 07:44 UTC",
      },
    ],
  },
};

const searchPage = {
  items: [
    {
      repositoryId: "rep-oci-001",
      repositoryName: "docker-release",
      repositoryFormat: "oci",
      artifactId: "art-1",
      name: "nginx",
      version: "1.27.4",
      digest:
        "sha256:4c0fdaa8b6341c1f2d0e1e3ed4a6c0a8bd52fa1a5d3f4f6d7e8b9c0a1b2c3d4e",
      size: 187_269_120,
      state: "visible",
      createdAt: "2026-08-20T10:00:00Z",
    },
    {
      repositoryId: "rep-mvn-002",
      repositoryName: "maven-internal",
      repositoryFormat: "maven",
      artifactId: "art-2",
      name: "com.example:core",
      version: "1.4.2",
      digest:
        "sha256:a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7e8f90",
      size: 2_411_829,
      state: "visible",
      createdAt: "2026-08-18T08:30:00Z",
    },
    {
      repositoryId: "rep-npm-003",
      repositoryName: "npm-mirror",
      repositoryFormat: "npm",
      artifactId: "art-3",
      name: "react",
      version: "19.1.0",
      digest:
        "sha256:0f1e2d3c4b5a69788796959493929190f1e2d3c4b5a69788796959493929190",
      size: 12_884_901,
      state: "quarantined",
      createdAt: "2026-08-10T12:00:00Z",
    },
  ],
  total: 3,
};

const browsePage = {
  items: [
    {
      id: "node-1",
      kind: "coordinate",
      name: "nginx",
      hasChildren: true,
      coordinate: "docker.io/library/nginx",
    },
    {
      id: "node-2",
      kind: "coordinate",
      name: "redis",
      hasChildren: true,
      coordinate: "docker.io/library/redis",
    },
    {
      id: "node-3",
      kind: "coordinate",
      name: "postgres",
      hasChildren: true,
      coordinate: "docker.io/library/postgres",
    },
  ],
};

export async function mockConsole(page: Page) {
  await page.addInitScript(() => {
    localStorage.setItem("ag.console.token", "mock-admin-token");
    localStorage.setItem("ag.console.role", "admin");
  });
  // Generic fallback for remaining management GETs.
  await page.route("**/api/v2/**", (route) => {
    if (route.request().method() !== "GET") {
      return route.fulfill({ status: 204 });
    }
    return route.fulfill({ json: { items: [] } });
  });
  await page.route("**/auth/session", (route) =>
    route.fulfill({ json: { authenticated: true, identity } }),
  );
  await page.route("**/api/v2/identity", (route) =>
    route.fulfill({ json: identity }),
  );
  await page.route("**/api/v2/site-settings", (route) =>
    route.fulfill({
      json: {
        version: "1",
        siteName: "Artifact Gateway",
        logoUrl: "",
        brandMark: "AG",
        enabledThemeIds: ["gateway-dark", "gateway-light"],
        defaultThemeId: "gateway-dark",
        availableThemes: themes,
        updatedAt: "2026-08-27T00:00:00Z",
      },
    }),
  );
  await page.route("**/api/v2/formats", (route) =>
    route.fulfill({ json: { items: formatProfiles } }),
  );
  await page.route("**/api/v2/repositories**", (route) => {
    const url = new URL(route.request().url());
    const path = url.pathname;
    if (path === "/api/v2/repositories") {
      return route.fulfill({ json: { items: repositories } });
    }
    const repo =
      repositories.find((r) => path.includes(r.id)) ?? repositories[0];
    if (path.endsWith(`/repositories/${repo.id}`)) {
      return route.fulfill({ json: repo });
    }
    if (path.endsWith("/browse")) {
      return route.fulfill({ json: browsePage });
    }
    if (path.endsWith("/capabilities")) {
      return route.fulfill({
        json: {
          format: repo.format,
          repositoryType: repo.type ?? "hosted",
          operations: ["read", "publish", "browse", "delete"],
        },
      });
    }
    if (path.endsWith("/effective-access")) {
      return route.fulfill({
        json: {
          actor: identity.actor,
          identity,
          resource: "",
          simulated: false,
          repository: {
            id: repo.id,
            name: repo.name,
            format: repo.format,
            type: repo.type ?? "hosted",
            state: repo.state,
          },
          anonymousRead: {
            allowed: false,
            source: "policy",
            reason: "disabled",
          },
          permissions: {
            read: { allowed: true, source: "role", reason: "administrator" },
            write: { allowed: true, source: "role", reason: "administrator" },
            admin: { allowed: true, source: "role", reason: "administrator" },
            intelligence: {
              allowed: true,
              source: "role",
              reason: "administrator",
            },
          },
        },
      });
    }
    if (path.endsWith("/capacity")) {
      return route.fulfill({
        json:
          capacities.find((c) => c.repositoryId === repo.id) ?? capacities[0],
      });
    }
    if (path.includes("/artifact-search") || path.endsWith("/artifacts")) {
      return route.fulfill({ json: searchPage });
    }
    return route.fulfill({ json: { items: [] } });
  });
  await page.route("**/api/v2/repository-capacities", (route) =>
    route.fulfill({ json: capacities }),
  );
  await page.route("**/api/v2/groups**", (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path === "/api/v2/groups") {
      return route.fulfill({ json: { items: groups } });
    }
    if (path.endsWith("/capacity")) {
      const group = groups.find((g) => path.includes(g.id)) ?? groups[0];
      return route.fulfill({
        json: {
          groupId: group.id,
          format: group.format,
          members: group.members.map((m, i) => ({
            position: m.position,
            repositoryId: m.repositoryId,
            format: "maven",
            usedBytes: 1_000_000_000 * (i + 1),
            objectCount: 100 * (i + 1),
          })),
        },
      });
    }
    return route.fulfill({
      json: groups.find((g) => path.includes(g.id)) ?? groups[0],
    });
  });
  await page.route("**/api/v2/audits**", (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path.endsWith("/audits/page")) {
      return route.fulfill({ json: { items: audits } });
    }
    return route.fulfill({ json: audits });
  });
  await page.route("**/api/v2/users**", (route) =>
    route.fulfill({
      json: {
        items: users,
        total: users.length,
        offset: 0,
        limit: 50,
      },
    }),
  );
  await page.route("**/api/v2/service-accounts**", (route) =>
    route.fulfill({ json: { items: serviceAccounts } }),
  );
  await page.route("**/api/v2/api-keys**", (route) =>
    route.fulfill({ json: { items: apiKeys } }),
  );
  await page.route("**/api/v2/runtime/nodes**", (route) =>
    route.fulfill({ json: runtimeNodes }),
  );
  await page.route("**/api/v2/diagnostics", (route) =>
    route.fulfill({
      json: {
        build: { version: "0.1.0", goVersion: "go1.24", revision: "db234fb1" },
        runtime: {
          instanceId: "gw-api-0",
          roles: ["api", "coordinator"],
          workerFormats: [],
          workerKinds: [],
        },
        dependencies: [
          { name: "postgres", status: "reachable" },
          { name: "object-store", status: "reachable" },
        ],
      },
    }),
  );
  await page.route("**/api/v2/artifact-search**", (route) =>
    route.fulfill({ json: searchPage }),
  );
  await page.route("**/api/v2/lifecycle-jobs**", (route) =>
    route.fulfill({
      json: [
        {
          repositoryId: "rep-oci-001",
          repositoryName: "docker-release",
          job: {
            id: "job-101",
            kind: "promotion",
            state: "running",
            createdAt: "2026-08-29T08:40:00Z",
            startedAt: "2026-08-29T08:41:00Z",
            attempts: 1,
            maxAttempts: 3,
            progressCurrent: 42,
            progressTotal: 100,
            progressMessage: "复制层 42/100",
          },
        },
        {
          repositoryId: "rep-mvn-002",
          repositoryName: "maven-internal",
          job: {
            id: "job-102",
            kind: "retention",
            state: "failed",
            createdAt: "2026-08-29T07:10:00Z",
            startedAt: "2026-08-29T07:10:10Z",
            completedAt: "2026-08-29T07:12:00Z",
            attempts: 3,
            maxAttempts: 3,
            progressCurrent: 0,
            progressTotal: 0,
            lastError: "storage backend timeout",
          },
        },
        {
          repositoryId: "rep-go-005",
          repositoryName: "go-modules",
          job: {
            id: "job-103",
            kind: "reclaim",
            state: "completed",
            createdAt: "2026-08-28T22:00:00Z",
            startedAt: "2026-08-28T22:00:05Z",
            completedAt: "2026-08-28T22:03:00Z",
            attempts: 1,
            maxAttempts: 3,
            progressCurrent: 12,
            progressTotal: 12,
          },
        },
      ],
    }),
  );
  await page.route("**/api/v2/scheduled-tasks**", (route) =>
    route.fulfill({
      json: [
        {
          id: "task-001",
          name: "docker-release retention",
          description: "每小时清理过期制品",
          kind: "repository-retention",
          repositoryId: "rep-oci-001",
          intervalMinutes: 60,
          enabled: true,
          nextRunAt: "2026-08-29T10:00:00Z",
          lastRunAt: "2026-08-29T09:00:00Z",
          lastRunState: "submitted",
          version: "1",
          createdAt: "2026-06-01T00:00:00Z",
          updatedAt: "2026-08-01T00:00:00Z",
        },
        {
          id: "task-002",
          name: "audit cleanup",
          description: "每日清理过期审计记录",
          kind: "audit-retention",
          intervalMinutes: 1440,
          enabled: false,
          nextRunAt: "2026-08-30T02:00:00Z",
          version: "1",
          createdAt: "2026-06-01T00:00:00Z",
          updatedAt: "2026-07-15T00:00:00Z",
        },
      ],
    }),
  );
  await page.route("**/api/v2/webhook-subscriptions**", (route) =>
    route.fulfill({ json: { items: [] } }),
  );
  await page.route("**/api/v2/webhook-deliveries**", (route) =>
    route.fulfill({ json: { items: [] } }),
  );
  await page.route("**/api/v2/authorization-roles**", (route) =>
    route.fulfill({
      json: {
        items: [
          {
            id: "role-001",
            name: "release-writer",
            description: "Publish to hosted repos",
            permissions: ["repository:write", "artifact:read"],
            builtin: false,
          },
        ],
      },
    }),
  );
  await page.route("**/api/v2/authorization-templates**", (route) =>
    route.fulfill({ json: { items: [] } }),
  );
  await page.route("**/api/v2/repository-grants**", (route) =>
    route.fulfill({
      json: [
        {
          principal: "service:ci-runner",
          scopes: ["repositories:write", "repositories:read"],
        },
        {
          principal: "service:scanner-sync",
          scopes: ["repositories:intelligence"],
        },
      ],
    }),
  );
  await page.route("**/api/v2/anonymous-access-policy", (route) =>
    route.fulfill({
      json: {
        enabled: false,
        allowProtocolReads: false,
        updatedAt: "2026-08-01T00:00:00Z",
        version: "1",
      },
    }),
  );
  await page.route("**/api/v2/authentication/oidc**", (route) =>
    route.fulfill({
      json: { enabled: false, issuerUrl: "", clientId: "" },
    }),
  );
  await page.route("**/api/v2/audit-retention-policy", (route) =>
    route.fulfill({
      json: { version: "2", enabled: true, keepDays: 90 },
    }),
  );
  await page.route("**/api/v2/audit-retention/jobs**", (route) =>
    route.fulfill({
      json: [
        {
          id: "acj-001",
          policyVersion: "2",
          cutoffAt: "2026-05-31T00:00:00Z",
          batchSize: 500,
          deleted: 1284,
          state: "completed",
          createdAt: "2026-08-29T02:00:00Z",
          startedAt: "2026-08-29T02:00:05Z",
          completedAt: "2026-08-29T02:04:00Z",
        },
      ],
    }),
  );
}
