import { expect, test, type Page } from "@playwright/test";
import { authenticateAsAdmin } from "./support/auth";

async function mockAudits(page: Page) {
  await authenticateAsAdmin(page);
  await page.route("**/api/v2/repositories**", (route) =>
    route.fulfill({ json: { items: [] } }),
  );
  await page.route("**/api/v2/groups**", (route) =>
    route.fulfill({ json: { items: [] } }),
  );
}

test("audit date search uses the Ant Design range picker and ISO query bounds", async ({
  page,
}) => {
  await mockAudits(page);
  const auditRequests: string[] = [];
  await page.route("**/api/v2/audits/page**", (route) => {
    auditRequests.push(route.request().url());
    return route.fulfill({ json: { items: [] } });
  });

  await page.goto("/audits");
  await expect(page.getByRole("heading", { name: "审计日志" })).toBeVisible();
  const picker = page.locator(".ant-picker-range");
  const inputs = picker.locator("input");
  await expect(picker).toBeVisible();
  await expect(inputs).toHaveCount(2);
  await expect(page.locator('input[type="datetime-local"]')).toHaveCount(0);

  await inputs.nth(0).fill("2026-08-18 09:00");
  await inputs.nth(0).press("Enter");
  await inputs.nth(1).fill("2026-08-18 10:30");
  await inputs.nth(1).press("Enter");

  await expect
    .poll(() =>
      auditRequests.some((url) => {
        const query = new URL(url).searchParams;
        return Boolean(query.get("from") && query.get("to"));
      }),
    )
    .toBe(true);
  const rangedRequest = [...auditRequests].reverse().find((url) => {
    const query = new URL(url).searchParams;
    return Boolean(query.get("from") && query.get("to"));
  });
  expect(rangedRequest).toBeDefined();
  const query = new URL(rangedRequest!).searchParams;
  expect(query.get("from")).toBe(new Date(2026, 7, 18, 9, 0).toISOString());
  expect(query.get("to")).toBe(new Date(2026, 7, 18, 10, 30).toISOString());
});

test("audit date picker stays inside the card at narrow zoom", async ({
  page,
}) => {
  const consoleErrors: string[] = [];
  const pageErrors: string[] = [];
  page.on("console", (message) => {
    if (message.type() === "error") consoleErrors.push(message.text());
  });
  page.on("pageerror", (error) => pageErrors.push(error.message));
  await page.setViewportSize({ width: 320, height: 900 });
  await mockAudits(page);
  await page.route("**/api/v2/audits/page**", (route) =>
    route.fulfill({ json: { items: [] } }),
  );

  await page.goto("/audits");
  const picker = page.locator(".ant-picker-range");
  await expect(picker).toBeVisible();
  const geometry = await picker.evaluate((element) => {
    const picker = element.getBoundingClientRect();
    const card = element
      .closest<HTMLElement>(".ag-card")!
      .getBoundingClientRect();
    return {
      insideCard: picker.left >= card.left && picker.right <= card.right,
      horizontalOverflow:
        document.documentElement.scrollWidth -
        document.documentElement.clientWidth,
    };
  });
  expect(geometry.insideCard).toBe(true);
  expect(geometry.horizontalOverflow).toBeLessThanOrEqual(0);
  expect(consoleErrors).toEqual([]);
  expect(pageErrors).toEqual([]);
});

test("audit refresh failure retains the previously loaded records", async ({
  page,
}) => {
  const consoleErrors: string[] = [];
  const pageErrors: string[] = [];
  page.on("console", (message) => {
    if (message.type() === "error") consoleErrors.push(message.text());
  });
  page.on("pageerror", (error) => pageErrors.push(error.message));
  await mockAudits(page);
  let failRefresh = false;
  await page.route("**/api/v2/audits/page**", (route) => {
    if (failRefresh) {
      return route.fulfill({
        status: 503,
        json: {
          code: "audit_temporarily_unavailable",
          message: "refresh failed",
          status: 503,
        },
      });
    }
    return route.fulfill({
      json: {
        items: [
          {
            actor: "service-account:release-bot",
            occurredAt: "2026-08-19T08:00:00Z",
            operation: "get",
            outcome: "resolved",
            repository: "stable-releases",
          },
        ],
      },
    });
  });

  await page.goto("/audits");
  await expect(page.getByText("stable-releases")).toBeVisible();
  failRefresh = true;
  await page.getByRole("button", { name: "刷新" }).click();
  await expect(page.getByText("refresh failed")).toBeVisible();
  await expect(page.getByText("stable-releases")).toBeVisible();
  await expect(page.getByText("加载中…")).toHaveCount(0);
  expect(consoleErrors).toHaveLength(1);
  expect(consoleErrors[0]).toContain("503");
  expect(pageErrors).toEqual([]);
});
