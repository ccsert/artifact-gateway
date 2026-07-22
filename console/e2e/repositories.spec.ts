import { expect, test } from '@playwright/test';

const first = { id: '11111111-1111-4111-8111-111111111111', name: 'images', format: 'oci', state: 'active', version: '1' };
const created = { id: '22222222-2222-4222-8222-222222222222', name: 'releases', format: 'maven', state: 'active', version: '1' };

test('creates, refreshes, disables, and surfaces API errors', async ({ page }) => {
  let repositories = [first];
  let failList = false;
  await page.route('**/api/v2/repositories**', async (route) => {
    const request = route.request();
    if (failList && request.method() === 'GET') return route.fulfill({ status: 500, contentType: 'application/problem+json', body: JSON.stringify({ message: 'Inventory is unavailable.' }) });
    if (request.method() === 'POST') { repositories = [...repositories, created]; return route.fulfill({ status: 201, contentType: 'application/json', body: JSON.stringify(created) }); }
    if (request.method() === 'DELETE') return route.fulfill({ status: 202, contentType: 'application/json', body: JSON.stringify({ id: created.id, state: 'pending' }) });
    const id = request.url().split('/').pop();
    if (id === created.id) return route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ ...created, state: 'deleting', version: '2' }) });
    return route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ items: repositories }) });
  });

  await page.goto('/');
  await expect(page.getByText('images')).toBeVisible();
  await page.getByLabel('Name').fill('releases');
  await page.getByLabel('Format').selectOption('maven');
  await page.getByRole('button', { name: 'Create repository' }).click();
  await expect(page.getByRole('row', { name: 'releases MAVEN active' })).toBeVisible();
  await page.getByRole('button', { name: 'Disable repository' }).click();
  await expect(page.getByText('Disabled')).toBeVisible();
  failList = true;
  await page.getByRole('button', { name: 'Refresh repositories' }).click();
  await expect(page.getByRole('alert')).toContainText('Inventory is unavailable.');
});
