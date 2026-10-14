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
  const { t } = usePreferences();
  return (
    <Segmented<ArtifactView>
      aria-label={t("repository.artifactBrowseView")}
      options={[
        {
          value: "directory",
          label: t("repository.directory"),
          icon: <FolderOutlined />,
        },
        {
          value: "list",
          label: t("repository.list"),
          icon: <UnorderedListOutlined />,
        },
      ]}
      value={value}
      onChange={onChange}
    />
  );
}
