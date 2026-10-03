import { expect, test } from "@playwright/test";
import { authenticateAsAdmin } from "./support/auth";
import { defaultSiteSettings } from "../src/lib/siteSettings";

test.use({ timezoneId: "Asia/Shanghai" });

test("rejects a 24-hour-plus-one-second custom window before requesting logs", async ({
  page,
}) => {
  await authenticateAsAdmin(page);
  await page.route("**/api/v2/site-settings", (route) =>
    route.fulfill({ json: defaultSiteSettings }),
  );
  const requests: string[] = [];
  await page.route("**/api/v2/runtime/logs**", (route) => {
    requests.push(route.request().url());
    return route.fulfill({
      json: {
        scope: "local",
        instanceId: "synthetic",
        sessionId: "session",
        items: [],
        afterCursor: "cursor",
      },
    });
  });
  await page.goto("/system?tab=logs");
  await expect(page.getByText("当前范围内没有日志")).toBeVisible();
  const custom = page.getByRole("combobox", { name: "时间范围", exact: true });
  if (await custom.count()) {
    await custom.click();
    await page.getByRole("option", { name: "自定义", exact: true }).click();
  }
  await page.getByPlaceholder("开始日期").fill("2026-10-02 22:07:26");
  await page.getByPlaceholder("开始日期").press("Enter");
  await page.getByPlaceholder("结束日期").fill("2026-10-03 22:07:27");
  await page.getByPlaceholder("结束日期").press("Enter");
  await page.getByPlaceholder("结束日期").press("Escape");
  const count = requests.length;
  await page.getByRole("button", { name: /查询$/ }).click();
  await expect(page.getByRole("alert")).toContainText(
    "时间范围不能超过 24 小时",
  );
  expect(requests.length).toBe(count);
});
