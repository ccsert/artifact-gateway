import { Card } from "../../components/ui/Layout";
import { UsageSnippetBlock } from "../../components/ui/PublicBrowsePrimitives";
import { usePreferences } from "../../lib/preferences";
import type { UsageSnippet } from "../../lib/usage";
import type { PublicRepository } from "./types";

/** How to register the selected repository with its native client. */
export function PublicUsageAside({
  selectedRepository,
  usageLabel,
  globalUsage,
  copiedCoordinate,
  onCopyUsage,
}: {
  selectedRepository: PublicRepository;
  usageLabel: string;
  globalUsage: UsageSnippet[];
  copiedCoordinate: string | null;
  onCopyUsage: (snippet: UsageSnippet) => unknown;
}) {
  const { text } = usePreferences();
  const copyUsage = onCopyUsage;
  return (
    <aside className="lg:sticky lg:top-24">
      <Card className="overflow-hidden">
        <div className="border-b border-line px-4 py-3">
          <div className="text-sm font-medium text-fg-strong">{usageLabel}</div>
          <div className="mt-1 text-xs text-fg-tertiary">
            {text("适用于仓库", "For repository")}{" "}
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
            {text(
              "匿名浏览无需 Token；推送、私有仓库和管理操作仍需登录。",
              "Anonymous browsing needs no token. Publishing, private repositories, and management still require sign-in.",
            )}
          </div>
        </div>
      </Card>
    </aside>
  );
}
