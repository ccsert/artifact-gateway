import { type ReactNode } from "react";
import { Button, Collapse, Popover, Tooltip } from "antd";
import { InfoCircleOutlined } from "@ant-design/icons";
import { Link } from "react-router-dom";
import type {
  Repository,
  RepositoryCapacity,
  RepositoryEffectiveAccess,
} from "../../client";
import { AccessDecisionSummary } from "../access-control/AccessDecisionSummary";
import { FormatBadge, StateBadge } from "../../components/ui/Badge";
import { IdentitySummary } from "../identity/IdentitySummary";
import { Card } from "../../components/ui/Layout";
import { formatBytes, formatNumber } from "../../lib/format";
import { usePreferences } from "../../lib/preferences";
import { CopyButton } from "./RepositoryUsageGuides";

export function RepositoryTabSurface({
  standalone,
  children,
}: {
  standalone: boolean;
  children: ReactNode;
}) {
  return standalone ? children : <Card bodyClassName="p-4">{children}</Card>;
}

export function RepositoryConceptHelp({ repo }: { repo: Repository }) {
  const { text } = usePreferences();
  const typeLabel =
    repo.type === "proxy" ? "Proxy Repository" : "Hosted Repository";
  const concepts = [
    [
      "Repository",
      text(
        "一个格式命名空间，承载访问策略、制品或上游配置。",
        "A format namespace containing access policy, artifacts, and upstream configuration.",
      ),
    ],
    [
      typeLabel,
      repo.type === "proxy"
        ? text(
            "按需从上游拉取并缓存响应，不提供发布入口。",
            "Fetches and caches upstream responses on demand; publishing is disabled.",
          )
        : text(
            "保存已校验并发布的制品，可执行删除、恢复和保留。",
            "Stores verified published artifacts and supports deletion, restore, and retention.",
          ),
    ],
    [
      "Artifact",
      text(
        "用户可见的逻辑制品身份，例如 Maven 坐标或 OCI 镜像。",
        "A user-visible logical identity such as a Maven coordinate or OCI image.",
      ),
    ],
    [
      "Asset",
      text(
        "制品下的不可变文件或 Blob，例如 JAR、POM 或镜像层。",
        "An immutable file or blob under an artifact, such as a JAR, POM, or image layer.",
      ),
    ],
    ...(repo.type === "proxy"
      ? [
          [
            "Cache Entry",
            text(
              "上游响应的缓存索引与字节，不等同于 Hosted 制品。",
              "An index and bytes for an upstream response; it is not a hosted artifact.",
            ),
          ],
        ]
      : []),
    ...(repo.type === "hosted"
      ? [
          [
            "Publication",
            text(
              "将完整且通过校验的 staged 内容转为可见制品。",
              "Turns complete, validated staged content into a visible artifact.",
            ),
          ],
          [
            "Tombstone",
            text(
              "删除后的可恢复记录；字节会在确认无引用后回收。",
              "A restorable deletion record; bytes are reclaimed after references are gone.",
            ),
          ],
          [
            "Retention Policy",
            text(
              "按格式规则选择过期版本或路径，生成可审阅的回收任务。",
              "Selects expired versions or paths by format rules and creates a reviewable reclamation job.",
            ),
          ],
        ]
      : []),
  ];

  return (
    <Popover
      placement="bottomRight"
      title={text("概念说明", "Concepts")}
      content={
        <div className="grid max-w-[34rem] grid-cols-2 gap-x-5 gap-y-3 text-xs">
          {concepts.map(([term, description]) => (
            <div key={term}>
              <div className="font-medium text-zinc-200">{term}</div>
              <div className="mt-0.5 leading-5 text-zinc-500">
                {description}
              </div>
            </div>
          ))}
        </div>
      }
    >
      <Tooltip title={text("查看概念说明", "View concepts")}>
        <Button
          type="text"
          size="small"
          icon={<InfoCircleOutlined />}
          aria-label={text("查看概念说明", "View concepts")}
        />
      </Tooltip>
    </Popover>
  );
}

export function RepositorySummary({
  repo,
  capacity,
  onOpenCapacity,
}: {
  repo: Repository;
  capacity: RepositoryCapacity | null;
  onOpenCapacity: () => void;
}) {
  const { text } = usePreferences();
  const protocolPath = `${window.location.origin}/${repo.format}/${repo.name}`;

  return (
    <div
      className="border-b border-zinc-800/70 pb-3"
      role="group"
      aria-label={text("仓库摘要", "Repository summary")}
    >
      <div className="ag-repository-summary-heading flex min-w-0 items-start justify-between gap-6">
        <div className="min-w-0">
          <div className="flex min-w-0 items-center gap-2">
            <h1 className="truncate text-xl font-semibold text-zinc-50">
              {repo.name}
            </h1>
            <FormatBadge format={repo.format} />
            <StateBadge state={repo.state} />
          </div>
          <div className="mt-1 flex flex-wrap items-center gap-x-2 gap-y-1 text-xs text-zinc-500">
            <span>{repo.type ?? "hosted"}</span>
            <span aria-hidden="true">·</span>
            <span>
              {repo.anonymousRead
                ? text("允许匿名读取", "Anonymous reads")
                : text("私有读取", "Private reads")}
            </span>
            <span aria-hidden="true">·</span>
            <span className="font-mono">ID {repo.id}</span>
            <span aria-hidden="true">·</span>
            <span>v{repo.version}</span>
          </div>
        </div>
        <div className="ag-repository-summary-endpoint flex min-w-0 shrink-0 items-center gap-2 pt-0.5">
          <RepositoryConceptHelp repo={repo} />
          <span className="text-xs text-zinc-500">
            {text("协议入口", "Protocol endpoint")}
          </span>
          <code
            className="min-w-0 max-w-[32rem] flex-1 truncate font-mono text-xs text-zinc-300"
            title={protocolPath}
          >
            {protocolPath}
          </code>
          <CopyButton text={protocolPath} />
        </div>
      </div>
      <div className="mt-2 flex items-center gap-4 text-xs">
        {repo.anonymousRead && (
          <Link
            to={`/browse?repository=${encodeURIComponent(repo.id)}`}
            className="font-medium text-[var(--ag-link)] hover:text-[var(--ag-link-hover)]"
          >
            {text("打开公开浏览", "Open public browser")}
          </Link>
        )}
        <Button
          type="link"
          size="small"
          className="h-auto p-0 text-xs text-zinc-400"
          onClick={onOpenCapacity}
        >
          {capacity
            ? text(
                `${formatBytes(capacity.usedBytes)} · ${formatNumber(capacity.objectCount)} 个对象`,
                `${formatBytes(capacity.usedBytes)} · ${formatNumber(capacity.objectCount)} objects`,
              )
            : text("查看容量", "View capacity")}
        </Button>
      </div>
    </div>
  );
}

export function EffectiveAccessPanel({
  effectiveAccess,
}: {
  effectiveAccess: RepositoryEffectiveAccess;
}) {
  const { text } = usePreferences();
  return (
    <Collapse
      ghost
      className="mb-4 border-b border-zinc-800/60"
      items={[
        {
          key: "effective-access",
          label: (
            <span className="text-xs text-zinc-400">
              {text("当前访问判定", "Effective access")}
              <span className="ml-2 font-mono text-zinc-600">
                {effectiveAccess.actor}
              </span>
            </span>
          ),
          children: (
            <div className="border-t border-zinc-800/70 pt-3 text-xs">
              <IdentitySummary identity={effectiveAccess.identity} />
              <div className="mt-4">
                <AccessDecisionSummary access={effectiveAccess} />
              </div>
              <div className="mt-3 text-xs text-zinc-600">
                {text(
                  "判定顺序：管理员身份 → 全局角色 → 仓库授权 → 旧版静态策略。",
                  "Decision order: administrator identity → global role → repository grant → legacy static policy.",
                )}
              </div>
            </div>
          ),
        },
      ]}
    />
  );
}
