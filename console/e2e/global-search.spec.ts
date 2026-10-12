import { expect, test } from "@playwright/test";
import { authenticateAsAdmin } from "./support/auth";

test("contains search resets cursors on mode changes and clear, including a shared URL", async ({
  page,
}) => {
  await authenticateAsAdmin(page);
  await page.route("**/api/v2/repositories**", (route) =>
    route.fulfill({ json: { items: [] } }),
  );
  const requests: URLSearchParams[] = [];
  await page.route("**/api/v2/artifact-search**", (route) => {
    const params = new URL(route.request().url()).searchParams;
    requests.push(params);
    return route.fulfill({
      json: {
        searchedRepositories: 1,
        items: [
          {
            repositoryId: "20000000-0000-0000-0000-000000000001",
            repositoryName: "raw-releases",
            format: "raw",
            matchKind: "coordinate",
            match: params.get("match"),
            coordinate: params.has("pageToken")
              ? "releases/alpha-2.bin"
              : "releases/alpha.bin",
          },
        ],
        ...(params.has("pageToken")
          ? {}
          : { nextPageToken: "contains-page-2" }),
      },
    });
  });
  await page.goto("/search?q=alpha&match=contains");
  await expect(
    page.getByText("releases/alpha.bin", { exact: true }),
  ).toBeVisible();
  expect(requests[0].get("match")).toBe("contains");
  await page.getByRole("button", { name: "加载更多", exact: true }).click();
  await expect(
    page.getByText("releases/alpha-2.bin", { exact: true }),
  ).toBeVisible();
  expect(requests[1].get("pageToken")).toBe("contains-page-2");
  await page.getByText("前缀", { exact: true }).click();
  await expect.poll(() => requests.length).toBe(3);
  expect(requests[2].get("match")).toBe("prefix");
  expect(requests[2].has("pageToken")).toBe(false);
  await expect(
    page.getByText("releases/alpha-2.bin", { exact: true }),
  ).toHaveCount(0);
  await expect(page).toHaveURL(/match=prefix/);
  await page.getByRole("searchbox").fill("");
  await expect(
    page.getByText("输入关键词开始搜索", { exact: true }),
  ).toBeVisible();
  await expect(page).toHaveURL(/match=prefix/);
  expect(requests).toHaveLength(3);
});

test("public contains search appends server pages and isolates mode query keys", async ({
  page,
}) => {
  await page.route("**/auth/session", (route) =>
    route.fulfill({ json: { authenticated: false } }),
  );
  await page.route("**/api/v2/public/repositories", (route) =>
    route.fulfill({
      json: {
        enabled: true,
        items: [
          {
            id: "public-contains",
            name: "raw-public",
            format: "raw",
            type: "hosted",
            anonymousRead: true,
          },
        ],
      },
    }),
  );
  const requests: URLSearchParams[] = [];
  await page.route(
    "**/api/v2/repositories/public-contains/artifact-search**",
    (route) => {
      const params = new URL(route.request().url()).searchParams;
      requests.push(params);
      return route.fulfill({
        json: {
          items: [
            {
              coordinate: params.has("pageToken") ? "alpha-2.bin" : "alpha.bin",
            },
          ],
          ...(params.has("pageToken") ? {} : { nextPageToken: "public-next" }),
        },
      });
    },
  );
  await page.goto("/browse?repository=public-contains&q=alpha&match=contains");
  await expect(page.getByText("alpha.bin", { exact: true })).toBeVisible();
  expect(requests[0].get("match")).toBe("contains");
  await page.getByRole("button", { name: "加载更多", exact: true }).click();
  await expect(page.getByText("alpha-2.bin", { exact: true })).toBeVisible();
  expect(requests[1].get("pageToken")).toBe("public-next");
  await page.getByText("前缀", { exact: true }).click();
  await expect.poll(() => requests.length).toBe(3);
  expect(requests[2].get("match")).toBe("prefix");
  expect(requests[2].has("pageToken")).toBe(false);
  await expect(page.getByText("alpha-2.bin", { exact: true })).toHaveCount(0);
});

const repositoryId = "20000000-0000-0000-0000-000000000001";

test("global Maven search groups versions and preserves an exact deep link", async ({
  page,
}) => {
  await authenticateAsAdmin(page);
  await page.route("**/api/v2/repositories**", (route) =>
    route.fulfill({ json: { items: [] } }),
  );
  await page.route("**/api/v2/artifact-search**", (route) =>
    route.fulfill({
      json: {
        searchedRepositories: 1,
        items: [
          {
            repositoryId,
            repositoryName: "maven-hosted",
            format: "maven",
            coordinate: "com.example:demo:1.0.0",
            digest: `sha256:${"a".repeat(64)}`,
            size: 1024,
            createdAt: "2026-01-01T00:00:00Z",
            publisher: "ci",
          },
          {
            repositoryId,
            repositoryName: "maven-hosted",
            format: "maven",
            coordinate: "com.example:demo:2.0.0",
            digest: `sha256:${"b".repeat(64)}`,
            size: 2048,
            createdAt: "2026-02-01T00:00:00Z",
            publisher: "release-bot",
          },
        ],
      },
    }),
  );
  await page.route(
    `**/api/v2/repositories/${repositoryId}/maven/coordinates**`,
    (route) =>
      route.fulfill({
        json: {
          items: [
            {
              coordinate: "com.example:demo:1.0.0",
              digest: `sha256:${"a".repeat(64)}`,
              createdAt: "2026-01-01T00:00:00Z",
              publisher: "ci",
            },
            {
              coordinate: "com.example:demo:2.0.0",
              digest: `sha256:${"b".repeat(64)}`,
              createdAt: "2026-02-01T00:00:00Z",
              publisher: "release-bot",
            },
          ],
        },
      }),
  );
  await page.route(
    `**/api/v2/repositories/${repositoryId}/artifact-intelligence**`,
    (route) =>
      route.fulfill({
        status: 404,
        json: {
          code: "not_found",
          message: "artifact intelligence not found",
          status: 404,
        },
      }),
  );

  await page.goto("/search?q=demo");
  await expect(
    page.getByText("com.example:demo", { exact: true }),
  ).toBeVisible();
  await expect(page.getByText("2 个版本", { exact: true })).toBeVisible();

  const repositoryLink = page.getByRole("link", {
    name: "maven-hosted",
    exact: true,
  });
  const href = await repositoryLink.getAttribute("href");
  expect(href).toBeTruthy();
  expect(new URL(href!, "http://localhost").searchParams.get("artifact")).toBe(
    "com.example:demo:2.0.0",
  );

  await page.getByRole("button", { name: "展开 com.example:demo" }).click();
  await expect(page.getByText("版本与构建", { exact: true })).toBeVisible();
  await expect(
    page.getByRole("button", { name: /1\.0\.0/ }).first(),
  ).toBeVisible();
  await page
    .getByRole("button", { name: /1\.0\.0/ })
    .first()
    .click();
  await expect(
    page.getByText("com.example:demo:1.0.0", { exact: true }).last(),
  ).toBeVisible();
});
