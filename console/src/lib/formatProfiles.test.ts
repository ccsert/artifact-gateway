import { afterEach, describe, expect, it, vi } from "vitest";
import { listFormatProfiles } from "../client";
import {
  groupFormats,
  loadFormatProfiles,
  repositoryFormats,
  repositoryTypes,
  resetFormatProfilesCacheForTests,
} from "./formatProfiles";
import type { FormatProfile } from "../client";

vi.mock("../client", () => ({
  listFormatProfiles: vi.fn(),
}));

const mockListFormatProfiles = vi.mocked(listFormatProfiles);

const profiles: FormatProfile[] = [
  {
    format: "oci",
    repositoryTypes: ["hosted", "proxy"],
    groupSupported: true,
    anonymousRead: true,
    hostedOperations: ["read", "publish"],
    proxyOperations: ["read"],
  },
  {
    format: "raw",
    repositoryTypes: ["hosted"],
    groupSupported: false,
    anonymousRead: true,
    hostedOperations: ["read", "publish"],
    proxyOperations: [],
  },
  {
    format: "npm",
    repositoryTypes: ["hosted", "proxy"],
    groupSupported: true,
    anonymousRead: true,
    hostedOperations: ["read", "publish", "browse"],
    proxyOperations: ["read", "browse"],
  },
];

afterEach(() => {
  vi.clearAllMocks();
  resetFormatProfilesCacheForTests();
});

describe("format profiles", () => {
  it("loads the capability catalog once", async () => {
    mockListFormatProfiles.mockResolvedValue({
      data: { items: profiles },
    } as never);

    const [first, second] = await Promise.all([
      loadFormatProfiles(),
      loadFormatProfiles(),
    ]);

    expect(first).toEqual(profiles);
    expect(second).toEqual(profiles);
    expect(mockListFormatProfiles).toHaveBeenCalledTimes(1);
  });

  it("filters repository and group formats by declared support", () => {
    expect(repositoryFormats(profiles, "hosted")).toEqual([
      "oci",
      "raw",
      "npm",
    ]);
    expect(repositoryFormats(profiles, "proxy")).toEqual(["oci", "npm"]);
    expect(repositoryTypes(profiles)).toEqual(["hosted", "proxy"]);
    expect(groupFormats(profiles)).toEqual(["oci", "npm"]);
  });

  it("retries after a failed request", async () => {
    mockListFormatProfiles
      .mockResolvedValueOnce({ error: new Error("unavailable") } as never)
      .mockResolvedValueOnce({ data: { items: profiles } } as never);

    await expect(loadFormatProfiles()).rejects.toThrow("unavailable");
    await expect(loadFormatProfiles()).resolves.toEqual(profiles);
    expect(mockListFormatProfiles).toHaveBeenCalledTimes(2);
  });
});
