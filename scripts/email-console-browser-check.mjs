// Invoked by TestPostgresEmailConsoleBrowserTLS with a disposable real API/PG/TLS sink.
import { createRequire } from 'node:module';
import { spawn } from 'node:child_process';
import fs from 'node:fs/promises';
import path from 'node:path';
import net from 'node:net';
const require = createRequire(new URL('../console/package.json', import.meta.url));
const { chromium, expect } = require('@playwright/test');
const apiURL = process.env.EMAIL_CONSOLE_API;
if (!apiURL || new URL(apiURL).hostname !== '127.0.0.1') throw new Error('Owned loopback fixture API is required');
const port = Number(process.env.EMAIL_CONSOLE_BROWSER_PORT || 4196);
// Fail if the selected port belongs to another task, before starting Vite.
const probe = net.createServer();
await new Promise((resolve, reject) => { probe.once('error', reject); probe.listen(port, '127.0.0.1', resolve); });
await new Promise(resolve => probe.close(resolve));
const output = path.resolve('.impeccable/review');
await fs.mkdir(output, { recursive: true });
const vite = spawn(process.execPath, [path.resolve('console/node_modules/vite/bin/vite.js'), '--host', '127.0.0.1', '--port', String(port), '--strictPort'], { cwd: path.resolve('console'), env: { ...process.env, VITE_GATEWAY_PROXY_TARGET: apiURL }, stdio: 'ignore' });
const origin = `http://127.0.0.1:${port}`;
let browser;
try {
  let ready = false;
  for (let i = 0; i < 100; i++) { try { ready = (await fetch(origin)).ok; } catch {} if (ready) break; await new Promise(r => setTimeout(r, 100)); }
  await new Promise(r => setTimeout(r, 200));
  if (vite.exitCode !== null) throw new Error('Owned Vite exited before readiness');
  if (!ready) throw new Error('Owned Vite did not start');
  browser = await chromium.launch();
  const context = await browser.newContext({ baseURL: origin, extraHTTPHeaders: { Authorization: 'Bearer admin-secret' }, viewport: { width: 1440, height: 1000 } });
  const page = await context.newPage();
  const expectedHTTP = []; let expectedConflictURL;
  const errors = []; const external = []; const keys = []; let sends = 0;
  page.on('pageerror', e => errors.push(e.message));
  page.on('console', m => { if (m.type() === 'error') { if (m.text().includes('status of 412') && m.location().url === expectedConflictURL) expectedHTTP.push(412); else errors.push(m.text()); } });
  page.on('request', r => { if (r.url().startsWith('http') && !r.url().startsWith(origin)) external.push(new URL(r.url()).origin); if (r.url().endsWith('email-notifications:test')) { sends++; keys.push(r.headers()['idempotency-key']); } });
  await page.goto('/system?tab=email');
  await expect(page.getByText('尚无邮件目标', { exact: true })).toBeVisible();
  await page.getByRole('button', { name: '新建邮件目标' }).click();
  await page.getByLabel('目标名称', { exact: true }).fill('Owned synthetic operations');
  await page.getByLabel('收件人地址', { exact: true }).fill('synthetic-recipient@example.test');
  await page.getByRole('button', { name: '保存停用目标' }).click();
  await expect(page.getByText(/邮件目标已保存/)).toBeVisible();
  const api = context.request;
  const listResponse = await api.get('/api/v2/email-targets'); expect(listResponse.headers()['cache-control']).toBe('no-store');
  const targets = await listResponse.json(); const target = targets.find(x => x.name === 'Owned synthetic operations');
  expect(target.enabled).toBe(false); expect(target.recipientConfigured).toBe(true); expect(target).not.toHaveProperty('recipient');
  await page.getByRole('button', { name: '预览模板' }).click();
  await expect(page.frameLocator('.ag-email-preview').getByRole('heading', { name: '临时挂载的可用空间偏低', exact: true })).toBeVisible();
  expect((await (await api.get(`${apiURL}/__fixture/attempts`)).json()).attempts).toBe(0); expect(sends).toBe(0);
  await page.getByRole('button', { name: '启用 Owned synthetic operations' }).click();
  await page.getByRole('button', { name: '取消', exact: true }).click(); expect((await (await api.get(`/api/v2/email-targets/${target.id}`)).json()).enabled).toBe(false);
  await page.getByRole('button', { name: '启用 Owned synthetic operations' }).click();
  await page.getByRole('button', { name: '确认启用', exact: true }).click(); await expect(page.getByRole('button', { name: '测试 Owned synthetic operations' })).toBeEnabled();
  await page.getByRole('button', { name: '编辑 Owned synthetic operations' }).click(); await expect(page.getByLabel('收件人地址', { exact: true })).toHaveValue('');
  await page.getByLabel('目标名称', { exact: true }).fill('Owned synthetic edited');
  const before = await (await api.get(`/api/v2/email-targets/${target.id}`)).json();
  const competing = await api.put(`/api/v2/email-targets/${target.id}`, { headers: { 'If-Match': before.version }, data: { name: before.name, locale: before.locale, enabled: before.enabled } }); expect(competing.status()).toBe(200);
  await page.getByLabel('收件人地址', { exact: true }).fill('replacement@example.test');
  expectedConflictURL = origin + '/api/v2/email-targets/' + target.id;
  await page.getByRole('button', { name: '保存目标' }).click(); await expect(page.getByText(/配置已变更/)).toBeVisible(); await expect(page.getByLabel('收件人地址', { exact: true })).toHaveValue('');
  expectedConflictURL = undefined;
  await page.getByRole('button', { name: '刷新最新目标并重新编辑' }).click(); await expect(page.getByRole('button', { name: '保存目标' })).toBeEnabled(); await expect(page.getByLabel('目标名称', { exact: true })).toHaveValue('Owned synthetic edited');
  await page.getByRole('button', { name: '保存目标' }).click(); await expect(page.getByText(/邮件目标已保存/)).toBeVisible();
  await page.reload(); await page.getByRole('button', { name: '测试 Owned synthetic edited' }).click();
  await page.getByRole('button', { name: '确认发送合成测试' }).evaluate(el => { el.click(); el.click(); });
  await expect(page.getByText(/合成测试已入队/)).toBeVisible(); expect(sends).toBe(1);
  await expect.poll(async () => (await (await api.get('/api/v2/email-deliveries')).json()).some(x => x.state === 'accepted'), { timeout: 15000 }).toBe(true);
  await page.getByRole('button', { name: '刷新', exact: true }).click(); await expect(page.getByText('SMTP 服务器已接受', { exact: true })).toBeVisible();
  await api.post(`${apiURL}/__fixture/failure`);
  await page.getByRole('button', { name: '测试 Owned synthetic edited' }).click();
  await page.getByRole('combobox', { name: '合成场景', exact: true }).click(); await page.locator('.ant-select-dropdown:visible').getByText('严重', { exact: true }).click();
  await page.getByRole('button', { name: '确认发送合成测试' }).click(); await expect(page.getByText(/合成测试已入队/)).toBeVisible();
  await expect.poll(async () => (await (await api.get('/api/v2/email-deliveries')).json()).some(x => x.state === 'dead' && x.errorCode === 'smtp_permanent_rejection'), { timeout: 15000 }).toBe(true);
  await page.getByRole('button', { name: '刷新', exact: true }).click(); await expect(page.getByText('SMTP 永久拒绝', { exact: true })).toBeVisible();
  await page.getByRole('button', { name: '停用 Owned synthetic edited' }).click(); await page.getByRole('button', { name: '确认停用', exact: true }).click(); await expect(page.getByRole('button', { name: '测试 Owned synthetic edited' })).toBeDisabled();
  expect(sends).toBe(2); expect(new Set(keys).size).toBe(2);
  expect(await page.evaluate(() => JSON.stringify(localStorage) + JSON.stringify(sessionStorage))).not.toContain('synthetic-recipient');
  await context.close();
  let combinations = 0;
  for (const width of [320, 390, 1440]) for (const locale of ['zh-CN', 'en-US']) for (const theme of ['dark', 'light']) {
    const ctx = await browser.newContext({ baseURL: origin, extraHTTPHeaders: { Authorization: 'Bearer admin-secret' }, viewport: { width, height: 1000 }, colorScheme: theme });
    await ctx.addInitScript(({ locale, theme }) => { if (window.top !== window) return; localStorage.setItem('ag.console.locale', locale); localStorage.setItem('ag.console.theme', theme); }, { locale, theme });
    const p = await ctx.newPage(); p.on('pageerror', e => errors.push(e.message)); p.on('console', m => { if (m.type() === 'error') errors.push(m.text()); });
    const zh = locale === 'zh-CN';
    await p.goto('/system?tab=email');
    await expect(p.getByRole('button', { name: zh ? '新建邮件目标' : 'New email target' })).toBeVisible();
    const previewButton = p.getByRole('button', { name: zh ? '预览模板' : 'Preview template' });
    for (const [scenario, label, title] of [['warning', zh ? '警告' : 'Warning', zh ? '临时挂载的可用空间偏低' : 'Temporary mount is running low on space'], ['critical', zh ? '严重' : 'Critical', zh ? '临时挂载的可用空间偏低' : 'Temporary mount is running low on space'], ['resolved', zh ? '恢复' : 'Recovered', zh ? '临时挂载空间已恢复' : 'Temporary mount space has recovered']]) {
      await p.getByRole('combobox', { name: zh ? '预览场景' : 'Preview scenario' }).click(); await p.locator('.ant-select-dropdown:visible').getByText(label, { exact: true }).click();
      await previewButton.click();
      const frame = p.frameLocator('.ag-email-preview');
      await expect(frame.getByRole('heading', { name: title, exact: true })).toBeVisible();
      expect(await frame.locator('body').evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)).toBe(0);
      expect(await p.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)).toBe(0);
      const gaps = await p.locator('.ag-email-notifications').evaluate(el => { const boxes = [...el.children].map(x => x.getBoundingClientRect()); return boxes.slice(1).map((b,i) => b.top - boxes[i].bottom); });
      for (const gap of gaps) { expect(gap).toBeGreaterThanOrEqual(15); expect(gap).toBeLessThanOrEqual(18); }
      if (scenario === 'warning' || width === 1440) {
        await p.locator('.ag-email-preview').scrollIntoViewIfNeeded(); await p.waitForTimeout(150);
        await p.screenshot({ path: path.join(output, `email-preview-${width}-${locale}-${theme}-${scenario}.png`), animations: 'disabled' });
        // Chromium can cull opaque iframe surfaces outside the viewport during a
        // full-page capture. Keep the same width and render the whole document.
        const height = await p.evaluate(() => document.documentElement.scrollHeight);
        await p.setViewportSize({ width, height }); await p.evaluate(() => scrollTo(0, 0)); await p.waitForTimeout(150);
        await p.screenshot({ path: path.join(output, `email-${width}-${locale}-${theme}-${scenario}.png`), fullPage: true, animations: 'disabled' });
        await p.setViewportSize({ width, height: 1000 });
      }
      combinations++;
    }
    await ctx.close();
  }
  expect(errors).toEqual([]); expect(external).toEqual([]);
  console.log(JSON.stringify({ status: 'passed', browser: browser.version(), realAPI: true, realPG: true, ownedTLS: true, explicitTests: sends, MIMEAccepted: 1, permanentFailure: 1, previewCombinations: combinations, widths: [320,390,1440], locales: ['zh-CN','en'], themes: ['dark','light'], CAS: '412 retained safe draft and cleared recipient', browserErrors: errors, expectedCASHTTPDiagnostics: expectedHTTP, externalRequests: external, limits: 'Chromium, not Gmail/Outlook/Apple Mail; synthetic SMTP sink only' }));
} finally { if (browser) await browser.close(); vite.kill('SIGTERM'); await new Promise(resolve => { if (vite.exitCode !== null) resolve(); else vite.once('exit', resolve); }); }
