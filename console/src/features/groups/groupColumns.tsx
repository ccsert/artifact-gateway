import type { Translate } from "../../lib/preferences";
import type { Repository } from "../../client";
import { MoreOutlined } from "@ant-design/icons";
import { Button, Dropdown } from "antd";
import type { ColumnsType } from "antd/es/table";
import { Link } from "react-router-dom";
import type { Group } from "../../client";
import { Badge, FormatBadge } from "../../components/ui/Badge";
import { CopyableValue } from "../../components/ui/ConsolePrimitives";
import { formatDescriptor, supportsDirectoryBrowse } from "../../formats";

/** A dialog a group row opens; the page renders the one that is active. */
export type GroupAction = "members" | "resolution" | "capacity" | "settings";

/** The group table: name and endpoint, format, access, members and actions. */
export function groupColumns({
  repoName,
  repoById,
  onAction,
  onDelete,
  t,
}: {
  repoName: (repositoryId: string) => string;
  repoById: (repositoryId: string) => Repository | undefined;
  onAction: (action: GroupAction, group: Group) => void;
  onDelete: (group: Group) => void;
  t: Translate;
}): ColumnsType<Group> {
  return [
    {
      title: t("groups.name"),
      dataIndex: "name",
      key: "name",
      width: 240,
      render: (name: string, group) => {
        const endpoint = formatDescriptor(group.format)?.groupEndpoint?.(name);
        return (
          <div className="min-w-0">
            <div className="font-medium text-fg-strong">{name}</div>
            {endpoint && (
              <CopyableValue
                value={endpoint.value}
                label={endpoint.label}
                className="mt-1 max-w-full text-xs text-fg-tertiary"
              />
            )}
          </div>
        );
      },
    },
    {
      title: t("groups.format"),
      dataIndex: "format",
      key: "format",
      width: 100,
      render: (format: Group["format"]) => <FormatBadge format={format} />,
    },
    {
      title: t("groups.access"),
      dataIndex: "anonymousRead",
      key: "anonymousRead",
      width: 140,
      render: (anonymousRead: boolean) => (
        <Badge tone={anonymousRead ? "success" : "neutral"}>
          {anonymousRead ? t("groups.anonymousRead") : t("groups.private")}
        </Badge>
      ),
    },
    {
      title: t("groups.membersConfiguredOrder"),
      // Members take the width the other columns leave.
      key: "members",

      render: (_value, group) => (
        <div className="flex flex-wrap gap-1">
          {[...(group.members ?? [])]
            .sort((a, b) => a.position - b.position)
            .map((member) => {
              const repository = repoById(member.repositoryId);
              return (
                <span
                  key={member.repositoryId}
                  className="rounded-md bg-surface-hover px-2 py-0.5 font-mono text-xs text-fg-secondary"
                  title={member.repositoryId}
                >
                  {member.position + 1}. {repoName(member.repositoryId)} ·{" "}
                  {repository?.type ?? t("groups.unknown")} ·{" "}
                  {repository?.anonymousRead ? "anon" : "private"}
                </span>
              );
            })}
          {(group.members ?? []).length === 0 && (
            <span className="text-xs text-fg-disabled">
              {t("groups.noMembers")}
            </span>
          )}
        </div>
      ),
    },
    {
      title: t("groups.actions"),
      key: "actions",
      fixed: "right",
      width: 150,
      align: "right",
      render: (_value, group) => (
        <div className="flex items-center justify-end gap-1">
          <Button
            size="small"
            type="text"
            onClick={() => onAction("members", group)}
          >
            {t("groups.editMembers")}
          </Button>
          <Dropdown
            trigger={["click"]}
            menu={{
              items: [
                ...(supportsDirectoryBrowse(group.format, "group")
                  ? [
                      {
                        key: "browse",
                        label: (
                          <Link to={`/groups/${group.id}/browse`}>
                            {t("groups.browseDirectory")}
                          </Link>
                        ),
                      },
                    ]
                  : []),
                { key: "resolution", label: t("groups.resolutionOrder") },
                { key: "capacity", label: t("groups.capacity") },
                { key: "settings", label: t("groups.settings") },
                { type: "divider" as const },
                {
                  key: "delete",
                  danger: true,
                  label: t("groups.deleteGroupAction"),
                },
              ],
              onClick: ({ key }) => {
                if (key === "delete") onDelete(group);
                else if (key !== "browse") onAction(key as GroupAction, group);
              },
            }}
          >
            <Button
              size="small"
              type="text"
              icon={<MoreOutlined />}
              data-group-actions={group.id}
              aria-label={t("groups.moreActionsFor", { name: group.name })}
            />
          </Dropdown>
        </div>
      ),
    },
  ];
}
