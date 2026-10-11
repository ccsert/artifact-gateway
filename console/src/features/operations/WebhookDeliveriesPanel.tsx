import { useState } from "react";
import { EditOutlined, PlusOutlined, ReloadOutlined } from "@ant-design/icons";
import { Button, Space, Switch, Tooltip } from "antd";
import type { ColumnsType } from "antd/es/table";
import { updateWebhookSubscription } from "../../client";
import type { WebhookEventType, WebhookSubscription } from "../../client";
import { usePreferences } from "../../lib/preferences";
import { Badge } from "../../components/ui/Badge";
import { EmptyState, ErrorBanner, Loading } from "../../components/ui/Feedback";
import { Card, CardHeader } from "../../components/ui/Layout";
import { ConsoleTable } from "../../components/ui/ConsolePrimitives";
import {
  useInvalidateWebhooks,
  useWebhookSubscriptions,
} from "./webhookQueries";
import { SubscriptionEditorDialog } from "./SubscriptionEditorDialog";
import { WebhookDeliveryLog } from "./WebhookDeliveryLog";

/** Webhook subscriptions and their recent deliveries. */
export function WebhookDeliveriesPanel() {
  const { text } = usePreferences();
  const subscriptionsQuery = useWebhookSubscriptions();
  const subscriptions = subscriptionsQuery.data ?? null;
  const loading = subscriptionsQuery.isFetching;
  const invalidate = useInvalidateWebhooks();
  const [actionError, setActionError] = useState<unknown>(null);
  const error = actionError ?? subscriptionsQuery.error;
  const [busyId, setBusyId] = useState<string | null>(null);
  // Each opening gets a new session so the dialog starts from its
  // subscription while a closing dialog keeps its content through the exit
  // animation.
  const [editor, setEditor] = useState<{
    session: number;
    open: boolean;
    subscription: WebhookSubscription | null;
  } | null>(null);

  const load = () => {
    setActionError(null);
    return invalidate();
  };

  const openEditor = (subscription: WebhookSubscription | null) => {
    setActionError(null);
    setEditor((current) => ({
      session: (current?.session ?? 0) + 1,
      open: true,
      subscription,
    }));
  };
  const openCreate = () => openEditor(null);
  const openEdit = (subscription: WebhookSubscription) =>
    openEditor(subscription);

  const setEnabled = async (
    subscription: WebhookSubscription,
    enabled: boolean,
  ) => {
    setBusyId(subscription.id);
    setActionError(null);
    const result = await updateWebhookSubscription({
      path: { subscriptionId: subscription.id },
      headers: { "If-Match": subscription.version },
      body: {
        name: subscription.name,
        endpointUrl: subscription.endpointUrl,
        eventTypes: subscription.eventTypes,
        enabled,
      },
    });
    setBusyId(null);
    if (result.error) {
      setActionError(result.error);
      return;
    }
    await invalidate();
  };

  const subscriptionColumns: ColumnsType<WebhookSubscription> = [
    {
      title: text("订阅", "Subscription"),
      key: "name",
      width: 210,
      render: (_, subscription) => (
        <div className="min-w-0">
          <div className="truncate text-sm font-medium text-fg-strong">
            {subscription.name}
          </div>
          <div className="mt-1 text-xs text-fg-disabled">
            v{subscription.version}
          </div>
        </div>
      ),
    },
    {
      title: text("接收地址", "Endpoint"),
      dataIndex: "endpointUrl",
      key: "endpointUrl",
      width: 340,
      ellipsis: true,
      render: (value: string) => (
        <span className="font-mono text-xs text-fg-secondary" title={value}>
          {value}
        </span>
      ),
    },
    {
      title: text("事件", "Events"),
      dataIndex: "eventTypes",
      key: "eventTypes",
      width: 260,
      render: (eventTypes: WebhookEventType[]) => (
        <Space size={[4, 4]} wrap>
          {eventTypes.map((eventType) => (
            <Badge key={eventType} tone="visualization-6">
              {eventType}
            </Badge>
          ))}
        </Space>
      ),
    },
    {
      title: text("签名", "Signing"),
      dataIndex: "secretConfigured",
      key: "secretConfigured",
      width: 100,
      render: (configured: boolean) => (
        <Badge tone={configured ? "success" : "danger"}>
          {configured ? text("已配置", "Configured") : text("缺失", "Missing")}
        </Badge>
      ),
    },
    {
      title: text("启用", "Enabled"),
      dataIndex: "enabled",
      key: "enabled",
      width: 90,
      render: (enabled: boolean, subscription) => (
        <Switch
          size="small"
          value={enabled}
          loading={busyId === subscription.id}
          aria-label={text("启用 Webhook 订阅", "Enable webhook subscription")}
          onChange={(value) => void setEnabled(subscription, value)}
        />
      ),
    },
    {
      title: text("操作", "Actions"),
      key: "actions",
      fixed: "right",
      width: 80,
      render: (_, subscription) => (
        <Tooltip title={text("编辑订阅", "Edit subscription")}>
          <Button
            size="small"
            icon={<EditOutlined />}
            aria-label={text("编辑订阅", "Edit subscription")}
            onClick={() => openEdit(subscription)}
          />
        </Tooltip>
      ),
    },
  ];

  return (
    <div className="ag-page-stack">
      {error ? <ErrorBanner error={error} onRetry={load} /> : null}
      <Card>
        <CardHeader
          title={text("Webhook 订阅", "Webhook subscriptions")}
          extra={
            <Space>
              <Button
                icon={<ReloadOutlined />}
                loading={loading}
                onClick={() => void load()}
              >
                {text("刷新", "Refresh")}
              </Button>
              <Button
                type="primary"
                icon={<PlusOutlined />}
                aria-label={text("新建订阅", "New subscription")}
                onClick={openCreate}
              >
                {text("新建订阅", "New subscription")}
              </Button>
            </Space>
          }
        />
        {subscriptions === null ? (
          // A first load in flight is the only case that shows a placeholder:
          // once it fails, the banner above is the whole answer.
          error ? null : (
            <Loading label={text("加载 Webhook 订阅…", "Loading webhooks…")} />
          )
        ) : subscriptions.length === 0 ? (
          <EmptyState
            title={text("还没有 Webhook 订阅", "No webhook subscriptions yet")}
            hint={text(
              "创建订阅，将隔离状态变更可靠投递给自动化系统。",
              "Create a subscription to deliver quarantine state changes to automation systems.",
            )}
          />
        ) : (
          <ConsoleTable<WebhookSubscription>
            rowKey="id"
            dataSource={subscriptions}
            columns={subscriptionColumns}
            pagination={false}
            scroll={{ x: 1080 }}
          />
        )}
      </Card>

      <WebhookDeliveryLog subscriptions={subscriptions} />
      {editor && (
        <SubscriptionEditorDialog
          key={editor.session}
          open={editor.open}
          subscription={editor.subscription}
          onClose={() =>
            setEditor((current) => current && { ...current, open: false })
          }
        />
      )}
    </div>
  );
}
