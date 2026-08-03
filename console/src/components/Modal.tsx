import { Button, Modal as AntdModal, Space } from 'antd';
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
      footer={footer ?? null}
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
        <Space>
          <Button onClick={onClose} disabled={busy}>
            取消
          </Button>
          <Button
            type="primary"
            onClick={onConfirm}
            danger={danger}
            loading={busy}
          >
            {confirmLabel}
          </Button>
        </Space>
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
