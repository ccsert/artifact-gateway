import { expect, type Page } from "@playwright/test";

/**
 * Repository detail groups its tasks (#274): a group tab bar plus the tasks of
 * the selected group.
 */
function repositoryNavigation(page: Page) {
  const navigation = page.getByRole("navigation", { name: "仓库任务" });
  return {
    groups: navigation.locator(".ag-repository-tabs").getByRole("tab"),
    group: (name: string) =>
      navigation
        .locator(".ag-repository-tabs")
        .getByRole("tab", { name, exact: true }),
    tasks: navigation.locator(".ag-repository-subtabs").getByRole("tab"),
    task: (name: string) =>
      navigation
        .locator(".ag-repository-subtabs")
        .getByRole("tab", { name, exact: true }),
  };
}

/**
 * Reads the group tabs and the selected group's tasks without opening other
 * groups, so mocked pages never load surfaces the test did not prepare.
 * The full group-to-task mapping is covered by the RepositoryDetail unit tests.
 */
export async function repositoryNavigationState(page: Page) {
  const navigation = repositoryNavigation(page);
  await expect(navigation.groups.first()).toBeVisible();
  return {
    groups: await navigation.groups.allTextContents(),
    tasks: await navigation.tasks.allTextContents(),
  };
}

export async function openRepositoryTask(
  page: Page,
  group: string,
  task: string,
) {
  const navigation = repositoryNavigation(page);
  await navigation.group(group).click();
  await navigation.task(task).click();
  await expect(navigation.task(task)).toHaveAttribute("aria-selected", "true");
}
