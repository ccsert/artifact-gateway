import type { Repository } from "../../client";
import { FolderOpenOutlined, DeleteOutlined } from "@ant-design/icons";
import { Button } from "antd";
import type { ColumnsType } from "antd/es/table";
import type { Group } from "../../client";
import { Badge, FormatBadge } from "../../components/ui/Badge";
import { CopyableValue } from "../../components/ui/ConsolePrimitives";
import { formatDescriptor, supportsDirectoryBrowse } from "../../formats";
import { GroupResolutionDialog } from "./pages/GroupResolutionDialog";

import { CapacityDialog } from "./GroupCapacityDialog";
import { MembersDialog } from "./GroupMembersDialog";
import { RenameGroupDialog } from "./RenameGroupDialog";

type Localize = (chinese: string, english: string) => string;

/** The group table: name and endpoint, format, access, members and actions. */
export function groupColumns({
  repos,
  repoName,
  repoById,
  onSaved,
  onDelete,
  text,
}: {
  repos: Repository[];
  repoName: (repositoryId: string) => string;
  repoById: (repositoryId: string) => Repository | undefined;
  onSaved: () => void;
  onDelete: (group: Group) => void;
  text: Localize;
}): ColumnsType<Group> {
  return [
    {
      title: text("名称", "Name"),
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
      title: text("格式", "Format"),
      dataIndex: "format",
      key: "format",
      width: 100,
      render: (format: Group["format"]) => <FormatBadge format={format} />,
    },
    {
      title: text("访问", "Access"),
      dataIndex: "anonymousRead",
      key: "anonymousRead",
      width: 140,
      render: (anonymousRead: boolean) => (
        <Badge tone={anonymousRead ? "success" : "neutral"}>
          {anonymousRead
            ? text("匿名可读", "Anonymous read")
            : text("私有", "Private")}
        </Badge>
      ),
    },
    {
      title: text("成员（配置顺序）", "Members (configured order)"),
      key: "members",
      width: 440,
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
                  {repository?.type ?? text("未知", "unknown")} ·{" "}
                  {repository?.anonymousRead ? "anon" : "private"}
                </span>
              );
            })}
          {(group.members ?? []).length === 0 && (
            <span className="text-xs text-fg-disabled">
              {text("无成员", "No members")}
            </span>
          )}
        </div>
      ),
    },
    {
      title: text("版本", "Version"),
      dataIndex: "version",
      key: "version",
      width: 90,
      render: (version: string) => (
        <span className="font-mono text-xs text-fg-tertiary">v{version}</span>
      ),
    },
    {
      title: text("操作", "Actions"),
      key: "actions",
      width: 280,
      align: "right",
      render: (_value, group) => (
        <div className="flex flex-wrap items-center justify-end gap-2">
          {supportsDirectoryBrowse(group.format, "group") && (
            <Button
              size="small"
              href={`/groups/${group.id}/browse`}
              icon={<FolderOpenOutlined />}
            >
              {text("浏览目录", "Browse directory")}
            </Button>
          )}
          <GroupResolutionDialog group={group} />
          <RenameGroupDialog group={group} onSaved={onSaved} />
          <MembersDialog group={group} repos={repos} onSaved={onSaved} />
          <CapacityDialog group={group} />
          <Button
            type="text"
            size="small"
            danger
            icon={<DeleteOutlined />}
            aria-label={text(
              `删除分组 ${group.name}`,
              `Delete group ${group.name}`,
            )}
            onClick={() => onDelete(group)}
          />
        </div>
      ),
    },
  ];
}
