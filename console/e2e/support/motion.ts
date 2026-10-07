import { expect, type Locator } from "@playwright/test";

/** AntD visibility starts before the opening scale has finished. */
export async function waitForModalOpen(dialog: Locator) {
  await expect(dialog).toBeVisible();
  await expect(dialog).not.toHaveClass(/ant-zoom-(?:appear|enter)/);
}
