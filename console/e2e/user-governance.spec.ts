import { expect, test } from "@playwright/test";

const token = process.env.GATEWAY_ADMIN_TOKEN;
const authorization = { Authorization: `Bearer ${token!}` };
let createdUserId = "";

test.skip(!token, "GATEWAY_ADMIN_TOKEN is required for user governance E2E");

test.afterEach(async ({ request }) => {
  if (!createdUserId) return;
  const deleted = await request.delete(`/api/v2/users/${createdUserId}`, {
    headers: authorization,
  });
  expect(
    deleted.ok() || deleted.status() === 404,
    `delete user: ${deleted.status()} ${await deleted.text()}`,
  ).toBeTruthy();
  createdUserId = "";
});

test("mandatory password change keeps the restricted session in memory", async ({
  page,
  request,
}) => {
  const suffix = `${Date.now()}-${Math.random().toString(16).slice(2, 8)}`;
  const username = `e2e-password-${suffix}`;
  const temporaryPassword = `Temporary-${suffix}`;
  const personalPassword = `Personal-${suffix}`;

  const created = await request.post("/api/v2/users", {
    headers: authorization,
    data: {
      name: username,
      displayName: "Password change E2E",
      email: `${username}@example.test`,
      description: "Temporary browser test account",
      password: temporaryPassword,
      role: "reader",
      mustChangePassword: true,
    },
  });
  expect(
    created.ok(),
    `create user: ${created.status()} ${await created.text()}`,
  ).toBeTruthy();
  createdUserId = ((await created.json()) as { id: string }).id;

  await page.goto("/login");
  await page.getByLabel("用户名", { exact: true }).fill(username);
  const passwordInput = page.getByLabel("密码", { exact: true });
  await passwordInput.fill(temporaryPassword);

  const [loginResponse] = await Promise.all([
    page.waitForResponse(
      (response) =>
        response.url().endsWith("/auth/login") &&
        response.request().method() === "POST",
      { timeout: 10_000 },
    ),
    passwordInput.press("Enter"),
  ]);
  expect(loginResponse.status()).toBe(200);
  const restricted = (await loginResponse.json()) as {
    token: string;
    mustChangePassword: boolean;
  };
  expect(restricted.mustChangePassword).toBe(true);

  await expect(page.getByText("更新初始密码", { exact: true })).toBeVisible();
  expect(
    await page.evaluate(() => localStorage.getItem("ag.console.token")),
  ).toBeNull();

  await page.getByLabel("新密码", { exact: true }).fill(personalPassword);
  const confirmPassword = page.getByLabel("确认新密码", { exact: true });
  await confirmPassword.fill(personalPassword);
  const [passwordResponse] = await Promise.all([
    page.waitForResponse(
      (response) =>
        response.url().endsWith("/auth/change-password") &&
        response.request().method() === "POST",
      { timeout: 10_000 },
    ),
    confirmPassword.press("Enter"),
  ]);
  expect(passwordResponse.status()).toBe(204);

  await expect(page).toHaveURL("/search");
  const storedToken = await page.evaluate(() =>
    localStorage.getItem("ag.console.token"),
  );
  expect(storedToken).toBeTruthy();
  expect(storedToken).not.toBe(restricted.token);
});
