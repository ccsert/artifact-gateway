import { readdirSync, readFileSync } from "node:fs";
import { join, relative, resolve } from "node:path";
import { describe, expect, it } from "vitest";

const root = resolve(process.cwd(), "src");

function sources(directory: string): string[] {
  return readdirSync(directory, { withFileTypes: true }).flatMap((entry) => {
    const path = join(directory, entry.name);
    if (entry.isDirectory())
      return entry.name === "client" ? [] : sources(path);
    return /\.tsx?$/.test(entry.name) && !/\.test\.tsx?$/.test(entry.name)
      ? [path]
      : [];
  });
}

// Files that may call `fetch` next to an effect, and why. Everything else
// reads through query hooks (src/lib/query/README.md).
const allowed: Record<string, string> = {
  "lib/auth.tsx":
    "the session check sits above the query provider and decides its identity",
  "features/auth/Login.tsx":
    "sign-in and password change are form submissions, not reads",
  "features/artifact-detail/CargoCrateDetail.tsx":
    "the .crate download runs from a button, not on render",
};

describe("data layer", () => {
  it("keeps raw fetch out of components that run effects", () => {
    const mixed = sources(root)
      .filter((file) => {
        const source = readFileSync(file, "utf8");
        return /\bfetch\(/.test(source) && /\buseEffect\b/.test(source);
      })
      .map((file) => relative(root, file))
      .filter((file) => !(file in allowed));
    expect(mixed).toEqual([]);
  });
});
