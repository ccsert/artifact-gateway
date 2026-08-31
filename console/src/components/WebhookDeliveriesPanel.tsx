import { useCallback, useEffect, useMemo, useState } from "react";
import {
  EditOutlined,
  PlusOutlined,
  RedoOutlined,
  ReloadOutlined,
} from "@ant-design/icons";
import { Button, Input, Select, Space, Switch, Table, Tooltip } from "antd";
import type { ColumnsType } from "antd/es/table";
import {
  createWebhookSubscription,
  listWebhookDeliveries,
  listWebhookSubscriptions,
  replayWebhookDelivery,
  updateWebhookSubscription,
} from "../client";
import type {
  CreateWebhookSubscriptionWritable,
  UpdateWebhookSubscriptionWritable,
  WebhookDelivery,
  WebhookDeliveryState,
  WebhookEventType,
  WebhookSubscription,
} from "../client";
import { usePreferences } from "../lib/preferences";
import { formatDate } from "../lib/format";
import { Badge, StateBadge } from "./Badge";
import { EmptyState, ErrorBanner, Loading } from "./Feedback";
import { Card, CardHeader, Field } from "./Layout";
import { Modal, useDisclosure } from "./Modal";

type SubscriptionForm = {
  name: string;
  endpointUrl: string;
  secret: string;
  eventTypes: WebhookEventType[];
  enabled: boolean;
};

const DEFAULT_FORM: SubscriptionForm = {
  name: "",
  endpointUrl: "",
  secret: "",
  eventTypes: ["artifact.quarantined", "artifact.released"],
  enabled: true,
};

const DELIVERY_STATES: WebhookDeliveryState[] = [
  "pending",
  "delivering",
  "retrying",
  "succeeded",
  "dead",
];

export function WebhookDeliveriesPanel() {
  const { locale, text } = usePreferences();
  const dialog = useDisclosure();
  const [subscriptions, setSubscriptions] = useState<
    WebhookSubscription[] | null
  >(null);
  const [deliveries, setDeliveries] = useState<WebhookDelivery[] | null>(null);
  const [stateFilter, setStateFilter] = useState<WebhookDeliveryState | "all">(
    "all",
  );
  const [error, setError] = useState<unknown>(null);
  const [loading, setLoading] = useState(false);
  const [busyId, setBusyId] = useState<string | null>(null);
  const [editing, setEditing] = useState<WebhookSubscription | null>(null);
  const [form, setForm] = useState<SubscriptionForm>(DEFAULT_FORM);

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const [subscriptionResult, deliveryResult] = await Promise.all([
        listWebhookSubscriptions(),
        listWebhookDeliveries({
          query: {
            state: stateFilter === "all" ? undefined : stateFilter,
            limit: 200,
          },
        }),
      ]);
      if (subscriptionResult.error) throw subscriptionResult.error;
      if (deliveryResult.error) throw deliveryResult.error;
      setSubscriptions(subscriptionResult.data ?? []);
      setDeliveries(deliveryResult.data ?? []);
    } catch (nextError) {
      setError(nextError);
    } finally {
      setLoading(false);
    }
  }, [stateFilter]);

  useEffect(() => {
    void load();
  }, [load]);

  const subscriptionById = useMemo(
    () => new Map((subscriptions ?? []).map((item) => [item.id, item])),
    [subscriptions],
  );

  const openCreate = () => {
    setEditing(null);
    setForm(DEFAULT_FORM);
    setError(null);
    dialog.show();
  };

  const openEdit = (subscription: WebhookSubscription) => {
    setEditing(subscription);
    setForm({
      name: subscription.name,
      endpointUrl: subscription.endpointUrl,
      secret: "",
      eventTypes: subscription.eventTypes,
      enabled: subscription.enabled,
    });
    setError(null);
    dialog.show();
  };

  const save = async () => {
    setBusyId(editing?.id ?? "create");
    setError(null);
    const base = {
      name: form.name.trim(),
      endpointUrl: form.endpointUrl.trim(),
      eventTypes: form.eventTypes,
      enabled: form.enabled,
    };
    const result = editing
      ? await updateWebhookSubscription({
          path: { subscriptionId: editing.id },
          headers: { "If-Match": editing.version },
          body: {
            ...base,
            secret: form.secret || undefined,
          } satisfies UpdateWebhookSubscriptionWritable,
        })
      : await createWebhookSubscription({
          body: {
            ...base,
            secret: form.secret,
          } satisfies CreateWebhookSubscriptionWritable,
        });
    setBusyId(null);
    if (result.error) {
      setError(result.error);
      return;
    }
    dialog.hide();
    setEditing(null);
    setForm(DEFAULT_FORM);
    await load();
  };

  const setEnabled = async (
    subscription: WebhookSubscription,
    enabled: boolean,
  ) => {
    setBusyId(subscription.id);
    setError(null);
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
      setError(result.error);
      return;
    }
    await load();
  };

  const replay = async (delivery: WebhookDelivery) => {
    setBusyId(delivery.id);
    setError(null);
    const result = await replayWebhookDelivery({
      path: { deliveryId: delivery.id },
    });
    setBusyId(null);
    if (result.error) {
      setError(result.error);
      return;
    }
    await load();
  };

  const subscriptionColumns: ColumnsType<WebhookSubscription> = [
    {
      title: text("订阅", "Subscription"),
      key: "name",
      width: 210,
      render: (_, subscription) => (
        <div className="min-w-0">
          <div className="truncate text-sm font-medium text-zinc-100">
            {subscription.name}
          </div>
          <div className="mt-1 text-xs text-zinc-600">
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
        <span className="font-mono text-xs text-zinc-400" title={value}>
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

  const deliveryColumns: ColumnsType<WebhookDelivery> = [
    {
      title: text("订阅", "Subscription"),
      dataIndex: "subscriptionId",
      key: "subscription",
      width: 190,
      render: (subscriptionId: string) => (
        <span className="text-xs text-zinc-300">
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
        <span className="font-mono text-xs text-zinc-400">{eventType}</span>
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
        <span className="font-mono text-xs text-zinc-400">{attempts}/8</span>
      ),
    },
    {
      title: text("最近结果", "Last result"),
      key: "result",
      width: 280,
      render: (_, delivery) => (
        <div className="min-w-0 text-xs text-zinc-400">
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
        <span className="whitespace-nowrap text-xs text-zinc-500">
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
          <span className="text-xs text-zinc-600">—</span>
        ),
    },
  ];

  const valid =
    form.name.trim().length > 0 &&
    form.endpointUrl.trim().startsWith("https://") &&
    form.eventTypes.length > 0 &&
    (Boolean(editing) || form.secret.length >= 32) &&
    (!form.secret || form.secret.length >= 32);

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
        {!subscriptions ? (
          <Loading label={text("加载 Webhook 订阅…", "Loading webhooks…")} />
        ) : subscriptions.length === 0 ? (
          <EmptyState
            title={text("还没有 Webhook 订阅", "No webhook subscriptions yet")}
            hint={text(
              "创建订阅，将隔离状态变更可靠投递给自动化系统。",
              "Create a subscription to deliver quarantine state changes to automation systems.",
            )}
            action={
              <Button
                type="primary"
                icon={<PlusOutlined />}
                aria-label={text("新建订阅", "New subscription")}
                onClick={openCreate}
              >
                {text("新建订阅", "New subscription")}
              </Button>
            }
          />
        ) : (
          <Table<WebhookSubscription>
            className="ag-console-table"
            rowKey="id"
            size="middle"
            dataSource={subscriptions}
            columns={subscriptionColumns}
            pagination={false}
            scroll={{ x: 1080 }}
          />
        )}
      </Card>

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
        {!deliveries ? (
          <Loading label={text("加载投递记录…", "Loading deliveries…")} />
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
          <Table<WebhookDelivery>
            className="ag-console-table"
            rowKey="id"
            size="middle"
            dataSource={deliveries}
            columns={deliveryColumns}
            pagination={false}
            scroll={{ x: 1140, y: 480 }}
          />
        )}
      </Card>

      <Modal
        open={dialog.open}
        title={
          editing
            ? text("编辑 Webhook 订阅", "Edit webhook subscription")
            : text("新建 Webhook 订阅", "New webhook subscription")
        }
        onClose={() => {
          dialog.hide();
          setEditing(null);
        }}
        footer={
          <Space>
            <Button
              aria-label={text("取消", "Cancel")}
              onClick={dialog.hide}
              disabled={busyId !== null}
            >
              {text("取消", "Cancel")}
            </Button>
            <Button
              type="primary"
              aria-label={
                editing ? text("保存", "Save") : text("创建", "Create")
              }
              disabled={!valid}
              loading={busyId === (editing?.id ?? "create")}
              onClick={() => void save()}
            >
              {editing ? text("保存", "Save") : text("创建", "Create")}
            </Button>
          </Space>
        }
      >
        <div className="space-y-4">
          {error ? <ErrorBanner error={error} /> : null}
          <Field label={text("订阅名称", "Subscription name")}>
            <Input
              value={form.name}
              maxLength={100}
              placeholder={text("例如：安全自动化", "e.g. Security automation")}
              onChange={(event) =>
                setForm((current) => ({ ...current, name: event.target.value }))
              }
            />
          </Field>
          <Field
            label={text("HTTPS 接收地址", "HTTPS endpoint")}
            hint={text(
              "生产投递会拒绝私网、环回和重定向目标。",
              "Production delivery rejects private, loopback, and redirect targets.",
            )}
          >
            <Input
              value={form.endpointUrl}
              placeholder="https://hooks.example.com/artifacts"
              onChange={(event) =>
                setForm((current) => ({
                  ...current,
                  endpointUrl: event.target.value,
                }))
              }
            />
          </Field>
          <Field
            group
            label={text("订阅事件", "Events")}
            hint={text(
              "首个版本支持制品隔离与解除隔离。",
              "The first version supports artifact quarantine and release events.",
            )}
          >
            <Select<WebhookEventType[]>
              mode="multiple"
              className="w-full"
              value={form.eventTypes}
              options={[
                {
                  value: "artifact.quarantined",
                  label: "artifact.quarantined",
                },
                { value: "artifact.released", label: "artifact.released" },
              ]}
              onChange={(eventTypes) =>
                setForm((current) => ({ ...current, eventTypes }))
              }
            />
          </Field>
          <Field
            label={text("HMAC 签名密钥", "HMAC signing secret")}
            hint={
              editing
                ? text(
                    "留空则保留现有密钥；填写会立即轮换。",
                    "Leave blank to keep the current secret; entering one rotates it immediately.",
                  )
                : text(
                    "至少 32 个字符；保存后不会再次显示。",
                    "At least 32 characters; it is never shown again after saving.",
                  )
            }
          >
            <Input.Password
              value={form.secret}
              autoComplete="new-password"
              placeholder={text("至少 32 个字符", "At least 32 characters")}
              onChange={(event) =>
                setForm((current) => ({
                  ...current,
                  secret: event.target.value,
                }))
              }
            />
          </Field>
          <Field label={text("状态", "Status")}>
            <div className="flex h-8 items-center gap-2">
              <Switch
                value={form.enabled}
                onChange={(enabled) =>
                  setForm((current) => ({ ...current, enabled }))
                }
              />
              <span className="text-xs text-zinc-400">
                {form.enabled
                  ? text("接收新事件", "Receive new events")
                  : text("暂停生成新投递", "Pause new deliveries")}
              </span>
            </div>
          </Field>
        </div>
      </Modal>
    </div>
  );
}
