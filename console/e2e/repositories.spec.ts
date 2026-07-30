import { expect, test } from '@playwright/test';

test('browses managed Maven repositories through the generated client', async ({ page }) => {
  await page.route('**/api/v2/repositories**', async (route) => {
    await route.fulfill({
      contentType: 'application/json',
      body: JSON.stringify({
        items: [{
          id: 'repo-maven',
          name: 'releases',
          format: 'maven',
          type: 'hosted',
          state: 'active',
          version: '1',
        }],
        nextCursor: '',
      }),
    });
  });

  await page.goto('/repositories');
  await expect(page.getByRole('heading', { name: /仓库|repositories/i })).toBeVisible();
  await expect(page.getByText('releases')).toBeVisible();
  await expect(page.getByText('maven', { exact: true })).toBeVisible();
});
