import { defineConfig } from "@playwright/test";

const port = Number(process.env.PLAYWRIGHT_PORT ?? 4173);

export default defineConfig({
  testDir: "./e2e",
  timeout: 30_000,
  use: {
    baseURL: `http://127.0.0.1:${port}`,
    browserName: "chromium",
  },
  webServer: {
    command: `npm run dev -- --host 127.0.0.1 --port ${port}`,
    port,
    reuseExistingServer: false,
  },
});
