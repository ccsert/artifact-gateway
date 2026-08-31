const visualizationSlots = {
  oci: 0,
  maven: 1,
  npm: 2,
  pypi: 3,
  go: 4,
  conan: 5,
  raw: 6,
  apt: 7,
} as const;

type VisualizationSlot =
  (typeof visualizationSlots)[keyof typeof visualizationSlots];
export type ArtifactFormatVisualizationTone =
  `visualization-${1 | 2 | 3 | 4 | 5 | 6 | 7 | 8}`;

export function artifactFormatVisualizationSlot(
  format: string,
): VisualizationSlot | undefined {
  return visualizationSlots[format as keyof typeof visualizationSlots];
}

export function artifactFormatVisualizationTone(
  format: string,
): ArtifactFormatVisualizationTone | undefined {
  const slot = artifactFormatVisualizationSlot(format);
  return slot === undefined
    ? undefined
    : (`visualization-${slot + 1}` as ArtifactFormatVisualizationTone);
}

export function artifactFormatVisualizationClass(format: string): string {
  const slot = artifactFormatVisualizationSlot(format);
  return slot === undefined
    ? ""
    : `ag-visualization-tone ag-visualization-tone-${slot + 1}`;
}
