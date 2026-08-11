import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { getArtifactIntelligence } from "../client";
import type { ArtifactIntelligence } from "../client";
import { PreferencesProvider } from "../lib/preferences";
import { ArtifactIntelligencePanel } from "./ArtifactIntelligencePanel";

vi.mock("../client", () => ({ getArtifactIntelligence: vi.fn() }));

const mockGetArtifactIntelligence = vi.mocked(getArtifactIntelligence);
const digest = `sha256:${"a".repeat(64)}`;

function intelligence(
  vulnerability: ArtifactIntelligence["vulnerability"],
): ArtifactIntelligence {
  return {
    repositoryId: "repo-1",
    format: "raw",
    coordinate: "releases/widget.bin",
    digest,
    signatures: [],
    sboms: [],
    licenses: [],
    vulnerability,
    version: "1",
    createdAt: "2026-08-11T08:00:00Z",
    updatedAt: "2026-08-11T08:00:00Z",
    updatedBy: "scanner:grype",
  };
}

function renderPanel() {
  return render(
    <PreferencesProvider>
      <ArtifactIntelligencePanel
        repositoryId="repo-1"
        format="raw"
        coordinate="releases/widget.bin"
        digest={digest}
      />
    </PreferencesProvider>,
  );
}

afterEach(() => {
  cleanup();
  localStorage.clear();
  vi.clearAllMocks();
});

describe("ArtifactIntelligencePanel", () => {
  it("does not present a failed scan as clean", async () => {
    mockGetArtifactIntelligence.mockResolvedValue({
      data: intelligence({
        scanner: "grype",
        status: "error",
        critical: 0,
        high: 0,
        medium: 0,
        low: 0,
        unknown: 0,
      }),
    } as never);

    renderPanel();

    expect(await screen.findByText("扫描失败")).toBeInTheDocument();
    expect(screen.getByText("扫描未成功，请检查任务详情")).toBeInTheDocument();
    expect(screen.queryByText("未发现问题")).not.toBeInTheDocument();
  });

  it("includes unknown findings in an affected summary", async () => {
    mockGetArtifactIntelligence.mockResolvedValue({
      data: intelligence({
        scanner: "grype",
        status: "affected",
        critical: 0,
        high: 0,
        medium: 0,
        low: 0,
        unknown: 2,
      }),
    } as never);

    renderPanel();

    expect(await screen.findByText("受影响")).toBeInTheDocument();
    expect(screen.getByText("0 C · 0 H · 0 M · 0 L · 2 U")).toBeInTheDocument();
  });
});
