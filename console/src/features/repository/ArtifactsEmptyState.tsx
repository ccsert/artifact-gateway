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
  const { t } = usePreferences();
  const hint =
    repo.type === "hosted" || repo.type === "proxy"
      ? formatDescriptor(repo.format)?.emptyHint[repo.type]
      : undefined;
  return (
    <EmptyState
      title={
        searching
          ? t("repository.noMatchingArtifacts")
          : t("repository.noArtifacts")
      }
      hint={
        searching
          ? t("repository.tryADifferentSearchTerm")
          : hint
            ? t(hint)
            : t("repository.pushArtifactsWithTheMatchingClient", {
                format: repo.format,
              })
      }
    />
  );
}
