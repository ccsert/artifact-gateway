import { readFileSync } from "node:fs";
import { defineConfig } from "@playwright/test";
import baseConfig from "./playwright.config";

const { version } = JSON.parse(
  readFileSync(new URL("./package.json", import.meta.url), "utf8"),
);
const port = Number(process.env.PLAYWRIGHT_PORT ?? 4173);

// Exercise the real built bundle, rather than the development server's dev
// identity. The expected value is independent of the bundled code under test.
export default defineConfig({
  ...baseConfig,
  testMatch: "console-build-version.spec.ts",
  metadata: { consoleBuildVersion: version },
  webServer:
    process.env.PLAYWRIGHT_EXTERNAL_SERVER === "1"
      ? undefined
      : {
          command: `npm run preview -- --host 127.0.0.1 --port ${port}`,
          port,
          reuseExistingServer: false,
        },
});
