import type { MessageKey } from "../../messages";
import type { Translate } from "../../lib/preferences";
import { type ReactNode } from "react";
import {
  AppstoreOutlined,
  FileOutlined,
  FolderOpenOutlined,
  FolderOutlined,
  TagsOutlined,
} from "@ant-design/icons";
import type { TreeDataNode } from "antd";
import type { BrowseNode } from "../../client";

export interface RepositoryTreeDataNode extends TreeDataNode {
  browseNode?: BrowseNode;
  parentId?: string;
  pageToken?: string;
  loadMore?: boolean;
  children?: RepositoryTreeDataNode[];
}

export function nodeKindLabel(kind: BrowseNode["kind"], t: Translate) {
  const labels: Record<BrowseNode["kind"], MessageKey> = {
    directory: "repository.directory",
    namespace: "repository.namespace",
    component: "repository.component",
    version: "repository.version",
    asset: "repository.asset",
  };
  return t(labels[kind]);
}

export function nodeIcon(
  kind: BrowseNode["kind"],
  expanded: boolean,
): ReactNode {
  if (kind === "asset") return <FileOutlined />;
  if (kind === "component") return <AppstoreOutlined />;
  if (kind === "version") return <TagsOutlined />;
  return expanded ? <FolderOpenOutlined /> : <FolderOutlined />;
}

export function loadedNodeCount(nodes: RepositoryTreeDataNode[]): number {
  return nodes.reduce(
    (count, node) =>
      count + (node.loadMore ? 0 : 1) + loadedNodeCount(node.children ?? []),
    0,
  );
}

export function replaceNodeChildren(
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

export function appendNodePage(
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
