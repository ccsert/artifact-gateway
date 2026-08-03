import { useState } from 'react';
import { UploadOutlined } from '@ant-design/icons';
import { Button, Input, Space, Upload } from 'antd';
import { useAuth } from '../lib/auth';
import { Modal, useDisclosure } from './Modal';
import { Field } from './Layout';
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
      <Button
        icon={<UploadOutlined />}
        onClick={() => {
          setError(null);
          dialog.show();
        }}
      >
        上传
      </Button>
      <Modal
        open={dialog.open}
        title={`上传到 ${repo.name}`}
        onClose={dialog.hide}
        footer={
          <Space>
            <Button onClick={dialog.hide} disabled={busy}>
              取消
            </Button>
            <Button type="primary" onClick={submit} loading={busy} disabled={!file}>
              上传
            </Button>
          </Space>
        }
      >
        <div className="space-y-4">
          {error ? <ErrorBanner error={error} /> : null}
          <Field label="文件" group>
            <Space>
              <Upload
                maxCount={1}
                showUploadList={false}
                beforeUpload={(selectedFile) => {
                  pickFile(selectedFile);
                  return Upload.LIST_IGNORE;
                }}
              >
                <Button icon={<UploadOutlined />}>选择文件</Button>
              </Upload>
              <span className="max-w-72 truncate text-xs text-zinc-400" title={file?.name}>
                {file?.name ?? '尚未选择文件'}
              </span>
            </Space>
          </Field>
          <Field label="目标路径" hint="相对于仓库根；留空则用文件名。不要以 / 开头。">
            <Input
              className="font-mono"
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
