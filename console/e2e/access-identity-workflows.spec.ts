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
let drawerUserId = "";

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
  if (drawerUserId) {
    await request.delete(`/api/v2/users/${drawerUserId}`, {
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

test("a custom role feeds a template that is applied to a repository", async ({
  page,
  request,
}) => {
  await authenticate(page);
  const roleName = `e2e-role-${suffix}`;
  const templateName = `e2e-template-${suffix}`;
  const principal = `service-account:${grantAccountId}`;
  const named = async (path: string, name: string) => {
    const response = await request.get(path, { headers: admin() });
    await expectOK(response, `list ${path}`);
    return ((await response.json()) as Array<{ name: string }>).filter(
      (item) => item.name === name,
    );
  };

  await page.goto("/access?tab=policies");
  await page.getByRole("button", { name: /新建角色/ }).click();
  const roleDialog = page.getByRole("dialog", { name: "新建授权角色" });
  await waitForModalOpen(roleDialog);
  await roleDialog.getByLabel("角色名称").fill(roleName);
  await roleDialog.getByLabel("仓库权限").click();
  await page
    .locator(".ant-select-item-option")
    .filter({ hasText: "写入" })
    .click();
  // A multiple select keeps its list open over the footer; close it first.
  await roleDialog.getByLabel("角色名称").click();
  await expect(page.locator(".ant-select-dropdown:visible")).toHaveCount(0);
  await roleDialog.getByRole("button", { name: /^保\s*存$/ }).click();
  await expect(roleDialog).toBeHidden();
  await expect
    .poll(
      async () => (await named("/api/v2/authorization-roles", roleName)).length,
    )
    .toBe(1);

  // The template editor reads the same roles list, so the new role is offered
  // without reloading the page.
  await page.getByRole("button", { name: /新建模板/ }).click();
  const editor = page.getByRole("dialog", { name: "新建授权模板" });
  await waitForModalOpen(editor);
  await editor.getByPlaceholder("模板名称").fill(templateName);
  await editor.getByRole("combobox").first().fill(principal);
  await editor.getByText("自定义权限").click();
  await page
    .locator(".ant-select-item-option")
    .filter({ hasText: roleName })
    .click();
  await editor.getByRole("button", { name: /^保\s*存$/ }).click();
  await expect(editor).toBeHidden();
  const templateRow = page.getByRole("row").filter({ hasText: templateName });
  await expect(templateRow).toBeVisible();

  await templateRow.getByRole("button", { name: /应\s*用/ }).click();
  const apply = page.getByRole("dialog", { name: "应用授权模板" });
  await waitForModalOpen(apply);
  await apply.getByRole("combobox").click();
  await page.keyboard.type(repositoryName);
  await page
    .locator(".ant-select-item-option")
    .filter({ hasText: repositoryName })
    .click();
  await apply.getByRole("button", { name: /^应\s*用$/ }).click();
  await expect(apply).toBeHidden();
  await expect
    .poll(async () => (await grantsFor(request, principal))[0]?.scopes)
    .toEqual(
      expect.arrayContaining(["repositories:read", "repositories:write"]),
    );

  // Applying refreshes the cross-repository list on the grants tab.
  await page.getByRole("tab", { name: /授权记录/ }).click();
  await expect(
    page.getByRole("row").filter({ hasText: repositoryName }),
  ).toBeVisible();

  await page.getByRole("tab", { name: /策略与模板/ }).click();
  await templateRow.getByRole("button", { name: "删除模板" }).click();
  await page
    .getByRole("button", { name: /^删\s*除$/ })
    .last()
    .click();
  await expect(templateRow).toHaveCount(0);
  expect(
    await named("/api/v2/authorization-templates", templateName),
  ).toHaveLength(0);

  await page
    .getByRole("row")
    .filter({ hasText: roleName })
    .getByRole("button", { name: "删除角色" })
    .click();
  await page
    .getByRole("button", { name: /^删\s*除$/ })
    .last()
    .click();
  await expect(page.getByRole("row").filter({ hasText: roleName })).toHaveCount(
    0,
  );
  expect(await named("/api/v2/authorization-roles", roleName)).toHaveLength(0);
});

test("the user drawer saves a profile and adds, narrows, and removes a repository grant", async ({
  page,
  request,
}) => {
  await authenticate(page);
  const username = `e2e-drawer-${suffix}`;
  const principal = `user:${username}`;
  const created = await request.post("/api/v2/users", {
    headers: admin(),
    data: {
      name: username,
      displayName: "Drawer grant E2E",
      email: `${username}@example.test`,
      password: `Drawer-${suffix}`,
      role: "member",
    },
  });
  await expectOK(created, "create user");
  drawerUserId = ((await created.json()) as { id: string }).id;

  await page.goto("/users");
  await page.getByText(username, { exact: true }).click();
  const drawer = page.getByRole("dialog").filter({ hasText: "仓库授权" });

  // A profile save shows in the list behind the drawer without a reload.
  const renamed = `Drawer renamed ${suffix}`;
  await drawer.getByLabel("显示名").fill(renamed);
  await drawer.getByRole("button", { name: /保存更改/ }).click();
  await expect(
    page.getByRole("row").filter({ hasText: username }),
  ).toContainText(renamed);
  const profile = await request.get(`/api/v2/users/${drawerUserId}`, {
    headers: admin(),
  });
  await expectOK(profile, "read user");
  expect(((await profile.json()) as { displayName: string }).displayName).toBe(
    renamed,
  );
  await drawer.getByRole("button", { name: /添加授权/ }).click();

  const editor = page.getByRole("dialog", { name: "添加仓库授权" });
  await waitForModalOpen(editor);
  await editor.getByRole("combobox").first().click();
  await page.keyboard.type(repositoryName);
  await page
    .locator(".ant-select-item-option")
    .filter({ hasText: repositoryName })
    .click();
  await expect(
    editor.getByRole("combobox", { name: "权限级别" }),
  ).toBeVisible();
  await editor.getByRole("button", { name: /^保\s*存$/ }).click();
  await expect(editor).toBeHidden();
  await expect
    .poll(async () => (await grantsFor(request, principal)).length)
    .toBe(1);
  const row = drawer.getByRole("listitem").filter({ hasText: repositoryName });
  await expect(row).toContainText("整个仓库");

  // Narrowing the scope moves the entry: the repository-wide row goes and
  // exactly one prefixed row remains.
  await row.getByRole("button", { name: "编辑该条授权" }).click();
  const edit = page.getByRole("dialog", { name: "编辑仓库授权" });
  await waitForModalOpen(edit);
  await edit.getByText("限定范围", { exact: true }).click();
  // The prefix field is the dialog's last combobox (an AutoComplete).
  await edit.getByRole("combobox").last().fill("releases/");
  await edit.getByRole("button", { name: /^保\s*存$/ }).click();
  await expect(edit).toBeHidden();
  await expect
    .poll(async () =>
      (await grantsFor(request, principal)).map(
        (grant) => (grant as { resourcePrefix?: string }).resourcePrefix,
      ),
    )
    .toEqual(["releases/"]);
  await expect(row).toContainText("releases/");

  await row.getByRole("button", { name: "移除仓库授权" }).click();
  // Move onto the open confirmation the way a pointer would, so the trigger's
  // tooltip closes instead of covering the confirm button.
  const confirmation = page.locator(".ant-popconfirm:visible");
  await confirmation.getByText("移除该用户的仓库授权？").hover();
  await expect(
    page.getByRole("tooltip", { name: "移除仓库授权" }),
  ).toBeHidden();
  await confirmation.getByRole("button", { name: /^移\s*除$/ }).click();
  await expect
    .poll(async () => (await grantsFor(request, principal)).length)
    .toBe(0);
  await expect(row).toHaveCount(0);
});
