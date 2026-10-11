import type { FormEvent } from "react";
import { ArrowLeftOutlined } from "@ant-design/icons";
import { Button, Input } from "antd";
import { FormatBadge } from "../../components/ui/Badge";
import { Card } from "../../components/ui/Layout";
import { usePreferences } from "../../lib/preferences";
import type { PublicRepository } from "./types";

/** Inside a repository: the way back to the catalog and the artifact search. */
export function PublicRepositoryBar({
  selectedRepository,
  queryDraft,
  onQueryDraftChange,
  onSubmit,
  onBack,
}: {
  selectedRepository: PublicRepository | undefined;
  queryDraft: string;
  onQueryDraftChange: (value: string) => void;
  onSubmit: (event: FormEvent) => void;
  onBack: () => void;
}) {
  const { text } = usePreferences();
  return (
    <Card bodyClassName="p-5 sm:p-6">
      <div className="flex flex-wrap items-center gap-2 text-sm text-fg-secondary">
        <Button
          type="text"
          size="small"
          icon={<ArrowLeftOutlined />}
          onClick={onBack}
        >
          {text("公开仓库", "Public repositories")}
        </Button>
        <span className="text-fg-disabled">/</span>
        <span className="font-medium text-fg-strong">
          {selectedRepository?.name ?? text("未知仓库", "Unknown repository")}
        </span>
        {selectedRepository && (
          <FormatBadge format={selectedRepository.format} />
        )}
      </div>
      <form onSubmit={onSubmit} className="mt-5 flex items-center gap-2">
        <Input
          className="min-w-0 flex-1 font-mono text-xs"
          placeholder={text(
            "坐标、路径或名称前缀（可选）",
            "Coordinate, path, or name prefix (optional)",
          )}
          value={queryDraft}
          onChange={(event) => onQueryDraftChange(event.target.value)}
        />
        <Button
          type="primary"
          htmlType="submit"
          className="shrink-0 whitespace-nowrap"
        >
          {text("搜索", "Search")}
        </Button>
      </form>
    </Card>
  );
}
