import {
  useCallback,
  useEffect,
  useRef,
  useState,
  type ReactNode,
} from "react";
import {
  AppstoreOutlined,
  ShrinkOutlined,
  ArrowUpOutlined,
  BranchesOutlined,
  FileOutlined,
  FolderOpenOutlined,
  FolderOutlined,
  ReloadOutlined,
  TagsOutlined,
} from "@ant-design/icons";
import { Button, ConfigProvider, Tree } from "antd";
import type { TreeDataNode, TreeProps } from "antd";
import { browseGroup, browseRepository } from "../../client";
import type {
  BrowseNode,
  BrowseNodePage,
  GroupBrowsePage,
  BrowseSource,
  Repository,
} from "../../client";
import { Badge } from "../../components/Badge";
import { EmptyState, ErrorBanner, Loading } from "../../components/Feedback";
import { formatBytes, formatDate, shortDigest } from "../../lib/format";
import { usePreferences } from "../../lib/preferences";
import { decodeRawPathForDisplay } from "../../lib/rawPath";
import { CopyButton } from "./RepositoryUsageGuides";

interface RepositoryTreeDataNode extends TreeDataNode {
  browseNode?: BrowseNode;
  parentId?: string;
  pageToken?: string;
  loadMore?: boolean;
  children?: RepositoryTreeDataNode[];
}

function nodeKindLabel(
  kind: BrowseNode["kind"],
  text: (zh: string, en: string) => string,
) {
  const labels: Record<BrowseNode["kind"], [string, string]> = {
    directory: ["目录", "Directory"],
    namespace: ["命名空间", "Namespace"],
    component: ["组件", "Component"],
    version: ["版本", "Version"],
    asset: ["资产", "Asset"],
  };
  return text(...labels[kind]);
}

function nodeIcon(kind: BrowseNode["kind"], expanded: boolean): ReactNode {
  if (kind === "asset") return <FileOutlined />;
  if (kind === "component") return <AppstoreOutlined />;
  if (kind === "version") return <TagsOutlined />;
  return expanded ? <FolderOpenOutlined /> : <FolderOutlined />;
}

function loadedNodeCount(nodes: RepositoryTreeDataNode[]): number {
  return nodes.reduce(
    (count, node) =>
      count + (node.loadMore ? 0 : 1) + loadedNodeCount(node.children ?? []),
    0,
  );
}

function replaceNodeChildren(
  nodes: RepositoryTreeDataNode[],
  key: React.Key,
  children: RepositoryTreeDataNode[],
): RepositoryTreeDataNode[] {
  return nodes.map((node) => {
    if (node.key === key) return { ...node, children };
    if (!node.children) return node;
    return {
      ...node,
      children: replaceNodeChildren(node.children, key, children),
    };
  });
}

function appendNodePage(
  nodes: RepositoryTreeDataNode[],
  key: React.Key,
  children: RepositoryTreeDataNode[],
): RepositoryTreeDataNode[] {
  return nodes.map((node) => {
    if (node.key === key) {
      const retained = (node.children ?? []).filter((child) => !child.loadMore);
      return { ...node, children: [...retained, ...children] };
    }
    if (!node.children) return node;
    return { ...node, children: appendNodePage(node.children, key, children) };
  });
}

export function RepositoryBrowseTree({
  repo,
  onOpenInList,
  initialPage,
  onGroupRefresh,
}: {
  repo: Pick<Repository, "id" | "name" | "format"> & {
    type?: Repository["type"] | "group";
  };
  initialPage?: BrowseNodePage;
  onGroupRefresh?: (page: GroupBrowsePage) => void;
  onOpenInList: (node: BrowseNode) => void;
}) {
  const { text } = usePreferences();
  const requestVersion = useRef(0);
  const pending = useRef(new Map<string, Promise<void>>());
  const retryAction = useRef<(() => Promise<void>) | null>(null);
  const [treeEpoch, setTreeEpoch] = useState(0);
  const [treeData, setTreeData] = useState<RepositoryTreeDataNode[]>([]);
  const [selected, setSelected] = useState<BrowseNode | null>(null);
  const [expandedKeys, setExpandedKeys] = useState<React.Key[]>([]);
  const [refreshing, setRefreshing] = useState(false);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<unknown>(null);

  const requestPage = useCallback(
    async (query: {
      parent?: string;
      pageToken?: string;
      pageSize: number;
    }) => {
      if (repo.type === "group") {
        const response = await browseGroup({
          path: { groupId: repo.id },
          query,
        });
        return { ...response, groupPage: response.data };
      }
      const response = await browseRepository({
        path: { repositoryId: repo.id },
        query,
      });
      return { ...response, groupPage: undefined };
    },
    [repo.id, repo.type],
  );

  const nodeTitle = useCallback(
    (node: BrowseNode) => (
      <span className="ag-repository-tree-node-title" data-kind={node.kind}>
        <span className="ag-repository-tree-node-name" title={node.name}>
          {node.name}
        </span>
        {node.sources && node.sources.length > 1 && (
          <span
            className="ag-repository-tree-node-meta"
            title={text("多个本地来源", "Multiple local sources")}
          >
            <BranchesOutlined aria-hidden /> {node.sources.length}
          </span>
        )}
        {node.kind === "asset" && node.size !== undefined && (
          <span className="ag-repository-tree-node-meta">
            {formatBytes(node.size)}
          </span>
        )}
      </span>
    ),
    [text],
  );

  const toTreeNodes = useCallback(
    (
      items: BrowseNode[],
      parentId?: string,
      nextPageToken?: string,
    ): RepositoryTreeDataNode[] => {
      const nodes: RepositoryTreeDataNode[] = items.map((node) => ({
        key: node.id,
        title: nodeTitle(node),
        browseNode: node,
        isLeaf: !node.hasChildren,
        icon: ({ expanded }) => nodeIcon(node.kind, Boolean(expanded)),
      }));
      if (nextPageToken) {
        nodes.push({
          key: `more:${parentId ?? "root"}:${nextPageToken}`,
          title: text("加载更多", "Load more"),
          isLeaf: true,
          loadMore: true,
          parentId,
          pageToken: nextPageToken,
          icon: <span className="ag-repository-tree-more-mark" />,
        });
      }
      return nodes;
    },
    [nodeTitle, text],
  );

  const loadRoot = useCallback(
    async function loadRootRequest(preserveTree = false) {
      const version = ++requestVersion.current;
      if (!preserveTree) {
        setTreeData([]);
        setLoading(true);
      }
      setRefreshing(preserveTree);
      setError(null);
      retryAction.current = () => loadRootRequest(preserveTree);
      try {
        const response = await requestPage({ pageSize: 50 });
        if (version !== requestVersion.current) return;
        if (response.error || !response.data) {
          setError(
            response.error ??
              new Error(text("读取目录失败", "Failed to load directory")),
          );
          return;
        }
        if (response.groupPage) onGroupRefresh?.(response.groupPage);
        setSelected(null);
        setExpandedKeys([]);
        setTreeEpoch((epoch) => epoch + 1);
        retryAction.current = null;
        setTreeData(
          toTreeNodes(
            response.data.items,
            undefined,
            response.data.nextPageToken,
          ),
        );
      } catch (requestError) {
        if (version === requestVersion.current) setError(requestError);
      } finally {
        if (version === requestVersion.current) {
          setLoading(false);
          setRefreshing(false);
        }
      }
    },
    [onGroupRefresh, requestPage, text, toTreeNodes],
  );

  useEffect(() => {
    setSelected(null);
    if (initialPage) {
      requestVersion.current += 1;
      setTreeData(
        toTreeNodes(initialPage.items, undefined, initialPage.nextPageToken),
      );
      setLoading(false);
    } else void loadRoot(false);
    return () => {
      requestVersion.current += 1;
    };
  }, [initialPage, loadRoot, toTreeNodes]);

  const loadBranch = (
    node: RepositoryTreeDataNode,
    append: boolean,
  ): Promise<void> => {
    const version = requestVersion.current;
    const parentId = append ? node.parentId : node.browseNode?.id;
    const pageToken = append ? node.pageToken : undefined;
    const key = `${version}:${parentId ?? "root"}:${pageToken ?? "children"}`;
    const existing = pending.current.get(key);
    if (existing) return existing;
    setError(null);
    const request = (async () => {
      try {
        const response = await requestPage({
          parent: parentId,
          pageSize: 50,
          ...(pageToken ? { pageToken } : {}),
        });
        if (version !== requestVersion.current) return;
        if (response.error || !response.data) {
          throw (
            response.error ??
            new Error(text("读取目录失败", "Failed to load directory"))
          );
        }
        const page = toTreeNodes(
          response.data.items,
          parentId,
          response.data.nextPageToken,
        );
        setTreeData((current) => {
          if (!append) return replaceNodeChildren(current, node.key, page);
          if (parentId) return appendNodePage(current, parentId, page);
          return [...current.filter((item) => !item.loadMore), ...page];
        });
        // Another branch may have failed while this request was in flight.
        // Its visible error must retain the matching retry action.
      } catch (requestError) {
        if (version === requestVersion.current) {
          setError(requestError);
          retryAction.current = () => loadBranch(node, append);
        }
        throw requestError;
      } finally {
        pending.current.delete(key);
      }
    })();
    pending.current.set(key, request);
    return request;
  };

  const loadChildren: TreeProps<RepositoryTreeDataNode>["loadData"] = (
    node,
  ) => {
    if (!node.browseNode?.hasChildren || node.children)
      return Promise.resolve();
    // The Tree retries rejected loads automatically. Keep the visible failure
    // stable; the Retry action repeats this exact parent explicitly.
    return loadBranch(node, false).catch(() => undefined);
  };

  const retry = () => {
    const operation = retryAction.current;
    if (operation) void operation().catch(() => undefined);
  };

  const selectNode: TreeProps<RepositoryTreeDataNode>["onSelect"] = (
    _keys,
    info,
  ) => {
    const node = info.node as RepositoryTreeDataNode;
    if (node.loadMore) {
      void loadBranch(node, true).catch(() => undefined);
      return;
    }
    setSelected(node.browseNode ?? null);
  };

  if (loading) {
    return <Loading label={text("正在读取目录…", "Loading directory…")} />;
  }

  if (treeData.length === 0 && error !== null) {
    return <ErrorBanner error={error} onRetry={() => void loadRoot(false)} />;
  }

  if (treeData.length === 0 && error === null) {
    return (
      <EmptyState
        compact
        title={text("暂无可浏览制品", "No browseable artifacts")}
        hint={
          repo.type === "group"
            ? text(
                "当前可读取的成员尚无已发布制品或有效缓存。",
                "Readable members have no published artifacts or live cache entries yet.",
              )
            : repo.type === "proxy"
              ? text(
                  "Proxy 目录只展示已经获取并记录的缓存资产。",
                  "Proxy directories show only fetched and recorded cache assets.",
                )
              : text(
                  "发布 Maven 制品或上传 Raw 文件后，目录会按格式语义自动生成。",
                  "Publish Maven artifacts or upload Raw files to populate the format-aware directory.",
                )
        }
      />
    );
  }

  return (
    <div className="ag-repository-browse">
      {error !== null && (
        <div className="ag-repository-browse-error">
          <ErrorBanner error={error} onRetry={retry} />
        </div>
      )}
      <section
        className="ag-repository-tree-pane"
        aria-label={text("仓库目录", "Repository directory")}
      >
        <div className="ag-repository-tree-heading">
          <div>
            <h3>{text("制品目录", "Artifact directory")}</h3>
            <p>
              {repo.type === "group"
                ? text(
                    "成员制品与已知缓存",
                    "Member publications and known cache",
                  )
                : repo.type === "proxy"
                  ? text("只显示已缓存资产", "Cached assets only")
                  : text(
                      "按目录逐层浏览制品",
                      "Explore artifacts by directory",
                    )}
            </p>
          </div>
          <div className="ag-repository-tree-tools">
            <Button
              type="text"
              size="small"
              icon={<ShrinkOutlined />}
              disabled={expandedKeys.length === 0}
              aria-label={text("收起全部目录", "Collapse all directories")}
              title={text("收起全部目录", "Collapse all directories")}
              onClick={() => setExpandedKeys([])}
            />
            <Button
              type="text"
              size="small"
              icon={<ReloadOutlined aria-hidden />}
              aria-label={text("刷新", "Refresh")}
              loading={refreshing}
              onClick={() => void loadRoot(true)}
            >
              {text("刷新", "Refresh")}
            </Button>
          </div>
        </div>
        <div className="ag-repository-tree-root">
          <FolderOpenOutlined aria-hidden />
          <span title={repo.name}>{repo.name}</span>
          <span>{repo.format.toUpperCase()}</span>
        </div>
        <ConfigProvider
          theme={{
            components: {
              Tree: {
                titleHeight: 36,
                indentSize: 20,
                directoryNodeSelectedBg: "var(--ag-action-primary-soft)",
                directoryNodeSelectedColor: "var(--ag-content-strong)",
                nodeHoverBg: "var(--ag-surface-table-header)",
              },
            },
          }}
        >
          <Tree.DirectoryTree<RepositoryTreeDataNode>
            key={treeEpoch}
            className="ag-repository-tree"
            aria-label={text("制品目录树", "Artifact directory tree")}
            blockNode
            expandAction="click"
            loadData={loadChildren}
            onSelect={selectNode}
            selectedKeys={selected ? [selected.id] : []}
            expandedKeys={expandedKeys}
            onExpand={setExpandedKeys}
            showIcon
            showLine={false}
            treeData={treeData}
          />
        </ConfigProvider>
        <div className="ag-repository-tree-footer">
          {text(
            `已加载 ${loadedNodeCount(treeData)} 项`,
            `${loadedNodeCount(treeData)} entries loaded`,
          )}
          <span>
            {text("展开目录加载更多", "Expand directories to load more")}
          </span>
        </div>
      </section>
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
                    {repo.format === "raw"
                      ? decodeRawPathForDisplay(
                          selected.coordinate ?? selected.path ?? "",
                        )
                      : (selected.coordinate ?? selected.path)}
                  </code>
                  <CopyButton
                    text={selected.coordinate ?? selected.path ?? ""}
                  />
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
            {repo.format === "maven" &&
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
    </div>
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
