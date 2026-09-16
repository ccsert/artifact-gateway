import type {
  ArtifactIntelligenceSummary,
  ProxyCacheAsset,
} from "../../client";

export type ProxyMavenFile = ProxyCacheAsset;

// 统一的制品行，按格式从不同端点归一化而来
export interface ArtifactRow {
  key: string;
  coordinate: string;
  digest?: string;
  createdAt?: string;
  state?: string;
  size?: number;
  contentType?: string;
  publisher?: string;
  cachedAt?: string;
  sourceUrl?: string;
  intelligence?: ArtifactIntelligenceSummary;
  buildNumber?: number;
  // maven 聚合：同 group:artifact 的版本数与最新版本
  versionCount?: number;
  latestVersion?: string;
  fileCount?: number;
  primaryFiles?: string[];
  files?: ProxyMavenFile[];
}
