import type {
  MigrationSource,
  MigrationSourceDiscovery,
  MigrationSourceMappingPreview,
  MigrationSourcePreflight,
  MigrationSourceRepository,
} from "../client";

export const migrationSource: MigrationSource = {
  id: "2a5d97f8-f1db-4b87-8bba-4d84698a21c6",
  name: "Build Nexus",
  baseUrl: "https://nexus.example.com/nexus",
  kind: "nexus3",
  credentialsConfigured: true,
  version: "1",
  createdAt: "2026-10-19T00:00:00Z",
  updatedAt: "2026-10-19T00:00:00Z",
};
const repository = (
  name: string,
  overrides: Partial<MigrationSourceRepository> = {},
): MigrationSourceRepository => ({
  name,
  format: "maven2",
  type: "hosted",
  versionPolicy: "RELEASE",
  status: "supported",
  reason: "hosted_read_only_inventory",
  members: [],
  remoteUrl: "",
  fields: [],
  ...overrides,
});
export const migrationPreflight: MigrationSourcePreflight = {
  state: "complete",
  visibility: "credential_visible",
  sourceId: migrationSource.id,
  sourceVersion: migrationSource.version,
  repositories: [
    repository("releases"),
    repository("raw-source", { format: "raw", versionPolicy: "" }),
    repository("central", {
      type: "proxy",
      status: "preview_only",
      reason: "proxy_configuration_only",
      fields: [
        {
          field: "httpClient.authentication",
          disposition: "manual",
          reason: "manual_credentials",
        },
      ],
    }),
    repository("public", {
      type: "group",
      status: "preview_only",
      members: ["releases", "central"],
      reason: "group_configuration_only",
    }),
    repository("snapshots", {
      status: "unsupported",
      versionPolicy: "SNAPSHOT",
      reason: "maven_snapshot_unsupported",
    }),
    repository("docker-hosted", {
      format: "docker",
      status: "unsupported",
      versionPolicy: "",
      reason: "format_unsupported",
    }),
  ],
  stats: { requests: 7, pages: 1, responseBytes: 2000 },
};
export const migrationDiscovery: MigrationSourceDiscovery = {
  state: "complete",
  visibility: "credential_visible",
  sourceId: migrationSource.id,
  sourceVersion: migrationSource.version,
  repositories: migrationPreflight.repositories.slice(0, 2),
  assets: [
    {
      repository: "releases",
      path: "com/acme/library/1.0+build/library-1.0+build.jar",
      downloadUrl: `${migrationSource.baseUrl}/repository/releases/com/acme/library/1.0+build/library-1.0+build.jar`,
      size: 2048,
      checksum: { sha256: "a".repeat(64) },
    },
    {
      repository: "raw-source",
      path: "exports/" + "a-long-source-path-".repeat(8) + "build+1.txt",
      downloadUrl: `${migrationSource.baseUrl}/repository/raw-source/exports/build+1.txt`,
      size: 512,
      checksum: {},
    },
  ],
  unsupportedAssets: [
    {
      repository: "releases",
      path: "com/acme/library/maven-metadata.xml",
      reason: "maven_mixed_metadata_requires_regeneration",
    },
  ],
  stats: { requests: 5, pages: 2, responseBytes: 3000 },
};
export const migrationMapping: MigrationSourceMappingPreview = {
  state: "complete",
  sourceId: migrationSource.id,
  sourceVersion: migrationSource.version,
  valid: true,
  mappings: [
    {
      sourceRepository: "releases",
      targetName: "native-releases",
      status: "ready",
      reasons: [],
      targetMembers: [],
      fields: [],
    },
  ],
  stats: { requests: 3, pages: 1, responseBytes: 1500 },
};
