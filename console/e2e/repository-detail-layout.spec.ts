import { expect, test, type Page } from "@playwright/test";
import { defaultSiteSettings } from "../src/lib/siteSettings";
import { authenticateAsAdmin } from "./support/auth";

const repositoryId = "repo-layout";

async function mockRepositoryDetail(
  page: Page,
  {
    scannerEnabled = false,
    distributionEnabled = false,
    format = "raw",
  }: {
    scannerEnabled?: boolean;
    distributionEnabled?: boolean;
    format?: "raw" | "npm" | "maven" | "cargo";
  } = {},
) {
  await authenticateAsAdmin(page);
  await page.route("**/api/v2/site-settings", (route) =>
    route.fulfill({ json: defaultSiteSettings }),
  );
  const repositoryName =
    format === "npm"
      ? "npm-hosted"
      : format === "maven"
        ? "maven-hosted"
        : format === "cargo"
          ? "cargo-hosted"
          : "release-files";
  const npmPackage = "pipeone-npm-frontend-validation-v2-beta";
  const npmDigest = `sha256:${"8".repeat(64)}`;

  if (format === "npm") {
    await page.route(`**/npm/${repositoryName}/${npmPackage}`, (route) =>
      route.fulfill({
        json: {
          name: npmPackage,
          "dist-tags": { latest: "0.1.3" },
          versions: {
            "0.1.3": {
              name: npmPackage,
              version: "0.1.3",
              description:
                "Minimal frontend service used to validate PipeOne's NPM delivery path.",
              license: "UNLICENSED",
              dist: { integrity: "sha512-example", shasum: "0ea64af013" },
              _artifactGateway: {
                digest: npmDigest,
                publisher: "resolver",
                size: 4024,
                source: "hosted",
                cacheStatus: "cached",
              },
            },
          },
          time: { "0.1.3": "2026-08-14T08:01:43Z" },
        },
      }),
    );
  }

  await page.route("**/api/v2/repositories?**", (route) =>
    route.fulfill({
      json: {
        items: distributionEnabled
          ? [
              {
                id: repositoryId,
                name: "release-files",
                format: "raw",
                type: "hosted",
                anonymousRead: true,
                mavenStrictPublication: false,
                state: "active",
                version: "1",
              },
              {
                id: "repo-production",
                name: "production-files",
                format: "raw",
                type: "hosted",
                anonymousRead: false,
                mavenStrictPublication: false,
                state: "active",
                version: "1",
              },
              {
                id: "repo-proxy",
                name: "upstream-proxy",
                format: "raw",
                type: "proxy",
                anonymousRead: false,
                mavenStrictPublication: false,
                state: "active",
                version: "1",
              },
            ]
          : [],
      },
    }),
  );

  await page.route(`**/api/v2/repositories/${repositoryId}**`, (route) => {
    const request = route.request();
    const path = new URL(request.url()).pathname;

    if (path.endsWith("/promotions") && request.method() === "POST") {
      const body = request.postDataJSON() as {
        coordinate: string;
        digest: string;
      };
      return route.fulfill({
        status: 202,
        json: {
          id: "promotion-job-layout",
          kind: "promotion",
          state: "pending",
          createdAt: "2026-08-12T08:00:00Z",
          attempts: 0,
          maxAttempts: 3,
          progressCurrent: 0,
          progressTotal: 0,
          details: {
            format: "raw",
            coordinate: body.coordinate,
            digest: body.digest,
          },
        },
      });
    }
    if (path.endsWith("/replications") && request.method() === "GET") {
      return route.fulfill({ json: [] });
    }
    if (
      format === "npm" &&
      path.endsWith("/artifact-scans") &&
      request.method() === "GET"
    ) {
      return route.fulfill({
        json: {
          coordinate: `${npmPackage}@0.1.3`,
          digest: npmDigest,
          state: "never",
        },
      });
    }
    if (path.endsWith("/artifact-scans") && request.method() === "POST") {
      const body = request.postDataJSON() as {
        coordinate: string;
        digest: string;
      };
      return route.fulfill({
        json: {
          id: "scan-job-layout",
          kind: "scan",
          state: "pending",
          createdAt: "2026-08-12T08:00:00Z",
          attempts: 0,
          maxAttempts: 3,
          progressCurrent: 0,
          progressTotal: 0,
          details: {
            format: "raw",
            coordinate: body.coordinate,
            digest: body.digest,
          },
        },
      });
    }
    if (request.method() === "PATCH") {
      return route.fulfill({
        json: {
          id: repositoryId,
          name: repositoryName,
          format,
          type: "hosted",
          anonymousRead: true,
          mavenStrictPublication: false,
          state: "active",
          version: "2",
        },
      });
    }
    if (path.endsWith("/artifact-identities")) {
      return route.fulfill({
        json: {
          items:
            format === "npm"
              ? [
                  {
                    coordinate: `${npmPackage}@0.1.3`,
                    digest: npmDigest,
                    size: 4024,
                    publishedAt: "2026-08-14T08:01:43Z",
                  },
                ]
              : Array.from({ length: 20 }, (_, index) => ({
                  coordinate: `releases/example-${index + 1}.zip`,
                  digest: `sha256:${String(index).padStart(64, "0")}`,
                  size: 1024 * (index + 1),
                  publishedAt: "2026-08-08T08:00:00Z",
                })),
        },
      });
    }
    if (path.endsWith("/artifact-search")) {
      return route.fulfill({
        json: {
          items:
            format === "npm"
              ? [
                  {
                    coordinate: npmPackage,
                    digest: npmDigest,
                    size: 4024,
                    createdAt: "2026-08-14T08:01:43Z",
                    publisher: "resolver",
                    version: "0.1.3",
                    versionCount: 4,
                  },
                ]
              : Array.from({ length: 20 }, (_, index) => ({
                  coordinate: `releases/example-${index + 1}.zip`,
                  digest: `sha256:${String(index).padStart(64, "0")}`,
                  size: 1024 * (index + 1),
                  createdAt: "2026-08-08T08:00:00Z",
                })),
        },
      });
    }
    if (format === "npm" && path.endsWith("/artifact-quarantine")) {
      return route.fulfill({
        status: 404,
        json: { code: "not_found", message: "not found", status: 404 },
      });
    }
    if (format === "npm" && path.endsWith("/artifact-intelligence")) {
      return route.fulfill({
        status: 404,
        json: { code: "not_found", message: "not found", status: 404 },
      });
    }
    if (path.endsWith("/capabilities")) {
      return route.fulfill({
        json: {
          format,
          type: "hosted",
          operations: ["read", "publish", "browse", "delete"],
          artifactScanning: scannerEnabled,
          publicationScanning: scannerEnabled,
        },
      });
    }
    if (path.endsWith("/lifecycle-jobs")) {
      return route.fulfill({ json: [] });
    }
    if (path.endsWith("/security-policy")) {
      return route.fulfill({
        json: {
          version: "1",
          enabled: false,
          autoScanOnPublish: false,
          requireSignature: false,
          requireVerifiedSignature: false,
          requireSbom: false,
          requireProvenance: false,
          requireVulnerabilityScan: false,
          maxAllowedSeverity: "critical",
          failOnScanError: true,
          allowedLicenses: [],
        },
      });
    }
    if (path.endsWith("/quarantine-read-policy")) {
      return route.fulfill({ json: { version: "1", enabled: false } });
    }
    if (path.endsWith("/effective-access")) {
      const allowed = {
        allowed: true,
        source: "administrator",
        reason: "administrator",
      };
      return route.fulfill({
        json: {
          actor: "admin",
          identity: {
            actor: "mock-admin",
            kind: "local_session",
            role: "admin",
            administrator: true,
          },
          repository: {
            id: repositoryId,
            name: repositoryName,
            format,
            type: "hosted",
            state: "active",
          },
          anonymousRead: {
            allowed: true,
            source: "anonymous_policy",
            reason: "repository_anonymous_read_enabled",
          },
          permissions: {
            read: allowed,
            write: allowed,
            admin: allowed,
            intelligence: allowed,
          },
        },
      });
    }
    if (path.endsWith("/capacity")) {
      return route.fulfill({
        json: {
          repositoryId,
          format,
          usedBytes: format === "npm" ? 4024 : 1024 * 1024,
          objectCount: format === "npm" ? 1 : 20,
          quotaBytes: 0,
        },
      });
    }
    if (path.endsWith("/grants") && request.method() === "GET") {
      return route.fulfill({ json: [] });
    }
    return route.fulfill({
      json: {
        id: repositoryId,
        name: repositoryName,
        format,
        type: "hosted",
        anonymousRead: true,
        mavenStrictPublication: false,
        state: "active",
        version: "1",
      },
    });
  });
}

test("Cargo publish guide stays readable at desktop and mobile widths", async ({
  page,
}, testInfo) => {
  const pageErrors: string[] = [];
  const consoleErrors: string[] = [];
  page.on("pageerror", (error) => pageErrors.push(error.message));
  page.on("console", (message) => {
    if (message.type() === "error") consoleErrors.push(message.text());
  });
  await mockRepositoryDetail(page, { format: "cargo" });

  for (const width of [1440, 390]) {
    await page.setViewportSize({ width, height: 900 });
    await page.goto(`/repositories/${repositoryId}?tab=publish`);
    const guide = page.getByRole("heading", { name: "配置 Cargo 仓库" });
    await expect(guide).toBeVisible();
    await expect(
      page.getByText("cargo publish --registry gateway", { exact: false }),
    ).toBeVisible();
    const explanationBox = await guide.locator("..").boundingBox();
    const snippetsBox = await page
      .getByText("credential-provider =", { exact: false })
      .locator("../..")
      .boundingBox();
    expect(explanationBox).not.toBeNull();
    expect(snippetsBox).not.toBeNull();
    if (width > 600) {
      const gap =
        (snippetsBox?.x ?? 0) -
        ((explanationBox?.x ?? 0) + (explanationBox?.width ?? 0));
      expect(gap).toBeGreaterThanOrEqual(14);
      expect(gap).toBeLessThanOrEqual(18);
    } else {
      const gap =
        (snippetsBox?.y ?? 0) -
        ((explanationBox?.y ?? 0) + (explanationBox?.height ?? 0));
      expect(gap).toBeGreaterThanOrEqual(14);
      expect(gap).toBeLessThanOrEqual(18);
    }
    expect(
      await page.evaluate(
        () => document.body.scrollWidth - document.body.clientWidth,
      ),
    ).toBe(0);
    await page.screenshot({
      path: testInfo.outputPath(`cargo-publish-${width}.png`),
      fullPage: true,
    });
  }
  expect(pageErrors).toEqual([]);
  expect(consoleErrors).toEqual([]);
});

for (const width of [1440, 390]) {
  test(`repository usage and capacity keep a compact layout at ${width}px`, async ({
    page,
  }, testInfo) => {
    const errors: string[] = [];
    page.on("pageerror", (error) => errors.push(error.message));
    page.on("console", (message) => {
      if (message.type() === "error") errors.push(message.text());
    });
    await page.setViewportSize({ width, height: 900 });
    await mockRepositoryDetail(page);
    await page.route(
      `**/api/v2/repositories/${repositoryId}/capacity`,
      (route) =>
        route.fulfill({
          json: {
            repositoryId,
            format: "raw",
            usedBytes: 1024 * 1024,
            objectCount: 20,
            quotaBytes: 10 * 1024 * 1024,
          },
        }),
    );
    await page.route(
      `**/api/v2/repositories/${repositoryId}/artifact-usage**`,
      (route) =>
        route.fulfill({
          json: {
            repositoryId,
            generatedAt: "2026-09-30T12:00:00Z",
            totals: { downloadCount: 1, totalBytes: 1024, resources: 1 },
            totalCount: 1,
            items: [
              {
                format: "raw",
                resource: "releases/widget.zip",
                downloadCount: 1,
                totalBytes: 1024,
                firstDownloadedAt: "2026-09-29T12:00:00Z",
                lastDownloadedAt: "2026-09-30T12:00:00Z",
              },
            ],
          },
        }),
    );

    await page.goto(`/repositories/${repositoryId}?tab=capacity`);
    const metrics = page.getByRole("group", { name: "页面摘要" });
    await expect(metrics).toBeVisible();
    await expect(metrics.locator(":scope > div")).toHaveCount(3);
    await expect(page.getByText("主资产缓存")).toHaveCount(0);
    await expect(page.getByText("Hosted 仓库的容量来自")).toHaveCount(0);
    await expect(page.getByText("使用率")).toBeVisible();
    const capacityStack = metrics.locator("..");
    await expect(capacityStack).toHaveClass(/ag-page-stack/);
    await page.evaluate(() => window.scrollTo(0, 0));
    if (process.env.CAPTURE_LAYOUT_EVIDENCE === "1") {
      await page.screenshot({
        path: testInfo.outputPath(`repository-capacity-${width}.png`),
        fullPage: true,
      });
    }
    await page.goto(`/repositories/${repositoryId}?tab=usage`);
    await expect(page.getByText("releases/widget.zip")).toBeVisible();
    await expect(page.getByText("累计下载")).toHaveCount(0);
    await expect(
      page.getByRole("table").getByRole("columnheader", { name: "累计流量" }),
    ).toHaveCount(1);
    await page.evaluate(() => window.scrollTo(0, 0));
    expect(
      await page
        .locator("html")
        .evaluate((element) => element.scrollWidth - element.clientWidth),
    ).toBeLessThanOrEqual(0);
    expect(errors).toEqual([]);
    if (process.env.CAPTURE_LAYOUT_EVIDENCE === "1") {
      await page.screenshot({
        path: testInfo.outputPath(`repository-usage-${width}.png`),
        fullPage: true,
      });
    }
  });
}

test("npm detail keeps version selection and package content aligned when intelligence is absent", async ({
  page,
}) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await mockRepositoryDetail(page, { format: "npm" });

  const packageName = "pipeone-npm-frontend-validation-v2-beta";
  await page.goto(
    `/repositories/${repositoryId}?artifact=${packageName}&version=0.1.3`,
  );

  await expect(page.getByText("扫描状态 · NPM", { exact: true })).toBeVisible();
  const versionPanel = page.getByText(/^选择版本/).locator("..");
  const detailPanel = page
    .getByText(`${packageName}@0.1.3`, { exact: true })
    .locator("../..");
  const [versionBox, detailBox] = await Promise.all([
    versionPanel.boundingBox(),
    detailPanel.boundingBox(),
  ]);

  expect(versionBox).not.toBeNull();
  expect(detailBox).not.toBeNull();
  expect(detailBox?.width ?? 0).toBeGreaterThan(700);
  expect(detailBox?.x ?? 0).toBeGreaterThan(
    (versionBox?.x ?? 0) + (versionBox?.width ?? 0),
  );
  expect(Math.abs((detailBox?.y ?? 0) - (versionBox?.y ?? 0))).toBeLessThan(2);
});

test("repository detail keeps the whole content region stable when security becomes scrollable", async ({
  page,
}, testInfo) => {
  const pageErrors: string[] = [];
  const consoleErrors: string[] = [];
  page.on("pageerror", (error) => pageErrors.push(error.message));
  page.on("console", (message) => {
    if (message.type() === "error") consoleErrors.push(message.text());
  });
  await page.setViewportSize({ width: 1920, height: 900 });
  await mockRepositoryDetail(page, { format: "npm" });

  await page.goto(`/repositories/${repositoryId}`);

  const navigation = page.getByRole("navigation", { name: "仓库任务" });
  const taskTabs = navigation.locator(".ag-repository-tabs").getByRole("tab");
  await expect(taskTabs).toHaveCount(12);
  for (const label of [
    "制品",
    "使用统计",
    "发布",
    "访问授权",
    "保留策略",
    "制品扫描",
    "安全准入",
    "容量",
    "晋升 / 复制",
    "生命周期任务",
    "墓碑",
    "设置",
  ]) {
    await expect(
      navigation.getByRole("tab", { name: label, exact: true }),
    ).toBeVisible();
  }
  await expect(navigation.locator(".ant-tabs-nav-more")).toBeHidden();

  const beforeSecurity = await page.evaluate(() => {
    const main = document.querySelector<HTMLElement>(".ag-main");
    const summary = document.querySelector<HTMLElement>(
      '[aria-label="仓库摘要"]',
    );
    const navigation = document.querySelector<HTMLElement>(
      ".ag-repository-navigation",
    );
    return {
      clientWidth: document.documentElement.clientWidth,
      scrollHeight: document.documentElement.scrollHeight,
      scrollbarGutter: getComputedStyle(document.documentElement)
        .scrollbarGutter,
      mainLeft: main?.getBoundingClientRect().left,
      summaryLeft: summary?.getBoundingClientRect().left,
      navigationLeft: navigation?.getBoundingClientRect().left,
    };
  });
  if (process.env.CAPTURE_REPOSITORY_DETAIL) {
    await page.screenshot({
      path: testInfo.outputPath("repository-content-artifacts.png"),
      fullPage: false,
    });
  }
  await navigation.getByRole("tab", { name: "安全准入", exact: true }).click();
  await expect(
    page.getByRole("heading", {
      name: "安全准入与隔离读取",
      exact: true,
    }),
  ).toBeVisible();
  const afterSecurity = await page.evaluate(() => {
    const main = document.querySelector<HTMLElement>(".ag-main");
    const summary = document.querySelector<HTMLElement>(
      '[aria-label="仓库摘要"]',
    );
    const navigation = document.querySelector<HTMLElement>(
      ".ag-repository-navigation",
    );
    return {
      clientWidth: document.documentElement.clientWidth,
      scrollHeight: document.documentElement.scrollHeight,
      scrollbarGutter: getComputedStyle(document.documentElement)
        .scrollbarGutter,
      mainLeft: main?.getBoundingClientRect().left,
      summaryLeft: summary?.getBoundingClientRect().left,
      navigationLeft: navigation?.getBoundingClientRect().left,
    };
  });
  expect(afterSecurity.scrollHeight).toBeGreaterThan(
    beforeSecurity.scrollHeight,
  );
  expect(beforeSecurity.scrollbarGutter).toBe("stable");
  expect(afterSecurity.scrollbarGutter).toBe("stable");
  expect(afterSecurity.clientWidth).toBe(beforeSecurity.clientWidth);
  expect(afterSecurity.mainLeft).toBe(beforeSecurity.mainLeft);
  expect(afterSecurity.summaryLeft).toBe(beforeSecurity.summaryLeft);
  expect(afterSecurity.navigationLeft).toBe(beforeSecurity.navigationLeft);

  const tabBoxes = await taskTabs.evaluateAll((tabs) =>
    tabs.map((tab) => {
      const box = tab.getBoundingClientRect();
      return { top: box.top, bottom: box.bottom };
    }),
  );
  expect(Math.max(...tabBoxes.map((box) => box.top))).toBeLessThan(
    Math.min(...tabBoxes.map((box) => box.bottom)),
  );
  expect(
    await page.evaluate(
      () => document.body.scrollWidth - document.body.clientWidth,
    ),
  ).toBe(0);
  expect(pageErrors).toEqual([]);
  expect(consoleErrors).toEqual([]);

  if (process.env.CAPTURE_REPOSITORY_DETAIL) {
    await page.screenshot({
      path: testInfo.outputPath("repository-content-security.png"),
      fullPage: false,
    });
  }
});

test("repository detail keeps operational content above the fold", async ({
  page,
}, testInfo) => {
  const pageErrors: string[] = [];
  const consoleErrors: string[] = [];
  page.on("pageerror", (error) => pageErrors.push(error.message));
  page.on("console", (message) => {
    if (message.type() === "error") consoleErrors.push(message.text());
  });
  await page.setViewportSize({ width: 1440, height: 900 });
  await mockRepositoryDetail(page);

  await page.goto(`/repositories/${repositoryId}`);

  const summary = page.getByRole("group", { name: "仓库摘要" });
  await expect(summary).toBeVisible();
  await expect(summary).toContainText("1.0 MiB · 20 个对象");
  const summaryBox = await summary.boundingBox();
  expect(summaryBox?.height ?? Number.POSITIVE_INFINITY).toBeLessThan(110);

  await page.getByRole("button", { name: "查看概念说明" }).click();
  await expect(page.getByText("概念说明", { exact: true })).toBeVisible();
  await expect(
    page.getByText("Hosted Repository", { exact: true }),
  ).toBeVisible();
  await page.keyboard.press("Escape");

  const navigation = page.getByRole("navigation", { name: "仓库任务" });
  const taskTabs = navigation.locator(".ag-repository-tabs").getByRole("tab");
  await expect(taskTabs).toHaveCount(11);
  for (const label of [
    "制品",
    "使用统计",
    "访问授权",
    "保留策略",
    "制品扫描",
    "安全准入",
    "容量",
    "晋升 / 复制",
    "生命周期任务",
    "墓碑",
    "设置",
  ]) {
    await expect(
      navigation.getByRole("tab", { name: label, exact: true }),
    ).toBeVisible();
  }
  await expect(
    navigation.getByRole("tab", { name: "发布", exact: true }),
  ).toHaveCount(0);
  await expect(page.getByRole("tab", { name: "设置" })).toBeVisible();
  await expect(page.getByRole("button", { name: "设置" })).toHaveCount(0);

  const table = page.locator(".ag-console-table");
  await expect(table).toBeVisible();
  const surface = page.locator(".ag-card").filter({ has: table });
  await expect(surface).toHaveCount(1);
  const navigationBox = await navigation.boundingBox();
  const surfaceBox = await surface.boundingBox();
  const summaryToNavigationGap =
    (navigationBox?.y ?? 0) -
    ((summaryBox?.y ?? 0) + (summaryBox?.height ?? 0));
  const navigationToSurfaceGap =
    (surfaceBox?.y ?? 0) -
    ((navigationBox?.y ?? 0) + (navigationBox?.height ?? 0));
  expect(summaryToNavigationGap).toBeGreaterThanOrEqual(15);
  expect(summaryToNavigationGap).toBeLessThanOrEqual(17);
  expect(navigationToSurfaceGap).toBeGreaterThanOrEqual(15);
  expect(navigationToSurfaceGap).toBeLessThanOrEqual(17);

  // Tab labels are separated by the 24px gutter alone — no tab padding doubles
  // the gap — and the first tab sits flush with the content edge.
  const firstTabBoxes = await taskTabs.evaluateAll((tabs) =>
    tabs.slice(0, 4).map((tab) => {
      const box = tab.getBoundingClientRect();
      return { x: box.x, right: box.x + box.width };
    }),
  );
  for (let index = 1; index < firstTabBoxes.length; index += 1) {
    const gap = firstTabBoxes[index].x - firstTabBoxes[index - 1].right;
    expect(gap).toBeGreaterThanOrEqual(23);
    expect(gap).toBeLessThanOrEqual(25);
  }
  expect(
    Math.abs((firstTabBoxes[0]?.x ?? 0) - (summaryBox?.x ?? 0)),
  ).toBeLessThanOrEqual(1);

  expect(
    (await table.boundingBox())?.y ?? Number.POSITIVE_INFINITY,
  ).toBeLessThan(400);

  if (process.env.CAPTURE_REPOSITORY_DETAIL) {
    await page.screenshot({
      path: testInfo.outputPath("repository-detail.png"),
      fullPage: true,
    });
  }

  await page.getByRole("tab", { name: "访问授权" }).click();
  await expect(page.getByRole("tab", { name: "访问授权" })).toHaveAttribute(
    "aria-selected",
    "true",
  );
  await expect(page).toHaveURL(/\?tab=grants$/);

  await page.goto(`/repositories/${repositoryId}`);

  await page.setViewportSize({ width: 390, height: 844 });
  await expect(taskTabs).toHaveCount(11);
  await expect(
    page.getByRole("tab", { name: "制品", exact: true }),
  ).toHaveAttribute("aria-selected", "true");
  expect(
    await page.evaluate(
      () => document.body.scrollWidth - document.body.clientWidth,
    ),
  ).toBe(0);
  expect(pageErrors).toEqual([]);
  expect(consoleErrors).toEqual([]);

  if (process.env.CAPTURE_REPOSITORY_DETAIL) {
    await page.screenshot({
      path: testInfo.outputPath("repository-detail-mobile.png"),
      fullPage: true,
    });
  }
});

test("repository grants tab manages rows in a table", async ({
  page,
}, testInfo) => {
  const pageErrors: string[] = [];
  const consoleErrors: string[] = [];
  page.on("pageerror", (error) => pageErrors.push(error.message));
  page.on("console", (message) => {
    if (message.type() === "error") consoleErrors.push(message.text());
  });
  await page.setViewportSize({ width: 1440, height: 900 });
  await mockRepositoryDetail(page);

  const grants = [
    {
      principal: "user:alice",
      scopes: ["repositories:write"],
      resourcePrefix: "releases/",
    },
    { principal: "user:bob", scopes: ["repositories:read"] },
  ];
  const upsertBodies: unknown[] = [];
  const upsertHeaders: Record<string, string>[] = [];
  const deleteQueries: string[] = [];
  await page.route(
    `**/api/v2/repositories/${repositoryId}/grants**`,
    (route) => {
      const request = route.request();
      if (request.method() === "POST") {
        upsertBodies.push(request.postDataJSON());
        upsertHeaders.push(request.headers());
        return route.fulfill({ json: grants });
      }
      if (request.method() === "DELETE") {
        deleteQueries.push(new URL(request.url()).search);
        return route.fulfill({ status: 204 });
      }
      return route.fulfill({ json: grants });
    },
  );
  await page.route("**/api/v2/users**", (route) =>
    route.fulfill({
      json: {
        items: [{ name: "alice", role: "member", state: "active" }],
      },
    }),
  );
  await page.route("**/api/v2/api-keys**", (route) =>
    route.fulfill({ json: { items: [] } }),
  );
  await page.route("**/api/v2/service-accounts**", (route) =>
    route.fulfill({ json: { items: [] } }),
  );
  await page.route("**/api/v2/authorization-roles**", (route) =>
    route.fulfill({ json: [] }),
  );

  await page.goto(`/repositories/${repositoryId}?tab=grants`);

  const access = page
    .locator(".ant-collapse-item")
    .filter({ hasText: "当前访问判定" });
  await access.getByRole("button").click();
  await expect(access.getByText("mock-admin", { exact: true })).toBeVisible();
  await expect(access.getByText("允许", { exact: true })).toHaveCount(5);
  await expect(page.getByText(/判定顺序：/)).toHaveCount(0);
  await access.getByRole("button").click();

  const addButton = page.getByRole("button", { name: /添加授权/ });
  await expect(addButton).toBeVisible();
  const table = page.locator(".ag-console-table");
  await expect(table).toBeVisible();
  await expect(table.getByRole("row")).toHaveCount(3);
  await expect(table).toContainText("user:alice");
  await expect(table).toContainText("releases/");

  // Editing one row writes exactly that row: a single-grant upsert with no
  // If-Match precondition, never the repository's whole grant set.
  await table
    .getByRole("button", { name: /编\s*辑/ })
    .first()
    .click();
  const dialog = page.getByRole("dialog");
  await expect(dialog).toBeVisible();
  await dialog.getByRole("button", { name: /^保\s*存$/ }).click();
  await expect(dialog).toBeHidden();
  expect(upsertBodies).toEqual([
    {
      principal: "user:alice",
      scopes: ["repositories:write"],
      resourcePrefix: "releases/",
    },
  ]);
  expect(upsertHeaders[0]["if-match"]).toBeUndefined();

  // Removing one row deletes exactly its (principal, prefix) key.
  await table.getByRole("button", { name: "移除该行授权" }).first().click();
  await page.getByRole("button", { name: /^移\s*除$/ }).click();
  await expect.poll(() => deleteQueries.length).toBe(1);
  const deleteParams = new URLSearchParams(deleteQueries[0]);
  expect(deleteParams.get("principal")).toBe("user:alice");
  expect(deleteParams.get("resourcePrefix")).toBe("releases/");

  expect(
    await page.evaluate(
      () => document.body.scrollWidth - document.body.clientWidth,
    ),
  ).toBe(0);
  expect(pageErrors).toEqual([]);
  expect(consoleErrors).toEqual([]);

  if (process.env.CAPTURE_REPOSITORY_DETAIL) {
    await page.screenshot({
      path: testInfo.outputPath("repository-grants-table.png"),
      fullPage: true,
    });
  }

  await page.setViewportSize({ width: 390, height: 844 });
  await expect(table).toBeVisible();
  expect(
    await page.evaluate(
      () => document.body.scrollWidth - document.body.clientWidth,
    ),
  ).toBe(0);
  expect(pageErrors).toEqual([]);
  expect(consoleErrors).toEqual([]);

  if (process.env.CAPTURE_REPOSITORY_DETAIL) {
    await page.screenshot({
      path: testInfo.outputPath("repository-grants-table-mobile.png"),
      fullPage: true,
    });
  }
});

test("repository settings live in a tab and keep the update workflow", async ({
  page,
}, testInfo) => {
  await mockRepositoryDetail(page, { format: "maven" });
  const updateRequest = page.waitForRequest(
    (request) =>
      request.method() === "PATCH" &&
      new URL(request.url()).pathname.endsWith(`/repositories/${repositoryId}`),
  );

  await page.goto(`/repositories/${repositoryId}`);
  const settingsTab = page.getByRole("tab", { name: "设置" });
  await settingsTab.click();

  await expect(page.getByRole("heading", { name: "仓库设置" })).toBeVisible();
  await expect
    .poll(async () => {
      const [tabBox, indicatorBox] = await Promise.all([
        settingsTab.boundingBox(),
        page.locator(".ant-tabs-ink-bar").boundingBox(),
      ]);
      if (!tabBox || !indicatorBox) return Number.POSITIVE_INFINITY;
      const tabCenter = tabBox.x + tabBox.width / 2;
      const indicatorCenter = indicatorBox.x + indicatorBox.width / 2;
      return Math.abs(tabCenter - indicatorCenter);
    })
    .toBeLessThan(2);
  const anonymousSwitch = page.getByRole("switch", { name: "允许匿名读取" });
  const strictSwitch = page.getByRole("switch", { name: "严格发布" });
  await expect(anonymousSwitch).toBeChecked();
  await expect(strictSwitch).not.toBeChecked();

  const [anonymousPolicy, strictPolicy] = await Promise.all([
    anonymousSwitch.locator("..").boundingBox(),
    strictSwitch.locator("..").boundingBox(),
  ]);
  expect(anonymousPolicy).not.toBeNull();
  expect(strictPolicy).not.toBeNull();
  const policyGap =
    (strictPolicy?.y ?? 0) -
    ((anonymousPolicy?.y ?? 0) + (anonymousPolicy?.height ?? 0));
  expect(policyGap).toBeGreaterThanOrEqual(23);
  expect(policyGap).toBeLessThanOrEqual(25);

  await strictSwitch.click();
  await page.getByRole("button", { name: "保存更改" }).click();

  const submitted = await updateRequest;
  expect(submitted.postDataJSON()).toMatchObject({
    anonymousRead: true,
    mavenStrictPublication: true,
  });
  await expect(page.getByText("仓库设置已保存")).toBeVisible();

  if (process.env.CAPTURE_REPOSITORY_DETAIL) {
    await page.screenshot({
      path: testInfo.outputPath("repository-settings-desktop.png"),
      fullPage: true,
    });
  }

  await page.setViewportSize({ width: 390, height: 844 });
  expect(
    await page.evaluate(
      () => document.body.scrollWidth - document.body.clientWidth,
    ),
  ).toBe(0);
  await expect(strictSwitch).toBeVisible();

  if (process.env.CAPTURE_REPOSITORY_DETAIL) {
    await page.screenshot({
      path: testInfo.outputPath("repository-settings-mobile.png"),
      fullPage: true,
    });
  }
});

test("scanning uses a frameless responsive workspace", async ({
  page,
}, testInfo) => {
  const pageErrors: string[] = [];
  page.on("pageerror", (error) => pageErrors.push(error.message));
  await page.setViewportSize({ width: 1440, height: 900 });
  await mockRepositoryDetail(page);

  await page.goto(`/repositories/${repositoryId}?tab=scanning`);
  await expect(
    page.getByRole("heading", { name: "制品扫描", exact: true }),
  ).toBeVisible();

  await expect(page.locator(".ag-card .ag-card")).toHaveCount(0);

  const scannerWarning = page
    .getByRole("alert")
    .filter({ hasText: "当前仓库未配置可用扫描器" });
  await expect(scannerWarning).toBeVisible();
  await expect(page.getByText("扫描与处置是两个步骤")).toHaveCount(0);

  const artifactScanHeading = page.getByRole("heading", {
    name: "选择并扫描不可变制品",
    exact: true,
  });
  const artifactScanCard = page
    .locator(".ag-card")
    .filter({ has: artifactScanHeading });
  const artifactPicker = artifactScanCard.getByRole("combobox", {
    name: "搜索并选择制品",
  });
  const scanHint = artifactScanCard.getByText(
    "选择后会自动锁定规范坐标与完整摘要；最多显示 50 条，可输入关键词检索历史版本和 Conan 修订。仅在无法检索时使用高级手动输入。",
    { exact: true },
  );
  const submitScan = artifactScanCard.getByRole("button", { name: "提交扫描" });
  const [artifactCardBox, pickerBox, hintBox, submitBox] = await Promise.all([
    artifactScanCard.boundingBox(),
    artifactPicker.boundingBox(),
    scanHint.boundingBox(),
    submitScan.boundingBox(),
  ]);
  expect(artifactCardBox).not.toBeNull();
  expect(pickerBox).not.toBeNull();
  expect(hintBox).not.toBeNull();
  expect(submitBox).not.toBeNull();

  const warningBox = await scannerWarning.boundingBox();
  const desktopGap =
    (artifactCardBox?.y ?? 0) -
    ((warningBox?.y ?? 0) + (warningBox?.height ?? 0));
  expect(desktopGap).toBeGreaterThanOrEqual(15);
  expect(desktopGap).toBeLessThanOrEqual(17);

  const cardLeft = artifactCardBox?.x ?? 0;
  const cardRight = cardLeft + (artifactCardBox?.width ?? 0);
  const cardBottom = (artifactCardBox?.y ?? 0) + (artifactCardBox?.height ?? 0);
  expect((pickerBox?.x ?? 0) - cardLeft).toBeGreaterThanOrEqual(20);
  expect(
    cardRight - ((pickerBox?.x ?? 0) + (pickerBox?.width ?? 0)),
  ).toBeGreaterThanOrEqual(20);
  expect((hintBox?.x ?? 0) - cardLeft).toBeGreaterThanOrEqual(20);
  expect(
    cardRight - ((submitBox?.x ?? 0) + (submitBox?.width ?? 0)),
  ).toBeGreaterThanOrEqual(20);
  expect(
    cardBottom - ((submitBox?.y ?? 0) + (submitBox?.height ?? 0)),
  ).toBeGreaterThanOrEqual(16);

  await artifactScanCard.getByRole("button", { name: "高级手动输入" }).click();
  await expect(
    artifactScanCard.getByRole("textbox", { name: "制品坐标" }),
  ).toBeVisible();
  await expect(
    artifactScanCard.getByRole("textbox", { name: "SHA-256 摘要" }),
  ).toBeVisible();

  const recentJobsHeading = page.getByRole("heading", {
    name: "最近扫描任务",
    exact: true,
  });
  const recentJobsCard = page
    .locator(".ag-card")
    .filter({ has: recentJobsHeading });
  const recentHeader = recentJobsHeading.locator("..");
  const recentEmpty = recentJobsCard.locator(".ant-empty");
  const [recentCardBox, recentHeaderBox, recentEmptyBox] = await Promise.all([
    recentJobsCard.boundingBox(),
    recentHeader.boundingBox(),
    recentEmpty.boundingBox(),
  ]);
  expect(recentCardBox).not.toBeNull();
  expect(recentHeaderBox).not.toBeNull();
  expect(recentEmptyBox).not.toBeNull();
  expect(
    (recentEmptyBox?.y ?? 0) -
      ((recentHeaderBox?.y ?? 0) + (recentHeaderBox?.height ?? 0)),
  ).toBeLessThanOrEqual(20);
  expect(
    (recentCardBox?.y ?? 0) +
      (recentCardBox?.height ?? 0) -
      ((recentEmptyBox?.y ?? 0) + (recentEmptyBox?.height ?? 0)),
  ).toBeLessThanOrEqual(20);

  if (process.env.CAPTURE_REPOSITORY_DETAIL) {
    await page.screenshot({
      path: testInfo.outputPath("repository-scanning.png"),
      fullPage: true,
    });
  }

  await page.setViewportSize({ width: 390, height: 844 });
  const narrowWarningBox = await scannerWarning.boundingBox();
  const narrowCardBox = await artifactScanCard.boundingBox();
  const mobileGap =
    (narrowCardBox?.y ?? 0) -
    ((narrowWarningBox?.y ?? 0) + (narrowWarningBox?.height ?? 0));
  expect(mobileGap).toBeGreaterThanOrEqual(15);
  expect(mobileGap).toBeLessThanOrEqual(17);
  expect(
    await page.evaluate(
      () => document.body.scrollWidth - document.body.clientWidth,
    ),
  ).toBe(0);
  expect(pageErrors).toEqual([]);
  if (process.env.CAPTURE_REPOSITORY_DETAIL) {
    await page.screenshot({
      path: testInfo.outputPath("repository-scanning-mobile.png"),
      fullPage: true,
    });
  }
});

test("scanning selects a searchable immutable artifact before queuing", async ({
  page,
}) => {
  const coordinate = "releases/example-1.zip";
  const digest = `sha256:${"0".repeat(64)}`;
  await mockRepositoryDetail(page, { scannerEnabled: true });
  const scanRequest = page.waitForRequest(
    (request) =>
      request.method() === "POST" &&
      new URL(request.url()).pathname.endsWith(
        `/repositories/${repositoryId}/artifact-scans`,
      ),
  );

  await page.goto(`/repositories/${repositoryId}?tab=scanning`);
  const artifactScanCard = page
    .locator(".ag-card")
    .filter({ hasText: "选择并扫描不可变制品" });
  const picker = artifactScanCard.getByRole("combobox", {
    name: "搜索并选择制品",
  });
  await picker.click();
  const option = page
    .locator(".ant-select-item-option")
    .filter({ hasText: coordinate })
    .first();
  await expect(option).toBeVisible();
  await option.click();

  await expect(
    artifactScanCard.getByText(digest, { exact: true }),
  ).toBeVisible();
  await artifactScanCard.getByRole("button", { name: "提交扫描" }).click();

  expect((await scanRequest).postDataJSON()).toEqual({ coordinate, digest });
  await expect(page.getByText("扫描任务已提交")).toBeVisible();
});

test("promotion selects a source artifact and a compatible Hosted target", async ({
  page,
}, testInfo) => {
  const pageErrors: string[] = [];
  page.on("pageerror", (error) => pageErrors.push(error.message));
  await page.setViewportSize({ width: 1440, height: 900 });
  const coordinate = "releases/example-1.zip";
  const digest = `sha256:${"0".repeat(64)}`;
  await mockRepositoryDetail(page, { distributionEnabled: true });
  const promotionRequest = page.waitForRequest(
    (request) =>
      request.method() === "POST" &&
      new URL(request.url()).pathname.endsWith(
        `/repositories/${repositoryId}/promotions`,
      ),
  );

  await page.goto(`/repositories/${repositoryId}?tab=distribute`);
  await expect(page.locator(".ag-distribution").getByRole("alert")).toHaveCount(
    0,
  );
  const sourcePicker = page.getByRole("combobox", {
    name: "搜索并选择源制品",
  });
  await sourcePicker.click();
  const artifactOption = page
    .locator(".ant-select-item-option")
    .filter({ hasText: coordinate })
    .first();
  await expect(artifactOption).toBeVisible();
  await artifactOption.click();

  const targetPicker = page.getByRole("combobox", { name: "选择目标仓库" });
  await targetPicker.click();
  await expect(
    page.locator(".ant-select-item-option").filter({
      hasText: "production-files",
    }),
  ).toBeVisible();
  await expect(
    page.locator(".ant-select-item-option").filter({
      hasText: "upstream-proxy",
    }),
  ).toHaveCount(0);
  await page
    .locator(".ant-select-item-option")
    .filter({ hasText: "production-files" })
    .click();
  await page.getByRole("button", { name: /晋\s*升/ }).click();

  expect((await promotionRequest).postDataJSON()).toEqual({
    targetRepositoryId: "repo-production",
    coordinate,
    digest,
  });
  await expect(
    page.getByText("晋升任务已提交，请在目标仓库的「生命周期任务」查看进度"),
  ).toBeVisible();
  if (process.env.CAPTURE_REPOSITORY_DETAIL) {
    await page.screenshot({
      path: testInfo.outputPath("repository-promotion.png"),
      fullPage: true,
    });
  }
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(sourcePicker).toBeVisible();
  // The select portal can briefly keep its desktop position while closing.
  await expect
    .poll(() =>
      page.evaluate(
        () =>
          document.documentElement.scrollWidth -
          document.documentElement.clientWidth,
      ),
    )
    .toBeLessThanOrEqual(0);
  expect(pageErrors).toEqual([]);
  if (process.env.CAPTURE_REPOSITORY_DETAIL) {
    await page.screenshot({
      path: testInfo.outputPath("repository-promotion-mobile.png"),
      fullPage: true,
    });
  }
});

test("security guardrails use independent desktop columns", async ({
  page,
}, testInfo) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await mockRepositoryDetail(page);

  await page.goto(`/repositories/${repositoryId}?tab=security`);
  const readHeading = page.getByRole("heading", {
    name: "隔离制品读取",
    exact: true,
  });
  const admissionHeading = page.getByRole("heading", {
    name: "晋升准入",
    exact: true,
  });
  await expect(readHeading).toBeVisible();
  await expect(admissionHeading).toBeVisible();
  await expect(page.locator(".ag-card .ag-card")).toHaveCount(0);

  const readCard = page.locator(".ag-card").filter({ has: readHeading });
  const admissionCard = page
    .locator(".ag-card")
    .filter({ has: admissionHeading });
  const [readBox, admissionBox] = await Promise.all([
    readCard.boundingBox(),
    admissionCard.boundingBox(),
  ]);
  expect(readBox).not.toBeNull();
  expect(admissionBox).not.toBeNull();
  expect(Math.abs((readBox?.y ?? 0) - (admissionBox?.y ?? 0))).toBeLessThan(2);
  expect(admissionBox?.x ?? 0).toBeGreaterThan(
    (readBox?.x ?? 0) + (readBox?.width ?? 0),
  );

  await page.setViewportSize({ width: 1024, height: 900 });
  const [narrowReadBox, narrowAdmissionBox] = await Promise.all([
    readCard.boundingBox(),
    admissionCard.boundingBox(),
  ]);
  expect(
    Math.abs((narrowReadBox?.x ?? 0) - (narrowAdmissionBox?.x ?? 0)),
  ).toBeLessThan(2);
  expect(narrowAdmissionBox?.y ?? 0).toBeGreaterThan(
    (narrowReadBox?.y ?? 0) + (narrowReadBox?.height ?? 0),
  );

  await page.setViewportSize({ width: 390, height: 844 });
  const [mobileReadBox, mobileAdmissionBox] = await Promise.all([
    readCard.boundingBox(),
    admissionCard.boundingBox(),
  ]);
  expect(
    Math.abs((mobileReadBox?.x ?? 0) - (mobileAdmissionBox?.x ?? 0)),
  ).toBeLessThan(2);
  expect(mobileAdmissionBox?.y ?? 0).toBeGreaterThan(
    (mobileReadBox?.y ?? 0) + (mobileReadBox?.height ?? 0),
  );
  expect(
    await page.evaluate(
      () => document.body.scrollWidth - document.body.clientWidth,
    ),
  ).toBe(0);

  if (process.env.CAPTURE_REPOSITORY_DETAIL) {
    await page.screenshot({
      path: testInfo.outputPath("repository-security-mobile.png"),
      fullPage: true,
    });
  }
});

test("a quarantined artifact reads as an ongoing state in both themes", async ({
  page,
}, testInfo) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await mockRepositoryDetail(page, { format: "npm" });
  const packageName = "pipeone-npm-frontend-validation-v2-beta";
  await page.route("**/artifact-quarantine**", (route) =>
    route.fulfill({
      json: {
        repositoryId,
        format: "npm",
        coordinate: `${packageName}@0.1.3`,
        digest: `sha256:${"8".repeat(64)}`,
        state: "quarantined",
        reason: "critical vulnerability under investigation",
        version: "2",
        updatedBy: "alice",
        updatedAt: "2026-08-11T08:00:00Z",
        quarantinedAt: "2026-08-11T08:00:00Z",
      },
    }),
  );

  const quarantineAlert = page
    .locator(".ant-alert")
    .filter({ hasText: "制品已隔离" })
    .first();
  for (const mode of ["dark", "light"] as const) {
    if (mode === "light") {
      await page.getByRole("button", { name: /选择主题/ }).click();
      await page.getByRole("menuitem", { name: /Gateway Light/ }).click();
      await expect(page.locator("html")).toHaveAttribute("data-theme", "light");
    }
    await page.goto(
      `/repositories/${repositoryId}?artifact=${packageName}&version=0.1.3`,
    );
    await expect(quarantineAlert).toBeVisible();
    await expect(quarantineAlert).toHaveClass(/ant-alert-warning/);
    await expect(quarantineAlert).not.toHaveClass(/ant-alert-error/);
    if (process.env.CAPTURE_LAYOUT_EVIDENCE === "1") {
      await page.screenshot({
        path: testInfo.outputPath(`tone-quarantine-${mode}.png`),
      });
    }
  }
});

test("a blocked admission reads as a policy judgment in both themes", async ({
  page,
}, testInfo) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await mockRepositoryDetail(page, { distributionEnabled: true });
  await page.route("**/security-policy:evaluate**", (route) =>
    route.fulfill({
      json: {
        allowed: false,
        enforced: true,
        policyVersion: "5",
        intelligencePresent: true,
        reasons: ["verified_signature_required"],
      },
    }),
  );

  const coordinate = "releases/example-1.zip";
  const openEvaluation = async () => {
    await page.getByRole("combobox", { name: "搜索并选择源制品" }).click();
    await page
      .locator(".ant-select-item-option")
      .filter({ hasText: coordinate })
      .first()
      .click();
    await page.getByRole("combobox", { name: "选择目标仓库" }).click();
    await page
      .locator(".ant-select-item-option")
      .filter({ hasText: "production-files" })
      .first()
      .click();
    await page.getByRole("button", { name: "评估准入" }).click();
  };

  const policyAlert = page
    .locator(".ant-alert")
    .filter({ hasText: "安全策略阻止晋升" })
    .first();
  for (const mode of ["dark", "light"] as const) {
    if (mode === "light") {
      await page.getByRole("button", { name: /选择主题/ }).click();
      await page.getByRole("menuitem", { name: /Gateway Light/ }).click();
      await expect(page.locator("html")).toHaveAttribute("data-theme", "light");
    }
    await page.goto(`/repositories/${repositoryId}?tab=distribute`);
    await openEvaluation();
    await expect(policyAlert).toBeVisible();
    await expect(policyAlert).toHaveClass(/ant-alert-warning/);
    await expect(policyAlert).not.toHaveClass(/ant-alert-error/);
    if (process.env.CAPTURE_LAYOUT_EVIDENCE === "1") {
      await page.screenshot({
        path: testInfo.outputPath(`tone-policy-${mode}.png`),
      });
    }
  }
});

test("the grant dialog keeps every field reachable at both widths", async ({
  page,
}, testInfo) => {
  await mockRepositoryDetail(page);
  await page.route(`**/api/v2/repositories/${repositoryId}/grants**`, (route) =>
    route.fulfill({ json: [] }),
  );
  await page.route("**/api/v2/users**", (route) =>
    route.fulfill({
      json: {
        items: [{ name: "alice", role: "member", state: "active" }],
      },
    }),
  );
  await page.route("**/api/v2/api-keys**", (route) =>
    route.fulfill({ json: { items: [] } }),
  );
  await page.route("**/api/v2/service-accounts**", (route) =>
    route.fulfill({ json: { items: [] } }),
  );
  await page.route("**/api/v2/authorization-roles**", (route) =>
    route.fulfill({ json: [] }),
  );

  for (const [width, height] of [
    [1440, 900],
    [390, 844],
  ] as const) {
    for (const mode of ["dark", "light"] as const) {
      if (mode === "light") {
        await page.getByRole("button", { name: /选择主题/ }).click();
        await page.getByRole("menuitem", { name: /Gateway Light/ }).click();
        await expect(page.locator("html")).toHaveAttribute(
          "data-theme",
          "light",
        );
      }
      await page.setViewportSize({ width, height });
      await page.goto(`/repositories/${repositoryId}?tab=grants`);
      await page
        .getByRole("button", { name: /添加授权/ })
        .last()
        .click();
      const dialog = page.locator(".ant-modal").first();
      await expect(dialog).toBeVisible();
      await page.waitForTimeout(400);

      const body = dialog.locator(".ant-modal-body");
      // Nothing may sit past the dialog's own edge: the body scrolls vertically
      // only, so horizontally clipped controls are unreachable.
      const overflow = await body.evaluate((element) => ({
        hidden: element.scrollWidth - element.clientWidth,
        width: Math.round(element.getBoundingClientRect().width),
      }));
      expect(overflow.hidden, `${width}px dialog body overflows`).toBe(0);

      for (const label of ["授权主体", "权限级别", "资源范围", "本规则授予"]) {
        await expect(body.getByText(label, { exact: true })).toBeVisible();
      }
      // The scope control and its prefix input are reachable, not cut off.
      const prefix = body.getByText("整个仓库", { exact: true });
      await expect(prefix).toBeVisible();
      const box = await prefix.boundingBox();
      const dialogBox = await dialog.boundingBox();
      expect(box).not.toBeNull();
      expect(dialogBox).not.toBeNull();
      expect(box!.x).toBeGreaterThanOrEqual(dialogBox!.x);
      expect(box!.x + box!.width).toBeLessThanOrEqual(
        dialogBox!.x + dialogBox!.width + 1,
      );

      if (width === 1440) {
        // Browser font metrics can round the same modal by one CSS pixel.
        expect(overflow.width).toBeGreaterThanOrEqual(630);
        expect(overflow.width).toBeLessThanOrEqual(634);
      }
      if (process.env.CAPTURE_LAYOUT_EVIDENCE === "1") {
        await page.screenshot({
          path: testInfo.outputPath(`grant-dialog-${width}-${mode}.png`),
        });
      }
      await page.keyboard.press("Escape");
      await expect(dialog).toBeHidden();
    }
  }
});
