import { expect, test, type Page } from "@playwright/test";
import type { RepositoryQuotaAlertRule } from "../src/client";
import { mockDefaultSiteSettings } from "./support/siteSettings";

const policy = {
  warningBasisPoints: 8000,
  criticalBasisPoints: 9500,
  recoveryBelowBasisPoints: 7500,
  warningForSeconds: 120,
  criticalForSeconds: 30,
  recoveryForSeconds: 300,
  maxSampleAgeSeconds: 60,
};
const rule: RepositoryQuotaAlertRule = {
  id: "rule-a",
  repositoryId: "repo-a",
  targetId: "target-a",
  targetVersion: "target-v1",
  enabled: false,
  deleted: false,
  version: "rule-v1",
  stateVersion: "state-v1",
  sequence: 1,
  policy,
  state: {
    severity: "critical",
    phase: "firing",
    dataState: "stale",
    usedBytes: 0,
    quotaBytes: 10000,
    lastSampleAt: "2026-10-04T10:00:00Z",
  },
  createdAt: "2026-10-04T09:00:00Z",
  updatedAt: "2026-10-04T10:00:00Z",
};

async function fixture(
  page: Page,
  locale = "zh-CN",
  theme = "dark",
  empty = false,
) {
  await mockDefaultSiteSettings(page);
  await page.clock.setFixedTime(new Date("2026-10-04T12:00:00Z"));
  await page.addInitScript(
    ({ locale, theme }) => {
      localStorage.setItem("ag.console.locale", locale);
      localStorage.setItem("ag.console.theme", theme);
    },
    { locale, theme },
  );
  await page.route("**/auth/session", (route) =>
    route.fulfill({
      json: {
        authenticated: true,
        identity: {
          actor: "synthetic-admin",
          kind: "local_session",
          role: "admin",
          administrator: true,
        },
      },
    }),
  );
  await page.route("**/api/v2/diagnostics", (route) =>
    route.fulfill({
      json: {
        build: { version: "dev", revision: "unknown" },
        runtime: { instanceId: "synthetic", roles: ["api"] },
        dependencies: [],
        queues: [],
        nodes: {
          status: "healthy",
          online: 1,
          stale: 0,
          offline: 0,
          issues: [],
        },
      },
    }),
  );
  await page.route("**/api/v2/repositories?*", (route) =>
    route.fulfill({
      json: {
        items: [
          {
            id: "repo-a",
            name: "Synthetic Raw",
            format: "raw",
            type: "hosted",
            state: "active",
          },
        ],
      },
    }),
  );
  await page.route("**/api/v2/email-targets", (route) =>
    route.fulfill({
      json: [
        {
          id: "target-a",
          name: "Synthetic operations",
          locale: "zh-CN",
          enabled: true,
          recipientConfigured: true,
          version: "target-v1",
          createdAt: rule.createdAt,
          updatedAt: rule.updatedAt,
        },
      ],
    }),
  );
  await page.route("**/api/v2/email-notifications", (route) =>
    route.fulfill({
      json: { enabled: false, reason: "encryption_key_unavailable" },
    }),
  );
  let current = empty ? [] : [rule];
  const writes: Array<{
    method: string;
    body: unknown;
    version: string | null;
  }> = [];
  await page.route(
    "**/api/v2/repository-quota-alert-rules**",
    async (route) => {
      const req = route.request();
      const path = new URL(req.url()).pathname;
      if (req.method() === "GET" && path.endsWith("/events"))
        return route.fulfill({
          json: [
            {
              id: "event-a",
              ruleId: "rule-a",
              ruleVersion: "rule-v1",
              repositoryId: "repo-a",
              repositoryName: "Synthetic Raw",
              episodeId: "episode-a",
              sequence: 1,
              scenario: "critical",
              usedBytes: 9900,
              quotaBytes: 10000,
              policy,
              occurredAt: rule.updatedAt,
              sampleAt: rule.updatedAt,
              evidenceSince: rule.createdAt,
              targetId: "target-a",
              targetVersion: "target-v1",
              notificationCode: "queued",
              templateVersion: "quota-1",
              deliveryId: "delivery-a",
              deliveryState: "accepted",
            },
          ],
        });
      if (req.method() === "GET") return route.fulfill({ json: current });
      const body = req.method() === "DELETE" ? undefined : req.postDataJSON();
      writes.push({
        method: req.method(),
        body,
        version: req.headers()["if-match"] ?? null,
      });
      current = [
        {
          ...rule,
          ...body,
          version: "rule-v2",
          deleted: req.method() === "DELETE",
          state: {
            ...rule.state,
            dataState: req.method() === "DELETE" ? "deleted" : "disabled",
          },
        },
      ];
      return req.method() === "DELETE"
        ? route.fulfill({ status: 204 })
        : route.fulfill({
            status: req.method() === "POST" ? 201 : 200,
            json: current[0],
          });
    },
  );
  await page.route("**/api/v2/email-deliveries/delivery-a", (route) =>
    route.fulfill({
      json: {
        id: "delivery-a",
        eventId: "event-a",
        targetId: "target-a",
        targetVersion: "target-v1",
        scenario: "critical",
        locale: "zh-CN",
        templateVersion: "quota-1",
        state: "accepted",
        attempts: 2,
        possibleDuplicate: true,
        version: "delivery-v2",
        createdAt: rule.createdAt,
        updatedAt: rule.updatedAt,
        acceptedAt: rule.updatedAt,
      },
    }),
  );
  return writes;
}

async function geometry(page: Page) {
  const result = await page.evaluate(() => {
    const stack = document.querySelector(".ag-quota-alerts")!;
    const children = [...stack.children]
      .map((el) => el.getBoundingClientRect())
      .filter((rect) => rect.height > 0);
    return {
      overflow: document.documentElement.scrollWidth - window.innerWidth,
      gaps: children.slice(1).map((rect, i) => rect.top - children[i].bottom),
    };
  });
  expect(result.overflow).toBeLessThanOrEqual(1);
  for (const gap of result.gaps) {
    expect(gap).toBeGreaterThanOrEqual(16);
    expect(gap).toBeLessThanOrEqual(18);
  }
}

async function capture(page: Page, path: string) {
  await page.evaluate(() => window.scrollTo(0, 0));
  await expect.poll(() => page.evaluate(() => window.scrollY)).toBe(0);
  await page.evaluate(
    () =>
      new Promise<void>((resolve) =>
        requestAnimationFrame(() => requestAnimationFrame(() => resolve())),
      ),
  );
  await page.screenshot({ path, fullPage: true, animations: "disabled" });
}

for (const width of [1440, 390, 320])
  for (const locale of ["zh-CN", "en-US"])
    for (const theme of ["dark", "light"]) {
      test(`quota state, editor and evidence fit ${width} ${locale} ${theme}`, async ({
        page,
      }) => {
        const errors: string[] = [];
        page.on("pageerror", (error) => errors.push(error.message));
        page.on("console", (message) => {
          if (message.type() === "error") errors.push(message.text());
        });
        await page.setViewportSize({ width, height: 900 });
        const writes = await fixture(page, locale, theme);
        await page.goto("/system?tab=alerts");
        await page.addStyleTag({
          content:
            "*, *::before, *::after { animation-duration: 0s !important; transition-duration: 0s !important; }",
        });
        await expect(
          page.getByRole("button", {
            name:
              locale === "zh-CN" ? "查看 Synthetic Raw" : "View Synthetic Raw",
          }),
        ).toBeVisible();
        await geometry(page);
        await page
          .getByRole("button", {
            name:
              locale === "zh-CN" ? "查看 Synthetic Raw" : "View Synthetic Raw",
          })
          .click();
        await expect(
          page.getByText(
            locale === "zh-CN"
              ? "SMTP 服务器已接受"
              : "Accepted by SMTP server",
            { exact: true },
          ),
        ).toBeVisible();
        await geometry(page);
        await page
          .getByRole("button", {
            name: locale === "zh-CN" ? "查看交付详情" : "View delivery details",
          })
          .click();
        await expect(
          page.getByText(
            locale === "zh-CN" ? "可能重复" : "Possible duplicate",
            { exact: true },
          ),
        ).toBeVisible();
        await expect(
          page.getByRole("button", {
            name: locale === "zh-CN" ? "启用规则" : "Enable rule",
          }),
        ).toBeDisabled();
        await page
          .getByRole("button", {
            name: locale === "zh-CN" ? "编辑规则" : "Edit rule",
          })
          .click();
        await expect(
          page.getByRole("textbox", {
            name:
              locale === "zh-CN" ? "警告阈值（%）" : "Warning threshold (%)",
          }),
        ).toHaveValue("80");
        await geometry(page);
        if (theme === "dark")
          await capture(
            page,
            `../.impeccable/review/quota-editor-${width}-${locale}.png`,
          );
        await page
          .getByRole("button", {
            name: locale === "zh-CN" ? "取消" : "Cancel",
            exact: true,
          })
          .click();
        await expect(page.getByRole("textbox")).toHaveCount(0);
        if (theme === "light")
          await capture(
            page,
            `../.impeccable/review/quota-detail-${width}-${locale}.png`,
          );
        expect(writes).toHaveLength(0);
        expect(errors).toEqual([]);
        expect(
          await page.evaluate(() =>
            Object.keys(localStorage).filter(
              (key) => !/theme|locale|^ag\.console\.role$/.test(key),
            ),
          ),
        ).toEqual([]);
      });
    }

test("creation is explicit, disabled, and persists exact policy; deletion retains history", async ({
  page,
}) => {
  const writes = await fixture(page, "zh-CN", "dark", true);
  await page.goto("/system?tab=alerts");
  await page.getByRole("button", { name: "创建规则" }).click();
  await expect(
    page.getByRole("textbox", { name: "警告阈值（%）" }),
  ).toHaveValue("");
  await page.getByRole("combobox", { name: "仓库", exact: true }).click();
  await page
    .getByRole("option", { name: "Synthetic Raw", exact: true })
    .click();
  await page.getByRole("combobox", { name: "邮件目标", exact: true }).click();
  await page.getByRole("option", { name: /Synthetic operations/ }).click();
  for (const [label, value] of [
    ["警告阈值（%）", "80.01"],
    ["严重阈值（%）", "95"],
    ["恢复阈值（%）", "75"],
    ["警告持续时间（秒）", "120"],
    ["严重持续时间（秒）", "30"],
    ["恢复持续时间（秒）", "300"],
    ["最大样本年龄（秒）", "60"],
  ])
    await page.getByRole("textbox", { name: label, exact: true }).fill(value);
  await page.getByRole("button", { name: "创建停用规则", exact: true }).click();
  await expect(page.getByText("规则已保存", { exact: true })).toBeVisible();
  expect(writes).toEqual([
    {
      method: "POST",
      version: null,
      body: {
        repositoryId: "repo-a",
        targetId: "target-a",
        enabled: false,
        policy: { ...policy, warningBasisPoints: 8001 },
      },
    },
  ]);
  await page.getByRole("button", { name: "删除规则" }).click();
  await page.getByRole("button", { name: "取消", exact: true }).click();
  expect(writes).toHaveLength(1);
  await page.getByRole("button", { name: "删除规则" }).click();
  await page.getByRole("button", { name: "确认删除", exact: true }).click();
  await expect(
    page.getByText("规则已删除，历史仍可查看", { exact: true }),
  ).toBeVisible();
  expect(writes[1]).toMatchObject({ method: "DELETE", version: "rule-v2" });
  await expect(
    page.getByRole("button", { name: "查看交付详情" }),
  ).toBeVisible();
});

test("a forced password gate has no form and filters unsafe response messages", async ({
  page,
}) => {
  await fixture(page);
  await page.route("**/api/v2/repository-quota-alert-rules", (route) =>
    route.fulfill({
      status: 403,
      json: {
        code: "password_change_required",
        message: "smtp-sensitive-marker",
      },
    }),
  );
  await page.goto("/system?tab=alerts");
  await expect(page.getByText(/必须先修改密码/)).toBeVisible();
  await expect(page.getByRole("textbox")).toHaveCount(0);
  await expect(page.getByText(/sensitive-marker/)).toHaveCount(0);
});

test("polling pauses when hidden, refreshes on return, and stops outside the alerts tab", async ({
  page,
}) => {
  await fixture(page);
  await page.clock.install();
  let reads = 0;
  await page.route("**/api/v2/repository-quota-alert-rules", (route) => {
    reads++;
    return route.fulfill({ json: [rule] });
  });
  await page.goto("/system?tab=alerts");
  await expect(
    page.getByRole("button", { name: "查看 Synthetic Raw" }),
  ).toBeVisible();
  expect(reads).toBe(1);
  await page.evaluate(() => {
    Object.defineProperty(document, "visibilityState", {
      value: "hidden",
      configurable: true,
    });
    document.dispatchEvent(new Event("visibilitychange"));
  });
  await page.clock.fastForward(90000);
  expect(reads).toBe(1);
  await page.evaluate(() => {
    Object.defineProperty(document, "visibilityState", {
      value: "visible",
      configurable: true,
    });
    document.dispatchEvent(new Event("visibilitychange"));
  });
  await expect.poll(() => reads).toBe(2);
  await page.getByRole("tab", { name: "运行日志", exact: true }).click();
  await page.clock.fastForward(90000);
  expect(reads).toBe(2);
});

for (const locale of ["zh-CN", "en-US"]) {
  test(`desktop quota field errors focus selections and expose precise constraints ${locale}`, async ({
    page,
  }) => {
    await page.setViewportSize({ width: 1440, height: 1000 });
    await page.route("**/api/**", (route) =>
      route.fulfill({ status: 503, json: { code: "synthetic-unavailable" } }),
    );
    const writes = await fixture(page, locale, "dark");
    await page.goto("/system?tab=alerts");
    const zh = locale === "zh-CN";
    await page
      .getByRole("button", {
        name: zh ? "创建规则" : "Create rule",
        exact: true,
      })
      .click();
    const save = page.getByRole("button", {
      name: zh ? "创建停用规则" : "Create disabled rule",
      exact: true,
    });
    await save.focus();
    await page.keyboard.press("Enter");
    const repository = page.getByRole("combobox", {
      name: zh ? "仓库" : "Repository",
      exact: true,
    });
    await expect(repository).toBeFocused();
    await expect(repository).toHaveAttribute("aria-invalid", "true");
    await expect(repository).toHaveAccessibleDescription(
      zh ? /请选择仓库/ : /Choose a repository/,
    );
    const warning = page.getByRole("textbox", {
      name: zh ? "警告阈值（%）" : "Warning threshold (%)",
      exact: true,
    });
    await warning.fill("80.001");
    await expect(warning).toHaveAccessibleDescription(
      zh ? /最多两位小数/ : /up to two decimals/,
    );
    const duration = page.getByRole("textbox", {
      name: zh ? "警告持续时间（秒）" : "Warning hold duration (seconds)",
      exact: true,
    });
    await duration.fill("120");
    await expect(duration).not.toHaveAttribute("aria-invalid", "true");
    // Read settled geometry after the editor's entrance animation.
    await expect
      .poll(() =>
        page.locator(".ag-quota-alerts").evaluate(async (stack) => {
          await Promise.all(
            stack
              .getAnimations({ subtree: true })
              .map((animation) => animation.finished),
          );
          return stack
            .getAnimations({ subtree: true })
            .filter((animation) => animation.playState === "running").length;
        }),
      )
      .toBe(0);
    await geometry(page);
    expect(writes).toHaveLength(0);
  });
}
