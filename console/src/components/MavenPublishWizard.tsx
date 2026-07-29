import { useState } from 'react';
import { createPublishSession, getPublishSession, commitPublishSession } from '../client';
import type { PublishSession, DeclaredObject } from '../client';
import { useAuth } from '../lib/auth';
import { Card, DataTable, Field, inputClass, btnPrimary, btnSecondary } from './Layout';
import { ErrorBanner } from './Feedback';
import { StateBadge, Badge } from './Badge';
import { formatBytes, formatDate, shortDigest } from '../lib/format';

async function sha256Hex(file: File): Promise<string> {
  const buf = await file.arrayBuffer();
  const hash = await crypto.subtle.digest('SHA-256', buf);
  return Array.from(new Uint8Array(hash))
    .map((b) => b.toString(16).padStart(2, '0'))
    .join('');
}

interface StagedFile {
  name: string;
  digest: string;
  size: number;
  file: File;
  uploaded: boolean;
}

type Step = 'declare' | 'upload' | 'done';

export function MavenPublishWizard({ repositoryId, onPublished }: { repositoryId: string; onPublished?: () => void }) {
  const { token } = useAuth();
  const [step, setStep] = useState<Step>('declare');
  const [coordinate, setCoordinate] = useState('');
  const [pomObject, setPomObject] = useState('');
  const [staged, setStaged] = useState<StagedFile[]>([]);
  const [session, setSession] = useState<PublishSession | null>(null);
  const [error, setError] = useState<unknown>(null);
  const [busy, setBusy] = useState(false);
  const [uploading, setUploading] = useState(false);

  // 选择文件后计算 digest
  const addFiles = async (files: FileList | null) => {
    if (!files) return;
    const next: StagedFile[] = [];
    for (const file of Array.from(files)) {
      const digest = `sha256:${await sha256Hex(file)}`;
      next.push({ name: file.name, digest, size: file.size, file, uploaded: false });
    }
    setStaged((prev) => {
      const merged = [...prev];
      for (const f of next) {
        const idx = merged.findIndex((x) => x.name === f.name);
        if (idx >= 0) merged[idx] = f;
        else merged.push(f);
      }
      return merged;
    });
    // 自动推断 pomObject 和 coordinate
    const pom = next.find((f) => f.name.endsWith('.pom'));
    if (pom && !pomObject) setPomObject(pom.name);
  };

  const createSession = async () => {
    setBusy(true);
    setError(null);
    const objects: DeclaredObject[] = staged.map((f) => ({ name: f.name, digest: f.digest, size: f.size }));
    const { data, error: err } = await createPublishSession({
      path: { repositoryId },
      body: { format: 'maven', coordinate: coordinate.trim(), pomObject, objects },
      headers: { 'Idempotency-Key': crypto.randomUUID() },
    });
    setBusy(false);
    if (err) {
      setError(err);
      return;
    }
    if (data) {
      setSession(data);
      setStep('upload');
    }
  };

  const uploadAll = async () => {
    if (!session) return;
    setUploading(true);
    setError(null);
    for (const f of staged) {
      if (f.uploaded) continue;
      try {
        const res = await fetch(`/api/v2/publish-sessions/${session.id}/objects/${encodeURIComponent(f.name)}`, {
          method: 'PUT',
          headers: { Authorization: `Bearer ${token}`, 'Content-Type': 'application/octet-stream' },
          body: f.file,
        });
        if (!res.ok) throw new Error(`${f.name}: ${res.status} ${(await res.text()).slice(0, 100)}`);
        setStaged((prev) => prev.map((x) => (x.name === f.name ? { ...x, uploaded: true } : x)));
      } catch (e) {
        setError(e);
        setUploading(false);
        return;
      }
    }
    setUploading(false);
    // 刷新会话状态
    const { data } = await getPublishSession({ path: { sessionId: session.id } });
    if (data) setSession(data);
  };

  const commit = async () => {
    if (!session) return;
    setBusy(true);
    setError(null);
    const { error: err } = await commitPublishSession({ path: { sessionId: session.id } });
    setBusy(false);
    if (err) {
      setError(err);
      return;
    }
    setStep('done');
    onPublished?.();
  };

  const reset = () => {
    setStep('declare');
    setCoordinate('');
    setPomObject('');
    setStaged([]);
    setSession(null);
    setError(null);
  };

  const allUploaded = staged.length > 0 && staged.every((f) => f.uploaded);

  return (
    <div className="space-y-5">
      {error !== null && <ErrorBanner error={error} />}

      {/* 步骤指示 */}
      <div className="flex items-center gap-2 text-xs">
        {(['declare', 'upload', 'done'] as const).map((s, i) => {
          const labels = { declare: '1 声明坐标与对象', upload: '2 上传文件', done: '3 完成' };
          const active = step === s;
          const passed = (['declare', 'upload', 'done'] as const).indexOf(step) > i;
          return (
            <span key={s} className={`flex items-center gap-2 ${i > 0 ? 'before:content-["→"] before:mr-2 before:text-zinc-700' : ''}`}>
              <span className={active ? 'font-medium text-cyan-300' : passed ? 'text-emerald-400' : 'text-zinc-600'}>
                {labels[s]}
              </span>
            </span>
          );
        })}
      </div>

      {step === 'declare' && (
        <div className="space-y-4">
          <div className="grid grid-cols-1 gap-4 md:grid-cols-2">
            <Field label="Maven 坐标" hint="group:artifact:version，如 com.acme:widget:1.0.0">
              <input
                className={`${inputClass} font-mono text-xs`}
                placeholder="com.example:my-lib:1.0.0"
                value={coordinate}
                onChange={(e) => setCoordinate(e.target.value)}
              />
            </Field>
            <Field label="POM 对象名" hint="staged 文件中作为 POM 的那个">
              <input
                className={`${inputClass} font-mono text-xs`}
                placeholder="my-lib-1.0.0.pom"
                value={pomObject}
                onChange={(e) => setPomObject(e.target.value)}
              />
            </Field>
          </div>
          <Field label="发布文件" hint="选择 POM、JAR、源码包等；浏览器会自动计算 sha256">
            <input
              type="file"
              multiple
              onChange={(e) => addFiles(e.target.files)}
              className="block w-full text-sm text-zinc-400 file:mr-3 file:rounded-md file:border-0 file:bg-zinc-800 file:px-3 file:py-2 file:text-sm file:text-zinc-200 hover:file:bg-zinc-700"
            />
          </Field>
          {staged.length > 0 && (
            <Card>
              <DataTable columns={['文件名', 'sha256', '大小', '']}>
                {staged.map((f) => (
                  <tr key={f.name}>
                    <td className="px-4 py-2 font-mono text-xs text-zinc-200">{f.name}</td>
                    <td className="px-4 py-2 font-mono text-xs text-zinc-500" title={f.digest}>
                      {shortDigest(f.digest)}
                    </td>
                    <td className="px-4 py-2 text-xs text-zinc-400">{formatBytes(f.size)}</td>
                    <td className="px-4 py-2 text-right">
                      <button
                        onClick={() => setStaged((prev) => prev.filter((x) => x.name !== f.name))}
                        className="text-xs text-zinc-600 hover:text-rose-400"
                      >
                        移除
                      </button>
                    </td>
                  </tr>
                ))}
              </DataTable>
            </Card>
          )}
          <button
            onClick={createSession}
            disabled={busy || !coordinate.trim() || !pomObject || staged.length === 0}
            className={btnPrimary}
          >
            {busy ? '创建会话…' : '创建发布会话'}
          </button>
        </div>
      )}

      {step === 'upload' && session && (
        <div className="space-y-4">
          <div className="flex flex-wrap items-center gap-3 rounded-lg border border-zinc-800 px-4 py-3 text-xs text-zinc-400">
            <span>
              会话 <code className="font-mono text-zinc-300">{session.id.slice(0, 8)}…</code>
            </span>
            <StateBadge state={session.state} />
            <span>过期时间：{formatDate(session.expiresAt)}</span>
          </div>
          <Card>
            <DataTable columns={['文件名', '大小', '状态']}>
              {staged.map((f) => (
                <tr key={f.name}>
                  <td className="px-4 py-2 font-mono text-xs text-zinc-200">{f.name}</td>
                  <td className="px-4 py-2 text-xs text-zinc-400">{formatBytes(f.size)}</td>
                  <td className="px-4 py-2">
                    {f.uploaded ? <Badge tone="green">已上传</Badge> : <Badge tone="zinc">待上传</Badge>}
                  </td>
                </tr>
              ))}
            </DataTable>
          </Card>
          <div className="flex gap-2">
            <button onClick={uploadAll} disabled={uploading || allUploaded} className={btnSecondary}>
              {uploading ? '上传中…' : allUploaded ? '全部已上传' : '上传全部文件'}
            </button>
            <button onClick={commit} disabled={busy || !allUploaded} className={btnPrimary}>
              {busy ? '提交中…' : '提交发布'}
            </button>
            <button onClick={reset} className="ml-auto text-xs text-zinc-600 hover:text-zinc-400">
              放弃并重新开始
            </button>
          </div>
        </div>
      )}

      {step === 'done' && (
        <div className="rounded-lg border border-emerald-500/30 bg-emerald-500/10 px-4 py-6 text-center">
          <p className="text-sm font-medium text-emerald-300">发布成功</p>
          <p className="mt-1 font-mono text-xs text-emerald-400/70">{coordinate}</p>
          <button onClick={reset} className={`${btnPrimary} mt-4`}>
            再发布一个
          </button>
        </div>
      )}
    </div>
  );
}
