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
  const { t } = usePreferences();
  return (
    <aside
      className="ag-repository-tree-inspector"
      aria-label={t("repository.nodeDetails")}
    >
      {selected ? (
        <div className="ag-repository-tree-inspector-content">
          <div className="ag-repository-tree-inspector-heading">
            <div className="ag-repository-tree-inspector-title">
              {nodeIcon(selected.kind, true)}
              <strong>{selected.name}</strong>
            </div>
            <Badge tone="neutral">{nodeKindLabel(selected.kind, t)}</Badge>
          </div>
          {selected.kind === "asset" &&
            selected.coordinate &&
            repo.type !== "group" && (
              <Button
                className="ag-repository-tree-open"
                onClick={() => onOpenInList(selected)}
              >
                {t("repository.openInList")}
              </Button>
            )}
          {(selected.coordinate || selected.path) && (
            <div className="ag-repository-tree-field">
              <span>{t("repository.canonicalLocation")}</span>
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
              aria-label={t("repository.localSources")}
            >
              <h4>
                {t("repository.localSources")}{" "}
                <span>{selected.sources.length}</span>
              </h4>
              <p>{t("repository.orderedByVisibleMemberPriorityNotAnActual")}</p>
              {selected.kind === "asset" &&
                new Set(
                  selected.sources
                    .map((source) => source.digest)
                    .filter(Boolean),
                ).size > 1 && (
                  <p>
                    <Badge tone="warning">
                      {t("repository.differentContentAtThisPath")}
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
                label={t("repository.sourceRepository")}
                value={selected.sourceRepositoryName}
              />
            )}
            {selected.cacheState === "cached" && (
              <Evidence
                label={t("repository.cacheState")}
                value={t("repository.cached")}
              />
            )}
            {selected.buildNumber !== undefined && (
              <Evidence
                label={t("repository.buildNumber")}
                value={selected.buildNumber}
              />
            )}
            {selected.size !== undefined && (
              <Evidence
                label={t("repository.size")}
                value={formatBytes(selected.size)}
              />
            )}
            {selected.contentType && (
              <Evidence
                label={t("repository.contentType")}
                value={selected.contentType}
              />
            )}
            {(selected.cachedAt || selected.createdAt) && (
              <Evidence
                label={
                  selected.cachedAt
                    ? t("repository.cacheUpdated")
                    : t("repository.updated")
                }
                value={formatDate(selected.cachedAt ?? selected.createdAt!)}
              />
            )}
          </dl>
          {formatDescriptor(repo.format)?.separateAssetPath &&
            selected.kind === "asset" &&
            selected.path && (
              <div className="ag-repository-tree-field">
                <span>{t("repository.assetPath")}</span>
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
          <strong>{t("repository.selectAnItemInTheDirectory")}</strong>
          <p>
            {t("repository.inspectItsFullNameSourcesAndVerificationDetails")}
          </p>
          <span>
            <ArrowUpOutlined aria-hidden />{" "}
            {t("repository.arrowKeysToBrowseEnterToSelect")}
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
  const { t } = usePreferences();
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
                ? t("repository.cached")
                : t("repository.published")}
            </span>
          )}
          {source.size !== undefined && <span>{formatBytes(source.size)}</span>}
        </div>
        {source.cacheRepositoryName && (
          <p>
            {t("repository.cacheScope")} · {source.cacheRepositoryName}
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
