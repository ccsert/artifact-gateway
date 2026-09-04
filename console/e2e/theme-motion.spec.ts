import { expect, test, type Page } from "@playwright/test";
import {
  defaultConsoleThemes,
  resolveConsoleTheme,
} from "../src/lib/consoleTheme";

async function mockThemeSurface(page: Page) {
  await page.route("**/api/v2/site-settings", (route) =>
    route.fulfill({
      json: {
        version: "1",
        siteName: "Artifact Gateway",
        logoUrl: "",
        brandMark: "AG",
        enabledThemeIds: defaultConsoleThemes.map((theme) => theme.id),
        defaultThemeId: "gateway-dark",
        availableThemes: defaultConsoleThemes,
        updatedAt: "2026-08-27T00:00:00Z",
      },
    }),
  );
  await page.route("**/auth/session", (route) =>
    route.fulfill({ json: { authenticated: false } }),
  );
  await page.route("**/auth/oidc/config", (route) =>
    route.fulfill({ json: { enabled: false } }),
  );
}

async function chooseThemeImmediately(page: Page, name: string) {
  await page.locator(".ag-theme-toggle").dispatchEvent("click");
  const item = page.getByRole("menuitem", { name: new RegExp(name, "u") });
  await item.waitFor({ state: "attached" });
  await item.dispatchEvent("click");
}

const normalizeValues = (values: Readonly<Record<string, string>>) =>
  Object.fromEntries(
    Object.entries(values).map(([name, value]) => [
      name,
      value.replace(/\s+/gu, " ").trim(),
    ]),
  );

test("reduced motion switches the complete palette atomically", async ({
  page,
}) => {
  await page.emulateMedia({ reducedMotion: "reduce" });
  await mockThemeSurface(page);
  await page.goto("/login");
  await page.getByPlaceholder("alice").fill("theme-motion-qa");
  await page.getByPlaceholder("••••••••").fill("local-only");
  const expected = resolveConsoleTheme(
    defaultConsoleThemes.find((theme) => theme.id === "gateway-light")!,
  ).cssVariables;
  await page.evaluate((variableNames) => {
    window.__themeMotionQA = { transitionCalls: 0, instantCommits: [] };
    Object.defineProperty(document, "startViewTransition", {
      configurable: true,
      value: (update: () => void | Promise<void>) => {
        window.__themeMotionQA.transitionCalls += 1;
        void update();
        return { finished: Promise.resolve(), skipTransition() {} };
      },
    });
    const root = document.documentElement;
    new MutationObserver(() => {
      if (root.dataset.themeTransition !== "instant") return;
      const button = document.querySelector<HTMLElement>(".ant-btn-primary");
      const buttonStyle = button ? getComputedStyle(button) : null;
      window.__themeMotionQA.instantCommits.push({
        themeId: root.dataset.themeId ?? null,
        variables: Object.fromEntries(
          variableNames.map((name) => [
            name,
            root.style.getPropertyValue(name),
          ]),
        ),
        primaryButtonBackground: buttonStyle?.backgroundColor ?? null,
        transitionProperty: buttonStyle?.transitionProperty ?? null,
        transitionDuration: buttonStyle?.transitionDuration ?? null,
      });
    }).observe(root, {
      attributes: true,
      attributeFilter: ["data-theme-id", "data-theme-transition"],
    });
  }, Object.keys(expected));

  await chooseThemeImmediately(page, "Gateway Light");
  await expect(page.locator("html")).toHaveAttribute(
    "data-theme-id",
    "gateway-light",
  );
  await expect
    .poll(() => page.locator("html").getAttribute("data-theme-transition"))
    .toBeNull();

  const trace = await page.evaluate(() => window.__themeMotionQA);
  expect(trace.transitionCalls).toBe(0);
  const commit = trace.instantCommits?.find(
    (candidate) => candidate.themeId === "gateway-light",
  );
  expect(commit).toBeDefined();
  expect(normalizeValues(commit!.variables)).toEqual(normalizeValues(expected));
  expect(commit!.primaryButtonBackground).toBe("rgb(8, 145, 178)");
  expect(commit!.transitionProperty).toBe("none");
  expect(commit!.transitionDuration).toBe("0s");
});

test("rapid theme choices cancel stale reveals and leave one complete contract", async ({
  page,
}) => {
  await page.emulateMedia({ reducedMotion: "no-preference" });
  await mockThemeSurface(page);
  await page.goto("/login");
  await page.evaluate(() => {
    window.__themeMotionQA = { transitionCalls: 0, skipCalls: 0 };
    Object.defineProperty(document, "startViewTransition", {
      configurable: true,
      value: (update: () => void | Promise<void>) => {
        window.__themeMotionQA.transitionCalls += 1;
        void Promise.resolve().then(update);
        let settled = false;
        let resolveFinished!: () => void;
        const finished = new Promise<void>((resolve) => {
          resolveFinished = resolve;
        });
        const finish = () => {
          if (settled) return;
          settled = true;
          resolveFinished();
        };
        window.__finishThemeTransitionQA = finish;
        return {
          finished,
          skipTransition: () => {
            window.__themeMotionQA.skipCalls += 1;
            finish();
          },
        };
      },
    });
  });

  // Dispatch directly so the next choice arrives while the previous browser
  // reveal still owns the document snapshot; actionability waits intentionally
  // wait behind that pseudo-element and would no longer exercise cancellation.
  await chooseThemeImmediately(page, "Gateway Light");
  await chooseThemeImmediately(page, "Aerok Dark");
  await chooseThemeImmediately(page, "Aerok Light");
  await expect(page.locator("html")).toHaveAttribute(
    "data-theme-id",
    "aerok-light",
  );
  await page.evaluate(() => window.__finishThemeTransitionQA?.());
  await expect
    .poll(() => page.locator("html").getAttribute("data-theme-transition"))
    .toBeNull();

  const expected = resolveConsoleTheme(
    defaultConsoleThemes.find((theme) => theme.id === "aerok-light")!,
  ).cssVariables;
  const actual = await page.locator("html").evaluate((root, names) => {
    const element = root as HTMLElement;
    return Object.fromEntries(
      names.map((name) => [name, element.style.getPropertyValue(name)]),
    );
  }, Object.keys(expected));
  const trace = await page.evaluate(() => window.__themeMotionQA);
  const revealState = await page.locator("html").evaluate((root) => ({
    x: (root as HTMLElement).style.getPropertyValue("--ag-theme-reveal-x"),
    y: (root as HTMLElement).style.getPropertyValue("--ag-theme-reveal-y"),
    radius: (root as HTMLElement).style.getPropertyValue(
      "--ag-theme-reveal-radius",
    ),
  }));
  expect(trace.transitionCalls).toBe(3);
  expect(trace.skipCalls).toBeGreaterThanOrEqual(2);
  expect(normalizeValues(actual)).toEqual(normalizeValues(expected));
  expect(revealState).toEqual({ x: "", y: "", radius: "" });
});

declare global {
  interface Window {
    __themeMotionQA: {
      transitionCalls: number;
      skipCalls?: number;
      instantCommits?: Array<{
        themeId: string | null;
        variables: Record<string, string>;
        primaryButtonBackground: string | null;
        transitionProperty: string | null;
        transitionDuration: string | null;
      }>;
    };
    __finishThemeTransitionQA?: () => void;
  }
}
