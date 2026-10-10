import { defineConfig } from "@playwright/test";

const port = Number(process.env.PLAYWRIGHT_PORT ?? 4173);
const externalServer = process.env.PLAYWRIGHT_EXTERNAL_SERVER === "1";

export default defineConfig({
  testDir: "./e2e",
  // Visual baselines run fully mocked through playwright.visual.config.ts.
  testIgnore: "visual/**",
  timeout: 30_000,
  use: {
    baseURL: `http://127.0.0.1:${port}`,
    browserName: "chromium",
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
  },
  webServer: externalServer
    ? undefined
    : {
        command: `npm run dev -- --host 127.0.0.1 --port ${port}`,
        port,
        reuseExistingServer: false,
      },
});
