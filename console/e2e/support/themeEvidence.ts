import type { Page, TestInfo } from "@playwright/test";

/** Record only UI theme state; never capture credentials or API payloads. */
export async function recordThemeEvidence(page: Page) {
  await page.addInitScript(() => {
    const records: ThemeEvidence[] = [];
    window.__themeEvidence = records;
    const record = (event: string) => {
      const root = document.documentElement;
      let preferenceThemeId: string | null = null;
      try {
        preferenceThemeId = localStorage.getItem("ag.console.theme.id");
      } catch {
        // Theme diagnostics also work when browser storage is unavailable.
      }
      records.push({
        at: performance.now(),
        event,
        themeId: root?.dataset.themeId ?? null,
        theme: root?.dataset.theme ?? null,
        transition: root?.dataset.themeTransition ?? null,
        preferenceThemeId,
        visibility: document.visibilityState,
      });
      if (records.length > 100) records.shift();
    };
    for (const event of ["pointerdown", "pointerup", "click"]) {
      document.addEventListener(
        event,
        (input) => {
          if (
            input.target instanceof Element &&
            input.target.closest(".ag-theme-toggle, .ag-theme-dropdown")
          ) {
            record(event);
          }
        },
        true,
      );
    }
    new MutationObserver(() => record("theme-attribute")).observe(document, {
      attributes: true,
      subtree: true,
      attributeFilter: ["data-theme-id", "data-theme", "data-theme-transition"],
    });
    const start = document.startViewTransition?.bind(document);
    if (!start) return;
    document.startViewTransition = (options) => {
      const update = typeof options === "function" ? options : options?.update;
      record("capture-request");
      const callback = async () => {
        record("update-start");
        try {
          await update?.();
          record("update-end");
        } catch (error) {
          record("update-error");
          throw error;
        }
      };
      const transition = start(
        typeof options === "object"
          ? { ...options, update: callback }
          : callback,
      );
      void transition.finished.then(
        () => record("capture-finished"),
        () => record("capture-error"),
      );
      return transition;
    };
  });
}

export async function attachThemeEvidence(page: Page, testInfo: TestInfo) {
  if (testInfo.status === testInfo.expectedStatus || page.isClosed()) return;
  const records = await page.evaluate(() => window.__themeEvidence ?? []);
  await testInfo.attach("theme-events.json", {
    body: JSON.stringify(records, null, 2),
    contentType: "application/json",
  });
}

interface ThemeEvidence {
  at: number;
  event: string;
  themeId: string | null;
  theme: string | null;
  transition: string | null;
  preferenceThemeId: string | null;
  visibility: DocumentVisibilityState;
}

declare global {
  interface Window {
    __themeEvidence?: ThemeEvidence[];
  }
}
