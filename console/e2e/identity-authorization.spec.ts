import { expect, test } from "@playwright/test";
import { authenticateWithIdentity } from "./support/auth";

test("server identity overrides a stale administrator role", async ({
  page,
}) => {
  await authenticateWithIdentity(page, {
    actor: "gateway-resolver",
    kind: "static_resolver",
    administrator: false,
  });

  await page.goto("/access");

  await expect(page).toHaveURL(/\/search$/);
  await expect(page.getByRole("heading", { name: "全局搜索" })).toBeVisible();
  await expect(page.getByRole("link", { name: "制品搜索" })).toBeVisible();
  await expect(page.getByRole("link", { name: "访问控制" })).toHaveCount(0);
  await expect(page.getByRole("link", { name: "API 密钥" })).toHaveCount(0);
});

test("a scoped token signs in through identity without repository discovery", async ({
  page,
}) => {
  let repositoryDiscoveryRequests = 0;
  await page.route("**/api/v2/identity", (route) =>
    route.fulfill({
      json: {
        actor: "gateway-resolver",
        kind: "static_resolver",
        administrator: false,
      },
    }),
  );
  await page.route("**/api/v2/repositories?**", (route) => {
    repositoryDiscoveryRequests += 1;
    return route.fulfill({ status: 403, json: { code: "access_denied" } });
  });

  await page.goto("/login");
  await page.getByText("访问令牌", { exact: true }).click();
  await page.getByRole("textbox", { name: "访问令牌" }).fill("scoped-token");
  await page.getByRole("button", { name: /验证并登录/ }).click();

  await expect(page).toHaveURL(/\/search$/);
  await expect(page.getByRole("heading", { name: "全局搜索" })).toBeVisible();
  expect(repositoryDiscoveryRequests).toBe(0);
});
