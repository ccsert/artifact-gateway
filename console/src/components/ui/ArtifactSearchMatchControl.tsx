import { Segmented, Tooltip } from "antd";
import type { ArtifactSearchMatch } from "../../client";
import { usePreferences } from "../../lib/preferences";

export function ArtifactSearchMatchControl({
  value,
  onChange,
}: {
  value: ArtifactSearchMatch;
  onChange: (value: ArtifactSearchMatch) => void;
}) {
  const { t } = usePreferences();
  return (
    <Tooltip
      title={t("ui.containsMatchesLiteralTextWithoutCaseSensitivityShort")}
    >
      <Segmented<ArtifactSearchMatch>
        aria-label={t("ui.searchMatchMode")}
        value={value}
        options={[
          { value: "contains", label: t("ui.contains") },
          { value: "prefix", label: t("ui.prefix") },
        ]}
        onChange={onChange}
      />
    </Tooltip>
  );
}
