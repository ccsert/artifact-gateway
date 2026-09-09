import { expect, test } from "@playwright/test";
import { authenticateAsAdmin } from "./support/auth";

const repositoryId = "apt-console";
const repo = {
  id: repositoryId,
  name: "debian-staging",
  format: "apt",
  type: "hosted",
  state: "active",
  version: "1",
  anonymousRead: false,
  mavenStrictPublication: false,
};
const snapshot = {
  id: "11111111-1111-4111-8111-111111111111",
  repositoryId,
  suite: "stable",
  sequence: 4,
  state: "visible",
  releaseDigest: `sha256:${"a".repeat(64)}`,
  inReleaseDigest: `sha256:${"b".repeat(64)}`,
  keyFingerprint: "F".repeat(40),
  signerIdentity: "debian-staging-reference-signer",
  signatureAlgorithm: "openpgp",
  createdAt: "2026-09-08T10:00:00Z",
  publishedAt: "2026-09-08T10:00:00Z",
};
const packages = ["artifact-gateway-runtime", "artifact-gateway-console"].map(
  (name, index) => ({
    publicationSessionId: `session-${index}`,
    component: "main",
    poolPath: `pool/main/a/${name}/${name}_1.2.3_amd64.deb`,
    revision: {
      id: `revision-${index}`,
      repositoryId,
      package: name,
      version: "1.2.3",
      architecture: "amd64",
      canonicalIdentity: `${name}@1.2.3#amd64`,
      digest: `sha256:${String(index).repeat(64)}`,
      size: 4096,
      objectName: `native/apt/${index}`,
      publisher: "operator",
      createdAt: "2026-08-01T00:00:00Z",
    },
  }),
);

for (const [width, theme, locale] of [
  [1440, "dark", "zh-CN"],
  [390, "light", "en-US"],
] as const) {
  test(`APT preview, apply and distribution at ${width}px ${theme}`, async ({
    page,
  }, testInfo) => {
    await page.setViewportSize({ width, height: 1000 });
    await page.emulateMedia({ reducedMotion: "reduce" });
    await authenticateAsAdmin(page);
    await page.addInitScript(
      ({ theme, locale }) => {
        localStorage.setItem("ag.console.theme", theme);
        localStorage.setItem("ag.console.locale", locale);
      },
      { theme, locale },
    );
    const errors: string[] = [];
    page.on("pageerror", (error) => errors.push(error.message));
    page.on("console", (message) => {
      if (message.type() === "error") errors.push(message.text());
    });
    const applied: unknown[] = [];
    const replications: unknown[] = [];
    let sequence = 4;
    let visiblePackages = packages;
    let currentSnapshotId = snapshot.id;
    await page.route("**/api/v2/repositories?**", (route) =>
      route.fulfill({
        json: {
          items: [
            repo,
            { ...repo, id: "apt-target", name: "debian-production" },
          ],
        },
      }),
    );
    await page.route("**/api/v2/repositories/apt-console**", (route) => {
      const req = route.request();
      const path = new URL(req.url()).pathname;
      if (path.endsWith("/apt/lifecycle/preview"))
        return route.fulfill({
          json: {
            expectedSnapshotId: snapshot.id,
            removeSessionIds: ["session-0"],
            restoreIds: [],
            remainingPackages: 1,
            recoveryDays: 7,
          },
        });
      if (path.endsWith("/apt/lifecycle")) {
        if (req.method() === "POST") {
          applied.push({
            body: req.postDataJSON(),
            key: req.headers()["idempotency-key"],
          });
          sequence++;
          visiblePackages = packages.slice(1);
          currentSnapshotId = "22222222-2222-4222-8222-222222222222";
          return route.fulfill({
            json: { ...snapshot, id: currentSnapshotId, sequence },
          });
        }
        return route.fulfill({
          json: {
            snapshots: [
              { snapshot: { ...snapshot, id: currentSnapshotId, sequence } },
            ],
            packages: visiblePackages,
            deletions: [],
          },
        });
      }
      if (path.endsWith("/replications")) {
        if (req.method() === "POST") {
          replications.push(req.postDataJSON());
          return route.fulfill({ status: 202, json: { id: "plan-new" } });
        }
        return route.fulfill({ json: [] });
      }
      if (path.endsWith("/capabilities"))
        return route.fulfill({
          json: {
            format: "apt",
            type: "hosted",
            operations: [],
            artifactScanning: true,
            publicationScanning: true,
          },
        });
      if (path.endsWith("/capacity"))
        return route.fulfill({
          json: {
            repositoryId,
            format: "apt",
            usedBytes: 8192,
            objectCount: 2,
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
            actor: "operator",
            permissions: {
              admin: allowed,
              read: allowed,
              write: allowed,
              intelligence: allowed,
            },
          },
        });
      }
      return route.fulfill({ json: repo });
    });
    const en = locale === "en-US";
    await page.goto(`/repositories/${repositoryId}?tab=apt-snapshots`);
    await expect(
      page.getByText(en ? "Current snapshot #4" : "当前快照 #4", {
        exact: true,
      }),
    ).toBeVisible();
    const surface = page.locator(".ag-apt-operations");
    const gaps = await surface.evaluate((el) => {
      const rects = Array.from(el.children).map((child) =>
        child.getBoundingClientRect(),
      );
      return rects.slice(1).map((r, i) => r.top - rects[i].bottom);
    });
    expect(gaps.every((gap) => gap >= 15 && gap <= 18)).toBe(true);
    const noOverflow = async () =>
      expect(
        await page.evaluate(
          () => document.documentElement.scrollWidth <= innerWidth,
        ),
      ).toBe(true);
    await noOverflow();
    await page.evaluate(() => window.scrollTo(0, 0));
    await page.screenshot({
      animations: "disabled",
      path: testInfo.outputPath(`apt-${width}.png`),
      fullPage: true,
    });
    await page
      .getByRole("button", {
        name: en ? "Preview retention" : "预览保留清理",
        exact: true,
      })
      .click();
    const dialog = page.getByRole("dialog");
    await expect(dialog.getByText(packages[0].poolPath)).toBeVisible();
    await expect(dialog.getByText(packages[1].poolPath)).toHaveCount(0);
    await noOverflow();
    await expect
      .poll(() => dialog.evaluate((el) => getComputedStyle(el).opacity))
      .toBe("1");
    await page.evaluate(() => window.scrollTo(0, 0));
    await page.screenshot({
      animations: "disabled",
      path: testInfo.outputPath(`apt-preview-${width}.png`),
      fullPage: false,
    });
    const applyButton = dialog.getByRole("button", {
      name: en ? "Apply and sign new snapshot" : "应用并签署新快照",
      exact: true,
    });
    for (const interaction of ["rest", "hover", "focus"] as const) {
      if (interaction === "hover") await applyButton.hover();
      if (interaction === "focus") {
        await page.mouse.move(0, 0);
        await applyButton.focus();
      }
      await expect
        .poll(() =>
          applyButton.evaluate((el) => {
            const color = getComputedStyle(el).color;
            let background = "";
            for (
              let node: Element | null = el;
              node;
              node = node.parentElement
            ) {
              const candidate = getComputedStyle(node).backgroundColor;
              if (
                candidate !== "rgba(0, 0, 0, 0)" &&
                candidate !== "transparent"
              ) {
                background = candidate;
                break;
              }
            }
            const luminance = (value: string) => {
              const rgb = value
                .match(/[\d.]+/g)!
                .slice(0, 3)
                .map(Number)
                .map((v) => {
                  const c = v / 255;
                  return c <= 0.04045
                    ? c / 12.92
                    : ((c + 0.055) / 1.055) ** 2.4;
                });
              return 0.2126 * rgb[0] + 0.7152 * rgb[1] + 0.0722 * rgb[2];
            };
            const [a, b] = [luminance(color), luminance(background)].sort(
              (x, y) => y - x,
            );
            return (a + 0.05) / (b + 0.05);
          }),
        )
        .toBeGreaterThanOrEqual(4.5);
      if (width === 1440)
        await page.screenshot({
          path: testInfo.outputPath(`apt-button-${interaction}.png`),
          animations: "disabled",
        });
    }
    await dialog
      .getByRole("button", {
        name: en ? "Apply and sign new snapshot" : "应用并签署新快照",
        exact: true,
      })
      .click();
    await expect(
      page.getByText(
        en ? "Published stable snapshot #5" : "已发布 stable 快照 #5",
        { exact: true },
      ),
    ).toBeVisible();
    expect(applied).toEqual([
      {
        body: {
          suite: "stable",
          action: "retention",
          expectedSnapshotId: snapshot.id,
          keepLatest: 1,
          olderThanDays: 30,
          publicationSessionIds: ["session-0"],
        },
        key: expect.any(String),
      },
    ]);
    await page
      .getByRole("tab", {
        name: en ? "Promote / replicate" : "晋升 / 复制",
        exact: true,
      })
      .click();
    await page
      .getByRole("combobox", {
        name: en ? "Search and select a source artifact" : "搜索并选择源制品",
      })
      .click();
    await page
      .getByText("artifact-gateway-console 1.2.3 · amd64", { exact: true })
      .click();
    await page
      .getByRole("combobox", {
        name: en ? "Select target repository" : "选择目标仓库",
      })
      .click();
    await page.getByText("debian-production", { exact: true }).click();
    await page
      .getByRole("textbox", {
        name: en ? "Target suite" : "目标发行套件",
        exact: true,
      })
      .fill("bookworm");
    await expect(page.locator(".ant-select-dropdown:visible")).toHaveCount(0);
    await noOverflow();
    await page.evaluate(() => window.scrollTo(0, 0));
    await page.screenshot({
      animations: "disabled",
      path: testInfo.outputPath(`apt-distribute-${width}.png`),
      fullPage: true,
    });
    await page
      .locator(".ag-distribution")
      .getByRole("button", { name: en ? "Replicate" : /复\s*制/, exact: true })
      .click();
    await expect(
      page.getByText(
        en
          ? "Replication plan created. Track its progress below."
          : "复制计划已创建，下方查看进度",
        { exact: true },
      ),
    ).toBeVisible();
    expect(replications).toEqual([
      {
        targetRepositoryId: "apt-target",
        coordinate: packages[1].poolPath,
        digest: packages[1].revision.digest,
        aptTargetSuite: "bookworm",
      },
    ]);
    expect(errors).toEqual([]);
  });
}
