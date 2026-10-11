import { expect, test, type Page } from "@playwright/test";

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
