import type { Repository } from "../../client";
import { EmptyState } from "../../components/ui/Feedback";
import { formatDescriptor } from "../../formats";
import { usePreferences } from "../../lib/preferences";

/**
 * An empty artifact list: no match for a search, or what this repository's
 * format and type need before content appears.
 */
export function ArtifactsEmptyState({
  searching,
  repo,
}: {
  searching: boolean;
  repo: Repository;
}) {
  const { text } = usePreferences();
  const hint =
    repo.type === "hosted" || repo.type === "proxy"
      ? formatDescriptor(repo.format)?.emptyHint[repo.type]
      : undefined;
  return (
    <EmptyState
      title={
        searching
          ? text("没有匹配的制品", "No matching artifacts")
          : text("暂无制品", "No artifacts")
      }
      hint={
        searching
          ? text("换个关键词试试", "Try a different search term")
          : hint
            ? text(...hint)
            : text(
                `通过 ${repo.format} 客户端推送制品后会显示在这里`,
                "Push artifacts with the matching client to display them here.",
              )
      }
    />
  );
}
