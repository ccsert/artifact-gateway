import { expect, test, type Page } from "@playwright/test";
import { defaultConsoleThemes } from "../src/lib/consoleTheme";
import { authenticateAsAdmin } from "./support/auth";

/**
 * A dialog whose body already caps its own height must not gain a second
 * scrolling layer from the table inside it: two scrollers stacked in one
 * dialog fight over the wheel, and the table's own scrollbar hides rows the
 * reader has no way to reach without noticing.
 */
async function expectSingleScrollLayer(page: Page, viewportHeight: number) {
  const layers = await page
    .locator(".ant-modal")
    .first()
    .evaluate((dialog) => {
      const body = dialog.querySelector(
        ".ant-modal-body",
      ) as HTMLElement | null;
      const scrolls = (node: Element) => {
        const overflowY = getComputedStyle(node).overflowY;
        return (
          (overflowY === "auto" || overflowY === "scroll") &&
          node.scrollHeight > node.clientHeight + 1
        );
      };
      const nested = Array.from(dialog.querySelectorAll("*"))
        .filter((node) => node !== body && scrolls(node))
        .map((node) => String(node.className).slice(0, 80));
      return {
        nested,
        bodyScrolls: body ? scrolls(body) : false,
        fixedHeightTableBodies:
          dialog.querySelectorAll(".ant-table-body").length,
        dialogBox: dialog.getBoundingClientRect(),
      };
    });

  expect(layers.fixedHeightTableBodies).toBe(0);
  expect(layers.bodyScrolls).toBe(true);
  expect(layers.nested).toEqual([]);
  expect(layers.dialogBox.top).toBeGreaterThanOrEqual(0);
  expect(layers.dialogBox.bottom).toBeLessThanOrEqual(viewportHeight);
  expect(
    await page.evaluate(
      () => document.body.scrollWidth - document.body.clientWidth,
    ),
  ).toBe(0);
  return layers.dialogBox;
}

async function mockPoliciesTab(page: Page) {
  await authenticateAsAdmin(page);
  await page.route("**/api/v2/site-settings", (route) =>
    route.fulfill({
      json: {
        version: 1,
        siteName: "Artifact Gateway",
        logoUrl: "",
        brandMark: "AG",
        enabledThemeIds: ["gateway-dark"],
        defaultThemeId: "gateway-dark",
        availableThemes: defaultConsoleThemes,
      },
    }),
  );
  await page.route("**/api/v2/users**", (route) =>
    route.fulfill({ json: { items: [] } }),
  );
  await page.route("**/api/v2/api-keys**", (route) =>
    route.fulfill({ json: { items: [] } }),
  );
  await page.route("**/api/v2/service-accounts**", (route) =>
    route.fulfill({ json: { items: [] } }),
  );
  await page.route("**/api/v2/repositories**", (route) =>
    route.fulfill({ json: { items: [] } }),
  );
  await page.route("**/api/v2/repository-grants**", (route) =>
    route.fulfill({ json: [] }),
  );
  await page.route("**/api/v2/anonymous-access-policy**", (route) =>
    route.fulfill({ json: { enabled: false, version: "1" } }),
  );
  await page.route("**/api/v2/authorization-roles**", (route) =>
    route.fulfill({ json: [] }),
  );
  await page.route("**/api/v2/authorization-templates**", (route) =>
    route.fulfill({ json: [] }),
  );
}

async function openTemplateEditor(page: Page, rules: number) {
  await page.goto("/access?tab=policies");
  await page.getByRole("button", { name: /新建模板/ }).click();
  const dialog = page.locator(".ant-modal").first();
  await expect(dialog).toBeVisible();
  const addRule = dialog.getByRole("button", { name: /添加规则/ });
  for (let index = 1; index < rules; index += 1) {
    await addRule.click();
  }
  await expect(dialog.locator(".ant-table-tbody tr.ant-table-row")).toHaveCount(
    rules,
  );
  return dialog;
}

test("the template editor dialog scrolls in one layer at the wide tier", async ({
  page,
}, testInfo) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  const runtimeErrors: string[] = [];
  page.on("pageerror", (error) => runtimeErrors.push(error.message));
  page.on("console", (message) => {
    if (message.type() === "error") runtimeErrors.push(message.text());
  });
  await mockPoliciesTab(page);

  const dialog = await openTemplateEditor(page, 8);
  // A dialog that embeds a table takes the wide tier instead of its own width.
  expect(Math.round((await dialog.boundingBox())!.width)).toBe(1152);
  const box = await expectSingleScrollLayer(page, 900);
  expect(box.left).toBeGreaterThanOrEqual(0);
  expect(box.right).toBeLessThanOrEqual(1440);
  expect(runtimeErrors).toEqual([]);

  if (process.env.CAPTURE_LAYOUT_EVIDENCE === "1") {
    await page.screenshot({
      path: testInfo.outputPath("template-dialog-desktop.png"),
    });
  }
});

test("the template editor dialog stays inside a narrow viewport", async ({
  page,
}, testInfo) => {
  await page.setViewportSize({ width: 390, height: 844 });
  const runtimeErrors: string[] = [];
  page.on("pageerror", (error) => runtimeErrors.push(error.message));
  page.on("console", (message) => {
    if (message.type() === "error") runtimeErrors.push(message.text());
  });
  await mockPoliciesTab(page);

  await openTemplateEditor(page, 6);
  const box = await expectSingleScrollLayer(page, 844);
  expect(box.width).toBeLessThanOrEqual(390);
  expect(box.left).toBeGreaterThanOrEqual(0);
  expect(box.right).toBeLessThanOrEqual(390);
  expect(runtimeErrors).toEqual([]);

  if (process.env.CAPTURE_LAYOUT_EVIDENCE === "1") {
    await page.screenshot({
      path: testInfo.outputPath("template-dialog-mobile.png"),
    });
  }
});
