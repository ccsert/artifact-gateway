import { useCallback, useEffect, useState } from "react";
import {
  Alert,
  Button,
  Form,
  InputNumber,
  Popconfirm,
  Space,
  Switch,
  Table,
} from "antd";
import type { ColumnsType } from "antd/es/table";
import { ReloadOutlined } from "@ant-design/icons";
import {
  getAuditRetentionPolicy,
  replaceAuditRetentionPolicy,
  executeAuditRetention,
  listAuditRetentionJobs,
} from "../client";
import type { AuditRetentionPolicy, AuditCleanupJob } from "../client";
import { PageHeader, Card, CardHeader } from "../components/Layout";
import { Loading, ErrorBanner } from "../components/Feedback";
import { StateBadge } from "../components/Badge";
import { formatDate, formatNumber } from "../lib/format";
import { MetricStrip } from "../components/ConsolePrimitives";

export function AuditRetentionPage() {
  const [policy, setPolicy] = useState<AuditRetentionPolicy | null>(null);
  const [jobs, setJobs] = useState<AuditCleanupJob[]>([]);
  const [error, setError] = useState<unknown>(null);
  const [enabled, setEnabled] = useState(false);
  const [keepDays, setKeepDays] = useState(90);
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState<unknown>(null);
  const [notice, setNotice] = useState("");
  const [executing, setExecuting] = useState(false);

  const load = useCallback(async () => {
    setError(null);
    const [p, j] = await Promise.all([
      getAuditRetentionPolicy(),
      listAuditRetentionJobs(),
    ]);
    if (p.error) {
      setError(p.error);
      return;
    }
    if (p.data) {
      setPolicy(p.data);
      setEnabled(p.data.enabled);
      setKeepDays(p.data.keepDays);
    }
    setJobs(j.data ?? []);
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  const save = async () => {
    if (!policy) return;
    setSaving(true);
    setSaveError(null);
    setNotice("");
    const { error: err } = await replaceAuditRetentionPolicy({
      body: { ...policy, enabled, keepDays },
      headers: { "If-Match": policy.version },
    });
    setSaving(false);
    if (err) {
      setSaveError(err);
      return;
    }
    setNotice("策略已保存");
    void load();
  };

  const execute = async () => {
    setExecuting(true);
    setSaveError(null);
    setNotice("");
    const { error: err } = await executeAuditRetention({
      headers: { "Idempotency-Key": crypto.randomUUID() },
    });
    setExecuting(false);
    if (err) {
      setSaveError(err);
      return;
    }
    setNotice("清理任务已提交");
    setTimeout(() => void load(), 1000);
  };

  if (error !== null) {
    return (
      <div>
        <PageHeader title="审计保留策略" />
        <ErrorBanner error={error} onRetry={load} />
      </div>
    );
  }
  if (!policy) return <Loading />;
  const policyDirty =
    enabled !== policy.enabled || keepDays !== policy.keepDays;
  const canExecute = policy.enabled && !policyDirty;
  const jobColumns: ColumnsType<AuditCleanupJob> = [
    {
      title: "ID",
      dataIndex: "id",
      key: "id",
      width: 150,
      render: (value: string) => (
        <span className="font-mono text-xs text-zinc-500" title={value}>
          {value.slice(0, 8)}…
        </span>
      ),
    },
    {
      title: "状态",
      dataIndex: "state",
      key: "state",
      width: 130,
      render: (value: string) => <StateBadge state={value} />,
    },
    {
      title: "截止时间",
      dataIndex: "cutoffAt",
      key: "cutoffAt",
      width: 180,
      render: (value: string) => (
        <span className="whitespace-nowrap text-xs text-zinc-400">
          {formatDate(value)}
        </span>
      ),
    },
    {
      title: "已删除",
      dataIndex: "deleted",
      key: "deleted",
      width: 110,
      render: (value: number) => (
        <span className="text-xs text-zinc-300">{formatNumber(value)}</span>
      ),
    },
    {
      title: "批次大小",
      dataIndex: "batchSize",
      key: "batchSize",
      width: 110,
      render: (value: number) => (
        <span className="text-xs text-zinc-400">{value}</span>
      ),
    },
    {
      title: "创建时间",
      dataIndex: "createdAt",
      key: "createdAt",
      width: 180,
      render: (value: string) => (
        <span className="whitespace-nowrap text-xs text-zinc-500">
          {formatDate(value)}
        </span>
      ),
    },
    {
      title: "错误",
      dataIndex: "lastError",
      key: "lastError",
      width: 320,
      render: (value: string | undefined) => (
        <span
          className="block max-w-80 truncate text-xs text-rose-400"
          title={value}
        >
          {value ?? "—"}
        </span>
      ),
    },
  ];

  return (
    <div>
      <PageHeader
        title="审计保留策略"
        description="全局审计日志的自动清理规则"
      />
      {saveError !== null && (
        <div className="mb-4">
          <ErrorBanner error={saveError} />
        </div>
      )}
      {notice && (
        <Alert className="mb-4" type="success" showIcon title={notice} />
      )}
      <MetricStrip
        items={[
          {
            label: "自动清理",
            value: enabled ? "已启用" : "未启用",
            hint: enabled ? `保留 ${keepDays} 天` : "仅保留策略，不会自动删除",
            tone: enabled ? "success" : "default",
          },
          {
            label: "保留周期",
            value: `${keepDays} 天`,
            hint: "超过截止时间的审计记录可被清理",
          },
          {
            label: "最近任务",
            value: jobs.length,
            hint: "含已完成与失败的历史任务",
          },
        ]}
      />
      <Card className="mt-4 mb-6">
        <div className="grid max-w-5xl gap-8 p-5 xl:grid-cols-[minmax(0,1fr)_320px]">
          <div className="min-w-0">
            <div className="mb-4">
              <h2 className="text-sm font-semibold text-zinc-200">策略设置</h2>
              <p className="mt-1 text-xs text-zinc-500">
                控制审计日志的自动保留周期，保存后由后台任务异步处理。
              </p>
            </div>
            <Form layout="vertical">
              <Form.Item
                label="启用自动清理"
                extra="关闭后不会自动删除记录，但已保存的保留周期仍会保留。"
              >
                <Switch
                  checked={enabled}
                  onChange={(checked) => {
                    setEnabled(checked);
                    if (checked && keepDays < 1) setKeepDays(90);
                  }}
                  aria-label="切换自动清理"
                />
              </Form.Item>
              <Form.Item
                label="保留天数"
                extra="超过该天数的审计记录将被清理。"
              >
                <InputNumber
                  min={enabled ? 1 : 0}
                  precision={0}
                  className="w-full"
                  value={keepDays}
                  onChange={(value) => setKeepDays(value ?? (enabled ? 1 : 0))}
                />
              </Form.Item>
              <Space>
                <Button
                  type="primary"
                  onClick={save}
                  loading={saving}
                  disabled={!policyDirty}
                >
                  保存策略
                </Button>
                <Popconfirm
                  disabled={!canExecute}
                  title="确认立即执行审计清理？"
                  description="将提交异步删除任务，并按当前保留天数处理符合条件的记录。"
                  okText="执行清理"
                  cancelText="取消"
                  okButtonProps={{ danger: true, loading: executing }}
                  onConfirm={execute}
                >
                  <Button danger loading={executing} disabled={!canExecute}>
                    立即执行清理
                  </Button>
                </Popconfirm>
              </Space>
            </Form>
          </div>
          <Alert
            className="h-fit"
            type="warning"
            showIcon
            title="清理说明"
            description={
              policyDirty
                ? "请先保存当前策略；立即执行只会使用已经保存并启用的策略。"
                : policy.enabled
                  ? "立即执行会提交异步删除任务；保存策略不会立即删除记录。"
                  : "启用自动清理并保存策略后，才能提交清理任务。"
            }
          />
        </div>
      </Card>

      <Card>
        <CardHeader
          title={`清理任务（${jobs.length}）`}
          extra={
            <Button
              size="small"
              icon={<ReloadOutlined />}
              onClick={() => void load()}
            >
              刷新
            </Button>
          }
        />
        {jobs.length === 0 ? (
          <p className="px-4 py-8 text-center text-sm text-zinc-500">
            暂无清理任务
          </p>
        ) : (
          <Table<AuditCleanupJob>
            className="ag-console-table"
            rowKey="id"
            size="middle"
            dataSource={jobs}
            columns={jobColumns}
            pagination={false}
            scroll={{ x: 1160, y: "calc(100vh - 470px)" }}
          />
        )}
      </Card>
    </div>
  );
}
