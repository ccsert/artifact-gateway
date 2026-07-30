import { useState } from 'react';
import { useAuth } from '../lib/auth';
import { Modal, useDisclosure } from './Modal';
import { Field, inputClass, btnPrimary, btnSecondary } from './Layout';
import { ErrorBanner } from './Feedback';
import type { Repository } from '../client';

// Uploads a single object to a Raw Hosted repository via the native
// /raw/<repository>/<path> PUT route. The server computes the sha256 digest,
// so no Digest header is required; a provided one would be validated.
export function RawUploadDialog({ repo, onUploaded }: { repo: Repository; onUploaded: () => void }) {
  const { token } = useAuth();
  const dialog = useDisclosure();
  const [file, setFile] = useState<File | null>(null);
  const [path, setPath] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);

  const pickFile = (f: File | null) => {
    setFile(f);
    setPath((current) => current || f?.name || '');
  };

  const submit = async () => {
    if (!file) return;
    const segments = (path.trim() || file.name).replace(/^\/+/, '').split('/').map((s) => encodeURIComponent(s));
    if (segments.length === 0 || segments.some((s) => s === '')) {
      setError(new Error('目标路径不能为空或包含空段'));
      return;
    }
    setBusy(true);
    setError(null);
    try {
      const res = await fetch(`/raw/${encodeURIComponent(repo.name)}/${segments.join('/')}`, {
        method: 'PUT',
        headers: {
          Authorization: `Bearer ${token}`,
          'Content-Type': file.type || 'application/octet-stream',
        },
        body: file,
      });
      if (!res.ok) {
        const text = await res.text().catch(() => '');
        throw new Error(`上传失败 (${res.status})${text ? `：${text.slice(0, 200)}` : ''}`);
      }
      dialog.hide();
      setFile(null);
      setPath('');
      onUploaded();
    } catch (e) {
      setError(e);
    } finally {
      setBusy(false);
    }
  };

  return (
    <>
      <button onClick={dialog.show} className={btnSecondary}>
        上传
      </button>
      <Modal
        open={dialog.open}
        title={`上传到 ${repo.name}`}
        onClose={dialog.hide}
        footer={
          <>
            <button onClick={dialog.hide} className={btnSecondary}>
              取消
            </button>
            <button onClick={submit} disabled={busy || !file} className={btnPrimary}>
              {busy ? '上传中…' : '上传'}
            </button>
          </>
        }
      >
        <div className="space-y-4">
          {error ? <ErrorBanner error={error} /> : null}
          <Field label="文件">
            <input
              type="file"
              className={inputClass}
              onChange={(e) => pickFile(e.target.files?.[0] ?? null)}
            />
          </Field>
          <Field label="目标路径" hint="相对于仓库根；留空则用文件名。不要以 / 开头。">
            <input
              className={`${inputClass} font-mono`}
              placeholder="releases/widget.tar"
              value={path}
              onChange={(e) => setPath(e.target.value)}
            />
          </Field>
          <p className="text-xs text-zinc-500">服务端会校验 sha256；同名路径会被覆盖。</p>
        </div>
      </Modal>
    </>
  );
}
