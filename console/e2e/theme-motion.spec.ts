import { expect, test, type Page } from "@playwright/test";
import {
  defaultConsoleThemes,
  resolveConsoleTheme,
} from "../src/lib/consoleTheme";
import { defaultSiteSettings } from "../src/lib/siteSettings";
import { authenticateAsAdmin } from "./support/auth";
import {
  attachThemeEvidence,
  recordThemeEvidence,
} from "./support/themeEvidence";

test.beforeEach(async ({ page }) => recordThemeEvidence(page));
test.afterEach(async ({ page }, testInfo) =>
  attachThemeEvidence(page, testInfo),
);

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
  expect(commit!.primaryButtonBackground).toBe("rgb(8, 127, 156)");
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

for (const [lastChoice, expectedID] of [
  ["Aerok Dark", "aerok-dark"],
  ["Gateway Dark", "gateway-dark"],
] as const) {
  test(`a skipped theme snapshot cannot replace a newer choice of ${lastChoice}`, async ({
    page,
  }) => {
    await mockThemeSurface(page);
    await page.goto("/login");
    await page.evaluate(() => {
      const updates: Array<() => void | Promise<void>> = [];
      Object.defineProperty(document, "startViewTransition", {
        configurable: true,
        value: (update: () => void | Promise<void>) => {
          updates.push(update);
          return {
            finished: new Promise<void>(() => {}),
            skipTransition() {},
          };
        },
      });
      window.__runThemeSnapshotQA = async (index) => {
        await updates[index]();
      };
    });

    // skipTransition() still schedules the old update callback. Deliver the
    // callbacks in their native FIFO order after the newer choice is accepted.
    await chooseThemeImmediately(page, "Gateway Light");
    await chooseThemeImmediately(page, lastChoice);
    await page.evaluate(() => window.__runThemeSnapshotQA?.(0));
    await expect(page.locator("html")).toHaveAttribute(
      "data-theme-id",
      "gateway-dark",
    );
    await page.evaluate(() => window.__runThemeSnapshotQA?.(1));
    await expect(page.locator("html")).toHaveAttribute(
      "data-theme-id",
      expectedID,
    );
    expect(
      await page.evaluate(() => localStorage.getItem("ag.console.theme.id")),
    ).toBe(expectedID);
  });
}

test("a single native theme choice commits when its visual capture fails", async ({
  page,
}) => {
  const pageErrors: string[] = [];
  page.on("pageerror", (error) => pageErrors.push(error.message));
  await mockThemeSurface(page);
  await page.goto("/login");
  await page.evaluate(() => {
    for (let index = 0; index < 2; index += 1) {
      const probe = document.createElement("span");
      probe.textContent = "capture probe";
      probe.style.viewTransitionName = "duplicate-single-theme-capture";
      document.body.append(probe);
    }
    const start = document.startViewTransition.bind(document);
    window.__singleThemeCaptureCallsQA = 0;
    window.__singleThemeCaptureErrorsQA = [];
    document.startViewTransition = (options) => {
      window.__singleThemeCaptureCallsQA += 1;
      const transition = start(options);
      void transition.ready.catch((error: unknown) => {
        window.__singleThemeCaptureErrorsQA.push(
          error instanceof DOMException ? error.name : "unknown",
        );
      });
      return transition;
    };
  });
  await page.getByRole("button", { name: /选择主题/ }).click();
  await page.getByRole("menuitem", { name: /Gateway Light/ }).click();
  await expect(page.locator("html")).toHaveAttribute(
    "data-theme-id",
    "gateway-light",
  );
  await expect
    .poll(() => page.evaluate(() => window.__singleThemeCaptureErrorsQA))
    .toEqual(["InvalidStateError"]);
  expect(await page.evaluate(() => window.__singleThemeCaptureCallsQA)).toBe(1);
  await expect
    .poll(() => page.locator("html").getAttribute("data-theme-transition"))
    .toBeNull();
  expect(
    await page.evaluate(() => localStorage.getItem("ag.console.theme.id")),
  ).toBe("gateway-light");
  const expected = resolveConsoleTheme(
    defaultConsoleThemes.find((theme) => theme.id === "gateway-light")!,
  ).cssVariables;
  const actual = await page
    .locator("html")
    .evaluate(
      (root, names) =>
        Object.fromEntries(
          names.map((name) => [
            name,
            (root as HTMLElement).style.getPropertyValue(name),
          ]),
        ),
      Object.keys(expected),
    );
  expect(normalizeValues(actual)).toEqual(normalizeValues(expected));
  expect(pageErrors).toEqual([]);
});

test("a single theme choice survives SPA navigation before its capture update", async ({
  page,
}) => {
  await authenticateAsAdmin(page);
  await page.route("**/api/v2/site-settings", (route) =>
    route.fulfill({ json: defaultSiteSettings }),
  );
  await page.route("**/api/v2/repositories?**", (route) =>
    route.fulfill({ json: { items: [] } }),
  );
  await page.route("**/api/v2/formats", (route) => route.fulfill({ json: [] }));
  await page.route("**/api/v2/repository-capacities", (route) =>
    route.fulfill({ json: [] }),
  );
  await page.goto("/search");
  await page.evaluate(() => {
    const start = document.startViewTransition.bind(document);
    const gate = new Promise<void>((resolve) => {
      window.__releaseSingleThemeCaptureQA = resolve;
    });
    window.__singleThemeCaptureCallsQA = 0;
    document.startViewTransition = (options) => {
      window.__singleThemeCaptureCallsQA += 1;
      const update = typeof options === "function" ? options : options?.update;
      const transition = start(async () => {
        await gate;
        await update?.();
      });
      void transition.ready.catch(() => undefined);
      return transition;
    };
  });
  await page.getByRole("button", { name: /选择主题/ }).click();
  await page.getByRole("menuitem", { name: /Gateway Light/ }).click();
  await page.locator('a[href="/repositories"]').first().dispatchEvent("click");
  await expect(page).toHaveURL(/\/repositories$/);
  await page.evaluate(() => window.__releaseSingleThemeCaptureQA?.());
  await expect(page.locator("html")).toHaveAttribute(
    "data-theme-id",
    "gateway-light",
  );
  expect(await page.evaluate(() => window.__singleThemeCaptureCallsQA)).toBe(1);
  await expect
    .poll(() => page.locator("html").getAttribute("data-theme-transition"))
    .toBeNull();
  expect(
    await page.evaluate(() => localStorage.getItem("ag.console.theme.id")),
  ).toBe("gateway-light");
});

test("a single theme choice survives document navigation before its capture update", async ({
  page,
}, testInfo) => {
  await mockThemeSurface(page);
  await page.goto("/login");
  await page.evaluate(() => {
    const start = document.startViewTransition.bind(document);
    window.__singleThemeCaptureCallsQA = 0;
    document.startViewTransition = (options) => {
      const update = typeof options === "function" ? options : options?.update;
      window.__singleThemeCaptureCallsQA += 1;
      const gate = new Promise<void>(() => {});
      const transition = start(async () => {
        await gate;
        await update?.();
      });
      void transition.ready.catch(() => undefined);
      return transition;
    };
  });
  await page.getByRole("button", { name: /选择主题/ }).click();
  await page.getByRole("menuitem", { name: /Gateway Light/ }).click();
  const beforeNavigation = await page.evaluate(() => ({
    captures: window.__singleThemeCaptureCallsQA,
    storedThemeId: localStorage.getItem("ag.console.theme.id"),
    events: window.__themeEvidence,
  }));
  await testInfo.attach("theme-before-document-navigation.json", {
    body: JSON.stringify(beforeNavigation, null, 2),
    contentType: "application/json",
  });
  expect(beforeNavigation.captures).toBe(1);
  // The next document must restore the accepted choice, even when navigation
  // destroys this document before its optional visual snapshot can commit.
  await page.goto("/login?navigation-before-capture=1");
  await expect(
    page.getByRole("button", { name: /选择主题.*Gateway Light/ }),
  ).toBeVisible();
  await expect(page.locator("html")).toHaveAttribute(
    "data-theme-id",
    "gateway-light",
  );
});

test("native skipped snapshots preserve a newer choice of the current theme", async ({
  page,
}) => {
  await mockThemeSurface(page);
  await page.goto("/login");
  await page.evaluate(() => {
    const start = document.startViewTransition.bind(document);
    const releases: Array<() => void> = [];
    window.__nativeThemeUpdatesQA = [];
    document.startViewTransition = (options) => {
      const update = typeof options === "function" ? options : options?.update;
      const index = releases.length;
      const gate = new Promise<void>((resolve) => releases.push(resolve));
      const transition = start(async () => {
        await gate;
        await update?.();
        window.__nativeThemeUpdatesQA.push(index);
      });
      // A deliberately skipped native capture rejects only its ready promise.
      void transition.ready.catch(() => undefined);
      return transition;
    };
    window.__releaseNativeThemeCaptureQA = (index) => releases[index]();
  });
  const chooseWhileCapturing = async (name: string) => {
    await page.locator(".ag-theme-toggle").dispatchEvent("click");
    const item = page
      .locator(".ag-theme-dropdown [role=menuitem]")
      .filter({ hasText: name });
    await item.waitFor({ state: "attached" });
    await item.dispatchEvent("click");
  };
  await chooseWhileCapturing("Gateway Light");
  await chooseWhileCapturing("Gateway Dark");
  await page.evaluate(() => window.__releaseNativeThemeCaptureQA?.(0));
  await expect
    .poll(() => page.evaluate(() => window.__nativeThemeUpdatesQA))
    .toEqual([0]);
  await expect(page.locator("html")).toHaveAttribute(
    "data-theme-id",
    "gateway-dark",
  );
  await page.evaluate(() => window.__releaseNativeThemeCaptureQA?.(1));
  await expect
    .poll(() => page.evaluate(() => window.__nativeThemeUpdatesQA))
    .toEqual([0, 1]);
  await expect(page.locator("html")).toHaveAttribute(
    "data-theme-id",
    "gateway-dark",
  );
  await expect
    .poll(() => page.locator("html").getAttribute("data-theme-transition"))
    .toBeNull();
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
    __runThemeSnapshotQA?: (index: number) => Promise<void>;
    __nativeThemeUpdatesQA: number[];
    __releaseNativeThemeCaptureQA?: (index: number) => void;
    __singleThemeCaptureCallsQA: number;
    __singleThemeCaptureErrorsQA: string[];
    __releaseSingleThemeCaptureQA?: () => void;
  }
}
