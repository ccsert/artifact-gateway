import { useCallback, useEffect, useState } from "react";
import { Alert, Button, InputNumber, Popconfirm, Space, Switch } from "antd";
import { ReloadOutlined } from "@ant-design/icons";
import {
  getAuditRetentionPolicy,
  replaceAuditRetentionPolicy,
  executeAuditRetention,
  listAuditRetentionJobs,
} from "../client";
import type { AuditRetentionPolicy, AuditCleanupJob } from "../client";
import {
  PageHeader,
  Card,
  CardHeader,
  DataTable,
  Field,
  StatCard,
} from "../components/Layout";
import { Loading, ErrorBanner } from "../components/Feedback";
import { StateBadge } from "../components/Badge";
import { formatDate, formatNumber } from "../lib/format";

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
  const policyDirty = enabled !== policy.enabled || keepDays !== policy.keepDays;
  const canExecute = policy.enabled && !policyDirty;

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
      {notice && <Alert className="mb-4" type="success" showIcon title={notice} />}
      <div className="mb-6 grid grid-cols-1 gap-3 sm:grid-cols-3">
        <StatCard
          label="自动清理"
          value={enabled ? "已启用" : "未启用"}
          sub={enabled ? `保留 ${keepDays} 天` : "仅保留策略，不会自动删除"}
        />
        <StatCard
          label="保留周期"
          value={`${keepDays} 天`}
          sub="超过截止时间的审计记录可被清理"
        />
        <StatCard
          label="最近任务"
          value={jobs.length}
          sub="含已完成与失败的历史任务"
        />
      </div>
      <Card className="mb-6 p-5">
        <div className="flex max-w-lg flex-col gap-4">
          <label className="flex items-center justify-between">
            <span className="text-sm text-zinc-300">启用自动清理</span>
            <Switch
              checked={enabled}
              onChange={(checked) => {
                setEnabled(checked);
                if (checked && keepDays < 1) setKeepDays(90);
              }}
              aria-label="切换自动清理"
            />
          </label>
          <Field
            label="保留天数"
            hint="超过该天数的审计记录将被清理"
          >
            <InputNumber
              min={enabled ? 1 : 0}
              precision={0}
              className="w-full"
              value={keepDays}
              onChange={(value) => setKeepDays(value ?? (enabled ? 1 : 0))}
            />
          </Field>
          <Space>
            <Button type="primary" onClick={save} loading={saving} disabled={!policyDirty}>保存策略</Button>
            <Popconfirm
              disabled={!canExecute}
              title="确认立即执行审计清理？"
              description="将提交异步删除任务，并按当前保留天数处理符合条件的记录。"
              okText="执行清理"
              cancelText="取消"
              okButtonProps={{ danger: true, loading: executing }}
              onConfirm={execute}
            >
              <Button danger loading={executing} disabled={!canExecute}>立即执行清理</Button>
            </Popconfirm>
          </Space>
          <Alert
            type="warning"
            showIcon
            title="清理说明"
            description={policyDirty
              ? "请先保存当前策略；立即执行只会使用已经保存并启用的策略。"
              : policy.enabled
                ? "立即执行会提交异步删除任务；保存策略不会立即删除记录。"
                : "启用自动清理并保存策略后，才能提交清理任务。"}
          />
        </div>
      </Card>

      <Card>
        <CardHeader
          title={`清理任务（${jobs.length}）`}
          extra={
            <Button size="small" icon={<ReloadOutlined />} onClick={() => void load()}>刷新</Button>
          }
        />
        {jobs.length === 0 ? (
          <p className="px-4 py-8 text-center text-sm text-zinc-500">
            暂无清理任务
          </p>
        ) : (
          <DataTable
            columns={[
              "ID",
              "状态",
              "截止时间",
              "已删除",
              "批次大小",
              "创建时间",
              "错误",
            ]}
          >
            {jobs.map((j) => (
              <tr key={j.id} className="hover:bg-zinc-800/30">
                <td
                  className="px-4 py-2.5 font-mono text-xs text-zinc-500"
                  title={j.id}
                >
                  {j.id.slice(0, 8)}…
                </td>
                <td className="px-4 py-2.5">
                  <StateBadge state={j.state} />
                </td>
                <td className="whitespace-nowrap px-4 py-2.5 text-xs text-zinc-400">
                  {formatDate(j.cutoffAt)}
                </td>
                <td className="px-4 py-2.5 text-xs text-zinc-300">
                  {formatNumber(j.deleted)}
                </td>
                <td className="px-4 py-2.5 text-xs text-zinc-400">
                  {j.batchSize}
                </td>
                <td className="whitespace-nowrap px-4 py-2.5 text-xs text-zinc-500">
                  {formatDate(j.createdAt)}
                </td>
                <td
                  className="max-w-48 truncate px-4 py-2.5 text-xs text-rose-400"
                  title={j.lastError}
                >
                  {j.lastError ?? "—"}
                </td>
              </tr>
            ))}
          </DataTable>
        )}
      </Card>
    </div>
  );
}
