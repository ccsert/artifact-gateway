import { describe, expect, it } from "vitest";
import { createTranslator } from "../../messages/translate";
import {
  nexusBatch,
  nexusCapabilities,
  nexusEntries,
  nexusScanFailure,
} from "../../test/nexusBatchFixtures";
import {
  batchError,
  batchReason,
  batchRequest,
  canCancel,
  canPause,
  canResume,
  canRetry,
  capacityEvidence,
  emptyCapacity,
  inventoryComplete,
  observationReport,
  scanStage,
} from "./nexusBatchModel";

const now = Date.parse("2026-10-19T00:10:00Z");
const input = {
  ...emptyCapacity(),
  observedAt: "2026-10-19T00:05:00Z",
  validUntil: "2026-10-19T00:35:00Z",
  freeBytes: "1000000",
  downloadBytes: "0",
  uploadBytes: "0",
  backupBytes: "0",
  restoreBytes: "0",
  headroomBytes: "20",
};
describe("operator capacity evidence", () => {
  it("starts unknown and requires every explicitly observed byte count", () => {
    expect(() =>
      capacityEvidence(emptyCapacity(), nexusCapabilities, undefined, now),
    ).toThrow();
    expect(() =>
      capacityEvidence(
        { ...input, backupBytes: "" },
        nexusCapabilities,
        undefined,
        now,
      ),
    ).toThrow();
    expect(
      capacityEvidence(input, nexusCapabilities, undefined, now),
    ).toMatchObject({
      targetId: nexusCapabilities.targetId,
      poolId: nexusCapabilities.poolId,
      downloadBytes: 0,
      freeBytes: 1_000_000,
    });
  });
  it.each(["-1", "1.5", "1e3", "9007199254740992", "NaN", "0xff"])(
    "rejects an ambiguous or inexact byte count %s",
    (value) => {
      expect(() =>
        capacityEvidence(
          { ...input, freeBytes: value },
          nexusCapabilities,
          undefined,
          now,
        ),
      ).toThrow();
    },
  );
  it("rejects missing runtime binding, disabled execution, stale/future/long evidence and local times", () => {
    for (const binding of [
      undefined,
      { ...nexusCapabilities, executionEnabled: false },
      { ...nexusCapabilities, poolId: "" },
    ])
      expect(() => capacityEvidence(input, binding, undefined, now)).toThrow();
    for (const times of [
      { observedAt: "2026-10-19T00:05:00" },
      { observedAt: "2026-10-19T00:11:00Z" },
      { validUntil: "2026-10-19T00:09:00Z" },
      { validUntil: "2026-10-19T01:06:00Z" },
    ])
      expect(() =>
        capacityEvidence(
          { ...input, ...times },
          nexusCapabilities,
          undefined,
          now,
        ),
      ).toThrow();
  });
  it("requires a strictly newer observation for resume and retry", () => {
    expect(() =>
      capacityEvidence(input, nexusCapabilities, input.observedAt, now),
    ).toThrow();
    expect(
      capacityEvidence(input, nexusCapabilities, "2026-10-19T00:04:59Z", now),
    ).toBeDefined();
  });
});
describe("batch truth and safe reports", () => {
  it("does not infer inventory completeness from a completed state or a loaded first page", () => {
    expect(inventoryComplete(nexusBatch, [nexusEntries[0]], true)).toBe(false);
    expect(
      inventoryComplete(nexusBatch, [nexusEntries[0], nexusEntries[0]], false),
    ).toBe(false);
    expect(
      inventoryComplete(
        { ...nexusBatch, scanComplete: false },
        nexusEntries,
        false,
      ),
    ).toBe(false);
    expect(inventoryComplete(nexusBatch, nexusEntries, false)).toBe(true);
  });
  it("exports full hashes and separate counters, with unverified acceptance and no arbitrary error/validator text", () => {
    const batch = {
      ...nexusBatch,
      totalBytes: null,
      lastError: "password=secret",
    };
    const report = observationReport(batch, nexusEntries, false, []);
    expect(report).toMatchObject({
      totalBytes: null,
      inventoryComplete: false,
      sourceVerificationBytes: 40,
      transferredBytes: 120,
      verifiedBytes: 80,
      visibleIdentities: 2,
      coldClient: "unverified",
      finalCatchupAfterSourceFreeze: "unverified",
    });
    expect(report.entries[0].assets[0].sha256).toHaveLength(64);
    expect(JSON.stringify(report)).not.toMatch(/secret|validator/);
    const failed = observationReport(
      {
        ...batch,
        lastScanFailure: { ...nexusScanFailure, code: "password=secret" },
      },
      [],
      false,
      [],
    );
    expect(failed.lastScanFailure).toMatchObject({
      sourcePath: nexusScanFailure.sourcePath,
      code: "unclassified_failure",
    });
    expect(JSON.stringify(failed)).not.toContain("secret");
  });
  it("preserves bounded OCI and Conan diagnostics in the exported observation", () => {
    for (const code of [
      "oci_dependency_missing",
      "oci_media_type_unsupported",
      "oci_graph_limit_exceeded",
      "conan_revision_incomplete",
      "conan_parent_recipe_incomplete",
    ]) {
      const report = observationReport(
        {
          ...nexusBatch,
          lastScanFailure: {
            ...nexusScanFailure,
            stage: "oci_graph_validation",
            code,
          },
        },
        [],
        false,
        [],
      );
      expect(report.lastScanFailure).toMatchObject({
        stage: "oci_graph_validation",
        code,
      });
    }
    expect(scanStage("oci_graph_validation")).toBe("nexusBatch.scanGraph");
  });
  it("keeps npm scan failures bounded and localized in reports", () => {
    expect(scanStage("npm_packument_validation")).toBe("nexusBatch.scanNPM");
    const t = createTranslator("zh-CN");
    for (const code of [
      "npm_closure_incomplete",
      "npm_packument_invalid",
      "npm_component_identity_invalid",
      "npm_integrity_mismatch",
      "npm_tarball_identity_invalid",
    ]) {
      const report = observationReport(
        {
          ...nexusBatch,
          lastScanFailure: {
            ...nexusScanFailure,
            stage: "npm_packument_validation",
            code,
          },
        },
        [],
        false,
        [],
      );
      expect(report.lastScanFailure).toMatchObject({
        stage: "npm_packument_validation",
        code,
      });
      expect(batchReason(code, t)).toContain("npm");
      expect(JSON.stringify(report)).not.toContain("password");
    }
  });
  it("gates controls by server state and retryable counts rather than optimistic progress", () => {
    expect(canPause({ ...nexusBatch, state: "running" })).toBe(true);
    expect(canResume({ ...nexusBatch, state: "paused" })).toBe(true);
    expect(canRetry(nexusBatch)).toBe(false);
    expect(
      canRetry({ ...nexusBatch, counts: { ...nexusBatch.counts, failed: 1 } }),
    ).toBe(true);
    expect(canCancel(nexusBatch)).toBe(false);
    expect(
      canRetry({
        ...nexusBatch,
        state: "failed",
        scanComplete: false,
        entryCount: 0,
        counts: { ...nexusBatch.counts, committed: 0 },
      }),
    ).toBe(true);
  });
  it("sanitizes SDK/body/network errors and marks uncertain writes as unknown", async () => {
    expect(batchError({ status: 412, message: "password=hidden" }).key).toBe(
      "nexusBatch.versionConflict",
    );
    expect(batchError(new Error("password=hidden"), true).key).toBe(
      "nexusBatch.unknownOutcome",
    );
    await expect(
      batchRequest(
        Promise.resolve({
          error: { code: "capacity_unknown", message: "hidden" },
        }),
        true,
      ),
    ).rejects.toMatchObject({ key: "nexusBatch.capacityUnknown" });
    await expect(batchRequest(Promise.resolve({}))).rejects.toMatchObject({
      key: "nexusBatch.requestFailed",
    });
  });
  it("maps the actual source boundary codes without confusing changed asset bytes with a changed connection version", () => {
    const t = createTranslator("zh-CN");
    expect(batchReason("source_missing", t)).toBe(
      t("nexusBatch.sourceMissing"),
    );
    for (const code of [
      "source_changed",
      "source_download_failed",
      "source_invalid_response",
      "source_timeout",
      "source_tls_failed",
      "source_unreachable",
    ])
      expect(batchReason(code, t)).toBe(t("nexusBatch.sourceFailure"));
    expect(batchReason("source_version_changed", t)).toBe(
      t("nexusBatch.sourceChanged"),
    );
  });
});
