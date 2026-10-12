import { type ReactNode } from "react";
import { ArrowUpOutlined, FolderOpenOutlined } from "@ant-design/icons";
import { Button } from "antd";
import type { BrowseNode, BrowseSource, Repository } from "../../client";
import { Badge } from "../../components/ui/Badge";
import { formatBytes, formatDate, shortDigest } from "../../lib/format";
import { usePreferences } from "../../lib/preferences";
import { artifactCoordinateForDisplay, formatDescriptor } from "../../formats";
import { CopyButton } from "./RepositoryUsageGuides";

import { nodeIcon, nodeKindLabel } from "./browseTreeModel";

/** The selected tree node: its identity, evidence and sources. */
export function BrowseNodePanel({
  repo,
  selected,
  onOpenInList,
}: {
  repo: Pick<Repository, "id" | "name" | "format"> & {
    type?: Repository["type"] | "group";
  };
  selected: BrowseNode | null;
  onOpenInList: (node: BrowseNode) => void;
}) {
  const { text } = usePreferences();
  return (
    <aside
      className="ag-repository-tree-inspector"
      aria-label={text("节点详情", "Node details")}
    >
      {selected ? (
        <div className="ag-repository-tree-inspector-content">
          <div className="ag-repository-tree-inspector-heading">
            <div className="ag-repository-tree-inspector-title">
              {nodeIcon(selected.kind, true)}
              <strong>{selected.name}</strong>
            </div>
            <Badge tone="neutral">{nodeKindLabel(selected.kind, text)}</Badge>
          </div>
          {selected.kind === "asset" &&
            selected.coordinate &&
            repo.type !== "group" && (
              <Button
                className="ag-repository-tree-open"
                onClick={() => onOpenInList(selected)}
              >
                {text("在列表中查看", "Open in list")}
              </Button>
            )}
          {(selected.coordinate || selected.path) && (
            <div className="ag-repository-tree-field">
              <span>{text("规范位置", "Canonical location")}</span>
              <div>
                <code>
                  {artifactCoordinateForDisplay(
                    repo.format,
                    selected.coordinate ?? selected.path ?? "",
                  )}
                </code>
                <CopyButton text={selected.coordinate ?? selected.path ?? ""} />
              </div>
            </div>
          )}
          {selected.sources && (
            <section
              className="ag-repository-tree-sources"
              aria-label={text("本地来源", "Local sources")}
            >
              <h4>
                {text("本地来源", "Local sources")}{" "}
                <span>{selected.sources.length}</span>
              </h4>
              <p>
                {text(
                  "按可见成员的候选顺序排列，不代表本次下载命中。",
                  "Ordered by visible member priority, not an actual download result.",
                )}
              </p>
              {selected.kind === "asset" &&
                new Set(
                  selected.sources
                    .map((source) => source.digest)
                    .filter(Boolean),
                ).size > 1 && (
                  <p>
                    <Badge tone="warning">
                      {text(
                        "同路径存在不同内容",
                        "Different content at this path",
                      )}
                    </Badge>
                  </p>
                )}
              <ol>
                {selected.sources.map((source) => (
                  <SourceRow
                    key={source.repositoryId}
                    source={source}
                    groupName={repo.name}
                  />
                ))}
              </ol>
            </section>
          )}
          <dl className="ag-repository-tree-evidence">
            {selected.sourceRepositoryName && (
              <Evidence
                label={text("来源仓库", "Source repository")}
                value={selected.sourceRepositoryName}
              />
            )}
            {selected.cacheState === "cached" && (
              <Evidence
                label={text("缓存状态", "Cache state")}
                value={text("已缓存", "Cached")}
              />
            )}
            {selected.buildNumber !== undefined && (
              <Evidence
                label={text("构建号", "Build number")}
                value={selected.buildNumber}
              />
            )}
            {selected.size !== undefined && (
              <Evidence
                label={text("大小", "Size")}
                value={formatBytes(selected.size)}
              />
            )}
            {selected.contentType && (
              <Evidence
                label={text("内容类型", "Content type")}
                value={selected.contentType}
              />
            )}
            {(selected.cachedAt || selected.createdAt) && (
              <Evidence
                label={
                  selected.cachedAt
                    ? text("缓存更新时间", "Cache updated")
                    : text("更新时间", "Updated")
                }
                value={formatDate(selected.cachedAt ?? selected.createdAt!)}
              />
            )}
          </dl>
          {formatDescriptor(repo.format)?.separateAssetPath &&
            selected.kind === "asset" &&
            selected.path && (
              <div className="ag-repository-tree-field">
                <span>{text("文件路径", "Asset path")}</span>
                <div>
                  <code>{selected.path}</code>
                  <CopyButton text={selected.path} />
                </div>
              </div>
            )}
          {selected.digest && (
            <div className="ag-repository-tree-field">
              <span>SHA-256</span>
              <div>
                <code title={selected.digest}>
                  {shortDigest(selected.digest)}
                </code>
                <CopyButton text={selected.digest} />
              </div>
            </div>
          )}
        </div>
      ) : (
        <div className="ag-repository-tree-inspector-empty">
          <FolderOpenOutlined aria-hidden />
          <strong>
            {text("从目录中选择一项", "Select an item in the directory")}
          </strong>
          <p>
            {text(
              "在这里查看完整名称、来源与校验信息。",
              "Inspect its full name, sources and verification details here.",
            )}
          </p>
          <span>
            <ArrowUpOutlined aria-hidden />{" "}
            {text(
              "方向键浏览 · Enter 选择",
              "Arrow keys to browse · Enter to select",
            )}
          </span>
        </div>
      )}
    </aside>
  );
}

function Evidence({ label, value }: { label: string; value: ReactNode }) {
  return (
    <div>
      <dt>{label}</dt>
      <dd>{value}</dd>
    </div>
  );
}

function browseSourceHref(source: BrowseSource, groupName: string) {
  const query = new URLSearchParams();
  // A Group-only cache index is not present in the member's own cache listing.
  if (source.coordinate && source.cacheRepositoryName !== groupName) {
    query.set("artifact", source.coordinate);
    if (source.path) query.set("asset", source.path);
    if (source.buildNumber) query.set("build", String(source.buildNumber));
    if (source.digest) query.set("digest", source.digest);
  }
  return `/repositories/${source.repositoryId}${query.size ? `?${query}` : ""}`;
}

function SourceRow({
  source,
  groupName,
}: {
  source: BrowseSource;
  groupName: string;
}) {
  const { text } = usePreferences();
  return (
    <li>
      <span className="ag-repository-source-order">
        {source.resolutionOrder}
      </span>
      <div className="ag-repository-source-content">
        <a href={browseSourceHref(source, groupName)}>
          {source.repositoryName}
        </a>
        <div className="ag-repository-source-meta">
          <Badge>{source.type === "hosted" ? "Hosted" : "Proxy"}</Badge>
          {source.digest && (
            <span>
              {source.type === "proxy"
                ? text("已缓存", "Cached")
                : text("已发布", "Published")}
            </span>
          )}
          {source.size !== undefined && <span>{formatBytes(source.size)}</span>}
        </div>
        {source.cacheRepositoryName && (
          <p>
            {text("缓存范围", "Cache scope")} · {source.cacheRepositoryName}
          </p>
        )}
        {source.digest && (
          <div className="ag-repository-source-digest">
            <code title={source.digest}>{shortDigest(source.digest)}</code>
            <CopyButton text={source.digest} />
          </div>
        )}
      </div>
    </li>
  );
}
