import { expect, test } from "@playwright/test";
import type { FormatProfile, Repository } from "../src/client";
import { defaultSiteSettings } from "../src/lib/siteSettings";
import { authenticateAsAdmin } from "./support/auth";
import {
  attachThemeEvidence,
  recordThemeEvidence,
} from "./support/themeEvidence";

test.beforeEach(async ({ page }) => recordThemeEvidence(page));
test.afterEach(async ({ page }, info) => attachThemeEvidence(page, info));

const repository: Repository = {
  id: "b6acc734-7772-42a4-a4d4-a0da7e0d3470",
  name: "oci-discovery",
  format: "oci",
  type: "proxy",
  endpoint: "https://registry.example.test",
  allowedHosts: ["registry.example.test"],
  anonymousRead: false,
  mavenStrictPublication: false,
  state: "active",
  version: "1",
};

const profile: FormatProfile = {
  format: "oci",
  repositoryTypes: ["hosted", "proxy"],
  groupSupported: true,
  anonymousRead: true,
  hostedOperations: ["read", "publish", "browse"],
  proxyOperations: ["read", "browse"],
  ociDiscovery: {
    catalog: true,
    referrers: true,
    tagFallback: false,
    signatureVerification: false,
  },
};

for (const locale of ["en-US", "zh-CN"] as const) {
  for (const width of [1440, 390]) {
    test(`OCI discovery coverage stays clear in ${locale} at ${width}px`, async ({
      page,
    }, info) => {
      const errors: string[] = [];
      page.on("pageerror", (error) => errors.push(error.message));
      page.on("console", (message) => {
        if (message.type() === "error") errors.push(message.text());
      });
      await page.setViewportSize({ width, height: 900 });
      await page.addInitScript((language) => {
        localStorage.setItem("ag.console.locale", language);
        localStorage.setItem("ag.console.theme", "light");
      }, locale);
      await authenticateAsAdmin(page);
      await page.route("**/api/v2/site-settings", (route) =>
        route.fulfill({ json: defaultSiteSettings }),
      );
      await page.route("**/api/v2/formats", (route) =>
        route.fulfill({ json: { items: [profile] } }),
      );
      await page.route(`**/api/v2/repositories/${repository.id}**`, (route) => {
        const path = new URL(route.request().url()).pathname;
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
              repository,
              anonymousRead: { allowed: false },
              permissions: {
                read: allowed,
                write: allowed,
                admin: allowed,
                intelligence: allowed,
              },
            },
          });
        }
        if (path.endsWith("/oci/images"))
          return route.fulfill({
            json: { items: [{ name: "team/image" }] },
          });
        return route.fulfill({ json: repository });
      });
      await page.goto(`/repositories/${repository.id}?tab=artifacts`);
      const notice = page.getByRole("region", {
        name:
          locale === "zh-CN"
            ? "OCI 目录与关联制品"
            : "OCI catalog and attached artifacts",
      });
      await expect(notice).toBeVisible();
      await expect(notice).toContainText(
        locale === "zh-CN"
          ? "缓存列表不代表完整库存"
          : "cache below is not a complete inventory",
      );
      await expect(notice).toContainText(
        locale === "zh-CN"
          ? "尚未验证签名者身份与信任策略"
          : "does not verify signer identity or trust policy",
      );
      await expect(notice).toContainText(
        locale === "zh-CN" ? "不使用标签回退" : "tag fallback is unavailable",
      );
      await expect(page.getByText("team/image", { exact: true })).toBeVisible();
      await page.evaluate(() => document.fonts.ready);
      const geometry = await notice.evaluate((section) => {
        const bounds = section.getBoundingClientRect();
        const toolbar = section.parentElement
          ?.querySelector(".ag-artifact-view-toolbar")
          ?.getBoundingClientRect();
        return {
          width: bounds.width,
          right: bounds.right,
          gap: (toolbar?.top ?? 0) - bounds.bottom,
          overflow: document.documentElement.scrollWidth - innerWidth,
          textOverflow: Math.max(
            ...[...section.querySelectorAll("p")].map(
              (paragraph) => paragraph.scrollWidth - paragraph.clientWidth,
            ),
          ),
        };
      });
      expect(geometry.width).toBeGreaterThan(240);
      expect(geometry.right).toBeLessThanOrEqual(width - 14);
      expect(geometry.textOverflow).toBeLessThanOrEqual(1);
      expect(geometry.gap).toBeGreaterThanOrEqual(14);
      expect(geometry.gap).toBeLessThanOrEqual(18);
      expect(geometry.overflow).toBeLessThanOrEqual(1);
      expect(errors).toEqual([]);
      await page.screenshot({
        path: info.outputPath(`oci-discovery-${locale}-${width}.png`),
        fullPage: true,
      });
    });
  }
}
