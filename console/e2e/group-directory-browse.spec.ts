import { expect, test } from "@playwright/test";
import { defaultConsoleThemes } from "../src/lib/consoleTheme";
import { authenticateAsAdmin } from "./support/auth";

const groupId = "11111111-1111-4111-8111-111111111111";
const hostedId = "22222222-2222-4222-8222-222222222222";
const proxyId = "33333333-3333-4333-8333-333333333333";
const path =
  "com/aerok/platform/pipeline-sdk/2.4.0/pipeline-sdk-2.4.0-sources.jar";
const sources = [
  {
    repositoryId: hostedId,
    repositoryName: "engineering-releases",
    type: "hosted",
    resolutionOrder: 1,
    coordinate: "com.aerok.platform:pipeline-sdk:2.4.0",
    path,
    digest: `sha256:${"a".repeat(64)}`,
    size: 284672,
  },
  {
    repositoryId: proxyId,
    repositoryName: "internal-maven-mirror-with-a-long-repository-name",
    type: "proxy",
    resolutionOrder: 3,
    coordinate: "com.aerok.platform:pipeline-sdk:2.4.0",
    path,
    digest: `sha256:${"b".repeat(64)}`,
    size: 285696,
    cacheRepositoryName: "maven-public",
    cachedAt: "2026-09-08T01:02:03Z",
  },
];
const asset = {
  id: "asset",
  kind: "asset",
  name: "pipeline-sdk-2.4.0-sources.jar",
  hasChildren: false,
  coordinate: sources[0].coordinate,
  path,
  sources,
};

for (const [width, theme] of [
  [1440, "gateway-dark"],
  [1024, "gateway-light"],
  [390, "gateway-dark"],
] as const) {
  test(`Group directory shows source evidence and usable geometry at ${width}px`, async ({
    page,
  }, testInfo) => {
    await page.setViewportSize({ width, height: 1000 });
    await page.emulateMedia({ reducedMotion: "reduce" });
    const failures: string[] = [];
    page.on("pageerror", (error) => failures.push(error.message));
    page.on("console", (message) => {
      if (
        message.type() === "error" &&
        !(failRefresh && message.text().includes("400"))
      )
        failures.push(message.text());
    });
    let failRefresh = false;
    await page.route("**/api/v2/**", (route) => {
      const url = new URL(route.request().url());
      if (url.pathname.endsWith("/site-settings"))
        return route.fulfill({
          json: {
            version: "1",
            siteName: "Artifact Gateway",
            logoUrl: "",
            brandMark: "AG",
            enabledThemeIds: defaultConsoleThemes.map((item) => item.id),
            defaultThemeId: theme,
            availableThemes: defaultConsoleThemes,
          },
        });
      if (!url.pathname.endsWith("/browse"))
        return route.fulfill({ json: { items: [] } });
      const parent = url.searchParams.get("parent");
      if (!parent && failRefresh)
        return route.fulfill({
          status: 400,
          json: {
            code: "invalid_page_token",
            message: "Navigation changed. Refresh the directory.",
          },
        });
      let items: object[];
      if (!parent)
        items = [
          "com.aerok.platform",
          "com.aerok.tools",
          "io.opentelemetry",
          "org.apache.commons",
          "org.springframework",
        ].map((name, index) => ({
          id: `namespace-${index}`,
          kind: "namespace",
          name,
          hasChildren: true,
        }));
      else if (parent.startsWith("namespace"))
        items = ["pipeline-sdk", "repository-client", "shared-models"].map(
          (name, index) => ({
            id: `component-${index}`,
            kind: "component",
            name,
            hasChildren: true,
          }),
        );
      else if (parent.startsWith("component"))
        items = ["2.4.0", "2.5.0-SNAPSHOT · 20260908.010203-2"].map(
          (name, index) => ({
            id: `version-${index}`,
            kind: "version",
            name,
            hasChildren: true,
          }),
        );
      else
        items = [
          asset,
          ...["jar", "pom", "jar.sha256"].map((extension) => ({
            id: extension,
            kind: "asset",
            name: `pipeline-sdk-2.4.0.${extension}`,
            hasChildren: false,
            sources,
          })),
        ];
      return route.fulfill({
        json: {
          groupId,
          groupName: "maven-public",
          format: "maven",
          items,
          candidates: [
            sources[0],
            {
              repositoryId: "44444444-4444-4444-8444-444444444444",
              repositoryName: "maven-central",
              type: "proxy",
              resolutionOrder: 2,
            },
            sources[1],
          ],
        },
      });
    });
    await authenticateAsAdmin(page);
    await page.goto(`/groups/${groupId}/browse`);
    const tree = page.locator(".ag-repository-tree");
    await tree.getByText("com.aerok.platform", { exact: true }).click();
    await tree.getByText("pipeline-sdk", { exact: true }).click();
    await tree.getByText("2.4.0", { exact: true }).click();
    await tree.getByText(asset.name, { exact: true }).click();
    const inspector = page.getByRole("complementary", { name: "节点详情" });
    await expect(
      inspector.getByRole("heading", { name: "本地来源 2" }),
    ).toBeVisible();
    await expect(
      inspector.getByText("缓存范围 · maven-public", { exact: true }),
    ).toBeVisible();
    await expect(
      inspector.getByRole("link", { name: sources[0].repositoryName }),
    ).toHaveAttribute("href", /artifact=com.aerok.platform/);
    await expect(
      inspector.getByRole("link", { name: sources[1].repositoryName }),
    ).toHaveAttribute("href", `/repositories/${proxyId}`);
    await expect(
      page
        .getByRole("region", { name: "可见成员候选顺序" })
        .getByRole("link", { name: "maven-central" }),
    ).toBeVisible();
    const [context, directory, left, right] = await Promise.all([
      page.locator(".ag-group-browse-context").boundingBox(),
      page.locator(".ag-repository-browse").boundingBox(),
      page.locator(".ag-repository-tree-pane").boundingBox(),
      inspector.boundingBox(),
    ]);
    expect(context && directory && left && right).toBeTruthy();
    const gap = directory!.y - context!.y - context!.height;
    expect(gap).toBeGreaterThanOrEqual(15);
    expect(gap).toBeLessThanOrEqual(18);
    if (width > 900)
      expect(left!.x + left!.width).toBeLessThanOrEqual(right!.x + 1);
    else expect(left!.y + left!.height).toBeLessThanOrEqual(right!.y + 1);
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= innerWidth,
      ),
    ).toBe(true);
    await page.screenshot({
      path: testInfo.outputPath(`group-directory-${width}.png`),
      fullPage: true,
    });
    failRefresh = true;
    await page.getByRole("button", { name: "刷新", exact: true }).click();
    await expect(
      page.getByText("Navigation changed. Refresh the directory."),
    ).toBeVisible();
    await expect(
      inspector.getByRole("link", { name: sources[0].repositoryName }),
    ).toBeVisible();
    failRefresh = false;
    await page.getByRole("button", { name: "刷新", exact: true }).click();
    await expect(inspector.getByText("从目录中选择一项")).toBeVisible();
    await tree.getByText("com.aerok.platform", { exact: true }).click();
    await page.getByRole("button", { name: "收起全部目录" }).click();
    await expect(
      tree.getByText("pipeline-sdk", { exact: true }),
    ).not.toBeVisible();
    // Keyboard expansion remains native to the Tree, including reduced motion.
    await page.getByRole("tree", { name: "制品目录树" }).focus();
    await page.keyboard.press("Home");
    await page.keyboard.press("ArrowRight");
    await expect(tree.getByText("pipeline-sdk", { exact: true })).toBeVisible();
    expect(failures).toEqual([]);
  });
}
