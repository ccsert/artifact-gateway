import { expect, test } from "@playwright/test";
import { mockGateway } from "./visual/mockGateway";
import { repositoryPage, scheduledTasks } from "./visual/fixtures";

for (const locale of ["zh-CN", "en-US"] as const) {
  for (const theme of ["gateway-dark", "gateway-light"] as const) {
    test(`scheduled task columns and actions fit at desktop width (${locale}, ${theme})`, async ({
      page,
    }) => {
      await page.setViewportSize({ width: 1440, height: 900 });
      await page.addInitScript(
        ({ locale, theme }) => {
          localStorage.setItem("ag.console.locale", locale);
          localStorage.setItem("ag.console.theme.id", theme);
        },
        { locale, theme },
      );
      await mockGateway(page, { authenticated: true });
      const task = scheduledTasks[0];
      const repositoryName = "repository-with-a-long-unbroken-name-".repeat(3);
      await page.route("**/api/v2/repositories?**", (route) =>
        route.fulfill({
          json: {
            ...repositoryPage,
            items: repositoryPage.items.map((repository) =>
              repository.id === task.repositoryId
                ? { ...repository, name: repositoryName }
                : repository,
            ),
          },
        }),
      );
      await page.goto("/operations");
      const row = page.getByRole("row").filter({ hasText: task.name });
      await expect(row).toBeVisible();
      await page.evaluate(() => document.fonts.ready);
      const widths = await row.getByRole("cell").evaluateAll((cells) =>
        cells.map((cell) => ({
          width: cell.clientWidth,
          contentWidth: cell.scrollWidth,
        })),
      );
      for (const cell of widths)
        expect(cell.contentWidth).toBeLessThanOrEqual(cell.width + 1);
      const more = row.getByRole("button", {
        name:
          locale === "zh-CN"
            ? `${task.name} 的更多操作`
            : `More actions for ${task.name}`,
      });
      await expect(more).toBeInViewport({ ratio: 1 });
      await more.click();
      await expect(
        page.getByRole("menuitem", {
          name: locale === "zh-CN" ? "编辑" : "Edit",
        }),
      ).toBeVisible();
    });
  }
}
