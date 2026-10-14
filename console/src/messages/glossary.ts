/**
 * The Console's names for the domain terms in CONTEXT.md. Messages use the
 * Chinese label below for each English term; `avoid` lists the synonyms the
 * message test rejects, so one concept keeps one name across pages.
 *
 * CONTEXT.zh-CN.md writes Promotion as 晋级; the Console has always shown
 * 晋升, and keeps it.
 */
export const glossary: readonly {
  en: string;
  zh: string;
  avoid?: readonly string[];
}[] = [
  { en: "Repository", zh: "仓库", avoid: ["存储库"] },
  { en: "Hosted repository", zh: "Hosted 仓库" },
  { en: "Proxy repository", zh: "Proxy 仓库", avoid: ["镜像仓库"] },
  { en: "Group", zh: "分组", avoid: ["群组", "仓库组"] },
  { en: "Artifact", zh: "制品", avoid: ["工件", "构件"] },
  { en: "Asset", zh: "资产" },
  { en: "Upstream", zh: "上游" },
  { en: "Publication", zh: "发布" },
  { en: "Promotion", zh: "晋升", avoid: ["晋级"] },
  { en: "Replication", zh: "复制" },
  { en: "Tombstone", zh: "墓碑" },
  { en: "Quarantine", zh: "隔离" },
  { en: "Retention policy", zh: "保留策略" },
  { en: "Grant", zh: "授权" },
  { en: "Service account", zh: "服务账号", avoid: ["服务账户"] },
  { en: "Credential", zh: "凭据", avoid: ["凭证"] },
  { en: "API key", zh: "API 密钥" },
  { en: "Audit", zh: "审计" },
  { en: "Webhook delivery", zh: "Webhook 投递" },
];
