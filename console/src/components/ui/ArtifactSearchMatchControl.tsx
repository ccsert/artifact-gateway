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
  const { text } = usePreferences();
  return (
    <Tooltip
      title={text(
        "包含为忽略大小写的字面匹配。短词可能较慢，搜索超过 5 秒会终止；可补充关键词或选择前缀。",
        "Contains matches literal text without case sensitivity. Short queries may be slower; searches stop after 5 seconds. Add more text or select Prefix.",
      )}
    >
      <Segmented<ArtifactSearchMatch>
        aria-label={text("搜索匹配方式", "Search match mode")}
        value={value}
        options={[
          { value: "contains", label: text("包含", "Contains") },
          { value: "prefix", label: text("前缀", "Prefix") },
        ]}
        onChange={onChange}
      />
    </Tooltip>
  );
}
