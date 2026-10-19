import type {
  NexusMigrationBatch,
  NexusMigrationCapabilities,
  NexusMigrationEntry,
  Repository,
} from "../client";
import { migrationSource } from "./migrationFixtures";

export const nexusCapabilities: NexusMigrationCapabilities = {
  targetId: "gateway-object-target",
  poolId: "fixture-physical-pool",
  executionEnabled: true,
  capacityEvidenceSource: "operator-reported",
  policyVersion: "release-raw-v1",
  maxAttempts: 3,
};
export const nexusTargets: Repository[] = [
  {
    id: "00000000-0000-4000-8000-000000000010",
    name: "native-releases",
    format: "maven",
    type: "hosted",
    state: "active",
    version: "1",
    anonymousRead: false,
    mavenStrictPublication: true,
  },
  {
    id: "00000000-0000-4000-8000-000000000011",
    name: "native-raw",
    format: "raw",
    type: "hosted",
    state: "active",
    version: "1",
    anonymousRead: false,
    mavenStrictPublication: false,
  },
];
export const nexusBatch: NexusMigrationBatch = {
  id: "00000000-0000-4000-8000-000000000020",
  version: "7",
  sourceId: migrationSource.id,
  sourceVersion: "1",
  mappings: [
    { sourceRepository: "releases", targetRepositoryId: nexusTargets[0].id },
    { sourceRepository: "raw-source", targetRepositoryId: nexusTargets[1].id },
  ],
  policyVersion: "release-raw-v1",
  capacityEvidence: {
    targetId: nexusCapabilities.targetId,
    poolId: nexusCapabilities.poolId,
    observedAt: "2026-10-19T00:00:00Z",
    validUntil: "2026-10-19T00:30:00Z",
    freeBytes: 100_000_000,
    downloadBytes: 0,
    uploadBytes: 0,
    backupBytes: 0,
    restoreBytes: 0,
    headroomBytes: 1_000_000,
  },
  state: "completed",
  manifestSha256: "a".repeat(64),
  scanComplete: true,
  metadataDiscovered: 2,
  entryCount: 2,
  totalBytes: 80,
  sourceVerificationBytes: 40,
  transferredBytes: 120,
  verifiedBytes: 80,
  visibleIdentities: 2,
  counts: {
    pending: 0,
    uploaded: 0,
    verified: 0,
    committed: 2,
    skipped: 0,
    conflict: 0,
    failed: 0,
    unsupported: 0,
    sourceMissing: 0,
    sourceDeleted: 0,
  },
  lastError: "",
  createdAt: "2026-10-19T00:00:00Z",
  updatedAt: "2026-10-19T00:01:00Z",
};
export const nexusEntries: NexusMigrationEntry[] = [
  "releases",
  "raw-source",
].map((name, index) => ({
  id: `entry-${index}`,
  batchId: nexusBatch.id,
  sourceRepository: name,
  targetRepositoryId: nexusTargets[index].id,
  format: index === 0 ? "maven" : "raw",
  identity:
    index === 0
      ? "com.acme:library:1.0.0"
      : "exports/" + "long-source-path-".repeat(8) + "build+1.txt",
  state: "committed",
  attempts: 1,
  assets: [
    {
      sourcePath:
        index === 0
          ? "com/acme/library/1.0.0/library-1.0.0.pom"
          : "exports/build+1.txt",
      targetPath:
        index === 0
          ? "com/acme/library/1.0.0/library-1.0.0.pom"
          : "exports/build+1.txt",
      sha256: (index === 0 ? "b" : "c").repeat(64),
      size: 40,
      validator: "must-not-export-upstream-validator",
      uploaded: true,
      verified: true,
    },
  ],
  transferredBytes: 60,
  verifiedBytes: 40,
  visibleIdentities: 1,
  lastError: "",
  updatedAt: nexusBatch.updatedAt,
}));
