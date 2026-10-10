import { defineConfig } from "@playwright/test";
import baseConfig from "./playwright.config";

// Fully mocked screenshots: no Gateway is required. Baselines are only valid
// for the Linux Playwright container (see `make console-visual`).
export default defineConfig({
  ...baseConfig,
  testDir: "./e2e/visual",
  testMatch: "*.visual.spec.ts",
  testIgnore: [],
  snapshotPathTemplate: "{testDir}/__screenshots__/{arg}{ext}",
  expect: {
    toHaveScreenshot: {
      animations: "disabled",
      caret: "hide",
      maxDiffPixelRatio: 0.002,
    },
  },
  use: {
    ...baseConfig.use,
    viewport: { width: 1440, height: 900 },
    locale: "zh-CN",
    timezoneId: "Asia/Shanghai",
    reducedMotion: "reduce",
  },
});
