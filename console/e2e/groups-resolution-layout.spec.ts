import { expect, test } from "@playwright/test";
import { defaultConsoleThemes } from "../src/lib/consoleTheme";
import { authenticateAsAdmin } from "./support/auth";

const groupId = "11111111-1111-4111-8111-111111111111";
const members = Array.from({ length: 12 }, (_, i) => ({
  repositoryId: `22222222-2222-4222-8222-${String(i).padStart(12, "0")}`,
  repositoryName: `engineering-release-artifacts-with-a-long-name-${i}`,
  type: i < 6 ? "hosted" : "proxy",
  configuredPosition: i < 6 ? i + 6 : i - 6,
  resolutionOrder: i + 1,
}));

for (const width of [1440, 390]) {
  test(`Group resolution preserves server order and geometry at ${width}px`, async ({
    page,
  }, testInfo) => {
    await page.setViewportSize({ width, height: 900 });
    await page.emulateMedia({ reducedMotion: "reduce" });
    const errors: string[] = [];
    page.on("pageerror", (error) => errors.push(error.message));
    page.on("console", (message) => {
      if (message.type() === "error") errors.push(message.text());
    });
    let inspected = 0;
    await page.route("**/api/v2/**", (route) => {
      const path = new URL(route.request().url()).pathname;
      if (path.endsWith("/site-settings"))
        return route.fulfill({
          json: {
            version: "1",
            siteName: "Artifact Gateway",
            logoUrl: "",
            brandMark: "AG",
            enabledThemeIds: defaultConsoleThemes.map((theme) => theme.id),
            defaultThemeId: "gateway-dark",
            availableThemes: defaultConsoleThemes,
          },
        });
      if (path.endsWith("/resolution")) {
        inspected++;
        return route.fulfill({
          json: {
            groupId,
            groupVersion: "1",
            format: "raw",
            strategy: "hosted_first",
            excludedMemberCount: 0,
            members,
          },
        });
      }
      if (path.endsWith("/groups"))
        return route.fulfill({
          json: {
            items: [
              {
                id: groupId,
                name: "release-group",
                format: "raw",
                anonymousRead: false,
                version: "1",
                members: members.map((member) => ({
                  repositoryId: member.repositoryId,
                  position: member.configuredPosition,
                })),
              },
            ],
          },
        });
      if (path.endsWith("/repositories"))
        return route.fulfill({ json: { items: [] } });
      if (path.endsWith("/formats"))
        return route.fulfill({
          json: {
            items: [
              {
                format: "raw",
                repositoryTypes: ["hosted", "proxy"],
                groupSupported: true,
              },
            ],
          },
        });
      return route.fulfill({ json: { items: [] } });
    });
    await authenticateAsAdmin(page);
    await page.goto("/groups");
    const trigger = page.getByRole("button", { name: "解析顺序", exact: true });
    await expect(trigger).toBeVisible();
    const metric = await page.locator(".ag-metric-strip").boundingBox();
    const primary = await page.locator(".ag-page-primary").boundingBox();
    const pageGap = primary!.y - metric!.y - metric!.height;
    expect(pageGap).toBeGreaterThanOrEqual(24);
    expect(pageGap).toBeLessThanOrEqual(26);
    await trigger.click();
    const dialog = page.getByRole("dialog", {
      name: "解析顺序：release-group",
    });
    const rows = dialog.getByTestId("resolution-member");
    await expect(rows).toHaveCount(12);
    await expect(rows.first().getByRole("link")).toHaveText(
      members[0].repositoryName,
    );
    await expect(rows.first().getByText("配置位置 7")).toBeVisible();
    const body = dialog.locator(".ag-group-resolution");
    // Read both rectangles in one browser frame. A transform can be "none"
    // before rc-motion starts its enter phase, so a one-off CSS check can race
    // the modal's scale animation on a busy CI runner.
    await expect(async () => {
      const geometry = await body.evaluate((element) => {
        const dialogElement = element.closest('[role="dialog"]')!;
        const style = getComputedStyle(dialogElement);
        const explanation = element.querySelector(":scope > p")!;
        const list = element.querySelector("ol")!;
        return {
          gap:
            list.getBoundingClientRect().top -
            explanation.getBoundingClientRect().bottom,
          transform: style.transform,
          opacity: style.opacity,
        };
      });
      expect(geometry.transform).toBe("none");
      expect(geometry.opacity).toBe("1");
      expect(geometry.gap).toBeGreaterThanOrEqual(16);
      expect(geometry.gap).toBeLessThanOrEqual(18);
    }).toPass();
    const rect = await dialog.boundingBox();
    expect(rect!.x).toBeGreaterThanOrEqual(0);
    expect(rect!.x + rect!.width).toBeLessThanOrEqual(width);
    expect(rect!.y).toBeGreaterThanOrEqual(0);
    expect(rect!.y + rect!.height).toBeLessThanOrEqual(900);
    expect(
      await body.evaluate(
        (element) => element.scrollWidth <= element.clientWidth,
      ),
    ).toBe(true);
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= innerWidth,
      ),
    ).toBe(true);
    await page.screenshot({
      path: testInfo.outputPath(`group-resolution-${width}.png`),
      fullPage: true,
      animations: "disabled",
    });
    await rows.last().scrollIntoViewIfNeeded();
    await expect(rows.last().getByRole("link")).toBeInViewport();
    await expect(
      dialog.getByRole("button", { name: "关闭", exact: true }),
    ).toBeInViewport();
    await dialog.getByRole("button", { name: "刷新", exact: true }).click();
    await expect.poll(() => inspected).toBe(2);
    await page.keyboard.press("Escape");
    await expect(dialog).not.toBeVisible();
    await expect(trigger).toBeFocused();
    expect(errors).toEqual([]);
  });
}
