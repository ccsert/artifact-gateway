import { expect, test } from "@playwright/test";
import { authenticateAsAdmin } from "./support/auth";

for (const width of [1440, 390]) {
  test(`runtime logs keep a bounded layout and load older entries at ${width}px`, async ({
    page,
  }, testInfo) => {
    const pageErrors: string[] = [];
    page.on("pageerror", (error) => pageErrors.push(error.message));
    await page.setViewportSize({ width, height: 900 });
    await authenticateAsAdmin(page);
    await page.route("**/api/v2/runtime/logs**", (route) => {
      const older = new URL(route.request().url()).searchParams.get(
        "beforeSequence",
      );
      return route.fulfill({
        json: {
          scope: "local",
          instanceId: "gateway-01",
          sessionId: "session-01",
          items: [
            {
              sequence: older ? 1 : 2,
              time: "2026-09-30T08:00:00Z",
              level: "INFO",
              message: older ? "older entry" : "recent entry",
              instanceId: "gateway-01",
              sessionId: "session-01",
              component: "http",
              operation: "request",
              requestId: "request-01",
              traceId: "trace-01",
            },
          ],
          ...(older ? {} : { nextSequence: 2 }),
        },
      });
    });

    await page.goto("/system?tab=logs");
    await expect(page.getByRole("heading", { name: "系统运行" })).toBeVisible();
    await expect(page.getByText("recent entry")).toBeVisible();
    const card = page.locator(".ag-card").filter({
      has: page.getByRole("heading", { name: "运行日志" }),
    });
    const note = card.getByText(/仅查询当前进程的内存日志/);
    const filters = card.locator(".ag-filter-bar");
    const [cardBox, noteBox, filtersBox] = await Promise.all([
      card.boundingBox(),
      note.boundingBox(),
      filters.boundingBox(),
    ]);
    expect(cardBox && noteBox && filtersBox).toBeTruthy();
    expect(noteBox!.y - cardBox!.y).toBeLessThan(90);
    expect(filtersBox!.y - (noteBox!.y + noteBox!.height)).toBeLessThan(40);
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth - innerWidth,
      ),
    ).toBeLessThanOrEqual(1);
    await expect(card.locator(".ant-alert-info")).toHaveCount(0);
    await page.screenshot({
      path: testInfo.outputPath(`runtime-logs-${width}.png`),
      fullPage: true,
    });

    await page.getByRole("button", { name: /加载更早日志/ }).click();
    await expect(page.getByText("older entry")).toBeVisible();
    await expect(page.getByText("recent entry")).toBeVisible();
    await expect(
      page.getByRole("button", { name: /加载更早日志/ }),
    ).toHaveCount(0);
    expect(pageErrors).toEqual([]);
  });
}

test("empty runtime logs do not show pagination", async ({ page }) => {
  await authenticateAsAdmin(page);
  await page.route("**/api/v2/runtime/logs**", (route) =>
    route.fulfill({
      json: {
        scope: "local",
        instanceId: "gateway-01",
        sessionId: "session-01",
        items: [],
      },
    }),
  );
  await page.goto("/operations?tab=logs&requestId=legacy-link");
  await expect(page).toHaveURL(/\/system\?tab=logs&requestId=legacy-link$/);
  await expect(page.getByText("当前范围内没有日志")).toBeVisible();
  await expect(page.getByRole("button", { name: /加载更早日志/ })).toHaveCount(
    0,
  );
});
