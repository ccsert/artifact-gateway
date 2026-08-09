import { useEffect, useState } from "react";
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
import { ClearOutlined } from "@ant-design/icons";
import { Link } from "react-router-dom";
import {
  getAnonymousAccessPolicy,
  listRepositoryGrants,
  replaceAnonymousAccessPolicy,
} from "../client";
import type { AnonymousAccessPolicy, Repository } from "../client";
import { PageHeader, Card, CardHeader } from "../components/Layout";
import {
  Loading,
  ErrorBanner,
  EmptyState,
  Spinner,
} from "../components/Feedback";
import { FormatBadge, Badge } from "../components/Badge";
import { IdentitySummary } from "../components/IdentitySummary";
import { useAuth } from "../lib/auth";
import {
  CopyableValue,
  FilterBar,
  FilterField,
  MetricStrip,
} from "../components/ConsolePrimitives";
import { usePreferences } from "../lib/preferences";

interface GrantRow {
  repositoryId: string;
  repositoryName: string;
  format: Repository["format"];
  principal: string;
  scopes: string[];
  resourcePrefix: string;
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
  if (!value.startsWith("api-key:"))
    return <span className="font-mono text-xs text-zinc-200">{value}</span>;
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
        <span className="font-mono text-xs text-zinc-500">{value || "—"}</span>
      ),
    },
    {
      title: text("操作", "Actions"),
      key: "actions",
      fixed: "right",
      width: 90,
      render: (_, row) => (
        <Link
          to={`/repositories/${row.repositoryId}`}
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
          "跨仓库查看匿名策略与逐仓库授权；规则在对应仓库中编辑。",
          "Review anonymous policy and per-repository grants; edit rules from each repository.",
        )}
      />
      <Card className="mb-4" bodyClassName="px-4 py-3">
        <div className="mb-3 flex items-center justify-between gap-4">
          <div>
            <div className="text-sm font-medium text-zinc-200">
              {text("当前登录身份", "Current identity")}
            </div>
            <p className="mt-0.5 text-xs text-zinc-500">
              {text(
                "权限判定使用的真实凭据来源与全局角色",
                "Credential source and global role used for authorization",
              )}
            </p>
          </div>
          {identityLoading && <Spinner />}
        </div>
        {identity ? (
          <IdentitySummary identity={identity} />
        ) : (
          !identityLoading && (
            <span className="text-xs text-zinc-500">
              {text(
                "暂时无法读取身份信息",
                "Identity is temporarily unavailable",
              )}
            </span>
          )
        )}
      </Card>
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
            scroll={{ x: 1050 }}
          />
        )}
      </Card>
    </div>
  );
}
