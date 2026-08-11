import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { PyPIProjectDetail } from "./PyPIProjectDetail";
import { AuthProvider } from "../lib/auth";
import { PreferencesProvider } from "../lib/preferences";

vi.mock("./ArtifactScanStatus", () => ({
  ArtifactScanStatus: () => null,
}));
vi.mock("./ArtifactIntelligencePanel", () => ({
  ArtifactIntelligencePanel: () => null,
}));
vi.mock("./ArtifactQuarantinePanel", () => ({
  ArtifactQuarantinePanel: ({
    repositoryId,
    label,
    digest,
    canManage,
  }: {
    repositoryId?: string;
    label?: string;
    digest?: string;
    canManage?: boolean;
  }) =>
    repositoryId ? (
      <div data-testid="pypi-quarantine-control">
        {label}|{digest}|{String(canManage)}
      </div>
    ) : null,
}));

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
            {
              filename: "gateway_widget-2.0.0.tar.gz",
              url: "../../packages/gateway_widget-2.0.0.tar.gz",
              hashes: { sha256: "c".repeat(64) },
              "_artifact-gateway": {
                version: "2.0.0",
                size: 4096,
                publisher: "next-bot",
                cached: true,
                "created-at": "2026-08-02T00:00:00Z",
                "file-type": "sdist",
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
            repositoryId="repo-1"
            repoName="python"
            project="gateway-widget"
            initialVersion="2.0.0"
            canQuarantine
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
    expect(screen.getByText("gateway_widget-2.0.0.tar.gz")).toBeInTheDocument();
    expect(
      screen.queryByText("gateway_widget-1.0.0-py3-none-any.whl"),
    ).not.toBeInTheDocument();
    expect(screen.getAllByTestId("pypi-quarantine-control")).toHaveLength(2);
    expect(
      screen.getByText(
        `gateway_widget-2.0.0-py3-none-any.whl|sha256:${"b".repeat(64)}|true`,
      ),
    ).toBeInTheDocument();
    expect(
      screen.getByText(
        `gateway_widget-2.0.0.tar.gz|sha256:${"c".repeat(64)}|true`,
      ),
    ).toBeInTheDocument();
  });
});
