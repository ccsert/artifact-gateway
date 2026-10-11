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
  const { locale, text } = usePreferences();
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
      title: text("订阅", "Subscription"),
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
      title: text("事件", "Event"),
      dataIndex: "eventType",
      key: "eventType",
      width: 190,
      render: (eventType: WebhookEventType) => (
        <span className="font-mono text-xs text-fg-secondary">{eventType}</span>
      ),
    },
    {
      title: text("状态", "State"),
      dataIndex: "state",
      key: "state",
      width: 110,
      render: (state: WebhookDeliveryState) => <StateBadge state={state} />,
    },
    {
      title: text("尝试", "Attempts"),
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
      title: text("最近结果", "Last result"),
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
      title: text("更新时间", "Updated"),
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
      title: text("操作", "Actions"),
      key: "actions",
      fixed: "right",
      width: 90,
      render: (_, delivery) =>
        delivery.state === "dead" ? (
          <Tooltip title={text("重放投递", "Replay delivery")}>
            <Button
              size="small"
              icon={<RedoOutlined />}
              aria-label={text("重放投递", "Replay delivery")}
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
        title={text("最近投递", "Recent deliveries")}
        extra={
          <Select<WebhookDeliveryState | "all">
            aria-label={text("投递状态", "Delivery state")}
            value={stateFilter}
            className="w-40"
            options={[
              { value: "all", label: text("全部状态", "All states") },
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
          <Loading label={text("加载投递记录…", "Loading deliveries…")} />
        )
      ) : deliveries.length === 0 ? (
        <EmptyState
          compact
          title={text("暂无投递记录", "No deliveries yet")}
          hint={text(
            "订阅匹配到制品隔离或解除隔离事件后，记录会显示在这里。",
            "Matching artifact quarantine events appear here after delivery is scheduled.",
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
