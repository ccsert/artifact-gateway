import { expect, test, type Page } from "@playwright/test";
import { authenticateAsAdmin } from "./support/auth";

/**
 * The page stack separates its direct children with one gap, so no child may
 * carry its own vertical margin: a child margin either doubles the rhythm or
 * is silently cancelled by the stack's `margin-block: 0 !important`, and both
 * outcomes mean the authored spacing is not the rendered spacing.
 *
 * Measurements are polled because a page renders placeholders first and the
 * real surfaces a tick later; polling asserts the settled layout instead of a
 * frame caught mid-swap.
 */
async function measureStack(page: Page) {
  return page
    .locator(".ag-page-stack")
    .first()
    .evaluate((stack) => {
      const children = Array.from(stack.children) as HTMLElement[];
      // The stack zeroes child margins with !important, so an authored margin
      // class is dead code that lies about the rendered spacing; catch it here.
      const marginClasses = children
        .map((child) => child.className)
        .filter((name) => /\bm[tbxy]?-\d/.test(String(name)));
      const boxes = children.slice(0, 4).map((child) => {
        const box = child.getBoundingClientRect();
        return { top: box.top, bottom: box.bottom };
      });
      const badGaps: number[] = [];
      for (let index = 1; index < boxes.length; index += 1) {
        const gap = boxes[index].top - boxes[index - 1].bottom;
        const related = gap >= 15 && gap <= 17;
        const primary = gap >= 23 && gap <= 25;
        if (!related && !primary) badGaps.push(Math.round(gap * 10) / 10);
      }
      const overflow = document.body.scrollWidth - document.body.clientWidth;
      return { marginClasses, badGaps, overflow };
    });
}

async function expectStackRhythm(page: Page) {
  await expect
    .poll(async () => (await measureStack(page)).marginClasses, {
      timeout: 5000,
    })
    .toEqual([]);
  await expect
    .poll(async () => (await measureStack(page)).badGaps, {
      timeout: 5000,
    })
    .toEqual([]);
  await expect
    .poll(async () => (await measureStack(page)).overflow, {
      timeout: 5000,
    })
    .toBe(0);
}

async function mockIdentityAndSearch(page: Page) {
  await authenticateAsAdmin(page);
  await page.route("**/api/v2/users**", (route) =>
    route.fulfill({ json: { items: [] } }),
  );
  await page.route("**/api/v2/api-keys**", (route) =>
    route.fulfill({ json: { items: [] } }),
  );
  await page.route("**/api/v2/repositories**", (route) =>
    route.fulfill({ json: { items: [] } }),
  );
  await page.route("**/api/v2/artifact-search**", (route) =>
    route.fulfill({ json: { items: [], nextPageToken: undefined } }),
  );
}

test("identity and search pages keep one rhythm source", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await mockIdentityAndSearch(page);

  for (const path of ["/users", "/keys", "/search?q=demo"]) {
    await page.goto(path);
    await expect(page.locator(".ag-page-stack").first()).toBeVisible();
    await expectStackRhythm(page);
  }

  await page.setViewportSize({ width: 390, height: 844 });
  for (const path of ["/users", "/keys", "/search?q=demo"]) {
    await page.goto(path);
    await expectStackRhythm(page);
  }
});
