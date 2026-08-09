import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { PyPIProjectDetail } from "./PyPIProjectDetail";
import { AuthProvider } from "../lib/auth";
import { PreferencesProvider } from "../lib/preferences";

afterEach(() => {
  cleanup();
  localStorage.clear();
  vi.restoreAllMocks();
});

describe("PyPIProjectDetail", () => {
  it("selects one searchable version and only renders its distribution files", async () => {
    vi.spyOn(globalThis, "fetch").mockImplementation(async (input) => {
      if (input === "/auth/session") return new Response(null, { status: 401 });
      return new Response(
        JSON.stringify({
          name: "gateway-widget",
          files: [
            {
              filename: "gateway_widget-1.0.0-py3-none-any.whl",
              url: "../../packages/gateway_widget-1.0.0-py3-none-any.whl",
              hashes: { sha256: "a".repeat(64) },
              "_artifact-gateway": {
                version: "1.0.0",
                size: 1024,
                publisher: "release-bot",
                cached: true,
                "created-at": "2026-08-01T00:00:00Z",
                "file-type": "bdist_wheel",
              },
            },
            {
              filename: "gateway_widget-2.0.0-py3-none-any.whl",
              url: "../../packages/gateway_widget-2.0.0-py3-none-any.whl",
              hashes: { sha256: "b".repeat(64) },
              "requires-python": ">=3.11",
              "_artifact-gateway": {
                version: "2.0.0",
                size: 2048,
                publisher: "next-bot",
                cached: true,
                "created-at": "2026-08-02T00:00:00Z",
                "file-type": "bdist_wheel",
              },
            },
          ],
        }),
        {
          status: 200,
          headers: { "Content-Type": "application/vnd.pypi.simple.v1+json" },
        },
      );
    });

    render(
      <AuthProvider>
        <PreferencesProvider>
          <PyPIProjectDetail
            repoName="python"
            project="gateway-widget"
            initialVersion="2.0.0"
          />
        </PreferencesProvider>
      </AuthProvider>,
    );

    expect(
      await screen.findByText("gateway-widget==2.0.0"),
    ).toBeInTheDocument();
    expect(
      screen.getByText("gateway_widget-2.0.0-py3-none-any.whl"),
    ).toBeInTheDocument();
    expect(
      screen.queryByText("gateway_widget-1.0.0-py3-none-any.whl"),
    ).not.toBeInTheDocument();
  });
});
