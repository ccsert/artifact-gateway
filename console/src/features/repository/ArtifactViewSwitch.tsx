import { FolderOutlined, UnorderedListOutlined } from "@ant-design/icons";
import { Segmented } from "antd";
import { usePreferences } from "../../lib/preferences";

export type ArtifactView = "directory" | "list";

/** Switches a repository's artifacts between the directory tree and the list. */
export function ArtifactViewSwitch({
  value,
  onChange,
}: {
  value: ArtifactView;
  onChange: (value: ArtifactView) => void;
}) {
  const { text } = usePreferences();
  return (
    <Segmented<ArtifactView>
      aria-label={text("制品浏览视图", "Artifact browse view")}
      options={[
        {
          value: "directory",
          label: text("目录", "Directory"),
          icon: <FolderOutlined />,
        },
        {
          value: "list",
          label: text("列表", "List"),
          icon: <UnorderedListOutlined />,
        },
      ]}
      value={value}
      onChange={onChange}
    />
  );
}
