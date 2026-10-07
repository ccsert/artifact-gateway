import { expect, test, type Page } from "@playwright/test";
import fs from "node:fs/promises";
import path from "node:path";
import { authenticateAsAdmin } from "./support/auth";
import { mockDefaultSiteSettings } from "./support/siteSettings";
import type { EmailDelivery, EmailTarget } from "../src/client";
import { defaultConsoleThemes } from "../src/lib/consoleTheme";

// Playwright's DOM snapshot streamer emits a sandbox script error even for a
// script-free srcdoc. Keep trace events, network, sources and screenshots;
// avoid injecting the DOM snapshotter into this deliberately inert preview.
test.use({
  trace: {
    mode: "retain-on-failure",
    snapshots: false,
    screenshots: true,
    sources: true,
  },
});

const target: EmailTarget = {
  id: "00000000-0000-4000-8000-000000000001",
  name: "Synthetic operations / 合成运维",
  locale: "zh-CN",
  enabled: true,
  recipientConfigured: true,
  version: "v1",
  createdAt: "2026-01-01T12:00:00Z",
  updatedAt: "2026-01-01T12:00:00Z",
};
const delivery: EmailDelivery = {
  id: "synthetic-delivery",
  eventId: "synthetic-event",
  targetId: target.id,
  targetVersion: "v1",
  scenario: "critical",
  locale: "zh-CN",
  templateVersion: "2",
  state: "retrying",
  attempts: 2,
  possibleDuplicate: true,
  errorCode: "smtp_temporary_rejection",
  version: "d1",
  nextAttemptAt: "2026-01-01T12:10:00Z",
  createdAt: target.createdAt,
  updatedAt: target.updatedAt,
};
async function fixture(
  page: Page,
  locale = "zh-CN",
  theme = "dark",
  empty = false,
) {
  await page.addInitScript(
    ({ locale, theme }) => {
      if (window.top !== window) return;
      localStorage.setItem("ag.console.locale", locale);
      localStorage.setItem("ag.console.theme", theme);
    },
    { locale, theme },
  );
  await authenticateAsAdmin(page);
  await mockDefaultSiteSettings(page);
  let targets = empty ? [] : [target];
  const writes: Array<{
    path: string;
    body: Record<string, unknown>;
    headers: Record<string, string>;
  }> = [];
  await page.route("**/api/v2/email-targets**", async (route) => {
    const req = route.request();
    if (req.method() === "GET")
      return route.fulfill({
        json: new URL(req.url()).pathname.endsWith(target.id)
          ? targets[0]
          : targets,
      });
    const body = req.postDataJSON();
    writes.push({ path: req.url(), body, headers: req.headers() });
    const changed = {
      ...target,
      ...body,
      recipient: undefined,
      recipientConfigured: true,
      version: "v2",
    };
    targets = [changed];
    return route.fulfill({
      status: req.method() === "POST" ? 201 : 200,
      json: changed,
    });
  });
  await page.route("**/api/v2/email-notifications", (route) =>
    route.fulfill({ json: { enabled: true, reason: "ready" } }),
  );
  await page.route("**/api/v2/email-deliveries?*", (route) =>
    route.fulfill({
      json: empty
        ? []
        : [
            delivery,
            {
              ...delivery,
              id: "accepted",
              state: "accepted",
              possibleDuplicate: false,
              errorCode: undefined,
            },
            {
              ...delivery,
              id: "dead",
              state: "dead",
              errorCode: "private-raw-response",
              possibleDuplicate: false,
            },
          ],
    }),
  );
  await page.route("**/api/v2/email-notifications:preview", (route) => {
    const { scenario, locale } = route.request().postDataJSON();
    return route.fulfill({
      json: {
        subject: `Synthetic ${scenario} / 合成预览`,
        text: `Synthetic ${locale} ${scenario}`,
        html: `<style>body{font-family:Arial}table{width:100%;table-layout:fixed}</style><table><tr><td><h1>Synthetic ${scenario}</h1><p>合成告警 ${locale}</p></td></tr></table>`,
        templateVersion: "2",
      },
    });
  });
  await page.route("**/api/v2/email-notifications:test", (route) => {
    writes.push({
      path: route.request().url(),
      body: route.request().postDataJSON(),
      headers: route.request().headers(),
    });
    return route.fulfill({
      status: 202,
      json: { ...delivery, state: "pending" },
    });
  });
  return writes;
}
async function geometry(page: Page) {
  await expect
    .poll(() =>
      page.evaluate(
        () =>
          document.documentElement.scrollWidth -
          document.documentElement.clientWidth,
      ),
    )
    .toBeLessThanOrEqual(0);
  const gaps = await page.locator(".ag-email-notifications").evaluate((el) => {
    const boxes = [...el.children]
      .filter((x) => x.getBoundingClientRect().height > 0)
      .map((x) => x.getBoundingClientRect());
    return boxes.slice(1).map((box, i) => box.top - boxes[i].bottom);
  });
  for (const gap of gaps) {
    expect(gap).toBeGreaterThanOrEqual(15);
    expect(gap).toBeLessThanOrEqual(18);
  }
}
async function capture(page: Page, name: string) {
  if (process.env.EMAIL_CONSOLE_SCREENSHOTS !== "1") return;
  const output = path.resolve("../.impeccable/review");
  await fs.mkdir(output, { recursive: true });
  await page.evaluate(() => scrollTo(0, 0));
  await page.screenshot({
    path: path.join(output, name),
    fullPage: true,
    animations: "disabled",
  });
}
for (const width of [320, 390, 1440])
  for (const locale of ["zh-CN", "en-US"])
    for (const theme of ["dark", "light"]) {
      test(`email workspace ${width} ${locale} ${theme}`, async ({ page }) => {
        const errors: string[] = [];
        page.on("pageerror", (e) => errors.push(e.message));
        page.on("console", (m) => {
          if (m.type() === "error") errors.push(m.text());
        });
        await page.setViewportSize({ width, height: 900 });
        await fixture(page, locale, theme);
        await page.goto("/system?tab=email");
        const zh = locale === "zh-CN";
        await expect(
          page.getByRole("button", {
            name: zh ? "新建邮件目标" : "New email target",
          }),
        ).toBeVisible();
        await geometry(page);
        await expect(page.getByText("private-raw-response")).toHaveCount(0);
        await expect(
          page.getByText(zh ? "SMTP 服务器已接受" : "Accepted by SMTP server", {
            exact: true,
          }),
        ).toBeVisible();
        await expect(
          page.getByText(zh ? /可能重复：/ : /Possible duplicate:/),
        ).toBeVisible();
        await page
          .getByRole("button", { name: zh ? "预览模板" : "Preview template" })
          .click();
        const frame = page.frameLocator(".ag-email-preview");
        await expect(
          frame.getByRole("heading", { name: "Synthetic warning" }),
        ).toBeVisible();
        await expect(page.locator("iframe")).toHaveAttribute("sandbox", "");
        await geometry(page);
        await page
          .getByRole("button", {
            name: zh ? `编辑 ${target.name}` : `Edit ${target.name}`,
          })
          .click();
        await capture(page, `email-editor-${width}-${locale}-${theme}.png`);
        await page
          .getByRole("textbox", {
            name: zh ? "收件人地址" : "Recipient address",
          })
          .fill("private@example.test");
        await expect(
          page.getByRole("textbox", {
            name: zh ? "收件人地址" : "Recipient address",
          }),
        ).toHaveAttribute("autocomplete", "off");
        await geometry(page);
        await page
          .getByRole("button", { name: zh ? "取消" : "Cancel", exact: true })
          .click();
        await page
          .getByRole("button", {
            name: zh ? `测试 ${target.name}` : `Test ${target.name}`,
          })
          .click();
        await expect(page.getByText(/v1/)).toBeVisible();
        await expect(page.getByRole("button", { name: /close/i })).toHaveCount(
          0,
        );
        await geometry(page);
        await capture(page, `email-confirm-${width}-${locale}-${theme}.png`);
        await page
          .getByRole("button", { name: zh ? "取消" : "Cancel", exact: true })
          .click();
        expect(
          await page.evaluate(
            () => JSON.stringify(localStorage) + JSON.stringify(sessionStorage),
          ),
        ).not.toContain("private@example.test");
        expect(page.url()).not.toContain("private");
        expect(errors).toEqual([]);
      });
    }

test("preview suppresses scripts, external requests and links before insertion", async ({
  page,
}) => {
  await fixture(page);
  const external: string[] = [];
  await page.route("https://evil.example/**", (route) => {
    external.push(route.request().url());
    return route.abort();
  });
  await page.route("**/api/v2/email-notifications:preview", (route) =>
    route.fulfill({
      json: {
        subject: "<img src=x>",
        text: "Synthetic text",
        templateVersion: "2",
        html: '<base href="https://evil.example"><meta http-equiv="refresh" content="0;url=https://evil.example"><h1>Inert synthetic preview</h1><img src="https://evil.example/image"><iframe src="https://evil.example/frame"></iframe><script>parent.document.body.dataset.compromised="yes"</script><svg onload="alert(1)"></svg><a href="https://evil.example/link" onclick="alert(1)">Disabled detail link</a><form action="https://evil.example/form"><input></form>',
      },
    }),
  );
  await page.goto("/system?tab=email");
  await page.getByRole("button", { name: "预览模板" }).click();
  const frame = page.frameLocator(".ag-email-preview");
  await expect(
    frame.getByRole("heading", { name: "Inert synthetic preview" }),
  ).toBeVisible();
  await expect(frame.locator("a,img,iframe,script,svg,form,base")).toHaveCount(
    0,
  );
  await frame.getByText("Disabled detail link").click();
  expect(page.url()).toContain("/system?tab=email");
  expect(external).toEqual([]);
  expect(
    await page.evaluate(() => document.body.dataset.compromised),
  ).toBeUndefined();
});

test("cancel/navigation clears drafts and duplicate confirmation queues only once", async ({
  page,
}) => {
  const writes = await fixture(page);
  await page.goto("/system?tab=email");
  await page.getByRole("button", { name: "新建邮件目标" }).click();
  await page.getByLabel("目标名称", { exact: true }).fill("Synthetic new");
  await page
    .getByLabel("收件人地址", { exact: true })
    .fill("private@example.test");
  await page.getByRole("tab", { name: "配额告警", exact: true }).click();
  await page.getByRole("tab", { name: "邮件通知", exact: true }).click();
  await page.getByRole("button", { name: "新建邮件目标" }).click();
  await expect(page.getByLabel("收件人地址", { exact: true })).toHaveValue("");
  await page.getByRole("button", { name: "取消", exact: true }).click();
  await page.getByRole("button", { name: `测试 ${target.name}` }).click();
  expect(writes).toHaveLength(0);
  await page
    .getByRole("button", { name: "确认发送合成测试" })
    .evaluate((el: HTMLButtonElement) => {
      el.click();
      el.click();
    });
  await expect(page.getByText(/合成测试已入队/)).toBeVisible();
  expect(writes).toHaveLength(1);
  expect(writes[0].headers["if-match"]).toBe("v1");
  expect(writes[0].body).toEqual({ targetId: target.id, scenario: "warning" });
});

test("initial failure and empty states are mutually exclusive; refresh denial removes form", async ({
  page,
}) => {
  await fixture(page, "zh-CN", "dark", true);
  let release!: () => void;
  const gate = new Promise<void>((resolve) => {
    release = resolve;
  });
  await page.route("**/api/v2/email-targets", async (route) => {
    await gate;
    return route.fulfill({
      status: 503,
      json: { code: "unavailable", message: "private-raw" },
    });
  });
  await page.goto("/system?tab=email");
  await expect(page.getByText("加载中…", { exact: true })).toBeVisible();
  release();
  await expect(
    page.getByText("无法读取邮件通知数据。请重试读取。", { exact: true }),
  ).toBeVisible();
  await expect(page.getByText("加载中…", { exact: true })).toHaveCount(0);
  await expect(page.getByText("private-raw")).toHaveCount(0);
  await page.unroute("**/api/v2/email-targets");
  await page.getByRole("button", { name: /重试|Retry/ }).click();
  await expect(page.getByText("尚无邮件目标", { exact: true })).toBeVisible();
  await page.getByRole("button", { name: "新建邮件目标" }).click();
  await page
    .getByLabel("收件人地址", { exact: true })
    .fill("private@example.test");
  await page.route("**/api/v2/email-targets", (route) =>
    route.fulfill({ status: 403, json: { code: "password_change_required" } }),
  );
  await page.getByRole("button", { name: "刷新", exact: true }).click();
  await expect(page.getByText(/必须先修改密码/)).toBeVisible();
  await expect(page.getByLabel("收件人地址", { exact: true })).toHaveCount(0);
});

for (const mode of ["dark", "light"]) {
  test(`Gateway ${mode} desktop primary action preserves readable text in normal hover active`, async ({
    page,
  }, testInfo) => {
    await page.setViewportSize({ width: 1440, height: 1000 });
    await page.route("**/api/**", (route) =>
      route.fulfill({ status: 503, json: { code: "synthetic-unavailable" } }),
    );
    await fixture(page, "zh-CN", mode);
    await page.addInitScript(
      (id) => localStorage.setItem("ag.console.theme.id", id),
      `gateway-${mode}`,
    );
    await page.route("**/api/v2/site-settings", (route) =>
      route.fulfill({
        json: {
          version: "synthetic-1",
          siteName: "Artifact Gateway",
          logoUrl: "",
          brandMark: "AG",
          availableThemes: defaultConsoleThemes,
          enabledThemeIds: defaultConsoleThemes.map((theme) => theme.id),
          defaultThemeId: "gateway-dark",
          updatedAt: "2026-01-01T12:00:00Z",
        },
      }),
    );
    await page.goto("/system?tab=email");
    await expect(page.locator("html")).toHaveAttribute(
      "data-theme-id",
      `gateway-${mode}`,
    );
    const button = page.getByRole("button", {
      name: "新建邮件目标",
      exact: true,
    });
    const colors = () =>
      button.evaluate((element) => {
        const style = getComputedStyle(element);
        const rgb = (color: string) =>
          color
            .match(/[\d.]+/g)!
            .slice(0, 3)
            .map(Number);
        const luminance = (values: number[]) =>
          values
            .map((v) => v / 255)
            .map((v) =>
              v <= 0.04045 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4,
            )
            .reduce(
              (value, channel, index) =>
                value + channel * [0.2126, 0.7152, 0.0722][index],
              0,
            );
        const contrast = (a: number[], b: number[]) => {
          const [lo, hi] = [luminance(a), luminance(b)].sort((x, y) => x - y);
          return (hi + 0.05) / (lo + 0.05);
        };
        const layers: number[][] = [];
        for (
          let parent = element.parentElement;
          parent;
          parent = parent.parentElement
        ) {
          const values = getComputedStyle(parent)
            .backgroundColor.match(/[\d.]+/g)!
            .map(Number);
          layers.push([...values.slice(0, 3), values[3] ?? 1]);
        }
        const backdrop = layers
          .reverse()
          .reduce(
            (bg, layer) =>
              bg.map(
                (channel, index) =>
                  channel * (1 - layer[3]) + layer[index] * layer[3],
              ),
            [255, 255, 255],
          );
        return {
          foreground: style.color,
          background: style.backgroundColor,
          fontSize: style.fontSize,
          textContrast: contrast(rgb(style.color), rgb(style.backgroundColor)),
          boundaryContrast: contrast(rgb(style.backgroundColor), backdrop),
        };
      });
    const results = [];
    const expectedBackground =
      mode === "dark"
        ? ["rgb(6, 182, 212)", "rgb(34, 211, 238)", "rgb(8, 145, 178)"]
        : ["rgb(8, 127, 156)", "rgb(14, 116, 144)", "rgb(21, 94, 117)"];
    for (const state of ["normal", "hover", "active"]) {
      if (state === "hover") await button.hover();
      if (state === "active") await page.mouse.down();
      await expect
        .poll(async () => (await colors()).background)
        .toBe(expectedBackground[results.length]);
      if (state === "active") {
        expect(
          await button.evaluate((element) => element.matches(":active")),
        ).toBe(true);
      }
      await expect
        .poll(async () => (await colors()).textContrast)
        .toBeGreaterThanOrEqual(4.5);
      const measured = await colors();
      expect(measured.boundaryContrast).toBeGreaterThanOrEqual(3);
      results.push({ state, ...measured });
    }
    await page.mouse.move(10, 10);
    await page.mouse.up();
    await testInfo.attach("actual-button-contrast", {
      body: JSON.stringify(results, null, 2),
      contentType: "application/json",
    });
  });
}

for (const locale of ["zh-CN", "en-US"]) {
  for (const theme of ["dark", "light"]) {
    test(`desktop field validation uses keyboard focus and associated errors ${locale} ${theme}`, async ({
      page,
    }) => {
      await page.setViewportSize({ width: 1440, height: 1000 });
      await page.route("**/api/**", (route) =>
        route.fulfill({ status: 503, json: { code: "synthetic-unavailable" } }),
      );
      const writes = await fixture(page, locale, theme);
      await page.addInitScript(
        (id) => localStorage.setItem("ag.console.theme.id", id),
        `gateway-${theme}`,
      );
      await page.goto("/system?tab=email");
      const zh = locale === "zh-CN";
      await page
        .getByRole("button", {
          name: zh ? "新建邮件目标" : "New email target",
          exact: true,
        })
        .click();
      const save = page.getByRole("button", {
        name: zh ? "保存停用目标" : "Save disabled target",
        exact: true,
      });
      await save.focus();
      await page.keyboard.press("Enter");
      const name = page.getByRole("textbox", {
        name: zh ? "目标名称" : "Target name",
        exact: true,
      });
      const recipient = page.getByRole("textbox", {
        name: zh ? "收件人地址" : "Recipient address",
        exact: true,
      });
      await expect(name).toBeFocused();
      await expect(name).toHaveAttribute("aria-invalid", "true");
      await expect(name).toHaveAccessibleDescription(
        zh ? /请输入目标名称/ : /Enter a target name/,
      );
      await name.fill("Synthetic");
      await expect(name).not.toHaveAttribute("aria-invalid", "true");
      await save.focus();
      await page.keyboard.press("Enter");
      await expect(recipient).toBeFocused();
      await expect(recipient).toHaveAccessibleDescription(
        zh ? /请输入单个收件人邮箱地址/ : /Enter one recipient email address/,
      );
      await recipient.fill("sensitive-marker-invalid");
      await expect(recipient).toHaveAccessibleDescription(
        zh ? /请输入单个有效邮箱/ : /Enter one valid mailbox/,
      );
      await geometry(page);
      expect(writes).toHaveLength(0);
      await page
        .getByRole("button", { name: zh ? "取消" : "Cancel", exact: true })
        .click();
      await page
        .getByRole("button", {
          name: zh ? "新建邮件目标" : "New email target",
          exact: true,
        })
        .click();
      await expect(recipient).toHaveValue("");
      await expect(recipient).not.toHaveAttribute("aria-invalid", "true");
      await expect(
        page.getByText("sensitive-marker-invalid", { exact: true }),
      ).toHaveCount(0);
    });
  }
}
