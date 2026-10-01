import { expect, test, type Page } from "@playwright/test";
import { authenticateAsAdmin } from "./support/auth";
import { expectTabGutter } from "./support/tabs";
import { defaultConsoleThemes } from "../src/lib/consoleTheme";

async function mockDiagnostics(page: Page, gatewayVersion: string) {
  await authenticateAsAdmin(page);
  await page.route("**/api/v2/site-settings", (route) =>
    route.fulfill({
      json: {
        version: "1",
        siteName: "Artifact Gateway",
        brandMark: "AG",
        logoUrl: "",
        enabledThemeIds: ["gateway-dark", "gateway-light"],
        defaultThemeId: "gateway-dark",
        availableThemes: defaultConsoleThemes,
        updatedAt: "2026-10-01T00:00:00Z",
      },
    }),
  );
  for (const path of [
    "lifecycle-jobs",
    "audit-retention/jobs",
    "scheduled-tasks",
  ]) {
    await page.route(`**/api/v2/${path}**`, (route) =>
      route.fulfill({ json: [] }),
    );
  }
  await page.route("**/api/v2/repositories**", (route) =>
    route.fulfill({ json: { items: [] } }),
  );
  const health = {
    status: "healthy",
    online: 0,
    stale: 0,
    offline: 0,
    issues: [],
  };
  await page.route("**/api/v2/runtime/nodes", (route) =>
    route.fulfill({
      json: { items: [], health, releaseSource: "not_configured" },
    }),
  );
  await page.route("**/api/v2/diagnostics", (route) =>
    route.fulfill({
      json: {
        generatedAt: "2026-10-01T00:00:00Z",
        build: {
          version: gatewayVersion,
          revision: "synthetic-revision",
          goVersion: "go test",
          modified: false,
        },
        runtime: {
          instanceId: "synthetic-api",
          roles: ["api"],
          workerFormats: [],
          workerKinds: [],
        },
        dependencies: [],
        queues: [],
        nodes: health,
      },
    }),
  );
}

for (const mismatch of [false, true]) {
  test(`Console build identity and ${mismatch ? "differing" : "matching"} Gateway`, async ({
    page,
  }, testInfo) => {
    const consoleVersion = String(
      testInfo.config.metadata.consoleBuildVersion ?? "dev",
    );
    const gatewayVersion = mismatch
      ? "v9.9.9"
      : consoleVersion === "dev"
        ? "v1.2.3"
        : `v${consoleVersion}`;
    const errors: string[] = [];
    page.on("pageerror", (error) => errors.push(error.message));
    page.on("console", (message) => {
      if (message.type() === "error") errors.push(message.text());
    });
    await mockDiagnostics(page, gatewayVersion);
    await page.setViewportSize({ width: 1440, height: 1000 });
    await page.goto("/system?tab=diagnostics");
    const identity = page.locator(".ag-diagnostics-identity-card");
    const consoleRow = identity
      .locator(".ag-diagnostic-identity-row")
      .filter({ has: page.locator("dt", { hasText: "Console 版本" }) });
    await expect(consoleRow.locator("dd")).toHaveText(consoleVersion);
    await expect(
      identity.getByText(gatewayVersion, { exact: true }),
    ).toBeVisible();
    await expect(
      identity.getByText("synthetic-revision", { exact: true }),
    ).toBeVisible();
    const warning = identity.getByText("前后端版本不一致", { exact: true });
    if (mismatch && consoleVersion !== "dev") {
      await expect(warning).toBeVisible();
      await expect(
        identity.getByText(/镜像 tag 不一致或浏览器缓存了旧 bundle/),
      ).toBeVisible();
    } else {
      await expect(warning).toHaveCount(0);
    }

    for (const width of [1440, 390]) {
      await page.setViewportSize({ width, height: 1000 });
      await expect.poll(() => page.evaluate(() => window.scrollX)).toBe(0);
      await expectTabGutter(page, ".ag-compact-tabs", ".ag-page-stack");
      const boxes = await page.locator(".ag-diagnostics-tab").evaluate((root) =>
        Array.from(root.children).map((element) => {
          const box = element.getBoundingClientRect();
          return { top: box.top, bottom: box.bottom };
        }),
      );
      for (let index = 1; index < boxes.length; index += 1) {
        const gap = boxes[index].top - boxes[index - 1].bottom;
        expect(gap).toBeGreaterThanOrEqual(24);
        expect(gap).toBeLessThanOrEqual(26);
      }
      const geometry = await page
        .locator(".ag-diagnostics-detail-grid")
        .evaluate((root) => {
          const identity = root
            .querySelector(".ag-diagnostics-identity-card")!
            .getBoundingClientRect();
          const dependencies = root
            .querySelector(".ag-diagnostics-dependencies-card")!
            .getBoundingClientRect();
          const beside = identity.left >= dependencies.right;
          return {
            gap: beside
              ? identity.left - dependencies.right
              : identity.top - dependencies.bottom,
            beside,
            overflow:
              document.documentElement.scrollWidth -
              document.documentElement.clientWidth,
          };
        });
      expect(geometry.beside).toBe(width === 1440);
      expect(geometry.gap).toBeGreaterThanOrEqual(16);
      expect(geometry.gap).toBeLessThanOrEqual(18);
      expect(geometry.overflow).toBeLessThanOrEqual(1);
      if (width === 1440) {
        const navigation = await page
          .locator(".ag-sider-desktop")
          .boundingBox();
        const dependencies = await page
          .locator(".ag-diagnostics-dependencies-card")
          .boundingBox();
        expect(navigation).not.toBeNull();
        expect(dependencies).not.toBeNull();
        expect(dependencies!.x).toBeGreaterThanOrEqual(
          navigation!.x + navigation!.width + 23,
        );
      }
      await expect(identity).toBeVisible();
      await page.screenshot({
        path: testInfo.outputPath(
          `console-build-${consoleVersion}-${mismatch ? "mismatch" : "match"}-${width}.png`,
        ),
        fullPage: width < 1024,
      });
    }
    expect(errors).toEqual([]);
  });
}
