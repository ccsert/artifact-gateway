import { readdirSync, readFileSync } from "node:fs";
import { join, resolve } from "node:path";
import { describe, expect, it } from "vitest";
import { defaultConsoleThemes, resolveConsoleTheme } from "./consoleTheme";

const styles = readFileSync(resolve(process.cwd(), "src/styles.css"), "utf8");
const runtimeStyles = styles.slice(styles.indexOf("html {"));

function applicationSources(directory: string): string[] {
  return readdirSync(directory, { withFileTypes: true }).flatMap((entry) => {
    const path = join(directory, entry.name);
    if (entry.isDirectory()) {
      if (entry.name === "client" || path.endsWith(join("lib", "theme"))) {
        return [];
      }
      return applicationSources(path);
    }
    if (!entry.name.match(/\.(?:ts|tsx)$/u) || entry.name.includes(".test.")) {
      return [];
    }
    return [path];
  });
}

describe("semantic theme CSS contract", () => {
  it("bootstraps every variable produced by the runtime projection", () => {
    const variables = Object.keys(
      resolveConsoleTheme(defaultConsoleThemes[0]).cssVariables,
    );

    for (const variable of variables) {
      expect(styles).toContain(`${variable}:`);
    }
  });

  it("keeps route and component styles free of literal colors", () => {
    expect(runtimeStyles).not.toMatch(/#[0-9a-f]{3,8}\b/iu);
    expect(runtimeStyles).not.toMatch(/rgba?\(/iu);
  });

  it("keeps application components free of literal colors", () => {
    for (const path of applicationSources(resolve(process.cwd(), "src"))) {
      const source = readFileSync(path, "utf8");
      expect(source, path).not.toMatch(/#[0-9a-f]{3,8}\b/iu);
      expect(source, path).not.toMatch(/rgba?\(/iu);
      expect(source, path).not.toMatch(
        /\b(?:text|bg|border)-(?:red|rose|orange|amber|yellow|lime|green|emerald|cyan|sky|blue|indigo|violet|purple|fuchsia|pink)-/iu,
      );
      expect(source, path).not.toMatch(
        /["'](?:red|rose|orange|amber|yellow|lime|green|emerald|cyan|sky|blue|indigo|violet|purple|fuchsia|pink|magenta|gold|volcano|geekblue)["']/iu,
      );
    }
  });

  it("uses neutral roles for native selection and budgets primary action use", () => {
    const selectionRule = runtimeStyles.match(/::selection\s*\{[^}]+\}/u)?.[0];

    expect(selectionRule).toContain("var(--ag-selection-foreground)");
    expect(selectionRule).toContain("var(--ag-selection-background)");
    expect(selectionRule).not.toContain("action-primary");

    const primaryUses = runtimeStyles.match(/var\(--ag-action-primary\)/gu);
    expect(primaryUses?.length ?? 0).toBeLessThanOrEqual(3);
  });

  it("reveals the new theme radially over a stable old snapshot", () => {
    expect(runtimeStyles).toMatch(
      /::view-transition-old\(root\)\s*\{\s*animation:\s*none;/u,
    );
    expect(runtimeStyles).toMatch(
      /::view-transition-new\(root\)\s*\{[^}]*ag-theme-radial-reveal\s+240ms/u,
    );
    expect(runtimeStyles).toContain("--ag-theme-reveal-x");
    expect(runtimeStyles).toContain("--ag-theme-reveal-y");
    expect(runtimeStyles).toContain("--ag-theme-reveal-radius");
    expect(runtimeStyles).toMatch(/clip-path:\s*circle\(/u);
    expect(runtimeStyles).not.toContain("ag-theme-fade-out");
    expect(runtimeStyles).not.toContain('data-theme-transition="fallback"');
  });

  it("does not consume the retired generic theme aliases", () => {
    const retired = [
      "--ag-bg",
      "--ag-surface",
      "--ag-surface-solid",
      "--ag-overlay",
      "--ag-brand",
      "--ag-brand-soft",
      "--ag-brand-glow",
      "--ag-text",
      "--ag-text-dim",
      "--ag-text-mute",
      "--ag-selection-bg",
      "--ag-selection-text",
      "--ag-shadow-card",
      "--ag-shadow-pop",
      "--ag-radius",
      "--ag-radius-sm",
    ];

    for (const variable of retired) {
      expect(runtimeStyles).not.toContain(`var(${variable})`);
    }
  });
});
