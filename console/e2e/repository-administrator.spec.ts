import { expect, test, type Page } from "@playwright/test";
import { defaultSiteSettings } from "../src/lib/siteSettings";
import {
  authenticateAsAdmin,
  authenticateAsMember,
  authenticateAsRepositoryAdministrator,
} from "./support/auth";

const repositoryId = "11111111-1111-1111-1111-111111111111";

const repository = {
  id: repositoryId,
  name: "release-files",
  format: "raw",
  type: "hosted",
  anonymousRead: false,
  mavenStrictPublication: false,
  state: "active",
  version: "1",
};

type Granted = {
  read?: boolean;
  write?: boolean;
  admin?: boolean;
  intelligence?: boolean;
};

function decision(allowed: boolean) {
  return {
    allowed,
    source: allowed ? "grant" : "none",
    reason: allowed ? "grant_scope" : "no_matching_rule",
  };
}

/**
 * Answers the repository detail page the way the tiered server does. The
 * identity is a plain member in every case; only the effective-access answer
 * separates a repository administrator from a reader, which is exactly the
 * input the tab tiering reads.
 */
async function mockTieredRepository(
  page: Page,
  granted: Granted,
  catalog: unknown[] = [repository],
) {
  await page.route("**/api/v2/site-settings", (route) =>
    route.fulfill({ json: defaultSiteSettings }),
  );
  await page.route("**/api/v2/repositories?**", (route) =>
    route.fulfill({ json: { items: catalog } }),
  );
  await page.route("**/api/v2/repositories", (route) =>
    route.fulfill({ json: { items: catalog } }),
  );
  // The platform-tier lists stay administrator-only, so the grant editor's
  // principal picker cannot populate for a repository administrator; it falls
  // back to the custom-actor field.
  for (const platformOnly of [
    "**/api/v2/users?**",
    "**/api/v2/api-keys?**",
    "**/api/v2/service-accounts?**",
    "**/api/v2/authorization-roles",
  ]) {
    await page.route(platformOnly, (route) =>
      route.fulfill({
        status: 403,
        json: { status: 403, code: "access_denied", title: "forbidden" },
      }),
    );
  }
  await page.route("**/api/v2/repositories/**", (route) => {
    const request = route.request();
    const path = new URL(request.url()).pathname;

    if (path.endsWith("/effective-access")) {
      return route.fulfill({
        json: {
          actor: "user:member",
          identity: {
            actor: "user:member",
            kind: "local_session",
            role: "member",
            administrator: false,
          },
          repository: {
            id: repository.id,
            name: repository.name,
            format: repository.format,
            type: repository.type,
            state: repository.state,
          },
          resource: "",
          simulated: false,
          anonymousRead: decision(false),
          permissions: {
            read: decision(granted.read === true),
            write: decision(granted.write === true),
            admin: decision(granted.admin === true),
            intelligence: decision(granted.intelligence === true),
          },
        },
      });
    }
    if (path.endsWith("/capabilities")) {
      return route.fulfill({
        json: {
          format: "raw",
          type: "hosted",
          operations: ["read", "publish"],
          artifactScanning: false,
          publicationScanning: false,
        },
      });
    }
    if (path.endsWith("/capacity")) {
      return route.fulfill({
        json: {
          repositoryId,
          format: "raw",
          usedBytes: 1048576,
          objectCount: 20,
          quotaBytes: 0,
        },
      });
    }
    if (path.endsWith("/artifact-search")) {
      return route.fulfill({ json: { items: [], nextPageToken: null } });
    }
    if (path.endsWith("/grants") && request.method() === "GET") {
      return route.fulfill({ headers: { ETag: '"3"' }, json: [] });
    }
    return route.fulfill({ json: repository });
  });
}

const managementTabs = [
  "访问授权",
  "保留策略",
  "安全准入",
  "容量",
  "晋升 / 复制",
  "生命周期任务",
  "墓碑",
  "设置",
];

test("a repository administrator manages its repository without platform navigation", async ({
  page,
}) => {
  await mockTieredRepository(page, {
    read: true,
    write: true,
    admin: true,
    intelligence: true,
  });
  await authenticateAsRepositoryAdministrator(page, "user:repo-admin");

  await page.goto(`/repositories/${repositoryId}`);

  const navigation = page.getByRole("navigation", { name: "仓库任务" });
  await expect(
    navigation.getByRole("tab", { name: "制品", exact: true }),
  ).toBeVisible();
  for (const label of managementTabs) {
    await expect(
      navigation.getByRole("tab", { name: label, exact: true }),
    ).toBeVisible();
  }
  await expect(
    navigation.getByRole("tab", { name: "发布", exact: true }),
  ).toHaveCount(0);

  for (const platformLink of ["访问控制", "API 密钥", "用户", "审计日志"]) {
    await expect(
      page.getByRole("link", { name: platformLink, exact: true }),
    ).toHaveCount(0);
  }
  await expect(page.getByRole("button", { name: /新建仓库/ })).toHaveCount(0);

  await page.getByRole("tab", { name: "访问授权", exact: true }).click();
  await expect(page).toHaveURL(/\?tab=grants$/);
  await expect(page.getByRole("tab", { name: "访问授权" })).toHaveAttribute(
    "aria-selected",
    "true",
  );
  await expect(page.getByText("暂无授权规则", { exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: "编辑授权" })).toBeVisible();
});

test("a reader keeps only the read surfaces and cannot reach a management tab by URL", async ({
  page,
}) => {
  await mockTieredRepository(page, { read: true });
  await authenticateAsMember(page, "user:reader");

  await page.goto(`/repositories/${repositoryId}?tab=settings`);

  await expect(
    page.getByRole("tab", { name: "制品", exact: true }),
  ).toHaveAttribute("aria-selected", "true");
  await expect(page).not.toHaveURL(/tab=settings/);
  for (const label of managementTabs) {
    await expect(
      page.getByRole("tab", { name: label, exact: true }),
    ).toHaveCount(0);
  }
  await expect(page.getByRole("tab", { name: "使用统计" })).toBeVisible();
});

test("a repository administrator is redirected away from platform administration", async ({
  page,
}) => {
  await mockTieredRepository(page, { read: true, admin: true });
  await authenticateAsRepositoryAdministrator(page, "user:repo-admin");

  await page.goto("/users");

  await expect(page).toHaveURL(/\/repositories$/);
  await expect(page.getByRole("link", { name: "release-files" })).toBeVisible();
});

test("a member without grants is told where repository access comes from", async ({
  page,
}) => {
  await mockTieredRepository(page, { read: true }, []);
  await authenticateAsMember(page, "user:no-grants");

  await page.goto("/repositories");

  await expect(
    page.getByText(
      "你还没有任何仓库授权。仓库权限由平台管理员按仓库分配；请联系管理员为你的账号授权。",
      { exact: true },
    ),
  ).toBeVisible();
  await expect(page.getByRole("button", { name: /新建仓库/ })).toHaveCount(0);
});

test("a platform administrator keeps every tab without repository grants", async ({
  page,
}) => {
  await mockTieredRepository(page, {});
  await authenticateAsAdmin(page);

  await page.goto(`/repositories/${repositoryId}`);

  const navigation = page.getByRole("navigation", { name: "仓库任务" });
  for (const label of [...managementTabs, "制品", "使用统计", "制品扫描"]) {
    await expect(
      navigation.getByRole("tab", { name: label, exact: true }),
    ).toBeVisible();
  }
});
