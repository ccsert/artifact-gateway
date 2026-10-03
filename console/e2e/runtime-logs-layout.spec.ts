import { expect, test, type Locator, type Page } from "@playwright/test";
import { authenticateAsAdmin, authenticateAsMember } from "./support/auth";
import { defaultSiteSettings } from "../src/lib/siteSettings";
import type { RuntimeLogEntry, RuntimeLogPage } from "../src/client";

const entry = (sequence: number): RuntimeLogEntry => ({
  sequence,
  time: "2026-10-03T00:00:00Z",
  level: ["ERROR", "WARN", "INFO", "DEBUG"][sequence % 4],
  instanceId: "synthetic-gateway",
  sessionId: "synthetic-session",
  component: "http",
  operation: "http.request",
  message: `event-${sequence}`,
  requestId: "request-01",
  traceId: "trace-01",
  status: 200,
  durationMs: 12,
  method: "GET",
  route: "GET /api/v2/repositories/{repositoryId}",
  requestClass: "management",
});
const data = (
  items: RuntimeLogEntry[],
  extra: Partial<RuntimeLogPage> = {},
): RuntimeLogPage => ({
  scope: "local",
  instanceId: "synthetic-gateway",
  sessionId: "synthetic-session",
  items,
  afterCursor: `cursor-${items.at(-1)?.sequence ?? 0}`,
  order: "ascending",
  hasMore: false,
  retention: {
    earliestSequence: 1,
    latestSequence: items.at(-1)?.sequence ?? 0,
    gap: false,
  },
  source: {
    minimumLevel: "INFO",
    accessMode: "limited",
    slowThresholdMs: 1000,
    capacityLines: 1000,
    maxLineBytes: 16384,
    components: ["http", "proxy_worker"],
    componentsTruncated: false,
  },
  ...extra,
});
async function shell(page: Page) {
  await authenticateAsAdmin(page);
  await page.route("**/api/v2/site-settings", (route) =>
    route.fulfill({ json: defaultSiteSettings }),
  );
}
function errors(page: Page) {
  const failures: string[] = [];
  page.on("pageerror", (error) => failures.push(error.message));
  page.on("console", (message) => {
    if (message.type() === "error") failures.push(message.text());
  });
  return failures;
}
async function gap(upper: Locator, lower: Locator) {
  const [a, b] = await Promise.all([upper.boundingBox(), lower.boundingBox()]);
  expect(a).not.toBeNull();
  expect(b).not.toBeNull();
  return b!.y - a!.y - a!.height;
}
async function geometry(page: Page) {
  const header = page.locator(".ag-page-stack > .ag-page-header");
  const tabs = page.locator(".ag-page-stack > .ag-compact-tabs");
  expect(await gap(header, tabs)).toBeGreaterThanOrEqual(24);
  expect(await gap(header, tabs)).toBeLessThanOrEqual(26);
  const bar = tabs.locator(".ant-tabs-nav").first();
  const card = tabs.locator(".ag-card").first();
  expect(await gap(bar, card)).toBeGreaterThanOrEqual(16);
  expect(await gap(bar, card)).toBeLessThanOrEqual(18);
  expect(
    await page.evaluate(
      () =>
        document.documentElement.scrollWidth -
        document.documentElement.clientWidth,
    ),
  ).toBeLessThanOrEqual(0);
  const rows = page.locator(".ag-runtime-log-row");
  if ((await rows.count()) > 1)
    expect(await gap(rows.nth(0), rows.nth(1))).toBeGreaterThanOrEqual(0);
  const [cardBox, streamBox] = await Promise.all([
    card.boundingBox(),
    rows
      .count()
      .then((count) =>
        count ? page.locator(".ag-runtime-log-stream").boundingBox() : null,
      ),
  ]);
  if (streamBox) {
    expect(streamBox.height).toBeLessThanOrEqual(560);
    expect(streamBox.x).toBeGreaterThanOrEqual(cardBox!.x);
    expect(streamBox.x + streamBox.width).toBeLessThanOrEqual(
      cardBox!.x + cardBox!.width,
    );
  }
  if (await page.locator(".ag-sider-desktop").isVisible()) {
    const sider = await page.locator(".ag-sider-desktop").boundingBox();
    expect(cardBox!.x).toBeGreaterThanOrEqual(sider!.x + sider!.width);
  }
}

test.describe("runtime log touch controls", () => {
  test.use({ hasTouch: true });
  for (const width of [320, 390]) {
    test(`help disclosure has a 44px touch target at ${width}px`, async ({
      page,
    }) => {
      await page.setViewportSize({ width, height: 900 });
      await shell(page);
      await page.route("**/api/v2/runtime/logs**", (route) =>
        route.fulfill({ json: data([entry(1)]) }),
      );
      await page.goto("/system?tab=logs");
      const help = page.locator(".ag-runtime-log-help summary");
      await expect(help).toBeVisible();
      const target = await help.boundingBox();
      expect(target!.height).toBeGreaterThanOrEqual(44);
      await help.click();
      await expect(page.locator(".ag-runtime-log-help")).toHaveAttribute(
        "open",
        "",
      );
      await geometry(page);
      await page
        .getByRole("combobox", { name: "时间范围", exact: true })
        .click();
      await page.getByRole("option", { name: "自定义", exact: true }).click();
      await page.getByPlaceholder("开始日期").click();
      const calendar = page.locator(".ag-runtime-log-calendar:visible");
      await expect(calendar).toBeVisible();
      const popup = await calendar.boundingBox();
      expect(popup!.x).toBeGreaterThanOrEqual(12);
      expect(popup!.x + popup!.width).toBeLessThanOrEqual(width - 12);
      await geometry(page);
    });
  }
});

for (const [width, locale, theme] of [
  [1440, "zh-CN", "dark"],
  [390, "en-US", "light"],
  [320, "zh-CN", "dark"],
  [900, "zh-CN", "dark"],
] as const) {
  test(`literal bounded log rows, history and export at ${width}px ${theme}`, async ({
    page,
    context,
  }, testInfo) => {
    const failures = errors(page);
    await page.setViewportSize({ width, height: 900 });
    await context.grantPermissions(["clipboard-read", "clipboard-write"]);
    await shell(page);
    await page.addInitScript(
      ({ locale, theme }) => {
        localStorage.setItem("ag.console.locale", locale);
        localStorage.setItem("ag.console.theme", theme);
      },
      { locale, theme },
    );
    const items = [entry(2), entry(3), entry(4), entry(5)];
    items[0].message =
      '<img src=x onerror="window.__logExecuted=true">\u001b[31m literal\nsecond line';
    items[1].message = "long-" + "x".repeat(6000);
    items[2].component = "unknown-component-" + "z".repeat(160);
    const requests: URL[] = [];
    await page.route("**/api/v2/runtime/logs**", (route) => {
      const url = new URL(route.request().url());
      requests.push(url);
      return route.fulfill({
        json: data(
          url.searchParams.has("beforeSequence") ? [entry(1)] : items,
          url.searchParams.has("beforeSequence") ? {} : { nextSequence: 2 },
        ),
      });
    });
    await page.goto("/system?tab=logs&requestId=request-01&traceId=trace-01");
    const stream = page.locator(".ag-runtime-log-stream");
    await expect(stream.locator(".ag-runtime-log-row")).toHaveCount(4);
    await expect(page.locator("html")).toHaveAttribute("data-theme", theme);
    for (const level of ["ERROR", "WARN", "INFO", "DEBUG"])
      await expect(
        stream.locator(".ag-runtime-log-level", { hasText: level }),
      ).toBeVisible();
    await expect(stream.getByText(/unknown-component-/)).toBeVisible();
    expect(await stream.locator("img, script, iframe").count()).toBe(0);
    await expect(
      stream.locator(".ag-runtime-log-message").first(),
    ).toContainText("\\u001b[31m literal\nsecond line");
    expect(
      await stream.locator(".ag-runtime-log-message").nth(1).textContent(),
    ).toHaveLength(4097);
    expect(
      await stream.locator(".ag-runtime-log-detail").first().textContent(),
    ).toContain(
      "GET · GET /api/v2/repositories/{repositoryId} · management · status=200 · 12ms",
    );
    await expect(stream.locator("a")).toHaveCount(0);
    await geometry(page);
    await stream.evaluate((node) => {
      node.scrollTop = 0;
    });
    await page.screenshot({
      path: testInfo.outputPath(`runtime-logs-${width}-${theme}.png`),
      fullPage: true,
      animations: "disabled",
    });
    await geometry(page);
    await page.getByRole("switch").click();
    await expect(stream).toHaveClass(/ag-runtime-log-nowrap/);
    expect(
      await stream
        .locator(".ag-runtime-log-message")
        .nth(1)
        .evaluate((node) => node.scrollWidth > node.clientWidth),
    ).toBe(true);
    await geometry(page);
    await page.getByRole("button", { name: /复制已加载|Copy loaded/ }).click();
    const copied = await page.evaluate(() => navigator.clipboard.readText());
    const parsed = copied
      .trim()
      .split("\n")
      .map((line) => JSON.parse(line));
    expect(parsed).toHaveLength(4);
    expect(parsed[0].status).toBe(200);
    expect(parsed[0].route).toBe(items[0].route);
    expect(parsed[0].message).toContain("\\u001b");
    const downloadEvent = page.waitForEvent("download");
    await page
      .getByRole("button", { name: /下载 NDJSON|Download NDJSON/ })
      .click();
    const download = await downloadEvent;
    expect(download.suggestedFilename()).toBe("runtime-logs.ndjson");
    const reader = await download.createReadStream();
    const chunks: Buffer[] = [];
    for await (const chunk of reader!) chunks.push(Buffer.from(chunk));
    expect(Buffer.concat(chunks).toString("utf8")).toBe(copied);
    await stream
      .locator(".ag-runtime-log-message")
      .first()
      .evaluate((node) => {
        const range = document.createRange();
        range.selectNodeContents(node);
        const selection = getSelection()!;
        selection.removeAllRanges();
        selection.addRange(range);
        document.dispatchEvent(new Event("selectionchange"));
      });
    await page.getByRole("button", { name: /复制选择|Copy selection/ }).click();
    expect(await page.evaluate(() => navigator.clipboard.readText())).toContain(
      "second line",
    );
    await page
      .getByRole("button", { name: /加载更早日志|Load older logs/ })
      .click();
    await expect(stream.locator(".ag-runtime-log-row")).toHaveCount(5);
    await expect(stream.getByText("event-1", { exact: true })).toBeVisible();
    expect(requests.at(-1)?.searchParams.get("beforeSequence")).toBe("2");
    expect(requests.at(-1)?.searchParams.get("requestId")).toBe("request-01");
    expect(requests.at(-1)?.searchParams.get("traceId")).toBe("trace-01");
    expect(failures).toEqual([]);
  });
}

test("bounded follow retains the consumed anchor and reports only fetched unread records", async ({
  page,
}) => {
  const failures = errors(page);
  await shell(page);
  let stage = 0;
  const cursors: string[] = [];
  await page.route("**/api/v2/runtime/logs**", (route) => {
    const cursor = new URL(route.request().url()).searchParams.get(
      "afterCursor",
    );
    if (cursor) {
      cursors.push(cursor);
      stage++;
    }
    const first = stage === 0 ? 1 : (stage - 1) * 100 + 51;
    const last = stage === 0 ? 50 : stage === 4 ? 430 : stage * 100 + 50;
    return route.fulfill({
      json: data(
        Array.from({ length: last - first + 1 }, (_, i) => entry(first + i)),
        {
          hasMore: stage > 0 && stage < 4,
          retention: {
            earliestSequence: stage === 4 ? 350 : 1,
            latestSequence: last,
            gap: stage === 4,
          },
        },
      ),
    });
  });
  await page.goto("/system?tab=logs");
  const stream = page.locator(".ag-runtime-log-stream");
  await expect(stream.locator(".ag-runtime-log-row")).toHaveCount(50);
  await stream.evaluate((node) => {
    node.scrollTop = 0;
    node.dispatchEvent(new Event("scroll"));
  });
  await page.getByRole("button", { name: "跟随新日志" }).click();
  await expect(page.getByRole("button", { name: /100 条未读/ })).toBeVisible();
  expect(await stream.evaluate((node) => node.scrollTop)).toBe(0);
  await expect(stream.locator('[data-sequence="430"]')).toHaveCount(1, {
    timeout: 15_000,
  });
  await page.getByRole("button", { name: "暂停跟随" }).click();
  expect(cursors).toEqual([
    "cursor-50",
    "cursor-150",
    "cursor-250",
    "cursor-350",
  ]);
  await expect(stream.locator(".ag-runtime-log-row")).toHaveCount(300);
  const sequences = await stream
    .locator(".ag-runtime-log-row")
    .evaluateAll((nodes) =>
      nodes.map((node) => Number((node as HTMLElement).dataset.sequence)),
    );
  expect(sequences).toEqual(Array.from({ length: 300 }, (_, i) => i + 131));
  await expect(page.getByText(/客户端容量已移除 130 条/)).toBeVisible();
  await expect(page.getByText("服务器保留已覆盖部分未读日志")).toBeVisible();
  await page.getByRole("button", { name: /条未读 · 回到底部/ }).click();
  await expect(page.getByRole("button", { name: "暂停跟随" })).toBeVisible();
  expect(
    await stream.evaluate(
      (node) => node.scrollHeight - node.scrollTop - node.clientHeight,
    ),
  ).toBeLessThanOrEqual(32);
  expect(failures).toEqual([]);
});

test("empty source policy and failed refresh retain honest async states", async ({
  page,
}) => {
  await shell(page);
  let fail = false;
  await page.route("**/api/v2/runtime/logs**", (route) =>
    route.fulfill(
      fail
        ? {
            status: 503,
            contentType: "application/problem+json",
            json: {
              status: 503,
              code: "log_buffer_unavailable",
              message: "synthetic unavailable",
            },
          }
        : { json: data([]) },
    ),
  );
  await page.goto("/operations?tab=logs&requestId=legacy-link");
  await expect(page).toHaveURL(/\/system\?tab=logs&requestId=legacy-link$/);
  await expect(page.getByText("当前范围内没有日志")).toBeVisible();
  await page.getByText("查询与复制说明", { exact: true }).click();
  await expect(page.getByText(/DEBUG 筛选不会启用 DEBUG/)).toBeVisible();
  await expect(page.getByText(/最低 INFO · 请求 limited/)).toBeVisible();
  await expect(
    page.getByRole("button", { name: "下载 NDJSON" }),
  ).toBeDisabled();
  await geometry(page);
  fail = true;
  await page.getByRole("button", { name: "刷新快照" }).click();
  await expect(
    page.getByText("当前节点未启用内存日志查询，请检查日志缓冲配置。"),
  ).toBeVisible();
  await expect(page.getByText("当前范围内没有日志")).toBeVisible();
  await expect(page.getByText("查询运行日志…")).toHaveCount(0);
  await geometry(page);
});

test("non-administrators cannot enter the runtime stream or request logs", async ({
  page,
}) => {
  await authenticateAsMember(page);
  let calls = 0;
  await page.route("**/api/v2/site-settings", (route) =>
    route.fulfill({ json: defaultSiteSettings }),
  );
  await page.route("**/api/v2/runtime/logs**", (route) => {
    calls++;
    return route.fulfill({ json: data([entry(1)]) });
  });
  await page.route("**/api/v2/repositories**", (route) =>
    route.fulfill({ json: { items: [] } }),
  );
  await page.goto("/system?tab=logs");
  await expect(page.getByRole("heading", { name: "系统运行" })).toHaveCount(0);
  await expect(page.locator(".ag-runtime-log-stream")).toHaveCount(0);
  expect(calls).toBe(0);
});

for (const width of [1440, 390])
  test(`time, exact component and audit filters reset together at ${width}px`, async ({
    page,
  }) => {
    await page.setViewportSize({ width, height: 900 });
    await shell(page);
    await page.addInitScript(() =>
      localStorage.setItem("ag.console.locale", "en-US"),
    );
    const requests: URL[] = [];
    await page.route("**/api/v2/runtime/logs**", (route) => {
      requests.push(new URL(route.request().url()));
      return route.fulfill({ json: data([entry(1)]) });
    });
    await page.goto("/system?tab=logs&requestId=request-01&traceId=trace-01");
    await expect(page.getByText("event-1", { exact: true })).toBeVisible();
    await page
      .getByRole("combobox", { name: "Time range", exact: true })
      .click();
    await page.getByRole("option", { name: "Custom", exact: true }).click();
    await page.getByPlaceholder("Start date").fill("2026-10-03 00:00:00");
    await page.getByPlaceholder("Start date").press("Enter");
    await page.getByPlaceholder("End date").fill("2026-10-03 01:00:00");
    await page.getByPlaceholder("End date").press("Enter");
    await page.getByPlaceholder("End date").press("Escape");
    await page
      .locator(".ag-runtime-log-filters label")
      .filter({ has: page.getByText("Component", { exact: true }) })
      .getByRole("combobox")
      .fill("custom_worker");
    await page.getByRole("button", { name: /Search$/ }).click();
    await expect
      .poll(() => requests.at(-1)?.searchParams.get("component"))
      .toBe("custom_worker");
    const expected = await page.evaluate(() => [
      new Date(2026, 9, 3, 0, 0, 0).toISOString(),
      new Date(2026, 9, 3, 1, 0, 0).toISOString(),
    ]);
    expect(requests.at(-1)?.searchParams.get("from")).toBe(expected[0]);
    expect(requests.at(-1)?.searchParams.get("to")).toBe(expected[1]);
    expect(requests.at(-1)?.searchParams.get("requestId")).toBe("request-01");
    expect(requests.at(-1)?.searchParams.get("traceId")).toBe("trace-01");
    expect(requests.at(-1)?.searchParams.has("afterCursor")).toBe(false);
    await page
      .getByRole("button", { name: "Clear filters", exact: true })
      .click();
    await expect
      .poll(() => requests.at(-1)?.searchParams.toString())
      .toBe("limit=50");
    await expect(page.getByText(/Current correlation filters/)).toHaveCount(0);
    await geometry(page);
  });

test("native selection is cleared on snapshot, session and permission recovery", async ({
  page,
  context,
}) => {
  await context.grantPermissions(["clipboard-read", "clipboard-write"]);
  await shell(page);
  let version = 1,
    deny = false;
  await page.route("**/api/v2/runtime/logs**", (route) =>
    route.fulfill(
      deny
        ? {
            status: 403,
            json: {
              status: 403,
              code: "password_change_required",
              message: "synthetic revoked",
            },
          }
        : {
            json: data(
              [
                {
                  ...entry(version),
                  sessionId: version >= 3 ? "new-session" : "synthetic-session",
                  message: `safe-snapshot-${version}`,
                },
              ],
              { sessionId: version >= 3 ? "new-session" : "synthetic-session" },
            ),
          },
    ),
  );
  await page.goto("/system?tab=logs&requestId=initial-filter");
  const copy = page.getByRole("button", { name: "复制选择" });
  const select = async () => {
    await page.locator(".ag-runtime-log-message").evaluate((node) => {
      const range = document.createRange();
      range.selectNodeContents(node);
      const selection = getSelection()!;
      selection.removeAllRanges();
      selection.addRange(range);
    });
    await expect(copy).toBeEnabled();
    await copy.click();
    expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(
      `safe-snapshot-${version}`,
    );
  };
  await expect(
    page.getByText("safe-snapshot-1", { exact: true }),
  ).toBeVisible();
  await select();
  version = 2;
  await page.getByRole("button", { name: "刷新快照" }).click();
  await expect(
    page.getByText("safe-snapshot-2", { exact: true }),
  ).toBeVisible();
  await expect(copy).toBeDisabled();
  await select();
  version = 3;
  await page.getByRole("button", { name: "刷新快照" }).click();
  await expect(
    page.getByText("safe-snapshot-3", { exact: true }),
  ).toBeVisible();
  await expect(copy).toBeDisabled();
  expect(await page.evaluate(() => getSelection()?.toString())).toBe("");
  await select();
  deny = true;
  await page.getByRole("button", { name: "刷新快照" }).click();
  await expect(
    page.getByText("需要先更新密码，之后再查询运行日志。"),
  ).toBeVisible();
  await expect(copy).toHaveCount(0);
  deny = false;
  version = 4;
  await page.getByRole("button", { name: "刷新快照" }).click();
  await expect(
    page.getByText("safe-snapshot-4", { exact: true }),
  ).toBeVisible();
  await expect(copy).toBeDisabled();
  expect(await page.evaluate(() => getSelection()?.toString())).toBe("");
  await select();
  version = 5;
  await page.getByRole("button", { name: "清除筛选" }).click();
  await expect(
    page.getByText("safe-snapshot-5", { exact: true }),
  ).toBeVisible();
  await expect(copy).toBeDisabled();
});
