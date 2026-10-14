import type { Translate } from "../../lib/preferences";
import { ReloadOutlined } from "@ant-design/icons";
import { Alert, Button, Tooltip } from "antd";
import type { ColumnsType } from "antd/es/table";
import type { RuntimeNode } from "../../client";
import { formatDate } from "../../lib/format";
import { FormatBadge, StateBadge } from "../../components/ui/Badge";
import { EmptyState, ErrorBanner, Loading } from "../../components/ui/Feedback";
import { Card, CardHeader } from "../../components/ui/Layout";
import { usePreferences } from "../../lib/preferences";
import { ConsoleTable } from "../../components/ui/ConsolePrimitives";
import { useRuntimeNodes } from "./systemQueries";

function runtimeNodeColumns(
  locale: string,
  t: Translate,
  currentSessionId: string | null,
): ColumnsType<RuntimeNode> {
  return [
    {
      title: t("operations.instance2"),
      dataIndex: "instanceId",
      key: "instanceId",
      width: 190,
      render: (value: string, node) => (
        <div className="min-w-0">
          <div className="font-mono text-xs text-fg">{value}</div>
          {node.sessionId === currentSessionId && (
            <div className="text-xs text-[var(--ag-status-success)]">
              {t("operations.connectedNode")}
            </div>
          )}
          <div
            className="truncate text-xs text-fg-disabled"
            title={node.sessionId}
          >
            {t("operations.session")} {node.sessionId.slice(0, 12)}…
          </div>
        </div>
      ),
    },
    {
      title: t("operations.buildVersion"),
      key: "build",
      width: 190,
      render: (_, node) => (
        <div className="text-xs">
          <div className="text-fg">
            {node.version && node.version !== "unknown"
              ? node.version === "dev"
                ? t("operations.developmentBuild")
                : node.version
              : t("operations.versionUnknown")}
          </div>
          <div className="font-mono text-fg-tertiary">
            {node.revision && node.revision !== "unknown"
              ? node.revision.slice(0, 12)
              : t("operations.revisionUnknown")}
          </div>
        </div>
      ),
    },
    {
      title: t("operations.status"),
      dataIndex: "status",
      key: "status",
      width: 100,
      render: (value: RuntimeNode["status"]) => <StateBadge state={value} />,
    },
    {
      title: t("operations.roles"),
      dataIndex: "roles",
      key: "roles",
      width: 150,
      render: (roles: string[]) => (
        <span className="text-xs text-fg-secondary">{roles.join(" · ")}</span>
      ),
    },
    {
      title: t("operations.workerCapabilities"),
      key: "capabilities",
      width: 330,
      render: (_, node) => (
        <div className="flex flex-wrap items-center gap-1.5">
          {node.workerFormats.length > 0 ? (
            node.workerFormats.map((format) => (
              <FormatBadge key={format} format={format} />
            ))
          ) : (
            <span className="text-xs text-fg-disabled">
              {t("operations.noFormatWorker")}
            </span>
          )}
          {node.workerKinds.length > 0 && (
            <span className="ml-1 text-xs text-fg-tertiary">
              {node.workerKinds.join(" · ")}
            </span>
          )}
        </div>
      ),
    },
    {
      title: t("operations.started2"),
      dataIndex: "startedAt",
      key: "startedAt",
      width: 190,
      render: (value: string) => (
        <span className="whitespace-nowrap text-xs text-fg-tertiary">
          {formatDate(value, locale)}
        </span>
      ),
    },
    {
      title: t("operations.lastHeartbeat"),
      dataIndex: "lastSeenAt",
      key: "lastSeenAt",
      width: 190,
      render: (value: string, node) =>
        node.stoppedAt ? (
          <span className="whitespace-nowrap text-xs text-fg-tertiary">
            {t("operations.stopped")} {formatDate(node.stoppedAt, locale)}
          </span>
        ) : (
          <span className="whitespace-nowrap text-xs text-fg-tertiary">
            {formatDate(value, locale)}
          </span>
        ),
    },
  ];
}

// Older persisted node rows may contain NULL for fields introduced after the
// initial runtime inventory schema. Normalize those responses before they
// reach table renderers so a single legacy row cannot take down Operations.
export function RuntimeNodesPanel({
  pollIntervalMs = 15_000,
}: {
  pollIntervalMs?: number;
}) {
  const { locale, t } = usePreferences();
  const nodesQuery = useRuntimeNodes(pollIntervalMs);
  const nodes = nodesQuery.data?.nodes ?? null;
  const health = nodesQuery.data?.health ?? null;
  const nodeColumns = runtimeNodeColumns(
    locale,
    t,
    nodesQuery.data?.currentSessionId ?? null,
  );
  const error = nodesQuery.error;
  const refreshing = nodesQuery.isFetching && nodes !== null;
  const load = () => void nodesQuery.refetch();

  return (
    <Card className="ag-runtime-nodes-card">
      <CardHeader
        title={t("operations.runtimeNodes")}
        extra={
          <div className="flex items-center gap-2">
            {health && <StateBadge state={health.status} />}
            <span className="text-xs text-fg-tertiary">
              {nodes
                ? t("operations.instances", { length: nodes.length })
                : t("operations.loading")}
            </span>
            <Tooltip title={t("operations.refreshRuntimeNodes")}>
              <Button
                aria-label={t("operations.refreshRuntimeNodes")}
                type="text"
                icon={<ReloadOutlined />}
                loading={refreshing}
                onClick={() => void load()}
              />
            </Tooltip>
          </div>
        }
      />
      {health && (health.issues ?? []).length > 0 && (
        <div className="px-5 pt-4">
          <Alert
            className="ag-feedback-enter"
            type={health.status === "critical" ? "error" : "warning"}
            showIcon
            title={t("operations.clusterCapabilitiesNeedAttention")}
            description={
              <div className="space-y-1">
                {(health.issues ?? []).map((issue) => (
                  <div key={issue.code}>
                    <span className="font-mono text-xs text-fg-tertiary">
                      {issue.code}
                    </span>
                    <span className="ml-2">{issue.message}</span>
                    {issue.affectedNodes?.length ? (
                      <div className="break-all font-mono text-xs text-fg-tertiary">
                        {t("operations.sessions")}:{" "}
                        {issue.affectedNodes.join(", ")}
                      </div>
                    ) : null}
                  </div>
                ))}
              </div>
            }
          />
        </div>
      )}
      {nodes !== null && (
        <p className="px-5 pt-3 text-xs text-fg-tertiary">
          {t("operations.noReleaseSourceIsConfiguredSoTheLatest")}
        </p>
      )}
      {error ? (
        <ErrorBanner error={error} onRetry={load} />
      ) : nodes === null ? (
        <div className="px-5 py-6">
          <Loading label={t("operations.loadingRuntimeNodes")} />
        </div>
      ) : nodes.length === 0 ? (
        <div className="px-5 py-6">
          <EmptyState
            title={t("operations.noNodeHeartbeatsReceived")}
            hint={t("operations.nodesAppearAutomaticallyAfterStartup")}
          />
        </div>
      ) : (
        <ConsoleTable<RuntimeNode>
          rowKey={(node) => node.sessionId}
          dataSource={nodes}
          columns={nodeColumns}
          pagination={false}
          scroll={{ x: 1260, y: 260 }}
        />
      )}
    </Card>
  );
}
