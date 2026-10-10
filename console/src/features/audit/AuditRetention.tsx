import { useCallback, useEffect, useState } from "react";
import { Button, Form, InputNumber, Popconfirm, Space, Switch } from "antd";
import type { ColumnsType } from "antd/es/table";
import { ReloadOutlined } from "@ant-design/icons";
import {
  getAuditRetentionPolicy,
  replaceAuditRetentionPolicy,
  executeAuditRetention,
  listAuditRetentionJobs,
} from "../../client";
import type { AuditRetentionPolicy, AuditCleanupJob } from "../../client";
import { PageHeader, Card, CardHeader } from "../../components/ui/Layout";
import { ErrorBanner, Loading, Notice } from "../../components/ui/Feedback";
import { StateBadge } from "../../components/ui/Badge";
import { formatDate, formatNumber } from "../../lib/format";
import { ConsoleTable } from "../../components/ui/ConsolePrimitives";
import { usePreferences } from "../../lib/preferences";

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
      setEnabled(p.data.enabled ?? false);
      setKeepDays(p.data.keepDays ?? 90);
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
    try {
      const { error: err } = await replaceAuditRetentionPolicy({
        body: { ...policy, enabled, keepDays },
        headers: { "If-Match": policy.version },
      });
      if (err) {
        setSaveError(err);
        return;
      }
      setNotice(text("策略已保存", "Policy saved"));
      void load();
    } catch (nextError) {
      setSaveError(nextError);
    } finally {
      setSaving(false);
    }
  };

  const execute = async () => {
    setExecuting(true);
    setSaveError(null);
    setNotice("");
    try {
      const { error: err } = await executeAuditRetention({
        headers: { "Idempotency-Key": crypto.randomUUID() },
      });
      if (err) {
        setSaveError(err);
        return;
      }
      setNotice(text("清理任务已提交", "Cleanup job submitted"));
      setTimeout(() => void load(), 1000);
    } catch (nextError) {
      setSaveError(nextError);
    } finally {
      setExecuting(false);
    }
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
        <span className="font-mono text-xs text-fg-tertiary" title={value}>
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
        <span className="whitespace-nowrap text-xs text-fg-secondary">
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
        <span className="text-xs text-fg-secondary">
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
        <span className="text-xs text-fg-secondary">{value}</span>
      ),
    },
    {
      title: text("创建时间", "Created"),
      dataIndex: "createdAt",
      key: "createdAt",
      width: 180,
      render: (value: string) => (
        <span className="whitespace-nowrap text-xs text-fg-tertiary">
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
          className="block max-w-80 truncate text-xs text-[var(--ag-status-danger)]"
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
      {saveError !== null && <ErrorBanner error={saveError} />}
      {notice && (
        <Notice tone="success" title={notice} onClose={() => setNotice("")} />
      )}
      <Card>
        <div className="max-w-3xl p-5">
          <div className="mb-4">
            <h2 className="text-sm font-semibold text-fg">
              {text("策略设置", "Policy settings")}
            </h2>
            <p className="mt-1 text-xs text-fg-tertiary">
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
                title={text("确认立即执行审计清理？", "Run audit cleanup now?")}
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
          <p className="px-4 py-8 text-center text-sm text-fg-tertiary">
            {text("暂无清理任务", "No cleanup jobs")}
          </p>
        ) : (
          <ConsoleTable<AuditCleanupJob>
            rowKey="id"
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
