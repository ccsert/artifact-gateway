import { useState } from "react";
import type { Repository } from "../../client";
import { Button, Input, Select } from "antd";
import type { ColumnsType } from "antd/es/table";
import { ClearOutlined } from "@ant-design/icons";
import { Link, useSearchParams } from "react-router-dom";
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
  onRetry,
}: {
  rows: GrantRow[] | null;
  error: unknown;
  onRetry: () => void;
}) {
  const { t } = usePreferences();
  // `?principal=` opens the grants of one principal (linked from a service
  // account); a later change of that parameter replaces the filter.
  // A link names an exact principal; typing keeps the partial-search behavior.
  const [params] = useSearchParams();
  const urlPrincipal = params.get("principal") ?? "";
  const [principalFilter, setPrincipalFilter] = useState({
    value: urlPrincipal,
    exact: Boolean(urlPrincipal),
  });
  const [seededPrincipal, setSeededPrincipal] = useState(urlPrincipal);
  if (seededPrincipal !== urlPrincipal) {
    setSeededPrincipal(urlPrincipal);
    setPrincipalFilter({ value: urlPrincipal, exact: Boolean(urlPrincipal) });
  }
  const [repoFilter, setRepoFilter] = useState("");
  const [scopeFilter, setScopeFilter] = useState<
    "all" | "read" | "write" | "admin"
  >("all");
  const grants = rows ?? [];
  const filtered = grants
    .filter(
      (row) =>
        (!principalFilter.value ||
          (principalFilter.exact
            ? row.principal === principalFilter.value
            : row.principal
                .toLowerCase()
                .includes(principalFilter.value.toLowerCase()))) &&
        (!repoFilter ||
          row.repositoryName.toLowerCase().includes(repoFilter.toLowerCase())),
    )
    .filter(
      (row) =>
        scopeFilter === "all" || scopeLabel(row.scopes, t).key === scopeFilter,
    );
  const hasFilters = Boolean(
    principalFilter.value || repoFilter || scopeFilter !== "all",
  );
  const columns: ColumnsType<GrantRow> = [
    {
      title: t("access.principal2"),
      dataIndex: "principal",
      key: "principal",
      width: 250,
      render: (value: string) => <Principal value={value} />,
    },
    {
      title: t("access.repository"),
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
      title: t("access.format"),
      dataIndex: "format",
      key: "format",
      width: 120,
      render: (value: Repository["format"]) => <FormatBadge format={value} />,
    },
    {
      title: t("access.permission"),
      key: "scope",
      width: 190,
      render: (_, row) => {
        const scope = scopeLabel(row.scopes, t);
        return (
          <span>
            <Badge tone={scope.tone}>{scope.label}</Badge>
            {scope.key === "admin" && (
              <span className="ml-2 text-xs text-[var(--ag-status-danger)]">
                {t("access.elevated")}
              </span>
            )}
          </span>
        );
      },
    },
    {
      title: t("access.resourcePrefix"),
      dataIndex: "resourcePrefix",
      key: "resourcePrefix",
      width: 240,
      render: (value: string) => (
        <span className="font-mono text-xs text-fg-tertiary">
          {value || t("access.entireRepository")}
        </span>
      ),
    },
    {
      title: t("access.actions"),
      key: "actions",
      fixed: "right",
      width: 90,
      render: (_, row) => (
        <Link
          to={`/repositories/${row.repositoryId}?tab=grants`}
          className="text-xs text-[var(--ag-content-tertiary)] hover:text-[var(--ag-link-hover)]"
        >
          {t("access.edit2")}
        </Link>
      ),
    },
  ];

  return (
    <Card bodyClassName="p-0">
      <CardHeader
        title={t("access.repositoryGrants", {
          filtered: String(filtered.length),
        })}
      />
      <FilterBar
        embedded
        actions={
          <Button
            icon={<ClearOutlined />}
            disabled={!hasFilters}
            onClick={() => {
              setPrincipalFilter({ value: "", exact: false });
              setRepoFilter("");
              setScopeFilter("all");
            }}
          >
            {t("access.clear")}
          </Button>
        }
      >
        <FilterField label={t("access.principal")} className="min-w-[260px]">
          <Input
            allowClear
            placeholder={t("access.usernameApiKeyOrActor")}
            value={principalFilter.value}
            onChange={(event) =>
              setPrincipalFilter({ value: event.target.value, exact: false })
            }
          />
        </FilterField>
        <FilterField label={t("access.repository")} className="min-w-[220px]">
          <Input
            allowClear
            placeholder={t("access.repositoryName")}
            value={repoFilter}
            onChange={(event) => setRepoFilter(event.target.value)}
          />
        </FilterField>
        <FilterField
          label={t("access.permissionLevel")}
          className="min-w-[190px]"
        >
          <Select
            className="w-full"
            value={scopeFilter}
            options={[
              {
                value: "all",
                label: t("access.allPermissions"),
              },
              {
                value: "read",
                label: t("access.readBrowsePull"),
              },
              {
                value: "write",
                label: t("access.writePublishEdit"),
              },
              {
                value: "admin",
                label: t("access.adminGrantDelete"),
              },
            ]}
            onChange={(value: typeof scopeFilter) => setScopeFilter(value)}
          />
        </FilterField>
      </FilterBar>
      {error && rows ? (
        <div className="px-4 py-4">
          <ErrorBanner tone="warning" error={error} onRetry={onRetry} />
        </div>
      ) : null}
      {error && !rows ? (
        <div className="px-4 py-4">
          <ErrorBanner error={error} onRetry={onRetry} />
        </div>
      ) : !rows ? (
        <Loading />
      ) : filtered.length === 0 ? (
        <EmptyState
          title={
            rows.length === 0
              ? t("access.noRepositoryGrants")
              : t("access.noMatchingGrants")
          }
          hint={
            rows.length === 0
              ? t("access.addAPrincipalFromARepositorySAccessGrants")
              : t("access.tryAnotherFilter")
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
