import { expect, test, type Page } from "@playwright/test";
import type { Repository, UpdateRepositoryWritable } from "../src/client";
import { defaultSiteSettings } from "../src/lib/siteSettings";
import { authenticateAsAdmin } from "./support/auth";
import {
  attachThemeEvidence,
  recordThemeEvidence,
} from "./support/themeEvidence";

test.beforeEach(async ({ page }) => recordThemeEvidence(page));
test.afterEach(async ({ page }, info) => attachThemeEvidence(page, info));

const repository: Repository = {
  id: "33333333-3333-4333-8333-333333333333",
  name: "private-registry",
  format: "oci",
  type: "proxy",
  endpoint: "https://registry.example.test",
  allowedHosts: ["registry.example.test"],
  upstreamAuth: { scheme: "bearer", credentialsConfigured: true },
  ociBearer: {
    realm: "https://issuer.example.test/token",
    service: "private-registry",
  },
  egressProxy: {
    mode: "custom",
    protocol: "http",
    host: "proxy.example.test",
    port: 8080,
    credentialsConfigured: true,
  },
  anonymousRead: false,
  mavenStrictPublication: false,
  state: "active",
  version: "7",
};

async function mockSettings(page: Page) {
  await authenticateAsAdmin(page);
  await page.route("**/api/v2/site-settings", (route) =>
    route.fulfill({ json: defaultSiteSettings }),
  );
  let saved = repository;
  const updates: UpdateRepositoryWritable[] = [];
  await page.route(
    `**/api/v2/repositories/${repository.id}**`,
    async (route) => {
      const request = route.request();
      const path = new URL(request.url()).pathname;
      if (request.method() === "PATCH") {
        const body: UpdateRepositoryWritable = request.postDataJSON();
        updates.push(body);
        expect(request.headers()["if-match"]).toBe("7");
        if (updates.length < 3) {
          return route.fulfill({
            status: 400,
            contentType: "application/problem+json",
            json: {
              status: 400,
              code:
                updates.length === 1
                  ? "upstream_credential_rebind_required"
                  : "invalid_request",
              message: "Synthetic upstream configuration rejected",
              requestId: "safe-config-error",
            },
          });
        }
        saved = {
          ...repository,
          ociBearer: undefined,
          upstreamAuth: undefined,
          version: "8",
        };
        return route.fulfill({ json: saved });
      }
      if (path.endsWith("/capabilities"))
        return route.fulfill({
          json: {
            format: "oci",
            type: "proxy",
            operations: ["read", "browse"],
            artifactScanning: false,
            publicationScanning: false,
          },
        });
      if (path.endsWith("/capacity"))
        return route.fulfill({
          json: {
            repositoryId: repository.id,
            format: "oci",
            usedBytes: 0,
            objectCount: 0,
            quotaBytes: 0,
          },
        });
      if (path.endsWith("/effective-access")) {
        const allowed = {
          allowed: true,
          source: "administrator",
          reason: "administrator",
        };
        return route.fulfill({
          json: {
            actor: "mock-admin",
            identity: {
              actor: "mock-admin",
              kind: "local_session",
              role: "admin",
              administrator: true,
            },
            repository: {
              id: repository.id,
              name: repository.name,
              format: "oci",
              type: "proxy",
              state: "active",
            },
            anonymousRead: {
              allowed: false,
              source: "anonymous_policy",
              reason: "repository_anonymous_read_disabled",
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
      return route.fulfill({ json: saved });
    },
  );
  return updates;
}

for (const width of [1440, 390]) {
  test(`cached repository history navigation drops source and proxy secrets at ${width}px`, async ({
    page,
  }) => {
    const errors: string[] = [];
    page.on("pageerror", (error) => errors.push(error.message));
    page.on("console", (message) => {
      if (message.type() === "error") errors.push(message.text());
    });
    await page.setViewportSize({ width, height: 900 });
    await page.addInitScript(() =>
      localStorage.setItem("ag.console.locale", "en-US"),
    );
    await mockSettings(page);
    const second: Repository = {
      ...repository,
      id: "44444444-4444-4444-8444-444444444444",
      name: "second-registry",
      version: "9",
      endpoint: "https://second.example.test",
      allowedHosts: ["second.example.test"],
      ociBearer: null,
    };
    const writes: UpdateRepositoryWritable[] = [];
    await page.route(`**/api/v2/repositories/${second.id}**`, async (route) => {
      const request = route.request();
      const path = new URL(request.url()).pathname;
      if (request.method() === "PATCH") {
        expect(request.headers()["if-match"]).toBe("9");
        writes.push(request.postDataJSON());
        return route.fulfill({
          json: { ...second, version: "10" },
        });
      }
      if (path.endsWith("/capabilities"))
        return route.fulfill({
          json: {
            format: "oci",
            type: "proxy",
            operations: ["read", "browse"],
          },
        });
      if (path.endsWith("/capacity"))
        return route.fulfill({
          json: {
            repositoryId: second.id,
            format: "oci",
            usedBytes: 0,
            objectCount: 0,
            quotaBytes: 0,
          },
        });
      if (path.endsWith("/effective-access"))
        return route.fulfill({ json: {} });
      return route.fulfill({ json: second });
    });
    // Load B once, then navigate within the same document. Back reaches B's
    // warm QueryClient entry without a Loading unmount of the settings form.
    await page.goto(`/repositories/${second.id}?tab=settings`);
    await expect(page.getByLabel("Upstream URL", { exact: true })).toHaveValue(
      second.endpoint ?? "",
    );
    await page.evaluate((id) => {
      history.pushState({}, "", `/repositories/${id}?tab=settings`);
      dispatchEvent(new PopStateEvent("popstate"));
    }, repository.id);
    await expect(page.getByLabel("Upstream URL", { exact: true })).toHaveValue(
      repository.endpoint ?? "",
    );
    await page
      .getByLabel("Upstream token", { exact: true })
      .fill("owned-first-upstream-secret");
    await page
      .getByLabel("Proxy password (optional)", { exact: true })
      .fill("owned-first-proxy-secret");
    await page.goBack();
    await expect(
      page.getByRole("heading", { name: second.name, exact: true }),
    ).toBeVisible();
    await expect(page.getByLabel("Upstream URL", { exact: true })).toHaveValue(
      second.endpoint ?? "",
    );
    await expect(
      page.getByLabel("Upstream token", { exact: true }),
    ).toHaveValue("");
    await expect(
      page.getByLabel("Proxy password (optional)", { exact: true }),
    ).toHaveValue("");
    await expect(
      page.getByRole("switch", { name: "OCI token exchange" }),
    ).not.toBeChecked();
    await page.getByRole("button", { name: "Save changes" }).click();
    await expect.poll(() => writes.length).toBe(1);
    expect(writes[0]).toMatchObject({
      endpoint: second.endpoint,
      allowedHosts: second.allowedHosts,
    });
    expect(JSON.stringify(writes)).not.toContain("owned-first-");
    expect(
      await page
        .locator("html")
        .evaluate((node) => node.scrollWidth - node.clientWidth),
    ).toBeLessThanOrEqual(0);
    expect(errors).toEqual([]);
  });
}

for (const width of [1440, 390]) {
  for (const theme of ["light", "dark"] as const) {
    test(`OCI issuer editing clears secrets and keeps layout at ${width}px in ${theme}`, async ({
      page,
    }, info) => {
      const errors: string[] = [];
      page.on("pageerror", (error) => errors.push(error.message));
      page.on("console", (message) => {
        if (message.type() === "error") errors.push(message.text());
      });
      await page.setViewportSize({ width, height: 900 });
      await page.addInitScript((mode) => {
        localStorage.setItem("ag.console.locale", "zh-CN");
        localStorage.setItem("ag.console.theme", mode);
      }, theme);
      const updates = await mockSettings(page);
      await page.goto(`/repositories/${repository.id}?tab=settings`);
      const realm = page.getByLabel("令牌服务地址", { exact: true });
      const token = page.getByLabel("上游令牌", { exact: true });
      const password = page.getByLabel("代理认证密码（可选）", { exact: true });
      const save = page.getByRole("button", { name: /保存更改$/ });
      await expect(realm).toHaveValue(repository.ociBearer?.realm ?? "");
      await expect(token).toHaveValue("");
      await expect(password).toHaveValue("");
      await realm.fill("https://new-issuer.example.test/token");
      await expect(save).not.toHaveClass(/ant-btn-loading/);
      await save.click();
      await expect(
        page.getByText(/上游地址或 OCI 令牌绑定已更改/),
      ).toBeVisible();
      await expect(realm).toHaveValue("https://new-issuer.example.test/token");
      expect(updates[0]?.upstreamAuth).not.toHaveProperty("secret");
      expect(updates[0]?.allowedHosts).toEqual(repository.allowedHosts);

      const sourceSecret = "synthetic-private-source-value";
      const egressSecret = "synthetic-private-egress-value";
      await token.fill(sourceSecret);
      await password.fill(egressSecret);
      await expect(save).not.toHaveClass(/ant-btn-loading/);
      await save.click();
      await expect(
        page.getByText("Synthetic upstream configuration rejected"),
      ).toBeVisible();
      await expect(token).toHaveValue("");
      await expect(password).toHaveValue("");
      expect(updates[1]?.upstreamAuth?.secret).toBe(sourceSecret);
      expect(updates[1]?.egressProxy?.password).toBe(egressSecret);
      const storage = await page.evaluate(() =>
        JSON.stringify({
          local: { ...localStorage },
          session: { ...sessionStorage },
        }),
      );
      for (const secret of [sourceSecret, egressSecret]) {
        expect(page.url()).not.toContain(secret);
        expect(storage).not.toContain(secret);
        await expect(page.locator("body")).not.toContainText(secret);
      }

      const issuer = page.getByRole("group", {
        name: "OCI 令牌交换",
        exact: true,
      });
      const authentication = page.getByRole("group", {
        name: "上游认证",
        exact: true,
      });
      const issuerBox = await issuer.boundingBox();
      const authenticationBox = await authentication.boundingBox();
      expect(issuerBox).not.toBeNull();
      expect(authenticationBox).not.toBeNull();
      const gap =
        (authenticationBox?.y ?? 0) -
        (issuerBox?.y ?? 0) -
        (issuerBox?.height ?? 0);
      expect(gap).toBeGreaterThanOrEqual(14);
      expect(gap).toBeLessThanOrEqual(18);
      expect(
        await page.evaluate(
          () => document.body.scrollWidth - document.body.clientWidth,
        ),
      ).toBe(0);
      await page.screenshot({
        path: info.outputPath(`private-issuer-${width}-${theme}.png`),
        fullPage: true,
      });

      await page
        .getByRole("switch", { name: "OCI 令牌交换", exact: true })
        .click();
      await page.getByRole("radio", { name: /匿名访问/ }).click();
      await expect(save).not.toHaveClass(/ant-btn-loading/);
      await save.click();
      await expect(page.getByText("仓库设置已保存")).toBeVisible();
      expect(updates[2]?.ociBearer).toBeNull();
      expect(updates[2]?.upstreamAuth).toEqual({ scheme: "none" });
      expect(updates[2]?.egressProxy).not.toHaveProperty("password");
      await expect(realm).toHaveCount(0);
      // Browsers report expected HTTP 400 responses as console errors.
      expect(
        errors.filter(
          (error) =>
            !/Failed to load resource: the server responded with a status of 400/.test(
              error,
            ),
        ),
      ).toEqual([]);
    });
  }
}
