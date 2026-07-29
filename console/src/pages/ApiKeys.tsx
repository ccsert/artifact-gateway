import { useCallback, useEffect, useState } from 'react';
import { listApiKeys, createApiKey, revokeApiKey } from '../client';
import type { ApiKey, CreatedApiKey } from '../client';
import { PageHeader, Card, DataTable, Field, inputClass, btnPrimary, btnSecondary } from '../components/Layout';
import { Loading, ErrorBanner, EmptyState } from '../components/Feedback';
import { StateBadge } from '../components/Badge';
import { Modal, ConfirmDialog, useDisclosure } from '../components/Modal';
import { formatDate } from '../lib/format';

function CreateKeyDialog({ onCreated }: { onCreated: (key: CreatedApiKey) => void }) {
  const dialog = useDisclosure();
  const [name, setName] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);

  const submit = async () => {
    setBusy(true);
    setError(null);
    const { data, error: err } = await createApiKey({
      body: { name: name.trim(), roles: ['admin'] },
    });
    setBusy(false);
    if (err) {
      setError(err);
      return;
    }
    if (data) {
      dialog.hide();
      setName('');
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

  return (
    <div>
      <PageHeader
        title="API 密钥"
        description="管理可调管理 API 的访问密钥（admin 角色）"
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
        <Card>
          <DataTable columns={['名称', '角色', '状态', '创建时间', '吊销时间', 'ID', '']}>
            {keys.map((k) => (
              <tr key={k.id} className="group hover:bg-zinc-800/30">
                <td className="px-4 py-3 font-medium text-zinc-100">{k.name}</td>
                <td className="px-4 py-3 font-mono text-xs text-zinc-400">{k.roles.join(', ')}</td>
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
        </Card>
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
