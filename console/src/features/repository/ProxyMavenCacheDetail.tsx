import { Badge } from "../../components/ui/Badge";
import { formatBytes, shortDigest } from "../../lib/format";
import { usePreferences } from "../../lib/preferences";
import { mavenUsage } from "../../lib/usage";
import {
  CopyButton,
  RepositorySnippetBlock as SnippetBlock,
} from "./RepositoryUsageGuides";
import type { ArtifactRow, ProxyMavenFile } from "./artifactRow";

function parseMavenCachePath(path: string): {
  groupId: string;
  artifactId: string;
  version: string;
  fileName: string;
  coordinate: string;
} | null {
  const parts = path.split("/").filter(Boolean);
  if (parts.length < 4) return null;
  const fileName = parts.at(-1)!;
  const version = parts.at(-2)!;
  const artifactId = parts.at(-3)!;
  const groupParts = parts.slice(0, -3);
  if (
    groupParts.length === 0 ||
    !fileName.startsWith(`${artifactId}-${version}`)
  )
    return null;
  const groupId = groupParts.join(".");
  return {
    groupId,
    artifactId,
    version,
    fileName,
    coordinate: `${groupId}:${artifactId}:${version}`,
  };
}

export function ProxyMavenCacheDetail({
  repoName,
  meta,
}: {
  repoName: string;
  meta: ArtifactRow;
}) {
  const { text } = usePreferences();
  const parsed = meta.files?.[0]
    ? parseMavenCachePath(meta.files[0].path)
    : null;
  const primary = (meta.files ?? []).filter((file) => !file.sidecar);
  const sidecars = (meta.files ?? []).filter((file) => file.sidecar);
  const renderFile = (file: ProxyMavenFile) => {
    const url = `${window.location.origin}/maven/${repoName}/${file.path}`;
    return (
      <div key={file.path} className="px-3 py-2">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div className="min-w-0">
            <div className="flex items-center gap-2">
              <code
                className="truncate font-mono text-xs text-fg"
                title={file.name}
              >
                {file.name}
              </code>
              {file.sidecar && <Badge tone="neutral">checksum</Badge>}
            </div>
            <div className="mt-1 flex flex-wrap gap-3 text-xs text-fg-tertiary">
              <span>{formatBytes(file.size)}</span>
              {file.contentType && <span>{file.contentType}</span>}
              {file.digest && (
                <span className="font-mono">{shortDigest(file.digest)}</span>
              )}
            </div>
          </div>
          <CopyButton text={url} />
        </div>
        <code className="mt-1 block break-all font-mono text-xs leading-5 text-[var(--ag-content-secondary)]">
          {url}
        </code>
      </div>
    );
  };

  return (
    <div className="space-y-4">
      <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
        <div className="rounded-lg border border-line px-3 py-2">
          <div className="text-xs uppercase tracking-wider text-fg-tertiary">
            {text("主文件大小", "Primary size")}
          </div>
          <div className="mt-0.5 text-xs font-semibold text-fg-strong">
            {formatBytes(meta.size)}
          </div>
        </div>
        <div className="rounded-lg border border-line px-3 py-2">
          <div className="text-xs uppercase tracking-wider text-fg-tertiary">
            {text("文件数", "Files")}
          </div>
          <div className="mt-0.5 text-xs font-semibold text-fg-strong">
            {meta.fileCount ?? meta.files?.length ?? 0}
          </div>
        </div>
        {meta.publisher && (
          <div className="rounded-lg border border-line px-3 py-2">
            <div className="text-xs uppercase tracking-wider text-fg-tertiary">
              {text("成员", "Member")}
            </div>
            <div
              className="mt-0.5 truncate font-mono text-xs text-fg-strong"
              title={meta.publisher}
            >
              {meta.publisher}
            </div>
          </div>
        )}
      </div>

      {parsed && (
        <>
          <div className="rounded-lg border border-line bg-surface-translucent px-3 py-3 font-mono text-xs leading-6 text-fg-secondary">
            <div className="text-fg-tertiary">{parsed.groupId}</div>
            <div className="pl-4 text-fg-secondary">└─ {parsed.artifactId}</div>
            <div className="pl-8 text-[var(--ag-content-secondary)]">
              └─ {parsed.version}
            </div>
            <div className="pl-12 text-fg-tertiary">
              {text("主文件：", "Primary files: ")}
              {meta.primaryFiles?.join(", ") || "—"}
            </div>
          </div>
          <details className="group">
            <summary className="cursor-pointer text-sm font-medium text-[var(--ag-content-primary)] hover:text-[var(--ag-link-hover)]">
              {text("Maven 坐标用法", "Maven coordinate usage")}{" "}
              <span className="text-xs text-fg-disabled group-open:hidden">
                {text("（展开）", "(expand)")}
              </span>
            </summary>
            <div className="mt-2 grid gap-2 lg:grid-cols-3">
              {mavenUsage(repoName, parsed.coordinate).map((snippet) => (
                <SnippetBlock
                  key={snippet.label}
                  label={snippet.label}
                  code={snippet.code}
                />
              ))}
            </div>
          </details>
        </>
      )}

      <div className="rounded-lg border border-line bg-surface-translucent">
        <div className="border-b border-line px-3 py-2 text-xs uppercase tracking-wider text-fg-tertiary">
          {text("文件明细", "File details")}
        </div>
        <div className="divide-y divide-line">
          {primary.map(renderFile)}
          {sidecars.length > 0 && (
            <details className="group">
              <summary className="cursor-pointer px-3 py-2 text-xs text-[var(--ag-content-tertiary)] hover:text-[var(--ag-content-secondary)]">
                {text(
                  `校验 / 签名文件（${sidecars.length}）`,
                  `Checksums / signatures (${sidecars.length})`,
                )}
              </summary>
              <div className="divide-y divide-line border-t border-line">
                {sidecars.map(renderFile)}
              </div>
            </details>
          )}
        </div>
      </div>
    </div>
  );
}
