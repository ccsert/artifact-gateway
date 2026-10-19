import type {
  NexusMigrationBatch,
  NexusMigrationCapacityEvidence,
  NexusMigrationCapabilities,
  NexusMigrationEntry,
  NexusMigrationMapping,
} from "../../client";
import { LocalizedError, type Translator } from "../../messages/translate";
import type { NexusBatchMessageKey } from "../../messages/nexus-batch.zh-CN";

export type BatchDraft = {
  sourceId: string;
  mappings: (Partial<NexusMigrationMapping> & {
    sourceRepository: string;
    targetName?: string;
  })[];
};
export const capacityFields = [
  "freeBytes",
  "downloadBytes",
  "uploadBytes",
  "backupBytes",
  "restoreBytes",
  "headroomBytes",
] as const;
export type CapacityInput = Record<
  (typeof capacityFields)[number] | "observedAt" | "validUntil",
  string
>;
export const emptyCapacity = (): CapacityInput => ({
  observedAt: "",
  validUntil: "",
  freeBytes: "",
  downloadBytes: "",
  uploadBytes: "",
  backupBytes: "",
  restoreBytes: "",
  headroomBytes: "",
});

/** Never synthesize capacity from local disk space, an old observation, or a default zero. */
export function capacityEvidence(
  input: CapacityInput,
  binding: NexusMigrationCapabilities | undefined,
  previous?: string,
  now = Date.now(),
): NexusMigrationCapacityEvidence {
  if (!binding?.executionEnabled || !binding.targetId || !binding.poolId)
    throw new LocalizedError("nexusBatch.disabled");
  if (
    capacityFields.some((key) => !input[key].trim()) ||
    !input.observedAt.trim() ||
    !input.validUntil.trim()
  )
    throw new LocalizedError("nexusBatch.required");
  const bytes = Object.fromEntries(
    capacityFields.map((key) => {
      const raw = input[key].trim();
      const value = Number(raw);
      if (!/^\d+$/.test(raw) || !Number.isSafeInteger(value) || value < 0)
        throw new LocalizedError("nexusBatch.invalidNumber");
      return [key, value];
    }),
  ) as Record<(typeof capacityFields)[number], number>;
  const iso =
    /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})$/;
  const observed = Date.parse(input.observedAt);
  const until = Date.parse(input.validUntil);
  if (
    !iso.test(input.observedAt) ||
    !iso.test(input.validUntil) ||
    !Number.isFinite(observed) ||
    !Number.isFinite(until)
  )
    throw new LocalizedError("nexusBatch.invalidTime");
  if (
    observed > now ||
    until <= now ||
    until <= observed ||
    until - observed > 3_600_000
  )
    throw new LocalizedError("nexusBatch.expired");
  if (previous && observed <= Date.parse(previous))
    throw new LocalizedError("nexusBatch.newerEvidence");
  return {
    targetId: binding.targetId,
    poolId: binding.poolId,
    observedAt: input.observedAt,
    validUntil: input.validUntil,
    ...bytes,
  };
}

const codes: Record<string, NexusBatchMessageKey> = {
  version_conflict: "nexusBatch.versionConflict",
  idempotency_conflict: "nexusBatch.idempotencyConflict",
  capacity_unknown: "nexusBatch.capacityUnknown",
  capacity_unavailable: "nexusBatch.capacityUnknown",
  capacity_inventory_mismatch: "nexusBatch.capacityInsufficient",
  capacity_insufficient: "nexusBatch.capacityInsufficient",
  capacity_evidence_expired: "nexusBatch.expired",
  capacity_evidence_refresh_required: "nexusBatch.newerEvidence",
  capacity_binding_mismatch: "nexusBatch.capacityBinding",
  capacity_target_mismatch: "nexusBatch.capacityBinding",
  target_quota_exceeded: "nexusBatch.capacityInsufficient",
  source_changed: "nexusBatch.sourceChanged",
  source_version_changed: "nexusBatch.sourceChanged",
  migration_execution_disabled: "nexusBatch.disabled",
  invalid_state: "nexusBatch.invalidState",
  attempts_exhausted: "nexusBatch.invalidState",
  target_conflict: "nexusBatch.targetConflict",
  native_session_conflict: "nexusBatch.targetConflict",
  source_bytes_mismatch: "nexusBatch.sourceFailure",
  target_bytes_mismatch: "nexusBatch.sourceFailure",
};
export function batchReason(code: string, t: Translator): string {
  return t(
    Object.hasOwn(codes, code) ? codes[code] : "nexusBatch.unsupportedReason",
  );
}
/** Upstream messages can contain URLs/credentials; only bounded codes enter the view/cache. */
export function batchError(error: unknown, writing = false): LocalizedError {
  if (error instanceof LocalizedError) return error;
  const problem = error as { status?: unknown; code?: unknown } | null;
  const status =
    typeof problem?.status === "number" ? problem.status : undefined;
  const key =
    status === 401 || status === 403
      ? "migration.denied"
      : status === 404
        ? "migration.notFound"
        : status === 412
          ? "nexusBatch.versionConflict"
          : typeof problem?.code === "string" &&
              Object.hasOwn(codes, problem.code)
            ? codes[problem.code]
            : writing && (status === undefined || status >= 500)
              ? "nexusBatch.unknownOutcome"
              : "nexusBatch.requestFailed";
  return new LocalizedError(key, undefined, { status });
}
export function batchReadDenied(error: unknown) {
  const status = (error as { status?: unknown } | null)?.status;
  return status === 401 || status === 403;
}
export async function batchRequest<T>(
  request: Promise<{ data?: T; error?: unknown }>,
  writing = false,
): Promise<T> {
  try {
    const result = await request;
    if (result.error != null) throw result.error;
    if (result.data == null) throw new Error("missing_response");
    return result.data;
  } catch (error) {
    throw batchError(error, writing);
  }
}
export const batchActive = (batch: NexusMigrationBatch) =>
  ["scanning", "ready", "running", "pausing"].includes(batch.state);
export const canPause = (batch: NexusMigrationBatch) =>
  ["scanning", "ready", "running"].includes(batch.state);
export const canResume = (batch: NexusMigrationBatch) =>
  ["paused", "blocked"].includes(batch.state);
export const canRetry = (batch: NexusMigrationBatch) =>
  ["failed", "completed", "blocked"].includes(batch.state) &&
  batch.counts.failed + batch.counts.sourceMissing > 0;
export const canCancel = (batch: NexusMigrationBatch) =>
  !["completed", "cancelled"].includes(batch.state);
export const hasExceptions = (batch: NexusMigrationBatch) =>
  batch.counts.conflict +
    batch.counts.failed +
    batch.counts.unsupported +
    batch.counts.sourceMissing +
    batch.counts.sourceDeleted >
  0;
export function entryState(
  state: NexusMigrationEntry["state"],
): NexusBatchMessageKey {
  const keys: Record<NexusMigrationEntry["state"], NexusBatchMessageKey> = {
    pending: "nexusBatch.pending",
    uploaded: "nexusBatch.uploaded",
    verified: "nexusBatch.entryVerified",
    committed: "nexusBatch.committed",
    skipped: "nexusBatch.skipped",
    conflict: "nexusBatch.conflict",
    failed: "nexusBatch.entryFailed",
    unsupported: "nexusBatch.unsupported",
    source_missing: "nexusBatch.sourceMissing",
    source_deleted: "nexusBatch.sourceDeleted",
  };
  return keys[state];
}
export function inventoryComplete(
  batch: NexusMigrationBatch,
  entries: NexusMigrationEntry[],
  hasMore: boolean,
) {
  return (
    batch.scanComplete &&
    !hasMore &&
    entries.length === batch.entryCount &&
    new Set(entries.map((entry) => entry.id)).size === entries.length
  );
}

/** Explicit projection excludes source secrets, validators, and arbitrary lastError text. */
export function observationReport(
  batch: NexusMigrationBatch,
  entries: NexusMigrationEntry[],
  complete: boolean,
  endpoints: {
    repositoryId: string;
    name: string;
    format: string;
    url: string;
  }[],
) {
  return {
    reportKind: "nexus-migration-observation",
    observedAt: new Date().toISOString(),
    batchId: batch.id,
    batchVersion: batch.version,
    sourceId: batch.sourceId,
    sourceVersion: batch.sourceVersion,
    policyVersion: batch.policyVersion,
    mappings: batch.mappings,
    state: batch.state,
    updatedAt: batch.updatedAt,
    scanComplete: batch.scanComplete,
    inventoryComplete: complete,
    loadedEntries: entries.length,
    entryCount: batch.entryCount,
    metadataDiscovered: batch.metadataDiscovered,
    manifestSha256: batch.manifestSha256,
    totalBytes: batch.totalBytes,
    sourceVerificationBytes: batch.sourceVerificationBytes,
    transferredBytes: batch.transferredBytes,
    verifiedBytes: batch.verifiedBytes,
    visibleIdentities: batch.visibleIdentities,
    counts: batch.counts,
    entries: entries.map((entry) => ({
      id: entry.id,
      sourceRepository: entry.sourceRepository,
      targetRepositoryId: entry.targetRepositoryId,
      format: entry.format,
      identity: entry.identity,
      state: entry.state,
      attempts: entry.attempts,
      updatedAt: entry.updatedAt,
      assets: entry.assets.map(
        ({ sourcePath, targetPath, size, sha256, uploaded, verified }) => ({
          sourcePath,
          targetPath,
          size,
          sha256,
          uploaded,
          verified,
        }),
      ),
    })),
    endpoints,
    coldClient: "unverified",
    finalCatchupAfterSourceFreeze: "unverified",
    productionCutover: "unverified",
    remainingDependencies: [
      "proxy-and-group-configuration",
      "unsupported-formats-and-snapshots",
      "permissions-and-credentials",
      "ci-and-publisher-endpoints",
      "source-freeze-and-final-rescan",
      "target-only-cold-client",
      "rollback-new-write-reconciliation",
    ],
  };
}
