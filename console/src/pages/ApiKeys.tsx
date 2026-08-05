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

function CreateKeyDialog({
  onCreated,
}: {
  onCreated: (key: CreatedApiKey) => void;
}) {
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
        新建密钥
      </Button>
      <Modal
        open={dialog.open}
        title="新建 API 密钥"
        onClose={dialog.hide}
        footer={
          <Space>
            <Button onClick={dialog.hide} disabled={busy}>
              取消
            </Button>
            <Button
              type="primary"
              onClick={submit}
              loading={busy}
              disabled={!name.trim()}
            >
              创建
            </Button>
          </Space>
        }
      >
        <div className="space-y-4">
          {error !== null && <ErrorBanner error={error} />}
          <Field label="密钥名称" hint="用于标识用途，例如 ci-deploy">
            <Input
              className="font-mono"
              placeholder="my-key"
              value={name}
              onChange={(e) => setName(e.target.value)}
            />
          </Field>
          <Field
            label="角色"
            hint="按最小权限选择。角色在全仓库范围内生效，优先于逐仓库授权。"
          >
            <Select<typeof role>
              className="w-full"
              value={role}
              onChange={setRole}
              options={[
                {
                  value: "reader",
                  label: "reader · 只读（浏览 / 搜索 / 拉取）",
                },
                {
                  value: "writer",
                  label: "writer · 读写（可发布 / 编辑，不可管理用户与密钥）",
                },
                { value: "admin", label: "admin · 管理员（全部权限）" },
              ]}
            />
          </Field>
          <Field label="有效期" hint="到期后密钥会自动拒绝认证，无需手动吊销。">
            <Select<typeof validDays>
              className="w-full"
              value={validDays}
              onChange={setValidDays}
              options={[
                { value: 30, label: "30 天" },
                { value: 90, label: "90 天（推荐）" },
                { value: 180, label: "180 天" },
                { value: 365, label: "365 天" },
              ]}
            />
          </Field>
          <p className="text-xs text-zinc-500">
            创建后只会显示一次明文 Token，请立即保存。
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
  return (
    <Modal open onClose={onDone} title="密钥已创建：请立即保存 Token">
      <div className="space-y-3">
        <Alert
          type="warning"
          showIcon
          title="这是唯一一次显示明文 Token"
          description="关闭弹窗后将无法再次查看，请立即复制并保存到安全位置。"
        />
        <div className="rounded-lg border border-zinc-700 bg-zinc-950 p-3">
          <Typography.Text
            className="block break-all font-mono text-xs"
            copyable={{
              text: tokenKey.token,
              tooltips: ["复制 Token", "已复制"],
            }}
          >
            {tokenKey.token}
          </Typography.Text>
        </div>
        <div className="flex justify-end">
          <Button type="primary" onClick={onDone}>
            我已保存
          </Button>
        </div>
      </div>
    </Modal>
  );
}

export function ApiKeysPage() {
  const [keys, setKeys] = useState<ApiKey[] | null>(null);
  const [error, setError] = useState<unknown>(null);
  const [reveal, setReveal] = useState<CreatedApiKey | null>(null);
  const [toRevoke, setToRevoke] = useState<ApiKey | null>(null);
  const [revoking, setRevoking] = useState(false);
  const [filter, setFilter] = useState("");
  const [stateFilter, setStateFilter] = useState<
    "all" | "active" | "expired" | "revoked"
  >("all");

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
      title: "名称",
      dataIndex: "name",
      key: "name",
      width: 210,
      render: (name: string) => (
        <span className="font-medium text-zinc-100">{name}</span>
      ),
    },
    {
      title: "角色",
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
      title: "状态",
      key: "state",
      width: 130,
      render: (_value, key) => <StateBadge state={keyState(key)} />,
    },
    {
      title: "创建时间",
      dataIndex: "createdAt",
      key: "createdAt",
      width: 170,
      render: (value: string) => (
        <span className="whitespace-nowrap text-xs text-zinc-500">
          {formatDate(value)}
        </span>
      ),
    },
    {
      title: "到期时间",
      dataIndex: "expiresAt",
      key: "expiresAt",
      width: 170,
      render: (value: string | undefined) => (
        <span className="whitespace-nowrap text-xs text-zinc-500">
          {formatDate(value)}
        </span>
      ),
    },
    {
      title: "最后使用",
      dataIndex: "lastUsedAt",
      key: "lastUsedAt",
      width: 170,
      render: (value: string | undefined) => (
        <span className="whitespace-nowrap text-xs text-zinc-500">
          {formatDate(value)}
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
      title: "",
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
            aria-label={`吊销密钥 ${key.name}`}
            onClick={() => setToRevoke(key)}
          />
        ) : null,
    },
  ];

  return (
    <div>
      <PageHeader
        title="API 密钥"
        description="管理可调用管理 API 的访问密钥（reader / writer / admin）"
        actions={<CreateKeyDialog onCreated={setReveal} />}
      />
      {error !== null ? (
        <ErrorBanner error={error} onRetry={load} />
      ) : !keys ? (
        <Loading />
      ) : keys.length === 0 ? (
        <Card>
          <EmptyState
            title="暂无 API 密钥"
            hint="创建密钥以通过脚本/CI 调用管理 API"
          />
        </Card>
      ) : (
        <>
          <MetricStrip
            items={[
              {
                label: "有效密钥",
                value: activeKeys.length,
                hint: "可调用管理 API",
                tone: "success",
              },
              {
                label: "管理员密钥",
                value: adminKeys.length,
                hint: adminKeys.length
                  ? "建议定期轮换与审阅"
                  : "暂无高权限密钥",
                tone: adminKeys.length ? "danger" : "default",
              },
              {
                label: "已吊销 / 过期",
                value: keys.length - activeKeys.length,
                hint: "保留历史审计记录",
              },
            ]}
          />
          <Card>
            <FilterBar
              className="border-x-0 border-t-0 rounded-none"
              actions={
                filter || stateFilter !== "all" ? (
                  <Button
                    type="text"
                    onClick={() => {
                      setFilter("");
                      setStateFilter("all");
                    }}
                  >
                    清除筛选
                  </Button>
                ) : undefined
              }
            >
              <FilterField label="搜索" className="min-w-[280px]">
                <Input
                  allowClear
                  prefix={<SearchOutlined />}
                  placeholder="搜索名称或角色…"
                  value={filter}
                  onChange={(e) => setFilter(e.target.value)}
                />
              </FilterField>
              <FilterField label="状态" className="min-w-[160px]">
                <Select<typeof stateFilter>
                  className="w-full"
                  value={stateFilter}
                  onChange={setStateFilter}
                  options={[
                    { value: "all", label: "全部状态" },
                    { value: "active", label: "有效" },
                    { value: "expired", label: "已过期" },
                    { value: "revoked", label: "已吊销" },
                  ]}
                />
              </FilterField>
            </FilterBar>
            {visibleKeys.length === 0 ? (
              <EmptyState title="没有匹配的密钥" hint="调整筛选条件后重试" />
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
        title="吊销 API 密钥"
        message={
          <>
            确定吊销密钥{" "}
            <span className="font-mono text-zinc-100">{toRevoke?.name}</span>{" "}
            吗？ 吊销后该 Token 立即失效。
          </>
        }
        confirmLabel="吊销"
        danger
        busy={revoking}
        onConfirm={confirmRevoke}
        onClose={() => setToRevoke(null)}
      />
    </div>
  );
}
