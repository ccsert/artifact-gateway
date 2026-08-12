import { expect, test, type Page } from "@playwright/test";
import { authenticateAsAdmin } from "./support/auth";

const repositoryId = "repo-layout";

async function mockRepositoryDetail(
  page: Page,
  { scannerEnabled = false }: { scannerEnabled?: boolean } = {},
) {
  await authenticateAsAdmin(page);

  await page.route("**/api/v2/repositories?**", (route) =>
    route.fulfill({ json: { items: [] } }),
  );

  await page.route(`**/api/v2/repositories/${repositoryId}**`, (route) => {
    const request = route.request();
    const path = new URL(request.url()).pathname;

    if (path.endsWith("/artifact-scans") && request.method() === "POST") {
      const body = request.postDataJSON() as {
        coordinate: string;
        digest: string;
      };
      return route.fulfill({
        json: {
          id: "scan-job-layout",
          kind: "scan",
          state: "pending",
          createdAt: "2026-08-12T08:00:00Z",
          attempts: 0,
          maxAttempts: 3,
          progressCurrent: 0,
          progressTotal: 0,
          details: {
            format: "raw",
            coordinate: body.coordinate,
            digest: body.digest,
          },
        },
      });
    }
    if (request.method() === "PATCH") {
      return route.fulfill({
        json: {
          id: repositoryId,
          name: "release-files",
          format: "raw",
          type: "hosted",
          anonymousRead: true,
          state: "active",
          version: "2",
        },
      });
    }
    if (path.endsWith("/artifact-search")) {
      return route.fulfill({
        json: {
          items: Array.from({ length: 20 }, (_, index) => ({
            coordinate: `releases/example-${index + 1}.zip`,
            digest: `sha256:${String(index).padStart(64, "0")}`,
            size: 1024 * (index + 1),
            createdAt: "2026-08-08T08:00:00Z",
          })),
        },
      });
    }
    if (path.endsWith("/capabilities")) {
      return route.fulfill({
        json: {
          format: "raw",
          type: "hosted",
          operations: ["read", "publish", "browse", "delete"],
          artifactScanning: scannerEnabled,
          publicationScanning: scannerEnabled,
        },
      });
    }
    if (path.endsWith("/lifecycle-jobs")) {
      return route.fulfill({ json: [] });
    }
    if (path.endsWith("/security-policy")) {
      return route.fulfill({
        json: {
          version: "1",
          enabled: false,
          autoScanOnPublish: false,
          requireSignature: false,
          requireVerifiedSignature: false,
          requireSbom: false,
          requireProvenance: false,
          requireVulnerabilityScan: false,
          maxAllowedSeverity: "critical",
          failOnScanError: true,
          allowedLicenses: [],
        },
      });
    }
    if (path.endsWith("/quarantine-read-policy")) {
      return route.fulfill({ json: { version: "1", enabled: false } });
    }
    if (path.endsWith("/effective-access")) {
      const allowed = {
        allowed: true,
        source: "administrator",
        reason: "administrator",
      };
      return route.fulfill({
        json: {
          actor: "admin",
          identity: {
            actor: "mock-admin",
            kind: "local_session",
            role: "admin",
            administrator: true,
          },
          repository: {
            id: repositoryId,
            name: "release-files",
            format: "raw",
            type: "hosted",
            state: "active",
          },
          anonymousRead: {
            allowed: true,
            source: "anonymous_policy",
            reason: "repository_anonymous_read_enabled",
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
    if (path.endsWith("/capacity")) {
      return route.fulfill({
        json: {
          repositoryId,
          format: "raw",
          usedBytes: 1024 * 1024,
          objectCount: 20,
          quotaBytes: 0,
        },
      });
    }
    return route.fulfill({
      json: {
        id: repositoryId,
        name: "release-files",
        format: "raw",
        type: "hosted",
        anonymousRead: true,
        state: "active",
        version: "1",
      },
    });
  });
}

test("repository detail keeps operational content above the fold", async ({
  page,
}, testInfo) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await mockRepositoryDetail(page);

  await page.goto(`/repositories/${repositoryId}`);

  const summary = page.getByRole("group", { name: "仓库摘要" });
  await expect(summary).toBeVisible();
  await expect(summary).toContainText("1.0 MiB · 20 个对象");
  expect(
    (await summary.boundingBox())?.height ?? Number.POSITIVE_INFINITY,
  ).toBeLessThan(110);

  await page.getByRole("button", { name: "查看概念说明" }).click();
  await expect(page.getByText("概念说明", { exact: true })).toBeVisible();
  await expect(
    page.getByText("Hosted Repository", { exact: true }),
  ).toBeVisible();
  await page.keyboard.press("Escape");

  await expect(page.getByRole("tab", { name: "设置" })).toBeVisible();
  await expect(page.getByRole("button", { name: "设置" })).toHaveCount(0);

  const table = page.locator(".ag-console-table");
  await expect(table).toBeVisible();
  expect(
    (await table.boundingBox())?.y ?? Number.POSITIVE_INFINITY,
  ).toBeLessThan(400);

  if (process.env.CAPTURE_REPOSITORY_DETAIL) {
    await page.screenshot({
      path: testInfo.outputPath("repository-detail.png"),
      fullPage: true,
    });
  }
});

test("repository settings live in a tab and keep the update workflow", async ({
  page,
}, testInfo) => {
  await mockRepositoryDetail(page);
  const updateRequest = page.waitForRequest(
    (request) =>
      request.method() === "PATCH" &&
      new URL(request.url()).pathname.endsWith(`/repositories/${repositoryId}`),
  );

  await page.goto(`/repositories/${repositoryId}`);
  const settingsTab = page.getByRole("tab", { name: "设置" });
  await settingsTab.click();

  await expect(page.getByRole("heading", { name: "仓库设置" })).toBeVisible();
  await expect
    .poll(async () => {
      const [tabBox, indicatorBox] = await Promise.all([
        settingsTab.boundingBox(),
        page.locator(".ant-tabs-ink-bar").boundingBox(),
      ]);
      if (!tabBox || !indicatorBox) return Number.POSITIVE_INFINITY;
      const tabCenter = tabBox.x + tabBox.width / 2;
      const indicatorCenter = indicatorBox.x + indicatorBox.width / 2;
      return Math.abs(tabCenter - indicatorCenter);
    })
    .toBeLessThan(2);
  await expect(page.getByRole("switch")).toBeVisible();
  await expect(page.getByRole("switch")).toBeChecked();
  await page.getByRole("button", { name: "保存更改" }).click();

  await updateRequest;
  await expect(page.getByText("仓库设置已保存")).toBeVisible();

  if (process.env.CAPTURE_REPOSITORY_DETAIL) {
    await page.screenshot({
      path: testInfo.outputPath("repository-settings.png"),
      fullPage: true,
    });
  }
});

test("scanning uses a frameless responsive workspace", async ({
  page,
}, testInfo) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await mockRepositoryDetail(page);

  await page.goto(`/repositories/${repositoryId}?tab=scanning`);
  await expect(
    page.getByRole("heading", { name: "制品扫描", exact: true }),
  ).toBeVisible();

  await expect(page.locator(".ag-card .ag-card")).toHaveCount(0);

  const scannerWarning = page
    .getByRole("alert")
    .filter({ hasText: "当前仓库未配置可用扫描器" });
  const enforcementNotice = page
    .getByRole("alert")
    .filter({ hasText: "扫描与处置是两个步骤" });
  const [warningBox, noticeBox] = await Promise.all([
    scannerWarning.boundingBox(),
    enforcementNotice.boundingBox(),
  ]);
  expect(warningBox).not.toBeNull();
  expect(noticeBox).not.toBeNull();
  expect(Math.abs((warningBox?.y ?? 0) - (noticeBox?.y ?? 0))).toBeLessThan(2);
  expect(noticeBox?.x ?? 0).toBeGreaterThan(
    (warningBox?.x ?? 0) + (warningBox?.width ?? 0),
  );

  const artifactScanHeading = page.getByRole("heading", {
    name: "选择并扫描不可变制品",
    exact: true,
  });
  const artifactScanCard = page
    .locator(".ag-card")
    .filter({ has: artifactScanHeading });
  const artifactPicker = artifactScanCard.getByRole("combobox", {
    name: "搜索并选择制品",
  });
  const scanHint = artifactScanCard.getByText(
    "选择后会自动锁定规范坐标与完整摘要；最多显示 50 条，可输入前缀缩小范围。旧版本或 Conan 修订未列出时请使用高级手动输入。",
    { exact: true },
  );
  const submitScan = artifactScanCard.getByRole("button", { name: "提交扫描" });
  const [artifactCardBox, pickerBox, hintBox, submitBox] = await Promise.all([
    artifactScanCard.boundingBox(),
    artifactPicker.boundingBox(),
    scanHint.boundingBox(),
    submitScan.boundingBox(),
  ]);
  expect(artifactCardBox).not.toBeNull();
  expect(pickerBox).not.toBeNull();
  expect(hintBox).not.toBeNull();
  expect(submitBox).not.toBeNull();

  const cardLeft = artifactCardBox?.x ?? 0;
  const cardRight = cardLeft + (artifactCardBox?.width ?? 0);
  const cardBottom = (artifactCardBox?.y ?? 0) + (artifactCardBox?.height ?? 0);
  expect((pickerBox?.x ?? 0) - cardLeft).toBeGreaterThanOrEqual(20);
  expect(
    cardRight - ((pickerBox?.x ?? 0) + (pickerBox?.width ?? 0)),
  ).toBeGreaterThanOrEqual(20);
  expect((hintBox?.x ?? 0) - cardLeft).toBeGreaterThanOrEqual(20);
  expect(
    cardRight - ((submitBox?.x ?? 0) + (submitBox?.width ?? 0)),
  ).toBeGreaterThanOrEqual(20);
  expect(
    cardBottom - ((submitBox?.y ?? 0) + (submitBox?.height ?? 0)),
  ).toBeGreaterThanOrEqual(16);

  await artifactScanCard.getByRole("button", { name: "高级手动输入" }).click();
  await expect(
    artifactScanCard.getByRole("textbox", { name: "制品坐标" }),
  ).toBeVisible();
  await expect(
    artifactScanCard.getByRole("textbox", { name: "SHA-256 摘要" }),
  ).toBeVisible();

  const recentJobsHeading = page.getByRole("heading", {
    name: "最近扫描任务",
    exact: true,
  });
  const recentJobsCard = page
    .locator(".ag-card")
    .filter({ has: recentJobsHeading });
  const recentHeader = recentJobsHeading.locator("..");
  const recentEmpty = recentJobsCard.locator(".ant-empty");
  const [recentCardBox, recentHeaderBox, recentEmptyBox] = await Promise.all([
    recentJobsCard.boundingBox(),
    recentHeader.boundingBox(),
    recentEmpty.boundingBox(),
  ]);
  expect(recentCardBox).not.toBeNull();
  expect(recentHeaderBox).not.toBeNull();
  expect(recentEmptyBox).not.toBeNull();
  expect(
    (recentEmptyBox?.y ?? 0) -
      ((recentHeaderBox?.y ?? 0) + (recentHeaderBox?.height ?? 0)),
  ).toBeLessThanOrEqual(20);
  expect(
    (recentCardBox?.y ?? 0) +
      (recentCardBox?.height ?? 0) -
      ((recentEmptyBox?.y ?? 0) + (recentEmptyBox?.height ?? 0)),
  ).toBeLessThanOrEqual(20);

  if (process.env.CAPTURE_REPOSITORY_DETAIL) {
    await page.screenshot({
      path: testInfo.outputPath("repository-scanning.png"),
      fullPage: true,
    });
  }

  await page.setViewportSize({ width: 1024, height: 900 });
  const [narrowWarningBox, narrowNoticeBox] = await Promise.all([
    scannerWarning.boundingBox(),
    enforcementNotice.boundingBox(),
  ]);
  expect(
    Math.abs((narrowWarningBox?.x ?? 0) - (narrowNoticeBox?.x ?? 0)),
  ).toBeLessThan(2);
  expect(narrowNoticeBox?.y ?? 0).toBeGreaterThanOrEqual(
    (narrowWarningBox?.y ?? 0) + (narrowWarningBox?.height ?? 0) + 12,
  );
  expect(
    await page.evaluate(
      () => document.body.scrollWidth - document.body.clientWidth,
    ),
  ).toBe(0);
});

test("scanning selects a searchable immutable artifact before queuing", async ({
  page,
}) => {
  const coordinate = "releases/example-1.zip";
  const digest = `sha256:${"0".repeat(64)}`;
  await mockRepositoryDetail(page, { scannerEnabled: true });
  const scanRequest = page.waitForRequest(
    (request) =>
      request.method() === "POST" &&
      new URL(request.url()).pathname.endsWith(
        `/repositories/${repositoryId}/artifact-scans`,
      ),
  );

  await page.goto(`/repositories/${repositoryId}?tab=scanning`);
  const artifactScanCard = page
    .locator(".ag-card")
    .filter({ hasText: "选择并扫描不可变制品" });
  const picker = artifactScanCard.getByRole("combobox", {
    name: "搜索并选择制品",
  });
  await picker.click();
  const option = page
    .locator(".ant-select-item-option")
    .filter({ hasText: coordinate })
    .first();
  await expect(option).toBeVisible();
  await option.click();

  await expect(
    artifactScanCard.getByText(digest, { exact: true }),
  ).toBeVisible();
  await artifactScanCard.getByRole("button", { name: "提交扫描" }).click();

  expect((await scanRequest).postDataJSON()).toEqual({ coordinate, digest });
  await expect(page.getByText("扫描任务已提交")).toBeVisible();
});

test("security guardrails use independent desktop columns", async ({
  page,
}) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await mockRepositoryDetail(page);

  await page.goto(`/repositories/${repositoryId}?tab=security`);
  const readHeading = page.getByRole("heading", {
    name: "隔离制品读取",
    exact: true,
  });
  const admissionHeading = page.getByRole("heading", {
    name: "晋升准入",
    exact: true,
  });
  await expect(readHeading).toBeVisible();
  await expect(admissionHeading).toBeVisible();
  await expect(page.locator(".ag-card .ag-card")).toHaveCount(0);

  const readCard = page.locator(".ag-card").filter({ has: readHeading });
  const admissionCard = page
    .locator(".ag-card")
    .filter({ has: admissionHeading });
  const [readBox, admissionBox] = await Promise.all([
    readCard.boundingBox(),
    admissionCard.boundingBox(),
  ]);
  expect(readBox).not.toBeNull();
  expect(admissionBox).not.toBeNull();
  expect(Math.abs((readBox?.y ?? 0) - (admissionBox?.y ?? 0))).toBeLessThan(2);
  expect(admissionBox?.x ?? 0).toBeGreaterThan(
    (readBox?.x ?? 0) + (readBox?.width ?? 0),
  );

  await page.setViewportSize({ width: 1024, height: 900 });
  const [narrowReadBox, narrowAdmissionBox] = await Promise.all([
    readCard.boundingBox(),
    admissionCard.boundingBox(),
  ]);
  expect(
    Math.abs((narrowReadBox?.x ?? 0) - (narrowAdmissionBox?.x ?? 0)),
  ).toBeLessThan(2);
  expect(narrowAdmissionBox?.y ?? 0).toBeGreaterThan(
    (narrowReadBox?.y ?? 0) + (narrowReadBox?.height ?? 0),
  );
  expect(
    await page.evaluate(
      () => document.body.scrollWidth - document.body.clientWidth,
    ),
  ).toBe(0);
});
