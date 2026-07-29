import { useCallback, useEffect, useState } from 'react';
import {
  getAuditRetentionPolicy,
  replaceAuditRetentionPolicy,
  executeAuditRetention,
  listAuditRetentionJobs,
} from '../client';
import type { AuditRetentionPolicy, AuditCleanupJob } from '../client';
import { PageHeader, Card, CardHeader, DataTable, Field, inputClass, btnPrimary, btnSecondary, btnDanger } from '../components/Layout';
import { Loading, ErrorBanner } from '../components/Feedback';
import { StateBadge } from '../components/Badge';
import { formatDate, formatNumber } from '../lib/format';

export function AuditRetentionPage() {
  const [policy, setPolicy] = useState<AuditRetentionPolicy | null>(null);
  const [jobs, setJobs] = useState<AuditCleanupJob[]>([]);
  const [error, setError] = useState<unknown>(null);
  const [enabled, setEnabled] = useState(false);
  const [keepDays, setKeepDays] = useState(90);
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState<unknown>(null);
  const [notice, setNotice] = useState('');
  const [executing, setExecuting] = useState(false);

  const load = useCallback(async () => {
    setError(null);
    const [p, j] = await Promise.all([getAuditRetentionPolicy(), listAuditRetentionJobs()]);
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
    setNotice('');
    const { error: err } = await replaceAuditRetentionPolicy({
      body: { ...policy, enabled, keepDays },
      headers: { 'If-Match': policy.version },
    });
    setSaving(false);
    if (err) {
      setSaveError(err);
      return;
    }
    setNotice('策略已保存');
    void load();
  };

  const execute = async () => {
    setExecuting(true);
    setSaveError(null);
    setNotice('');
    const { error: err } = await executeAuditRetention({
      headers: { 'Idempotency-Key': crypto.randomUUID() },
    });
    setExecuting(false);
    if (err) {
      setSaveError(err);
      return;
    }
    setNotice('清理任务已提交');
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

  return (
    <div>
      <PageHeader title="审计保留策略" description="全局审计日志的自动清理规则" />
      {saveError !== null && <div className="mb-4"><ErrorBanner error={saveError} /></div>}
      {notice && (
        <div className="mb-4 rounded-lg border border-emerald-500/30 bg-emerald-500/10 px-4 py-2.5 text-sm text-emerald-300">
          {notice}
        </div>
      )}
      <Card className="mb-6 p-5">
        <div className="flex max-w-lg flex-col gap-4">
          <label className="flex cursor-pointer items-center justify-between">
            <span className="text-sm text-zinc-300">启用自动清理</span>
            <button
              type="button"
              onClick={() => setEnabled(!enabled)}
              className={`relative h-6 w-11 rounded-full transition-colors ${enabled ? 'bg-cyan-600' : 'bg-zinc-700'}`}
            >
              <span
                className={`absolute top-0.5 h-5 w-5 rounded-full bg-white transition-transform ${enabled ? 'translate-x-5' : 'translate-x-0.5'}`}
              />
            </button>
          </label>
          <Field label="保留天数 (keepDays)" hint="超过该天数的审计记录将被清理">
            <input
              type="number"
              min={1}
              className={inputClass}
              value={keepDays}
              onChange={(e) => setKeepDays(Number(e.target.value))}
            />
          </Field>
          <div className="flex gap-2">
            <button onClick={save} disabled={saving} className={btnPrimary}>
              {saving ? '保存中…' : '保存策略'}
            </button>
            <button onClick={execute} disabled={executing} className={btnDanger}>
              {executing ? '提交中…' : '立即执行清理'}
            </button>
          </div>
        </div>
      </Card>

      <Card>
        <CardHeader title={`清理任务（${jobs.length}）`} extra={<button onClick={() => void load()} className={btnSecondary}>刷新</button>} />
        {jobs.length === 0 ? (
          <p className="px-4 py-8 text-center text-sm text-zinc-500">暂无清理任务</p>
        ) : (
          <DataTable columns={['ID', '状态', '截止时间', '已删除', '批次大小', '创建时间', '错误']}>
            {jobs.map((j) => (
              <tr key={j.id} className="hover:bg-zinc-800/30">
                <td className="px-4 py-2.5 font-mono text-xs text-zinc-500" title={j.id}>
                  {j.id.slice(0, 8)}…
                </td>
                <td className="px-4 py-2.5">
                  <StateBadge state={j.state} />
                </td>
                <td className="whitespace-nowrap px-4 py-2.5 text-xs text-zinc-400">{formatDate(j.cutoffAt)}</td>
                <td className="px-4 py-2.5 text-xs text-zinc-300">{formatNumber(j.deleted)}</td>
                <td className="px-4 py-2.5 text-xs text-zinc-400">{j.batchSize}</td>
                <td className="whitespace-nowrap px-4 py-2.5 text-xs text-zinc-500">{formatDate(j.createdAt)}</td>
                <td className="max-w-48 truncate px-4 py-2.5 text-xs text-rose-400" title={j.lastError}>
                  {j.lastError ?? '—'}
                </td>
              </tr>
            ))}
          </DataTable>
        )}
      </Card>
    </div>
  );
}
