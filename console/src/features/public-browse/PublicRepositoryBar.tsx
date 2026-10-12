import type { FormEvent } from "react";
import { ArrowLeftOutlined } from "@ant-design/icons";
import type { ArtifactSearchMatch } from "../../client";
import { ArtifactSearchMatchControl } from "../../components/ui/ArtifactSearchMatchControl";
import { Button, Input } from "antd";
import { FormatBadge } from "../../components/ui/Badge";
import { Card } from "../../components/ui/Layout";
import { usePreferences } from "../../lib/preferences";
import type { PublicRepository } from "./types";
import { formatDescriptor } from "../../formats";

/** Inside a repository: the way back to the catalog and the artifact search. */
export function PublicRepositoryBar({
  selectedRepository,
  queryDraft,
  match,
  onMatchChange,
  onQueryDraftChange,
  onSubmit,
  onBack,
}: {
  selectedRepository: PublicRepository | undefined;
  queryDraft: string;
  match: ArtifactSearchMatch;
  onMatchChange: (value: ArtifactSearchMatch) => void;
  onQueryDraftChange: (value: string) => void;
  onSubmit: (event: FormEvent) => void;
  onBack: () => void;
}) {
  const { t } = usePreferences();
  return (
    <Card bodyClassName="p-5 sm:p-6">
      <div className="flex flex-wrap items-center gap-2 text-sm text-fg-secondary">
        <Button
          type="text"
          size="small"
          icon={<ArrowLeftOutlined />}
          onClick={onBack}
        >
          {t("publicBrowse.publicRepositories")}
        </Button>
        <span className="text-fg-disabled">/</span>
        <span className="font-medium text-fg-strong">
          {selectedRepository?.name ?? t("publicBrowse.unknownRepository")}
        </span>
        {selectedRepository && (
          <FormatBadge format={selectedRepository.format} />
        )}
      </div>
      {formatDescriptor(selectedRepository?.format) && (
        <form
          onSubmit={onSubmit}
          className="mt-5 flex flex-wrap items-center gap-2"
        >
          <ArtifactSearchMatchControl value={match} onChange={onMatchChange} />
          <Input
            className="min-w-0 flex-1 font-mono text-xs"
            placeholder={t(
              match === "contains"
                ? "publicBrowse.coordinatePathOrNameKeywordOptional"
                : "publicBrowse.coordinatePathOrNamePrefixOptional",
            )}
            value={queryDraft}
            onChange={(event) => onQueryDraftChange(event.target.value)}
          />
          <Button
            type="primary"
            htmlType="submit"
            className="shrink-0 whitespace-nowrap"
          >
            {t("publicBrowse.search")}
          </Button>
        </form>
      )}
    </Card>
  );
}
