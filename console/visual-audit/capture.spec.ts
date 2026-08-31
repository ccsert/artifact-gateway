import { test } from "@playwright/test";
import { mockConsole } from "./mock";

// Visual-audit harness (developer tool, excluded from the default e2e run).
// Captures every console page across themes and viewports with a mocked
// management API. Run from console/:
//   npx playwright test --config=visual-audit/playwright.config.ts
// Screenshots land in /tmp/ag-audit/. Prefer the *-viewport.png captures when
// judging fixed-sider pages: fullPage captures shift fixed elements.

const OUT = "/tmp/ag-audit";

const themes = [{ id: "gateway-dark" }, { id: "gateway-light" }];

const PAGES: Array<{ name: string; path: string; themeIds?: string[] }> = [
  { name: "login", path: "/login" },
  { name: "dashboard", path: "/" },
  { name: "repositories", path: "/repositories" },
  { name: "repository-detail", path: "/repositories/rep-oci-001" },
  { name: "search", path: "/search?query=react" },
  { name: "operations", path: "/operations" },
  { name: "groups", path: "/groups" },
  { name: "access", path: "/access" },
  { name: "audits", path: "/audits" },
  { name: "identity-providers", path: "/identity-providers" },
  { name: "site-settings", path: "/site-settings" },
  { name: "keys", path: "/keys" },
  { name: "service-accounts", path: "/service-accounts" },
  { name: "users", path: "/users" },
  { name: "audit-retention", path: "/audit-retention" },
];

const VIEWPORTS = [
  { label: "desktop", width: 1440, height: 900 },
  { label: "mobile", width: 390, height: 844 },
];

for (const theme of themes) {
  for (const vp of VIEWPORTS) {
    for (const target of PAGES) {
      test(`capture ${target.name} [${theme.id} ${vp.label}]`, async ({
        page,
      }) => {
        await mockConsole(page);
        await page.addInitScript(
          ([themeId]) =>
            localStorage.setItem("ag.console.theme.id", themeId as string),
          [theme.id],
        );
        await page.setViewportSize({ width: vp.width, height: vp.height });
        page.on("pageerror", (err) =>
          console.log(`[${target.name}] pageerror:`, err.stack ?? err.message),
        );
        await page.goto(target.path, { waitUntil: "networkidle" });
        await page.waitForTimeout(600);
        // Trigger viewport-lazy charts before capturing the full page.
        await page.evaluate(async () => {
          for (let y = 0; y < document.body.scrollHeight; y += 400) {
            window.scrollTo(0, y);
            await new Promise((r) => setTimeout(r, 40));
          }
          window.scrollTo(0, 0);
        });
        await page.waitForTimeout(600);
        await page.screenshot({
          path: `${OUT}/${theme.id}-${vp.label}-${target.name}.png`,
          fullPage: true,
        });
        await page.screenshot({
          path: `${OUT}/${theme.id}-${vp.label}-${target.name}-viewport.png`,
        });
      });
    }
  }
}

for (const theme of themes) {
  test(`capture login-unauth [${theme.id} desktop]`, async ({ page }) => {
    await mockConsole(page);
    await page.addInitScript((t) => {
      localStorage.removeItem("ag.console.token");
      localStorage.removeItem("ag.console.role");
      localStorage.setItem("ag.console.theme.id", t as string);
    }, theme.id);
    await page.route("**/auth/session", (route) =>
      route.fulfill({ json: { authenticated: false } }),
    );
    await page.setViewportSize({ width: 1440, height: 900 });
    await page.goto("/login", { waitUntil: "networkidle" });
    await page.waitForTimeout(600);
    await page.screenshot({
      path: `${OUT}/${theme.id}-desktop-login-unauth.png`,
    });
  });
}

test("capture public browse [gateway-dark desktop]", async ({ page }) => {
  await mockConsole(page);
  await page.addInitScript(() => {
    localStorage.removeItem("ag.console.token");
    localStorage.removeItem("ag.console.role");
    localStorage.setItem("ag.console.theme.id", "gateway-dark");
  });
  await page.route("**/auth/session", (route) =>
    route.fulfill({ json: { authenticated: false } }),
  );
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goto("/browse", { waitUntil: "networkidle" });
  await page.waitForTimeout(600);
  await page.screenshot({
    path: `${OUT}/gateway-dark-desktop-public-browse.png`,
    fullPage: true,
  });
});
