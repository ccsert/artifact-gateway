import { useMemo, useState } from "react";
import { RedoOutlined } from "@ant-design/icons";
import { Button, Select, Tooltip } from "antd";
import type { ColumnsType } from "antd/es/table";
import { replayWebhookDelivery } from "../../client";
import type {
  WebhookDelivery,
  WebhookDeliveryState,
  WebhookEventType,
  WebhookSubscription,
} from "../../client";
import { StateBadge } from "../../components/ui/Badge";
import { EmptyState, ErrorBanner, Loading } from "../../components/ui/Feedback";
import { Card, CardHeader } from "../../components/ui/Layout";
import { ConsoleTable } from "../../components/ui/ConsolePrimitives";
import { formatDate } from "../../lib/format";
import { usePreferences } from "../../lib/preferences";
import { DELIVERY_STATES } from "./webhookModel";
import { useWebhookDeliveries } from "./webhookQueries";

/** Recent deliveries for one state filter, with replay. */
export function WebhookDeliveryLog({
  subscriptions,
}: {
  subscriptions: WebhookSubscription[] | null;
}) {
  const { locale, t } = usePreferences();
  const [stateFilter, setStateFilter] = useState<WebhookDeliveryState | "all">(
    "all",
  );
  const deliveriesQuery = useWebhookDeliveries(stateFilter);
  const deliveries = deliveriesQuery.data ?? null;
  const [busyId, setBusyId] = useState<string | null>(null);
  const [replayError, setReplayError] = useState<unknown>(null);
  const error = replayError ?? deliveriesQuery.error;
  const subscriptionById = useMemo(
    () => new Map((subscriptions ?? []).map((item) => [item.id, item])),
    [subscriptions],
  );

  const replay = async (delivery: WebhookDelivery) => {
    setBusyId(delivery.id);
    setReplayError(null);
    try {
      const result = await replayWebhookDelivery({
        path: { deliveryId: delivery.id },
      });
      if (result.error) {
        setReplayError(result.error);
        return;
      }
      await deliveriesQuery.refetch();
    } catch (nextError) {
      setReplayError(nextError);
    } finally {
      setBusyId(null);
    }
  };

  const deliveryColumns: ColumnsType<WebhookDelivery> = [
    {
      title: t("operations.subscription"),
      dataIndex: "subscriptionId",
      key: "subscription",
      width: 190,
      render: (subscriptionId: string) => (
        <span className="text-xs text-fg-secondary">
          {subscriptionById.get(subscriptionId)?.name ?? subscriptionId}
        </span>
      ),
    },
    {
      title: t("operations.event"),
      dataIndex: "eventType",
      key: "eventType",
      width: 190,
      render: (eventType: WebhookEventType) => (
        <span className="font-mono text-xs text-fg-secondary">{eventType}</span>
      ),
    },
    {
      title: t("operations.state"),
      dataIndex: "state",
      key: "state",
      width: 110,
      render: (state: WebhookDeliveryState) => <StateBadge state={state} />,
    },
    {
      title: t("operations.attempts2"),
      dataIndex: "attempts",
      key: "attempts",
      width: 90,
      render: (attempts: number) => (
        <span className="font-mono text-xs text-fg-secondary">
          {attempts}/8
        </span>
      ),
    },
    {
      title: t("operations.lastResult"),
      key: "result",
      width: 280,
      render: (_, delivery) => (
        <div className="min-w-0 text-xs text-fg-secondary">
          <div>{delivery.lastStatus ? `HTTP ${delivery.lastStatus}` : "—"}</div>
          {delivery.lastError ? (
            <div
              className="mt-1 truncate text-[var(--ag-status-danger)]"
              title={delivery.lastError}
            >
              {delivery.lastError}
            </div>
          ) : null}
        </div>
      ),
    },
    {
      title: t("operations.updated"),
      dataIndex: "updatedAt",
      key: "updatedAt",
      width: 180,
      render: (value: string) => (
        <span className="whitespace-nowrap text-xs text-fg-tertiary">
          {formatDate(value, locale)}
        </span>
      ),
    },
    {
      title: t("operations.actions"),
      key: "actions",
      fixed: "right",
      width: 90,
      render: (_, delivery) =>
        delivery.state === "dead" ? (
          <Tooltip title={t("operations.replayDelivery")}>
            <Button
              size="small"
              icon={<RedoOutlined />}
              aria-label={t("operations.replayDelivery")}
              loading={busyId === delivery.id}
              onClick={() => void replay(delivery)}
            />
          </Tooltip>
        ) : (
          <span className="text-xs text-fg-disabled">—</span>
        ),
    },
  ];

  return (
    <Card>
      <CardHeader
        title={t("operations.recentDeliveries")}
        extra={
          <Select<WebhookDeliveryState | "all">
            aria-label={t("operations.deliveryState")}
            value={stateFilter}
            className="w-40"
            options={[
              { value: "all", label: t("operations.allStates") },
              ...DELIVERY_STATES.map((state) => ({
                value: state,
                label: state,
              })),
            ]}
            onChange={setStateFilter}
          />
        }
      />
      {error ? (
        <div className="px-5 pb-4">
          <ErrorBanner
            error={error}
            onRetry={() => {
              setReplayError(null);
              void deliveriesQuery.refetch();
            }}
          />
        </div>
      ) : null}
      {!deliveries ? (
        error ? null : (
          <Loading label={t("operations.loadingDeliveries")} />
        )
      ) : deliveries.length === 0 ? (
        <EmptyState
          compact
          title={t("operations.noDeliveriesYet")}
          hint={t(
            "operations.matchingArtifactQuarantineEventsAppearHereAfterDelivery",
          )}
        />
      ) : (
        <ConsoleTable<WebhookDelivery>
          rowKey="id"
          dataSource={deliveries}
          columns={deliveryColumns}
          pagination={false}
          scroll={{ x: 1140, y: 480 }}
        />
      )}
    </Card>
  );
}
