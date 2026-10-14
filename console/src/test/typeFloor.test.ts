import { readdirSync, readFileSync } from "node:fs";
import { join, relative, resolve } from "node:path";
import { describe, expect, it } from "vitest";

function stylesheets(directory: string): string[] {
  return readdirSync(directory, { withFileTypes: true }).flatMap((entry) => {
    const path = join(directory, entry.name);
    if (entry.isDirectory()) return stylesheets(path);
    return entry.name.endsWith(".css") ? [path] : [];
  });
}

// Glyphs that carry no text a reader must take in: keyboard key caps, the
// account menu caret and the tiny favicon preview.
const decorative = new Set([
  ".ag-command-trigger-kbd",
  ".ag-command-footer kbd",
  ".ag-account-caret",
  ".ag-site-preview-favicon",
]);

describe("type floor", () => {
  // DESIGN.md: operational text is 12px or larger.
  it("keeps readable text at 12px or more", () => {
    const root = resolve(process.cwd(), "src");
    const small: string[] = [];
    for (const file of stylesheets(root)) {
      // Innermost blocks only: a rule body never contains braces.
      const css = readFileSync(file, "utf8").replace(/\/\*[\s\S]*?\*\//g, "");
      for (const [, selector, body] of css.matchAll(/([^{}]+)\{([^{}]*)\}/g)) {
        const px = /font-size:\s*(\d+(?:\.\d+)?)px/.exec(body);
        if (!px || Number(px[1]) >= 12) continue;
        const selectors = selector.split(",").map((part) => part.trim());
        if (selectors.every((part) => decorative.has(part))) continue;
        small.push(
          `${relative(root, file)}: ${selectors.join(", ")} ${px[1]}px`,
        );
      }
    }
    expect(small).toEqual([]);
  });

  it("keeps Tailwind text sizes off the 9–11px range", () => {
    const root = resolve(process.cwd(), "src");
    const files = (dir: string): string[] =>
      readdirSync(dir, { withFileTypes: true }).flatMap((entry) => {
        const path = join(dir, entry.name);
        if (entry.isDirectory()) return files(path);
        return /\.tsx$/.test(entry.name) ? [path] : [];
      });
    const tiny = files(root).filter((file) =>
      /text-\[(9|10|11)px\]/.test(readFileSync(file, "utf8")),
    );
    expect(tiny.map((file) => relative(root, file))).toEqual([]);
  });
});
