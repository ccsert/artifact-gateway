import { useState } from "react";
import { Button, Form, InputNumber, Popconfirm, Space, Switch } from "antd";
import type { ColumnsType } from "antd/es/table";
import { ReloadOutlined } from "@ant-design/icons";
import {
  replaceAuditRetentionPolicy,
  executeAuditRetention,
} from "../../client";
import type { AuditCleanupJob } from "../../client";
import { PageHeader, Card, CardHeader } from "../../components/ui/Layout";
import { ErrorBanner, Loading, Notice } from "../../components/ui/Feedback";
import { StateBadge } from "../../components/ui/Badge";
import { formatDate, formatNumber } from "../../lib/format";
import { ConsoleTable } from "../../components/ui/ConsolePrimitives";
import { usePreferences } from "../../lib/preferences";
import { useQueryClient } from "@tanstack/react-query";
import { auditKeys } from "../../lib/query/keys";
import { useAuditRetentionJobs, useAuditRetentionPolicy } from "./auditQueries";

export function AuditRetentionPage() {
  const { locale, t } = usePreferences();
  const queryClient = useQueryClient();
  const policyQuery = useAuditRetentionPolicy();
  const jobsQuery = useAuditRetentionJobs();
  const policy = policyQuery.data ?? null;
  const jobs = jobsQuery.data ?? [];
  const error = policyQuery.error;
  // An edit belongs to the saved version it started from. A refreshed new
  // version resets the form instead of pairing old edits with a new If-Match.
  const [draft, setDraft] = useState<{
    version: string;
    enabled: boolean;
    keepDays: number;
  } | null>(null);
  const currentDraft = draft?.version === policy?.version ? draft : null;
  const enabled = currentDraft?.enabled ?? policy?.enabled ?? false;
  const keepDays = currentDraft?.keepDays ?? policy?.keepDays ?? 90;
  const editPolicy = (change: { enabled?: boolean; keepDays?: number }) => {
    if (!policy) return;
    setDraft({ version: policy.version, enabled, keepDays, ...change });
  };
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState<unknown>(null);
  const [notice, setNotice] = useState("");
  const [executing, setExecuting] = useState(false);
  const load = () =>
    Promise.allSettled([policyQuery.refetch(), jobsQuery.refetch()]);

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
      setNotice(t("audit.policySaved"));
      setDraft(null);
      void queryClient.invalidateQueries({
        queryKey: auditKeys.retentionPolicy(),
      });
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
      setNotice(t("audit.cleanupJobSubmitted"));
      void queryClient.invalidateQueries({
        queryKey: auditKeys.retentionJobs(),
      });
    } catch (nextError) {
      setSaveError(nextError);
    } finally {
      setExecuting(false);
    }
  };

  if (error !== null && !policy) {
    return (
      <div>
        <PageHeader title={t("audit.auditRetentionPolicy")} />
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
      title: t("audit.status"),
      dataIndex: "state",
      key: "state",
      width: 130,
      render: (value: string) => <StateBadge state={value} />,
    },
    {
      title: t("audit.cutoff"),
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
      title: t("audit.deleted"),
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
      title: t("audit.batchSize"),
      dataIndex: "batchSize",
      key: "batchSize",
      width: 110,
      render: (value: number) => (
        <span className="text-xs text-fg-secondary">{value}</span>
      ),
    },
    {
      title: t("audit.created"),
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
      title: t("audit.error"),
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
        title={t("audit.auditRetentionPolicy")}
        description={t("audit.automaticCleanupRulesForGlobalAuditRecords")}
      />
      {error !== null && <ErrorBanner error={error} onRetry={load} />}
      {saveError !== null && <ErrorBanner error={saveError} />}
      {notice && (
        <Notice tone="success" title={notice} onClose={() => setNotice("")} />
      )}
      <Card>
        <div className="max-w-3xl p-5">
          <div className="mb-4">
            <h2 className="text-sm font-semibold text-fg">
              {t("audit.policySettings")}
            </h2>
            <p className="mt-1 text-xs text-fg-tertiary">
              {t("audit.controlTheAutomaticAuditRetentionWindowChangesAre")}
            </p>
          </div>
          <Form layout="vertical">
            <Form.Item
              label={t("audit.enableAutomaticCleanup")}
              extra={t(
                "audit.disablingStopsAutomaticDeletionWhileRetainingTheSaved",
              )}
            >
              <Switch
                checked={enabled}
                onChange={(checked) => {
                  editPolicy({
                    enabled: checked,
                    keepDays: checked && keepDays < 1 ? 90 : keepDays,
                  });
                }}
                aria-label={t("audit.toggleAutomaticCleanup")}
              />
            </Form.Item>
            <Form.Item
              label={t("audit.retentionDays")}
              extra={t("audit.auditRecordsOlderThanThisAreEligibleFor")}
            >
              <InputNumber
                min={enabled ? 1 : 0}
                precision={0}
                className="w-full"
                value={keepDays}
                onChange={(value) =>
                  editPolicy({ keepDays: value ?? (enabled ? 1 : 0) })
                }
              />
            </Form.Item>
            <Space>
              <Button
                type="primary"
                onClick={save}
                loading={saving}
                disabled={!policyDirty}
              >
                {t("audit.savePolicy")}
              </Button>
              <Popconfirm
                disabled={!canExecute}
                title={t("audit.runAuditCleanupNow")}
                description={t(
                  "audit.submitAnAsynchronousDeletionJobForRecordsOutside",
                )}
                okText={t("audit.runCleanup")}
                cancelText={t("audit.cancel")}
                okButtonProps={{ danger: true, loading: executing }}
                onConfirm={execute}
              >
                <Button danger loading={executing} disabled={!canExecute}>
                  {t("audit.runCleanupNow")}
                </Button>
              </Popconfirm>
            </Space>
          </Form>
        </div>
      </Card>

      <Card>
        <CardHeader
          title={t("audit.cleanupJobs", { length: jobs.length })}
          extra={
            <Button
              size="small"
              icon={<ReloadOutlined />}
              onClick={() => void load()}
            >
              {t("audit.refresh")}
            </Button>
          }
        />
        {jobs.length === 0 ? (
          <p className="px-4 py-8 text-center text-sm text-fg-tertiary">
            {t("audit.noCleanupJobs")}
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
