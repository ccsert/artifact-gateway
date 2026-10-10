import { describe, expect, it } from "vitest";
import { shouldRetry, unwrap } from "./client";

describe("unwrap", () => {
  it("returns data and rethrows the SDK error value unchanged", async () => {
    await expect(
      unwrap(Promise.resolve({ data: { id: "r1" } })),
    ).resolves.toEqual({ id: "r1" });
    const problem = { status: 404, code: "not_found", title: "missing" };
    await expect(unwrap(Promise.resolve({ error: problem }))).rejects.toBe(
      problem,
    );
    await expect(
      unwrap(Promise.resolve({ error: "404 page not found" })),
    ).rejects.toBe("404 page not found");
  });
});

describe("shouldRetry", () => {
  it("retries a network failure or a 5xx answer once", () => {
    expect(shouldRetry(0, new TypeError("Failed to fetch"))).toBe(true);
    expect(shouldRetry(0, { status: 503 })).toBe(true);
    expect(shouldRetry(1, { status: 503 })).toBe(false);
  });

  it("treats authorization, validation and not-found answers as final", () => {
    for (const status of [400, 401, 403, 404, 409, 422]) {
      expect(shouldRetry(0, { status })).toBe(false);
    }
    expect(shouldRetry(0, "404 page not found")).toBe(false);
  });
});
