import { createHash } from "node:crypto";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { expect, test } from "@playwright/test";
import { defaultSiteSettings } from "../src/lib/siteSettings";
import { authenticateAsAdmin } from "./support/auth";

// Public synthetic Debian ar archive: USTAR control/data members, gzip mtime 0.
// This fixture exercises real browser hashing and the generated SDK's raw File
// serializer. API responses here are simulated, not a real signer/PG assertion.
const fixturePath = fileURLToPath(
  new URL("./fixtures/apt-first-widget.deb", import.meta.url),
);
const original = readFileSync(fixturePath);
const digest = `sha256:${createHash("sha256").update(original).digest("hex")}`;
const repositoryId = "apt-first-publication";
const repo = {
  id: repositoryId,
  name: "debian-first",
  format: "apt",
  type: "hosted",
  state: "active",
  version: "1",
  anonymousRead: false,
  mavenStrictPublication: false,
};
const session = {
  id: "11111111-1111-4111-8111-111111111111",
  repositoryId,
  suite: "stable",
  component: "main",
  publisher: "mock-admin",
  objectName: "apt-first-widget.deb",
  declaredDigest: digest,
  declaredSize: original.length,
  state: "open",
  expiresAt: "2099-01-01T00:00:00Z",
  createdAt: "2026-10-22T00:00:00Z",
};
const revision = {
  id: "22222222-2222-4222-8222-222222222222",
  repositoryId,
  package: "widget",
  version: "1.2.3",
  architecture: "amd64",
  canonicalIdentity: "widget@1.2.3#amd64",
  digest,
  size: original.length,
  objectName: "native/apt/widget",
  publisher: "mock-admin",
  createdAt: session.createdAt,
};
const snapshot = {
  id: "33333333-3333-4333-8333-333333333333",
  repositoryId,
  suite: "stable",
  sequence: 1,
  state: "visible",
  releaseDigest: `sha256:${"a".repeat(64)}`,
  inReleaseDigest: `sha256:${"b".repeat(64)}`,
  keyFingerprint: "F".repeat(40),
  signerIdentity: "synthetic-signer",
  signatureAlgorithm: "openpgp",
  createdAt: session.createdAt,
  publishedAt: session.createdAt,
};

for (const [width, locale, theme, errorMode] of [
  [1440, "zh-CN", "dark", "503"],
  [390, "en-US", "light", "503"],
  [1440, "en-US", "light", "lost-response"],
  [390, "zh-CN", "dark", "lost-response"],
] as const) {
  test(`APT first publication ${width}px ${locale} ${errorMode}`, async ({
    page,
  }, testInfo) => {
    expect(digest).toBe(
      "sha256:b8e6a938dd5372b04f26751b8fea447fa2277c4e4cc2a964c87daff0dc6db63d",
    );
    const en = locale === "en-US";
    await page.setViewportSize({ width, height: 1000 });
    await page.emulateMedia({ reducedMotion: "reduce" });
    await authenticateAsAdmin(page);
    await page.addInitScript(
      ({ locale, theme }) => {
        localStorage.setItem("ag.console.locale", locale);
        localStorage.setItem("ag.console.theme", theme);
      },
      { locale, theme },
    );
    await page.route("**/api/v2/site-settings", (route) =>
      route.fulfill({ json: defaultSiteSettings }),
    );
    await page.route("**/api/v2/repositories?**", (route) =>
      route.fulfill({ json: { items: [repo] } }),
    );
    const unexpected: string[] = [];
    const expectedHTTPFailures: string[] = [];
    page.on("pageerror", (error) => unexpected.push(error.message));
    page.on("console", (message) => {
      if (message.type() !== "error") return;
      if (
        message
          .location()
          .url.endsWith(`/repositories/${repositoryId}/apt/snapshots`) &&
        /503|ERR_CONNECTION_RESET|ERR_FAILED/.test(message.text())
      )
        expectedHTTPFailures.push(message.text());
      else unexpected.push(message.text());
    });
    const creates: { key: string | undefined; body: unknown }[] = [];
    const publications: { key: string | undefined; body: unknown }[] = [];
    let uploadCount = 0;
    let commitCount = 0;
    let committedKey: string | undefined;
    await page.route(
      `**/api/v2/repositories/${repositoryId}**`,
      async (route) => {
        const request = route.request();
        const path = new URL(request.url()).pathname;
        if (path.endsWith("/apt/lifecycle"))
          return route.fulfill({
            json: {
              snapshots: commitCount ? [{ snapshot }] : [],
              packages: commitCount
                ? [
                    {
                      publicationSessionId: session.id,
                      component: "main",
                      poolPath: "pool/main/w/widget/widget_1.2.3_amd64.deb",
                      revision,
                    },
                  ]
                : [],
              deletions: [],
            },
          });
        if (path.endsWith("/apt/publication-sessions")) {
          const body = request.postDataJSON();
          creates.push({ key: request.headers()["idempotency-key"], body });
          expect(body).toEqual({
            suite: "stable",
            component: "main",
            objectName: session.objectName,
            declaredDigest: digest,
            declaredSize: original.length,
          });
          return route.fulfill({ status: 201, json: session });
        }
        if (path.endsWith(`/apt/publication-sessions/${session.id}/package`)) {
          expect(request.method()).toBe("PUT");
          expect(request.headers()["content-type"]).toBe(
            "application/vnd.debian.binary-package",
          );
          expect(request.postDataBuffer()).toEqual(original);
          uploadCount++;
          return route.fulfill({ json: revision });
        }
        if (path.endsWith("/apt/snapshots")) {
          const body = request.postDataJSON();
          const key = request.headers()["idempotency-key"];
          publications.push({ key, body });
          expect(body).toEqual({
            suite: "stable",
            sequence: 1,
            publicationSessionIds: [session.id],
          });
          expect(key).toBeTruthy();
          if (publications.length === 1 && errorMode === "503")
            return route.fulfill({
              status: 503,
              json: {
                type: "about:blank",
                title: "Signer unavailable",
                status: 503,
                code: "signer_unavailable",
                message: "Synthetic signer unavailable",
                requestId: "apt-first-fixture",
              },
            });
          if (!committedKey) {
            commitCount++;
            committedKey = key;
          }
          expect(key).toBe(committedKey);
          if (publications.length === 1) return route.abort("connectionreset");
          return route.fulfill({ status: 201, json: snapshot });
        }
        if (path.endsWith("/effective-access")) {
          const allowed = {
            allowed: true,
            source: "administrator",
            reason: "administrator",
          };
          return route.fulfill({
            json: {
              actor: "mock-admin",
              permissions: {
                admin: allowed,
                read: allowed,
                write: allowed,
                intelligence: allowed,
              },
            },
          });
        }
        if (path.endsWith("/capabilities"))
          return route.fulfill({
            json: {
              format: "apt",
              type: "hosted",
              operations: [],
              artifactScanning: false,
              publicationScanning: false,
            },
          });
        if (path.endsWith("/capacity"))
          return route.fulfill({
            json: {
              repositoryId,
              format: "apt",
              usedBytes: 0,
              objectCount: 0,
              quotaBytes: 0,
            },
          });
        return route.fulfill({ json: repo });
      },
    );
    await page.goto(`/repositories/${repositoryId}?tab=apt-snapshots`);
    const surface = page.locator(".ag-apt-first-publication");
    const stage = surface.getByRole("button", {
      name: en ? "Stage and review package" : "暂存并核对软件包",
      exact: true,
    });
    await expect(stage).toBeDisabled();
    await surface.locator('input[type="file"]').setInputFiles(fixturePath);
    await expect(surface.getByText(digest, { exact: true })).toBeVisible();
    expect(creates).toEqual([]);
    expect(uploadCount).toBe(0);
    await stage.click();
    await expect(
      surface.getByText(revision.canonicalIdentity, { exact: true }),
    ).toBeVisible();
    await expect(
      surface.getByRole("heading", {
        name: en
          ? "Staged: review the server-identified package"
          : "已暂存：核对服务端识别的身份",
      }),
    ).toBeFocused();
    await page.keyboard.press("Tab");
    await expect(surface.getByRole("checkbox")).toBeFocused();
    expect(creates).toHaveLength(1);
    expect(uploadCount).toBe(1);
    expect(publications).toEqual([]);
    const publish = surface.getByRole("button", {
      name: en ? "Confirm and sign first snapshot" : "确认并签署首次快照",
      exact: true,
    });
    await expect(publish).toBeDisabled();
    const geometry = await surface.evaluate((element) => {
      const boxes = Array.from(element.children).map((child) =>
        child.getBoundingClientRect(),
      );
      return {
        gaps: boxes.slice(1).map((box, i) => box.top - boxes[i].bottom),
        left: element.getBoundingClientRect().left,
        right: element.getBoundingClientRect().right,
        overflow: document.documentElement.scrollWidth > innerWidth,
      };
    });
    expect(geometry.gaps.every((gap) => gap >= 15 && gap <= 18)).toBe(true);
    expect(geometry.left).toBeGreaterThanOrEqual(0);
    expect(geometry.right).toBeLessThanOrEqual(width);
    expect(geometry.overflow).toBe(false);
    const fields = await surface
      .locator(".ag-apt-first-inputs")
      .evaluate((element) => {
        const [file, component] = Array.from(element.children).map((child) =>
          child.getBoundingClientRect(),
        );
        return {
          horizontalGap: component.left - file.right,
          verticalGap: component.top - file.bottom,
        };
      });
    expect(
      width > 600 ? fields.horizontalGap : fields.verticalGap,
    ).toBeGreaterThanOrEqual(15);
    expect(
      width > 600 ? fields.horizontalGap : fields.verticalGap,
    ).toBeLessThanOrEqual(18);
    await page.screenshot({
      path: testInfo.outputPath(`first-staged-${width}-${locale}.png`),
      fullPage: true,
      animations: "disabled",
    });
    await surface
      .getByRole("checkbox", {
        name: en
          ? "I checked the package name, version and architecture"
          : "已核对包名、版本和架构",
      })
      .check();
    await publish.click();
    const retry = surface.getByRole("button", {
      name: en ? "Retry first publication" : "重试首次发布",
      exact: true,
    });
    await expect(retry).toBeEnabled();
    await expect(
      surface.getByText(revision.canonicalIdentity, { exact: true }),
    ).toBeVisible();
    await expect(surface.getByRole("checkbox")).toBeChecked();
    await expect(
      surface.getByText(session.objectName, { exact: true }),
    ).toBeVisible();
    await retry.click();
    await expect(
      page.getByText(en ? "Current snapshot #1" : "当前快照 #1", {
        exact: true,
      }),
    ).toBeVisible();
    await expect(surface).toHaveCount(0);
    await expect(
      page.getByText(snapshot.keyFingerprint, { exact: true }),
    ).toBeVisible();
    expect(publications).toHaveLength(2);
    expect(publications[1]).toEqual(publications[0]);
    expect(commitCount).toBe(1);
    expect(uploadCount).toBe(1);
    expect(expectedHTTPFailures).toHaveLength(1);
    expect(unexpected).toEqual([]);
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= innerWidth,
      ),
    ).toBe(true);
    expect(
      await page.evaluate(() =>
        Object.values(localStorage).some((value) =>
          /sha256:|session-first|apt-first-widget\.deb/.test(value),
        ),
      ),
    ).toBe(false);
    await testInfo.attach("first-publication-readback", {
      contentType: "application/json",
      body: JSON.stringify({
        fixtureSHA256: digest,
        fixtureBytes: original.length,
        creates,
        publications,
        uploadCount,
        commitCount,
        geometry,
        fields,
        expectedHTTPFailures,
        unexpected,
      }),
    });
    await page.screenshot({
      path: testInfo.outputPath(`first-visible-${width}-${locale}.png`),
      fullPage: true,
      animations: "disabled",
    });
  });
}
