import { expect, test, type Page } from "@playwright/test";
import { authenticateAsAdmin } from "./support/auth";

interface RGB {
  b: number;
  g: number;
  r: number;
}

function parseRGB(color: string): RGB {
  const [r, g, b] = color.match(/[\d.]+/g)?.map(Number) ?? [];
  if ([r, g, b].some((channel) => channel === undefined)) {
    throw new Error(`Unsupported color: ${color}`);
  }
  return { r, g, b };
}

function luminance({ r, g, b }: RGB) {
  const linear = [r, g, b].map((channel) => {
    const value = channel / 255;
    return value <= 0.04045 ? value / 12.92 : ((value + 0.055) / 1.055) ** 2.4;
  });
  return linear[0] * 0.2126 + linear[1] * 0.7152 + linear[2] * 0.0722;
}

function contrast(foreground: string, background: string) {
  const values = [
    luminance(parseRGB(foreground)),
    luminance(parseRGB(background)),
  ].sort((a, b) => b - a);
  return (values[0] + 0.05) / (values[1] + 0.05);
}

async function openInjectedRuntimeError(page: Page, theme: "dark" | "light") {
  await page.addInitScript((colorMode) => {
    localStorage.setItem("ag.console.theme", colorMode);
  }, theme);
  await authenticateAsAdmin(page);
  await page.route("**/api/v2/repositories**", (route) =>
    route.fulfill({
      json: {
        items: [
          {
            id: "repo-oci",
            name: "runtime-images",
            format: "oci",
            type: "hosted",
            state: "active",
            version: "1",
          },
        ],
      },
    }),
  );
  await page.route("**/api/v2/groups**", (route) =>
    route.fulfill({ json: { items: [] } }),
  );
  await page.route("**/api/v2/audits**", (route) =>
    route.fulfill({ json: [] }),
  );
  await page.route("**/api/v2/repository-capacities**", (route) =>
    route.fulfill({
      json: [
        {
          repositoryId: "repo-oci",
          format: "oci",
          usedBytes: 4 * 1024 * 1024,
          objectCount: 16,
          quotaBytes: 0,
        },
      ],
    }),
  );
  await page.route(
    "**/src/components/dashboard-charts/DashboardPiePlot.tsx*",
    (route) =>
      route.fulfill({
        body: `throw new TypeError("Injected route failure");\nexport default null;`,
        contentType: "application/javascript",
      }),
  );

  await page.goto("/");
  await page.getByTestId("storage-by-format-chart").scrollIntoViewIfNeeded();
  await expect(
    page.getByRole("heading", { name: "页面加载失败" }),
  ).toBeVisible();
}

for (const scenario of [
  { height: 1000, name: "desktop light", theme: "light", width: 1440 },
  { height: 844, name: "mobile dark", theme: "dark", width: 390 },
] as const) {
  test(`route error page stays readable on ${scenario.name}`, async ({
    page,
  }, testInfo) => {
    await page.setViewportSize({
      width: scenario.width,
      height: scenario.height,
    });
    await openInjectedRuntimeError(page, scenario.theme);

    const card = page.locator(".ag-route-error-card");
    const title = page.getByRole("heading", { name: "页面加载失败" });
    const description = card.locator(".ag-route-error-description");
    const styles = await card.evaluate((element) => {
      const cardStyle = getComputedStyle(element);
      const titleStyle = getComputedStyle(
        element.querySelector("h1") as HTMLElement,
      );
      const descriptionStyle = getComputedStyle(
        element.querySelector(".ag-route-error-description") as HTMLElement,
      );
      return {
        background: cardStyle.backgroundColor,
        description: descriptionStyle.color,
        title: titleStyle.color,
      };
    });

    await expect(card).toBeVisible();
    await expect(title).toBeVisible();
    await expect(description).toBeVisible();
    await expect(page.getByText("技术详情")).toBeVisible();
    await expect(page.getByRole("button", { name: "重新加载" })).toBeVisible();
    await expect(
      page.getByRole("link", { name: "浏览公开制品" }),
    ).toBeVisible();
    expect(contrast(styles.title, styles.background)).toBeGreaterThanOrEqual(
      4.5,
    );
    expect(
      contrast(styles.description, styles.background),
    ).toBeGreaterThanOrEqual(4.5);
    expect(
      await page
        .locator("html")
        .evaluate((element) =>
          Math.max(0, element.scrollWidth - element.clientWidth),
        ),
    ).toBe(0);

    if (process.env.CAPTURE_LAYOUT_EVIDENCE === "1") {
      await page.screenshot({
        path: testInfo.outputPath(`route-error-${scenario.theme}.png`),
        fullPage: true,
      });
    }
  });
}
