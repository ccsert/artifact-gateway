import { useState } from "react";
import { SearchOutlined, StopOutlined } from "@ant-design/icons";
import { Button, Input, Select } from "antd";
import type { ColumnsType } from "antd/es/table";
import { revokeApiKey } from "../../client";
import type { ApiKey, CreatedApiKey } from "../../client";
import { PageHeader, Card } from "../../components/ui/Layout";
import { Loading, ErrorBanner, EmptyState } from "../../components/ui/Feedback";
import { StateBadge, Badge } from "../../components/ui/Badge";
import { ConfirmDialog } from "../../components/ui/Modal";
import { OneTimeTokenDialog } from "../../components/ui/OneTimeTokenDialog";
import { formatDate } from "../../lib/format";
import {
  ConsoleTable,
  CopyableValue,
  FilterBar,
  FilterField,
  MetricStrip,
} from "../../components/ui/ConsolePrimitives";
import { usePreferences } from "../../lib/preferences";
import { ApiKeyCreateDialog } from "./ApiKeyCreateDialog";
import { useApiKeys, useInvalidateApiKeys } from "./apiKeyQueries";

export function ApiKeysPage() {
  const { locale, t } = usePreferences();
  const keysQuery = useApiKeys();
  const invalidateKeys = useInvalidateApiKeys();
  const keys: ApiKey[] | null = keysQuery.data?.items ?? null;
  const [mutationError, setError] = useState<unknown>(null);
  const error = mutationError ?? keysQuery.error ?? null;
  const [reveal, setReveal] = useState<CreatedApiKey | null>(null);
  const [toRevoke, setToRevoke] = useState<ApiKey | null>(null);
  const [revoking, setRevoking] = useState(false);
  const [filter, setFilter] = useState("");
  const [stateFilter, setStateFilter] = useState<
    "all" | "active" | "expired" | "revoked"
  >("active");

  const load = () => {
    setError(null);
    void invalidateKeys();
  };

  const confirmRevoke = async () => {
    if (!toRevoke) return;
    setRevoking(true);
    try {
      const { error: err } = await revokeApiKey({
        path: { apiKeyId: toRevoke.id },
      });
      if (!err) {
        setToRevoke(null);
        load();
      } else {
        setError(err);
      }
    } catch (nextError) {
      setError(nextError);
    } finally {
      setRevoking(false);
    }
  };

  const isExpired = (key: ApiKey) =>
    !!key.expiresAt && new Date(key.expiresAt).getTime() <= Date.now();
  const keyState = (key: ApiKey) =>
    key.revokedAt ? "revoked" : isExpired(key) ? "expired" : "active";
  const visibleKeys = (keys ?? []).filter(
    (key) =>
      (!filter ||
        key.name.toLowerCase().includes(filter.toLowerCase()) ||
        key.roles.some((role) => role.includes(filter.toLowerCase()))) &&
      (stateFilter === "all" || keyState(key) === stateFilter),
  );
  const activeKeys = (keys ?? []).filter((key) => keyState(key) === "active");
  const adminKeys = activeKeys.filter((key) => key.roles.includes("admin"));
  const columns: ColumnsType<ApiKey> = [
    {
      title: t("identity.name"),
      dataIndex: "name",
      key: "name",
      width: 210,
      render: (name: string) => (
        <span className="font-medium text-fg-strong">{name}</span>
      ),
    },
    {
      title: t("identity.roles"),
      dataIndex: "roles",
      key: "roles",
      width: 210,
      render: (roles: ApiKey["roles"]) => (
        <div className="flex flex-wrap gap-1">
          {roles.map((role) => (
            <Badge
              key={role}
              tone={role === "admin" ? "visualization-3" : "visualization-1"}
            >
              {role}
            </Badge>
          ))}
        </div>
      ),
    },
    {
      title: t("identity.status"),
      key: "state",
      width: 130,
      render: (_value, key) => <StateBadge state={keyState(key)} />,
    },
    {
      title: t("identity.created"),
      dataIndex: "createdAt",
      key: "createdAt",
      width: 170,
      render: (value: string) => (
        <span className="whitespace-nowrap text-xs text-fg-tertiary">
          {formatDate(value, locale)}
        </span>
      ),
    },
    {
      title: t("identity.expires"),
      dataIndex: "expiresAt",
      key: "expiresAt",
      width: 170,
      render: (value: string | undefined) => (
        <span className="whitespace-nowrap text-xs text-fg-tertiary">
          {formatDate(value, locale)}
        </span>
      ),
    },
    {
      title: t("identity.lastUsed"),
      dataIndex: "lastUsedAt",
      key: "lastUsedAt",
      width: 170,
      render: (value: string | undefined) => (
        <span className="whitespace-nowrap text-xs text-fg-tertiary">
          {formatDate(value, locale)}
        </span>
      ),
    },
    {
      title: "ID",
      dataIndex: "id",
      key: "id",
      width: 145,
      render: (id: string) => (
        <CopyableValue
          value={id}
          label={`${id.slice(0, 8)}…`}
          className="text-xs text-fg-tertiary"
        />
      ),
    },
    {
      title: t("identity.actions"),
      key: "actions",
      width: 82,
      align: "right",
      render: (_value, key) =>
        !key.revokedAt && !isExpired(key) ? (
          <Button
            type="text"
            size="small"
            danger
            icon={<StopOutlined />}
            aria-label={t("identity.revokeKey", { key: String(key.name) })}
            onClick={() => setToRevoke(key)}
          />
        ) : null,
    },
  ];

  return (
    <div className="ag-page-stack">
      <PageHeader
        title={t("identity.apiKeys")}
        description={t(
          "identity.manageMemberAndAdminCredentialsForManagementApis",
        )}
        actions={<ApiKeyCreateDialog onCreated={setReveal} />}
      />
      {error !== null && !keys ? (
        <ErrorBanner error={error} onRetry={load} />
      ) : !keys ? (
        <Loading />
      ) : keys.length === 0 ? (
        <Card>
          <EmptyState
            title={t("identity.noApiKeys")}
            hint={t("identity.createAKeyForScriptsAndCiToCall")}
          />
        </Card>
      ) : (
        <>
          {error !== null ? <ErrorBanner error={error} onRetry={load} /> : null}
          <MetricStrip
            items={[
              {
                label: t("identity.activeKeys"),
                value: activeKeys.length,
                hint: t("identity.canCallManagementApis"),
                tone: "success",
              },
              {
                label: t("identity.adminKeys"),
                value: adminKeys.length,
                hint: adminKeys.length
                  ? t("identity.rotateAndReviewRegularly")
                  : t("identity.noElevatedKeys"),
                tone: adminKeys.length ? "danger" : "default",
              },
              {
                label: t("identity.revokedExpired"),
                value: keys.length - activeKeys.length,
                hint: t("identity.retainedForAuditHistory"),
              },
            ]}
          />
          <Card>
            <FilterBar
              className="border-x-0 border-t-0 rounded-none"
              actions={
                filter || stateFilter !== "active" ? (
                  <Button
                    type="text"
                    onClick={() => {
                      setFilter("");
                      setStateFilter("active");
                    }}
                  >
                    {t("identity.clearFilters")}
                  </Button>
                ) : undefined
              }
            >
              <FilterField
                label={t("identity.search")}
                className="min-w-[280px]"
              >
                <Input
                  allowClear
                  prefix={<SearchOutlined />}
                  placeholder={t("identity.searchNamesOrRoles")}
                  value={filter}
                  onChange={(e) => setFilter(e.target.value)}
                />
              </FilterField>
              <FilterField
                label={t("identity.status")}
                className="min-w-[160px]"
              >
                <Select<typeof stateFilter>
                  className="w-full"
                  value={stateFilter}
                  onChange={setStateFilter}
                  options={[
                    { value: "all", label: t("identity.allStatuses") },
                    { value: "active", label: t("identity.active") },
                    { value: "expired", label: t("identity.expired") },
                    { value: "revoked", label: t("identity.revoked") },
                  ]}
                />
              </FilterField>
            </FilterBar>
            {visibleKeys.length === 0 ? (
              <EmptyState
                title={t("identity.noMatchingKeys")}
                hint={t("identity.adjustTheFiltersAndTryAgain")}
              />
            ) : (
              <ConsoleTable<ApiKey>
                rowKey="id"
                dataSource={visibleKeys}
                columns={columns}
                pagination={false}
                scroll={{ x: 1250 }}
              />
            )}
          </Card>
        </>
      )}
      {reveal && (
        <OneTimeTokenDialog
          title={t("identity.apiKeyCreatedSaveTheTokenNow")}
          warningTitle={t("identity.thisIsTheOnlyPlaintextTokenDisplay")}
          warningDescription={t(
            "identity.itCannotBeViewedAgainAfterClosingStoreIt",
          )}
          token={reveal.token}
          doneLabel={t("identity.iSavedIt")}
          onDone={() => {
            setReveal(null);
            load();
          }}
        />
      )}
      <ConfirmDialog
        open={!!toRevoke}
        title={t("identity.revokeApiKey")}
        message={
          <>
            {t("identity.revokeKey2")}{" "}
            <span className="font-mono text-fg-strong">{toRevoke?.name}</span>{" "}
            {t("identity.itsTokenBecomesInvalidImmediately")}
          </>
        }
        confirmLabel={t("identity.revoke")}
        danger
        busy={revoking}
        onConfirm={confirmRevoke}
        onClose={() => setToRevoke(null)}
      />
    </div>
  );
}
