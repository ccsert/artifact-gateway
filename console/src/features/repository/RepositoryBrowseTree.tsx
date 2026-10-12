import { useCallback, useEffect, useRef, useState } from "react";
import {
  ShrinkOutlined,
  BranchesOutlined,
  FolderOpenOutlined,
  ReloadOutlined,
} from "@ant-design/icons";
import { Button, ConfigProvider, Tree } from "antd";
import type { TreeProps } from "antd";
import { browseGroup, browseRepository } from "../../client";
import type {
  BrowseNode,
  BrowseNodePage,
  GroupBrowsePage,
  Repository,
} from "../../client";
import { EmptyState, ErrorBanner, Loading } from "../../components/ui/Feedback";
import { formatBytes } from "../../lib/format";
import { usePreferences } from "../../lib/preferences";

import {
  appendNodePage,
  loadedNodeCount,
  nodeIcon,
  replaceNodeChildren,
  type RepositoryTreeDataNode,
} from "./browseTreeModel";
import { BrowseNodePanel } from "./BrowseNodePanel";

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
      <BrowseNodePanel
        repo={repo}
        selected={selected}
        onOpenInList={onOpenInList}
      />
    </div>
  );
}
