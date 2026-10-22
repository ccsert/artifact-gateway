import { expect, test, type Page, type TestInfo } from "@playwright/test";
import { authenticateAsAdmin, authenticateAsMember } from "./support/auth";
import { defaultSiteSettings } from "../src/lib/siteSettings";
import { waitForModalOpen } from "./support/motion";
import { nexusCapabilities } from "../src/test/nexusBatchFixtures";
import {
  migrationDiscovery,
  migrationMapping,
  migrationPreflight,
  migrationSource,
} from "../src/test/migrationFixtures";

async function setup(page: Page, width: number) {
  await page.setViewportSize({ width, height: 1000 });
  await authenticateAsAdmin(page);
  await page.addInitScript((width) => {
    localStorage.setItem(
      "ag.console.locale",
      width === 390 ? "en-US" : "zh-CN",
    );
    localStorage.setItem(
      "ag.console.theme.id",
      width === 390 ? "gateway-light" : "gateway-dark",
    );
  }, width);
  await page.route("**/api/v2/site-settings", (route) =>
    route.fulfill({ json: defaultSiteSettings }),
  );
  await page.route("**/api/v2/migration-sources**", (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path.endsWith("/preflight"))
      return route.fulfill({ json: migrationPreflight });
    if (path.endsWith("/discovery"))
      return route.fulfill({ json: migrationDiscovery });
    if (path.endsWith("/mapping-preview"))
      return route.fulfill({ json: migrationMapping });
    return route.fulfill({
      json: path.endsWith(migrationSource.id)
        ? migrationSource
        : { items: [migrationSource], nextCursor: "" },
    });
  });
  const pageErrors: string[] = [];
  const consoleErrors: string[] = [];
  page.on("pageerror", (error) => pageErrors.push(error.message));
  page.on("console", (message) => {
    if (message.type() === "error") consoleErrors.push(message.text());
  });
  return { pageErrors, consoleErrors };
}
async function geometry(page: Page, width: number) {
  await page.evaluate(() => document.fonts.ready);
  expect(
    await page
      .locator("html")
      .evaluate((element) => element.scrollWidth - element.clientWidth),
  ).toBeLessThanOrEqual(0);
  const stacks = await page.locator(".ag-page-stack").evaluateAll((elements) =>
    elements.map((element) =>
      Array.from(element.children)
        .map((child) => {
          const rect = child.getBoundingClientRect();
          return {
            top: rect.top,
            bottom: rect.bottom,
            header: child.classList.contains("ag-page-header"),
            primary: child.classList.contains("ag-page-primary"),
          };
        })
        .filter((rect) => rect.bottom > rect.top),
    ),
  );
  for (const boxes of stacks)
    for (let index = 1; index < boxes.length; index++) {
      const gap = boxes[index].top - boxes[index - 1].bottom;
      const expected =
        boxes[index - 1].header || boxes[index].primary ? 24 : 16;
      expect(gap).toBeGreaterThanOrEqual(expected - 1);
      expect(gap).toBeLessThanOrEqual(expected + 2);
    }
  const rows = await page
    .locator(".ag-migration-repository")
    .evaluateAll((elements) =>
      elements.map((row) =>
        Array.from(row.children).map((child) => {
          const rect = child.getBoundingClientRect();
          return {
            top: rect.top,
            bottom: rect.bottom,
            left: rect.left,
            right: rect.right,
          };
        }),
      ),
    );
  for (const boxes of rows) {
    if (boxes.length !== 2) continue;
    if (width > 640) {
      expect(boxes[1].left - boxes[0].right).toBeGreaterThanOrEqual(15);
      expect(boxes[1].left - boxes[0].right).toBeLessThanOrEqual(18);
      expect(Math.abs(boxes[1].top - boxes[0].top)).toBeLessThanOrEqual(1);
    } else {
      expect(boxes[1].top - boxes[0].bottom).toBeGreaterThanOrEqual(15);
      expect(boxes[1].top - boxes[0].bottom).toBeLessThanOrEqual(18);
    }
  }
}
async function capture(page: Page, info: TestInfo, name: string) {
  if (process.env.CAPTURE_LAYOUT_EVIDENCE === "1")
    await page.screenshot({
      path: info.outputPath(`${name}.png`),
      fullPage: true,
      animations: "disabled",
    });
}
for (const width of [1440, 390]) {
  test(`source refusal clears cached metadata and open controls across workspace views at ${width}px`, async ({
    page,
  }, info) => {
    const errors = await setup(page, width);
    const chinese = width === 1440;
    await page.route("**/api/v2/migrations/nexus/capabilities", (route) =>
      route.fulfill({ json: nexusCapabilities }),
    );
    await page.route("**/api/v2/migrations/nexus/batches?**", (route) =>
      route.fulfill({ json: { items: [], nextCursor: "" } }),
    );
    await page.goto(`/operations?tab=migrations&source=${migrationSource.id}`);
    await expect(
      page.getByRole("heading", { name: migrationSource.name, exact: true }),
    ).toBeVisible();
    let refuse!: () => Promise<void>;
    await page.route(
      chinese
        ? "**/api/v2/migration-sources?**"
        : `**/api/v2/migration-sources/${migrationSource.id}`,
      (route) =>
        new Promise<void>((resolve) => {
          refuse = async () => {
            await route.fulfill({
              status: 403,
              json: { status: 403, message: "never-render-private-message" },
            });
            resolve();
          };
        }),
    );
    const request = page.waitForRequest((request) =>
      request
        .url()
        .includes(
          chinese
            ? "/api/v2/migration-sources?"
            : `/api/v2/migration-sources/${migrationSource.id}`,
        ),
    );
    await page
      .getByRole("button", {
        name: chinese ? "刷新源" : "Refresh source",
        exact: true,
      })
      .nth(chinese ? 0 : 1)
      .click();
    await request;
    await page
      .getByRole("button", {
        name: chinese ? "编辑源" : "Edit source",
        exact: true,
      })
      .click();
    const dialog = page.getByRole("dialog", {
      name: chinese ? "编辑迁移源" : "Edit migration source",
      exact: true,
    });
    await waitForModalOpen(dialog);
    await refuse();
    await expect(dialog).toBeHidden();
    await expect(page.locator("body")).not.toContainText(migrationSource.name);
    await expect(page.locator("body")).not.toContainText(
      migrationSource.baseUrl,
    );
    await expect(page.locator("body")).not.toContainText(
      "never-render-private-message",
    );
    await expect(
      page.getByRole("button", {
        name: chinese ? "添加迁移源" : "Add migration source",
        exact: true,
      }),
    ).toHaveCount(0);
    await page
      .getByRole("button", {
        name: chinese ? "批次与验收" : "Batches and acceptance",
        exact: true,
      })
      .click();
    await page
      .getByRole("button", {
        name: chinese ? "创建迁移批次" : "Create migration batch",
        exact: true,
      })
      .click();
    await expect(
      page.getByRole("combobox", {
        name: chinese ? "已保存的迁移源" : "Saved migration source",
        exact: true,
      }),
    ).toHaveCount(0);
    await page
      .getByRole("button", {
        name: chinese ? "连接与映射" : "Connections and mappings",
        exact: true,
      })
      .click();
    await expect(page.locator("body")).not.toContainText(
      migrationSource.baseUrl,
    );
    await geometry(page, width);
    await capture(page, info, "source-access-refused");
    expect(errors.pageErrors).toEqual([]);
    expect(
      errors.consoleErrors.filter(
        (message) =>
          !/^Failed to load resource: the server responded with a status of 403/.test(
            message,
          ),
      ),
    ).toEqual([]);
  });
  test(`migration source real route preserves geometry and previous results at ${width}px`, async ({
    page,
  }, info) => {
    const errors = await setup(page, width);
    const chinese = width === 1440;
    await page.goto(`/operations?tab=migrations&source=${migrationSource.id}`);
    await expect(
      page.getByRole("tab", { name: chinese ? "迁移源" : "Migration sources" }),
    ).toHaveAttribute("aria-selected", "true");
    await page
      .getByRole("button", { name: chinese ? "运行预检" : "Run preflight" })
      .click();
    await page
      .getByRole("checkbox", {
        name: chinese ? "选择 releases" : "Select releases",
        exact: true,
      })
      .check();
    await page
      .getByRole("checkbox", {
        name: chinese ? "选择 raw-source" : "Select raw-source",
        exact: true,
      })
      .check();
    await page
      .getByRole("textbox", {
        name: chinese ? "releases 的目标名称" : "Target name for releases",
      })
      .fill("native-releases");
    await page
      .getByRole("textbox", {
        name: chinese ? "raw-source 的目标名称" : "Target name for raw-source",
      })
      .fill("native-raw");
    await page
      .getByRole("button", {
        name: chinese
          ? "发现选中 Hosted 内容（2）"
          : "Discover selected Hosted content (2)",
      })
      .click();
    await expect(
      page.getByText(chinese ? /2 个源资产/ : /2 source assets/),
    ).toBeVisible();
    await page
      .getByRole("button", {
        name: chinese ? "预览显式映射" : "Preview explicit mappings",
      })
      .click();
    await expect(
      page.getByText(
        chinese
          ? /所选映射通过当前校验/
          : /Selected mappings pass current validation/,
      ),
    ).toBeVisible();
    await geometry(page, width);
    await capture(page, info, "source-success");
    expect(errors.pageErrors).toEqual([]);
    expect(errors.consoleErrors).toEqual([]);

    await page.route("**/api/v2/migration-sources/*/discovery", (route) =>
      route.fulfill({
        status: 502,
        json: {
          status: 502,
          code: "source_limit_exceeded",
          message: "must-not-render-secret",
        },
      }),
    );
    await page
      .getByRole("button", {
        name: chinese
          ? "发现选中 Hosted 内容（2）"
          : "Discover selected Hosted content (2)",
      })
      .click();
    await expect(
      page.getByText(
        chinese ? /保留上次成功结果/ : /Keeping the last successful result/,
      ),
    ).toBeVisible();
    await expect(
      page.getByText(chinese ? /2 个源资产/ : /2 source assets/),
    ).toBeVisible();
    await expect(page.locator("body")).not.toContainText(
      "must-not-render-secret",
    );
    await geometry(page, width);
    await capture(page, info, "source-refresh-error");
    expect(errors.pageErrors).toEqual([]);
    expect(
      errors.consoleErrors.filter(
        (value) =>
          !/^Failed to load resource: the server responded with a status of 502/.test(
            value,
          ),
      ),
    ).toEqual([]);
  });
  test(`migration source initial error and empty state have bounded rhythm at ${width}px`, async ({
    page,
  }, info) => {
    const errors = await setup(page, width);
    await page.route("**/api/v2/migration-sources?**", (route) =>
      route.fulfill({
        status: 503,
        json: { status: 503, message: "hidden-password" },
      }),
    );
    await page.goto("/operations?tab=migrations");
    await expect(
      page.getByText(
        width === 1440 ? /请求未完成/ : /The request did not complete/,
      ),
    ).toBeVisible();
    await expect(
      page.getByRole("status", { name: /Loading|加载/ }),
    ).toHaveCount(0);
    await expect(page.locator("body")).not.toContainText("hidden-password");
    await geometry(page, width);
    await capture(page, info, "source-initial-error");
    await page.route("**/api/v2/migration-sources?**", (route) =>
      route.fulfill({ json: { items: [], nextCursor: "" } }),
    );
    await page.getByRole("button", { name: /重试|Retry/ }).click();
    await expect(
      page.getByText(
        width === 1440 ? "尚未配置迁移源" : "No migration sources configured",
        { exact: true },
      ),
    ).toBeVisible();
    await geometry(page, width);
    expect(errors.pageErrors).toEqual([]);
    expect(
      errors.consoleErrors.filter(
        (value) =>
          !/^Failed to load resource: the server responded with a status of 503/.test(
            value,
          ),
      ),
    ).toEqual([]);
  });
}

test("a member cannot open the migration source deep link or issue source API requests", async ({
  page,
}) => {
  await authenticateAsMember(page);
  await page.route("**/api/v2/site-settings", (route) =>
    route.fulfill({ json: defaultSiteSettings }),
  );
  await page.route("**/api/v2/artifact-search**", (route) =>
    route.fulfill({ json: { items: [] } }),
  );
  await page.route("**/api/v2/repositories?**", (route) =>
    route.fulfill({ json: { items: [], nextCursor: "" } }),
  );
  let sourceRequests = 0;
  await page.route("**/api/v2/migration-sources**", (route) => {
    sourceRequests++;
    return route.fulfill({ status: 403, json: { status: 403 } });
  });
  await page.goto(`/operations?tab=migrations&source=${migrationSource.id}`);
  // The empty state confirms the redirected catalog consumed its response.
  await expect(page.getByText("暂无仓库", { exact: true })).toBeVisible();
  await expect(page).toHaveURL(/\/repositories$/);
  await expect(page.getByRole("tab", { name: "迁移源" })).toHaveCount(0);
  expect(sourceRequests).toBe(0);
});
