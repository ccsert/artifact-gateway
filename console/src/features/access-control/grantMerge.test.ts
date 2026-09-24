import { describe, expect, it } from "vitest";
import type { Grant } from "../../client";
import {
  normalizeResourcePrefix,
  removePrincipalGrants,
  replaceGrantEntry,
  upsertGrant,
} from "./grantMerge";

function grant(overrides: Partial<Grant> = {}): Grant {
  return {
    principal: "user:alice",
    scopes: ["repositories:read"],
    ...overrides,
  };
}

const aliceRead = grant();
const aliceReleases = grant({
  scopes: ["repositories:write"],
  resourcePrefix: "releases/",
});
const aliceSnapshots = grant({
  scopes: ["repositories:read"],
  resourcePrefix: "snapshots/",
});
const aliceStable = grant({
  scopes: ["repositories:read"],
  resourcePrefix: "stable/",
});
const bobWrite = grant({
  principal: "user:bob",
  scopes: ["repositories:write"],
  resourcePrefix: "releases/",
});
const ciAdmin = grant({
  principal: "service-account:ci",
  scopes: ["repositories:admin"],
});

describe("normalizeResourcePrefix", () => {
  it("treats blank prefixes as repository-wide and trims the rest", () => {
    expect(normalizeResourcePrefix(undefined)).toBeUndefined();
    expect(normalizeResourcePrefix("")).toBeUndefined();
    expect(normalizeResourcePrefix("   ")).toBeUndefined();
    expect(normalizeResourcePrefix("  releases/  ")).toBe("releases/");
  });
});

describe("upsertGrant", () => {
  it("appends a grant for a principal that is not present", () => {
    const existing = [bobWrite, ciAdmin];
    const merged = upsertGrant(existing, "user:alice", {
      scopes: ["repositories:write"],
      resourcePrefix: "releases/",
    });

    expect(merged).toEqual([
      bobWrite,
      ciAdmin,
      {
        principal: "user:alice",
        scopes: ["repositories:write"],
        resourcePrefix: "releases/",
      },
    ]);
    expect(merged).toHaveLength(3);
    expect(merged[0]).toBe(bobWrite);
    expect(merged[1]).toBe(ciAdmin);
  });

  it("normalizes a blank prefix to repository-wide access", () => {
    const merged = upsertGrant([], "user:alice", {
      scopes: ["repositories:read"],
      resourcePrefix: "   ",
    });

    expect(merged).toEqual([
      {
        principal: "user:alice",
        scopes: ["repositories:read"],
        resourcePrefix: undefined,
      },
    ]);
  });

  it("matches a blank prefix to an existing repository-wide entry", () => {
    const existing = [aliceRead, ciAdmin, aliceReleases];
    const merged = upsertGrant(existing, "user:alice", {
      scopes: ["repositories:admin"],
      resourcePrefix: "   ",
    });

    expect(merged).toEqual([
      { principal: "user:alice", scopes: ["repositories:admin"] },
      ciAdmin,
      aliceReleases,
    ]);
    expect(merged[1]).toBe(ciAdmin);
    expect(merged[2]).toBe(aliceReleases);
    expect(
      merged.filter((item) => item.principal === "user:alice"),
    ).toHaveLength(2);
  });

  it("replaces the entry with the same principal and prefix in place", () => {
    const existing = [ciAdmin, aliceReleases, bobWrite];
    const merged = upsertGrant(existing, "user:alice", {
      scopes: ["repositories:admin"],
      resourcePrefix: "releases/",
    });

    expect(merged).toEqual([
      ciAdmin,
      {
        principal: "user:alice",
        scopes: ["repositories:admin"],
        resourcePrefix: "releases/",
      },
      bobWrite,
    ]);
    expect(merged).toHaveLength(3);
    expect(merged[0]).toBe(ciAdmin);
    expect(merged[2]).toBe(bobWrite);
  });

  it("upserting a new prefix keeps the principal's other prefixes byte-identical", () => {
    const existing = [aliceReleases, ciAdmin, aliceSnapshots];
    const merged = upsertGrant(existing, "user:alice", {
      scopes: ["repositories:admin"],
      resourcePrefix: "stable/",
    });

    expect(merged).toEqual([
      aliceReleases,
      ciAdmin,
      aliceSnapshots,
      {
        principal: "user:alice",
        scopes: ["repositories:admin"],
        resourcePrefix: "stable/",
      },
    ]);
    // The decisive guarantee: the same principal's other entries come back as
    // the very same objects. The old merge collapsed them into the new entry.
    expect(merged[0]).toBe(aliceReleases);
    expect(merged[1]).toBe(ciAdmin);
    expect(merged[2]).toBe(aliceSnapshots);
    expect(
      merged.filter((item) => item.principal === "user:alice"),
    ).toHaveLength(3);
  });

  it("trims the principal before matching and writing", () => {
    const merged = upsertGrant([aliceRead], "  user:alice ", {
      scopes: ["repositories:write"],
    });

    expect(merged).toEqual([
      { principal: "user:alice", scopes: ["repositories:write"] },
    ]);
    expect(merged).toHaveLength(1);
  });

  it("trims the prefix before matching an existing entry", () => {
    const merged = upsertGrant([aliceReleases], "user:alice", {
      scopes: ["repositories:admin"],
      resourcePrefix: " releases/ ",
    });

    expect(merged).toEqual([
      {
        principal: "user:alice",
        scopes: ["repositories:admin"],
        resourcePrefix: "releases/",
      },
    ]);
  });

  it("starts from an empty list", () => {
    expect(
      upsertGrant([], "user:alice", { scopes: ["repositories:intelligence"] }),
    ).toEqual([
      { principal: "user:alice", scopes: ["repositories:intelligence"] },
    ]);
  });

  it("does not mutate the input list or share the scopes array", () => {
    const existing = [aliceRead];
    const change = { scopes: ["repositories:write" as const] };
    const merged = upsertGrant(existing, "user:alice", change);

    expect(existing).toEqual([aliceRead]);
    expect(merged[0].scopes).not.toBe(change.scopes);
    merged[0].scopes.push("repositories:admin");
    expect(change.scopes).toEqual(["repositories:write"]);
  });
});

describe("replaceGrantEntry", () => {
  it("moves the acted-on entry when an edit changes its prefix", () => {
    const existing = [aliceReleases, ciAdmin, aliceSnapshots];
    const merged = replaceGrantEntry(existing, "user:alice", "releases/", {
      scopes: ["repositories:admin"],
      resourcePrefix: "stable/",
    });

    expect(merged).toEqual([
      {
        principal: "user:alice",
        scopes: ["repositories:admin"],
        resourcePrefix: "stable/",
      },
      ciAdmin,
      aliceSnapshots,
    ]);
    expect(merged[2]).toBe(aliceSnapshots);
    expect(
      merged.filter((item) => item.principal === "user:alice"),
    ).toHaveLength(2);
  });

  it("behaves like the upsert when the prefix did not change", () => {
    const existing = [aliceReleases, ciAdmin];
    const merged = replaceGrantEntry(existing, "user:alice", "releases/", {
      scopes: ["repositories:admin"],
      resourcePrefix: "releases/",
    });

    expect(merged).toEqual([
      {
        principal: "user:alice",
        scopes: ["repositories:admin"],
        resourcePrefix: "releases/",
      },
      ciAdmin,
    ]);
    expect(merged[1]).toBe(ciAdmin);
  });

  it("targets the repository-wide entry with an empty previous prefix", () => {
    const existing = [aliceReleases, aliceRead, ciAdmin];
    const merged = replaceGrantEntry(existing, "user:alice", "", {
      scopes: ["repositories:write"],
      resourcePrefix: "stable/",
    });

    expect(merged).toEqual([
      aliceReleases,
      {
        principal: "user:alice",
        scopes: ["repositories:write"],
        resourcePrefix: "stable/",
      },
      ciAdmin,
    ]);
    expect(merged[0]).toBe(aliceReleases);
    expect(merged[2]).toBe(ciAdmin);
  });

  it("adds the change when the acted-on entry no longer exists", () => {
    const existing = [ciAdmin];
    const merged = replaceGrantEntry(existing, "user:alice", "releases/", {
      scopes: ["repositories:read"],
      resourcePrefix: "snapshots/",
    });

    expect(merged).toEqual([
      ciAdmin,
      {
        principal: "user:alice",
        scopes: ["repositories:read"],
        resourcePrefix: "snapshots/",
      },
    ]);
    expect(merged[0]).toBe(ciAdmin);
  });

  it("drops an entry already stored under the new prefix instead of duplicating it", () => {
    const existing = [aliceReleases, aliceStable, ciAdmin];
    const merged = replaceGrantEntry(existing, "user:alice", "releases/", {
      scopes: ["repositories:admin"],
      resourcePrefix: "stable/",
    });

    expect(merged).toEqual([
      {
        principal: "user:alice",
        scopes: ["repositories:admin"],
        resourcePrefix: "stable/",
      },
      ciAdmin,
    ]);
    expect(merged[1]).toBe(ciAdmin);
  });
});

describe("removePrincipalGrants", () => {
  it("drops every entry of the principal whatever the prefix, and nothing else", () => {
    const existing = [
      aliceReleases,
      bobWrite,
      aliceRead,
      aliceSnapshots,
      ciAdmin,
    ];
    const remaining = removePrincipalGrants(existing, "user:alice");

    expect(remaining).toEqual([bobWrite, ciAdmin]);
    expect(remaining).toHaveLength(2);
    expect(remaining[0]).toBe(bobWrite);
    expect(remaining[1]).toBe(ciAdmin);
  });

  it("returns the same entries when the principal is absent", () => {
    const existing = [bobWrite, ciAdmin];
    expect(removePrincipalGrants(existing, "user:alice")).toEqual(existing);
  });

  it("trims the principal before matching", () => {
    const existing = [aliceReleases, bobWrite];
    expect(removePrincipalGrants(existing, "  user:alice ")).toEqual([
      bobWrite,
    ]);
  });

  it("does not mutate the input list", () => {
    const existing = [aliceReleases, bobWrite];
    removePrincipalGrants(existing, "user:alice");
    expect(existing).toEqual([aliceReleases, bobWrite]);
  });
});
