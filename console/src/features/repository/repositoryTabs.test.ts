import { expect, it } from "vitest";
import type { Repository } from "../../client";
import { FORMAT_DESCRIPTORS } from "../../formats/descriptors";
import { repositoryTabAvailable, TABS } from "./repositoryTabs";

it("offers a registered publish guide without changing a page's format list", () => {
  const original = FORMAT_DESCRIPTORS.raw;
  FORMAT_DESCRIPTORS.raw = { ...original, publishGuide: true };
  const repository: Repository = {
    id: "raw",
    name: "raw",
    format: "raw",
    type: "hosted",
    anonymousRead: false,
    mavenStrictPublication: false,
    state: "active",
    version: "1",
  };
  const publish = TABS.find((tab) => tab.key === "publish")!;
  try {
    expect(repositoryTabAvailable(publish, repository)).toBe(true);
    expect(
      repositoryTabAvailable(publish, { ...repository, type: "proxy" }),
    ).toBe(false);
  } finally {
    FORMAT_DESCRIPTORS.raw = original;
  }
});
