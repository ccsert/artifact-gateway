import { useCallback, useEffect, useState } from "react";
import { PlusOutlined, SearchOutlined, StopOutlined } from "@ant-design/icons";
import { Alert, Button, Input, Select, Space, Table, Typography } from "antd";
import type { ColumnsType } from "antd/es/table";
import { listApiKeys, createApiKey, revokeApiKey } from "../client";
import type { ApiKey, CreatedApiKey } from "../client";
import { PageHeader, Card, Field } from "../components/Layout";
import { Loading, ErrorBanner, EmptyState } from "../components/Feedback";
import { StateBadge, Badge } from "../components/Badge";
import { Modal, ConfirmDialog, useDisclosure } from "../components/Modal";
import { formatDate } from "../lib/format";
import {
  CopyableValue,
  FilterBar,
  FilterField,
  MetricStrip,
} from "../components/ConsolePrimitives";
import { usePreferences } from "../lib/preferences";

function CreateKeyDialog({
  onCreated,
}: {
  onCreated: (key: CreatedApiKey) => void;
}) {
  const { text } = usePreferences();
  const dialog = useDisclosure();
  const [name, setName] = useState("");
  const [role, setRole] = useState<"reader" | "writer" | "admin">("reader");
  const [validDays, setValidDays] = useState<30 | 90 | 180 | 365>(90);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);

  const submit = async () => {
    setBusy(true);
    setError(null);
    const { data, error: err } = await createApiKey({
      body: {
        name: name.trim(),
        roles: [role],
        expiresAt: new Date(Date.now() + validDays * 86_400_000).toISOString(),
      },
    });
    setBusy(false);
    if (err) {
      setError(err);
      return;
    }
    if (data) {
      dialog.hide();
      setName("");
      setRole("reader");
      setValidDays(90);
      onCreated(data);
    }
  };

  return (
    <>
      <Button
        type="primary"
        icon={<PlusOutlined />}
        onClick={() => {
          setError(null);
          dialog.show();
        }}
      >
        {text("新建密钥", "New key")}
      </Button>
      <Modal
        open={dialog.open}
        title={text("新建 API 密钥", "New API key")}
        onClose={dialog.hide}
        footer={
          <Space>
            <Button onClick={dialog.hide} disabled={busy}>
              {text("取消", "Cancel")}
            </Button>
            <Button
              type="primary"
              onClick={submit}
              loading={busy}
              disabled={!name.trim()}
            >
              {text("创建", "Create")}
            </Button>
          </Space>
        }
      >
        <div className="space-y-4">
          {error !== null && <ErrorBanner error={error} />}
          <Field
            label={text("密钥名称", "Key name")}
            hint={text(
              "用于标识用途，例如 ci-deploy",
              "Describe its purpose, for example ci-deploy",
            )}
          >
            <Input
              className="font-mono"
              placeholder="my-key"
              value={name}
              onChange={(e) => setName(e.target.value)}
            />
          </Field>
          <Field
            label={text("角色", "Role")}
            hint={text(
              "按最小权限选择。角色在全仓库范围内生效，优先于逐仓库授权。",
              "Choose the least privilege. This global role applies across repositories before repository grants.",
            )}
          >
            <Select<typeof role>
              className="w-full"
              value={role}
              onChange={setRole}
              options={[
                {
                  value: "reader",
                  label: text(
                    "reader · 只读（浏览 / 搜索 / 拉取）",
                    "reader · browse / search / pull",
                  ),
                },
                {
                  value: "writer",
                  label: text(
                    "writer · 读写（可发布 / 编辑，不可管理用户与密钥）",
                    "writer · publish / edit, no user or key management",
                  ),
                },
                {
                  value: "admin",
                  label: text(
                    "admin · 管理员（全部权限）",
                    "admin · all permissions",
                  ),
                },
              ]}
            />
          </Field>
          <Field
            label={text("有效期", "Validity")}
            hint={text(
              "到期后密钥会自动拒绝认证，无需手动吊销。",
              "Authentication is rejected automatically after expiry.",
            )}
          >
            <Select<typeof validDays>
              className="w-full"
              value={validDays}
              onChange={setValidDays}
              options={[
                { value: 30, label: text("30 天", "30 days") },
                {
                  value: 90,
                  label: text("90 天（推荐）", "90 days (recommended)"),
                },
                { value: 180, label: text("180 天", "180 days") },
                { value: 365, label: text("365 天", "365 days") },
              ]}
            />
          </Field>
          <p className="text-xs text-zinc-500">
            {text(
              "创建后只会显示一次明文 Token，请立即保存。",
              "The plaintext token is shown once. Save it immediately.",
            )}
          </p>
        </div>
      </Modal>
    </>
  );
}

function TokenReveal({
  tokenKey,
  onDone,
}: {
  tokenKey: CreatedApiKey;
  onDone: () => void;
}) {
  const { text } = usePreferences();
  return (
    <Modal
      open
      onClose={onDone}
      title={text(
        "密钥已创建：请立即保存 Token",
        "API key created: save the token now",
      )}
    >
      <div className="space-y-3">
        <Alert
          type="warning"
          showIcon
          title={text(
            "这是唯一一次显示明文 Token",
            "This is the only plaintext token display",
          )}
          description={text(
            "关闭弹窗后将无法再次查看，请立即复制并保存到安全位置。",
            "It cannot be viewed again after closing. Store it securely now.",
          )}
        />
        <div className="rounded-lg border border-zinc-700 bg-zinc-950 p-3">
          <Typography.Text
            className="block break-all font-mono text-xs"
            copyable={{
              text: tokenKey.token,
              tooltips: [
                text("复制 Token", "Copy token"),
                text("已复制", "Copied"),
              ],
            }}
          >
            {tokenKey.token}
          </Typography.Text>
        </div>
        <div className="flex justify-end">
          <Button type="primary" onClick={onDone}>
            {text("我已保存", "I saved it")}
          </Button>
        </div>
      </div>
    </Modal>
  );
}

export function ApiKeysPage() {
  const { locale, text } = usePreferences();
  const [keys, setKeys] = useState<ApiKey[] | null>(null);
  const [error, setError] = useState<unknown>(null);
  const [reveal, setReveal] = useState<CreatedApiKey | null>(null);
  const [toRevoke, setToRevoke] = useState<ApiKey | null>(null);
  const [revoking, setRevoking] = useState(false);
  const [filter, setFilter] = useState("");
  const [stateFilter, setStateFilter] = useState<
    "all" | "active" | "expired" | "revoked"
  >("active");

  const load = useCallback(async () => {
    setError(null);
    const { data, error: err } = await listApiKeys();
    if (err) {
      setError(err);
      return;
    }
    setKeys(data?.items ?? []);
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  const confirmRevoke = async () => {
    if (!toRevoke) return;
    setRevoking(true);
    const { error: err } = await revokeApiKey({
      path: { apiKeyId: toRevoke.id },
    });
    setRevoking(false);
    if (!err) {
      setToRevoke(null);
      void load();
    } else {
      setError(err);
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
      title: text("名称", "Name"),
      dataIndex: "name",
      key: "name",
      width: 210,
      render: (name: string) => (
        <span className="font-medium text-zinc-100">{name}</span>
      ),
    },
    {
      title: text("角色", "Roles"),
      dataIndex: "roles",
      key: "roles",
      width: 210,
      render: (roles: ApiKey["roles"]) => (
        <div className="flex flex-wrap gap-1">
          {roles.map((role) => (
            <Badge
              key={role}
              tone={
                role === "admin" ? "red" : role === "writer" ? "blue" : "green"
              }
            >
              {role}
            </Badge>
          ))}
        </div>
      ),
    },
    {
      title: text("状态", "Status"),
      key: "state",
      width: 130,
      render: (_value, key) => <StateBadge state={keyState(key)} />,
    },
    {
      title: text("创建时间", "Created"),
      dataIndex: "createdAt",
      key: "createdAt",
      width: 170,
      render: (value: string) => (
        <span className="whitespace-nowrap text-xs text-zinc-500">
          {formatDate(value, locale)}
        </span>
      ),
    },
    {
      title: text("到期时间", "Expires"),
      dataIndex: "expiresAt",
      key: "expiresAt",
      width: 170,
      render: (value: string | undefined) => (
        <span className="whitespace-nowrap text-xs text-zinc-500">
          {formatDate(value, locale)}
        </span>
      ),
    },
    {
      title: text("最后使用", "Last used"),
      dataIndex: "lastUsedAt",
      key: "lastUsedAt",
      width: 170,
      render: (value: string | undefined) => (
        <span className="whitespace-nowrap text-xs text-zinc-500">
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
          className="text-xs text-zinc-500"
        />
      ),
    },
    {
      title: text("操作", "Actions"),
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
            aria-label={text(`吊销密钥 ${key.name}`, `Revoke key ${key.name}`)}
            onClick={() => setToRevoke(key)}
          />
        ) : null,
    },
  ];

  return (
    <div>
      <PageHeader
        title={text("API 密钥", "API keys")}
        description={text(
          "管理可调用管理 API 的访问密钥（reader / writer / admin）",
          "Manage reader, writer, and admin credentials for management APIs",
        )}
        actions={<CreateKeyDialog onCreated={setReveal} />}
      />
      {error !== null ? (
        <ErrorBanner error={error} onRetry={load} />
      ) : !keys ? (
        <Loading />
      ) : keys.length === 0 ? (
        <Card>
          <EmptyState
            title={text("暂无 API 密钥", "No API keys")}
            hint={text(
              "创建密钥以通过脚本/CI 调用管理 API",
              "Create a key for scripts and CI to call management APIs",
            )}
          />
        </Card>
      ) : (
        <>
          <MetricStrip
            items={[
              {
                label: text("有效密钥", "Active keys"),
                value: activeKeys.length,
                hint: text("可调用管理 API", "Can call management APIs"),
                tone: "success",
              },
              {
                label: text("管理员密钥", "Admin keys"),
                value: adminKeys.length,
                hint: adminKeys.length
                  ? text("建议定期轮换与审阅", "Rotate and review regularly")
                  : text("暂无高权限密钥", "No elevated keys"),
                tone: adminKeys.length ? "danger" : "default",
              },
              {
                label: text("已吊销 / 过期", "Revoked / expired"),
                value: keys.length - activeKeys.length,
                hint: text("保留历史审计记录", "Retained for audit history"),
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
                    {text("清除筛选", "Clear filters")}
                  </Button>
                ) : undefined
              }
            >
              <FilterField
                label={text("搜索", "Search")}
                className="min-w-[280px]"
              >
                <Input
                  allowClear
                  prefix={<SearchOutlined />}
                  placeholder={text(
                    "搜索名称或角色…",
                    "Search names or roles…",
                  )}
                  value={filter}
                  onChange={(e) => setFilter(e.target.value)}
                />
              </FilterField>
              <FilterField
                label={text("状态", "Status")}
                className="min-w-[160px]"
              >
                <Select<typeof stateFilter>
                  className="w-full"
                  value={stateFilter}
                  onChange={setStateFilter}
                  options={[
                    { value: "all", label: text("全部状态", "All statuses") },
                    { value: "active", label: text("有效", "Active") },
                    { value: "expired", label: text("已过期", "Expired") },
                    { value: "revoked", label: text("已吊销", "Revoked") },
                  ]}
                />
              </FilterField>
            </FilterBar>
            {visibleKeys.length === 0 ? (
              <EmptyState
                title={text("没有匹配的密钥", "No matching keys")}
                hint={text(
                  "调整筛选条件后重试",
                  "Adjust the filters and try again",
                )}
              />
            ) : (
              <Table<ApiKey>
                className="ag-console-table"
                rowKey="id"
                size="middle"
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
        <TokenReveal
          tokenKey={reveal}
          onDone={() => {
            setReveal(null);
            void load();
          }}
        />
      )}
      <ConfirmDialog
        open={!!toRevoke}
        title={text("吊销 API 密钥", "Revoke API key")}
        message={
          <>
            {text("确定吊销密钥", "Revoke key")}{" "}
            <span className="font-mono text-zinc-100">{toRevoke?.name}</span>{" "}
            {text(
              "吗？ 吊销后该 Token 立即失效。",
              "? Its token becomes invalid immediately.",
            )}
          </>
        }
        confirmLabel={text("吊销", "Revoke")}
        danger
        busy={revoking}
        onConfirm={confirmRevoke}
        onClose={() => setToRevoke(null)}
      />
    </div>
  );
}
