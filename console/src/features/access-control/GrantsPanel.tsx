import { useState } from "react";
import type { Repository } from "../../client";
import { Button, Input, Select } from "antd";
import type { ColumnsType } from "antd/es/table";
import { ClearOutlined } from "@ant-design/icons";
import { Link } from "react-router-dom";
import { Card, CardHeader } from "../../components/ui/Layout";
import { EmptyState, ErrorBanner, Loading } from "../../components/ui/Feedback";
import { FormatBadge, Badge } from "../../components/ui/Badge";
import {
  ConsoleTable,
  FilterBar,
  FilterField,
} from "../../components/ui/ConsolePrimitives";
import { usePreferences } from "../../lib/preferences";
import { grantRowKey } from "./grantDraft";
import { scopeLabel, type GrantRow } from "./accessModel";
import { Principal } from "./PrincipalLabel";

/** Every repository grant in one filterable list; edits happen on the repository. */
export function GrantsPanel({
  rows,
  error,
}: {
  rows: GrantRow[] | null;
  error: unknown;
}) {
  const { locale, text } = usePreferences();
  const english = locale === "en-US";
  const [principalFilter, setPrincipalFilter] = useState("");
  const [repoFilter, setRepoFilter] = useState("");
  const [scopeFilter, setScopeFilter] = useState<
    "all" | "read" | "write" | "admin"
  >("all");
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
          className="text-xs text-[var(--ag-link)] hover:text-[var(--ag-link-hover)]"
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
              <span className="ml-2 text-xs text-[var(--ag-status-danger)]">
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
        <span className="font-mono text-xs text-fg-tertiary">
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
          className="text-xs text-[var(--ag-content-tertiary)] hover:text-[var(--ag-link-hover)]"
        >
          {text("编辑 →", "Edit →")}
        </Link>
      ),
    },
  ];

  return (
    <Card bodyClassName="p-0">
      <CardHeader
        title={text(
          `逐仓库授权（${filtered.length}）`,
          `${filtered.length} repository grants`,
        )}
      />
      <FilterBar
        embedded
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
              {
                value: "all",
                label: text("全部权限", "All permissions"),
              },
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
        <ConsoleTable<GrantRow>
          rowKey={(row) =>
            grantRowKey(row.repositoryId, row.principal, row.resourcePrefix)
          }
          dataSource={filtered}
          columns={columns}
          pagination={false}
          scroll={{ x: 1050, y: 480 }}
        />
      )}
    </Card>
  );
}
