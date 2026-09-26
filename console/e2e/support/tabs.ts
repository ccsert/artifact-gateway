import { expect, type Page } from "@playwright/test";

/**
 * Asserts a task tab bar spaces its labels by the shared gutter alone.
 *
 * The gap is bounded on both sides — an accidentally huge gap is as much a
 * layout failure as a collapsed one — and the first label has to line up with
 * the surface below it, which is what the tab padding used to break.
 */
export async function expectTabGutter(
  page: Page,
  tabBarSelector: string,
  contentEdgeSelector: string,
) {
  const boxes = await page
    .locator(tabBarSelector)
    .getByRole("tab")
    .evaluateAll((tabs) =>
      tabs.slice(0, 4).map((tab) => {
        const box = tab.getBoundingClientRect();
        return { x: box.x, right: box.x + box.width };
      }),
    );
  expect(boxes.length).toBeGreaterThan(1);
  for (let index = 1; index < boxes.length; index += 1) {
    const gap = boxes[index].x - boxes[index - 1].right;
    expect(gap).toBeGreaterThanOrEqual(23);
    expect(gap).toBeLessThanOrEqual(25);
  }
  const edge = await page.locator(contentEdgeSelector).first().boundingBox();
  expect(edge).not.toBeNull();
  expect(Math.abs(boxes[0].x - (edge?.x ?? 0))).toBeLessThanOrEqual(1);
}
