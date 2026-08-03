import { Modal as AntdModal } from 'antd';
import { useState } from 'react';
import type { ReactNode } from 'react';

export function Modal({
  open,
  title,
  onClose,
  children,
  footer,
  wide,
}: {
  open: boolean;
  title: string;
  onClose: () => void;
  children: ReactNode;
  footer?: ReactNode;
  wide?: boolean;
}) {
  return (
    <AntdModal
      open={open}
      title={title}
      onCancel={onClose}
      footer={footer}
      centered
      destroyOnHidden
      width={wide ? 1152 : 520}
      styles={{
        body: {
          maxHeight: 'calc(85vh - 112px)',
          overflowY: 'auto',
        },
      }}
    >
      {children}
    </AntdModal>
  );
}

export function ConfirmDialog({
  open,
  title,
  message,
  confirmLabel = '确认',
  danger,
  busy,
  onConfirm,
  onClose,
}: {
  open: boolean;
  title: string;
  message: ReactNode;
  confirmLabel?: string;
  danger?: boolean;
  busy?: boolean;
  onConfirm: () => void;
  onClose: () => void;
}) {
  return (
    <Modal
      open={open}
      title={title}
      onClose={onClose}
      footer={
        <>
          <button
            onClick={onClose}
            className="rounded-md border border-zinc-700 px-3 py-1.5 text-sm text-zinc-300 hover:bg-zinc-800"
          >
            取消
          </button>
          <button
            onClick={onConfirm}
            disabled={busy}
            className={`rounded-md px-3 py-1.5 text-sm font-medium text-white disabled:opacity-50 ${
              danger ? 'bg-rose-600 hover:bg-rose-500' : 'bg-cyan-600 hover:bg-cyan-500'
            }`}
          >
            {busy ? '处理中…' : confirmLabel}
          </button>
        </>
      }
    >
      <div className="text-sm text-zinc-300">{message}</div>
    </Modal>
  );
}

export function useDisclosure() {
  const [open, setOpen] = useState(false);
  return {
    open,
    show: () => setOpen(true),
    hide: () => setOpen(false),
    toggle: () => setOpen((v) => !v),
  };
}
