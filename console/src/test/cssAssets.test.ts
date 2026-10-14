import { existsSync, readdirSync, readFileSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { describe, expect, it } from "vitest";

function stylesheets(directory: string): string[] {
  return readdirSync(directory, { withFileTypes: true }).flatMap((entry) => {
    const path = join(directory, entry.name);
    if (entry.isDirectory()) return stylesheets(path);
    return entry.name.endsWith(".css") ? [path] : [];
  });
}

describe("stylesheet assets", () => {
  // Feature stylesheets resolve url() against their own folder, so a rule
  // moved between files must have its paths rewritten.
  it("point every relative url() at a file that exists", () => {
    const missing = stylesheets(resolve(process.cwd(), "src")).flatMap((file) =>
      [...readFileSync(file, "utf8").matchAll(/url\(["']?([^"')]+)["']?\)/g)]
        .map((match) => match[1])
        .filter((url) => url.startsWith("."))
        .filter((url) => !existsSync(resolve(dirname(file), url)))
        .map((url) => `${file}: ${url}`),
    );
    expect(missing).toEqual([]);
  });
});
