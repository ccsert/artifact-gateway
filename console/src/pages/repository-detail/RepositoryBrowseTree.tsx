import {
  useCallback,
  useEffect,
  useRef,
  useState,
  type ReactNode,
} from "react";
import {
  AppstoreOutlined,
  FileOutlined,
  FolderOpenOutlined,
  FolderOutlined,
  ReloadOutlined,
  TagsOutlined,
} from "@ant-design/icons";
import { Button, Tree } from "antd";
import type { TreeDataNode, TreeProps } from "antd";
import { browseRepository } from "../../client";
import type { BrowseNode, Repository } from "../../client";
import { Badge } from "../../components/Badge";
import { EmptyState, ErrorBanner, Loading } from "../../components/Feedback";
import { formatBytes, formatDate, shortDigest } from "../../lib/format";
import { usePreferences } from "../../lib/preferences";
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
}: {
  repo: Repository;
  onOpenInList: (coordinate: string) => void;
}) {
  const { text } = usePreferences();
  const requestVersion = useRef(0);
  const [treeData, setTreeData] = useState<RepositoryTreeDataNode[]>([]);
  const [selected, setSelected] = useState<BrowseNode | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<unknown>(null);

  const nodeTitle = useCallback(
    (node: BrowseNode) => (
      <span className="ag-repository-tree-node-title">
        <span className="ag-repository-tree-node-name" title={node.name}>
          {node.name}
        </span>
        {node.kind === "asset" && node.size !== undefined && (
          <span className="ag-repository-tree-node-meta">
            {formatBytes(node.size)}
          </span>
        )}
      </span>
    ),
    [],
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
    async (preserveTree = false) => {
      const version = ++requestVersion.current;
      if (!preserveTree) {
        setTreeData([]);
        setLoading(true);
      }
      setError(null);
      const response = await browseRepository({
        path: { repositoryId: repo.id },
        query: { pageSize: 50 },
      });
      if (version !== requestVersion.current) return;
      setLoading(false);
      if (response.error || !response.data) {
        setError(
          response.error ??
            new Error(text("读取目录失败", "Failed to load directory")),
        );
        return;
      }
      setTreeData(
        toTreeNodes(
          response.data.items,
          undefined,
          response.data.nextPageToken,
        ),
      );
    },
    [repo.id, text, toTreeNodes],
  );

  useEffect(() => {
    setSelected(null);
    void loadRoot(false);
    return () => {
      requestVersion.current += 1;
    };
  }, [loadRoot]);

  const loadChildren: TreeProps<RepositoryTreeDataNode>["loadData"] = async (
    treeNode,
  ) => {
    const node = treeNode as RepositoryTreeDataNode;
    if (!node.browseNode?.hasChildren || node.children) return;
    const version = requestVersion.current;
    setError(null);
    const response = await browseRepository({
      path: { repositoryId: repo.id },
      query: { parent: node.browseNode.id, pageSize: 50 },
    });
    if (version !== requestVersion.current) return;
    if (response.error || !response.data) {
      const requestError =
        response.error ??
        new Error(text("读取子目录失败", "Failed to load child nodes"));
      setError(requestError);
      throw requestError;
    }
    setTreeData((current) =>
      replaceNodeChildren(
        current,
        node.key,
        toTreeNodes(
          response.data.items,
          node.browseNode?.id,
          response.data.nextPageToken,
        ),
      ),
    );
  };

  const loadMore = async (node: RepositoryTreeDataNode) => {
    const version = requestVersion.current;
    setError(null);
    const response = await browseRepository({
      path: { repositoryId: repo.id },
      query: {
        parent: node.parentId,
        pageSize: 50,
        pageToken: node.pageToken,
      },
    });
    if (version !== requestVersion.current) return;
    if (response.error || !response.data) {
      setError(
        response.error ??
          new Error(text("读取下一页失败", "Failed to load next page")),
      );
      return;
    }
    const page = toTreeNodes(
      response.data.items,
      node.parentId,
      response.data.nextPageToken,
    );
    const parentId = node.parentId;
    if (parentId) {
      setTreeData((current) => appendNodePage(current, parentId, page));
    } else {
      setTreeData((current) => [
        ...current.filter((item) => !item.loadMore),
        ...page,
      ]);
    }
  };

  const selectNode: TreeProps<RepositoryTreeDataNode>["onSelect"] = (
    _keys,
    info,
  ) => {
    const node = info.node as RepositoryTreeDataNode;
    if (node.loadMore) {
      void loadMore(node);
      return;
    }
    setSelected(node.browseNode ?? null);
  };

  if (loading) {
    return <Loading label={text("正在读取目录…", "Loading directory…")} />;
  }

  if (treeData.length === 0 && error === null) {
    return (
      <EmptyState
        compact
        title={text("暂无可浏览制品", "No browseable artifacts")}
        hint={
          repo.type === "proxy"
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
          <ErrorBanner error={error} onRetry={() => loadRoot(true)} />
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
              {repo.type === "proxy"
                ? text("只显示已缓存资产", "Cached assets only")
                : text("服务端格式投影", "Server-owned format projection")}
            </p>
          </div>
          <Button
            type="text"
            size="small"
            icon={<ReloadOutlined />}
            onClick={() => void loadRoot(true)}
          >
            {text("刷新", "Refresh")}
          </Button>
        </div>
        <Tree.DirectoryTree<RepositoryTreeDataNode>
          className="ag-repository-tree"
          aria-label={text("制品目录树", "Artifact directory tree")}
          blockNode
          expandAction="click"
          loadData={loadChildren}
          onSelect={selectNode}
          showIcon
          showLine={{ showLeafIcon: false }}
          treeData={treeData}
        />
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
              <Badge tone={selected.kind === "asset" ? "cyan" : "zinc"}>
                {nodeKindLabel(selected.kind, text)}
              </Badge>
            </div>
            {(selected.coordinate || selected.path) && (
              <div className="ag-repository-tree-field">
                <span>{text("规范位置", "Canonical location")}</span>
                <div>
                  <code>{selected.coordinate ?? selected.path}</code>
                  <CopyButton
                    text={selected.coordinate ?? selected.path ?? ""}
                  />
                </div>
              </div>
            )}
            {selected.digest && (
              <div className="ag-repository-tree-field">
                <span>{text("摘要", "Digest")}</span>
                <div>
                  <code title={selected.digest}>
                    {shortDigest(selected.digest)}
                  </code>
                  <CopyButton text={selected.digest} />
                </div>
              </div>
            )}
            {selected.contentType && (
              <div className="ag-repository-tree-field">
                <span>{text("内容类型", "Content type")}</span>
                <strong>{selected.contentType}</strong>
              </div>
            )}
            {selected.size !== undefined && (
              <div className="ag-repository-tree-field">
                <span>{text("大小", "Size")}</span>
                <strong>{formatBytes(selected.size)}</strong>
              </div>
            )}
            {selected.createdAt && (
              <div className="ag-repository-tree-field">
                <span>{text("更新时间", "Updated")}</span>
                <strong>{formatDate(selected.createdAt)}</strong>
              </div>
            )}
            {selected.kind === "asset" && selected.coordinate && (
              <Button
                onClick={() => {
                  if (selected.coordinate) onOpenInList(selected.coordinate);
                }}
              >
                {text("在列表中查看", "Open in list")}
              </Button>
            )}
          </div>
        ) : (
          <div className="ag-repository-tree-inspector-empty">
            <FolderOpenOutlined />
            <p>
              {text(
                "选择节点查看规范位置和资产证据",
                "Select a node to inspect its canonical location and asset evidence",
              )}
            </p>
          </div>
        )}
      </aside>
    </div>
  );
}
