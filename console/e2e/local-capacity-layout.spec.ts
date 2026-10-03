import { expect, test, type Page } from "@playwright/test";
import { authenticateAsAdmin, authenticateAsMember } from "./support/auth";
import { defaultSiteSettings } from "../src/lib/siteSettings";
import type { Diagnostics } from "../src/client";

const fixture: Diagnostics = {
  generatedAt: "2026-10-03T09:00:00Z",
  build: {
    version: "dev",
    revision: "synthetic",
    goVersion: "go1.test",
    modified: false,
  },
  runtime: {
    instanceId: "synthetic-capacity",
    sessionId: "synthetic-session",
    roles: ["api"],
    workerFormats: [],
    workerKinds: [],
  },
  dependencies: [],
  queues: [],
  nodes: { status: "healthy", online: 1, stale: 0, offline: 0, issues: [] },
  localCapacity: {
    checkedAt: "2026-10-03T09:00:00Z",
    source: "statfs",
    scope: "observer_mount_namespace",
    unit: "bytes",
    refreshIntervalSeconds: 15,
    maxSampleAgeSeconds: 60,
    timeoutMilliseconds: 1000,
    mounts: [
      {
        alias: "temporary",
        status: "available",
        totalBytes: 40960,
        availableBytes: 0,
        sampleAt: "2026-10-03T08:59:59Z",
        sharedWith: [],
      },
      {
        alias: "logs",
        status: "unknown",
        reason: "remote_filesystem",
        sharedWith: [],
      },
      {
        alias: "backups",
        status: "stale",
        reason: "timeout",
        sampleAt: "2026-10-03T08:58:00Z",
        sharedWith: [],
      },
    ],
  },
};

async function shell(page: Page, member = false) {
  if (member) await authenticateAsMember(page);
  else await authenticateAsAdmin(page);
  await page.route("**/api/v2/site-settings", (route) =>
    route.fulfill({ json: defaultSiteSettings }),
  );
  if (!member) {
    await page.route("**/api/v2/runtime/nodes", (route) =>
      route.fulfill({
        json: {
          items: [],
          health: fixture.nodes,
          releaseSource: "not_configured",
        },
      }),
    );
  }
}

for (const locale of ["zh-CN", "en-US"]) {
  test(`local mount capacity has bounded desktop/mobile geometry (${locale})`, async ({
    page,
  }, info) => {
    const errors: string[] = [];
    page.on("pageerror", (e) => errors.push(e.message));
    page.on("console", (message) => {
      if (message.type() === "error") errors.push(message.text());
    });
    await shell(page);
    await page.addInitScript(
      (value) => localStorage.setItem("ag.console.locale", value),
      locale,
    );
    await page.route("**/api/v2/diagnostics", (route) =>
      route.fulfill({ json: fixture }),
    );
    for (const width of [1440, 390]) {
      await page.setViewportSize({ width, height: 1000 });
      await page.goto("/system?tab=diagnostics");
      const card = page.locator(".ag-local-capacity-card");
      await expect(card).toBeVisible();
      await expect(card.getByText("0 B", { exact: true })).toBeVisible();
      await expect(
        card.getByText(
          locale === "en-US"
            ? "Remote filesystem; capacity unknown"
            : "远端文件系统，容量未知",
        ),
      ).toBeVisible();
      await expect(
        card.getByText(
          locale === "en-US"
            ? "Refresh timed out; capacity unknown"
            : "刷新超时，容量未知",
        ),
      ).toBeVisible();
      const geometry = await page
        .locator(".ag-system-diagnostics")
        .evaluate((root) => {
          const boxes = Array.from(root.children).map((el) =>
            el.getBoundingClientRect(),
          );
          const mounts = Array.from(
            root.querySelectorAll(".ag-local-capacity-mount"),
          ).map((el) => el.getBoundingClientRect());
          return {
            gaps: boxes.slice(1).map((box, i) => box.top - boxes[i].bottom),
            overflow: document.documentElement.scrollWidth > window.innerWidth,
            mounts: mounts.map((m) => ({
              x: m.x,
              right: m.right,
              top: m.top,
              bottom: m.bottom,
            })),
          };
        });
      expect(geometry.overflow).toBe(false);
      for (const gap of geometry.gaps) {
        expect(gap).toBeGreaterThanOrEqual(16);
        expect(gap).toBeLessThanOrEqual(18);
      }
      expect(geometry.mounts).toHaveLength(3);
      for (let i = 1; i < 3; i++) {
        if (width > 767)
          expect(
            geometry.mounts[i].x - geometry.mounts[i - 1].right,
          ).toBeGreaterThanOrEqual(23);
        else
          expect(
            geometry.mounts[i].top - geometry.mounts[i - 1].bottom,
          ).toBeGreaterThanOrEqual(23);
      }
      await card.screenshot({
        path: info.outputPath(`capacity-${locale}-${width}.png`),
      });
    }
    expect(errors).toEqual([]);
  });
}

test("repeat refresh is bounded; unavailable refresh preserves snapshot and permission loss removes it", async ({
  page,
}) => {
  await shell(page);
  let mode: "available" | "failed" | "forbidden" = "available";
  let calls = 0;
  await page.route("**/api/v2/diagnostics", async (route) => {
    calls++;
    if (mode === "available") return route.fulfill({ json: fixture });
    return route.fulfill({
      status: mode === "failed" ? 503 : 403,
      json: {
        code: mode === "failed" ? "internal_error" : "forbidden",
        status: mode === "failed" ? 503 : 403,
        message: "Synthetic refresh refusal",
        requestId: "synthetic-capacity-request",
      },
    });
  });
  await page.goto("/system?tab=diagnostics");
  await expect(page.locator(".ag-local-capacity-card")).toBeVisible();
  const before = calls;
  await page.getByRole("button", { name: "刷新诊断" }).evaluate((button) => {
    (button as HTMLButtonElement).click();
    (button as HTMLButtonElement).click();
  });
  await expect.poll(() => calls).toBe(before + 1);
  mode = "failed";
  await page.getByRole("button", { name: "刷新诊断" }).click();
  await expect(page.getByText("刷新失败，仍显示旧诊断快照")).toBeVisible();
  await expect(page.locator(".ag-local-capacity-card")).toBeVisible();
  mode = "forbidden";
  await page.getByRole("button", { name: "刷新诊断" }).click();
  await expect(page.getByText("Synthetic refresh refusal")).toBeVisible();
  await expect(page.locator(".ag-local-capacity-card")).toHaveCount(0);
});

test("member cannot open system capacity diagnostics", async ({ page }) => {
  await shell(page, true);
  let queried = false;
  await page.route("**/api/v2/diagnostics", (route) => {
    queried = true;
    return route.fulfill({ json: fixture });
  });
  await page.goto("/system?tab=diagnostics");
  await expect(page.locator(".ag-local-capacity-card")).toHaveCount(0);
  await expect(page.getByRole("heading", { name: "本地挂载容量" })).toHaveCount(
    0,
  );
  expect(queried).toBe(false);
});
