import { CheckCircleFilled, CloseCircleFilled } from "@ant-design/icons";
import type {
  EffectiveAccessDecision,
  RepositoryEffectiveAccess,
} from "../client";
import { usePreferences } from "../lib/preferences";

type Localize = (chinese: string, english: string) => string;

export function accessSourceLabel(source: string, text: Localize): string {
  switch (source) {
    case "administrator":
      return text("管理员身份", "Administrator identity");
    case "role":
      return text("全局角色", "Global role");
    case "legacy_static":
      return text("旧版静态策略", "Legacy static policy");
    case "anonymous_policy":
      return text("匿名访问策略", "Anonymous access policy");
    case "repository_grants":
      return text("仓库授权", "Repository grant");
    default:
      return source || text("未说明", "Not specified");
  }
}

export function accessReasonLabel(reason: string, text: Localize): string {
  const labels: Record<string, string> = {
    administrator: text(
      "管理员身份直接放行",
      "Allowed by administrator identity",
    ),
    role_admin: text(
      "全局 admin 角色允许此操作",
      "Allowed by the global admin role",
    ),
    role_writer: text(
      "全局 writer 角色允许此操作",
      "Allowed by the global writer role",
    ),
    role_reader: text(
      "全局 reader 角色允许读取",
      "Read allowed by the global reader role",
    ),
    scope_granted: text(
      "主体、权限级别和资源范围均匹配",
      "Principal, permission, and resource scope all match",
    ),
    scope_not_granted: text(
      "没有同时匹配主体、权限和资源范围的仓库规则",
      "No repository rule matches the principal, permission, and resource scope",
    ),
    grant_lookup_failed: text(
      "读取仓库授权失败，已拒绝访问",
      "Repository grants could not be loaded; access is denied",
    ),
    read_pattern_granted: text(
      "匹配旧版读取规则",
      "Matched a legacy read rule",
    ),
    write_pattern_granted: text(
      "匹配旧版写入规则",
      "Matched a legacy write rule",
    ),
    global_anonymous_access_disabled: text(
      "全局匿名读取未启用",
      "Global anonymous reads are disabled",
    ),
    repository_anonymous_read_disabled: text(
      "当前仓库未允许匿名读取",
      "Anonymous reads are disabled for this repository",
    ),
    repository_anonymous_read_enabled: text(
      "全局和仓库均允许匿名读取",
      "Anonymous reads are enabled globally and for this repository",
    ),
    repository_not_active: text(
      "仓库当前不处于可访问状态",
      "The repository is not currently active",
    ),
  };
  return labels[reason] ?? reason.replaceAll("_", " ");
}

function Decision({
  label,
  decision,
}: {
  label: string;
  decision: EffectiveAccessDecision;
}) {
  const { text } = usePreferences();
  return (
    <div className="min-w-0 px-4 py-3">
      <div className="text-xs font-medium text-zinc-500">{label}</div>
      <div
        className={`mt-1.5 flex items-center gap-1.5 text-sm font-semibold ${
          decision.allowed ? "text-[var(--ag-status-success)]" : "text-zinc-400"
        }`}
      >
        {decision.allowed ? <CheckCircleFilled /> : <CloseCircleFilled />}
        {decision.allowed ? text("允许", "Allowed") : text("拒绝", "Denied")}
      </div>
      <div className="mt-1 text-xs font-medium text-zinc-400">
        {accessSourceLabel(decision.source, text)}
      </div>
      <p className="mt-0.5 text-xs leading-4 text-zinc-600">
        {accessReasonLabel(decision.reason, text)}
      </p>
    </div>
  );
}

export function AccessDecisionSummary({
  access,
}: {
  access: RepositoryEffectiveAccess;
}) {
  const { text } = usePreferences();
  const decisions = [
    {
      label: text("匿名读取", "Anonymous reads"),
      decision: access.anonymousRead,
    },
    { label: text("读取", "Read"), decision: access.permissions.read },
    { label: text("写入", "Write"), decision: access.permissions.write },
    { label: text("管理", "Admin"), decision: access.permissions.admin },
    {
      label: text("制品情报", "Artifact intelligence"),
      decision: access.permissions.intelligence,
    },
  ];
  return (
    <div className="grid grid-cols-5 divide-x divide-zinc-800/70 overflow-hidden rounded-md border border-zinc-800/70 bg-zinc-950/20">
      {decisions.map((item) => (
        <Decision key={item.label} {...item} />
      ))}
    </div>
  );
}
