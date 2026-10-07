import type { Page } from "@playwright/test";
import { defaultSiteSettings } from "../../src/lib/siteSettings";

/** The app shell reads its theme configuration on every mocked page. */
export async function mockDefaultSiteSettings(page: Page) {
  await page.route("**/api/v2/site-settings", (route) =>
    route.request().method() === "GET"
      ? route.fulfill({ json: defaultSiteSettings })
      : route.continue(),
  );
}
