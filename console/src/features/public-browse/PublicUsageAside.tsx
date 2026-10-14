import { Card } from "../../components/ui/Layout";
import { UsageSnippetBlock } from "../../components/ui/PublicBrowsePrimitives";
import { usePreferences } from "../../lib/preferences";
import type { UsageSnippet } from "../../lib/usage";
import { publicRepositoryUsage } from "../../lib/publicRepositoryUsage";
import { publicUsageLabel } from "./publicFormats";
import type { PublicRepository } from "./types";

/** How to register the selected repository with its native client. */
export function PublicUsageAside({
  selectedRepository,
  copiedCoordinate,
  onCopyUsage,
}: {
  selectedRepository: PublicRepository;
  copiedCoordinate: string | null;
  onCopyUsage: (snippet: UsageSnippet) => unknown;
}) {
  const { t } = usePreferences();
  const usageLabel = publicUsageLabel(selectedRepository.format, t);
  const globalUsage = publicRepositoryUsage({
    format: selectedRepository.format,
    repositoryName: selectedRepository.name,
    origin: window.location.origin,
    host: window.location.host,
    t,
  });
  const copyUsage = onCopyUsage;
  return (
    <aside className="lg:sticky lg:top-24">
      <Card className="overflow-hidden">
        <div className="border-b border-line px-4 py-3">
          <div className="text-sm font-medium text-fg-strong">{usageLabel}</div>
          <div className="mt-1 text-xs text-fg-tertiary">
            {t("publicBrowse.forRepository")}{" "}
            <span className="font-mono text-fg-secondary">
              {selectedRepository.name}
            </span>
          </div>
        </div>
        <div className="space-y-3 p-3">
          {globalUsage.map((snippet) => (
            <UsageSnippetBlock
              key={snippet.label}
              snippet={snippet}
              copied={copiedCoordinate === snippet.code}
              onCopy={() => void copyUsage(snippet)}
              compact
            />
          ))}
          <div className="border-t border-line pt-3 text-xs leading-5 text-fg-disabled">
            {t("publicBrowse.usageAccessHint")}
          </div>
        </div>
      </Card>
    </aside>
  );
}
