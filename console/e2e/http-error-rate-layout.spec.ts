import { expect, test, type Page } from "@playwright/test";
import { authenticateAsAdmin, authenticateAsMember } from "./support/auth";
import { defaultSiteSettings } from "../src/lib/siteSettings";
import type { DiagnosticHttpErrorRate, Diagnostics } from "../src/client";

function rate(
  overrides: Partial<DiagnosticHttpErrorRate> = {},
): DiagnosticHttpErrorRate {
  const now = new Date().toISOString();
  return {
    checkedAt: now,
    sampleAt: now,
    windowStart: new Date(Date.now() - 300_000).toISOString(),
    windowEnd: now,
    instanceId: "synthetic-http-node",
    sessionId: "synthetic-http-session",
    source: "artifact_gateway_http_requests_total",
    scope: "responding_process_business_http",
    state: "available",
    windowSeconds: 300,
    minimumRequests: 20,
    sampleIntervalSeconds: 15,
    maxSampleAgeSeconds: 30,
    coverageSeconds: 300,
    requests: 20,
    errors: 2,
    ratio: 0.1,
    ...overrides,
  };
}

function diagnostics(httpErrorRate?: DiagnosticHttpErrorRate): Diagnostics {
  return {
    generatedAt: new Date().toISOString(),
    build: { version: "dev", revision: "synthetic", goVersion: "go1.test" },
    runtime: {
      instanceId: "synthetic-http-node",
      sessionId: "synthetic-http-session",
      roles: ["api"],
      workerFormats: [],
      workerKinds: [],
    },
    dependencies: [],
    queues: [],
    nodes: { status: "healthy", online: 1, stale: 0, offline: 0, issues: [] },
    httpErrorRate,
  };
}

async function shell(page: Page, member = false) {
  if (member) await authenticateAsMember(page);
  else await authenticateAsAdmin(page);
  await page.route("**/api/v2/site-settings", (route) =>
    route.fulfill({ json: defaultSiteSettings }),
  );
  if (!member)
    await page.route("**/api/v2/runtime/nodes", (route) =>
      route.fulfill({
        json: {
          items: [],
          health: diagnostics().nodes,
          releaseSource: "not_configured",
        },
      }),
    );
}

for (const locale of ["zh-CN", "en-US"]) {
  test(`HTTP observation has bounded desktop/mobile geometry (${locale})`, async ({
    page,
  }, info) => {
    const errors: string[] = [];
    page.on("pageerror", (e) => errors.push(e.message));
    page.on("console", (m) => {
      if (m.type() === "error") errors.push(m.text());
    });
    await shell(page);
    await page.addInitScript(
      (value) => localStorage.setItem("ag.console.locale", value),
      locale,
    );
    await page.route("**/api/v2/diagnostics", (route) =>
      route.fulfill({ json: diagnostics(rate()) }),
    );
    for (const width of [1440, 390]) {
      await page.setViewportSize({ width, height: 1000 });
      await page.goto("/system?tab=diagnostics");
      const card = page.locator(".ag-http-error-rate-card");
      await expect(card).toBeVisible();
      await expect(card.getByTestId("http-error-rate-ratio")).toHaveText("10%");
      await expect(card.getByText(/synthetic-http-node/)).toBeVisible();
      const geometry = await page
        .locator(".ag-system-diagnostics")
        .evaluate((root) => {
          const boxes = Array.from(root.children).map((el) =>
            el.getBoundingClientRect(),
          );
          const values = Array.from(
            root.querySelectorAll(".ag-http-error-rate-values > div"),
          ).map((el) => el.getBoundingClientRect());
          return {
            gaps: boxes.slice(1).map((box, i) => box.top - boxes[i].bottom),
            overflow: document.documentElement.scrollWidth > window.innerWidth,
            values: values.map((b) => ({
              x: b.x,
              right: b.right,
              top: b.top,
              bottom: b.bottom,
            })),
          };
        });
      expect(geometry.overflow).toBe(false);
      for (const gap of geometry.gaps) {
        expect(gap).toBeGreaterThanOrEqual(16);
        expect(gap).toBeLessThanOrEqual(18);
      }
      expect(geometry.values).toHaveLength(3);
      for (let i = 1; i < 3; i++) {
        const gap =
          width > 767
            ? geometry.values[i].x - geometry.values[i - 1].right
            : geometry.values[i].top - geometry.values[i - 1].bottom;
        expect(gap).toBeGreaterThanOrEqual(23);
        expect(gap).toBeLessThanOrEqual(25);
      }
      await card.screenshot({
        path: info.outputPath(`http-rate-${locale}-${width}.png`),
      });
    }
    expect(errors).toEqual([]);
  });
}

test("count boundary, no/low traffic, warmup, reset and stale observations hide misleading percentages", async ({
  page,
}) => {
  await shell(page);
  let current = rate();
  await page.route("**/api/v2/diagnostics", (route) =>
    route.fulfill({ json: diagnostics(current) }),
  );
  await page.goto("/system?tab=diagnostics");
  const card = page.locator(".ag-http-error-rate-card");
  await expect(card.getByTestId("http-error-rate-ratio")).toHaveText("10%");
  for (const [reason, requests, errors, message] of [
    ["low_sample", 19, 2, "少于 20 个请求，数据不足"],
    ["no_traffic", 0, 0, "窗口内无请求，比例未知"],
    ["warming_up", 20, 2, "窗口预热，数据不足"],
    ["session_changed", 1, 0, "进程会话已切换，窗口重新预热"],
    ["counter_reset", 1, 0, "计数器已重置，窗口重新预热"],
    ["sample_stale", 20, 2, "样本已过期，比例未知"],
  ] as const) {
    current = rate({
      state: reason === "sample_stale" ? "stale" : "unknown",
      reason,
      requests,
      errors,
      ratio: undefined,
    });
    await page.getByRole("button", { name: "刷新诊断" }).click();
    await expect(card.getByText(message)).toBeVisible();
    await expect(card.getByTestId("http-error-rate-ratio")).toHaveText("—");
  }
  current = rate({ requests: 20, errors: 0, ratio: 0 });
  await page.getByRole("button", { name: "刷新诊断" }).click();
  await expect(card.getByTestId("http-error-rate-ratio")).toHaveText("0%");
});

test("repeat refresh is bounded, failure retains old observation and permission loss clears it", async ({
  page,
}) => {
  await shell(page);
  let mode: "available" | "failed" | "forbidden" = "available";
  let calls = 0;
  await page.route("**/api/v2/diagnostics", (route) => {
    calls++;
    if (mode === "available")
      return route.fulfill({ json: diagnostics(rate()) });
    return route.fulfill({
      status: mode === "failed" ? 503 : 403,
      json: {
        code: mode === "failed" ? "internal_error" : "forbidden",
        status: mode === "failed" ? 503 : 403,
        message: "Synthetic HTTP observation refusal",
        requestId: "synthetic-http-request",
      },
    });
  });
  await page.goto("/system?tab=diagnostics");
  const card = page.locator(".ag-http-error-rate-card");
  await expect(card).toBeVisible();
  const before = calls;
  await page.getByRole("button", { name: "刷新诊断" }).evaluate((button) => {
    (button as HTMLButtonElement).click();
    (button as HTMLButtonElement).click();
  });
  await expect.poll(() => calls).toBe(before + 1);
  mode = "failed";
  await page.getByRole("button", { name: "刷新诊断" }).click();
  await expect(page.getByText("刷新失败，仍显示旧诊断快照")).toBeVisible();
  await expect(card.getByTestId("http-error-rate-ratio")).toHaveText("10%");
  mode = "forbidden";
  await page.getByRole("button", { name: "刷新诊断" }).click();
  await expect(
    page.getByText("Synthetic HTTP observation refusal"),
  ).toBeVisible();
  await expect(card).toHaveCount(0);
});

test("member cannot query or display HTTP diagnostics", async ({ page }) => {
  await shell(page, true);
  let queried = false;
  await page.route("**/api/v2/diagnostics", (route) => {
    queried = true;
    return route.fulfill({ json: diagnostics(rate()) });
  });
  await page.goto("/system?tab=diagnostics");
  await expect(page.locator(".ag-http-error-rate-card")).toHaveCount(0);
  expect(queried).toBe(false);
});
