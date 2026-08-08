import { expect, test } from "@playwright/test";

const token = process.env.GATEWAY_ADMIN_TOKEN;

test.skip(
  !token,
  "GATEWAY_ADMIN_TOKEN is required for the administrator browser gate",
);

test("administrator dashboard loads management data through the Console proxy", async ({
  page,
}) => {
  await page.addInitScript(
    ([accessToken, role]) => {
      localStorage.setItem("ag.console.token", accessToken);
      localStorage.setItem("ag.console.role", role);
    },
    [token!, "admin"],
  );

  const repositories = page.waitForResponse(
    (response) =>
      response.url().includes("/api/v2/repositories") &&
      response.status() === 200,
  );
  await page.goto("/");
  await repositories;
  await expect(page.getByRole("heading", { name: "总览" })).toBeVisible();
  await expect(page.getByText("仓库总数")).toBeVisible();
});
