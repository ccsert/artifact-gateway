import { execFileSync } from "node:child_process";
import { mkdtempSync, mkdirSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { expect, it } from "vitest";

it("tracks SDK aliases and distinguishes query-only helpers from shared action helpers", () => {
  const root = mkdtempSync(join(tmpdir(), "read-inventory-"));
  try {
    mkdirSync(join(root, "src/client"), { recursive: true });
    writeFileSync(
      join(root, "tsconfig.app.json"),
      JSON.stringify({
        compilerOptions: {
          target: "ES2022",
          module: "ESNext",
          lib: ["ES2022", "DOM"],
        },
        include: ["src"],
      }),
    );
    writeFileSync(
      join(root, "src/client/sdk.gen.ts"),
      `
      declare const client: { get<T>(options: unknown): Promise<T> };
      export const listRepositories = () => (client).get<unknown>({});
    `,
    );
    writeFileSync(
      join(root, "src/fixture.ts"),
      `
      import { listRepositories as loadRepos } from "./client/sdk.gen";
      import * as sdk from "./client/sdk.gen";
      function alias() { return loadRepos(); }
      function namespace() { return sdk.listRepositories(); }
      const localRead = loadRepos;
      function localAlias() { return localRead(); }
      function shared() { return loadRepos(); }
      function queryOnly() { return loadRepos(); }
      function timerShared() { return loadRepos(); }
      function memberShared() { return loadRepos(); }
      const registry = { memberShared };
      const query = { queryFn: () => Promise.all([shared(), queryOnly(), timerShared(), registry.memberShared()]) };
      function action() { return shared(); }
      setTimeout(timerShared, 0);
      function memberAction() { return registry.memberShared(); }
      const sameName = () => undefined;
      function notAnSDKRead() { return sameName(); }
    `,
    );
    const rows = JSON.parse(
      execFileSync(
        "node",
        [resolve("scripts/read-inventory.mjs"), "--root", root, "--json"],
        { encoding: "utf8" },
      ),
    ) as { function: string; where: string }[];
    expect(rows.map((row) => [row.function, row.where])).toEqual([
      ["alias", "other"],
      ["namespace", "other"],
      ["localAlias", "other"],
      ["shared", "other"],
      ["queryOnly", "query"],
      ["timerShared", "other"],
      ["memberShared", "other"],
    ]);
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});
