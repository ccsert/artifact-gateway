import react from "@vitejs/plugin-react";
import { defineConfig } from "vitest/config";

export default defineConfig({
  plugins: [react()],
  test: {
    environment: "jsdom",
    setupFiles: "./src/test/setup.ts",
    include: ["src/**/*.{test,spec}.{ts,tsx}"],
    exclude: ["e2e/**", "node_modules/**"],
    coverage: {
      provider: "v8",
      reporter: ["text-summary", "json-summary"],
      include: ["src/**/*.{ts,tsx}"],
      exclude: ["src/client/**", "src/test/**"],
      thresholds: {
        // Track the whole hand-written Console instead of reporting a high
        // percentage for a small allowlist. Raise these non-regression floors
        // as public-boundary tests cover more pages and workflows.
        lines: 40,
        functions: 53,
        statements: 40,
        branches: 65,
        "src/app/Layout.tsx": {
          lines: 90,
          functions: 60,
          statements: 90,
          branches: 80,
        },
        "src/lib/publicBrowseModel.ts": {
          lines: 70,
          functions: 70,
          statements: 70,
          branches: 60,
        },
        "src/components/PublicBrowsePrimitives.tsx": {
          lines: 70,
          functions: 70,
          statements: 70,
          branches: 60,
        },
        "src/components/RuntimeNodesPanel.tsx": {
          lines: 70,
          functions: 70,
          statements: 70,
          branches: 60,
        },
      },
    },
  },
});
