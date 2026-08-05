import { expect, test } from "@playwright/test";

const repositoryId = "20000000-0000-0000-0000-000000000001";

test("global Maven search groups versions and preserves an exact deep link", async ({
  page,
}) => {
  await page.addInitScript(() => {
    localStorage.setItem("ag.console.token", "mock-admin-token");
    localStorage.setItem("ag.console.role", "admin");
  });
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
