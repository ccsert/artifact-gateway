import { expect, test } from "@playwright/test";

const enabled = process.env.KEYCLOAK_OIDC_E2E === "1";
const username = process.env.KEYCLOAK_OIDC_USERNAME ?? "gateway-admin";
const password =
  process.env.KEYCLOAK_OIDC_PASSWORD ?? "gateway-oidc-test-password";
const consolePort = process.env.PLAYWRIGHT_PORT ?? "4173";
const keycloakPort = process.env.KEYCLOAK_OIDC_PORT ?? "8081";

test.describe("Keycloak OIDC browser sign-in", () => {
  test.skip(!enabled, "Set KEYCLOAK_OIDC_E2E=1 for the real Keycloak fixture.");

  test("completes Authorization Code + PKCE and creates a Gateway session", async ({
    page,
  }) => {
    await page.goto("/login");
    const signIn = page.getByRole("button", { name: /SSO|企业登录/ });
    await expect(signIn).toBeVisible();
    await signIn.click();

    await expect(page).toHaveURL(
      new RegExp(`127\\.0\\.0\\.1:${keycloakPort}/realms/artifact-gateway`),
    );
    await page.locator("#username").fill(username);
    await page.locator("#password").fill(password);
    await page.locator("#kc-login").click();

    await expect(page).toHaveURL(
      new RegExp(`127\\.0\\.0\\.1:${consolePort}/$`),
    );
    await expect(
      page.getByRole("button", { name: /退出|Sign out/ }),
    ).toBeVisible();

    const session = await page.request.get("/auth/session");
    await expect(session).toBeOK();
    const sessionBody = await session.json();
    expect(sessionBody).toMatchObject({
      authenticated: true,
      identity: {
        kind: "oidc",
        role: "admin",
        administrator: true,
      },
    });
    // OIDC `sub` is the provider's stable opaque subject, not the login name.
    expect(sessionBody.identity.actor).toEqual(expect.any(String));
    expect(sessionBody.identity.actor).not.toBe(username);

    expect(
      (await page.context().cookies()).find(
        (cookie) => cookie.name === "ag_session",
      ),
    ).toMatchObject({ httpOnly: true, sameSite: "Lax" });
  });
});
