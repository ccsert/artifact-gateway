import { execFileSync } from "node:child_process";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";

interface Read {
  file: string;
  line: number;
  call: string;
  where: "query" | "other";
  function: string;
}

/**
 * Gateway reads that run outside a query, by file and enclosing function,
 * with the reason each stays (issue 284). Every other read must run in a
 * queryFn or in code a queryFn reaches.
 */
const exceptions: Record<string, string> = {
  // Documented exceptions (src/lib/query/README.md).
  "lib/auth.tsx#AuthProvider":
    "the session check sits above the query provider and decides its identity",
  "features/repository/RepositoryBrowseTree.tsx#RepositoryBrowseTree":
    "the tree loads one node page at a time with retry and de-duplication",
  "features/operations/useRuntimeLogStream.ts#useRuntimeLogStream":
    "a followed log stream with gap detection is not a cached read",
  // Reads that are one step of an action the person started.
  "features/artifact-detail/MavenArtifactDetail.tsx#deleteCurrent":
    "finds the artifact id right before deleting it",
  "features/repository/MavenPublishWizard.tsx#uploadAll":
    "checks the publish session while uploading",
  "features/artifact-detail/CargoCrateDetail.tsx#download":
    "downloads the .crate file",
  "features/repository/ProxyMavenWarmCard.tsx#warm":
    "fetches through the proxy to warm its cache",
  "features/repository/APTArchiveOperations.tsx#exportArchive":
    "downloads the snapshot archive",
  "features/access-control/ApplyTemplateDialog.tsx#apply":
    "reads current grants right before replacing them",
  "features/identity/users/UserRepositoryAccessPanel.tsx#readRepository":
    "reads current grants right before removing them",
  "features/auth/Login.tsx#submitToken":
    "verifies a pasted token while signing in",
};

describe("read inventory", () => {
  const reads = JSON.parse(
    execFileSync(
      "node",
      [resolve(process.cwd(), "scripts/read-inventory.mjs"), "--json"],
      { encoding: "utf8" },
    ),
  ) as Read[];

  it("runs every Gateway read in a query or a listed exception", () => {
    const unlisted = reads
      .filter((read) => read.where === "other")
      .filter((read) => !(`${read.file}#${read.function}` in exceptions))
      .map(
        (read) => `${read.file}:${read.line} ${read.call} in ${read.function}`,
      );
    expect(unlisted).toEqual([]);
  });

  it("lists only exceptions that still exist", () => {
    const present = new Set(
      reads
        .filter((read) => read.where === "other")
        .map((read) => `${read.file}#${read.function}`),
    );
    expect(Object.keys(exceptions).filter((key) => !present.has(key))).toEqual(
      [],
    );
  });

  it("finds the reads it is meant to check", () => {
    // A broken inventory would pass the checks above by finding nothing.
    expect(
      reads.filter((read) => read.where === "query").length,
    ).toBeGreaterThan(90);
  });
});
