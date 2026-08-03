import { useCallback, useEffect, useState } from 'react';
import { listApiKeys, createApiKey, revokeApiKey } from '../client';
import type { ApiKey, CreatedApiKey } from '../client';
import { PageHeader, Card, DataTable, Field, inputClass, btnPrimary, btnSecondary, StatCard } from '../components/Layout';
import { Loading, ErrorBanner, EmptyState } from '../components/Feedback';
import { StateBadge, Badge } from '../components/Badge';
import { Modal, ConfirmDialog, useDisclosure } from '../components/Modal';
import { formatDate } from '../lib/format';

function CreateKeyDialog({ onCreated }: { onCreated: (key: CreatedApiKey) => void }) {
  const dialog = useDisclosure();
  const [name, setName] = useState('');
  const [role, setRole] = useState<'reader' | 'writer' | 'admin'>('reader');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);

  const submit = async () => {
    setBusy(true);
    setError(null);
    const { data, error: err } = await createApiKey({
      body: { name: name.trim(), roles: [role] },
    });
    setBusy(false);
    if (err) {
      setError(err);
      return;
    }
    if (data) {
      dialog.hide();
      setName('');
      setRole('reader');
      onCreated(data);
    }
  };

  return (
    <>
      <button onClick={dialog.show} className={btnPrimary}>
        + 新建密钥
      </button>
      <Modal
        open={dialog.open}
        title="新建 API 密钥"
        onClose={dialog.hide}
        footer={
          <>
            <button onClick={dialog.hide} className={btnSecondary}>
              取消
            </button>
            <button onClick={submit} disabled={busy || !name.trim()} className={btnPrimary}>
              {busy ? '创建中…' : '创建'}
            </button>
          </>
        }
      >
        <div className="space-y-4">
          {error !== null && <ErrorBanner error={error} />}
          <Field label="密钥名称" hint="用于标识用途，例如 ci-deploy">
            <input
              className={`${inputClass} font-mono`}
              placeholder="my-key"
              value={name}
              onChange={(e) => setName(e.target.value)}
            />
          </Field>
          <Field label="角色" hint="按最小权限选择。角色在全仓库范围内生效，优先于逐仓库授权。">
            <select className={inputClass} value={role} onChange={(e) => setRole(e.target.value as 'reader' | 'writer' | 'admin')}>
              <option value="reader">reader · 只读（浏览 / 搜索 / 拉取）</option>
              <option value="writer">writer · 读写（可发布 / 编辑，不可管理密钥与仓库删除外的管理操作）</option>
              <option value="admin">admin · 管理员（全部权限）</option>
            </select>
          </Field>
          <p className="text-xs text-zinc-500">创建后只会显示一次明文 Token，请立即保存。</p>
        </div>
      </Modal>
    </>
  );
}

function TokenReveal({ tokenKey, onDone }: { tokenKey: CreatedApiKey; onDone: () => void }) {
  const [copied, setCopied] = useState(false);
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(tokenKey.token);
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    } catch {
      /* clipboard 不可用时忽略 */
    }
  };
  return (
    <Modal open onClose={onDone} title="密钥已创建 — 请立即保存 Token">
      <div className="space-y-3">
        <p className="text-sm text-amber-300">
          这是唯一一次显示明文 Token，关闭后将无法再次查看。
        </p>
        <div className="rounded-lg border border-zinc-700 bg-zinc-950 p-3">
          <code className="block break-all font-mono text-xs text-cyan-300">{tokenKey.token}</code>
        </div>
        <div className="flex justify-end gap-2">
          <button onClick={copy} className={btnSecondary}>
            {copied ? '已复制 ✓' : '复制 Token'}
          </button>
          <button onClick={onDone} className={btnPrimary}>
            我已保存
          </button>
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
  const [filter, setFilter] = useState('');
  const [stateFilter, setStateFilter] = useState<'all' | 'active' | 'revoked'>('all');

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
    const { error: err } = await revokeApiKey({ path: { apiKeyId: toRevoke.id } });
    setRevoking(false);
    if (!err) {
      setToRevoke(null);
      void load();
    }
  };

  const visibleKeys = (keys ?? []).filter((key) =>
    (!filter || key.name.toLowerCase().includes(filter.toLowerCase()) || key.roles.some((role) => role.includes(filter.toLowerCase()))) &&
    (stateFilter === 'all' || (stateFilter === 'active' ? !key.revokedAt : !!key.revokedAt)),
  );
  const activeKeys = (keys ?? []).filter((key) => !key.revokedAt);
  const adminKeys = activeKeys.filter((key) => key.roles.includes('admin'));

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
          <EmptyState title="暂无 API 密钥" hint="创建密钥以通过脚本/CI 调用管理 API" />
        </Card>
      ) : (
        <>
          <div className="mb-6 grid grid-cols-1 gap-3 sm:grid-cols-3">
            <StatCard label="有效密钥" value={activeKeys.length} sub="可调用管理 API" />
            <StatCard label="管理员密钥" value={adminKeys.length} sub={adminKeys.length ? '建议定期轮换与审阅' : '暂无高权限密钥'} />
            <StatCard label="已吊销" value={keys.length - activeKeys.length} sub="保留历史审计记录" />
          </div>
          <Card>
            <div className="flex flex-wrap items-center gap-2 border-b border-zinc-800/80 px-4 py-3">
              <input className={`${inputClass} w-56`} placeholder="搜索名称或角色…" value={filter} onChange={(e) => setFilter(e.target.value)} />
              <select className={`${inputClass} w-auto min-w-28`} value={stateFilter} onChange={(e) => setStateFilter(e.target.value as typeof stateFilter)}>
                <option value="all">全部状态</option>
                <option value="active">有效</option>
                <option value="revoked">已吊销</option>
              </select>
            </div>
            {visibleKeys.length === 0 ? <EmptyState title="没有匹配的密钥" hint="调整筛选条件后重试" /> : (
              <DataTable columns={['名称', '角色', '状态', '创建时间', '吊销时间', 'ID', '']}>
                {visibleKeys.map((k) => (
              <tr key={k.id} className="group hover:bg-zinc-800/30">
                <td className="px-4 py-3 font-medium text-zinc-100">{k.name}</td>
                <td className="px-4 py-3"><div className="flex flex-wrap gap-1">{k.roles.map((role) => <Badge key={role} tone={role === 'admin' ? 'red' : role === 'writer' ? 'blue' : 'green'}>{role}</Badge>)}</div></td>
                <td className="px-4 py-3">
                  <StateBadge state={k.revokedAt ? 'revoked' : 'active'} />
                </td>
                <td className="whitespace-nowrap px-4 py-3 text-xs text-zinc-500">{formatDate(k.createdAt)}</td>
                <td className="whitespace-nowrap px-4 py-3 text-xs text-zinc-500">{formatDate(k.revokedAt)}</td>
                <td className="px-4 py-3 font-mono text-xs text-zinc-500" title={k.id}>
                  {k.id.slice(0, 8)}…
                </td>
                <td className="px-4 py-3 text-right">
                  {!k.revokedAt && (
                    <button
                      onClick={() => setToRevoke(k)}
                      className="rounded px-2 py-1 text-xs text-zinc-600 opacity-0 hover:bg-rose-500/10 hover:text-rose-400 group-hover:opacity-100"
                    >
                      吊销
                    </button>
                  )}
                </td>
              </tr>
                ))}
              </DataTable>
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
            确定吊销密钥 <span className="font-mono text-zinc-100">{toRevoke?.name}</span> 吗？
            吊销后该 Token 立即失效。
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
