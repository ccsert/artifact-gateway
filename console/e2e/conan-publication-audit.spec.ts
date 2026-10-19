import { readFileSync } from "node:fs";
import { expect, test } from "@playwright/test";
import type { AuditRecord } from "../src/client";
import { defaultSiteSettings } from "../src/lib/siteSettings";
import { authenticateAsAdmin } from "./support/auth";

// Captured from the native store after the pinned Conan 2.21.0 fixture really
// uploaded and committed a revision. This is the backend's JSON, not a
// client-side model of what an upload should have emitted.
const publicationAudit: AuditRecord = JSON.parse(
  readFileSync(
    new URL("./fixtures/conan-publication-audit.json", import.meta.url),
    "utf8",
  ),
);
const sessionId = publicationAudit.evidence!.publicationSessionId;
const digest = publicationAudit.evidence!.digest;

for (const width of [1440, 390])
  for (const theme of ["dark", "light"])
    for (const locale of ["zh-CN", "en-US"]) {
      test(`Conan publication audit stays readable at ${width}px in ${theme} ${locale}`, async ({
        page,
      }, testInfo) => {
        const errors: string[] = [];
        page.on("pageerror", (error) => errors.push(error.message));
        page.on("console", (message) => {
          if (message.type() === "error") errors.push(message.text());
        });
        await page.setViewportSize({ width, height: 1000 });
        await page.emulateMedia({ reducedMotion: "reduce" });
        await page.addInitScript(
          ({ theme, locale }) => {
            localStorage.setItem("ag.console.theme", theme);
            localStorage.setItem("ag.console.locale", locale);
          },
          { theme, locale },
        );
        await authenticateAsAdmin(page);
        await page.route("**/api/v2/site-settings", (route) =>
          route.fulfill({ json: defaultSiteSettings }),
        );
        for (const endpoint of ["repositories", "groups"])
          await page.route(`**/api/v2/${endpoint}**`, (route) =>
            route.fulfill({ json: { items: [] } }),
          );
        await page.route("**/api/v2/audits/page**", (route) =>
          route.fulfill({
            json: {
              items: [publicationAudit],
            },
          }),
        );
        await page.goto("/audits");
        const actor = page.getByText(publicationAudit.actor!, {
          exact: true,
        });
        await actor.click();
        const detail = page.locator(".ant-table-expanded-row");
        const session = detail.getByText(sessionId, { exact: true });
        const state = detail.getByText(
          locale === "zh-CN" ? "已提交" : "Committed",
          { exact: true },
        );
        await expect(session).toBeVisible();
        await expect(state).toBeVisible();
        await expect(detail.getByText(digest)).toHaveCount(0);
        await expect(detail.getByText("false", { exact: true })).toHaveCount(0);
        const geometry = await detail.evaluate((row) => {
          const fields = Array.from(row.querySelectorAll(".flex.min-w-0"));
          return {
            fields: fields.map((field) => {
              const label = field.firstElementChild!.getBoundingClientRect();
              const value = field.lastElementChild!.getBoundingClientRect();
              const bounds = field.getBoundingClientRect();
              return {
                fieldLeft: bounds.left,
                labelRight: label.right,
                valueLeft: value.left,
                valueRight: value.right,
                fieldRight: bounds.right,
              };
            }),
            overflow:
              document.documentElement.scrollWidth -
              document.documentElement.clientWidth,
          };
        });
        for (const field of geometry.fields) {
          expect(field.valueLeft - field.labelRight).toBeGreaterThanOrEqual(7);
          expect(field.valueLeft - field.labelRight).toBeLessThanOrEqual(9);
          expect(field.valueRight).toBeLessThanOrEqual(field.fieldRight + 1);
        }
        // Expansion remains readable even after clicking the actor column at
        // the far end of the horizontally scrolled table on a narrow screen.
        for (const field of geometry.fields.slice(0, 2)) {
          expect(field.fieldLeft).toBeGreaterThanOrEqual(0);
          expect(field.fieldRight).toBeLessThanOrEqual(width);
        }
        expect(geometry.overflow).toBeLessThanOrEqual(0);
        const [headerBox, filtersBox, tableBox] = await Promise.all([
          page.locator(".ag-page-header").boundingBox(),
          page.locator(".ag-page-stack > .ag-card").first().boundingBox(),
          page.locator(".ag-page-stack > .ag-card").last().boundingBox(),
        ]);
        const headerGap = filtersBox!.y - (headerBox!.y + headerBox!.height);
        const tableGap = tableBox!.y - (filtersBox!.y + filtersBox!.height);
        expect(headerGap).toBeGreaterThanOrEqual(23);
        expect(headerGap).toBeLessThanOrEqual(25);
        expect(tableGap).toBeGreaterThanOrEqual(15);
        expect(tableGap).toBeLessThanOrEqual(17);
        await session.scrollIntoViewIfNeeded();
        await page.screenshot({
          path: testInfo.outputPath(
            `conan-audit-${locale}-${theme}-${width}.png`,
          ),
          fullPage: true,
        });
        expect(errors).toEqual([]);
      });
    }
