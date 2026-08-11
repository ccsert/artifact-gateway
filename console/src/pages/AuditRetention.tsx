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
import { usePreferences } from "../lib/preferences";

export function AuditRetentionPage() {
  const { locale, text } = usePreferences();
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
    setNotice(text("策略已保存", "Policy saved"));
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
    setNotice(text("清理任务已提交", "Cleanup job submitted"));
    setTimeout(() => void load(), 1000);
  };

  if (error !== null) {
    return (
      <div>
        <PageHeader title={text("审计保留策略", "Audit retention policy")} />
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
      title: text("状态", "Status"),
      dataIndex: "state",
      key: "state",
      width: 130,
      render: (value: string) => <StateBadge state={value} />,
    },
    {
      title: text("截止时间", "Cutoff"),
      dataIndex: "cutoffAt",
      key: "cutoffAt",
      width: 180,
      render: (value: string) => (
        <span className="whitespace-nowrap text-xs text-zinc-400">
          {formatDate(value, locale)}
        </span>
      ),
    },
    {
      title: text("已删除", "Deleted"),
      dataIndex: "deleted",
      key: "deleted",
      width: 110,
      render: (value: number) => (
        <span className="text-xs text-zinc-300">
          {formatNumber(value, locale)}
        </span>
      ),
    },
    {
      title: text("批次大小", "Batch size"),
      dataIndex: "batchSize",
      key: "batchSize",
      width: 110,
      render: (value: number) => (
        <span className="text-xs text-zinc-400">{value}</span>
      ),
    },
    {
      title: text("创建时间", "Created"),
      dataIndex: "createdAt",
      key: "createdAt",
      width: 180,
      render: (value: string) => (
        <span className="whitespace-nowrap text-xs text-zinc-500">
          {formatDate(value, locale)}
        </span>
      ),
    },
    {
      title: text("错误", "Error"),
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
    <div className="ag-page-stack">
      <PageHeader
        title={text("审计保留策略", "Audit retention policy")}
        description={text(
          "全局审计日志的自动清理规则",
          "Automatic cleanup rules for global audit records",
        )}
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
            label: text("自动清理", "Automatic cleanup"),
            value: enabled
              ? text("已启用", "Enabled")
              : text("未启用", "Disabled"),
            hint: enabled
              ? text(`保留 ${keepDays} 天`, `Keep for ${keepDays} days`)
              : text(
                  "仅保留策略，不会自动删除",
                  "Policy is retained; records are not deleted automatically",
                ),
            tone: enabled ? "success" : "default",
          },
          {
            label: text("保留周期", "Retention period"),
            value: text(`${keepDays} 天`, `${keepDays} days`),
            hint: text(
              "超过截止时间的审计记录可被清理",
              "Records older than the cutoff can be removed",
            ),
          },
          {
            label: text("最近任务", "Recent jobs"),
            value: jobs.length,
            hint: text(
              "含已完成与失败的历史任务",
              "Includes completed and failed history",
            ),
          },
        ]}
      />
      <Card>
        <div className="grid max-w-5xl grid-cols-[minmax(0,1fr)_300px] gap-6 p-5">
          <div className="min-w-0">
            <div className="mb-4">
              <h2 className="text-sm font-semibold text-zinc-200">
                {text("策略设置", "Policy settings")}
              </h2>
              <p className="mt-1 text-xs text-zinc-500">
                {text(
                  "控制审计日志的自动保留周期，保存后由后台任务异步处理。",
                  "Control the automatic audit retention window. Changes are processed asynchronously.",
                )}
              </p>
            </div>
            <Form layout="vertical">
              <Form.Item
                label={text("启用自动清理", "Enable automatic cleanup")}
                extra={text(
                  "关闭后不会自动删除记录，但已保存的保留周期仍会保留。",
                  "Disabling stops automatic deletion while retaining the saved period.",
                )}
              >
                <Switch
                  checked={enabled}
                  onChange={(checked) => {
                    setEnabled(checked);
                    if (checked && keepDays < 1) setKeepDays(90);
                  }}
                  aria-label={text("切换自动清理", "Toggle automatic cleanup")}
                />
              </Form.Item>
              <Form.Item
                label={text("保留天数", "Retention days")}
                extra={text(
                  "超过该天数的审计记录将被清理。",
                  "Audit records older than this are eligible for cleanup.",
                )}
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
                  {text("保存策略", "Save policy")}
                </Button>
                <Popconfirm
                  disabled={!canExecute}
                  title={text(
                    "确认立即执行审计清理？",
                    "Run audit cleanup now?",
                  )}
                  description={text(
                    "将提交异步删除任务，并按当前保留天数处理符合条件的记录。",
                    "Submit an asynchronous deletion job for records outside the saved retention window.",
                  )}
                  okText={text("执行清理", "Run cleanup")}
                  cancelText={text("取消", "Cancel")}
                  okButtonProps={{ danger: true, loading: executing }}
                  onConfirm={execute}
                >
                  <Button danger loading={executing} disabled={!canExecute}>
                    {text("立即执行清理", "Run cleanup now")}
                  </Button>
                </Popconfirm>
              </Space>
            </Form>
          </div>
          <Alert
            className="h-fit"
            type="warning"
            showIcon
            title={text("清理说明", "Cleanup notes")}
            description={
              policyDirty
                ? text(
                    "请先保存当前策略；立即执行只会使用已经保存并启用的策略。",
                    "Save the current policy first; manual cleanup uses only the saved enabled policy.",
                  )
                : policy.enabled
                  ? text(
                      "立即执行会提交异步删除任务；保存策略不会立即删除记录。",
                      "Run cleanup submits an asynchronous deletion job; saving does not delete records immediately.",
                    )
                  : text(
                      "启用自动清理并保存策略后，才能提交清理任务。",
                      "Enable and save automatic cleanup before submitting a cleanup job.",
                    )
            }
          />
        </div>
      </Card>

      <Card>
        <CardHeader
          title={text(
            `清理任务（${jobs.length}）`,
            `Cleanup jobs (${jobs.length})`,
          )}
          extra={
            <Button
              size="small"
              icon={<ReloadOutlined />}
              onClick={() => void load()}
            >
              {text("刷新", "Refresh")}
            </Button>
          }
        />
        {jobs.length === 0 ? (
          <p className="px-4 py-8 text-center text-sm text-zinc-500">
            {text("暂无清理任务", "No cleanup jobs")}
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
