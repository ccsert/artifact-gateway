import { expect, test } from '@playwright/test';

const first = { id: '11111111-1111-4111-8111-111111111111', name: 'images', format: 'oci', state: 'active', version: '1' };
const created = { id: '22222222-2222-4222-8222-222222222222', name: 'conan-proxy', format: 'conan', state: 'active', version: '1' };
const cacheStatus = { object_count: 2, bytes: 4096, pending_candidates: 1, successful_runs: 3, failed_runs: 0, last_completed_at: '2026-07-25T06:00:00Z' };

test('creates, refreshes, disables, and surfaces API errors', async ({ page }) => {
  let repositories = [first];
  let failList = false;
  let createPayload: unknown;
  let cacheCollects = 0;
  await page.route('**/api/v1/operations/cache**', async (route) => {
    const request = route.request();
    if (request.method() === 'POST') { cacheCollects += 1; return route.fulfill({ status: 204 }); }
    return route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ ...cacheStatus, successful_runs: cacheStatus.successful_runs + cacheCollects }) });
  });
  await page.route('**/api/v2/repositories**', async (route) => {
    const request = route.request();
    if (failList && request.method() === 'GET') return route.fulfill({ status: 500, contentType: 'application/problem+json', body: JSON.stringify({ message: 'Inventory is unavailable.' }) });
    if (request.method() === 'POST') { createPayload = request.postDataJSON(); repositories = [...repositories, created]; return route.fulfill({ status: 201, contentType: 'application/json', body: JSON.stringify(created) }); }
    if (request.method() === 'DELETE') return route.fulfill({ status: 202, contentType: 'application/json', body: JSON.stringify({ id: created.id, state: 'pending' }) });
    const id = request.url().split('/').pop();
    if (id === created.id) return route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ ...created, state: 'deleting', version: '2' }) });
    return route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ items: repositories }) });
  });

  await page.goto('/');
  await expect(page.getByText('images')).toBeVisible();
  const cacheOperations = page.getByRole('region', { name: 'Cache operations' });
  await expect(cacheOperations.getByText('4,096')).toBeVisible();
  await page.getByRole('button', { name: 'Collect cache' }).click();
  await expect(cacheOperations.getByText('4', { exact: true })).toBeVisible();
  expect(cacheCollects).toBe(1);
  await page.getByLabel('Name').fill('conan-proxy');
  await page.getByLabel('Format').selectOption('conan');
  await page.getByRole('button', { name: 'Create repository' }).click();
  await expect(page.getByRole('row', { name: 'conan-proxy CONAN active' })).toBeVisible();
  expect(createPayload).toEqual({ name: 'conan-proxy', format: 'conan' });
  await page.getByRole('button', { name: 'Disable repository' }).click();
  await expect(page.getByText('Disabled')).toBeVisible();
  failList = true;
  await page.getByRole('button', { name: 'Refresh repositories' }).click();
  await expect(page.getByRole('alert')).toContainText('Inventory is unavailable.');
});
