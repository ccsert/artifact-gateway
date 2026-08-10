import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import type { ArtifactIntelligenceSummary } from "../client";
import { PreferencesProvider } from "../lib/preferences";
import { ArtifactSecurityBadge } from "./ArtifactSecurityBadge";

const text = (zh: string, en: string) => zh || en;
const renderBadge = (summary?: ArtifactIntelligenceSummary) =>
  render(
    <PreferencesProvider>
      <ArtifactSecurityBadge summary={summary} text={text} />
    </PreferencesProvider>,
  );

afterEach(cleanup);

describe("ArtifactSecurityBadge", () => {
  it("renders the stable status labels and finding count", () => {
    renderBadge({
      signatureCount: 1,
      sbomCount: 1,
      licenseCount: 1,
      vulnerabilityStatus: "affected",
      critical: 2,
      high: 1,
    });
    expect(screen.getByText("有风险 · 3")).toBeInTheDocument();
  });

  it.each([
    [undefined, "未扫描"],
    [{ signatureCount: 0, sbomCount: 1, licenseCount: 0 }, "1 项证据"],
    [
      {
        signatureCount: 0,
        sbomCount: 0,
        licenseCount: 0,
        vulnerabilityStatus: "clean",
      },
      "通过",
    ],
    [
      {
        signatureCount: 0,
        sbomCount: 0,
        licenseCount: 0,
        vulnerabilityStatus: "error",
      },
      "扫描错误",
    ],
  ])("renders %s as %s", (summary, label) => {
    renderBadge(summary as ArtifactIntelligenceSummary | undefined);
    expect(screen.getByText(label)).toBeInTheDocument();
  });
});
