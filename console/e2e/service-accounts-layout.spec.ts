import { expect, test, type Locator, type Page } from "@playwright/test";
import { authenticateAsAdmin } from "./support/auth";

const serviceAccountId = "11111111-1111-4111-8111-111111111111";

function captureRuntimeErrors(page: Page) {
  const errors: string[] = [];
  page.on("pageerror", (error) => errors.push(error.message));
  page.on("console", (message) => {
    if (message.type() === "error") errors.push(message.text());
  });
  return errors;
}

async function openServiceAccounts(page: Page) {
  await authenticateAsAdmin(page);
  await page.route("**/api/v2/service-accounts**", (route) => {
    const pathname = new URL(route.request().url()).pathname;
    if (pathname.endsWith("/credentials")) {
      return route.fulfill({
        json: {
          items: [
            {
              id: "22222222-2222-4222-8222-222222222222",
              serviceAccountId,
              name: "jenkins-blue",
              roles: [],
              createdAt: "2026-08-18T00:00:00Z",
              expiresAt: "2026-11-16T00:00:00Z",
            },
          ],
        },
      });
    }
    return route.fulfill({
      json: {
        items: [
          {
            id: serviceAccountId,
            name: "pipeone-ci",
            description: "Publishes PipeOne release artifacts",
            state: "active",
            createdAt: "2026-08-18T00:00:00Z",
            updatedAt: "2026-08-18T00:00:00Z",
            version: "3",
          },
        ],
      },
    });
  });

  await page.goto("/service-accounts");
  await expect(page.getByRole("heading", { name: "服务账号" })).toBeVisible();
  await expect(page.getByText("jenkins-blue", { exact: true })).toBeVisible();
}

async function verticalGap(upper: Locator, lower: Locator) {
  const upperBox = await upper.boundingBox();
  const lowerBox = await lower.boundingBox();
  if (!upperBox || !lowerBox) return -1;
  return Math.round(lowerBox.y - (upperBox.y + upperBox.height));
}

async function horizontalOverflow(page: Page) {
  return page
    .locator("html")
    .evaluate((element) =>
      Math.max(0, element.scrollWidth - element.clientWidth),
    );
}

test("service accounts keep desktop page surfaces and columns separated", async ({
  page,
}, testInfo) => {
  await page.setViewportSize({ width: 1280, height: 900 });
  const runtimeErrors = captureRuntimeErrors(page);
  await openServiceAccounts(page);

  const info = page.locator(".ant-alert").filter({
    hasText: "机器身份与凭据分离",
  });
  const metrics = page.locator(".ag-metric-strip");
  const workspace = page.locator(".ag-service-account-workspace");
  const cards = workspace.locator(":scope > .ag-card");
  await expect(workspace).toHaveCSS("opacity", "1");

  await expect.soft
    .poll(() => verticalGap(info, metrics))
    .toBeGreaterThanOrEqual(16);
  await expect.soft
    .poll(() => verticalGap(info, metrics))
    .toBeLessThanOrEqual(18);
  await expect.soft
    .poll(() => verticalGap(metrics, workspace))
    .toBeGreaterThanOrEqual(24);
  await expect.soft
    .poll(() => verticalGap(metrics, workspace))
    .toBeLessThanOrEqual(26);
  await expect(cards).toHaveCount(2);
  const [principalCard, credentialCard] = await Promise.all([
    cards.nth(0).boundingBox(),
    cards.nth(1).boundingBox(),
  ]);
  expect(principalCard).not.toBeNull();
  expect(credentialCard).not.toBeNull();
  const columnGap =
    credentialCard!.x - (principalCard!.x + principalCard!.width);
  expect(columnGap).toBeGreaterThanOrEqual(16);
  expect(columnGap).toBeLessThanOrEqual(18);
  expect(await horizontalOverflow(page)).toBe(0);
  expect.soft(runtimeErrors).toEqual([]);

  if (process.env.CAPTURE_LAYOUT_EVIDENCE === "1") {
    await page.screenshot({
      path: testInfo.outputPath("service-accounts-desktop.png"),
      fullPage: true,
    });
  }
});

test("service account cards form a separated mobile flow without overflow", async ({
  page,
}, testInfo) => {
  await page.setViewportSize({ width: 390, height: 844 });
  const runtimeErrors = captureRuntimeErrors(page);
  await openServiceAccounts(page);

  const workspace = page.locator(".ag-service-account-workspace");
  const cards = workspace.locator(":scope > .ag-card");
  await expect(workspace).toHaveCSS("opacity", "1");
  await expect(cards).toHaveCount(2);
  await expect.soft
    .poll(() => verticalGap(cards.nth(0), cards.nth(1)))
    .toBeGreaterThanOrEqual(16);
  await expect.soft
    .poll(() => verticalGap(cards.nth(0), cards.nth(1)))
    .toBeLessThanOrEqual(18);
  expect(await horizontalOverflow(page)).toBe(0);
  expect.soft(runtimeErrors).toEqual([]);

  if (process.env.CAPTURE_LAYOUT_EVIDENCE === "1") {
    await page.screenshot({
      path: testInfo.outputPath("service-accounts-mobile.png"),
      fullPage: true,
    });
  }
});
