import { describe, expect, it } from "vitest";
import type { RuntimeLogEntry } from "../../client";
import {
  boundRuntimeLogs,
  logNDJSON,
  logText,
  MAX_LOG_BYTES,
  MAX_LOG_RECORDS,
  projectLog,
} from "./runtimeLogView";

const entry = (
  sequence: number,
  message = `event-${sequence}`,
): RuntimeLogEntry => ({
  sequence,
  time: "2026-10-03T00:00:00Z",
  level: "INFO",
  message,
  instanceId: "synthetic-node",
  sessionId: "synthetic-session",
  component: "http",
  operation: "http.request",
  requestId: "request",
  traceId: "trace",
  status: 200,
  method: "GET",
  durationMs: 12,
});

describe("bounded runtime log view", () => {
  it("preserves the server route template in rows and NDJSON", () => {
    const source = {
      ...entry(1),
      route: "GET /api/v2/repositories/{repositoryId}",
    };
    const rows = boundRuntimeLogs(
      [source],
      source.instanceId,
      source.sessionId,
    ).entries;
    expect(JSON.parse(logNDJSON(rows))).toMatchObject({ route: source.route });
  });
  it("merges concurrent pages in sequence order, deduplicating only inside the current scope", () => {
    const result = boundRuntimeLogs(
      [entry(3), entry(1), entry(2), entry(2)],
      "synthetic-node",
      "synthetic-session",
    );
    expect(result.entries.map((item) => item.sequence)).toEqual([1, 2, 3]);
    expect(result.dropped).toBe(0);
    expect(
      boundRuntimeLogs(
        [{ ...entry(4), sessionId: "foreign" }],
        "synthetic-node",
        "synthetic-session",
      ).entries,
    ).toEqual([]);
  });

  it("discloses local record/byte trimming without inventing server retention loss", () => {
    const rows = Array.from({ length: MAX_LOG_RECORDS + 130 }, (_, i) =>
      entry(i + 1),
    );
    const result = boundRuntimeLogs(
      rows,
      "synthetic-node",
      "synthetic-session",
    );
    expect(result.entries).toHaveLength(MAX_LOG_RECORDS);
    expect(result.entries[0].sequence).toBe(131);
    expect(result.dropped).toBe(130);
    const large = boundRuntimeLogs(
      rows.map((row) => ({ ...row, message: "x".repeat(16_384) })),
      "synthetic-node",
      "synthetic-session",
    );
    expect(large.bytes).toBeLessThanOrEqual(MAX_LOG_BYTES);
    expect(new TextEncoder().encode(logNDJSON(large.entries)).byteLength).toBe(
      large.bytes,
    );
    expect(large.dropped).toBeGreaterThan(130);
    const unicode = boundRuntimeLogs(
      rows.map((row) => ({ ...row, message: "备份".repeat(5000) })),
      "synthetic-node",
      "synthetic-session",
    );
    expect(unicode.bytes).toBeLessThanOrEqual(MAX_LOG_BYTES);
    expect(
      new TextEncoder().encode(logNDJSON(unicode.entries)).byteLength,
    ).toBe(unicode.bytes);
    expect(unicode.entries.length).toBeLessThan(large.entries.length);
  });

  it("exports only bounded safe contract fields, preserves ordinary diagnostics and renders controls literally", () => {
    const source = {
      ...entry(1, "<img src=x onerror=alert(1)>\u001b[31m\nline two"),
      rawAttrs: { authorization: "synthetic-private-marker" },
      headers: { cookie: "synthetic-private-marker" },
    };
    const output = logNDJSON([projectLog(source)]);
    expect(output).not.toContain("synthetic-private-marker");
    expect(
      JSON.stringify(
        boundRuntimeLogs([source], source.instanceId, source.sessionId).entries,
      ),
    ).not.toContain("synthetic-private-marker");
    expect(JSON.parse(output)).toMatchObject({
      status: 200,
      durationMs: 12,
      method: "GET",
      message: "<img src=x onerror=alert(1)>\\u001b[31m\nline two",
    });
    expect(logText("a\u001b[0m\u202eb")).toBe("a\\u001b[0m\\u202eb");
    expect(logText("x".repeat(10_000), 4096).length).toBeLessThanOrEqual(4097);
  });
});
