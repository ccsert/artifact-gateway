import { expect, test, type Page } from "@playwright/test";
import type { PyPiYankState, PyPiYankUpdate } from "../src/client";
import { defaultSiteSettings } from "../src/lib/siteSettings";
import { authenticateAsMember } from "./support/auth";
import { waitForModalOpen } from "./support/motion";

const repositoryId = "41111111-1111-4111-8111-111111111111";
const project = "browser-widget";
const repositoryName = "python-browser";
const now = "2026-10-19T00:00:00Z";
const files = [
  "browser_widget-1.0-py3-none-any.whl",
  "browser_widget-1.0.tar.gz",
].map((filename, i) => ({
  filename,
  yanked: false,
  reason: "",
  digest: `sha256:${String(i + 1).repeat(64)}`,
  size: 1024,
}));

async function mockPyPI(page: Page, write = true) {
  await authenticateAsMember(page, "user:python-writer");
  let state: PyPiYankState = {
    repositoryId,
    project,
    version: "1.0",
    revision: "a".repeat(64),
    files: structuredClone(files),
  };
  const writes: PyPiYankUpdate[] = [];
  const failures: string[] = [];
  page.on("pageerror", (error) => failures.push(error.message));
  page.on("console", (message) => {
    if (message.type() === "error") failures.push(message.text());
  });
  const repo = {
    id: repositoryId,
    name: repositoryName,
    format: "pypi",
    type: "hosted",
    anonymousRead: false,
    state: "active",
    version: "1",
    mavenStrictPublication: false,
  };
  await page.route("**/api/v2/**", async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    const path = url.pathname;
    if (path.endsWith("/identity")) return route.fallback();
    if (path.endsWith("/site-settings"))
      return route.fulfill({ json: defaultSiteSettings });
    if (path.endsWith("/formats"))
      return route.fulfill({
        json: {
          items: [
            {
              format: "pypi",
              repositoryTypes: ["hosted", "proxy"],
              groupSupported: true,
              anonymousRead: true,
              publicationScanning: true,
              hostedOperations: ["read", "publish", "browse", "yank"],
              proxyOperations: ["read", "browse"],
            },
          ],
        },
      });
    if (path.endsWith("/yank")) {
      if (request.method() === "PATCH") {
        expect(write).toBe(true);
        expect(request.headers()["if-match"]).toBe(`"${state.revision}"`);
        const update = request.postDataJSON() as PyPiYankUpdate;
        writes.push(update);
        state = {
          ...state,
          revision: String(writes.length + 1).repeat(64),
          files: state.files.map((file) =>
            !update.filename || file.filename === update.filename
              ? {
                  ...file,
                  yanked: update.yanked,
                  reason: update.yanked ? (update.reason ?? "") : "",
                }
              : file,
          ),
        };
      }
      return route.fulfill({
        json: state,
        headers: { ETag: `"${state.revision}"` },
      });
    }
    if (path.endsWith("/effective-access")) {
      const allow = {
        allowed: true,
        source: "repository_grants",
        reason: "scope_granted",
      };
      const deny = {
        allowed: false,
        source: "repository_grants",
        reason: "scope_not_granted",
      };
      return route.fulfill({
        json: {
          actor: "user:python-writer",
          identity: {
            actor: "user:python-writer",
            kind: "local_session",
            role: "member",
            administrator: false,
          },
          repository: repo,
          permissions: {
            read: allow,
            write: write ? allow : deny,
            admin: deny,
            intelligence: deny,
          },
          anonymousRead: deny,
        },
      });
    }
    if (path.endsWith("/capabilities"))
      return route.fulfill({
        json: {
          format: "pypi",
          type: "hosted",
          operations: ["read", "publish", "browse", "yank"],
          artifactScanning: false,
          publicationScanning: false,
        },
      });
    if (path.endsWith("/capacity"))
      return route.fulfill({
        json: {
          repositoryId,
          format: "pypi",
          usedBytes: 2048,
          objectCount: 2,
          quotaBytes: 0,
        },
      });
    if (path.endsWith("/artifact-search"))
      return route.fulfill({
        json: {
          items: [
            {
              coordinate: project,
              digest: files[0].digest,
              size: 2048,
              version: "1.0",
              versionCount: 1,
              publisher: "python-fixture",
              createdAt: now,
            },
          ],
        },
      });
    const coordinate = `${project}@1.0`;
    const digest = url.searchParams.get("digest") ?? files[0].digest;
    if (path.endsWith("/artifact-quarantine"))
      return route.fulfill({
        json: {
          repositoryId,
          format: "pypi",
          coordinate,
          digest,
          state: "released",
          reason: "fixture release",
          version: "1",
          updatedBy: "python-fixture",
          updatedAt: now,
        },
      });
    if (path.endsWith("/artifact-intelligence"))
      return route.fulfill({
        json: {
          repositoryId,
          format: "pypi",
          coordinate,
          digest,
          signatures: [],
          sboms: [],
          licenses: [],
          version: "1",
          createdAt: now,
          updatedAt: now,
          updatedBy: "python-fixture",
        },
      });
    if (path.endsWith("/artifact-scans"))
      return route.fulfill({ json: { coordinate, digest, state: "never" } });
    if (path.endsWith("/repositories"))
      return route.fulfill({ json: { items: [repo] } });
    if (path.endsWith(`/${repositoryId}`)) return route.fulfill({ json: repo });
    return route.fulfill({ json: { items: [] } });
  });
  await page.route(`**/pypi/${repositoryName}/simple/${project}/`, (route) =>
    route.fulfill({
      json: {
        meta: { "api-version": "1.0" },
        name: project,
        files: state.files.map((file) => ({
          filename: file.filename,
          url: `../../packages/${file.filename}`,
          hashes: { sha256: file.digest.slice(7) },
          yanked: file.yanked ? file.reason || true : false,
          "_artifact-gateway": {
            version: "1.0",
            size: file.size,
            publisher: "python-fixture",
            cached: true,
            "created-at": now,
            "file-type": file.filename.endsWith(".whl")
              ? "bdist_wheel"
              : "sdist",
          },
        })),
      },
    }),
  );
  return { writes, failures };
}

for (const width of [1440, 390])
  for (const theme of ["dark", "light"]) {
    test(`a repository writer manages PyPI file states at ${width}px in ${theme}`, async ({
      page,
    }, testInfo) => {
      await page.setViewportSize({ width, height: 1000 });
      await page.emulateMedia({ reducedMotion: "reduce" });
      await page.addInitScript(
        (value) => localStorage.setItem("ag.console.theme", value),
        theme,
      );
      const { writes, failures } = await mockPyPI(page);
      await page.goto(
        `/repositories/${repositoryId}?artifact=${project}&version=1.0`,
      );
      const panel = page.getByRole("region", { name: "撤回状态" });
      await expect(panel.getByText("已撤回 0 / 2 个文件")).toBeVisible();
      await panel.getByRole("button", { name: "管理撤回" }).click();
      const dialog = page.getByRole("dialog");
      await waitForModalOpen(dialog);
      await page
        .getByLabel("撤回原因（可选）")
        .fill("ABI regression <text> & " + "details ".repeat(30));
      const scope = page.getByRole("combobox", {
        name: "操作范围",
        exact: true,
      });
      await scope.fill(files[0].filename);
      await scope.press("ArrowDown");
      await scope.press("Enter");
      await page.getByRole("button", { name: "确认撤回" }).click();
      await expect(dialog).toBeHidden();
      await expect(panel.getByText("已撤回 1 / 2 个文件")).toBeVisible();
      await expect(
        page.locator(".ag-pypi-detail").getByText("已撤回", { exact: true }),
      ).toBeVisible();
      await expect(
        page
          .locator(".ag-pypi-detail")
          .getByText("可正常选择", { exact: true }),
      ).toBeVisible();
      expect(writes[0].filename).toBe(files[0].filename);
      const geometry = await panel.evaluate((element) => {
        const current = element.getBoundingClientRect();
        const next = element.nextElementSibling?.getBoundingClientRect();
        return {
          left: current.left,
          right: current.right,
          gap: next ? next.top - current.bottom : 0,
          overflow:
            document.documentElement.scrollWidth -
            document.documentElement.clientWidth,
        };
      });
      expect(geometry.left).toBeGreaterThanOrEqual(0);
      expect(geometry.right).toBeLessThanOrEqual(width);
      expect(geometry.gap).toBeGreaterThanOrEqual(16);
      expect(geometry.gap).toBeLessThanOrEqual(18);
      expect(geometry.overflow).toBeLessThanOrEqual(0);
      await page.screenshot({
        path: testInfo.outputPath(`pypi-yank-${theme}-${width}.png`),
        fullPage: true,
      });
      await panel.getByRole("button", { name: "管理撤回" }).click();
      const action = page.getByRole("combobox", { name: "操作", exact: true });
      await action.click();
      await action.press("ArrowDown");
      await action.press("Enter");
      await page.getByRole("button", { name: "确认恢复" }).click();
      await expect(panel.getByText("已撤回 0 / 2 个文件")).toBeVisible();
      expect(writes[1]).toEqual({ yanked: false });
      expect(failures).toEqual([]);
    });
  }

test("a PyPI reader sees file state without mutation controls", async ({
  page,
}) => {
  const { writes, failures } = await mockPyPI(page, false);
  await page.goto(
    `/repositories/${repositoryId}?artifact=${project}&version=1.0`,
  );
  await expect(
    page.locator(".ag-pypi-detail").getByText("可正常选择"),
  ).toHaveCount(2);
  await expect(page.getByRole("button", { name: "管理撤回" })).toHaveCount(0);
  expect(writes).toEqual([]);
  expect(failures).toEqual([]);
});
