import { useCallback, useEffect, useState } from 'react';
import { useAuth } from '../lib/auth';
import {
  V1_FORMATS,
  createV1Group,
  disableV1Group,
  listKnownGroups,
  rememberGroupName,
} from '../lib/v1proxy';
import type { ProxyFormat, V1Group, V1Member } from '../lib/v1proxy';
import { PageHeader, Card, Field, inputClass, btnPrimary, btnSecondary } from '../components/Layout';
import { Loading, ErrorBanner, EmptyState } from '../components/Feedback';
import { FormatBadge, StateBadge } from '../components/Badge';
import { Modal, ConfirmDialog, useDisclosure } from '../components/Modal';
import { ProxyGroupDetail } from '../components/ProxyGroupDetail';

function CreateProxyGroupDialog({ onCreated }: { onCreated: () => void }) {
  const { token } = useAuth();
  const dialog = useDisclosure();
  const [format, setFormat] = useState<ProxyFormat>('maven');
  const [name, setName] = useState('');
  const [endpoint, setEndpoint] = useState('');
  const [allowedHosts, setAllowedHosts] = useState('');
  const [quotaGiB, setQuotaGiB] = useState(1);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);

  const needsHosts = format === 'raw' || format === 'conan';

  const submit = async () => {
    setBusy(true);
    setError(null);
    const hosts = allowedHosts
      .split(',')
      .map((h) => h.trim())
      .filter(Boolean);
    const member: V1Member = {
      name: 'upstream',
      type: 'proxy',
      endpoint: endpoint.trim(),
      position: 0,
      anonymous: false,
      ...(hosts.length > 0 ? { allowedHosts: hosts } : {}),
    };
    try {
      const g = await createV1Group(token, format, {
        name: name.trim(),
        enabled: true,
        cacheQuotaBytes: quotaGiB * 2 ** 30,
        members: [member],
      });
      rememberGroupName(format, g.name);
      dialog.hide();
      setName('');
      setEndpoint('');
      setAllowedHosts('');
      onCreated();
    } catch (e) {
      setError(e);
    } finally {
      setBusy(false);
    }
  };

  const endpointPlaceholder: Record<ProxyFormat, string> = {
    oci: 'https://registry-1.docker.io',
    maven: 'https://repo1.maven.org/maven2',
    raw: 'https://raw.githubusercontent.com',
    conan: 'https://conan.example.com',
  };

  return (
    <>
      <button onClick={dialog.show} className={btnPrimary}>
        + 新建代理组
      </button>
      <Modal
        open={dialog.open}
        title="新建代理组（v1）"
        onClose={dialog.hide}
        wide
        footer={
          <>
            <button onClick={dialog.hide} className={btnSecondary}>
              取消
            </button>
            <button
              onClick={submit}
              disabled={busy || !name.trim() || !endpoint.trim() || (needsHosts && !allowedHosts.trim())}
              className={btnPrimary}
            >
              {busy ? '创建中…' : '创建'}
            </button>
          </>
        }
      >
        <div className="space-y-4">
          {error !== null && <ErrorBanner error={error} />}
          <div className="grid grid-cols-2 gap-4">
            <Field label="格式">
              <div className="grid grid-cols-4 gap-1.5">
                {V1_FORMATS.map((f) => (
                  <button
                    key={f}
                    type="button"
                    onClick={() => setFormat(f)}
                    className={`rounded-md border px-2 py-1.5 font-mono text-xs ${
                      format === f
                        ? 'border-cyan-500/60 bg-cyan-500/10 text-cyan-300'
                        : 'border-zinc-700 text-zinc-400 hover:bg-zinc-800'
                    }`}
                  >
                    {f}
                  </button>
                ))}
              </div>
            </Field>
            <Field label="组名称" hint="客户端通过 /{format}/{name}/... 访问">
              <input
                className={`${inputClass} font-mono text-xs`}
                placeholder="maven-central"
                value={name}
                onChange={(e) => setName(e.target.value)}
              />
            </Field>
          </div>
          <Field label="上游地址 endpoint" hint="代理拉取的外部仓库地址">
            <input
              className={`${inputClass} font-mono text-xs`}
              placeholder={endpointPlaceholder[format]}
              value={endpoint}
              onChange={(e) => setEndpoint(e.target.value)}
            />
          </Field>
          <div className="grid grid-cols-2 gap-4">
            <Field
              label={`允许主机 allowedHosts${needsHosts ? '（必填）' : '（可选）'}`}
              hint="逗号分隔；raw/conan 强制，oci/maven 还需 env 级 allowlist"
            >
              <input
                className={`${inputClass} font-mono text-xs`}
                placeholder="repo1.maven.org"
                value={allowedHosts}
                onChange={(e) => setAllowedHosts(e.target.value)}
              />
            </Field>
            <Field label="缓存配额 (GiB)">
              <input
                type="number"
                min={0}
                className={inputClass}
                value={quotaGiB}
                onChange={(e) => setQuotaGiB(Number(e.target.value))}
              />
            </Field>
          </div>
        </div>
      </Modal>
    </>
  );
}

export function ProxyGroupsPage() {
  const { token } = useAuth();
  const [items, setItems] = useState<{ format: ProxyFormat; group: V1Group }[] | null>(null);
  const [error, setError] = useState<unknown>(null);
  const [selected, setSelected] = useState<{ format: ProxyFormat; group: V1Group } | null>(null);
  const [toDisable, setToDisable] = useState<{ format: ProxyFormat; group: V1Group } | null>(null);
  const [disabling, setDisabling] = useState(false);

  const load = useCallback(async () => {
    setError(null);
    try {
      const list = await listKnownGroups(token);
      setItems(list);
      // 保持选中项数据最新
      setSelected((prev) => {
        if (!prev) return list[0] ?? null;
        return list.find((x) => x.format === prev.format && x.group.name === prev.group.name) ?? list[0] ?? null;
      });
    } catch (e) {
      setError(e);
    }
  }, [token]);

  useEffect(() => {
    void load();
  }, [load]);

  const confirmDisable = async () => {
    if (!toDisable) return;
    setDisabling(true);
    try {
      await disableV1Group(token, toDisable.format, toDisable.group.name);
      setToDisable(null);
      void load();
    } catch (e) {
      setError(e);
    } finally {
      setDisabling(false);
    }
  };

  return (
    <div>
      <PageHeader
        title="代理组"
        description="v1 代理仓库：从配置的上游拉取并缓存（hosted 优先、proxy 兜底）"
        actions={<CreateProxyGroupDialog onCreated={load} />}
      />
      {error !== null ? (
        <ErrorBanner error={error} onRetry={load} />
      ) : !items ? (
        <Loading />
      ) : items.length === 0 ? (
        <Card>
          <EmptyState
            title="暂无代理组"
            hint="创建代理组以从上游仓库（如 Maven Central、Docker Hub）缓存拉取制品"
          />
        </Card>
      ) : (
        <div className="grid grid-cols-1 gap-4 lg:grid-cols-[300px_1fr]">
          {/* 左：组列表 */}
          <div className="space-y-2">
            {items.map(({ format, group }) => {
              const active = selected?.format === format && selected?.group.name === group.name;
              return (
                <button
                  key={`${format}-${group.name}`}
                  onClick={() => setSelected({ format, group })}
                  className={`w-full rounded-xl border px-4 py-3 text-left transition-colors ${
                    active
                      ? 'border-cyan-500/50 bg-cyan-500/5'
                      : 'border-zinc-800 bg-zinc-900/60 hover:border-zinc-700'
                  }`}
                >
                  <div className="flex items-center justify-between gap-2">
                    <span className={`font-medium ${active ? 'text-cyan-200' : 'text-zinc-100'}`}>{group.name}</span>
                    <FormatBadge format={format} />
                  </div>
                  <div className="mt-1.5 flex items-center gap-2 text-xs">
                    <StateBadge state={group.enabled ? 'enabled' : 'disabled'} />
                    <span className="text-zinc-600">
                      {group.members.filter((m) => m.type === 'proxy').length} 个代理上游
                    </span>
                  </div>
                </button>
              );
            })}
          </div>
          {/* 右：详情 */}
          <Card className="p-5">
            {selected ? (
              <>
                <div className="mb-4 flex items-center justify-between">
                  <h2 className="text-base font-semibold text-zinc-100">{selected.group.name}</h2>
                  {selected.group.enabled && (
                    <button
                      onClick={() => setToDisable(selected)}
                      className="rounded border border-amber-500/40 px-2.5 py-1 text-xs text-amber-300 hover:bg-amber-500/10"
                    >
                      禁用
                    </button>
                  )}
                </div>
                <ProxyGroupDetail format={selected.format} group={selected.group} />
              </>
            ) : (
              <EmptyState title="选择一个代理组查看详情" />
            )}
          </Card>
        </div>
      )}
      <ConfirmDialog
        open={!!toDisable}
        title="禁用代理组"
        message={
          <>
            确定禁用代理组 <span className="font-mono text-zinc-100">{toDisable?.group.name}</span> 吗？
            禁用后客户端将无法通过该组解析制品。
          </>
        }
        confirmLabel="禁用"
        danger
        busy={disabling}
        onConfirm={confirmDisable}
        onClose={() => setToDisable(null)}
      />
    </div>
  );
}
