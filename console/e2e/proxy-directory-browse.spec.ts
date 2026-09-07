import { expect, test, type Page } from "@playwright/test";
import { defaultConsoleThemes } from "../src/lib/consoleTheme";
import { authenticateAsAdmin } from "./support/auth";

const repositoryId = "55555555-5555-4555-8555-555555555555";
const repositoryName =
  "proxy-engineering-release-artifacts-long-repository-name";
const snapshotPath =
  "com/example/engineering/widget/1.0-SNAPSHOT/widget-1.0-20260907.010203-2-sources.jar";
const rawPath =
  "release%20notes/engineering-release-notes-for-the-current-stable-build.txt";

async function mockProxyDirectory(page: Page, format: "maven" | "raw") {
  const path = format === "maven" ? snapshotPath : rawPath;
  const coordinate =
    format === "maven" ? "com.example.engineering:widget:1.0-SNAPSHOT" : path;
  const asset = {
    id: "asset",
    kind: "asset",
    hasChildren: false,
    name: path.split("/").at(-1),
    path,
    coordinate,
    digest: `sha256:${"a".repeat(64)}`,
    size: 42,
    contentType: "application/octet-stream",
    buildNumber: format === "maven" ? 2 : undefined,
    cacheState: "cached",
    sourceRepositoryId: repositoryId,
    sourceRepositoryName: repositoryName,
    cachedAt: "2026-09-07T01:02:03Z",
  };
  const repo = {
    id: repositoryId,
    name: repositoryName,
    format,
    type: "proxy",
    endpoint: "https://upstream.example.test/repository",
    allowedHosts: ["upstream.example.test"],
    anonymousRead: false,
    state: "active",
    version: "1",
  };
  const allowed = {
    allowed: true,
    source: "administrator",
    reason: "administrator",
  };
  await page.route("**/api/v2/**", (route) => {
    const url = new URL(route.request().url());
    const endpoint = url.pathname;
    if (endpoint.endsWith("/site-settings"))
      return route.fulfill({
        json: {
          version: "1",
          siteName: "Artifact Gateway",
          logoUrl: "",
          brandMark: "AG",
          enabledThemeIds: defaultConsoleThemes.map((theme) => theme.id),
          defaultThemeId: "gateway-dark",
          availableThemes: defaultConsoleThemes,
        },
      });
    if (endpoint.endsWith("/browse")) {
      const parent = url.searchParams.get("parent");
      if (!parent)
        return route.fulfill({
          json: {
            items: [
              {
                id: "namespace",
                kind: format === "maven" ? "namespace" : "directory",
                name:
                  format === "maven"
                    ? "com.example.engineering"
                    : "release notes",
                hasChildren: true,
              },
            ],
          },
        });
      if (format === "raw" || parent === "build")
        return route.fulfill({ json: { items: [asset] } });
      if (parent === "namespace")
        return route.fulfill({
          json: {
            items: [
              {
                id: "component",
                kind: "component",
                name: "widget",
                hasChildren: true,
              },
            ],
          },
        });
      return route.fulfill({
        json: {
          items: [
            {
              id: "build",
              kind: "version",
              name: "1.0-SNAPSHOT · 20260907.010203-2",
              hasChildren: true,
              coordinate,
              buildNumber: 2,
            },
          ],
        },
      });
    }
    if (endpoint.endsWith("/capabilities"))
      return route.fulfill({
        json: {
          format,
          type: "proxy",
          operations: ["read", "browse"],
          artifactScanning: false,
          publicationScanning: false,
        },
      });
    if (endpoint.endsWith("/effective-access"))
      return route.fulfill({
        json: {
          actor: "admin",
          repository: repo,
          permissions: {
            read: allowed,
            write: allowed,
            admin: allowed,
            intelligence: allowed,
          },
          anonymousRead: { allowed: false },
        },
      });
    if (endpoint.endsWith("/capacity"))
      return route.fulfill({
        json: {
          repositoryId,
          format,
          usedBytes: 42,
          objectCount: 1,
          quotaBytes: 0,
        },
      });
    if (endpoint.endsWith("/cache"))
      return route.fulfill({
        json: {
          items: [
            {
              key: coordinate,
              coordinate,
              version: "1.0-SNAPSHOT",
              assetCount: 1,
              size: 42,
              assets: [
                {
                  path,
                  name: asset.name,
                  digest: asset.digest,
                  size: 42,
                  sidecar: false,
                },
              ],
            },
          ],
          totalEstimate: 1,
          groupBy: "version",
        },
      });
    if (endpoint.endsWith("/artifact-search"))
      return route.fulfill({ json: { items: [asset] } });
    if (endpoint.endsWith("/artifact-intelligence"))
      return route.fulfill({
        json: {
          coordinate,
          digest: asset.digest,
          signatures: [],
          sboms: [],
          licenses: [],
        },
      });
    if (endpoint.endsWith("/health"))
      return route.fulfill({
        json: {
          endpoint: repo.endpoint,
          reachable: true,
          status: 200,
          proxyAllowed: true,
          circuitOpen: false,
          cacheEnabled: true,
          checkedAt: "2026-09-07T01:02:03Z",
        },
      });
    if (endpoint === `/api/v2/repositories/${repositoryId}`)
      return route.fulfill({ json: repo });
    return route.fulfill({ json: { items: [] } });
  });
  await authenticateAsAdmin(page);
  return { asset, coordinate, path };
}

for (const format of ["maven", "raw"] as const) {
  for (const width of [1440, 390]) {
    test(`${format} Proxy directory preserves identity and geometry at ${width}px`, async ({
      page,
    }, testInfo) => {
      await page.setViewportSize({ width, height: 900 });
      await page.emulateMedia({ reducedMotion: "reduce" });
      const failures: string[] = [];
      page.on("pageerror", (error) => failures.push(error.message));
      page.on("console", (message) => {
        if (message.type() === "error") failures.push(message.text());
      });
      const { asset, coordinate, path } = await mockProxyDirectory(
        page,
        format,
      );
      await page.goto(`/repositories/${repositoryId}`);
      await page.getByText("目录", { exact: true }).click();
      const tree = page.locator(".ag-repository-tree");
      await tree
        .getByText(
          format === "maven" ? "com.example.engineering" : "release notes",
          { exact: true },
        )
        .click();
      if (format === "maven") {
        await tree.getByText("widget", { exact: true }).click();
        await tree
          .getByText("1.0-SNAPSHOT · 20260907.010203-2", { exact: true })
          .click();
      }
      await tree.getByText(asset.name!, { exact: true }).click();
      const inspector = page.getByRole("complementary", { name: "节点详情" });
      await expect(
        inspector.getByText("已缓存", { exact: true }),
      ).toBeVisible();
      await expect(
        inspector.getByText(repositoryName, { exact: true }),
      ).toBeVisible();
      if (format === "maven")
        await expect(
          inspector.getByText(snapshotPath, { exact: true }),
        ).toBeVisible();
      const [toolbar, directory, pane, detail] = await Promise.all([
        page.locator(".ag-artifact-view-toolbar").boundingBox(),
        page.locator(".ag-repository-browse").boundingBox(),
        page.locator(".ag-repository-tree-pane").boundingBox(),
        inspector.boundingBox(),
      ]);
      expect(toolbar && directory && pane && detail).toBeTruthy();
      const gap = directory!.y - toolbar!.y - toolbar!.height;
      expect(gap).toBeGreaterThanOrEqual(15);
      expect(gap).toBeLessThanOrEqual(18);
      if (width === 1440)
        expect(pane!.x + pane!.width).toBeLessThanOrEqual(detail!.x + 1);
      else expect(pane!.y + pane!.height).toBeLessThanOrEqual(detail!.y + 1);
      expect(
        await page.evaluate(
          () => document.documentElement.scrollWidth <= innerWidth,
        ),
      ).toBe(true);
      await page.screenshot({
        path: testInfo.outputPath(`proxy-${format}-${width}.png`),
        fullPage: true,
      });
      await inspector.getByRole("button", { name: "在列表中查看" }).click();
      await expect
        .poll(() => new URL(page.url()).searchParams.get("artifact"))
        .toBe(coordinate);
      expect(new URL(page.url()).searchParams.get("asset")).toBe(path);
      if (format === "maven")
        expect(new URL(page.url()).searchParams.get("build")).toBe("2");
      await page.reload();
      await expect(
        page.getByRole("alert", { name: "页面加载失败" }),
      ).toHaveCount(0);
      expect(failures).toEqual([]);
      await expect(
        page.getByPlaceholder(
          format === "maven" ? "搜索 GAV 坐标…" : "搜索路径…",
        ),
      ).toHaveValue(format === "raw" ? decodeURIComponent(path) : path);
      if (format === "raw")
        await expect(
          page.getByRole("button", { name: "删除文件", exact: true }),
        ).toHaveCount(0);
      expect(failures).toEqual([]);
    });
  }
}
