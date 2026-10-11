import { expect, test, type Page, type TestInfo } from "@playwright/test";
import { defaultSiteSettings } from "../src/lib/siteSettings";

const repository = {
  id: "11111111-1111-4111-8111-111111111111",
  name: "synthetic-recovery",
  format: "raw",
  type: "hosted",
  state: "active",
};
const grant = {
  repositoryId: repository.id,
  repositoryName: repository.name,
  format: "raw",
  principal: "user:synthetic",
  scopes: ["repositories:read"],
  resourcePrefix: "",
};

async function setup(page: Page, width: number) {
  await page.setViewportSize({ width, height: 1000 });
  await page.addInitScript((width) => {
    localStorage.setItem(
      "ag.console.locale",
      width === 390 ? "en-US" : "zh-CN",
    );
    localStorage.setItem("ag.console.theme", width === 390 ? "light" : "dark");
    localStorage.setItem("ag.console.token", "synthetic-review-token");
  }, width);
  const identity = {
    actor: "user:synthetic",
    kind: "local_session",
    role: "admin",
    administrator: true,
  };
  await page.route("**/api/v2/**", (route) =>
    route.fulfill({ json: { items: [] } }),
  );
  await page.route("**/auth/session", (route) =>
    route.fulfill({ json: { authenticated: true, identity } }),
  );
  await page.route("**/api/v2/identity", (route) =>
    route.fulfill({ json: identity }),
  );
  await page.route("**/api/v2/site-settings", (route) =>
    route.fulfill({ json: defaultSiteSettings }),
  );
  await page.route("**/api/v2/repositories**", (route) =>
    route.fulfill({ json: { items: [repository] } }),
  );
  await page.route("**/api/v2/repository-grants", (route) =>
    route.fulfill({ json: [grant] }),
  );
  await page.route("**/api/v2/anonymous-access-policy", (route) =>
    route.fulfill({ json: { enabled: false, version: "1" } }),
  );
  for (const name of ["authorization-roles", "authorization-templates"])
    await page.route(`**/api/v2/${name}`, (route) =>
      route.fulfill({ json: [] }),
    );
  const pageErrors: string[] = [],
    consoleErrors: string[] = [];
  page.on("pageerror", (error) => pageErrors.push(error.message));
  page.on("console", (message) => {
    if (message.type() === "error") consoleErrors.push(message.text());
  });
  return { pageErrors, consoleErrors };
}

async function geometry(
  page: Page,
  en: boolean,
  info: TestInfo,
  state: string,
) {
  expect(
    await page
      .locator("html")
      .evaluate((el) => el.scrollWidth - el.clientWidth),
  ).toBeLessThanOrEqual(0);
  const boxes = await page
    .getByRole("heading", {
      name: en ? "Access control" : "访问控制",
      exact: true,
    })
    .evaluate((el) => {
      const root = el.closest(".ag-page-stack");
      if (!root) throw new Error("Missing access page flow");
      return Array.from(root.children).map((child) => {
        const r = child.getBoundingClientRect();
        return {
          top: r.top,
          bottom: r.bottom,
          header: child.classList.contains("ag-page-header"),
          primary: child.classList.contains("ag-page-primary"),
        };
      });
    });
  for (let i = 1; i < boxes.length; i++) {
    const gap = boxes[i].top - boxes[i - 1].bottom;
    const expected =
      16 + (boxes[i - 1].header ? 8 : 0) + (boxes[i].primary ? 8 : 0);
    expect(gap).toBeGreaterThanOrEqual(expected - 1);
    expect(gap).toBeLessThanOrEqual(expected + 2);
  }
  if (process.env.CAPTURE_LAYOUT_EVIDENCE === "1")
    await page.screenshot({
      path: info.outputPath(`${state}.png`),
      fullPage: true,
      animations: "disabled",
    });
}

for (const width of [1440, 390]) {
  const en = width === 390;
  const retry = en ? /Retry/ : /重\s*试/;
  const fallback = en ? "This section is unavailable" : "此区域暂时无法显示";
  test(`access keeps cached grants after refresh failure at ${width}px`, async ({
    page,
  }, info) => {
    const errors = await setup(page, width);
    let failed = false;
    await page.route("**/api/v2/repository-grants", (route) =>
      failed
        ? route.fulfill({
            status: 503,
            json: { status: 503, message: "Synthetic refresh failed" },
          })
        : route.fulfill({ json: [grant] }),
    );
    await page.goto("/access");
    await expect(
      page.getByRole("link", { name: repository.name, exact: true }),
    ).toBeVisible();
    expect(errors.pageErrors).toEqual([]);
    expect(errors.consoleErrors).toEqual([]);
    if (width === 390)
      await page
        .getByRole("button", { name: "Open navigation", exact: true })
        .click();
    await page
      .getByRole("link", { name: en ? "API Keys" : "API 密钥", exact: true })
      .click();
    await expect(
      page.getByRole("heading", {
        name: en ? "API keys" : "API 密钥",
        exact: true,
      }),
    ).toBeVisible();
    failed = true;
    if (width === 390)
      await page
        .getByRole("button", { name: "Open navigation", exact: true })
        .click();
    await page
      .getByRole("link", {
        name: en ? "Access Control" : "访问控制",
        exact: true,
      })
      .click();
    await expect(
      page.getByText("Synthetic refresh failed", { exact: true }),
    ).toBeVisible();
    await expect(
      page.getByRole("link", { name: repository.name, exact: true }),
    ).toBeVisible();
    await geometry(page, en, info, "cached-warning");
    errors.consoleErrors.length = 0;
    failed = false;
    await page.getByRole("button", { name: retry, exact: true }).click();
    await expect(
      page.getByText("Synthetic refresh failed", { exact: true }),
    ).toHaveCount(0);
    expect(errors.pageErrors).toEqual([]);
    expect(errors.consoleErrors).toEqual([]);
  });
  test(`access section Retry fetches corrected grants at ${width}px`, async ({
    page,
  }, info) => {
    const errors = await setup(page, width);
    let broken = true,
      reads = 0;
    await page.route("**/api/v2/repository-grants", (route) => {
      reads++;
      return route.fulfill({
        json: [
          {
            ...grant,
            resourcePrefix: broken ? { malformed: true } : "releases/",
          },
        ],
      });
    });
    await page.goto("/access");
    await expect(page.getByText(fallback, { exact: true })).toBeVisible();
    await expect(
      page.getByRole("tab", {
        name: en ? "Access evaluation" : "权限检查",
        exact: true,
      }),
    ).toBeVisible();
    const before = reads;
    await geometry(page, en, info, "section-error");
    errors.consoleErrors.length = 0;
    broken = false;
    await page.getByRole("button", { name: retry, exact: true }).click();
    await expect(
      page.getByRole("link", { name: repository.name, exact: true }),
    ).toBeVisible();
    expect(reads).toBe(before + 1);
    await expect(page.locator(".ag-section-fallback")).toHaveCount(0);
    await expect(page.locator(".ag-route-error-page")).toHaveCount(0);
    await geometry(page, en, info, "section-recovered");
    expect(errors.pageErrors).toEqual([]);
    expect(errors.consoleErrors).toEqual([]);
  });
  test(`access metrics failure leaves policies usable at ${width}px`, async ({
    page,
  }, info) => {
    const errors = await setup(page, width);
    let broken = true;
    await page.route("**/api/v2/repository-grants", (route) =>
      route.fulfill({
        json: [{ ...grant, scopes: broken ? null : grant.scopes }],
      }),
    );
    await page.goto("/access?tab=policies");
    await expect(page.getByText(fallback, { exact: true })).toBeVisible();
    await expect(
      page.getByRole("heading", {
        name: en ? "Public access boundary" : "公开访问边界",
        exact: true,
      }),
    ).toBeVisible();
    await expect(page.locator(".ag-route-error-page")).toHaveCount(0);
    await geometry(page, en, info, "metrics-error");
    errors.consoleErrors.length = 0;
    broken = false;
    await page.getByRole("button", { name: retry, exact: true }).click();
    await expect(page.getByText(fallback, { exact: true })).toHaveCount(0);
    expect(errors.pageErrors).toEqual([]);
    expect(errors.consoleErrors).toEqual([]);
  });
  test(`access directory error can be retried locally at ${width}px`, async ({
    page,
  }, info) => {
    const errors = await setup(page, width);
    let broken = true,
      reads = 0;
    await page.route("**/api/v2/repositories**", (route) => {
      reads++;
      return route.fulfill({ json: { items: broken ? [null] : [repository] } });
    });
    await page.goto("/access?tab=evaluate");
    await expect(
      page.getByRole("button", { name: retry, exact: true }),
    ).toBeVisible();
    await expect(
      page.getByRole("combobox", {
        name: en ? "Principal" : "授权主体",
        exact: true,
      }),
    ).toBeVisible();
    await expect(page.locator(".ag-route-error-page")).toHaveCount(0);
    const before = reads;
    errors.consoleErrors.length = 0;
    broken = false;
    await page.getByRole("button", { name: retry, exact: true }).click();
    await expect(
      page.getByRole("button", { name: retry, exact: true }),
    ).toHaveCount(0);
    expect(reads).toBe(before + 1);
    await geometry(page, en, info, "directory-recovered");
    expect(errors.pageErrors).toEqual([]);
    expect(errors.consoleErrors).toEqual([]);
  });
}
