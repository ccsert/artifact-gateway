import type { NexusMigrationSchedule } from "../client";
import { nexusBatch } from "./nexusBatchFixtures";

export const nexusSchedule: NexusMigrationSchedule = {
  id: "00000000-0000-4000-8000-000000000030",
  version: "2",
  state: "waiting_capacity",
  templateBatchId: nexusBatch.id,
  sourceId: nexusBatch.sourceId,
  sourceVersion: nexusBatch.sourceVersion,
  mappings: nexusBatch.mappings,
  policyVersion: "native-v1",
  intervalSeconds: 3600,
  nextRunAt: "2026-10-22T10:00:00Z",
  lastSuccessfulAt: "2026-10-22T09:00:00Z",
  consumedObservedAt: nexusBatch.capacityEvidence.observedAt,
  capacityEvidence: null,
  activeBatchId: "",
  lastBatchId: nexusBatch.id,
  lastError: "capacity_evidence_refresh_required",
  dispatchAttempts: 0,
  createdAt: "2026-10-22T09:00:00Z",
  updatedAt: "2026-10-22T10:00:00Z",
};
