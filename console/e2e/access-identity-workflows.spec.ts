import {
  expect,
  test,
  type APIRequestContext,
  type Page,
} from "@playwright/test";
import { waitForModalOpen } from "./support/motion";

/**
 * Write flows of the access and identity pages against a live Gateway. Every
 * step is confirmed through the management API, not only on screen, so a
 * refactor that renders the same text but writes the wrong thing fails here.
 */

const token = process.env.GATEWAY_ADMIN_TOKEN;

test.skip(!token, "GATEWAY_ADMIN_TOKEN is required for the access gate");
test.describe.configure({ mode: "serial" });

const suffix = `${Date.now()}-${Math.random().toString(16).slice(2, 8)}`;
const repositoryName = `e2e-access-${suffix}`;
const grantAccountName = `e2e-grant-${suffix}`;
let repositoryId = "";
let grantAccountId = "";

const admin = () => ({ Authorization: `Bearer ${token!}` });

async function expectOK(
  response: Awaited<ReturnType<APIRequestContext["get"]>>,
  action: string,
) {
  expect(
    response.ok(),
    `${action}: ${response.status()} ${await response.text()}`,
  ).toBeTruthy();
}

async function authenticate(page: Page) {
  await page.addInitScript((accessToken) => {
    localStorage.setItem("ag.console.token", accessToken);
    localStorage.setItem("ag.console.role", "admin");
  }, token!);
}

interface GrantRecord {
  repositoryId: string;
  principal: string;
  scopes: string[];
}

async function grantsFor(request: APIRequestContext, principal: string) {
  const response = await request.get("/api/v2/repository-grants", {
    headers: admin(),
  });
  await expectOK(response, "list repository grants");
  return ((await response.json()) as GrantRecord[]).filter(
    (grant) =>
      grant.repositoryId === repositoryId && grant.principal === principal,
  );
}

async function identityStatus(request: APIRequestContext, bearer: string) {
  const response = await request.get("/api/v2/identity", {
    headers: { Authorization: `Bearer ${bearer}` },
  });
  return response.status();
}

test.beforeAll(async ({ request }) => {
  const repository = await request.post("/api/v2/repositories", {
    headers: { ...admin(), "Idempotency-Key": `e2e-access-repo-${suffix}` },
    data: { name: repositoryName, format: "raw", type: "hosted" },
  });
  await expectOK(repository, "create repository");
  repositoryId = (await repository.json()).id as string;

  const account = await request.post("/api/v2/service-accounts", {
    headers: { ...admin(), "Idempotency-Key": `e2e-access-sa-${suffix}` },
    data: { name: grantAccountName, description: "access E2E principal" },
  });
  await expectOK(account, "create service account");
  grantAccountId = (await account.json()).id as string;
});

test.afterAll(async ({ request }) => {
  if (repositoryId) {
    await request.delete(`/api/v2/repositories/${repositoryId}`, {
      headers: admin(),
    });
  }
});

test("a repository grant is created, edited from Access Control, and removed", async ({
  page,
  request,
}) => {
  await authenticate(page);
  const principal = `service-account:${grantAccountId}`;

  await page.goto(`/repositories/${repositoryId}?tab=grants`);
  await page
    .getByRole("button", { name: /添加授权/ })
    .first()
    .click();
  const dialog = page.getByRole("dialog", { name: "添加授权" });
  await waitForModalOpen(dialog);
  await dialog.getByRole("combobox", { name: "授权主体" }).click();
  await page.keyboard.type(grantAccountName);
  // Rich option labels: Ant Design names the accessible option by its value,
  // so pick the visible row that shows the account name.
  await page
    .locator(".ant-select-item-option")
    .filter({ hasText: grantAccountName })
    .click();
  await dialog.getByRole("combobox", { name: "权限级别" }).click();
  await page
    .locator(".ant-select-item-option")
    .filter({ hasText: "读取 · 浏览 / 拉取" })
    .click();
  await dialog.getByRole("button", { name: /^保\s*存$/ }).click();
  await expect(dialog).toBeHidden();
  await expect
    .poll(async () => (await grantsFor(request, principal))[0]?.scopes)
    .toEqual(["repositories:read"]);

  // The cross-repository list shows the grant and leads back to edit it.
  await page.goto("/access");
  const row = page.getByRole("row").filter({ hasText: repositoryName });
  await expect(row).toBeVisible();
  await row.getByRole("link", { name: /编辑/ }).click();
  await expect(page).toHaveURL(new RegExp(`/repositories/${repositoryId}`));

  const grantRow = page.getByRole("row").filter({ hasText: grantAccountName });
  await grantRow.getByRole("button", { name: /^编\s*辑$/ }).click();
  const editor = page.getByRole("dialog", { name: "编辑授权" });
  await waitForModalOpen(editor);
  await editor.getByRole("combobox", { name: "权限级别" }).click();
  await page
    .locator(".ant-select-item-option")
    .filter({ hasText: "写入 · 发布 / 编辑" })
    .click();
  await editor.getByRole("button", { name: /^保\s*存$/ }).click();
  await expect(editor).toBeHidden();
  await expect
    .poll(async () => (await grantsFor(request, principal))[0]?.scopes)
    .toContain("repositories:write");

  await grantRow.getByRole("button", { name: "移除该行授权" }).click();
  await page
    .getByRole("button", { name: /^移\s*除$/ })
    .last()
    .click();
  await expect
    .poll(async () => (await grantsFor(request, principal)).length)
    .toBe(0);

  await page.goto("/access");
  await expect(
    page.getByRole("row").filter({ hasText: repositoryName }),
  ).toHaveCount(0);
});

test("a service account credential is issued, rotated, and revoked", async ({
  page,
  request,
}) => {
  await authenticate(page);
  const accountName = `e2e-rotate-${suffix}`;

  await page.goto("/service-accounts");
  await page
    .getByRole("button", { name: /新建服务账号/ })
    .first()
    .click();
  const create = page.getByRole("dialog", { name: "新建服务账号" });
  await waitForModalOpen(create);
  await create.getByLabel("账号名称").fill(accountName);
  await create.getByRole("button", { name: /^创\s*建$/ }).click();
  await expect(create).toBeHidden();
  await expect(page.getByText(accountName).first()).toBeVisible();

  const issue = async (credentialName: string) => {
    await page.getByRole("button", { name: /签发新凭据/ }).click();
    const dialog = page.getByRole("dialog", { name: "签发新凭据" });
    await waitForModalOpen(dialog);
    await dialog.getByLabel("凭据名称").fill(credentialName);
    await dialog.getByRole("button", { name: /^签\s*发$/ }).click();
    const reveal = page.getByRole("dialog", {
      name: "凭据已签发：立即保存 Token",
    });
    const plaintext = (
      await reveal.locator(".break-all").first().textContent()
    )?.trim();
    expect(plaintext).toBeTruthy();
    await reveal.getByRole("button", { name: "我已安全保存" }).click();
    await expect(reveal).toBeHidden();
    return plaintext!;
  };

  const first = await issue(`first-${suffix}`);
  expect(await identityStatus(request, first)).toBe(200);

  // Rotation: a second credential works alongside the first, then the first
  // is revoked without interrupting the second.
  const second = await issue(`second-${suffix}`);
  expect(await identityStatus(request, second)).toBe(200);
  expect(await identityStatus(request, first)).toBe(200);

  await page
    .getByRole("row")
    .filter({ hasText: `first-${suffix}` })
    .getByRole("button", { name: /吊销/ })
    .click();
  await page
    .getByRole("dialog", { name: "吊销凭据" })
    .getByRole("button", { name: /确认吊销/ })
    .click();
  await expect.poll(() => identityStatus(request, first)).toBe(401);
  expect(await identityStatus(request, second)).toBe(200);
});

test("an API key is created, authenticates, and is revoked", async ({
  page,
  request,
}) => {
  await authenticate(page);
  const keyName = `e2e-key-${suffix}`;

  await page.goto("/keys");
  await page
    .getByRole("button", { name: /新建密钥/ })
    .first()
    .click();
  const create = page.getByRole("dialog", { name: "新建 API 密钥" });
  await waitForModalOpen(create);
  await create.getByLabel("密钥名称").fill(keyName);
  await create.getByRole("button", { name: /^创\s*建$/ }).click();
  const reveal = page.getByRole("dialog", {
    name: "密钥已创建：请立即保存 Token",
  });
  const plaintext = (
    await reveal.locator(".break-all").first().textContent()
  )?.trim();
  expect(plaintext).toBeTruthy();
  await reveal.getByRole("button", { name: "我已保存" }).click();
  expect(await identityStatus(request, plaintext!)).toBe(200);

  await page.getByRole("button", { name: `吊销密钥 ${keyName}` }).click();
  await page
    .getByRole("dialog", { name: "吊销 API 密钥" })
    .getByRole("button", { name: /^吊\s*销$/ })
    .click();
  await expect.poll(() => identityStatus(request, plaintext!)).toBe(401);
});
