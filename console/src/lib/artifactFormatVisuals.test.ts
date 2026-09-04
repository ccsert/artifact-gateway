import { describe, expect, it } from "vitest";
import {
  artifactFormatVisualizationClass,
  artifactFormatVisualizationSlot,
  artifactFormatVisualizationTone,
} from "./artifactFormatVisuals";

describe("artifact format visualization roles", () => {
  it("keeps protocol identity stable across charts, badges, and catalog marks", () => {
    expect(artifactFormatVisualizationSlot("oci")).toBe(0);
    expect(artifactFormatVisualizationTone("go")).toBe("visualization-5");
    expect(artifactFormatVisualizationClass("apt")).toBe(
      "ag-visualization-tone ag-visualization-tone-8",
    );
  });

  it("does not assign an operational color to an unknown format", () => {
    expect(artifactFormatVisualizationSlot("future")).toBeUndefined();
    expect(artifactFormatVisualizationTone("future")).toBeUndefined();
    expect(artifactFormatVisualizationClass("future")).toBe("");
  });
});
