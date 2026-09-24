import { describe, expect, it } from "vitest";
import type { Repository } from "../../client";
import type { RepositoryPermissions } from "../../lib/permissions";
import { repositoryTabAvailable, TABS, type Tab } from "./repositoryTabs";

function repository(overrides: Partial<Repository> = {}): Repository {
  return {
    id: "repo-1",
    name: "releases",
    format: "maven",
    type: "hosted",
    anonymousRead: false,
    mavenStrictPublication: false,
    state: "active",
    version: "1",
    ...overrides,
  };
}

const granted: RepositoryPermissions = {
  read: { allowed: true },
  write: { allowed: true },
  admin: { allowed: true },
  intelligence: { allowed: true },
};

const readOnly: RepositoryPermissions = {
  read: { allowed: true },
  write: { allowed: false },
  admin: { allowed: false },
  intelligence: { allowed: false },
};

function availableKeys(
  repo: Repository,
  permissions?: RepositoryPermissions,
): Tab[] {
  return TABS.filter((item) =>
    repositoryTabAvailable(item, repo, permissions),
  ).map((item) => item.key);
}

describe("repositoryTabAvailable", () => {
  it("offers a read-only reader the artifact surfaces but no management tab", () => {
    const keys = availableKeys(repository(), readOnly);

    expect(keys).toEqual(["artifacts", "usage"]);
  });

  it("hides privileged tabs while effective permissions are unknown", () => {
    const keys = availableKeys(repository());

    expect(keys).toEqual(["artifacts", "usage"]);
  });

  it("offers every applicable tab once the repository grants them", () => {
    const keys = availableKeys(repository(), granted);

    expect(keys).toEqual([
      "artifacts",
      "usage",
      "publish",
      "grants",
      "retention",
      "scanning",
      "security",
      "capacity",
      "distribute",
      "jobs",
      "tombstones",
      "settings",
    ]);
  });

  it("keeps publish behind repository write permission", () => {
    const writable = { ...readOnly, write: { allowed: true } };

    expect(availableKeys(repository(), writable)).toContain("publish");
    expect(availableKeys(repository(), readOnly)).not.toContain("publish");
  });

  it("keeps scanning behind the intelligence scope", () => {
    const intelligence = { ...readOnly, intelligence: { allowed: true } };

    expect(availableKeys(repository(), intelligence)).toContain("scanning");
    expect(availableKeys(repository(), readOnly)).not.toContain("scanning");
  });

  it("never offers publish on a proxy regardless of permission", () => {
    expect(availableKeys(repository({ type: "proxy" }), granted)).not.toContain(
      "publish",
    );
  });
});
