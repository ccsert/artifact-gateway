import { expect, test } from "@playwright/test";
import type { MigrationSource } from "../src/client";
import { waitForModalOpen } from "./support/motion";

const token = process.env.GATEWAY_ADMIN_TOKEN;
const sourceUrl = process.env.MIGRATION_SOURCE_FIXTURE_URL;
test.skip(
  !token || !sourceUrl,
  "Requires an isolated live Gateway and approved TLS Nexus fixture",
);

test("source connection, metadata reads, explicit mapping and credential lifecycle use the real Gateway", async ({
  page,
  request,
}) => {
  await page.addInitScript((token) => {
    localStorage.setItem("ag.console.token", token);
    localStorage.setItem("ag.console.role", "admin");
    localStorage.setItem("ag.console.locale", "zh-CN");
  }, token!);
  const headers = { Authorization: `Bearer ${token!}` };
  const name = `browser-source-${Date.now()}`;
  let source: MigrationSource | undefined;
  const read = async () => {
    const response = await request.get(
      `/api/v2/migration-sources/${source!.id}`,
      { headers },
    );
    expect(response.ok()).toBeTruthy();
    const text = await response.text();
    expect(text).not.toContain("fixture-reader");
    expect(text).not.toContain("fixture-password");
    expect(text).not.toContain('credentials"');
    expect(text).not.toContain("credentialCiphertext");
    source = JSON.parse(text) as MigrationSource;
    return source;
  };
  try {
    await page.goto("/operations?tab=migrations");
    await page.getByRole("button", { name: "添加迁移源" }).click();
    const dialog = page.getByRole("dialog", { name: "添加迁移源" });
    await waitForModalOpen(dialog);
    await dialog.getByRole("textbox", { name: "源名称" }).fill(name);
    await dialog
      .getByRole("textbox", { name: "Nexus HTTPS 地址" })
      .fill(sourceUrl!);
    await dialog.getByRole("combobox", { name: "源凭据" }).click();
    await page.getByRole("option", { name: "设置新凭据" }).click();
    await dialog
      .getByRole("textbox", { name: "源用户名" })
      .fill("fixture-reader");
    await dialog.getByLabel("源密码", { exact: true }).fill("fixture-password");
    const created = page.waitForResponse(
      (response) =>
        response.request().method() === "POST" &&
        new URL(response.url()).pathname === "/api/v2/migration-sources",
    );
    await dialog.getByRole("button", { name: "创建迁移源" }).click();
    const createdResponse = await created;
    expect(createdResponse.status()).toBe(201);
    source = (await createdResponse.json()) as MigrationSource;
    await expect(dialog).toBeHidden();
    expect((await read()).credentialsConfigured).toBe(true);
    await expect(page).toHaveURL(new RegExp(`source=${source.id}`));
    await page.getByRole("button", { name: "运行预检" }).click();
    await expect(
      page.getByRole("checkbox", { name: "选择 snapshots", exact: true }),
    ).toBeDisabled();
    for (const repository of ["releases", "raw-source"]) {
      await page
        .getByRole("checkbox", { name: `选择 ${repository}`, exact: true })
        .check();
      await page
        .getByRole("textbox", { name: `${repository} 的目标名称` })
        .fill(`browser-${repository}`);
    }
    await page
      .getByRole("button", { name: "发现选中 Hosted 内容（2）" })
      .click();
    await expect(page.getByText(/2 个源资产/)).toBeVisible();
    await expect(
      page.getByText("exports/build+1.txt", { exact: true }),
    ).toBeVisible();
    await page.getByRole("button", { name: "预览显式映射" }).click();
    await expect(page.getByText(/所选映射通过当前校验/)).toBeVisible();
    await page.reload();
    await expect(
      page.getByRole("heading", { name, exact: true }),
    ).toBeVisible();
    await expect(page.getByText("尚未运行预检", { exact: true })).toBeVisible();

    await page.getByRole("button", { name: "编辑源" }).click();
    const edit = page.getByRole("dialog", { name: "编辑迁移源" });
    await waitForModalOpen(edit);
    await expect(edit.locator('input[type="password"]')).toHaveCount(0);
    await edit.getByRole("combobox", { name: "源凭据" }).click();
    await page.getByRole("option", { name: "设置新凭据" }).click();
    await edit
      .getByRole("textbox", { name: "源用户名" })
      .fill("fixture-reader");
    await edit
      .getByLabel("源密码", { exact: true })
      .fill("fixture-password-rotated");
    await edit.getByRole("button", { name: "保存迁移源" }).click();
    await expect(edit).toBeHidden();
    expect((await read()).version).toBe("2");
    await page.getByRole("button", { name: "运行预检" }).click();
    await expect(
      page.getByRole("checkbox", { name: "选择 releases", exact: true }),
    ).toBeVisible();

    await page.getByRole("button", { name: "编辑源" }).click();
    await waitForModalOpen(edit);
    await edit.getByRole("combobox", { name: "源凭据" }).click();
    await page.getByRole("option", { name: "清除当前凭据" }).click();
    await edit.getByRole("button", { name: "保存迁移源" }).click();
    await expect(edit).toBeHidden();
    expect((await read()).credentialsConfigured).toBe(false);
    await page.getByRole("button", { name: "运行预检" }).click();
    await expect(page.getByText(/源访问被拒绝/)).toBeVisible();
    await expect(page.locator("body")).not.toContainText("fixture-password");
    expect(
      await page.evaluate(() =>
        JSON.stringify({
          local: { ...localStorage },
          session: { ...sessionStorage },
        }),
      ),
    ).not.toMatch(/fixture-reader|fixture-password/);
    const resolver = process.env.GATEWAY_RESOLVER_TOKEN;
    if (resolver)
      expect(
        (
          await request.get("/api/v2/migration-sources", {
            headers: { Authorization: `Bearer ${resolver}` },
          })
        ).status(),
      ).toBe(403);
    await page.getByRole("button", { name: "删除源", exact: true }).click();
    await page
      .getByRole("dialog", { name: "删除迁移源？" })
      .getByRole("button", { name: "删除源", exact: true })
      .click();
    await expect(page.getByRole("heading", { name, exact: true })).toHaveCount(
      0,
    );
    expect(
      (
        await request.get(`/api/v2/migration-sources/${source.id}`, { headers })
      ).status(),
    ).toBe(404);
    source = undefined;
  } finally {
    if (source) {
      const current = await read();
      await request.delete(`/api/v2/migration-sources/${current.id}`, {
        headers: { ...headers, "If-Match": current.version },
      });
    }
  }
});
