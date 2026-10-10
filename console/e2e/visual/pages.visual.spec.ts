import { expect, test, type Page } from "@playwright/test";
import { FIXED_NOW } from "./fixtures";
import { mockGateway } from "./mockGateway";

/**
 * Visual baseline for the Console refactor (#270). Structural refactors must
 * leave these screenshots unchanged; intentional design changes update them
 * in the same PR with before/after evidence.
 *
 * Baselines are rendered on Linux inside the Playwright container so local
 * runs and CI compare identical font stacks: `make console-visual`.
 */

interface Surface {
  name: string;
  path: string;
  /** Visible once the page has rendered its fixture data. */
  ready: (page: Page) => ReturnType<Page["getByText"]>;
  prepareScreenshot?: (page: Page) => Promise<void>;
  authenticated?: boolean;
  /** Puts the page into the state to capture once it is ready. */
  prepare?: (page: Page) => Promise<void>;
}

const surfaces: Surface[] = [
  {
    name: "login",
    path: "/login",
    authenticated: false,
    ready: (page) => page.getByRole("button", { name: /登录|Sign in/ }),
  },
  {
    name: "public-browse",
    path: "/browse",
    authenticated: false,
    ready: (page) => page.getByText("maven-releases").first(),
  },
  {
    name: "dashboard",
    path: "/",
    ready: (page) => page.getByText("maven-releases").first(),
    prepareScreenshot: async (page) => {
      // This plot loads on intersection and paints after its React wrapper.
      // A full-page screenshot can otherwise accept an empty canvas.
      await page
        .getByTestId("storage-by-format-chart")
        .scrollIntoViewIfNeeded();
      const canvas = page.getByTestId("ant-design-pie-ready").locator("canvas");
      await expect(canvas).toBeVisible();
      await expect
        .poll(() =>
          canvas.evaluate((element) => {
            const plot = element as HTMLCanvasElement;
            const context = plot.getContext("2d");
            if (!context || !plot.width || !plot.height) return false;
            const pixels = context.getImageData(
              0,
              0,
              plot.width,
              plot.height,
            ).data;
            // The fixture has categorical slices; neutral canvas/background
            // pixels do not establish that those data have been painted.
            for (let offset = 0; offset < pixels.length; offset += 4) {
              if (
                pixels[offset + 3] > 0 &&
                Math.max(
                  pixels[offset],
                  pixels[offset + 1],
                  pixels[offset + 2],
                ) -
                  Math.min(
                    pixels[offset],
                    pixels[offset + 1],
                    pixels[offset + 2],
                  ) >
                  32
              ) {
                return true;
              }
            }
            return false;
          }),
        )
        .toBe(true);
      await page.evaluate(() => window.scrollTo(0, 0));
    },
  },
  {
    name: "repositories",
    path: "/repositories",
    ready: (page) => page.getByText("cargo-hosted").first(),
  },
  {
    name: "groups",
    path: "/groups",
    ready: (page) => page.getByText("maven-public").first(),
  },
  {
    name: "search",
    path: "/search?q=demo",
    ready: (page) => page.getByText("com.acme:demo-service").first(),
  },
  {
    name: "access-control",
    path: "/access",
    ready: (page) => page.getByText("npm-hosted").first(),
  },
  {
    name: "audits",
    path: "/audits",
    ready: (page) => page.getByText("user:bob").first(),
  },
  {
    name: "users",
    path: "/users",
    ready: (page) => page.getByText("Alice Chen").first(),
  },
  {
    name: "service-accounts",
    path: "/service-accounts",
    ready: (page) => page.getByText("ci-publisher").first(),
  },
  {
    name: "operations",
    path: "/operations",
    ready: (page) => page.getByText("nightly-retention").first(),
  },
  {
    name: "command-palette",
    path: "/",
    ready: (page) => page.getByText("maven-releases").first(),
    prepare: async (page) => {
      await page.keyboard.press("ControlOrMeta+k");
      await page.getByRole("combobox", { name: "命令面板" }).fill("maven");
      await expect(
        page.getByRole("option", { name: /maven-central/ }),
      ).toBeVisible();
    },
  },
  {
    name: "system",
    path: "/system",
    ready: (page) => page.getByText("gateway-0").first(),
  },
];

const themes = ["gateway-dark", "gateway-light"] as const;

for (const theme of themes) {
  test.describe(theme, () => {
    for (const surface of surfaces) {
      test(surface.name, async ({ page }) => {
        await page.clock.setFixedTime(new Date(FIXED_NOW));
        await page.addInitScript((themeId) => {
          localStorage.setItem("ag.console.theme.id", themeId);
          localStorage.setItem("ag.console.locale", "zh-CN");
        }, theme);
        const gateway = await mockGateway(page, {
          authenticated: surface.authenticated ?? true,
        });

        await page.goto(surface.path);
        await expect(surface.ready(page)).toBeVisible();
        await surface.prepare?.(page);
        await page.evaluate(() => document.fonts.ready);
        await surface.prepareScreenshot?.(page);

        expect(gateway.unmatched, "requests without a fixture").toEqual([]);
        await expect(page).toHaveScreenshot([theme, `${surface.name}.png`], {
          fullPage: true,
        });
        expect(
          gateway.unmatched,
          "requests during screenshot without a fixture",
        ).toEqual([]);
      });
    }
  });
}
