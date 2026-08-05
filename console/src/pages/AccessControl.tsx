import { useEffect, useState } from "react";
import { Button, Collapse, Input, Popconfirm, Select, Switch } from "antd";
import { ClearOutlined } from "@ant-design/icons";
import { Link } from "react-router-dom";
import {
  getAnonymousAccessPolicy,
  listRepositories,
  listGrants,
  replaceAnonymousAccessPolicy,
} from "../client";
import type { AnonymousAccessPolicy, Repository, Grant } from "../client";
import { PageHeader, Card, CardHeader, DataTable } from "../components/Layout";
import { Loading, ErrorBanner, EmptyState } from "../components/Feedback";
import { FormatBadge, Badge } from "../components/Badge";
import { useAuth } from "../lib/auth";
import {
  CopyableValue,
  FilterBar,
  FilterField,
  MetricStrip,
} from "../components/ConsolePrimitives";

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
}[] = [
  {
    role: "admin",
    tone: "red",
    desc: "全部操作：浏览、发布、删除、授权、密钥管理",
  },
  {
    role: "writer",
    tone: "blue",
    desc: "读 + 写：发布、编辑、复制；不可管理密钥与 admin 授权",
  },
  { role: "reader", tone: "green", desc: "只读：浏览、搜索、拉取" },
];
const AUTHORIZATION_STEPS = [
  {
    title: "先看身份",
    text: "管理员身份直接放行；用户、API Key 或 OIDC 身份会先带着自己的全局角色进入判定。",
  },
  {
    title: "再看全局角色",
    text: "admin 允许全部操作，writer 允许读取和写入，reader 只允许读取。",
  },
  {
    title: "再看仓库规则",
    text: "主体、权限级别和资源前缀都匹配才放行；未匹配会拒绝。",
  },
  {
    title: "兼容旧策略",
    text: "仓库还没有被正式管理时，才回退到旧版静态仓库策略；匿名访问另按全局和仓库开关判断。",
  },
];

function scopeLabel(scopes: string[]): {
  key: "read" | "write" | "admin" | "unknown";
  label: string;
  tone: "red" | "blue" | "green" | "zinc";
} {
  if (scopes.includes("repositories:admin"))
    return { key: "admin", label: "管理员", tone: "red" };
  if (scopes.includes("repositories:write"))
    return { key: "write", label: "写入", tone: "blue" };
  if (scopes.includes("repositories:read"))
    return { key: "read", label: "读取", tone: "green" };
  return { key: "unknown", label: scopes.join(", ") || "未配置", tone: "zinc" };
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
  const { role } = useAuth();
  const canManageAnonymousPolicy = role === "" || role === "admin";
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
        const { data, error: err } = await listRepositories({
          query: { pageSize: 200 },
        });
        if (err || !data) throw err ?? new Error("加载仓库失败");
        const results = await Promise.all(
          data.items.map((repository) =>
            listGrants({ path: { repositoryId: repository.id } }).catch(
              () => null,
            ),
          ),
        );
        if (cancelled) return;
        const out: GrantRow[] = [];
        data.items.forEach((repository, index) => {
          const grants =
            (results[index] as { data?: Grant[] } | null)?.data ?? [];
          grants.forEach((grant) =>
            out.push({
              repositoryId: repository.id,
              repositoryName: repository.name,
              format: repository.format,
              principal: grant.principal,
              scopes: grant.scopes,
              resourcePrefix: grant.resourcePrefix ?? "",
            }),
          );
        });
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
  }, []);

  useEffect(() => {
    let cancelled = false;
    void getAnonymousAccessPolicy().then(({ data, error: err }) => {
      if (cancelled) return;
      if (err || !data)
        setAnonymousPolicyError(err ?? new Error("加载匿名访问策略失败"));
      else setAnonymousPolicy(data);
    });
    return () => {
      cancelled = true;
    };
  }, []);

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
      setAnonymousPolicyError(err ?? new Error("保存匿名访问策略失败"));
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
        scopeFilter === "all" || scopeLabel(row.scopes).key === scopeFilter,
    );
  const principalCount = new Set(grants.map((grant) => grant.principal)).size;
  const adminCount = grants.filter(
    (grant) => scopeLabel(grant.scopes).key === "admin",
  ).length;
  const writeCount = grants.filter(
    (grant) => scopeLabel(grant.scopes).key === "write",
  ).length;
  const hasFilters = Boolean(
    principalFilter || repoFilter || scopeFilter !== "all",
  );

  return (
    <div>
      <PageHeader
        title="访问控制"
        description="跨仓库查看匿名策略与逐仓库授权；规则在对应仓库中编辑。"
      />
      <MetricStrip
        items={[
          {
            label: "授权主体",
            value: principalCount,
            hint: "用户、API Key 或其他主体",
          },
          {
            label: "写入授权",
            value: writeCount,
            hint: "可发布或编辑制品",
            tone: writeCount ? "warning" : "default",
          },
          {
            label: "管理员授权",
            value: adminCount,
            hint: "具备授权与删除能力",
            tone: adminCount ? "danger" : "success",
          },
        ]}
      />
      <Card className="mt-4 px-4 py-3">
        <div className="flex items-center justify-between gap-6">
          <div>
            <div className="text-sm font-medium text-zinc-200">
              全局匿名读取
            </div>
            <p className="mt-1 text-xs text-zinc-500">
              只有同时满足全局策略与仓库 / 组策略时，未认证客户端才能读取。
            </p>
          </div>
          {anonymousPolicy && canManageAnonymousPolicy ? (
            <Popconfirm
              title={
                anonymousPolicy.enabled
                  ? "确认停用全局匿名读取？"
                  : "确认启用全局匿名读取？"
              }
              description={
                anonymousPolicy.enabled
                  ? "停用后所有未认证协议读取都会被拒绝。"
                  : "启用后满足仓库或组策略的制品可被未认证客户端读取。"
              }
              okText="继续"
              cancelText="取消"
              okButtonProps={{
                danger: anonymousPolicy.enabled,
                loading: savingAnonymousPolicy,
              }}
              onConfirm={() => updateAnonymousPolicy(!anonymousPolicy.enabled)}
            >
              <Switch
                checked={anonymousPolicy.enabled}
                loading={savingAnonymousPolicy}
                aria-label="切换全局匿名读取"
                onChange={() => undefined}
              />
            </Popconfirm>
          ) : canManageAnonymousPolicy ? (
            <span className="text-xs text-zinc-500">加载中…</span>
          ) : (
            <Badge tone="zinc">只读</Badge>
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
                权限判定顺序与角色能力{" "}
                <span className="ml-2 text-zinc-600">了解规则如何叠加</span>
              </span>
            ),
            children: (
              <div>
                <p className="mb-4 text-xs leading-5 text-zinc-500">
                  仓库规则只追加权限，不能撤销全局角色。
                </p>
                <div className="grid grid-cols-4 gap-5">
                  {AUTHORIZATION_STEPS.map((step, index) => (
                    <div
                      key={step.title}
                      className="border-l border-zinc-700 pl-3"
                    >
                      <div className="text-xs font-medium text-zinc-300">
                        {index + 1}. {step.title}
                      </div>
                      <p className="mt-1 text-[11px] leading-5 text-zinc-500">
                        {step.text}
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
                      <span>{item.desc}</span>
                    </div>
                  ))}
                </div>
              </div>
            ),
          },
        ]}
      />
      <Card className="p-0">
        <CardHeader title={`授权记录（${filtered.length}）`} />
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
              清除
            </Button>
          }
        >
          <FilterField label="授权主体" className="min-w-[260px]">
            <Input
              allowClear
              placeholder="用户名、API Key 或 actor"
              value={principalFilter}
              onChange={(event) => setPrincipalFilter(event.target.value)}
            />
          </FilterField>
          <FilterField label="仓库" className="min-w-[220px]">
            <Input
              allowClear
              placeholder="仓库名称"
              value={repoFilter}
              onChange={(event) => setRepoFilter(event.target.value)}
            />
          </FilterField>
          <FilterField label="权限级别" className="min-w-[190px]">
            <Select
              className="w-full"
              value={scopeFilter}
              options={[
                { value: "all", label: "全部权限" },
                { value: "read", label: "读取 · 浏览 / 拉取" },
                { value: "write", label: "写入 · 发布 / 编辑" },
                { value: "admin", label: "管理员 · 授权 / 删除" },
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
            title={rows.length === 0 ? "暂无逐仓库授权" : "没有匹配的授权记录"}
            hint={
              rows.length === 0
                ? "在各仓库的「访问授权」Tab 添加主体即可在此汇总"
                : "换个过滤条件试试"
            }
          />
        ) : (
          <DataTable columns={["主体", "仓库", "格式", "权限", "资源前缀", ""]}>
            {filtered.map((row, index) => {
              const scope = scopeLabel(row.scopes);
              return (
                <tr
                  key={`${row.repositoryId}-${row.principal}-${index}`}
                  className="hover:bg-zinc-800/30"
                >
                  <td className="px-4 py-2.5">
                    <Principal value={row.principal} />
                  </td>
                  <td className="px-4 py-2.5">
                    <Link
                      to={`/repositories/${row.repositoryId}`}
                      className="text-xs text-cyan-400 hover:text-cyan-300"
                    >
                      {row.repositoryName}
                    </Link>
                  </td>
                  <td className="px-4 py-2.5">
                    <FormatBadge format={row.format} />
                  </td>
                  <td className="px-4 py-2.5">
                    <Badge tone={scope.tone}>{scope.label}</Badge>
                    {scope.key === "admin" && (
                      <span className="ml-2 text-[10px] text-rose-300">
                        高权限
                      </span>
                    )}
                  </td>
                  <td className="px-4 py-2.5 font-mono text-xs text-zinc-500">
                    {row.resourcePrefix || "—"}
                  </td>
                  <td className="px-4 py-2.5 text-right">
                    <Link
                      to={`/repositories/${row.repositoryId}`}
                      className="text-xs text-zinc-500 hover:text-cyan-300"
                    >
                      编辑 →
                    </Link>
                  </td>
                </tr>
              );
            })}
          </DataTable>
        )}
      </Card>
    </div>
  );
}
