import { expect, test, type Locator, type Page } from "@playwright/test";
import { defaultConsoleThemes } from "../src/lib/consoleTheme";
import { authenticateAsAdmin } from "./support/auth";

function captureRuntimeErrors(page: Page) {
  const errors: string[] = [];
  page.on("pageerror", (error) => errors.push(error.message));
  page.on("console", (message) => {
    if (message.type() === "error") errors.push(message.text());
  });
  return errors;
}

async function mockAccessControl(page: Page) {
  await authenticateAsAdmin(page);
  await page.addInitScript(() => {
    localStorage.setItem("ag.console.theme", "light");
  });
  await page.route("**/api/v2/site-settings", (route) =>
    route.fulfill({
      json: {
        version: 1,
        siteName: "Artifact Gateway",
        logoUrl: "",
        brandMark: "AG",
        enabledThemeIds: [
          "gateway-dark",
          "gateway-light",
          "aerok-dark",
          "aerok-light",
        ],
        defaultThemeId: "gateway-dark",
        availableThemes: defaultConsoleThemes,
      },
    }),
  );
  await page.route("**/api/v2/repository-grants**", (route) =>
    route.fulfill({ json: [] }),
  );
  await page.route("**/api/v2/anonymous-access-policy**", (route) =>
    route.fulfill({ json: { enabled: true, version: "7" } }),
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
    route.fulfill({
      json: {
        items: [
          {
            id: "repo-public",
            name: "public-releases",
            format: "raw",
            type: "hosted",
            state: "active",
            anonymousRead: true,
          },
        ],
      },
    }),
  );
  await page.route("**/api/v2/authorization-roles**", (route) =>
    route.fulfill({ json: [] }),
  );
  await page.route("**/api/v2/authorization-templates**", (route) =>
    route.fulfill({ json: [] }),
  );
}

async function verticalGap(upper: Locator, lower: Locator) {
  const [upperBox, lowerBox] = await Promise.all([
    upper.boundingBox(),
    lower.boundingBox(),
  ]);
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

test("public access layers use coherent light-theme surfaces", async ({
  page,
}, testInfo) => {
  await page.setViewportSize({ width: 1280, height: 900 });
  const runtimeErrors = captureRuntimeErrors(page);
  await mockAccessControl(page);
  await page.goto("/access?tab=policies");

  const card = page.locator(".ag-public-access-card");
  const layers = card.locator(".ag-public-access-layer");
  await expect(page.getByText("公开访问边界")).toBeVisible();
  await expect(layers).toHaveCount(3);
  await expect(card.locator(".ag-public-access-header")).toHaveCSS(
    "background-color",
    "rgb(255, 255, 255)",
  );
  await expect(card.locator(".ag-public-access-summary")).toHaveCSS(
    "background-color",
    "rgb(255, 255, 255)",
  );
  for (const layer of await layers.all()) {
    await expect(layer).toHaveCSS("background-color", "rgb(250, 250, 250)");
  }
  expect(await horizontalOverflow(page)).toBe(0);
  expect(runtimeErrors).toEqual([]);

  if (process.env.CAPTURE_LAYOUT_EVIDENCE === "1") {
    await page.screenshot({
      path: testInfo.outputPath("access-control-light.png"),
      fullPage: true,
    });
  }

  await page.getByRole("button", { name: /选择主题.*Gateway Light/ }).click();
  await page.getByRole("menuitem", { name: /Gateway Dark/ }).click();
  await expect(page.locator("html")).toHaveAttribute("data-theme", "dark");
  await expect(card.locator(".ag-public-access-header")).toHaveCSS(
    "background-color",
    "rgb(20, 20, 23)",
  );
  for (const layer of await layers.all()) {
    await expect(layer).toHaveCSS("background-color", "rgb(20, 20, 23)");
  }
  expect(runtimeErrors).toEqual([]);
});

test("public access layers stack on mobile with bounded page rhythm", async ({
  page,
}, testInfo) => {
  await page.setViewportSize({ width: 390, height: 844 });
  const runtimeErrors = captureRuntimeErrors(page);
  await mockAccessControl(page);
  await page.goto("/access?tab=policies");

  const card = page.locator(".ag-public-access-card");
  const layers = card.locator(".ag-public-access-layer");
  const followingCard = card.locator("xpath=following-sibling::*[1]");
  await expect(layers).toHaveCount(3);
  const boxes = await layers.evaluateAll((elements) =>
    elements.map((element) => {
      const box = element.getBoundingClientRect();
      return {
        left: box.left,
        right: box.right,
        top: box.top,
        bottom: box.bottom,
      };
    }),
  );
  expect(boxes[1].top).toBeGreaterThanOrEqual(boxes[0].bottom);
  expect(boxes[2].top).toBeGreaterThanOrEqual(boxes[1].bottom);
  expect(boxes.map((box) => Math.round(box.left))).toEqual([
    Math.round(boxes[0].left),
    Math.round(boxes[0].left),
    Math.round(boxes[0].left),
  ]);
  await expect
    .poll(() => verticalGap(card, followingCard))
    .toBeGreaterThanOrEqual(16);
  await expect
    .poll(() => verticalGap(card, followingCard))
    .toBeLessThanOrEqual(18);
  expect(await horizontalOverflow(page)).toBe(0);
  expect(runtimeErrors).toEqual([]);

  if (process.env.CAPTURE_LAYOUT_EVIDENCE === "1") {
    await page.screenshot({
      path: testInfo.outputPath("access-control-mobile.png"),
      fullPage: true,
    });
  }
});
