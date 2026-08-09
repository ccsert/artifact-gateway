import { useEffect, useMemo, useState } from "react";
import {
  Button,
  Collapse,
  Input,
  Popconfirm,
  Select,
  Switch,
  Table,
} from "antd";
import type { ColumnsType } from "antd/es/table";
import { ClearOutlined, ExperimentOutlined } from "@ant-design/icons";
import { Link } from "react-router-dom";
import {
  getAnonymousAccessPolicy,
  getRepositoryEffectiveAccess,
  listApiKeys,
  listRepositories,
  listRepositoryGrants,
  listUsers,
  replaceAnonymousAccessPolicy,
} from "../client";
import type {
  AnonymousAccessPolicy,
  ApiKey,
  Repository,
  RepositoryEffectiveAccess,
  User,
} from "../client";
import { PageHeader, Card, CardHeader } from "../components/Layout";
import {
  Loading,
  ErrorBanner,
  EmptyState,
  Spinner,
} from "../components/Feedback";
import { FormatBadge, Badge } from "../components/Badge";
import { AccessDecisionSummary } from "../components/AccessDecisionSummary";
import { useAuth } from "../lib/auth";
import {
  CopyableValue,
  FilterBar,
  FilterField,
  MetricStrip,
} from "../components/ConsolePrimitives";
import { usePreferences } from "../lib/preferences";
import { AuthorizationTemplatesPanel } from "../components/AuthorizationTemplatesPanel";

interface GrantRow {
  repositoryId: string;
  repositoryName: string;
  format: Repository["format"];
  principal: string;
  scopes: string[];
  resourcePrefix: string;
}

type EvaluatorRole = "none" | "reader" | "writer" | "admin";

interface EvaluatorPrincipal {
  value: string;
  actor: string;
  label: string;
  detail: string;
  role: EvaluatorRole;
  current?: boolean;
  disabled?: boolean;
}

const CURRENT_PRINCIPAL = "__current__";
const CUSTOM_PRINCIPAL = "__custom__";

function strongestRole(roles: ApiKey["roles"]): EvaluatorRole {
  if (roles.includes("admin")) return "admin";
  if (roles.includes("writer")) return "writer";
  if (roles.includes("reader")) return "reader";
  return "none";
}

function resourcePlaceholder(format: Repository["format"] | undefined) {
  switch (format) {
    case "maven":
      return "org/example/app/1.0/app-1.0.jar";
    case "oci":
      return "team/backend";
    case "conan":
      return "pkg/1.0/user/stable";
    case "raw":
      return "releases/2026/app.tar.gz";
    case "npm":
      return "@scope/package";
    case "pypi":
      return "gateway-widget";
    case "go":
      return "example.com/team/widget";
    default:
      return "";
  }
}

const ROLE_REFERENCE: {
  role: string;
  tone: "red" | "blue" | "green";
  desc: string;
  descEn: string;
}[] = [
  {
    role: "admin",
    tone: "red",
    desc: "全部操作：浏览、发布、删除、授权、密钥管理",
    descEn:
      "All operations: browse, publish, delete, grants, and key management",
  },
  {
    role: "writer",
    tone: "blue",
    desc: "读 + 写：发布、编辑、复制；不可管理密钥与 admin 授权",
    descEn:
      "Read and write: publish, edit, and replicate; no key or admin grant management",
  },
  {
    role: "reader",
    tone: "green",
    desc: "只读：浏览、搜索、拉取",
    descEn: "Read-only: browse, search, and pull",
  },
];
const AUTHORIZATION_STEPS = [
  {
    title: "先看身份",
    text: "管理员身份直接放行；用户、API Key 或 OIDC 身份会先带着自己的全局角色进入判定。",
    titleEn: "Identify the principal",
    textEn:
      "Administrator identities pass directly. Users, API keys, and OIDC identities enter with their global role.",
  },
  {
    title: "再看全局角色",
    text: "admin 允许全部操作，writer 允许读取和写入，reader 只允许读取。",
    titleEn: "Apply the global role",
    textEn:
      "Admin allows all operations, writer allows reads and writes, and reader allows reads only.",
  },
  {
    title: "再看仓库规则",
    text: "主体、权限级别和资源前缀都匹配才放行；未匹配会拒绝。",
    titleEn: "Apply repository rules",
    textEn:
      "The principal, permission, and resource prefix must all match; otherwise access is denied.",
  },
  {
    title: "兼容旧策略",
    text: "仓库还没有被正式管理时，才回退到旧版静态仓库策略；匿名访问另按全局和仓库开关判断。",
    titleEn: "Legacy compatibility",
    textEn:
      "Unmanaged repositories can fall back to legacy static policy. Anonymous access is evaluated separately.",
  },
];

function scopeLabel(
  scopes: string[],
  english = false,
): {
  key: "read" | "write" | "admin" | "unknown";
  label: string;
  tone: "red" | "blue" | "green" | "zinc";
} {
  if (scopes.includes("repositories:admin"))
    return { key: "admin", label: english ? "Admin" : "管理员", tone: "red" };
  if (scopes.includes("repositories:write"))
    return { key: "write", label: english ? "Write" : "写入", tone: "blue" };
  if (scopes.includes("repositories:read"))
    return { key: "read", label: english ? "Read" : "读取", tone: "green" };
  return {
    key: "unknown",
    label: scopes.join(", ") || (english ? "Not configured" : "未配置"),
    tone: "zinc",
  };
}

function Principal({ value }: { value: string }) {
  const { text } = usePreferences();
  if (value.startsWith("user:")) {
    return (
      <div>
        <div className="text-xs font-medium text-zinc-200">
          {text("用户", "User")} · {value.slice("user:".length)}
        </div>
        <div className="mt-0.5 font-mono text-[10px] text-zinc-600">
          {value}
        </div>
      </div>
    );
  }
  if (!value.startsWith("api-key:")) {
    return (
      <div>
        <div className="text-xs font-medium text-zinc-200">
          {text("OIDC / 自定义", "OIDC / custom")}
        </div>
        <div className="mt-0.5 font-mono text-[10px] text-zinc-500">
          {value}
        </div>
      </div>
    );
  }
  const id = value.slice("api-key:".length);
  return (
    <CopyableValue
      value={value}
      label={`API Key · ${id.slice(0, 8)}${id.length > 8 ? "…" : ""}`}
      className="text-xs text-zinc-200"
    />
  );
}

export function AccessControlPage() {
  const { identity, identityLoading } = useAuth();
  const { locale, text } = usePreferences();
  const english = locale === "en-US";
  const canManageAnonymousPolicy = identity?.administrator === true;
  const [rows, setRows] = useState<GrantRow[] | null>(null);
  const [error, setError] = useState<unknown>(null);
  const [principalFilter, setPrincipalFilter] = useState("");
  const [repoFilter, setRepoFilter] = useState("");
  const [scopeFilter, setScopeFilter] = useState<
    "all" | "read" | "write" | "admin"
  >("all");
  const [anonymousPolicy, setAnonymousPolicy] =
    useState<AnonymousAccessPolicy | null>(null);
  const [anonymousPolicyError, setAnonymousPolicyError] =
    useState<unknown>(null);
  const [savingAnonymousPolicy, setSavingAnonymousPolicy] = useState(false);
  const [users, setUsers] = useState<User[]>([]);
  const [apiKeys, setApiKeys] = useState<ApiKey[]>([]);
  const [repositories, setRepositories] = useState<Repository[]>([]);
  const [evaluatorOptionsLoading, setEvaluatorOptionsLoading] = useState(true);
  const [evaluatorOptionsError, setEvaluatorOptionsError] =
    useState<unknown>(null);
  const [selectedPrincipal, setSelectedPrincipal] = useState(CURRENT_PRINCIPAL);
  const [customActor, setCustomActor] = useState("");
  const [customRole, setCustomRole] = useState<EvaluatorRole>("none");
  const [selectedRepository, setSelectedRepository] = useState("");
  const [resource, setResource] = useState("");
  const [evaluation, setEvaluation] =
    useState<RepositoryEffectiveAccess | null>(null);
  const [evaluationError, setEvaluationError] = useState<unknown>(null);
  const [evaluating, setEvaluating] = useState(false);

  useEffect(() => {
    let cancelled = false;
    void (async () => {
      setError(null);
      try {
        const { data, error: err } = await listRepositoryGrants();
        if (err || !data)
          throw (
            err ??
            new Error(text("加载访问规则失败", "Failed to load access rules"))
          );
        if (cancelled) return;
        const out: GrantRow[] = data.map((record) => ({
          repositoryId: record.repositoryId,
          repositoryName: record.repositoryName,
          format: record.format,
          principal: record.principal,
          scopes: record.scopes,
          resourcePrefix: record.resourcePrefix ?? "",
        }));
        out.sort(
          (a, b) =>
            a.principal.localeCompare(b.principal) ||
            a.repositoryName.localeCompare(b.repositoryName),
        );
        setRows(out);
      } catch (e) {
        if (!cancelled) setError(e);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [text]);

  useEffect(() => {
    let cancelled = false;
    void getAnonymousAccessPolicy().then(({ data, error: err }) => {
      if (cancelled) return;
      if (err || !data)
        setAnonymousPolicyError(
          err ??
            new Error(
              text(
                "加载匿名访问策略失败",
                "Failed to load anonymous access policy",
              ),
            ),
        );
      else setAnonymousPolicy(data);
    });
    return () => {
      cancelled = true;
    };
  }, [text]);

  useEffect(() => {
    let cancelled = false;
    setEvaluatorOptionsLoading(true);
    setEvaluatorOptionsError(null);
    void Promise.all([
      listUsers(),
      listApiKeys(),
      listRepositories({ query: { pageSize: 200 } }),
    ]).then(([usersResult, apiKeysResult, repositoriesResult]) => {
      if (cancelled) return;
      setEvaluatorOptionsLoading(false);
      if (
        usersResult.error ||
        apiKeysResult.error ||
        repositoriesResult.error ||
        !usersResult.data ||
        !apiKeysResult.data ||
        !repositoriesResult.data
      ) {
        setEvaluatorOptionsError(
          usersResult.error ??
            apiKeysResult.error ??
            repositoriesResult.error ??
            new Error(
              text(
                "加载权限检查选项失败",
                "Failed to load access evaluation options",
              ),
            ),
        );
        return;
      }
      setUsers(usersResult.data.items);
      setApiKeys(apiKeysResult.data.items);
      const activeRepositories = repositoriesResult.data.items.filter(
        (repository) => repository.state === "active",
      );
      setRepositories(activeRepositories);
      setSelectedRepository((current) =>
        activeRepositories.some((repository) => repository.id === current)
          ? current
          : (activeRepositories[0]?.id ?? ""),
      );
    });
    return () => {
      cancelled = true;
    };
  }, [text]);

  const updateAnonymousPolicy = async (enabled: boolean) => {
    if (!anonymousPolicy || savingAnonymousPolicy) return;
    setSavingAnonymousPolicy(true);
    setAnonymousPolicyError(null);
    const { data, error: err } = await replaceAnonymousAccessPolicy({
      body: { ...anonymousPolicy, enabled },
      headers: { "If-Match": anonymousPolicy.version },
    });
    setSavingAnonymousPolicy(false);
    if (err || !data)
      setAnonymousPolicyError(
        err ??
          new Error(
            text(
              "保存匿名访问策略失败",
              "Failed to save anonymous access policy",
            ),
          ),
      );
    else setAnonymousPolicy(data);
  };

  const evaluatorPrincipals = useMemo<EvaluatorPrincipal[]>(() => {
    const choices: EvaluatorPrincipal[] = [];
    if (identity) {
      choices.push({
        value: CURRENT_PRINCIPAL,
        actor: identity.actor,
        label: text("当前登录身份", "Current identity"),
        detail: `${identity.actor} · ${identity.role ?? text("无全局角色", "no global role")}`,
        role: identity.role ?? "none",
        current: true,
      });
    }
    choices.push(
      ...users.map((user) => ({
        value: `user:${user.name}`,
        actor: `user:${user.name}`,
        label: `${text("用户", "User")} · ${user.name}`,
        detail: `${text("全局角色", "Global role")} ${user.role}${
          user.state === "disabled" ? ` · ${text("已停用", "Disabled")}` : ""
        }`,
        role: user.role,
        disabled: user.state === "disabled",
      })),
      ...apiKeys.map((key) => {
        const role = strongestRole(key.roles);
        return {
          value: `api-key:${key.id}`,
          actor: `api-key:${key.id}`,
          label: `API Key · ${key.name}`,
          detail: `${text("全局角色", "Global role")} ${
            role === "none" ? text("无", "none") : role
          }${key.revokedAt ? ` · ${text("已撤销", "Revoked")}` : ""}`,
          role,
          disabled: Boolean(key.revokedAt),
        };
      }),
      {
        value: CUSTOM_PRINCIPAL,
        actor: customActor.trim(),
        label: text("OIDC / 自定义 actor", "OIDC / custom actor"),
        detail: text(
          "输入认证后产生的完整 actor，并选择其全局角色",
          "Enter the complete authenticated actor and select its global role",
        ),
        role: customRole,
      },
    );
    return choices;
  }, [apiKeys, customActor, customRole, identity, text, users]);

  const selectedPrincipalChoice = evaluatorPrincipals.find(
    (choice) => choice.value === selectedPrincipal,
  );
  const selectedRepositoryChoice = repositories.find(
    (repository) => repository.id === selectedRepository,
  );
  const effectiveEvaluatorRole =
    selectedPrincipal === CUSTOM_PRINCIPAL
      ? customRole
      : (selectedPrincipalChoice?.role ?? "none");

  const runEvaluation = async () => {
    const choice = selectedPrincipalChoice;
    if (!choice || !selectedRepository) {
      setEvaluationError(
        new Error(
          text("请选择主体和仓库", "Select a principal and repository"),
        ),
      );
      return;
    }
    const actor =
      selectedPrincipal === CUSTOM_PRINCIPAL
        ? customActor.trim()
        : choice.actor;
    if (!choice.current && !actor) {
      setEvaluationError(
        new Error(
          text(
            "请输入认证系统产生的完整 actor",
            "Enter the complete actor produced by the authentication system",
          ),
        ),
      );
      return;
    }
    setEvaluating(true);
    setEvaluationError(null);
    const { data, error: err } = await getRepositoryEffectiveAccess({
      path: { repositoryId: selectedRepository },
      query: choice.current
        ? { resource: resource.trim() || undefined }
        : {
            actor,
            role:
              effectiveEvaluatorRole === "none"
                ? undefined
                : effectiveEvaluatorRole,
            resource: resource.trim() || undefined,
          },
    });
    setEvaluating(false);
    if (err || !data) {
      setEvaluationError(
        err ?? new Error(text("权限检查失败", "Access evaluation failed")),
      );
      return;
    }
    setEvaluation(data);
  };

  const grants = rows ?? [];
  const filtered = grants
    .filter(
      (row) =>
        (!principalFilter ||
          row.principal
            .toLowerCase()
            .includes(principalFilter.toLowerCase())) &&
        (!repoFilter ||
          row.repositoryName.toLowerCase().includes(repoFilter.toLowerCase())),
    )
    .filter(
      (row) =>
        scopeFilter === "all" ||
        scopeLabel(row.scopes, english).key === scopeFilter,
    );
  const principalCount = new Set(grants.map((grant) => grant.principal)).size;
  const adminCount = grants.filter(
    (grant) => scopeLabel(grant.scopes, english).key === "admin",
  ).length;
  const writeCount = grants.filter(
    (grant) => scopeLabel(grant.scopes, english).key === "write",
  ).length;
  const hasFilters = Boolean(
    principalFilter || repoFilter || scopeFilter !== "all",
  );
  const columns: ColumnsType<GrantRow> = [
    {
      title: text("主体", "Principal"),
      dataIndex: "principal",
      key: "principal",
      width: 250,
      render: (value: string) => <Principal value={value} />,
    },
    {
      title: text("仓库", "Repository"),
      dataIndex: "repositoryName",
      key: "repositoryName",
      width: 220,
      render: (value: string, row) => (
        <Link
          to={`/repositories/${row.repositoryId}`}
          className="text-xs text-cyan-400 hover:text-cyan-300"
        >
          {value}
        </Link>
      ),
    },
    {
      title: text("格式", "Format"),
      dataIndex: "format",
      key: "format",
      width: 120,
      render: (value: Repository["format"]) => <FormatBadge format={value} />,
    },
    {
      title: text("权限", "Permission"),
      key: "scope",
      width: 190,
      render: (_, row) => {
        const scope = scopeLabel(row.scopes, english);
        return (
          <span>
            <Badge tone={scope.tone}>{scope.label}</Badge>
            {scope.key === "admin" && (
              <span className="ml-2 text-[10px] text-rose-300">
                {text("高权限", "Elevated")}
              </span>
            )}
          </span>
        );
      },
    },
    {
      title: text("资源前缀", "Resource prefix"),
      dataIndex: "resourcePrefix",
      key: "resourcePrefix",
      width: 240,
      render: (value: string) => (
        <span className="font-mono text-xs text-zinc-500">
          {value || text("整个仓库", "Entire repository")}
        </span>
      ),
    },
    {
      title: text("操作", "Actions"),
      key: "actions",
      fixed: "right",
      width: 90,
      render: (_, row) => (
        <Link
          to={`/repositories/${row.repositoryId}?tab=grants`}
          className="text-xs text-zinc-500 hover:text-cyan-300"
        >
          {text("编辑 →", "Edit →")}
        </Link>
      ),
    },
  ];

  return (
    <div>
      <PageHeader
        title={text("访问控制", "Access control")}
        description={text(
          "检查主体的最终权限，集中查看匿名策略与逐仓库授权",
          "Evaluate effective permissions and review anonymous and repository access rules",
        )}
      />
      <MetricStrip
        items={[
          {
            label: text("授权主体", "Principals"),
            value: principalCount,
            hint: text(
              "用户、API Key 或其他主体",
              "Users, API keys, or other principals",
            ),
          },
          {
            label: text("写入授权", "Write grants"),
            value: writeCount,
            hint: text("可发布或编辑制品", "Can publish or edit artifacts"),
            tone: writeCount ? "warning" : "default",
          },
          {
            label: text("管理员授权", "Admin grants"),
            value: adminCount,
            hint: text("具备授权与删除能力", "Can manage grants and delete"),
            tone: adminCount ? "danger" : "success",
          },
        ]}
      />
      <Card className="mt-4" bodyClassName="p-0">
        <div className="flex items-start justify-between gap-6 border-b border-zinc-800/70 px-5 py-4">
          <div>
            <div className="flex items-center gap-2 text-sm font-semibold text-zinc-100">
              <ExperimentOutlined className="text-cyan-400" />
              {text("权限检查", "Access evaluation")}
            </div>
            <p className="mt-1 text-xs text-zinc-500">
              {text(
                "使用网关真实判定链检查全局角色、仓库授权和资源前缀叠加后的结果。",
                "Run the Gateway decision chain across global roles, repository grants, and resource prefixes.",
              )}
            </p>
          </div>
          {(evaluatorOptionsLoading || identityLoading) && <Spinner />}
        </div>
        <div className="px-5 py-4">
          {evaluatorOptionsError !== null && (
            <div className="mb-4">
              <ErrorBanner error={evaluatorOptionsError} />
            </div>
          )}
          <div className="grid grid-cols-[minmax(250px,1.25fr)_150px_minmax(190px,1fr)_minmax(210px,1fr)_auto] items-end gap-3">
            <FilterField label={text("授权主体", "Principal")}>
              <Select
                className="w-full"
                showSearch={{ optionFilterProp: "label" }}
                loading={evaluatorOptionsLoading || identityLoading}
                value={selectedPrincipal}
                options={evaluatorPrincipals.map((choice) => ({
                  value: choice.value,
                  label: `${choice.label} · ${choice.detail}`,
                  disabled: choice.disabled,
                }))}
                onChange={(value) => {
                  setSelectedPrincipal(value);
                  setEvaluation(null);
                  setEvaluationError(null);
                }}
              />
            </FilterField>
            <FilterField label={text("全局角色", "Global role")}>
              <Select
                className="w-full"
                value={effectiveEvaluatorRole}
                disabled={selectedPrincipal !== CUSTOM_PRINCIPAL}
                options={[
                  { value: "none", label: text("无", "None") },
                  { value: "reader", label: "reader" },
                  { value: "writer", label: "writer" },
                  { value: "admin", label: "admin" },
                ]}
                onChange={(value: EvaluatorRole) => {
                  setCustomRole(value);
                  setEvaluation(null);
                }}
              />
            </FilterField>
            <FilterField label={text("仓库", "Repository")}>
              <Select
                className="w-full"
                showSearch={{ optionFilterProp: "label" }}
                loading={evaluatorOptionsLoading}
                value={selectedRepository || undefined}
                placeholder={text("选择仓库", "Select repository")}
                options={repositories.map((repository) => ({
                  value: repository.id,
                  label: `${repository.name} · ${repository.format}`,
                }))}
                onChange={(value) => {
                  setSelectedRepository(value);
                  setResource("");
                  setEvaluation(null);
                  setEvaluationError(null);
                }}
              />
            </FilterField>
            <FilterField
              label={text("具体资源（可选）", "Resource (optional)")}
            >
              <Input
                className="font-mono"
                allowClear
                placeholder={
                  resourcePlaceholder(selectedRepositoryChoice?.format) ||
                  text(
                    "留空检查整个仓库",
                    "Blank evaluates repository-wide access",
                  )
                }
                value={resource}
                onChange={(event) => {
                  setResource(event.target.value);
                  setEvaluation(null);
                  setEvaluationError(null);
                }}
                onPressEnter={() => void runEvaluation()}
              />
            </FilterField>
            <Button
              type="primary"
              icon={<ExperimentOutlined />}
              loading={evaluating}
              disabled={evaluatorOptionsLoading || !selectedRepository}
              onClick={() => void runEvaluation()}
            >
              {text("检查", "Evaluate")}
            </Button>
          </div>
          {selectedPrincipal === CUSTOM_PRINCIPAL && (
            <div className="mt-3 grid grid-cols-[minmax(250px,1.25fr)_150px_minmax(190px,1fr)_minmax(210px,1fr)_auto] gap-3">
              <FilterField
                label={text("完整 actor 标识", "Complete actor identifier")}
                className="col-span-2"
              >
                <Input
                  className="font-mono"
                  placeholder={text(
                    "例如 oidc:gitlab:team/release 或 ci-bot",
                    "For example: oidc:gitlab:team/release or ci-bot",
                  )}
                  value={customActor}
                  onChange={(event) => {
                    setCustomActor(event.target.value);
                    setEvaluation(null);
                    setEvaluationError(null);
                  }}
                  onPressEnter={() => void runEvaluation()}
                />
              </FilterField>
              <div className="col-span-3 flex items-end pb-2 text-[11px] text-zinc-600">
                {text(
                  "该值必须与认证完成后产生的 actor 完全一致；OIDC 角色需按实际映射结果选择。",
                  "This must exactly match the authenticated actor; choose the global role produced by the OIDC mapping.",
                )}
              </div>
            </div>
          )}
          {evaluationError !== null && (
            <div className="mt-4">
              <ErrorBanner error={evaluationError} />
            </div>
          )}
          {evaluation && (
            <div className="mt-4 border-t border-zinc-800/70 pt-4">
              <div className="mb-3 flex min-w-0 items-center gap-2 text-xs text-zinc-500">
                <Badge tone={evaluation.simulated ? "cyan" : "green"}>
                  {evaluation.simulated
                    ? text("模拟结果", "Simulated")
                    : text("当前身份", "Current identity")}
                </Badge>
                <span className="font-mono text-zinc-300">
                  {evaluation.actor}
                </span>
                <span>·</span>
                <span>{evaluation.repository.name}</span>
                <span>·</span>
                <span className="min-w-0 truncate font-mono">
                  {evaluation.resource || text("整个仓库", "Entire repository")}
                </span>
              </div>
              <AccessDecisionSummary access={evaluation} />
            </div>
          )}
        </div>
      </Card>
      <Card className="mt-4" bodyClassName="px-4 py-3">
        <div className="flex items-center justify-between gap-6">
          <div>
            <div className="text-sm font-medium text-zinc-200">
              {text("全局匿名读取", "Global anonymous reads")}
            </div>
            <p className="mt-1 text-xs text-zinc-500">
              {text(
                "只有同时满足全局策略与仓库 / 组策略时，未认证客户端才能读取。",
                "Unauthenticated clients can read only when both global and repository/group policy allow it.",
              )}
            </p>
          </div>
          {anonymousPolicy && canManageAnonymousPolicy ? (
            <Popconfirm
              title={
                anonymousPolicy.enabled
                  ? text(
                      "确认停用全局匿名读取？",
                      "Disable global anonymous reads?",
                    )
                  : text(
                      "确认启用全局匿名读取？",
                      "Enable global anonymous reads?",
                    )
              }
              description={
                anonymousPolicy.enabled
                  ? text(
                      "停用后所有未认证协议读取都会被拒绝。",
                      "All unauthenticated protocol reads will be rejected.",
                    )
                  : text(
                      "启用后满足仓库或组策略的制品可被未认证客户端读取。",
                      "Artifacts allowed by repository or group policy become readable without authentication.",
                    )
              }
              okText={text("继续", "Continue")}
              cancelText={text("取消", "Cancel")}
              okButtonProps={{
                danger: anonymousPolicy.enabled,
                loading: savingAnonymousPolicy,
              }}
              onConfirm={() => updateAnonymousPolicy(!anonymousPolicy.enabled)}
            >
              <Switch
                checked={anonymousPolicy.enabled}
                loading={savingAnonymousPolicy}
                aria-label={text(
                  "切换全局匿名读取",
                  "Toggle global anonymous reads",
                )}
                onChange={() => undefined}
              />
            </Popconfirm>
          ) : canManageAnonymousPolicy ? (
            <span className="text-xs text-zinc-500">
              {text("加载中…", "Loading…")}
            </span>
          ) : (
            <Badge tone="zinc">{text("只读", "Read-only")}</Badge>
          )}
        </div>
        {anonymousPolicyError !== null && (
          <div className="mt-3">
            <ErrorBanner error={anonymousPolicyError} />
          </div>
        )}
      </Card>
      <AuthorizationTemplatesPanel repositories={repositories} />
      <Collapse
        ghost
        className="my-4"
        items={[
          {
            key: "authorization",
            label: (
              <span className="text-xs text-zinc-400">
                {text(
                  "权限判定顺序与角色能力",
                  "Authorization order and roles",
                )}{" "}
                <span className="ml-2 text-zinc-600">
                  {text("了解规则如何叠加", "How rules combine")}
                </span>
              </span>
            ),
            children: (
              <div>
                <p className="mb-4 text-xs leading-5 text-zinc-500">
                  {text(
                    "仓库规则只追加权限，不能撤销全局角色。",
                    "Repository rules add permissions; they cannot revoke a global role.",
                  )}
                </p>
                <div className="grid grid-cols-4 gap-5">
                  {AUTHORIZATION_STEPS.map((step, index) => (
                    <div
                      key={step.title}
                      className="border-l border-zinc-700 pl-3"
                    >
                      <div className="text-xs font-medium text-zinc-300">
                        {index + 1}. {english ? step.titleEn : step.title}
                      </div>
                      <p className="mt-1 text-[11px] leading-5 text-zinc-500">
                        {english ? step.textEn : step.text}
                      </p>
                    </div>
                  ))}
                </div>
                <div className="mt-5 grid grid-cols-3 gap-3 border-t border-zinc-800/80 pt-4">
                  {ROLE_REFERENCE.map((item) => (
                    <div
                      key={item.role}
                      className="flex items-start gap-2 text-xs text-zinc-400"
                    >
                      <Badge tone={item.tone}>{item.role}</Badge>
                      <span>{english ? item.descEn : item.desc}</span>
                    </div>
                  ))}
                </div>
              </div>
            ),
          },
        ]}
      />
      <Card>
        <CardHeader
          title={text(
            `授权记录（${filtered.length}）`,
            `${filtered.length} grants`,
          )}
        />
        <FilterBar
          actions={
            <Button
              icon={<ClearOutlined />}
              disabled={!hasFilters}
              onClick={() => {
                setPrincipalFilter("");
                setRepoFilter("");
                setScopeFilter("all");
              }}
            >
              {text("清除", "Clear")}
            </Button>
          }
        >
          <FilterField
            label={text("授权主体", "Principal")}
            className="min-w-[260px]"
          >
            <Input
              allowClear
              placeholder={text(
                "用户名、API Key 或 actor",
                "Username, API key, or actor",
              )}
              value={principalFilter}
              onChange={(event) => setPrincipalFilter(event.target.value)}
            />
          </FilterField>
          <FilterField
            label={text("仓库", "Repository")}
            className="min-w-[220px]"
          >
            <Input
              allowClear
              placeholder={text("仓库名称", "Repository name")}
              value={repoFilter}
              onChange={(event) => setRepoFilter(event.target.value)}
            />
          </FilterField>
          <FilterField
            label={text("权限级别", "Permission level")}
            className="min-w-[190px]"
          >
            <Select
              className="w-full"
              value={scopeFilter}
              options={[
                { value: "all", label: text("全部权限", "All permissions") },
                {
                  value: "read",
                  label: text("读取 · 浏览 / 拉取", "Read · browse / pull"),
                },
                {
                  value: "write",
                  label: text("写入 · 发布 / 编辑", "Write · publish / edit"),
                },
                {
                  value: "admin",
                  label: text("管理员 · 授权 / 删除", "Admin · grant / delete"),
                },
              ]}
              onChange={(value: typeof scopeFilter) => setScopeFilter(value)}
            />
          </FilterField>
        </FilterBar>
        {error ? (
          <div className="px-4 py-4">
            <ErrorBanner error={error} />
          </div>
        ) : !rows ? (
          <Loading />
        ) : filtered.length === 0 ? (
          <EmptyState
            title={
              rows.length === 0
                ? text("暂无逐仓库授权", "No repository grants")
                : text("没有匹配的授权记录", "No matching grants")
            }
            hint={
              rows.length === 0
                ? text(
                    "在各仓库的「访问授权」Tab 添加主体即可在此汇总",
                    "Add a principal from a repository's Access grants tab to see it here",
                  )
                : text("换个过滤条件试试", "Try another filter")
            }
          />
        ) : (
          <Table<GrantRow>
            className="ag-console-table"
            rowKey={(row) =>
              `${row.repositoryId}-${row.principal}-${row.resourcePrefix}`
            }
            size="middle"
            dataSource={filtered}
            columns={columns}
            pagination={false}
            scroll={{ x: 1050, y: 420 }}
          />
        )}
      </Card>
    </div>
  );
}
