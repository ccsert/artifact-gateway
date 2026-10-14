import { expect, test, type Page } from "@playwright/test";
import { waitForModalOpen } from "./support/motion";

/**
 * Operations against a live Gateway: each tab loads on its own, including
 * when the page is opened directly on it.
 */

const token = process.env.GATEWAY_ADMIN_TOKEN;

test.skip(!token, "GATEWAY_ADMIN_TOKEN is required for the operations gate");

async function authenticate(page: Page) {
  await page.addInitScript((accessToken) => {
    localStorage.setItem("ag.console.token", accessToken);
    localStorage.setItem("ag.console.role", "admin");
  }, token!);
}

test("a direct link to job history loads the jobs", async ({ page }) => {
  await authenticate(page);
  const jobs = page.waitForResponse(
    (response) =>
      response.url().includes("/api/v2/lifecycle-jobs") && response.ok(),
  );

  await page.goto("/operations?tab=jobs");

  await jobs;
  await expect(page.getByRole("tab", { name: "执行记录" })).toHaveAttribute(
    "aria-selected",
    "true",
  );
  await expect(page.getByText("加载任务状态…")).toHaveCount(0);
  await expect(
    page
      .getByText("没有匹配的任务")
      .or(page.locator(".ag-operation-desktop-table .ant-table-row").first()),
  ).toBeVisible();
});

const suffix = `${Date.now()}-${Math.random().toString(16).slice(2, 8)}`;
const admin = () => ({ Authorization: `Bearer ${token!}` });

test("a scheduled task is created, paused, run, and deleted", async ({
  page,
  request,
}) => {
  await authenticate(page);
  const name = `e2e-schedule-${suffix}`;
  const stored = async () => {
    const response = await request.get("/api/v2/scheduled-tasks", {
      headers: admin(),
    });
    expect(response.ok()).toBeTruthy();
    return (
      (await response.json()) as Array<{
        id: string;
        name: string;
        enabled: boolean;
        kind: string;
      }>
    ).find((task) => task.name === name);
  };

  await page.goto("/operations?tab=schedules");
  await page.getByRole("button", { name: /新建计划/ }).click();
  const dialog = page.getByRole("dialog", { name: "新建计划任务" });
  await waitForModalOpen(dialog);
  await dialog.getByPlaceholder("例如：每日清理快照").fill(name);
  await dialog.getByText("执行仓库保留策略").click();
  await page
    .locator(".ant-select-item-option")
    .filter({ hasText: "执行审计保留策略" })
    .click();
  await dialog.getByRole("button", { name: /^创\s*建$/ }).click();
  await expect(dialog).toBeHidden();
  await expect.poll(async () => (await stored())?.kind).toBe("audit-retention");

  const row = page.getByRole("row").filter({ hasText: name });
  await row.getByRole("switch", { name: "启用计划任务" }).click();
  await expect.poll(async () => (await stored())?.enabled).toBe(false);

  // A manual run is recorded in the task's dispatch history.
  await row.getByRole("button", { name: "投递历史" }).click();
  await row.getByRole("button", { name: "立即执行" }).click();
  await expect(page.getByText("手动", { exact: true }).first()).toBeVisible();

  await row.getByRole("button", { name: `${name} 的更多操作` }).click();
  await page.getByRole("menuitem", { name: "删除计划" }).click();
  await page
    .getByRole("dialog")
    .getByRole("button", { name: /^删\s*除$/ })
    .click();
  await expect(row).toHaveCount(0);
  expect(await stored()).toBeUndefined();
});

test("a webhook subscription is created and paused", async ({
  page,
  request,
}) => {
  await authenticate(page);
  const name = `e2e-webhook-${suffix}`;
  const stored = async () => {
    const response = await request.get("/api/v2/webhook-subscriptions", {
      headers: admin(),
    });
    expect(response.ok()).toBeTruthy();
    return (
      (await response.json()) as Array<{ name: string; enabled: boolean }>
    ).find((subscription) => subscription.name === name);
  };

  await page.goto("/operations?tab=webhooks");
  await page.getByRole("button", { name: "新建订阅" }).click();
  const dialog = page.getByRole("dialog", { name: "新建 Webhook 订阅" });
  await waitForModalOpen(dialog);
  await dialog.getByPlaceholder("例如：安全自动化").fill(name);
  await dialog
    .getByPlaceholder("https://hooks.example.com/artifacts")
    .fill("https://hooks.example.test/artifacts");
  await dialog.getByPlaceholder("至少 32 个字符").fill("s".repeat(40));
  await dialog.getByRole("button", { name: "创建" }).click();
  // Signing secrets are stored encrypted; a Gateway started without
  // GATEWAY_SETTINGS_ENCRYPTION_KEY refuses them with a stable code.
  const refused = dialog.getByText("encryption_key_unavailable");
  await expect(
    refused.or(page.getByRole("row").filter({ hasText: name })),
  ).toBeVisible();
  test.skip(
    await refused.isVisible(),
    "The Gateway has no GATEWAY_SETTINGS_ENCRYPTION_KEY for webhook secrets.",
  );
  await expect(dialog).toBeHidden();
  await expect.poll(async () => (await stored())?.enabled).toBe(true);

  await page
    .getByRole("row")
    .filter({ hasText: name })
    .getByRole("switch", { name: "启用 Webhook 订阅" })
    .click();
  await expect.poll(async () => (await stored())?.enabled).toBe(false);
});
