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
