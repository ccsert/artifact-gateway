import { expect, test } from "@playwright/test";

test("public browse surfaces stay aligned with the dark console palette", async ({
  page,
}) => {
  await page.goto("/browse");
  await expect(page.getByRole("heading", { name: "公开制品" })).toBeVisible();

  const card = page.locator(".ant-card");
  await expect(card).not.toHaveCount(0);
  const surface = await card.first().evaluate((element) => {
    const style = getComputedStyle(element);
    const content = element.querySelector(".ant-card-body > div");
    return {
      background: style.backgroundColor,
      border: style.borderColor,
      contentPadding: content ? getComputedStyle(content).padding : null,
    };
  });

  expect(surface).toEqual({
    background: "rgba(24, 24, 27, 0.55)",
    border: "rgba(63, 63, 70, 0.35)",
    contentPadding: "24px",
  });
});

test("management tables keep action columns opaque and cards separated", async ({
  page,
}) => {
  await page.setViewportSize({ width: 900, height: 800 });
  await page.addInitScript(() => {
    localStorage.setItem("ag.console.token", "mock-admin-token");
    localStorage.setItem("ag.console.role", "admin");
  });
  await page.route("**/api/v2/repositories**", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        items: [
          {
            id: "repo-1",
            name: "images",
            format: "oci",
            type: "hosted",
            anonymousRead: true,
            state: "active",
            version: "1",
          },
        ],
      }),
    }),
  );
  await page.route("**/api/v2/repositories/repo-1/capacity**", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        repositoryId: "repo-1",
        format: "oci",
        usedBytes: 1024,
        objectCount: 1,
        quotaBytes: 0,
      }),
    }),
  );
  await page.route("**/api/v2/users**", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        items: [
          {
            id: "user-1",
            name: "test",
            role: "admin",
            state: "active",
            version: "1",
            createdAt: "2026-08-05T00:00:00Z",
          },
        ],
      }),
    }),
  );

  await page.goto("/users");
  await expect(page.getByRole("heading", { name: "用户" })).toBeVisible();

  const summary = page.getByRole("group", { name: "页面摘要" });
  const card = summary.locator(
    "xpath=following-sibling::*[contains(@class, 'ant-card')][1]",
  );
  const gap = await summary.evaluate((element) => {
    const next = element.nextElementSibling;
    if (!next) return 0;
    return (
      next.getBoundingClientRect().top - element.getBoundingClientRect().bottom
    );
  });
  await expect(card).toBeVisible();
  expect(gap).toBeGreaterThanOrEqual(16);

  await page.goto("/repositories");
  await expect(page.getByRole("heading", { name: "仓库" })).toBeVisible();
  const actionCell = page.locator(".ant-table-cell-fix-end").first();
  await expect(actionCell).toBeVisible();
  const actionSurface = await actionCell.evaluate((element) => {
    const style = getComputedStyle(element);
    return style.backgroundColor;
  });
  expect(actionSurface).toBe("rgb(20, 20, 23)");
});

test("deleted repositories stay archived unless explicitly requested", async ({
  page,
}) => {
  await page.addInitScript(() => {
    localStorage.setItem("ag.console.token", "mock-admin-token");
    localStorage.setItem("ag.console.role", "admin");
  });
  await page.route("**/api/v2/repositories**", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        items: [
          {
            id: "repo-active",
            name: "active-repository",
            format: "oci",
            type: "hosted",
            state: "active",
            version: "1",
          },
          {
            id: "repo-deleted",
            name: "archived-repository",
            format: "oci",
            type: "hosted",
            state: "deleted",
            version: "3",
          },
        ],
      }),
    }),
  );
  await page.route("**/api/v2/repositories/repo-active/capacity**", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        repositoryId: "repo-active",
        format: "oci",
        usedBytes: 0,
        objectCount: 0,
        quotaBytes: 0,
      }),
    }),
  );

  await page.goto("/repositories");
  await expect(page.getByText("active-repository")).toBeVisible();
  await expect(page.getByText("archived-repository")).not.toBeVisible();

  await page.getByText("运行中与删除中", { exact: true }).click();
  await page.getByText("已删除", { exact: true }).click();
  await expect(page.getByText("archived-repository")).toBeVisible();
  await expect(page.getByText("active-repository")).not.toBeVisible();
});

test("dashboard excludes archived repositories from operational status", async ({
  page,
}) => {
  await page.addInitScript(() => {
    localStorage.setItem("ag.console.token", "mock-admin-token");
    localStorage.setItem("ag.console.role", "admin");
  });
  await page.route("**/api/v2/repositories**", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        items: [
          {
            id: "repo-deleted",
            name: "archived-repository",
            format: "raw",
            type: "hosted",
            state: "deleted",
            version: "3",
          },
          {
            id: "repo-active",
            name: "active-repository",
            format: "oci",
            type: "hosted",
            state: "active",
            version: "1",
          },
        ],
      }),
    }),
  );
  await page.route("**/api/v2/groups**", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ items: [] }),
    }),
  );
  await page.route("**/api/v2/audits**", (route) =>
    route.fulfill({ status: 200, contentType: "application/json", body: "[]" }),
  );
  await page.route("**/api/v2/repository-capacities**", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify([
        {
          repositoryId: "repo-active",
          format: "oci",
          usedBytes: 1024,
          objectCount: 1,
          quotaBytes: 0,
        },
      ]),
    }),
  );

  await page.goto("/");
  await expect(page.getByText("平台运行正常")).toBeVisible();
  await expect(page.getByText("active-repository")).toBeVisible();
  await expect(page.getByText("archived-repository")).not.toBeVisible();
  await expect(
    page.getByRole("group", { name: "页面摘要" }).locator(":scope > div").first(),
  ).toContainText("1");
});
