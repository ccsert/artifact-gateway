import { execFileSync } from "node:child_process";
import { createHash, randomUUID } from "node:crypto";
import { readFileSync, writeFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import {
  chromium,
  expect,
  test,
  type APIRequestContext,
  type Locator,
  type Page,
} from "@playwright/test";
import type {
  MigrationSource,
  NexusMigrationBatch,
  NexusMigrationEntry,
  NexusMigrationCapabilities,
  Repository,
} from "../src/client";

type Runtime = {
  project: string;
  apiBaseURL: string;
  adminToken: string;
  resolverToken: string;
  source: MigrationSource;
  repositories: { maven: Repository; raw: Repository };
  capabilities: NexusMigrationCapabilities;
  composeFile: string;
  stateFile: string;
  fixtureFiles: string;
};
const configPath = process.env.MIGRATION_BATCH_FIXTURE_CONFIG;
const runtime: Runtime | undefined = configPath
  ? (JSON.parse(readFileSync(configPath, "utf8")) as Runtime)
  : undefined;
test.skip(
  !runtime,
  "Requires an explicitly owned live PG/S3/TLS Nexus batch fixture",
);
const compose = (args: string[]) => {
  if (!runtime || runtime.project !== "ag-383-batches")
    throw new Error("refuse_unowned_runtime");
  return execFileSync(
    "docker",
    [
      "compose",
      "-p",
      runtime.project,
      "-f",
      fileURLToPath(new URL("../../compose.integration.yml", import.meta.url)),
      "-f",
      runtime.composeFile,
      ...args,
    ],
    { encoding: "utf8", timeout: 60_000 },
  );
};
const phase = (value: number, options: Record<string, unknown> = {}) => {
  if (!runtime || runtime.project !== "ag-383-batches")
    throw new Error("refuse_unowned_fixture");
  writeFileSync(
    runtime.stateFile,
    JSON.stringify({
      phase: value,
      delayMs: 0,
      chunkDelayMs: 0,
      failMetadata: false,
      offline: false,
      ...options,
    }),
  );
};
async function authenticate(page: Page) {
  await page.addInitScript((token) => {
    localStorage.setItem("ag.console.token", token);
    localStorage.setItem("ag.console.role", "admin");
    localStorage.setItem("ag.console.locale", "zh-CN");
  }, runtime!.adminToken);
}
async function prepareTargets(request: APIRequestContext) {
  const suffix = Date.now();
  for (const format of ["maven", "raw"] as const) {
    const response = await request.post("/api/v2/repositories", {
      headers: {
        Authorization: `Bearer ${runtime!.adminToken}`,
        "Idempotency-Key": randomUUID(),
      },
      data: {
        name: `ag383-browser-${suffix}-${format}`,
        format,
        type: "hosted",
        anonymousRead: false,
        mavenStrictPublication: format === "maven",
      },
    });
    expect(response.ok()).toBeTruthy();
    runtime!.repositories[format] = (await response.json()) as Repository;
  }
}
async function fillCapacity(container: Page | Locator) {
  // In this owned fixture /data is the actual RustFS pool. The product never
  // performs this measurement or fills an operator's evidence on their behalf.
  const disk = compose(["exec", "-T", "rustfs", "df", "-Pk", "/data"]);
  const freeBytes =
    Number(disk.trim().split("\n").at(-1)!.trim().split(/\s+/)[3]) * 1024;
  const inventory: unknown = JSON.parse(
    readFileSync(runtime!.fixtureFiles, "utf8"),
  );
  const sizes = (value: unknown): number[] =>
    typeof value === "string"
      ? [Buffer.from(value, "base64").length]
      : value && typeof value === "object"
        ? Object.values(value).flatMap(sizes)
        : [];
  const downloadPeak = Math.max(...sizes(inventory));
  expect(Number.isSafeInteger(freeBytes) && freeBytes > 0).toBeTruthy();
  for (const [name, value] of [
    ["存储池可用字节", String(freeBytes)],
    ["下载峰值字节", String(downloadPeak)],
    ["上传峰值字节", "0"],
    ["备份峰值字节", "0"],
    ["恢复峰值字节", "0"],
    ["保留余量字节", "1048576"],
  ])
    await container.getByRole("textbox", { name, exact: true }).fill(value);
  await container
    .getByRole("textbox", { name: "容量观察时间（含时区）" })
    .fill(new Date(Date.now() - 250).toISOString());
  await container
    .getByRole("textbox", { name: "证据有效截止时间（含时区）" })
    .fill(new Date(Date.now() + 900_000).toISOString());
}
async function start(page: Page, rescan = false) {
  await page
    .getByRole("button", {
      name: rescan ? "重新扫描并追平" : "创建迁移批次",
      exact: true,
    })
    .click();
  if (!rescan) {
    await page
      .getByRole("combobox", { name: "已保存的迁移源", exact: true })
      .fill(runtime!.source.name);
    await page
      .locator(".ant-select-dropdown:visible")
      .getByText(runtime!.source.name, { exact: true })
      .click();
  }
  await page.getByRole("button", { name: "运行预检", exact: true }).click();
  for (const [source, target] of [
    ["releases", runtime!.repositories.maven.name],
    ["raw-source", runtime!.repositories.raw.name],
  ]) {
    const checkbox = page.getByRole("checkbox", {
      name: `${source} · ${source === "releases" ? "maven2" : "raw"}`,
      exact: true,
    });
    await checkbox.check();
    if (!rescan) {
      await page
        .getByRole("combobox", { name: `${source} 的目标仓库`, exact: true })
        .fill(target);
      await page
        .locator(".ant-select-dropdown:visible")
        .getByText(target, { exact: true })
        .click();
    }
  }
  const submit = page.getByRole("button", {
    name: "应用映射并创建批次",
    exact: true,
  });
  await expect(submit).toBeDisabled();
  await fillCapacity(page);
  const response = page.waitForResponse(
    (response) =>
      response.request().method() === "POST" &&
      new URL(response.url()).pathname === "/api/v2/migrations/nexus/batches",
  );
  await submit.click();
  const created = await response;
  expect(created.status()).toBe(202);
  const batch = (await created.json()) as NexusMigrationBatch;
  await expect(page).toHaveURL(new RegExp(`batch=${batch.id}`));
  return batch;
}
async function readBatch(request: APIRequestContext, id: string) {
  const response = await request.get(`/api/v2/migrations/nexus/batches/${id}`, {
    headers: { Authorization: `Bearer ${runtime!.adminToken}` },
  });
  expect(response.ok()).toBeTruthy();
  return (await response.json()) as NexusMigrationBatch;
}
async function finished(page: Page, request: APIRequestContext, id: string) {
  await expect
    .poll(async () => (await readBatch(request, id)).state, { timeout: 90_000 })
    .toBe("completed");
  await page
    .getByTestId("nexus-batch-detail")
    .getByRole("button", { name: "刷新批次" })
    .click();
  await expect(
    page
      .getByTestId("nexus-batch-detail")
      .getByText("后台批次已完成", { exact: true }),
  ).toBeVisible();
  return readBatch(request, id);
}
async function entryPage(request: APIRequestContext, id: string) {
  const response = await request.get(
    `/api/v2/migrations/nexus/batches/${id}/entries?limit=100`,
    { headers: { Authorization: `Bearer ${runtime!.adminToken}` } },
  );
  expect(response.ok()).toBeTruthy();
  return (await response.json()) as {
    items: NexusMigrationEntry[];
    nextCursor: string;
  };
}
async function retry(page: Page, batchId: string) {
  await page
    .getByTestId("nexus-batch-detail")
    .getByRole("button", { name: "刷新批次" })
    .click();
  await page
    .getByRole("button", { name: "重试可重试失败项", exact: true })
    .click();
  const dialog = page.getByRole("dialog", {
    name: "重试可重试失败项",
    exact: true,
  });
  const submit = dialog.getByRole("button", {
    name: "重试可重试失败项",
    exact: true,
  });
  await expect(submit).toBeDisabled();
  await fillCapacity(dialog);
  const response = page.waitForResponse(
    (response) =>
      response.request().method() === "POST" &&
      new URL(response.url()).pathname ===
        `/api/v2/migrations/nexus/batches/${batchId}/retry`,
  );
  await submit.click();
  expect((await response).status()).toBe(200);
  await expect(dialog).toBeHidden();
}

test("real source mappings, full copy, reopen and full rescan retain independent bytes and honest acceptance", async ({
  page: initial,
  request,
}, info) => {
  test.setTimeout(180_000);
  phase(1);
  await prepareTargets(request);
  let page = initial;
  await authenticate(page);
  const errors: string[] = [];
  page.on("pageerror", (error) => errors.push(error.message));
  await page.goto("/operations?tab=migrations&view=batches");
  const first = await start(page);
  const full = await finished(page, request, first.id);
  expect(full.scanComplete).toBe(true);
  expect(full.totalBytes).not.toBeNull();
  expect(full.verifiedBytes).toBeGreaterThan(0);
  expect(full.visibleIdentities).toBe(4);
  const entries = await entryPage(request, first.id);
  expect(entries.nextCursor).toBe("");
  expect(entries.items).toHaveLength(4);
  const files = JSON.parse(readFileSync(runtime!.fixtureFiles, "utf8")) as {
    initial: Record<string, Record<string, string>>;
  };
  for (const entry of entries.items)
    for (const asset of entry.assets) {
      const body = Buffer.from(
        files.initial[entry.sourceRepository][asset.sourcePath],
        "base64",
      );
      expect(asset.size).toBe(body.length);
      expect(asset.sha256).toBe(
        createHash("sha256").update(body).digest("hex"),
      );
      expect(asset.verified).toBe(true);
      const target =
        entry.format === "maven"
          ? runtime!.repositories.maven
          : runtime!.repositories.raw;
      const response = await request.get(
        `/${entry.format}/${target.name}/${asset.targetPath}`,
        // The fixture resolver intentionally has no Maven repository grant.
        // This byte read-back uses the actual administrator; it is not a
        // claim that production consumer credentials were migrated or tested.
        { headers: { Authorization: `Bearer ${runtime!.adminToken}` } },
      );
      expect(response.ok()).toBeTruthy();
      expect(await response.body()).toEqual(body);
    }
  await expect(
    page.getByText("本批次 target-only 冷客户端验收：未验证", { exact: true }),
  ).toBeVisible();
  const downloadPromise = page.waitForEvent("download");
  await page
    .getByRole("button", { name: "导出当前观察报告", exact: true })
    .click();
  const download = await downloadPromise;
  const exportPath = info.outputPath("observed-full.json");
  await download.saveAs(exportPath);
  const report = JSON.parse(readFileSync(exportPath, "utf8"));
  expect(report.inventoryComplete).toBe(true);
  expect(report.coldClient).toBe("unverified");
  expect(report.finalCatchupAfterSourceFreeze).toBe("unverified");
  expect(JSON.stringify(report)).not.toMatch(
    /fixture-password|fixture-reader|credentialCiphertext|validator/,
  );
  const url = page.url();
  await page.close();
  // A fresh browser process has no prior page/query cache or local storage.
  // Re-authentication supplies only the administrator token, never source credentials.
  const reopened = await chromium.launch();
  try {
    page = await reopened.newPage();
    await authenticate(page);
    await page.goto(url);
    await expect(
      page.getByRole("heading", { name: first.id, exact: true }),
    ).toBeVisible();
    await expect(
      page
        .getByTestId("nexus-batch-detail")
        .getByText("后台批次已完成", { exact: true }),
    ).toBeVisible();
    phase(2);
    const next = await start(page, true);
    const delta = await finished(page, request, next.id);
    expect(delta.counts.skipped).toBeGreaterThanOrEqual(3);
    expect(delta.counts.sourceDeleted).toBe(1);
    expect(delta.counts.committed).toBe(2);
    await expect(
      page.getByText(/存在冲突、失败、未支持或源变化/),
    ).toBeVisible();
    await expect(
      page.getByText("冻结写入后的最后追平点：未验证", { exact: true }),
    ).toBeVisible();
    await page.screenshot({
      path: info.outputPath("real-batch-rescan.png"),
      fullPage: true,
      animations: "disabled",
    });
    expect(errors).toEqual([]);
    if (runtime!.resolverToken)
      expect(
        (
          await request.get(`/api/v2/migrations/nexus/batches/${next.id}`, {
            headers: { Authorization: `Bearer ${runtime!.resolverToken}` },
          })
        ).status(),
      ).toBe(403);
  } finally {
    await reopened.close();
    phase(1);
  }
});

test("real pause drain, fresh resume, worker restart and cancel are durable server operations", async ({
  page,
  request,
}, info) => {
  test.setTimeout(240_000);
  phase(3, { chunkDelayMs: 80 });
  await prepareTargets(request);
  await authenticate(page);
  try {
    await page.goto("/operations?tab=migrations&view=batches");
    const batch = await start(page);
    await expect
      .poll(async () => (await readBatch(request, batch.id)).state, {
        timeout: 60_000,
      })
      .toBe("running");
    await page
      .getByTestId("nexus-batch-detail")
      .getByRole("button", { name: "刷新批次" })
      .click();
    await page.getByRole("button", { name: "暂停批次", exact: true }).click();
    await expect
      .poll(async () => (await readBatch(request, batch.id)).state, {
        timeout: 60_000,
      })
      .toBe("paused");
    await page
      .getByTestId("nexus-batch-detail")
      .getByRole("button", { name: "刷新批次" })
      .click();
    await page.getByRole("button", { name: "恢复批次", exact: true }).click();
    const dialog = page.getByRole("dialog", { name: "恢复批次", exact: true });
    await expect(
      dialog.getByRole("textbox", { name: "存储池可用字节", exact: true }),
    ).toHaveValue("");
    await fillCapacity(dialog);
    await dialog.getByRole("button", { name: "恢复批次", exact: true }).click();
    await expect(dialog).toBeHidden();
    compose(["restart", "migration-worker"]);
    const after = await finished(page, request, batch.id);
    expect(after.visibleIdentities).toBeGreaterThan(0);
    await page.screenshot({
      path: info.outputPath("real-worker-restart.png"),
      fullPage: true,
      animations: "disabled",
    });
    const cancelling = await start(page, true);
    await page.getByRole("button", { name: "取消批次", exact: true }).click();
    const confirm = page.getByRole("dialog", {
      name: "取消这个迁移批次？",
      exact: true,
    });
    await expect(
      confirm.getByText(/已提交的身份和源内容都会保留/),
    ).toBeVisible();
    await confirm
      .getByRole("button", { name: "取消批次", exact: true })
      .click();
    await expect
      .poll(async () => (await readBatch(request, cancelling.id)).state, {
        timeout: 60_000,
      })
      .toBe("cancelled");
  } finally {
    phase(1);
  }
});

test("real failed entry retry is bounded and reuses the batch without overwriting unrelated identities", async ({
  page,
  request,
}, info) => {
  test.setTimeout(150_000);
  phase(1, { failPaths: ["raw-source/release.bin"] });
  await prepareTargets(request);
  await authenticate(page);
  try {
    await page.goto("/operations?tab=migrations&view=batches");
    const batch = await start(page);
    await expect
      .poll(async () => (await readBatch(request, batch.id)).state, {
        timeout: 60000,
      })
      .toBe("failed");
    const failed = await readBatch(request, batch.id);
    expect(failed.scanComplete).toBe(true);
    expect(failed.counts.failed).toBe(1);
    expect(failed.visibleIdentities).toBe(3);
    phase(1);
    await retry(page, batch.id);
    const done = await finished(page, request, batch.id);
    expect(done.id).toBe(batch.id);
    expect(done.visibleIdentities).toBe(4);
    expect(done.counts.failed).toBe(0);
    const inventory = await entryPage(request, batch.id);
    const repaired = inventory.items.find(
      (entry) => entry.identity === "release.bin",
    );
    expect(repaired?.attempts).toBe(2);
    expect(repaired?.state).toBe("committed");
    await page.screenshot({
      path: info.outputPath("real-failed-entry-retry.png"),
      fullPage: true,
      animations: "disabled",
    });
  } finally {
    phase(1);
  }
});

for (const scenario of ["missing-source", "bad-digest"] as const)
  test(`real ${scenario} scan failure remains unsealed and exposes durable asset diagnosis`, async ({
    page,
    request,
  }, info) => {
    test.setTimeout(150_000);
    const path =
      scenario === "missing-source"
        ? "release.bin"
        : "org/example/ui-widget/1.0+build/ui-widget-1.0+build.jar";
    const repository =
      scenario === "missing-source" ? "raw-source" : "releases";
    phase(
      1,
      scenario === "missing-source"
        ? {
            omitHashesPaths: [`${repository}/${path}`],
            missingPaths: [`${repository}/${path}`],
          }
        : {
            omitDerivedHashesPaths: [`${repository}/${path}`],
            badBytesPaths: [`${repository}/${path}`],
          },
    );
    await prepareTargets(request);
    await authenticate(page);
    try {
      await page.goto("/operations?tab=migrations&view=batches");
      const batch = await start(page);
      await expect
        .poll(async () => (await readBatch(request, batch.id)).state, {
          timeout: 60000,
        })
        .toBe("failed");
      const failed = await readBatch(request, batch.id);
      expect(failed.scanComplete).toBe(false);
      expect(failed.totalBytes).toBeNull();
      expect(failed.entryCount).toBe(0);
      expect(failed.lastScanFailure?.sourceRepository).toBe(repository);
      expect(failed.lastScanFailure?.sourcePath).toBe(path);
      expect(failed.lastScanFailure?.identity).not.toBe("");
      const expectedCode =
        scenario === "missing-source" ? "source_missing" : "source_changed";
      expect(failed.lastScanFailure?.code).toBe(expectedCode);
      expect(failed.lastScanFailure?.stage).toBe(
        scenario === "missing-source"
          ? "source_verification_open"
          : "source_verification_digest",
      );
      expect(
        [...(failed.lastScanFailure?.sourceRepositories ?? [])].sort(),
      ).toEqual(["raw-source", "releases"]);
      if (scenario === "bad-digest")
        expect(failed.sourceVerificationBytes).toBeGreaterThan(0);
      const inventory = await entryPage(request, batch.id);
      expect(inventory.items).toEqual([]);
      await page
        .getByTestId("nexus-batch-detail")
        .getByRole("button", { name: "刷新批次" })
        .click();
      const diagnosis = page.getByRole("region", {
        name: "最近的持久扫描失败定位",
        exact: true,
      });
      await expect(diagnosis).toContainText(path);
      await expect(diagnosis).toContainText(repository);
      await expect(diagnosis).toContainText(expectedCode);
      await expect(
        page.getByText("未知；完整清单尚未封存", { exact: true }),
      ).toBeVisible();
      await expect(page.locator("body")).not.toContainText("fixture-password");
      await page.screenshot({
        path: info.outputPath(`real-${scenario}-diagnosis.png`),
        fullPage: true,
        animations: "disabled",
      });
      phase(1);
      await retry(page, batch.id);
      const completed = await finished(page, request, batch.id);
      expect(completed.id).toBe(batch.id);
      expect(completed.visibleIdentities).toBe(4);
    } finally {
      phase(1);
    }
  });

test("a real worker interruption during source probing never promotes a partial scan and can retry the original batch", async ({
  page,
  request,
}, info) => {
  test.setTimeout(240_000);
  phase(1, { omitHashesPaths: ["raw-source/release.bin"], delayMs: 5000 });
  await prepareTargets(request);
  await authenticate(page);
  try {
    await page.goto("/operations?tab=migrations&view=batches");
    const batch = await start(page);
    await expect
      .poll(
        async () => {
          const state = await readBatch(request, batch.id);
          return state.metadataDiscovered > 0 && !state.scanComplete;
        },
        { timeout: 60000 },
      )
      .toBe(true);
    compose(["kill", "-s", "KILL", "migration-worker"]);
    compose(["start", "migration-worker"]);
    phase(1);
    // Exercise the real two-minute lease expiry; never shorten the database
    // clock/lease or inject a synthetic checkpoint to make this gate pass.
    await expect
      .poll(async () => (await readBatch(request, batch.id)).state, {
        timeout: 160000,
      })
      .toBe("failed");
    const interrupted = await readBatch(request, batch.id);
    expect(interrupted.lastError).toBe("scan_interrupted");
    expect(interrupted.scanComplete).toBe(false);
    expect(interrupted.totalBytes).toBeNull();
    expect(interrupted.entryCount).toBe(0);
    await retry(page, batch.id);
    const done = await finished(page, request, batch.id);
    expect(done.id).toBe(batch.id);
    expect(done.visibleIdentities).toBe(4);
    await page.screenshot({
      path: info.outputPath("real-interrupted-scan-retry.png"),
      fullPage: true,
      animations: "disabled",
    });
  } finally {
    phase(1);
    compose(["start", "migration-worker"]);
  }
});

test("real explicit Hosted preview continues into generated-client batch application", async ({
  page,
  request,
}, info) => {
  test.setTimeout(120_000);
  phase(1);
  await prepareTargets(request);
  await authenticate(page);
  await page.goto(`/operations?tab=migrations&source=${runtime!.source.id}`);
  await page.getByRole("button", { name: "运行预检", exact: true }).click();
  for (const [source, target] of [
    ["releases", runtime!.repositories.maven.name],
    ["raw-source", runtime!.repositories.raw.name],
  ]) {
    await page
      .getByRole("checkbox", { name: `选择 ${source}`, exact: true })
      .check();
    await page
      .getByRole("textbox", { name: `${source} 的目标名称`, exact: true })
      .fill(target);
  }
  await page.getByRole("button", { name: "预览显式映射", exact: true }).click();
  await expect(page.getByText(/所选映射通过当前校验/)).toBeVisible();
  await page
    .getByRole("button", { name: "使用 2 个 Hosted 映射创建批次", exact: true })
    .click();
  await expect(page).toHaveURL(/view=batches/);
  await page.getByRole("button", { name: "运行预检", exact: true }).click();
  await expect(
    page.getByRole("combobox", { name: "releases 的目标仓库", exact: true }),
  ).toBeVisible();
  const submit = page.getByRole("button", {
    name: "应用映射并创建批次",
    exact: true,
  });
  await expect(submit).toBeDisabled();
  await fillCapacity(page);
  const response = page.waitForResponse(
    (response) =>
      response.request().method() === "POST" &&
      new URL(response.url()).pathname === "/api/v2/migrations/nexus/batches",
  );
  await submit.click();
  const created = await response;
  expect(created.status()).toBe(202);
  const batch = (await created.json()) as NexusMigrationBatch;
  expect(batch.mappings).toEqual(
    expect.arrayContaining([
      {
        sourceRepository: "releases",
        targetRepositoryId: runtime!.repositories.maven.id,
      },
      {
        sourceRepository: "raw-source",
        targetRepositoryId: runtime!.repositories.raw.id,
      },
    ]),
  );
  await expect(page).toHaveURL(new RegExp(`batch=${batch.id}`));
  await finished(page, request, batch.id);
  await expect(
    page.getByText("本批次 target-only 冷客户端验收：未验证", { exact: true }),
  ).toBeVisible();
  await page.screenshot({
    path: info.outputPath("real-preview-applied.png"),
    fullPage: true,
    animations: "disabled",
  });
});
